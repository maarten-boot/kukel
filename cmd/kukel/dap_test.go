package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-dap"
)

// dapClient plays the editor in a kukel dap session.
type dapClient struct {
	t       *testing.T
	w       io.Writer
	msgs    chan dap.Message // what kukel dap sends, read by one goroutine
	pending []dap.Message    // read but not yet asked for (an event before a response, ...)
	seq     int
	output  strings.Builder // the output events' text, by category: "stdout: ..." lines
	code    chan int
}

// startDap runs kukel dap in the test's directory.
func startDap(t *testing.T) *dapClient {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	c := &dapClient{t: t, w: inW, msgs: make(chan dap.Message, 100), code: make(chan int, 1)}
	go func() {
		c.code <- run([]string{"dap"}, inR, outW, io.Discard)
		outW.Close()
	}()
	go func() {
		r := bufio.NewReader(outR)
		for {
			msg, err := dap.ReadProtocolMessage(r)
			if err != nil {
				close(c.msgs)
				return
			}
			c.msgs <- msg
		}
	}()
	t.Cleanup(func() { inW.Close() })
	return c
}

func (c *dapClient) send(command string, args any) {
	c.t.Helper()
	c.seq++
	msg := map[string]any{"seq": c.seq, "type": "request", "command": command}
	if args != nil {
		msg["arguments"] = args
	}
	data, _ := json.Marshal(msg)
	if _, err := io.WriteString(c.w, "Content-Length: "+strconv.Itoa(len(data))+"\r\n\r\n"+string(data)); err != nil {
		c.t.Fatal(err)
	}
}

// next reads messages until one for which keep is true, collecting
// output on the way.
func (c *dapClient) next(keep func(dap.Message) bool) dap.Message {
	c.t.Helper()
	for i, msg := range c.pending {
		if keep(msg) {
			c.pending = append(c.pending[:i], c.pending[i+1:]...)
			return msg
		}
	}
	timeout := time.After(10 * time.Second)
	for {
		select {
		case msg, ok := <-c.msgs:
			if !ok {
				c.t.Fatal("kukel dap ended")
			}
			if o, isOutput := msg.(*dap.OutputEvent); isOutput {
				c.output.WriteString(o.Body.Category + ": " + o.Body.Output)
				continue
			}
			if keep(msg) {
				return msg
			}
			c.pending = append(c.pending, msg) // for a later next
		case <-timeout:
			c.t.Fatal("no answer from kukel dap")
		}
	}
}

// response reads the response to the last request.
func (c *dapClient) response() dap.ResponseMessage {
	c.t.Helper()
	msg := c.next(func(m dap.Message) bool {
		r, ok := m.(dap.ResponseMessage)
		return ok && r.GetResponse().RequestSeq == c.seq
	})
	return msg.(dap.ResponseMessage)
}

// ok sends a request and gives its successful response.
func (c *dapClient) ok(command string, args any) dap.ResponseMessage {
	c.t.Helper()
	c.send(command, args)
	r := c.response()
	if !r.GetResponse().Success {
		c.t.Fatalf("%s: %s", command, r.GetResponse().Message)
	}
	return r
}

// event reads events until the named one.
func (c *dapClient) event(name string) dap.EventMessage {
	c.t.Helper()
	return c.next(func(m dap.Message) bool {
		e, ok := m.(dap.EventMessage)
		return ok && e.GetEvent().Event == name
	}).(dap.EventMessage)
}

// stop disconnects and gives kukel dap's exit code, reading (and dropping)
// what it still sends, as an editor would.
func (c *dapClient) stop() int {
	c.t.Helper()
	c.ok("disconnect", nil)
	go func() {
		for range c.msgs {
		}
	}()
	select {
	case code := <-c.code:
		return code
	case <-time.After(10 * time.Second):
		c.t.Fatal("kukel dap did not end after disconnect")
	}
	return 0
}

// vars gives the variables of a reference, by name.
func (c *dapClient) vars(ref int) map[string]dap.Variable {
	c.t.Helper()
	r := c.ok("variables", map[string]any{"variablesReference": ref}).(*dap.VariablesResponse)
	out := map[string]dap.Variable{}
	for _, v := range r.Body.Variables {
		out[v.Name] = v
	}
	return out
}

