package processmod

import (
	"io"
	"strings"
	"sync"
	"time"

	"github.com/maarten-boot/kukel/internal/modules/eventsmod"
	"github.com/maarten-boot/kukel/internal/modules/fsmod"
	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

// Child is process::Child, a program that spawn started: proc::Process is
// made of one.
var Child = &native.Class{
	Name: "Child",
	Methods: map[string]value.NativeMethod{
		"stdin":   childMethod("stdin", 0, 0, stdinOf),
		"stdout":  childMethod("stdout", 0, 0, stdoutOf),
		"stderr":  childMethod("stderr", 0, 0, stderrOf),
		"wait":    childMethod("wait", 0, 1, waitChild),
		"kill":    childMethod("kill", 0, 1, killChild),
		"pid":     childMethod("pid", 0, 0, pidOf),
		"onexit":  childMethod("onexit", 1, 1, onexit),
		"onlines": childMethod("onlines", 2, 2, onlines),
		"running": childMethod("running", 0, 0, runningOf),
	},
	Show: func(o *value.ObjectObj) string {
		c := o.State.(*child)
		state := "running"
		if !c.isRunning() {
			state = "ended"
		}
		return "process::Child(" + c.j.argvs[0][0] + ", " + state + ")"
	},
}

// child is the Go part of a process::Child.
type child struct {
	j                   *job
	stdin, out, errPipe value.Value    // fs::Files, or nil
	result              value.Value    // run's map, once waited for
	readers             sync.WaitGroup // onlines's: onexit comes after their last line
}

func (c *child) isRunning() bool {
	select {
	case <-c.j.done:
		return false
	default:
		return true
	}
}

func childMethod(name string, min, max int, fn func(a native.Args, env value.Env, c *child) (value.Value, error)) value.NativeMethod {
	full := "process::Child::" + name
	return func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
		a, err := native.Check(full, args, min, max)
		if err != nil {
			return value.NilValue, err
		}
		return fn(a, env, this.State.(*child))
	}
}

// process::spawn(argv, options): starts a program and gives a
// process::Child at once; run's options, and "pipe" for in, out and err.
// A program that cannot be started gives run's map for that instead.
func spawn(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("process::spawn", args, 2, 2)
	if err != nil {
		return value.NilValue, err
	}
	argv, err := argvOf(a, a.V[0], "the command")
	if err != nil {
		return value.NilValue, err
	}
	o, err := optionsOf(a, true)
	if err != nil {
		return value.NilValue, err
	}
	j, failed := newJob(env, [][]string{argv}, o)
	if failed.Kind() != value.Nil {
		return failed, nil
	}
	if failed := j.start(); failed.Kind() != value.Nil {
		return failed, nil
	}
	c := &child{j: j, stdin: value.NilValue, out: value.NilValue, errPipe: value.NilValue, result: value.NilValue}
	name := argv[0]
	if j.stdin != nil {
		c.stdin = fsmod.NewPipe(env, "<stdin of "+name+">", "w", j.stdin)
	}
	if j.stdout != nil {
		c.out = fsmod.NewPipe(env, "<stdout of "+name+">", "r", j.stdout)
	}
	if j.stderr != nil {
		c.errPipe = fsmod.NewPipe(env, "<stderr of "+name+">", "r", j.stderr)
	}
	ch := children(env)
	ch.set[c] = struct{}{}
	return Child.Object(c), nil
}

func stdinOf(a native.Args, env value.Env, c *child) (value.Value, error)  { return c.stdin, nil }
func stdoutOf(a native.Args, env value.Env, c *child) (value.Value, error) { return c.out, nil }
func stderrOf(a native.Args, env value.Env, c *child) (value.Value, error) { return c.errPipe, nil }

func pidOf(a native.Args, env value.Env, c *child) (value.Value, error) {
	return value.MakeInt(int64(c.j.cmds[0].Process.Pid)), nil
}

func runningOf(a native.Args, env value.Env, c *child) (value.Value, error) {
	return value.MakeBool(c.isRunning()), nil
}

// c->wait([timeout]): waits for the end and gives run's map (the same map
// at every call); with a timeout (seconds) that passes first, nil, and the
// program runs on. The program's standard input pipe is closed first, so
// that a program that reads to its end ends.
func waitChild(a native.Args, env value.Env, c *child) (value.Value, error) {
	if c.result.Kind() != value.Nil {
		return c.result, nil
	}
	var timeout *time.Duration
	if a.Has(0) {
		d, err := a.Seconds(0, "timeout")
		if err != nil {
			return value.NilValue, err
		}
		timeout = &d
	}
	if c.stdin.Kind() != value.Nil {
		if err := fsmod.Close(c.stdin); err != nil {
			return value.NilValue, a.Errorf(value.IOError, "%s", native.IOMessage(err))
		}
	}
	defer native.Wait(env).Done()
	finished, err := c.j.wait(env, timeout)
	if err != nil || !finished {
		return value.NilValue, err
	}
	c.result = c.j.result()
	delete(children(env).set, c)
	return c.result, nil
}

