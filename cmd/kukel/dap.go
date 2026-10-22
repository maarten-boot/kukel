package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/google/go-dap"

	"github.com/maarten-boot/kukel/internal/builtins"
	"github.com/maarten-boot/kukel/internal/bytecode"
	"github.com/maarten-boot/kukel/internal/debugger"
	"github.com/maarten-boot/kukel/internal/value"
	"github.com/maarten-boot/kukel/internal/vm"
)

// kukel dap (TODO.md Q77): the Debug Adapter Protocol on standard input and
// output, the front end of package debugger for editors. A goroutine reads
// the editor's requests; the program runs on the main goroutine (as with
// kukel debug: tk needs it), after configurationDone. Where it stops,
// Stopped runs what needs the program's state (stackTrace, variables,
// evaluate, ...) on that goroutine, sent by the reader, until a request
// lets it go on. One thread, "main": workers run without the debugger.

// launchArgs are the attributes of a launch configuration.
type launchArgs struct {
	Program     string            `json:"program"`     // the .kkl file with main (more files: Files)
	Files       []string          `json:"files"`       // more source files of the program
	Args        []string          `json:"args"`        // the program's arguments
	Cwd         string            `json:"cwd"`         // its working directory
	Stdin       string            `json:"stdin"`       // a file for its standard input (none: empty)
	Env         map[string]string `json:"env"`         // more environment variables (KUKEL_PATH, ...)
	StopOnEntry bool              `json:"stopOnEntry"` // stop at the first line that runs
	NoDebug     bool              `json:"noDebug"`     // run without stopping
}

// dapThread is the one thread the adapter shows.
const dapThread = 1

type dapServer struct {
	in     *bufio.Reader
	wmu    sync.Mutex // writing messages: from both goroutines
	out    io.Writer
	seq    int
	logErr io.Writer // the adapter's own problems

	sess    *debugger.Session
	machine *vm.VM
	cancel  *vm.Canceller
	args    launchArgs
	stdout  *bufio.Writer // the program's standard output, as output events
	stdin   io.Closer     // the file of its standard input, or nil

	launched chan struct{} // closed by launch (when it compiled)
	started  chan struct{} // closed by configurationDone
	done     chan struct{} // closed when the reader ends (disconnect, end of input)
	once     sync.Once
	ending   atomic.Bool // terminate or disconnect: the program is to end

	// While the program is stopped (stopped is set on its goroutine, read by
	// the reader under mu): the requests that need its state come as
	// functions on calls, and a request that lets it go on on resume.
	mu      sync.Mutex
	stopped bool
	calls   chan func()
	resume  chan func() error // returns the error that ends the program, or nil
	refs    []dapRef          // variablesReference n is refs[n-1]; for one stop
	thrown  *vm.RuntimeError
}

// dapRef is what a variablesReference stands for.
type dapRef struct {
	scope string      // "locals", "globals", or "" for a value
	frame int         // the frame of locals, and where values' names are evaluated
	v     value.Value // a vector, map or object
	name  string      // an expression for v, for evaluateName ("" if none)
}

func cmdDap(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintln(stderr, "usage: kukel dap (an editor starts it: see docs/debugging.md)")
		return 2
	}
	s := &dapServer{in: bufio.NewReader(stdin), out: stdout, logErr: stderr,
		launched: make(chan struct{}), started: make(chan struct{}), done: make(chan struct{}),
		calls: make(chan func()), resume: make(chan func() error)}
	go s.read()
	select {
	case <-s.started:
	case <-s.done:
		return 0
	}
	code := s.runProgram()
	<-s.done // the editor disconnects after the program ended
	return code
}

// runProgram runs the launched program and tells the editor how it ended.
func (s *dapServer) runProgram() int {
	code, err := s.machine.Run(s.args.Args)
	s.stdout.Flush()
	if s.stdin != nil {
		s.stdin.Close()
	}
	if err != nil && !s.ending.Load() {
		s.output("stderr", err.Error()+"\n")
	}
	s.send(&dap.ExitedEvent{Event: s.event("exited"), Body: dap.ExitedEventBody{ExitCode: code}})
	s.send(&dap.TerminatedEvent{Event: s.event("terminated")})
	return code
}