const dapProgram = `class P {
    x, y;
    P(x, y);
}
P::P(x, y) { this->x = x; this->y = y; }
sq(n) {
    return n * n;
}
main(args) {
    local v = [1, 2, 3], m = {"a": 1}, p = new P(1, 2), t = 0;
    for (local i = 0; i < 3; i++) {
        t += sq(v[i]);
    }
    println("total", t);
    println(readline(), args);
}
`

// A session as an editor has it: breakpoints with a condition, a hit count
// (a function breakpoint) and a log message; the stack, scopes, variables
// (expanded), evaluate, changing values; the program's output and input.
func TestDap(t *testing.T) {
	t.Chdir(t.TempDir())
	os.WriteFile("p.kkl", []byte(dapProgram), 0o644)
	os.WriteFile("in.txt", []byte("hello\n"), 0o644)
	c := startDap(t)
	init := c.ok("initialize", map[string]any{"adapterID": "kukel"}).(*dap.InitializeResponse)
	if !init.Body.SupportsConditionalBreakpoints || !init.Body.SupportsLogPoints || !init.Body.SupportsSetVariable {
		t.Errorf("capabilities: %+v", init.Body)
	}
	c.ok("launch", map[string]any{"program": "p.kkl", "args": []string{"x"}, "stdin": "in.txt"})
	c.event("initialized")
	bps := c.ok("setBreakpoints", map[string]any{"source": map[string]any{"path": "p.kkl"},
		"breakpoints": []map[string]any{{"line": 12, "condition": "i == 1"}, {"line": 14, "logMessage": "t is {t}, {{literally}} {t > 0 ? \"}\" : {\"k\": \"{\"}}"}, {"line": 99}}}).(*dap.SetBreakpointsResponse)
	if b := bps.Body.Breakpoints; len(b) != 3 || !b[0].Verified || b[0].Line != 12 || !b[1].Verified || b[2].Verified || !strings.Contains(b[2].Message, "no code at or after") {
		t.Errorf("breakpoints: %+v", b)
	}
	fbs := c.ok("setFunctionBreakpoints", map[string]any{"breakpoints": []map[string]any{{"name": "sq", "hitCondition": "2"}, {"name": "nope"}}}).(*dap.SetFunctionBreakpointsResponse)
	if b := fbs.Body.Breakpoints; len(b) != 2 || !b[0].Verified || b[0].Line != 7 || b[1].Verified || b[1].Message != `no function "nope"` {
		t.Errorf("function breakpoints: %+v", b)
	}
	c.ok("configurationDone", nil)

	// The condition: at line 12 when i is 1 (t is 1 then).
	stop := c.event("stopped").(*dap.StoppedEvent)
	if stop.Body.Reason != "breakpoint" || len(stop.Body.HitBreakpointIds) != 1 {
		t.Errorf("stop 1: %+v", stop.Body)
	}
	st := c.ok("stackTrace", map[string]any{"threadId": 1}).(*dap.StackTraceResponse)
	if f := st.Body.StackFrames; len(f) != 1 || f[0].Name != "main" || f[0].Line != 12 || !strings.HasSuffix(f[0].Source.Path, "p.kkl") {
		t.Fatalf("stack: %+v", f)
	}
	sc := c.ok("scopes", map[string]any{"frameId": st.Body.StackFrames[0].Id}).(*dap.ScopesResponse)
	locals := c.vars(sc.Body.Scopes[0].VariablesReference)
	if locals["t"].Value != "1" || locals["i"].Value != "1" || locals["args"].Value != `["x"]` || locals["p"].Type != "P" {
		t.Errorf("locals: %+v", locals)
	}
	elems := c.vars(locals["v"].VariablesReference)
	if len(elems) != 3 || elems["[2]"].Value != "3" || elems["[2]"].EvaluateName != "v[2]" || locals["v"].IndexedVariables != 3 {
		t.Errorf("v: %+v", elems)
	}
	if fields := c.vars(locals["p"].VariablesReference); fields["y"].Value != "2" || fields["y"].EvaluateName != "p->y" {
		t.Errorf("p: %+v", fields)
	}
	if entries := c.vars(locals["m"].VariablesReference); entries[`"a"`].Value != "1" {
		t.Errorf("m: %+v", entries)
	}
	ev := c.ok("evaluate", map[string]any{"expression": "v[i] * 10 + (p->x > 0 ? 1 : 0)", "frameId": 0}).(*dap.EvaluateResponse)
	if ev.Body.Result != "21" {
		t.Errorf("evaluate: %+v", ev.Body)
	}
	c.send("evaluate", map[string]any{"expression": "sq(1)", "frameId": 0})
	if r := c.response(); r.GetResponse().Success || !strings.Contains(r.GetResponse().Message, "calls are not evaluated") {
		t.Errorf("evaluate a call: %+v", r.GetResponse())
	}
	// Change t and v[2]: the program goes on with them.
	if r := c.ok("setVariable", map[string]any{"variablesReference": sc.Body.Scopes[0].VariablesReference, "name": "t", "value": "100"}).(*dap.SetVariableResponse); r.Body.Value != "100" {
		t.Errorf("set t: %+v", r.Body)
	}
	c.ok("setVariable", map[string]any{"variablesReference": locals["v"].VariablesReference, "name": "[2]", "value": "10"})
	c.ok("continue", map[string]any{"threadId": 1})

	// The hit count: in sq, the 2nd time (n is 2).
	stop = c.event("stopped").(*dap.StoppedEvent)
	if stop.Body.Reason != "function breakpoint" {
		t.Errorf("stop 2: %+v", stop.Body)
	}
	st = c.ok("stackTrace", map[string]any{"threadId": 1}).(*dap.StackTraceResponse)
	if f := st.Body.StackFrames; len(f) != 2 || f[0].Name != "sq" || f[1].Name != "main" {
		t.Errorf("stack 2: %+v", f)
	}
	if ev := c.ok("evaluate", map[string]any{"expression": "n", "frameId": st.Body.StackFrames[0].Id}).(*dap.EvaluateResponse); ev.Body.Result != "2" {
		t.Errorf("n: %+v", ev.Body)
	}
	c.ok("continue", map[string]any{"threadId": 1})

	ex := c.event("exited").(*dap.ExitedEvent)
	c.event("terminated")
	if code := c.stop(); code != 0 || ex.Body.ExitCode != 0 {
		t.Errorf("exit code %d, %d", code, ex.Body.ExitCode)
	}
	want := "console: t is 204, {literally} }\nstdout: total 204\nstdout: hello [\"x\"]\n"
	if got := c.output.String(); got != want {
		t.Errorf("output:\n%s\nwant:\n%s", got, want)
	}
}

