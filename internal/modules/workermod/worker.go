// Package workermod is the native module "worker": workers are Kukel
// interpreters that run in parallel, each on its own goroutine, and talk
// to the interpreter that started them only through messages.
//
//	use worker;
//
//	square(id) {                        // runs in the worker
//	    for (local m = worker::receive(); m != nil; m = worker::receive())
//	        worker::send([id, m * m]);
//	}
//
//	main() {
//	    local w = worker::start(square, 1);   // or worker::startfile("sq.kkl", ...)
//	    w->send(7);
//	    println(w->receive());                // [1, 49]
//	    w->stop();                            // the worker's receive() gives nil
//	    w->wait();
//	}
//
// Interpreters share nothing: a message is a copy, made by the sender, of a
// boolean, a number, a string, or vectors and maps of those (and nil inside
// them; nil itself cannot be sent, receive gives it at the end).
package workermod

import (
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/maarten-boot/kukel/internal/builtins"
	"github.com/maarten-boot/kukel/internal/bytecode"
	"github.com/maarten-boot/kukel/internal/compiler"
	"github.com/maarten-boot/kukel/internal/loader"
	"github.com/maarten-boot/kukel/internal/modules/eventsmod"
	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
	"github.com/maarten-boot/kukel/internal/vm"
)

// ----------------------------------------------------------------------
// Queues

// queue holds the messages in one direction. Any goroutine may push; one
// side takes them (receive, or the goroutine that feeds onmessage).
type queue struct {
	mu        sync.Mutex
	items     []value.Value
	closed    bool
	ready     chan struct{} // signalled when items or closed change (capacity 1)
	callbacks bool          // onmessage took over: receive() is not allowed
}

func newQueue() *queue { return &queue{ready: make(chan struct{}, 1)} }

func (q *queue) signal() {
	select {
	case q.ready <- struct{}{}:
	default:
	}
}

// push adds v; it reports false if the queue is closed.
func (q *queue) push(v value.Value) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	q.items = append(q.items, v)
	q.signal()
	return true
}

// close ends the queue: once its messages are taken, pop gives no more.
func (q *queue) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.signal()
}

// pop takes the next message, waiting for one at most timeout (forever if
// timeout < 0). It gives false when the queue is closed and empty, when the
// time is up, or when cancel is closed. wait, if not nil, is called before
// it waits.
func (q *queue) pop(timeout time.Duration, cancel <-chan struct{}, wait func()) (value.Value, bool) {
	var expired <-chan time.Time
	if timeout >= 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		expired = t.C
	}
	for {
		q.mu.Lock()
		if len(q.items) > 0 {
			v := q.items[0]
			q.items[0] = value.NilValue
			q.items = q.items[1:]
			if len(q.items) > 0 {
				q.signal() // more for the next pop
			}
			q.mu.Unlock()
			return v, true
		}
		closed := q.closed
		q.mu.Unlock()
		if closed {
			q.signal() // let a later pop see it too
			return value.NilValue, false
		}
		if wait != nil {
			wait()
			wait = nil
		}
		select {
		case <-q.ready:
		case <-expired:
			return value.NilValue, false
		case <-cancel:
			return value.NilValue, false
		}
	}
}

// ----------------------------------------------------------------------
// Copying messages

// copyValue copies v for another interpreter. Vectors and maps are copied
// deeply; a part that occurs twice (or contains itself) is copied once, so
// the copy has the same shape.
func copyValue(a native.Args, v value.Value, seen map[any]value.Value) (value.Value, error) {
	switch v.Kind() {
	case value.Nil, value.Bool, value.Int, value.Float:
		return v, nil // immutable
	case value.String:
		return value.Detached(v), nil // immutable, but its buffer may still grow here
	case value.Vector:
		if c, ok := seen[v.Obj()]; ok {
			return c, nil
		}
		src := v.AsVector().Elems
		elems := make([]value.Value, len(src))
		c := value.MakeVector(elems)
		seen[v.Obj()] = c
		for i, e := range src {
			var err error
			if elems[i], err = copyValue(a, e, seen); err != nil {
				return value.NilValue, err
			}
		}
		return c, nil
	case value.Map:
		if c, ok := seen[v.Obj()]; ok {
			return c, nil
		}
		m := value.NewMap()
		c := value.MakeMap(m)
		seen[v.Obj()] = c
		var err error
		v.AsMap().Each(func(k, e value.Value) {
			if err == nil {
				var ce value.Value
				if ce, err = copyValue(a, e, seen); err == nil {
					err = m.Set(value.Detached(k), ce) // keys are strings and numbers
				}
			}
		})
		return c, err
	}
	return value.NilValue, a.Errorf(value.TypeError, "cannot send %s to another interpreter (only nil, booleans, numbers, strings, vectors and maps)", v.Kind())
}