// errDisconnected ends a program the editor disconnected from.
var errDisconnected = errors.New("the debugger disconnected")

// read handles the editor's requests until it disconnects.
func (s *dapServer) read() {
	defer s.once.Do(func() { close(s.done) })
	for {
		msg, err := dap.ReadProtocolMessage(s.in)
		if err != nil {
			if err != io.EOF {
				fmt.Fprintln(s.logErr, "kukel dap:", err)
			}
			s.end()
			return
		}
		req, ok := msg.(dap.RequestMessage)
		if !ok {
			continue // responses and events from the editor: none expected
		}
		if !s.handle(req) {
			return
		}
	}
}

// end ends the program, if it runs: it is cancelled (try cannot catch
// that), and a stopped program goes on with the cancellation's error.
func (s *dapServer) end() {
	s.ending.Store(true)
	if s.cancel != nil {
		s.cancel.Cancel(errDisconnected)
	}
	if s.going() {
		s.resume <- s.endError
	}
}

// endError is the error that ends the program after end: its
// cancellation (fatal, as exit).
func (s *dapServer) endError() error {
	if err := s.machine.Err(); err != nil {
		return err
	}
	return &value.ExitError{Code: 1}
}

// handle answers a request; false ends the session.
func (s *dapServer) handle(req dap.RequestMessage) bool {
	switch r := req.(type) {
	case *dap.InitializeRequest:
		s.send(&dap.InitializeResponse{Response: s.response(r.Request), Body: dap.Capabilities{
			SupportsConfigurationDoneRequest:  true,
			SupportsFunctionBreakpoints:       true,
			SupportsConditionalBreakpoints:    true,
			SupportsHitConditionalBreakpoints: true,
			SupportsLogPoints:                 true,
			SupportsEvaluateForHovers:         true,
			SupportsSetVariable:               true,
			SupportsExceptionInfoRequest:      true,
			SupportsTerminateRequest:          true,
			SupportTerminateDebuggee:          true,
			ExceptionBreakpointFilters: []dap.ExceptionBreakpointsFilter{{
				Filter: "uncaught", Label: "Uncaught exceptions", Default: true,
				Description: "Stop where an exception is thrown that nothing catches"}},
		}})
	case *dap.LaunchRequest:
		if err := s.launch(r); err != nil {
			s.fail(r.Request, err)
			return true
		}
		s.send(&dap.LaunchResponse{Response: s.response(r.Request)})
		s.send(&dap.InitializedEvent{Event: s.event("initialized")})
	case *dap.AttachRequest:
		s.fail(r.Request, errors.New("kukel dap launches programs; it cannot attach to one"))
	case *dap.SetBreakpointsRequest:
		s.setBreakpoints(r)
	case *dap.SetFunctionBreakpointsRequest:
		s.setFunctionBreakpoints(r)
	case *dap.SetExceptionBreakpointsRequest:
		if s.sess != nil {
			s.sess.SetUncaught(slices.Contains(r.Arguments.Filters, "uncaught"))
		}
		s.send(&dap.SetExceptionBreakpointsResponse{Response: s.response(r.Request)})
	case *dap.ConfigurationDoneRequest:
		s.send(&dap.ConfigurationDoneResponse{Response: s.response(r.Request)})
		if s.isLaunched() && !s.isStarted() && !s.ending.Load() {
			close(s.started)
		}
	case *dap.ThreadsRequest:
		s.send(&dap.ThreadsResponse{Response: s.response(r.Request),
			Body: dap.ThreadsResponseBody{Threads: []dap.Thread{{Id: dapThread, Name: "main"}}}})
	case *dap.StackTraceRequest:
		s.whileStopped(r.Request, func(m *vm.VM) dap.ResponseMessage { return s.stackTrace(m, r) })
	case *dap.ScopesRequest:
		s.whileStopped(r.Request, func(m *vm.VM) dap.ResponseMessage { return s.scopes(m, r) })
	case *dap.VariablesRequest:
		s.whileStopped(r.Request, func(m *vm.VM) dap.ResponseMessage { return s.variables(m, r) })
	case *dap.EvaluateRequest:
		s.whileStopped(r.Request, func(m *vm.VM) dap.ResponseMessage { return s.evaluate(m, r) })
	case *dap.SetVariableRequest:
		s.whileStopped(r.Request, func(m *vm.VM) dap.ResponseMessage { return s.setVariable(m, r) })
	case *dap.ExceptionInfoRequest:
		s.whileStopped(r.Request, func(m *vm.VM) dap.ResponseMessage { return s.exceptionInfo(r) })
	case *dap.ContinueRequest:
		s.goOn(r.Request, func(int) { s.sess.Continue() },
			&dap.ContinueResponse{Body: dap.ContinueResponseBody{AllThreadsContinued: true}})
	case *dap.NextRequest:
		s.goOn(r.Request, s.sess.Next, &dap.NextResponse{})
	case *dap.StepInRequest:
		s.goOn(r.Request, func(int) { s.sess.Step() }, &dap.StepInResponse{})
	case *dap.StepOutRequest:
		s.goOn(r.Request, s.sess.Finish, &dap.StepOutResponse{})
	case *dap.PauseRequest:
		switch {
		case s.sess == nil:
			s.fail(r.Request, errors.New("no program is launched"))
		case s.args.NoDebug:
			s.fail(r.Request, errors.New("the program runs without debugging: it cannot pause"))
		default:
			s.sess.Pause()
			s.send(&dap.PauseResponse{Response: s.response(r.Request)})
		}
	case *dap.TerminateRequest:
		s.send(&dap.TerminateResponse{Response: s.response(r.Request)})
		s.end()
		if !s.isStarted() { // else runProgram says it, when the program ended
			s.send(&dap.TerminatedEvent{Event: s.event("terminated")})
		}
	case *dap.DisconnectRequest:
		s.end()
		s.send(&dap.DisconnectResponse{Response: s.response(r.Request)})
		return false
	default:
		s.fail(*req.GetRequest(), fmt.Errorf("kukel dap does not support %q", req.GetRequest().Command))
	}
	return true
}