// Stepping from the first line, a stop at an exception nothing catches
// (and none when the editor turns that off), pause, disconnecting from a
// running program, a program that does not compile, running without the
// debugger.
func TestDapStepsAndEnds(t *testing.T) {
	t.Chdir(t.TempDir())
	os.WriteFile("s.kkl", []byte("f(x) {\n    local y = x + 1;\n    return y;\n}\nmain() {\n    local a = f(1);\n    println(a);\n    throw new Error(\"boom\", \"ValueError\");\n}\n"), 0o644)
	line := func(c *dapClient) int {
		c.event("stopped")
		st := c.ok("stackTrace", map[string]any{"threadId": 1}).(*dap.StackTraceResponse)
		return st.Body.StackFrames[0].Line
	}
	c := startDap(t)
	c.ok("initialize", nil)
	c.ok("launch", map[string]any{"program": "s.kkl", "stopOnEntry": true})
	c.event("initialized")
	c.ok("configurationDone", nil)
	var lines []int
	lines = append(lines, line(c)) // entry: main's first line
	c.ok("stepIn", map[string]any{"threadId": 1})
	lines = append(lines, line(c)) // into f
	c.ok("next", map[string]any{"threadId": 1})
	lines = append(lines, line(c))
	c.ok("stepOut", map[string]any{"threadId": 1})
	lines = append(lines, line(c)) // back in main
	c.ok("next", map[string]any{"threadId": 1})
	lines = append(lines, line(c))
	if want := []int{6, 2, 3, 7, 8}; !slices.Equal(lines, want) { // out of f: at the next line main starts
		t.Errorf("lines %v, want %v", lines, want)
	}
	c.ok("continue", map[string]any{"threadId": 1})
	stop := c.event("stopped").(*dap.StoppedEvent)
	if stop.Body.Reason != "exception" || stop.Body.Text != "ValueError: boom" {
		t.Errorf("exception stop: %+v", stop.Body)
	}
	info := c.ok("exceptionInfo", map[string]any{"threadId": 1}).(*dap.ExceptionInfoResponse)
	if info.Body.ExceptionId != "ValueError" || info.Body.Description != "boom" || info.Body.BreakMode != "unhandled" {
		t.Errorf("exception info: %+v", info.Body)
	}
	c.ok("continue", map[string]any{"threadId": 1})
	if ex := c.event("exited").(*dap.ExitedEvent); ex.Body.ExitCode != 1 {
		t.Errorf("exit code %d", ex.Body.ExitCode)
	}
	c.stop()
	if out := c.output.String(); !strings.Contains(out, "stdout: 2\n") || !strings.Contains(out, "stderr: ValueError: boom\n  at main (s.kkl:8)") {
		t.Errorf("output:\n%s", out)
	}

	// The exception filter off: no stop, the program ends.
	c = startDap(t)
	c.ok("initialize", nil)
	c.ok("launch", map[string]any{"program": "s.kkl"})
	c.event("initialized")
	c.ok("setExceptionBreakpoints", map[string]any{"filters": []string{}})
	c.ok("configurationDone", nil)
	if ex := c.event("exited").(*dap.ExitedEvent); ex.Body.ExitCode != 1 {
		t.Errorf("without the filter: exit code %d", ex.Body.ExitCode)
	}
	c.stop()

	// Pause a program in a loop, then disconnect while it runs.
	os.WriteFile("loop.kkl", []byte("main() {\n    local n = 0;\n    while (true) {\n        n++;\n    }\n}\n"), 0o644)
	c = startDap(t)
	c.ok("initialize", nil)
	c.ok("launch", map[string]any{"program": "loop.kkl"})
	c.event("initialized")
	c.ok("configurationDone", nil)
	c.ok("pause", map[string]any{"threadId": 1})
	if stop := c.event("stopped").(*dap.StoppedEvent); stop.Body.Reason != "pause" {
		t.Errorf("pause: %+v", stop.Body)
	}
	c.send("next", map[string]any{"threadId": 1})
	c.response()
	c.event("stopped")
	c.ok("continue", map[string]any{"threadId": 1})
	c.send("stackTrace", map[string]any{"threadId": 1})
	if r := c.response(); r.GetResponse().Success || r.GetResponse().Message != "the program is running" {
		t.Errorf("stackTrace while running: %+v", r.GetResponse())
	}
	c.stop()

	// A program that does not compile: launch fails, with the error.
	os.WriteFile("bad.kkl", []byte("main() { x = 1; }\n"), 0o644)
	c = startDap(t)
	c.ok("initialize", nil)
	c.send("launch", map[string]any{"program": "bad.kkl"})
	if r := c.response(); r.GetResponse().Success || !strings.Contains(r.GetResponse().Message, "bad.kkl:1:10: undefined name x") {
		t.Errorf("launch of a bad program: %+v", r.GetResponse())
	}
	c.stop()

	// Without debugging: breakpoints do not stop it.
	c = startDap(t)
	c.ok("initialize", nil)
	c.ok("launch", map[string]any{"program": "s.kkl", "noDebug": true})
	c.event("initialized")
	c.ok("setBreakpoints", map[string]any{"source": map[string]any{"path": "s.kkl"}, "breakpoints": []map[string]any{{"line": 7}}})
	c.ok("configurationDone", nil)
	if ex := c.event("exited").(*dap.ExitedEvent); ex.Body.ExitCode != 1 {
		t.Errorf("noDebug: exit code %d", ex.Body.ExitCode)
	}
	c.stop()
}

