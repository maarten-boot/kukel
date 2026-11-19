// Package processmod is the native module "process": starting a program,
// feeding it, capturing what it writes, and waiting for it, with time
// limits and cancellation that leave no processes behind (TODO.md Q118,
// docs/future.md §15). It has no policy of its own: the standard module
// proc, written in Kukel (internal/stdlib/proc.kkl), checks the arguments,
// turns failures into proc::ProcError and is what programs use.
//
// A Go program that embeds Kukel gets it only when it names it in
// StandardModules (F-74): it is the largest power a module gives.
package processmod

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

func init() {
	native.Register(&native.Module{
		Name:  "process",
		Doc:   "starting programs and waiting for them (under module proc)",
		OptIn: true,
		Funcs: map[string]native.Func{
			"run":      run,
			"lookpath": lookpath,
			"environ":  environ,
			"signum":   signumFunc,
		},
	})
}

// Grace is how long a process that a time limit or a cancellation ends
// gets after TERM before it is killed.
var Grace = 2 * time.Second

// live holds the processes that run, with whether each has a group of its
// own, for StopAll.
var live = struct {
	sync.Mutex
	m map[*exec.Cmd]bool
}{m: map[*exec.Cmd]bool{}}

// stopping is set by StopAll: kukel is about to end by a signal, so a run
// whose process it ended waits for that instead of reporting a failure.
var stopping atomic.Bool

// StopAll ends every process the module started that still runs, with
// what it started in turn: TERM, and KILL to what is left after Grace.
// kukel run calls it when it gets Ctrl-C, TERM or HUP, before it ends as
// the signal would end it: a child in a group of its own does not get the
// terminal's Ctrl-C.
func StopAll() {
	stopping.Store(true)
	live.Lock()
	cmds := make(map[*exec.Cmd]bool, len(live.m))
	for c, g := range live.m {
		cmds[c] = g
		terminate(c, g)
	}
	live.Unlock()
	for end := time.Now().Add(Grace); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		live.Lock()
		n := len(live.m)
		live.Unlock()
		if n == 0 {
			break
		}
	}
	for c, g := range cmds {
		kill(c, g)
	}
}

// process::lookpath(name): the path of the program name stands for (found
// on PATH unless it holds a /), or nil.
func lookpath(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("process::lookpath", args, 1, 1)
	if err != nil {
		return value.NilValue, err
	}
	name, err := a.String(0)
	if err != nil {
		return value.NilValue, err
	}
	p, err := exec.LookPath(name)
	if err != nil || name == "" {
		return value.NilValue, nil
	}
	return value.MakeString(p), nil
}

// process::signum(name): the number of signal name ("TERM"), or nil (also
// on Windows, which has none).
func signumFunc(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("process::signum", args, 1, 1)
	if err != nil {
		return value.NilValue, err
	}
	name, err := a.String(0)
	if err != nil {
		return value.NilValue, err
	}
	if n := signalNumber(name); n > 0 {
		return value.MakeInt(int64(n)), nil
	}
	return value.NilValue, nil
}

// process::environ(): the environment as a map, by name.
func environ(env value.Env, args []value.Value) (value.Value, error) {
	if _, err := native.Check("process::environ", args, 0, 0); err != nil {
		return value.NilValue, err
	}
	vars := os.Environ()
	sort.Strings(vars)
	m := value.NewMap()
	for _, kv := range vars {
		if k, v, ok := strings.Cut(kv, "="); ok && k != "" {
			m.Set(value.MakeString(k), value.MakeString(v))
		}
	}
	return value.MakeMap(m), nil
}

// options are run's, as proc gives them (checked there; checked again
// here, so that the module cannot be misused alone).
type options struct {
	in     string // "inherit", "null" or "data"
	data   string // with in "data"
	out    stream
	err    stream
	dir    string
	env    []string // nil: the program's environment
	limit  float64  // seconds, 0: none
	max    int      // bytes captured of each stream
	hasEnv bool
}

// stream is where output goes: "capture", "inherit", "null", "file",
// "append", or for err also "out".
type stream struct {
	kind, path string
}

// process::run(argv, options): runs a program to its end. It gives a map:
// "started" (false, with "error", when it could not be started), "code"
// (nil after a signal), "signal" and "signum", "out" and "err" (when
// captured), "time" (seconds), "timedout" and "truncated" (true when the
// time limit or the cap on captured output ended it). A failing program
// is not an error here: proc decides.
func run(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("process::run", args, 2, 2)
	if err != nil {
		return value.NilValue, err
	}
	argv, err := argvOf(a)
	if err != nil {
		return value.NilValue, err
	}
	o, err := optionsOf(a)
	if err != nil {
		return value.NilValue, err
	}
	return runCmd(env, argv, o)
}