// isLaunched and isStarted tell whether launch and configurationDone
// were.
func (s *dapServer) isLaunched() bool { return closed(s.launched) }
func (s *dapServer) isStarted() bool  { return closed(s.started) }

func closed(c chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}

// launch compiles the program in its working directory (program, files
// and stdin are relative to it), with its input, environment and output,
// for configurationDone to start. If it fails, it changes nothing.
func (s *dapServer) launch(r *dap.LaunchRequest) (err error) {
	if s.sess != nil {
		return errors.New("a program is launched already")
	}
	var a launchArgs
	if err := json.Unmarshal(r.Arguments, &a); err != nil {
		return fmt.Errorf("the launch configuration: %v", err)
	}
	if a.Program == "" {
		return errors.New(`the launch configuration names no "program" (the .kkl file with main)`)
	}
	var undo []func() // what to take back if it fails
	defer func() {
		if err != nil {
			for i := len(undo) - 1; i >= 0; i-- {
				undo[i]()
			}
		}
	}()
	if a.Cwd != "" {
		old, err := os.Getwd()
		if err != nil {
			return err
		}
		if err := os.Chdir(a.Cwd); err != nil {
			return err
		}
		undo = append(undo, func() { os.Chdir(old) })
	}
	for k, v := range a.Env {
		old, had := os.LookupEnv(k)
		os.Setenv(k, v)
		undo = append(undo, func() {
			if had {
				os.Setenv(k, old)
			} else {
				os.Unsetenv(k)
			}
		})
	}
	var input io.Reader = strings.NewReader("")
	var file *os.File
	if a.Stdin != "" {
		if file, err = os.Open(a.Stdin); err != nil {
			return fmt.Errorf("stdin: %v", err)
		}
		undo = append(undo, func() { file.Close() })
		input = file // read until the program ends
	}
	prog, err := compileFiles(append([]string{a.Program}, a.Files...))
	if err != nil {
		return err
	}
	prog = bytecode.Debuggable(prog)
	stdout := bufio.NewWriter(dapWriter{s, "stdout"})
	machine, err := vm.NewWith(prog, builtins.Lookup, cliConfig(), stdout, bufio.NewReader(input))
	if err != nil {
		return err
	}
	machine.SetStderr(dapWriter{s, "stderr"})
	s.args, s.machine, s.stdout = a, machine, stdout
	if file != nil {
		s.stdin = file
	}
	s.cancel = machine.Cancellable()
	s.sess = debugger.New(prog, s, a.StopOnEntry && !a.NoDebug)
	if !a.NoDebug {
		s.sess.Attach(machine)
	}
	close(s.launched)
	return nil
}