// launchDap starts a session of a program (launch's arguments), with
// breakpoints at lines of its file, up to configurationDone.
func launchDap(t *testing.T, launch map[string]any, lines ...int) *dapClient {
	t.Helper()
	c := startDap(t)
	c.ok("initialize", nil)
	c.ok("launch", launch)
	c.event("initialized")
	if len(lines) > 0 {
		var bps []map[string]any
		for _, l := range lines {
			bps = append(bps, map[string]any{"line": l})
		}
		c.ok("setBreakpoints", map[string]any{"source": map[string]any{"path": launch["program"]}, "breakpoints": bps})
	}
	c.ok("configurationDone", nil)
	return c
}

// noStop reports a stopped event among those read but not asked for.
func (c *dapClient) noStop(what string) {
	c.t.Helper()
	for _, m := range c.pending {
		if e, ok := m.(*dap.StoppedEvent); ok {
			c.t.Errorf("%s: stopped again: %+v", what, e.Body)
		}
	}
}

// Terminate and disconnect end the program in every state: try cannot
// catch that, it does not stop again, and kukel dap does not hang.
func TestDapTerminate(t *testing.T) {
	t.Chdir(t.TempDir())
	os.WriteFile("tr.kkl", []byte("main() {\n    for (local i = 0; i < 3; i++) {\n        try {\n            println(\"line\", i);\n        } catch (e) {\n            println(\"caught\", e->kind);\n        }\n    }\n    println(\"end\");\n}\n"), 0o644)
	os.WriteFile("loop.kkl", []byte("main() {\n    local n = 0;\n    while (true) {\n        n++;\n    }\n}\n"), 0o644)
	os.WriteFile("ex.kkl", []byte("main() {\n    throw new Error(\"boom\");\n}\n"), 0o644)

	// Terminate at a breakpoint inside a try.
	c := launchDap(t, map[string]any{"program": "tr.kkl"}, 4)
	c.event("stopped")
	c.ok("terminate", nil)
	c.event("exited")
	c.event("terminated")
	c.noStop("terminate in a try")
	c.stop()
	if out := c.output.String(); out != "" {
		t.Errorf("terminate in a try: output %q", out)
	}

	// Disconnect at the entry stop.
	c = launchDap(t, map[string]any{"program": "loop.kkl", "stopOnEntry": true})
	c.event("stopped")
	c.stop()

	// Disconnect in a try: no output after it.
	c = launchDap(t, map[string]any{"program": "tr.kkl"}, 4)
	c.event("stopped")
	c.stop()
	if out := c.output.String(); out != "" {
		t.Errorf("disconnect in a try: output %q", out)
	}

	// Terminate at an exception nothing catches.
	c = launchDap(t, map[string]any{"program": "ex.kkl"})
	if stop := c.event("stopped").(*dap.StoppedEvent); stop.Body.Reason != "exception" {
		t.Errorf("exception: %+v", stop.Body)
	}
	c.ok("terminate", nil)
	c.event("terminated")
	c.noStop("terminate at an exception")
	c.stop()

	// Terminate while it runs: no error in the console.
	c = launchDap(t, map[string]any{"program": "loop.kkl"})
	c.ok("terminate", nil)
	c.event("terminated")
	c.stop()
	if out := c.output.String(); strings.Contains(out, "Error") {
		t.Errorf("terminate while running: output %q", out)
	}

	// Terminate before configurationDone: terminated at once.
	c = startDap(t)
	c.ok("initialize", nil)
	c.ok("launch", map[string]any{"program": "loop.kkl"})
	c.event("initialized")
	c.ok("terminate", nil)
	c.event("terminated")
	c.ok("configurationDone", nil) // it does not start now
	if code := c.stop(); code != 0 {
		t.Errorf("terminate before configurationDone: exit %d", code)
	}
}