func copyMessage(a native.Args, v value.Value) (value.Value, error) {
	return copyValue(a, v, map[any]value.Value{})
}

// ----------------------------------------------------------------------
// Workers

// worker connects a parent and a worker interpreter.
type worker struct {
	name   string // what it runs: a function's name or a file
	inbox  *queue // parent -> worker
	outbox *queue // worker -> parent
	done   chan struct{}
	result value.Value // after done: what the worker's function returned (a copy)
	err    error       // after done: why it failed, or nil
}

type linkKey struct{}

// self is the worker the interpreter env runs, or nil in a main program.
func self(env value.Env) *worker {
	w, _ := env.Local(linkKey{}, func() any { return (*worker)(nil) }).(*worker)
	return w
}

// launch runs fn(args...) (fn nil: main) of prog in a new interpreter on
// its own goroutine. The new interpreter writes to the parent's output.
func launch(parent value.Env, name string, prog *bytecode.Program, fn value.Value, args []value.Value) (value.Value, error) {
	p, ok := parent.(*vm.VM)
	if !ok {
		return value.NilValue, errors.New("worker: not running in a kukel VM")
	}
	stdout, stderr := p.ShareOutput()
	child, err := vm.NewWith(prog, p.BuiltinLookup(), p.Config(), stdout, strings.NewReader(""))
	if err != nil {
		return value.NilValue, value.Errorf(value.ValueError, "worker: %v", err)
	}
	child.SetStderr(stderr)
	child.StepLimit = p.StepLimit
	if c := p.CurrentCanceller(); c != nil {
		c.Keep()
		child.ShareCanceller(c) // cancelling the parent cancels its workers
	}
	w := &worker{name: name, inbox: newQueue(), outbox: newQueue(), done: make(chan struct{})}
	child.Local(linkKey{}, func() any { return w })
	if prof := p.Profiler(); prof != nil { // profiled as the parent is, under a name of its own
		child.StartProfile(prof, prof.WorkerName(name))
	}
	go func() {
		defer close(w.done)
		defer w.outbox.close()
		defer child.StopProfile()
		r, err := child.Start(fn, args)
		var re *vm.RuntimeError
		if errors.As(err, &re) && re.Exit != nil {
			r, err = value.MakeInt(int64(*re.Exit)), nil // exit(code) ends this worker only
		}
		if err != nil {
			w.err = err
			return
		}
		if w.result, err = copyMessage(native.Args{Name: "its result"}, r); err != nil {
			w.err = err
		}
	}()
	return Worker.Object(w), nil
}

// failure is the exception wait() throws for a worker that failed.
func (w *worker) failure() error {
	msg := w.err.Error()
	var re *vm.RuntimeError
	var ve *value.Error
	switch {
	case errors.As(w.err, &re):
		msg = re.Kind
		if re.Msg != "" {
			msg += ": " + re.Msg
		}
	case errors.As(w.err, &ve): // the result could not be copied
		msg = ve.Kind + ": " + ve.Msg
	}
	return value.Errorf("WorkerError", "worker %s failed: %s", w.name, msg)
}

func (w *worker) state() string {
	select {
	case <-w.done:
		if w.err != nil {
			return "failed"
		}
		return "done"
	default:
		return "running"
	}
}

// ----------------------------------------------------------------------
// Sending and receiving, for both sides

func send(a native.Args, q *queue, v value.Value, closedMsg string) error {
	if v.IsNil() {
		return a.Errorf(value.ValueError, "nil cannot be sent (receive gives nil when there are no more messages)")
	}
	c, err := copyMessage(a, v)
	if err != nil {
		return err
	}
	if !q.push(c) {
		return a.Errorf(value.ValueError, "%s", closedMsg)
	}
	return nil
}

func receive(a native.Args, env value.Env, q *queue, timeoutArg int) (value.Value, error) {
	timeout := time.Duration(-1)
	if a.Has(timeoutArg) {
		var err error
		if timeout, err = a.Seconds(timeoutArg, "a usable timeout"); err != nil {
			return value.NilValue, err
		}
	}
	q.mu.Lock()
	callbacks := q.callbacks
	q.mu.Unlock()
	if callbacks {
		return value.NilValue, a.Errorf(value.ValueError, "messages go to the onmessage handler, receive() cannot take them")
	}
	v, _ := q.pop(timeout, env.Done(), func() { native.Flush(env) })
	if err := env.Err(); err != nil {
		return value.NilValue, err // cancelled
	}
	return v, nil
}