func argvOf(a native.Args) ([]string, error) {
	v := a.V[0]
	if v.Kind() != value.Vector || len(v.AsVector().Elems) == 0 {
		return nil, a.TypeError(0, "a vector of strings: the program and its arguments")
	}
	var argv []string
	for i, e := range v.AsVector().Elems {
		if e.Kind() != value.String {
			return nil, a.Errorf(value.TypeError, "argument %d of the command must be a string, not %s", i+1, e.Kind())
		}
		if strings.IndexByte(e.AsString(), 0) >= 0 {
			return nil, a.Errorf(value.ValueError, "argument %d of the command holds a NUL byte", i+1)
		}
		argv = append(argv, e.AsString())
	}
	if argv[0] == "" {
		return nil, a.Errorf(value.ValueError, "the program's name is empty")
	}
	return argv, nil
}

func optionsOf(a native.Args) (options, error) {
	o := options{in: "null", out: stream{kind: "capture"}, err: stream{kind: "inherit"}, max: 64 << 20}
	m := a.V[1]
	if m.Kind() != value.Map {
		return o, a.TypeError(1, "a map of options")
	}
	var bad error
	str := func(k string, v value.Value) (string, bool) {
		if v.Kind() != value.String {
			bad = a.Errorf(value.TypeError, "option %s must be a string, not %s", k, v.Kind())
			return "", false
		}
		return v.AsString(), true
	}
	m.AsMap().Each(func(k, v value.Value) {
		if bad != nil {
			return
		}
		key := k.String()
		switch key {
		case "in":
			if s, ok := str(key, v); ok {
				if s != "inherit" && s != "null" && s != "data" {
					bad = a.Errorf(value.ValueError, "option in: %q (inherit, null or data)", s)
				}
				o.in = s
			}
		case "data":
			if s, ok := str(key, v); ok {
				o.data = s
			}
		case "out", "err":
			st, err := streamOf(a, key, v)
			if err != nil {
				bad = err
			} else if key == "out" {
				o.out = st
			} else {
				o.err = st
			}
		case "dir":
			if s, ok := str(key, v); ok {
				o.dir = s
			}
		case "env":
			if v.Kind() != value.Vector {
				bad = a.Errorf(value.TypeError, "option env must be a vector of \"name=value\" strings, not %s", v.Kind())
				return
			}
			o.hasEnv = true
			o.env = []string{}
			for _, e := range v.AsVector().Elems {
				if s, ok := str(key, e); ok {
					o.env = append(o.env, s)
				}
			}
		case "timeout":
			if v.Kind() != value.Int && v.Kind() != value.Float {
				bad = a.Errorf(value.TypeError, "option timeout must be a number of seconds, not %s", v.Kind())
				return
			}
			f := v.AsFloat()
			if v.Kind() == value.Int {
				f = float64(v.AsInt())
			}
			if !(f > 0) || f > 1e9 {
				bad = a.Errorf(value.ValueError, "option timeout: %s seconds (more than 0)", v)
			}
			o.limit = f
		case "max":
			if v.Kind() != value.Int || v.AsInt() < 0 {
				bad = a.Errorf(value.ValueError, "option max must be an int of 0 or more (bytes), not %s", v.Repr())
				return
			}
			o.max = int(min(v.AsInt(), 1<<40))
		default:
			bad = a.Errorf(value.ValueError, "unknown option %s", k.Repr())
		}
	})
	if bad == nil && o.out.kind == "out" {
		bad = a.Errorf(value.ValueError, "option out: \"out\" is only for err")
	}
	return o, bad
}

func streamOf(a native.Args, key string, v value.Value) (stream, error) {
	if v.Kind() == value.String {
		switch s := v.AsString(); s {
		case "capture", "inherit", "null", "out":
			return stream{kind: s}, nil
		}
		return stream{}, a.Errorf(value.ValueError, "option %s: %s (capture, inherit, null%s, or {\"file\": path} or {\"append\": path})", key, v.Repr(), map[bool]string{true: ", out", false: ""}[key == "err"])
	}
	if v.Kind() == value.Map && v.AsMap().Len() == 1 {
		k := v.AsMap().Keys()[0]
		p, _, _ := v.AsMap().Get(k)
		if (k.String() == "file" || k.String() == "append") && k.Kind() == value.String && p.Kind() == value.String && p.AsString() != "" {
			return stream{kind: k.String(), path: p.AsString()}, nil
		}
	}
	return stream{}, a.Errorf(value.ValueError, "option %s: %s (capture, inherit, null, or {\"file\": path} or {\"append\": path})", key, v.Repr())
}

// overflow tells, once, that a capture went over its cap.
type overflow struct {
	once sync.Once
	ch   chan struct{}
}

func (o *overflow) trip() { o.once.Do(func() { close(o.ch) }) }

// capture is a writer that keeps at most max bytes; more trips over.
type capture struct {
	b    bytes.Buffer
	max  int
	over *overflow
}