// Requests with what does not fit: a bad frame, configurationDone twice,
// bad breakpoints and names; launch that fails changes nothing; pause
// without debugging.
func TestDapBadRequests(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	os.WriteFile("p.kkl", []byte(dapProgram), 0o644)
	c := launchDap(t, map[string]any{"program": "p.kkl"}, 12)
	c.ok("configurationDone", nil) // again: no harm
	c.event("stopped")
	st := c.ok("stackTrace", map[string]any{"threadId": 1}).(*dap.StackTraceResponse)
	if id := st.Body.StackFrames[0].Id; id != 1 {
		t.Errorf("frame id %d, want 1", id)
	}
	c.send("scopes", map[string]any{"frameId": 99})
	if r := c.response(); r.GetResponse().Success || r.GetResponse().Message != "no frame 99" {
		t.Errorf("scopes of frame 99: %+v", r.GetResponse())
	}
	c.ok("variables", map[string]any{"variablesReference": 1})
	c.send("evaluate", map[string]any{"expression": "t", "frameId": 2})
	if r := c.response(); r.GetResponse().Success || r.GetResponse().Message != "no frame 2" {
		t.Errorf("evaluate in frame 2: %+v", r.GetResponse())
	}
	sc := c.ok("scopes", map[string]any{"frameId": 1}).(*dap.ScopesResponse)
	locals := c.vars(sc.Body.Scopes[0].VariablesReference)
	c.send("setVariable", map[string]any{"variablesReference": locals["v"].VariablesReference, "name": "abc", "value": "1"})
	if r := c.response(); r.GetResponse().Success || r.GetResponse().Message != "no element abc" {
		t.Errorf("set v's element abc: %+v", r.GetResponse())
	}
	bps := c.ok("setBreakpoints", map[string]any{"source": map[string]any{"path": "p.kkl"}, "breakpoints": []map[string]any{
		{"line": 0}, {"line": 12, "condition": "len(v) > 1"}, {"line": 12, "condition": "t ="}, {"line": 12, "hitCondition": "% 0"},
		{"line": 12, "hitCondition": "==!=3"}}}).(*dap.SetBreakpointsResponse)
	want := []string{"no line 0: lines start at 1", "the condition len(v) > 1: calls are not evaluated", "the condition t =: expected expression",
		`a hit count % n needs an n above 0, not "% 0"`, `a hit count is a number`}
	for i, b := range bps.Body.Breakpoints {
		if b.Verified || !strings.HasPrefix(b.Message, want[i]) {
			t.Errorf("breakpoint %d: %+v, want %q", i, b, want[i])
		}
	}
	other := c.ok("setBreakpoints", map[string]any{"source": map[string]any{"path": "/elsewhere/x.kkl"}, "breakpoints": []map[string]any{{"line": 3}}}).(*dap.SetBreakpointsResponse)
	if b := other.Body.Breakpoints[0]; b.Verified || b.Message != "/elsewhere/x.kkl is not a file of the program" {
		t.Errorf("a breakpoint in another file: %+v", b)
	}
	c.ok("continue", map[string]any{"threadId": 1})
	c.event("terminated")
	c.stop()

	// A launch that fails leaves the working directory and environment as
	// they were; a second launch works.
	os.Mkdir("sub", 0o755)
	c = startDap(t)
	c.ok("initialize", nil)
	c.send("launch", map[string]any{"program": "nosuch.kkl", "cwd": "sub", "env": map[string]string{"KUKEL_DAP_TEST": "x"}})
	if r := c.response(); r.GetResponse().Success {
		t.Error("launch of a missing program worked")
	}
	if wd, _ := os.Getwd(); wd != dir {
		t.Errorf("after a failed launch, the working directory is %s", wd)
	}
	if _, set := os.LookupEnv("KUKEL_DAP_TEST"); set {
		t.Error("after a failed launch, the environment is changed")
	}
	c.ok("launch", map[string]any{"program": "p.kkl", "noDebug": true, "stdin": "p.kkl"})
	c.event("initialized")
	c.send("pause", map[string]any{"threadId": 1})
	if r := c.response(); r.GetResponse().Success {
		t.Error("pause without debugging worked")
	}
	c.ok("configurationDone", nil)
	c.event("terminated")
	c.stop()
}