// Stopped tells the editor where the program stopped, then runs its
// requests on this goroutine until one lets the program go on. After
// terminate or disconnect, it ends the program.
func (s *dapServer) Stopped(m *vm.VM, st debugger.Stop) error {
	m.FlushOutput()
	if s.ending.Load() {
		return s.endError()
	}
	s.mu.Lock()
	s.stopped, s.refs, s.thrown = true, nil, st.Thrown
	s.mu.Unlock()
	body := dap.StoppedEventBody{Reason: st.Reason, ThreadId: dapThread, AllThreadsStopped: true, HitBreakpointIds: st.Breaks}
	if st.Thrown != nil {
		body.Text = st.Thrown.Kind
		body.Description = "Uncaught " + st.Thrown.Kind
		if st.Thrown.Msg != "" {
			body.Text += ": " + st.Thrown.Msg
		}
	}
	s.send(&dap.StoppedEvent{Event: s.event("stopped"), Body: body})
	for {
		select {
		case f := <-s.calls:
			f()
		case f := <-s.resume: // going() marked the program running
			s.refs, s.thrown = nil, nil
			return f()
		case <-s.done: // no more requests (end has cancelled the program)
			return s.endError()
		}
	}
}

// Log shows a logpoint's message, or why a condition failed, in the debug
// console.
func (s *dapServer) Log(msg string) {
	s.machine.FlushOutput()
	s.output("console", msg+"\n")
}

// going reports whether the program is stopped, and marks it running if it
// is: before the request that lets it go on is sent to it on resume. So no
// request after it takes the program for stopped (it would wait for an
// answer from a program that went on). Only the reader calls it.
func (s *dapServer) going() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	stopped := s.stopped
	s.stopped = false
	return stopped
}

// whileStopped answers a request with what f gives on the program's
// goroutine, if it is stopped.
func (s *dapServer) whileStopped(req dap.Request, f func(m *vm.VM) dap.ResponseMessage) {
	s.mu.Lock()
	stopped := s.stopped
	s.mu.Unlock()
	if !stopped {
		s.fail(req, errors.New("the program is running"))
		return
	}
	reply := make(chan dap.ResponseMessage)
	s.calls <- func() { reply <- f(s.machine) }
	resp := <-reply
	*resp.GetResponse() = s.responseOf(req, resp.GetResponse())
	s.send(resp)
}

// goOn lets a stopped program go on as how says (given the frame depth).
func (s *dapServer) goOn(req dap.Request, how func(depth int), resp dap.ResponseMessage) {
	if !s.going() {
		s.fail(req, errors.New("the program is running"))
		return
	}
	*resp.GetResponse() = s.response(req)
	s.send(resp)
	s.resume <- func() error {
		how(len(s.machine.Frames()))
		return nil
	}
}

func (s *dapServer) stackTrace(m *vm.VM, r *dap.StackTraceRequest) dap.ResponseMessage {
	frames := m.Frames()
	var out []dap.StackFrame
	for i := len(frames) - 1; i >= 0; i-- {
		f := frames[i]
		out = append(out, dap.StackFrame{Id: i + 1, Name: f.Func.FuncName, Line: f.Line, Column: 1, Source: source(f.Func.File)})
	}
	if start := r.Arguments.StartFrame; start > 0 {
		out = out[min(start, len(out)):]
	}
	if n := r.Arguments.Levels; n > 0 && n < len(out) {
		out = out[:n]
	}
	return &dap.StackTraceResponse{Body: dap.StackTraceResponseBody{StackFrames: out, TotalFrames: len(frames)}}
}

// source names a file for the editor (by its absolute path).
func source(file string) *dap.Source {
	path, err := filepath.Abs(file)
	if err != nil {
		path = file
	}
	return &dap.Source{Name: filepath.Base(file), Path: path}
}