func (c *capture) Write(p []byte) (int, error) {
	if room := c.max - c.b.Len(); len(p) > room {
		c.b.Write(p[:max(room, 0)])
		c.over.trip()
		return len(p), nil // the rest is dropped; the process is ended
	}
	return c.b.Write(p)
}

func runCmd(env value.Env, argv []string, o options) (value.Value, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = o.dir
	if o.hasEnv {
		cmd.Env = o.env
	}
	term, termOut := native.Terminal(env)
	var files []*os.File
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()
	over := &overflow{ch: make(chan struct{})}
	var outCap, errCap *capture
	sink := func(s stream, std io.Writer, isErr bool) (io.Writer, error) {
		switch s.kind {
		case "capture":
			c := &capture{max: o.max, over: over}
			if isErr {
				errCap = c
			} else {
				outCap = c
			}
			return c, nil
		case "inherit":
			if !isErr && termOut != nil {
				return termOut, nil
			}
			return std, nil
		case "null":
			return nil, nil
		}
		flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
		if s.kind == "append" {
			flags = os.O_WRONLY | os.O_CREATE | os.O_APPEND
		}
		f, err := os.OpenFile(s.path, flags, 0o644)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
		return f, nil
	}
	w, err := sink(o.out, env.Stdout(), false)
	if err != nil {
		return notStarted(argv, err), nil
	}
	cmd.Stdout = w
	if o.err.kind == "out" {
		cmd.Stderr = cmd.Stdout
	} else if cmd.Stderr, err = sink(o.err, env.Stderr(), true); err != nil {
		return notStarted(argv, err), nil
	}
	inherited := false
	switch o.in {
	case "data":
		cmd.Stdin = strings.NewReader(o.data)
	case "inherit":
		inherited = true
		if term != nil && env.Stdin().Buffered() == 0 {
			cmd.Stdin = term
		} else {
			cmd.Stdin = env.Stdin()
		}
	}
	// A process group of its own, so that a time limit or a cancellation
	// ends what it started too; not when it reads the terminal, which
	// only the foreground group may.
	grouped := !inherited
	setGroup(cmd, grouped)
	cmd.WaitDelay = Grace // pipes a grandchild keeps open do not hold the wait

	defer native.Wait(env).Done()
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return notStarted(argv, err), nil
	}
	live.Lock()
	live.m[cmd] = grouped
	live.Unlock()
	done := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		live.Lock()
		delete(live.m, cmd)
		live.Unlock()
		done <- err
	}()
	var limit <-chan time.Time
	if o.limit > 0 {
		t := time.NewTimer(time.Duration(o.limit * float64(time.Second)))
		defer t.Stop()
		limit = t.C
	}
	timedout, truncated, cancelled := false, false, false
	var waitErr error
	select {
	case waitErr = <-done:
	case <-limit:
		timedout = true
	case <-over.ch:
		truncated = true
	case <-env.Done():
		cancelled = true
	}
	if timedout || truncated || cancelled {
		waitErr = stop(cmd, grouped, done)
	}
	if stopping.Load() {
		select {} // kukel ends by the signal that made StopAll run
	}
	elapsed := time.Since(start).Seconds()
	if cancelled {
		if e := env.Err(); e != nil {
			return value.NilValue, e
		}
	}
	r := value.NewMap()
	set := func(k string, v value.Value) { r.Set(value.MakeString(k), v) }
	set("started", value.True)
	code, signal, signum := exitOf(cmd, waitErr)
	set("code", code)
	set("signal", signal)
	set("signum", signum)
	set("out", captured(outCap))
	set("err", captured(errCap))
	set("time", value.MakeFloat(elapsed))
	set("timedout", value.MakeBool(timedout))
	set("truncated", value.MakeBool(truncated))
	return value.MakeMap(r), nil
}

func captured(c *capture) value.Value {
	if c == nil {
		return value.NilValue
	}
	return value.MakeString(c.b.String())
}

// stop ends a process: TERM (to its group), and KILL after Grace.
func stop(cmd *exec.Cmd, grouped bool, done chan error) error {
	terminate(cmd, grouped)
	select {
	case err := <-done:
		kill(cmd, grouped) // what the group left running
		return err
	case <-time.After(Grace):
		kill(cmd, grouped)
		return <-done
	}
}

// notStarted is the result for a program that could not be started.
func notStarted(argv []string, err error) value.Value {
	msg := err.Error()
	var ee *exec.Error
	var pe *os.PathError
	switch {
	case errors.As(err, &ee) && errors.Is(ee.Err, exec.ErrNotFound):
		msg = fmt.Sprintf("program %s not found", argv[0])
	case errors.As(err, &pe):
		msg = fmt.Sprintf("%s: %s", pe.Path, native.IOMessage(pe.Err))
	}
	r := value.NewMap()
	r.Set(value.MakeString("started"), value.False)
	r.Set(value.MakeString("error"), value.MakeString(msg))
	return value.MakeMap(r)
}