// onmessage makes the event loop of env call f(message) for every message
// of q; the loop keeps running until q ends.
func onmessage(a native.Args, env value.Env, q *queue, f value.Value) error {
	switch f.Kind() {
	case value.Function, value.Closure, value.Builtin, value.BoundMethod:
	default:
		return a.TypeError(0, "a function")
	}
	q.mu.Lock()
	if q.callbacks {
		q.mu.Unlock()
		return a.Errorf(value.ValueError, "an onmessage handler is already set")
	}
	q.callbacks = true
	q.mu.Unlock()
	loop := eventsmod.For(env)
	release := loop.Hold()
	go func() {
		defer release()
		for {
			v, ok := q.pop(-1, env.Done(), nil)
			if !ok {
				return
			}
			loop.Post(func() error {
				_, err := env.Call(f, []value.Value{v})
				return err
			})
		}
	}()
	return nil
}

// ----------------------------------------------------------------------
// The module

// Worker is the class worker::Worker: the parent's handle of a worker.
var Worker = &native.Class{Name: "Worker"}

func method(name string, min, max int, fn func(a native.Args, env value.Env, w *worker) (value.Value, error)) value.NativeMethod {
	full := "worker::Worker::" + name
	return func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
		a, err := native.Check(full, args, min, max)
		if err != nil {
			return value.NilValue, err
		}
		return fn(a, env, this.State.(*worker))
	}
}

func init() {
	Worker.Methods = map[string]value.NativeMethod{
		// send(v): a copy of v goes to the worker.
		"send": method("send", 1, 1, func(a native.Args, env value.Env, w *worker) (value.Value, error) {
			if w.state() != "running" {
				return value.NilValue, a.Errorf(value.ValueError, "the worker has ended")
			}
			return value.NilValue, send(a, w.inbox, a.V[0], "the worker was stopped")
		}),
		// receive([timeout]): the next message from the worker; nil when
		// the worker has ended (and its messages are taken), or after
		// timeout seconds.
		"receive": method("receive", 0, 1, func(a native.Args, env value.Env, w *worker) (value.Value, error) {
			return receive(a, env, w.outbox, 0)
		}),
		// onmessage(f): the event loop calls f(message) for each message.
		"onmessage": method("onmessage", 1, 1, func(a native.Args, env value.Env, w *worker) (value.Value, error) {
			return value.NilValue, onmessage(a, env, w.outbox, a.V[0])
		}),
		// stop(): no more messages to the worker; its receive() gives nil
		// once it has taken those sent before.
		"stop": method("stop", 0, 0, func(a native.Args, env value.Env, w *worker) (value.Value, error) {
			w.inbox.close()
			return value.NilValue, nil
		}),
		// wait(): waits until the worker ends and gives a copy of what its
		// function returned (a new copy each time); a worker that failed
		// gives a WorkerError.
		"wait": method("wait", 0, 0, func(a native.Args, env value.Env, w *worker) (value.Value, error) {
			if w.state() == "running" {
				native.Flush(env)
			}
			select {
			case <-w.done:
			case <-env.Done():
				return value.NilValue, env.Err()
			}
			if w.err != nil {
				return value.NilValue, w.failure()
			}
			return copyMessage(a, w.result) // a new copy each time
		}),
		// done(): whether the worker has ended.
		"done": method("done", 0, 0, func(a native.Args, env value.Env, w *worker) (value.Value, error) {
			return value.MakeBool(w.state() != "running"), nil
		}),
	}
	Worker.Show = func(o *value.ObjectObj) string {
		w := o.State.(*worker)
		return "worker::Worker(" + w.name + ", " + w.state() + ")"
	}

	inWorker := func(name string, env value.Env) (*worker, error) {
		w := self(env)
		if w == nil {
			return nil, value.Errorf(value.ValueError, "%s: this interpreter is not a worker", name)
		}
		return w, nil
	}
	native.Register(&native.Module{
		Name: "worker",
		Doc:  "workers: interpreters that run in parallel and exchange messages",
		Funcs: map[string]native.Func{
			// start(f, args...): runs f(args...) in a new interpreter of this
			// program (with its own globals). f is a top-level or static
			// member function; the arguments are copied.
			"start": func(env value.Env, args []value.Value) (value.Value, error) {
				a, err := native.Check("worker::start", args, 1, -1)
				if err != nil {
					return value.NilValue, err
				}
				fn, ok := args[0].Obj().(*bytecode.Function)
				if args[0].Kind() != value.Function || !ok {
					return value.NilValue, a.Errorf(value.TypeError, "argument 1 must be a top-level or static member function, not %s (a closure, bound method or built-in cannot run in another interpreter)", kindName(args[0]))
				}
				copied, err := copyArgs(a, args[1:])
				if err != nil {
					return value.NilValue, err
				}
				return launch(env, fn.FuncName, env.(*vm.VM).Program(), args[0], copied)
			},
			// startfile(path, args...): compiles the program in path (a .kkl
			// file, with its modules; or a .kkc file) and runs its main with
			// the vector of the (copied) arguments.
			"startfile": func(env value.Env, args []value.Value) (value.Value, error) {
				a, err := native.Check("worker::startfile", args, 1, -1)
				if err != nil {
					return value.NilValue, err
				}
				path, err := a.String(0)
				if err != nil {
					return value.NilValue, err
				}
				copied, err := copyArgs(a, args[1:])
				if err != nil {
					return value.NilValue, err
				}
				config := env.(*vm.VM).Config()
				if config != nil && config.NoFileModules {
					return value.NilValue, a.Errorf(value.ValueError, "this program may not read program files (startfile is not available)")
				}
				prog, err := load(a, config, path)
				if err != nil {
					return value.NilValue, err
				}
				return launch(env, path, prog, value.NilValue, []value.Value{value.MakeVector(copied)})
			},
			// send(v): in a worker, a copy of v goes to the parent.
			"send": func(env value.Env, args []value.Value) (value.Value, error) {
				a, err := native.Check("worker::send", args, 1, 1)
				if err != nil {
					return value.NilValue, err
				}
				w, err := inWorker(a.Name, env)
				if err != nil {
					return value.NilValue, err
				}
				return value.NilValue, send(a, w.outbox, args[0], "the parent no longer receives")
			},
			// receive([timeout]): in a worker, the next message from the
			// parent; nil after the parent's stop() (once the messages sent
			// before are taken), or after timeout seconds.
			"receive": func(env value.Env, args []value.Value) (value.Value, error) {
				a, err := native.Check("worker::receive", args, 0, 1)
				if err != nil {
					return value.NilValue, err
				}
				w, err := inWorker(a.Name, env)
				if err != nil {
					return value.NilValue, err
				}
				return receive(a, env, w.inbox, 0)
			},
			// onmessage(f): in a worker, the event loop calls f(message) for
			// each message from the parent, until the parent's stop().
			"onmessage": func(env value.Env, args []value.Value) (value.Value, error) {
				a, err := native.Check("worker::onmessage", args, 1, 1)
				if err != nil {
					return value.NilValue, err
				}
				w, err := inWorker(a.Name, env)
				if err != nil {
					return value.NilValue, err
				}
				return value.NilValue, onmessage(a, env, w.inbox, args[0])
			},
			// isworker(): whether this interpreter is a worker.
			"isworker": func(env value.Env, args []value.Value) (value.Value, error) {
				if _, err := native.Check("worker::isworker", args, 0, 0); err != nil {
					return value.NilValue, err
				}
				return value.MakeBool(self(env) != nil), nil
			},
		},
		Classes: []*native.Class{Worker},
	})
}