// frameIndex gives the index into m.Frames() of a frameId (stackTrace's: one
// more than the index, so that 0, as when there is none, is the top frame).
func frameIndex(m *vm.VM, id int) (int, error) {
	n := len(m.Frames())
	switch {
	case id == 0:
		return n - 1, nil
	case id < 0 || id > n:
		return 0, fmt.Errorf("no frame %d", id)
	}
	return id - 1, nil
}

func (s *dapServer) scopes(m *vm.VM, r *dap.ScopesRequest) dap.ResponseMessage {
	frame, err := frameIndex(m, r.Arguments.FrameId)
	if err != nil {
		return failure(err)
	}
	locals := s.ref(dapRef{scope: "locals", frame: frame})
	globals := s.ref(dapRef{scope: "globals", frame: frame})
	return &dap.ScopesResponse{Body: dap.ScopesResponseBody{Scopes: []dap.Scope{
		{Name: "Locals", PresentationHint: "locals", VariablesReference: locals},
		{Name: "Globals", VariablesReference: globals, Expensive: true},
	}}}
}

// ref gives a variablesReference for r (valid until the program goes on).
func (s *dapServer) ref(r dapRef) int {
	s.refs = append(s.refs, r)
	return len(s.refs)
}

func (s *dapServer) variables(m *vm.VM, r *dap.VariablesRequest) dap.ResponseMessage {
	n := r.Arguments.VariablesReference
	if n < 1 || n > len(s.refs) {
		return &dap.VariablesResponse{Body: dap.VariablesResponseBody{Variables: []dap.Variable{}}}
	}
	ref := s.refs[n-1]
	vars := []dap.Variable{}
	switch ref.scope {
	case "locals":
		if this := m.Frames()[ref.frame].This; this != nil {
			vars = append(vars, s.variable(ref.frame, "this", "this", value.ObjectValue(this)))
		}
		for _, v := range m.Vars(ref.frame) {
			vars = append(vars, s.variable(ref.frame, v.Name, v.Name, v.Value))
		}
	case "globals":
		for _, name := range m.Globals() {
			v, _ := m.Global(name)
			vars = append(vars, s.variable(ref.frame, name, name, v))
		}
	default:
		vars = s.children(ref, r.Arguments.Filter, r.Arguments.Start, r.Arguments.Count)
	}
	return &dap.VariablesResponse{Body: dap.VariablesResponseBody{Variables: vars}}
}

// children gives the elements of a vector or the entries of a map (they
// are "indexed": count of them from start, if count > 0, so that an editor
// can ask for a large one in parts), or the data members of an object.
func (s *dapServer) children(ref dapRef, filter string, start, count int) []dap.Variable {
	out := []dap.Variable{}
	child := func(name, expr string, v value.Value) {
		if ref.name == "" {
			expr = ""
		}
		out = append(out, s.variable(ref.frame, name, expr, v))
	}
	v := ref.v
	if filter == "named" && v.Kind() != value.Object || filter == "indexed" && v.Kind() == value.Object {
		return out
	}
	// in tells whether element i is one asked for.
	in := func(i int) bool { return i >= start && (count <= 0 || i < start+count) }
	switch v.Kind() {
	case value.Vector:
		elems := v.AsVector().Elems
		end := len(elems)
		if count > 0 {
			end = min(len(elems), start+count)
		}
		for i := min(start, end); i < end; i++ {
			child("["+strconv.Itoa(i)+"]", ref.name+"["+strconv.Itoa(i)+"]", elems[i])
		}
	case value.Map:
		i := 0
		v.AsMap().Each(func(k, e value.Value) {
			if in(i) {
				child(debugger.Show(k), ref.name+"["+k.Repr()+"]", e)
			}
			i++
		})
	case value.Object:
		o := v.AsObject()
		for i, name := range o.Class.FieldNames {
			child(name, ref.name+"->"+name, o.Fields[i])
		}
	}
	return out
}

// variable shows a value: vectors, maps and objects with something in them
// can be expanded. expr is an expression for it ("" if none).
func (s *dapServer) variable(frame int, name, expr string, v value.Value) dap.Variable {
	ref, named, indexed := s.expandable(frame, expr, v)
	return dap.Variable{Name: name, Value: debugger.Show(v), Type: typeName(v), EvaluateName: expr,
		VariablesReference: ref, NamedVariables: named, IndexedVariables: indexed}
}