// c->kill([signal]): sends a signal ("TERM", the default, "KILL", "INT",
// "HUP", "QUIT", "USR1", "USR2") to the program and what it started (its
// process group); on Windows each ends the program. A program that has
// ended gets nothing. It gives whether the program was still running.
func killChild(a native.Args, env value.Env, c *child) (value.Value, error) {
	name := "TERM"
	if a.Has(0) {
		var err error
		if name, err = a.String(0); err != nil {
			return value.NilValue, err
		}
		if _, ok := signals[name]; !ok {
			return value.NilValue, a.Errorf(value.ValueError, "unknown signal %q (TERM, KILL, INT, HUP, QUIT, USR1 or USR2)", name)
		}
	}
	if !c.isRunning() {
		return value.False, nil
	}
	signalJob(c.j, name)
	return value.True, nil
}

// c->onexit(f): the event loop (events::run) calls f(result) when the
// program has ended, with run's map, after the lines of its onlines
// handlers; until then the loop goes on. The program counts as waited for.
func onexit(a native.Args, env value.Env, c *child) (value.Value, error) {
	f := a.V[0]
	if !f.IsCallable() {
		return value.NilValue, a.TypeError(0, "a function")
	}
	loop := eventsmod.For(env)
	release := loop.Hold()
	go func() {
		<-c.j.done
		c.readers.Wait() // the lines first, then the end
		loop.Post(func() error {
			defer release()
			if c.result.Kind() == value.Nil {
				c.result = c.j.result()
				delete(children(env).set, c)
			}
			_, err := env.Call(f, []value.Value{c.result})
			return err
		})
	}()
	return value.NilValue, nil
}

// c->onlines(f, stream): the event loop calls f(line) for each line of the
// pipe stream ("out" or "err"), without its line ending, and f(nil) at its
// end; until then the loop goes on. The pipe's fs::File is read here from
// now on (what it had buffered too): read it no more.
func onlines(a native.Args, env value.Env, c *child) (value.Value, error) {
	f := a.V[0]
	if !f.IsCallable() {
		return value.NilValue, a.TypeError(0, "a function")
	}
	name, err := a.String(1)
	if err != nil {
		return value.NilValue, err
	}
	var pipe value.Value
	switch name {
	case "out":
		pipe = c.out
	case "err":
		pipe = c.errPipe
	default:
		return value.NilValue, a.Errorf(value.ValueError, "the stream is \"out\" or \"err\", not %q", name)
	}
	if pipe.Kind() == value.Nil {
		return value.NilValue, a.Errorf(value.ValueError, "the program's %s is no pipe (\"pipe\" among the options)", name)
	}
	r := fsmod.Reader(pipe)
	if r == nil {
		return value.NilValue, a.Errorf(value.ValueError, "the program's %s pipe is closed", name)
	}
	loop := eventsmod.For(env)
	release := loop.Hold()
	c.readers.Add(1)
	call := func(v value.Value) {
		loop.Post(func() error {
			_, err := env.Call(f, []value.Value{v})
			return err
		})
	}
	go func() {
		defer release()
		defer c.readers.Done()
		for {
			line, err := r.ReadString('\n')
			if line != "" {
				call(value.MakeString(strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")))
			}
			if err != nil {
				if err != io.EOF {
					break // the pipe was closed meanwhile
				}
				call(value.NilValue)
				return
			}
		}
	}()
	return value.NilValue, nil
}

// childrenKey finds an interpreter's running children.
type childrenKey struct{}

type running struct {
	env value.Env
	set map[*child]struct{}
}

func children(env value.Env) *running {
	return env.Local(childrenKey{}, func() any { return &running{env: env, set: map[*child]struct{}{}} }).(*running)
}

// End is called when the program ends (VM.End): the children it did not
// wait for are waited for, after their pipes are closed (so that one that
// reads its input, or writes to a pipe nobody reads, ends); after a
// cancellation they are ended first.
func (r *running) End() error {
	if len(r.set) == 0 {
		return nil
	}
	defer native.Wait(r.env).Done()
	for c := range r.set {
		for _, p := range []value.Value{c.stdin, c.out, c.errPipe} {
			if p.Kind() != value.Nil {
				fsmod.Close(p)
			}
		}
		if r.env.Err() != nil {
			c.j.stop()
		}
		<-c.j.done
	}
	r.set = map[*child]struct{}{}
	return nil
}