// The variables a frame sees: a local before a captured variable of the
// same name; large values shortened, and a large map in parts; a function
// breakpoint stops once per call, not where a loop on its first line
// comes back.
func TestDapValues(t *testing.T) {
	t.Chdir(t.TempDir())
	os.WriteFile("v.kkl", []byte(`count(n) {
    for (local i = 0; i < n; i++) { }
}
main() {
    local x = 1, big = [], m = {};
    for (local i = 0; i < 100000; i++) {
        push(big, i);
        m[i] = i;
    }
    local f = fn() {
        println(x);
        {
            local x = 2;
            println(x);
        }
    };
    f();
    count(4);
}
`), 0o644)
	c := launchDap(t, map[string]any{"program": "v.kkl"}, 14)
	c.ok("setFunctionBreakpoints", map[string]any{"breakpoints": []map[string]any{{"name": "count"}}})
	c.event("stopped")
	if ev := c.ok("evaluate", map[string]any{"expression": "x"}).(*dap.EvaluateResponse); ev.Body.Result != "2" {
		t.Errorf("x in the inner block: %s", ev.Body.Result)
	}
	// main's frame (1; f's is 2).
	sc := c.ok("scopes", map[string]any{"frameId": 1}).(*dap.ScopesResponse)
	vars := c.vars(sc.Body.Scopes[0].VariablesReference)
	if b := vars["big"]; len(b.Value) > 1100 || !strings.HasSuffix(b.Value, "…]") || b.IndexedVariables != 100000 {
		t.Errorf("big: %d bytes, %d indexed", len(b.Value), b.IndexedVariables)
	}
	mv := vars["m"]
	if mv.IndexedVariables != 100000 || mv.NamedVariables != 0 {
		t.Errorf("m: %+v", mv)
	}
	part := c.ok("variables", map[string]any{"variablesReference": mv.VariablesReference, "filter": "indexed", "start": 500, "count": 3}).(*dap.VariablesResponse)
	if v := part.Body.Variables; len(v) != 3 || v[0].Name != "500" || v[2].Value != "502" || v[1].EvaluateName != "m[501]" {
		t.Errorf("m from 500: %+v", v)
	}
	c.ok("setVariable", map[string]any{"variablesReference": mv.VariablesReference, "name": "501", "value": "-1"})
	if ev := c.ok("evaluate", map[string]any{"expression": "m[501]", "frameId": 1}).(*dap.EvaluateResponse); ev.Body.Result != "-1" {
		t.Errorf("m[501] set: %s", ev.Body.Result)
	}
	// The elements of an expression that is not a name are named in
	// parentheses; a failed hover or watch is not for the user's attention.
	ev := c.ok("evaluate", map[string]any{"expression": "true ? big : big", "frameId": 1}).(*dap.EvaluateResponse)
	part = c.ok("variables", map[string]any{"variablesReference": ev.Body.VariablesReference, "filter": "indexed", "start": 0, "count": 1}).(*dap.VariablesResponse)
	if v := part.Body.Variables; len(v) != 1 || v[0].EvaluateName != "(true ? big : big)[0]" {
		t.Errorf("an element of a conditional expression: %+v", v)
	}
	for ctx, show := range map[string]bool{"hover": false, "watch": false, "repl": true} {
		c.send("evaluate", map[string]any{"expression": "nosuch", "context": ctx})
		if e, ok := c.response().(*dap.ErrorResponse); !ok || e.Body.Error == nil || e.Body.Error.ShowUser != show {
			t.Errorf("a failed %s: %+v", ctx, e)
		}
	}
	// In f's frame: x (captured) first, then the local x.
	sc = c.ok("scopes", map[string]any{"frameId": 2}).(*dap.ScopesResponse)
	r := c.ok("variables", map[string]any{"variablesReference": sc.Body.Scopes[0].VariablesReference}).(*dap.VariablesResponse)
	if v := r.Body.Variables; len(v) != 2 || v[0].Value != "1" || v[1].Value != "2" {
		t.Errorf("f's variables: %+v", v)
	}
	c.ok("setVariable", map[string]any{"variablesReference": sc.Body.Scopes[0].VariablesReference, "name": "x", "value": "3"})
	c.ok("continue", map[string]any{"threadId": 1})
	if stop := c.event("stopped").(*dap.StoppedEvent); stop.Body.Reason != "function breakpoint" {
		t.Errorf("count: %+v", stop.Body)
	}
	c.ok("continue", map[string]any{"threadId": 1})
	c.event("exited")
	c.noStop("count's loop")
	c.stop()
	if out := c.output.String(); out != "stdout: 1\nstdout: 3\n" {
		t.Errorf("output %q", out)
	}
}

// The program's output with workers that write too, flushed where it
// stops (run with -race).
func TestDapWorkerOutput(t *testing.T) {
	t.Chdir(t.TempDir())
	os.WriteFile("w.kkl", []byte("use worker;\nchat(n) {\n    for (local i = 0; i < n; i++) {\n        println(\"w\", i);\n    }\n    return n;\n}\n"+
		"main() {\n    local w = worker::start(chat, 2000);\n    for (local i = 0; i < 5; i++) {\n        println(\"m\", i);\n    }\n    println(w->wait());\n}\n"), 0o644)
	c := launchDap(t, map[string]any{"program": "w.kkl"}, 11)
	for range 5 {
		c.event("stopped")
		c.ok("continue", map[string]any{"threadId": 1})
	}
	c.event("exited")
	c.stop()
	// (An output event may end inside a line: the text without the categories.)
	if out := strings.ReplaceAll(c.output.String(), "stdout: ", ""); !strings.HasSuffix(out, "\n2000\n") || !strings.Contains(out, "m 4\n") {
		t.Errorf("output %.300q", out)
	}
}