func (s *dapServer) expandable(frame int, expr string, v value.Value) (ref, named, indexed int) {
	switch v.Kind() {
	case value.Vector:
		if n := len(v.AsVector().Elems); n > 0 {
			return s.ref(dapRef{frame: frame, v: v, name: expr}), 0, n
		}
	case value.Map:
		if n := v.AsMap().Len(); n > 0 {
			return s.ref(dapRef{frame: frame, v: v, name: expr}), 0, n // indexed: in parts when large
		}
	case value.Object:
		if n := len(v.AsObject().Fields); n > 0 {
			return s.ref(dapRef{frame: frame, v: v, name: expr}), n, 0
		}
	}
	return 0, 0, 0
}

// typeName is the type the editor shows: the class of an object, else as
// typeof gives it.
func typeName(v value.Value) string {
	if v.Kind() == value.Object {
		return v.AsObject().Class.Name
	}
	return v.Kind().String()
}

func (s *dapServer) evaluate(m *vm.VM, r *dap.EvaluateRequest) dap.ResponseMessage {
	frame, err := frameIndex(m, r.Arguments.FrameId)
	if err != nil {
		return failure(err)
	}
	v, err := debugger.Eval(m, frame, r.Arguments.Expression)
	if err != nil {
		return failure(err)
	}
	ref, named, indexed := s.expandable(frame, r.Arguments.Expression, v)
	return &dap.EvaluateResponse{Body: dap.EvaluateResponseBody{Result: debugger.Show(v), Type: typeName(v),
		VariablesReference: ref, NamedVariables: named, IndexedVariables: indexed}}
}

// setVariable changes a variable of a scope, an element of a vector or map,
// or a data member of an object, to the value of an expression.
func (s *dapServer) setVariable(m *vm.VM, r *dap.SetVariableRequest) dap.ResponseMessage {
	n, name := r.Arguments.VariablesReference, r.Arguments.Name
	if n < 1 || n > len(s.refs) {
		return failure(errors.New("that variable is no longer there"))
	}
	ref := s.refs[n-1]
	v, err := debugger.Eval(m, ref.frame, r.Arguments.Value)
	if err != nil {
		return failure(err)
	}
	switch ref.scope {
	case "locals":
		if name == "this" || !debugger.Assign(m, ref.frame, name, v) {
			err = fmt.Errorf("%s cannot be changed", name)
		}
	case "globals":
		if !m.SetGlobal(name, v) {
			err = fmt.Errorf("%s cannot be changed", name)
		}
	default:
		switch x := ref.v; x.Kind() {
		case value.Vector:
			if i, e := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, "["), "]")); e != nil {
				err = fmt.Errorf("no element %s", name)
			} else {
				err = value.SetIndex(x, value.MakeInt(int64(i)), v)
			}
		case value.Map:
			err = fmt.Errorf("no entry %s", name)
			x.AsMap().Each(func(k, _ value.Value) {
				if debugger.Show(k) == name {
					err = value.SetIndex(x, k, v)
				}
			})
		case value.Object:
			err = value.SetMember(x, name, v)
		}
	}
	if err != nil {
		return failure(err)
	}
	ref2, named, indexed := s.expandable(ref.frame, "", v)
	return &dap.SetVariableResponse{Body: dap.SetVariableResponseBody{Value: debugger.Show(v), Type: typeName(v),
		VariablesReference: ref2, NamedVariables: named, IndexedVariables: indexed}}
}

func (s *dapServer) exceptionInfo(r *dap.ExceptionInfoRequest) dap.ResponseMessage {
	if s.thrown == nil {
		return failure(errors.New("the program did not stop at an exception"))
	}
	t := s.thrown
	return &dap.ExceptionInfoResponse{Body: dap.ExceptionInfoResponseBody{ExceptionId: t.Kind, Description: t.Msg,
		BreakMode: "unhandled", Details: &dap.ExceptionDetails{Message: t.Msg, TypeName: t.Kind, StackTrace: t.Error()}}}
}