func copyArgs(a native.Args, args []value.Value) ([]value.Value, error) {
	out := make([]value.Value, len(args))
	for i, v := range args {
		var err error
		if out[i], err = copyMessage(a, v); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// load compiles a .kkl file (with the modules it uses, found as kukel run
// finds them) or reads a .kkc file.
func load(a native.Args, config *native.Config, path string) (*bytecode.Program, error) {
	if strings.HasSuffix(path, ".kkc") {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, a.Errorf(value.IOError, "%v", err)
		}
		prog, err := bytecode.Decode(data) // Decode also verifies
		if err != nil {
			return nil, a.Errorf(value.ValueError, "%s: %v", path, err)
		}
		return prog, nil
	}
	files, err := loader.LoadWith([]string{path}, config)
	if err == nil {
		var prog *bytecode.Program
		if prog, err = compiler.CompileWith(files, builtins.Names(), config); err == nil {
			return prog, nil
		}
	}
	var pe *os.PathError
	if errors.As(err, &pe) {
		return nil, a.Errorf(value.IOError, "%v", err)
	}
	return nil, a.Errorf("CompileError", "%v", err)
}

// kindName names what a value is, closer than typeof: a closure, bound
// method and built-in are all "function" there.
func kindName(v value.Value) string {
	switch v.Kind() {
	case value.Closure:
		return "a function literal"
	case value.BoundMethod:
		return "a bound method"
	case value.Builtin:
		return "a built-in function"
	case value.Nil:
		return "nil"
	case value.Int, value.Object:
		return "an " + v.Kind().String()
	}
	return "a " + v.Kind().String()
}