// setBreakpoints sets the line breakpoints of a file (all of them: those
// set before are removed).
func (s *dapServer) setBreakpoints(r *dap.SetBreakpointsRequest) {
	path := r.Arguments.Source.Path
	if path == "" {
		path = r.Arguments.Source.Name
	}
	out := []dap.Breakpoint{}
	if s.sess != nil {
		s.sess.Clear(func(b *debugger.Breakpoint) bool { return b.Func == "" && debugger.SameFile(b.File, path) })
	}
	for _, sb := range r.Arguments.Breakpoints {
		out = append(out, s.setBreak(debugger.Breakpoint{File: path, Line: sb.Line, Cond: sb.Condition, Hit: sb.HitCondition, Log: sb.LogMessage}))
	}
	s.send(&dap.SetBreakpointsResponse{Response: s.response(r.Request), Body: dap.SetBreakpointsResponseBody{Breakpoints: out}})
}

func (s *dapServer) setFunctionBreakpoints(r *dap.SetFunctionBreakpointsRequest) {
	out := []dap.Breakpoint{}
	if s.sess != nil {
		s.sess.Clear(func(b *debugger.Breakpoint) bool { return b.Func != "" })
	}
	for _, fb := range r.Arguments.Breakpoints {
		out = append(out, s.setBreak(debugger.Breakpoint{Func: fb.Name, Cond: fb.Condition, Hit: fb.HitCondition}))
	}
	s.send(&dap.SetFunctionBreakpointsResponse{Response: s.response(r.Request), Body: dap.SetFunctionBreakpointsResponseBody{Breakpoints: out}})
}

// setBreak sets one breakpoint, and says where it is, or why it is not.
func (s *dapServer) setBreak(b debugger.Breakpoint) dap.Breakpoint {
	if s.sess == nil {
		return dap.Breakpoint{Message: "no program is launched"}
	}
	bp, _, err := s.sess.SetBreak(b)
	if err != nil {
		return dap.Breakpoint{Message: err.Error()}
	}
	return dap.Breakpoint{Id: bp.ID, Verified: true, Line: bp.Line, Source: source(bp.File)}
}

// dapWriter sends what the program writes as output events.
type dapWriter struct {
	s        *dapServer
	category string
}

func (w dapWriter) Write(p []byte) (int, error) {
	w.s.output(w.category, string(p))
	return len(p), nil
}

func (s *dapServer) output(category, text string) {
	s.send(&dap.OutputEvent{Event: s.event("output"), Body: dap.OutputEventBody{Category: category, Output: text}})
}

// send writes a message, with the next sequence number.
func (s *dapServer) send(msg dap.Message) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.seq++
	switch m := msg.(type) {
	case dap.ResponseMessage:
		m.GetResponse().Seq = s.seq
	case dap.EventMessage:
		m.GetEvent().Seq = s.seq
	}
	if err := dap.WriteProtocolMessage(s.out, msg); err != nil {
		fmt.Fprintln(s.logErr, "kukel dap:", err)
	}
}

func (s *dapServer) event(name string) dap.Event {
	return dap.Event{ProtocolMessage: dap.ProtocolMessage{Type: "event"}, Event: name}
}

func (s *dapServer) response(req dap.Request) dap.Response {
	return dap.Response{ProtocolMessage: dap.ProtocolMessage{Type: "response"}, Command: req.Command, RequestSeq: req.Seq, Success: true}
}

// responseOf fills in the request's part of a response f made: its
// success and message stay.
func (s *dapServer) responseOf(req dap.Request, r *dap.Response) dap.Response {
	out := s.response(req)
	out.Success, out.Message = r.Success || r.Message == "", r.Message
	return out
}

// failure is the response to a request that failed (whileStopped fills in
// the rest).
func failure(err error) dap.ResponseMessage {
	return &dap.ErrorResponse{Response: dap.Response{Success: false, Message: err.Error()},
		Body: dap.ErrorResponseBody{Error: &dap.ErrorMessage{Format: err.Error(), ShowUser: true}}}
}

func (s *dapServer) fail(req dap.Request, err error) {
	resp := failure(err)
	*resp.GetResponse() = s.responseOf(req, resp.GetResponse())
	s.send(resp)
}
