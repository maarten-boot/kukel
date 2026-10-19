// Package eventsmod is the native module "events": an event loop that
// calls Kukel functions when timers expire or other modules post events.
//
//	use events;
//	events::every(1, fn() { println("tick"); });
//	events::after(3.5, events::stop);
//	events::run();          // returns when nothing is left, or on stop()
//
// Each interpreter has its own loop. Handlers run one at a time on the
// interpreter's goroutine, inside events::run(). Other Go modules (a GUI,
// sockets) feed the loop from their own goroutines with Loop.Post, and keep
// run() going while they may still deliver events with Loop.Hold.
package eventsmod

import (
	"container/heap"
	"sync"
	"time"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

// Loop is the event loop of one interpreter.
type Loop struct {
	env value.Env

	mu     sync.Mutex
	posted []func() error // from Post, run in order on the interpreter's goroutine
	holds  int            // open Holds
	wake   chan struct{}  // signalled by Post and Release (capacity 1)

	// Used only on the interpreter's goroutine.
	timers  timerHeap
	active  int // timers that will still fire
	seq     uint64
	running bool
	stopped bool
	onerror value.Value // nil, or the handler for exceptions of handlers
	polls   []*poll
}

// poll is a function the loop calls regularly (Loop.Poll).
type poll struct {
	fn    func() error
	every time.Duration
	last  time.Time
}

type loopKey struct{}

// For returns the loop of the interpreter env belongs to.
func For(env value.Env) *Loop {
	return env.Local(loopKey{}, func() any {
		return &Loop{env: env, wake: make(chan struct{}, 1)}
	}).(*Loop)
}

// Post makes fn run on the interpreter's goroutine, inside events::run(),
// after the events posted before it. It may be called from any goroutine.
// fn may call Kukel functions (env.Call); an error it returns is treated
// like an exception of a handler.
func (l *Loop) Post(fn func() error) {
	l.mu.Lock()
	l.posted = append(l.posted, fn)
	l.mu.Unlock()
	l.signal()
}

// Poll has fn called on the interpreter's goroutine about every `every`
// while events::run() runs: for a source that must be asked for its events
// from that goroutine, such as a GUI toolkit. An error fn returns is
// treated like an exception of a handler. Poll is called on the
// interpreter's goroutine; the returned function stops the calls.
func (l *Loop) Poll(fn func() error, every time.Duration) (remove func()) {
	p := &poll{fn: fn, every: every}
	l.polls = append(l.polls, p)
	return func() {
		for i, q := range l.polls {
			if q == p {
				l.polls = append(l.polls[:i], l.polls[i+1:]...)
				return
			}
		}
	}
}

// runPolls calls the polls that are due, and gives the time the next one
// is due (zero: none).
func (l *Loop) runPolls(now time.Time) (time.Time, error) {
	var next time.Time
	for _, p := range append([]*poll(nil), l.polls...) { // a poll may remove polls
		if now.Sub(p.last) >= p.every {
			p.last = now
			if err := l.dispatch(p.fn()); err != nil {
				return time.Time{}, err
			}
		}
		if due := p.last.Add(p.every); next.IsZero() || due.Before(next) {
			next = due
		}
	}
	return next, nil
}

// Hold keeps events::run() going (instead of returning when nothing is
// left) until the returned function is called: for a source that may
// still post events, such as an open window or socket. It may be called
// from any goroutine; calling the release function more than once has no
// effect.
func (l *Loop) Hold() (release func()) {
	l.mu.Lock()
	l.holds++
	l.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			l.holds--
			l.mu.Unlock()
			l.signal()
		})
	}
}

func (l *Loop) signal() {
	select {
	case l.wake <- struct{}{}:
	default: // already signalled
	}
}

// queued returns the number of posted functions and of holds.
func (l *Loop) queued() (posted, holds int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.posted), l.holds
}

// next takes the first posted function.
func (l *Loop) next() func() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	fn := l.posted[0]
	l.posted[0] = nil
	l.posted = l.posted[1:]
	return fn
}

// run dispatches events until stop() or until nothing is left: no active
// timer, nothing posted and no hold.
func (l *Loop) run(a native.Args) error {
	if l.running {
		return a.Errorf(value.ValueError, "the event loop is already running (a handler cannot call events::run)")
	}
	l.running, l.stopped = true, false
	defer func() { l.running = false }()
	for !l.stopped {
		// Polls that are due, also when the loop is busy.
		now := time.Now()
		nextPoll, err := l.runPolls(now)
		if err != nil {
			return err
		}
		// Timers that are due, in the order of their deadlines.
		for !l.stopped && len(l.timers) > 0 && !l.timers[0].deadline.After(now) {
			t := heap.Pop(&l.timers).(*timer)
			if t.cancelled {
				continue
			}
			if t.interval > 0 {
				t.deadline = t.deadline.Add(t.interval)
				if t.deadline.Before(now) { // fell behind: do not catch up in a burst
					t.deadline = now.Add(t.interval)
				}
				l.push(t)
			} else {
				t.fired = true
				l.active--
			}
			_, err := l.env.Call(t.fn, t.args)
			if err := l.dispatch(err); err != nil {
				return err
			}
		}
		// Posted events: those queued now (events they post wait for the
		// next round, so that timers are not starved). What is not run
		// stays queued, also when an exception leaves run().
		n, _ := l.queued()
		for i := 0; i < n && !l.stopped; i++ {
			if err := l.dispatch(l.next()()); err != nil {
				return err
			}
		}
		if l.stopped || n > 0 {
			continue
		}
		// Nothing left?
		for len(l.timers) > 0 && l.timers[0].cancelled {
			heap.Pop(&l.timers)
		}
		posted, holds := l.queued()
		if posted > 0 {
			continue
		}
		if l.active == 0 && holds == 0 {
			return nil
		}
		// Wait for the next timer, poll, posted event or released hold.
		var wait *time.Timer
		var due <-chan time.Time
		if len(l.timers) > 0 || !nextPoll.IsZero() {
			deadline := nextPoll
			if len(l.timers) > 0 && (deadline.IsZero() || l.timers[0].deadline.Before(deadline)) {
				deadline = l.timers[0].deadline
			}
			wait = time.NewTimer(time.Until(deadline))
			due = wait.C
		}
		native.Flush(l.env)
		select {
		case <-l.wake:
		case <-due:
		case <-l.env.Done(): // the program was cancelled
			if wait != nil {
				wait.Stop()
			}
			return l.env.Err()
		}
		if wait != nil {
			wait.Stop()
		}
	}
	return nil
}

// dispatch handles the outcome of a handler: an exception goes to the
// onerror handler if there is one (the loop goes on), else out of run().
func (l *Loop) dispatch(err error) error {
	if err == nil {
		return nil
	}
	thrown, ok := l.env.Thrown(err)
	if !ok || l.onerror.IsNil() {
		return err
	}
	_, err = l.env.Call(l.onerror, []value.Value{thrown})
	return err
}

// ----------------------------------------------------------------------
// Timers

// timer is the state of an events::Timer object.
type timer struct {
	deadline  time.Time
	interval  time.Duration // > 0 for every()
	fn        value.Value
	args      []value.Value
	seq       uint64 // timers with the same deadline fire in the order they were made
	index     int    // in the heap; -1 when not in it
	every     string // the interval as written, for Show
	cancelled bool
	fired     bool // an after() timer that has run
}

type timerHeap []*timer

func (h timerHeap) Len() int { return len(h) }
func (h timerHeap) Less(i, j int) bool {
	if !h[i].deadline.Equal(h[j].deadline) {
		return h[i].deadline.Before(h[j].deadline)
	}
	return h[i].seq < h[j].seq
}
func (h timerHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index, h[j].index = i, j
}
func (h *timerHeap) Push(x any) {
	t := x.(*timer)
	t.index = len(*h)
	*h = append(*h, t)
}
func (h *timerHeap) Pop() any {
	old := *h
	t := old[len(old)-1]
	old[len(old)-1] = nil
	t.index = -1
	*h = old[:len(old)-1]
	return t
}

func (l *Loop) push(t *timer) {
	l.seq++
	t.seq = l.seq
	heap.Push(&l.timers, t)
}

// Timer is the class events::Timer: what after() and every() give.
var Timer = &native.Class{
	Name: "Timer",
	Methods: map[string]value.NativeMethod{
		// cancel() stops the timer; it does not fire again. Cancelling a
		// timer that is no longer active has no effect.
		"cancel": func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
			if _, err := native.Check("events::Timer::cancel", args, 0, 0); err != nil {
				return value.NilValue, err
			}
			t := this.State.(*timer)
			if !t.cancelled && !t.fired {
				t.cancelled = true
				l := For(env)
				l.active--
				if t.index >= 0 { // out of the heap now, not at its deadline
					heap.Remove(&l.timers, t.index)
				}
			}
			return value.NilValue, nil
		},
		// active() tells whether the timer will still fire.
		"active": func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
			if _, err := native.Check("events::Timer::active", args, 0, 0); err != nil {
				return value.NilValue, err
			}
			t := this.State.(*timer)
			return value.MakeBool(!t.cancelled && !t.fired), nil
		},
	},
	Show: func(o *value.ObjectObj) string {
		t := o.State.(*timer)
		switch {
		case t.cancelled:
			return "events::Timer(cancelled)"
		case t.fired:
			return "events::Timer(fired)"
		case t.interval > 0:
			return "events::Timer(every " + t.every + "s)"
		}
		return "events::Timer(once)"
	},
}

// callable reports a TypeError unless argument i can be called.
func callable(a native.Args, i int) error {
	switch a.V[i].Kind() {
	case value.Function, value.Closure, value.Builtin, value.BoundMethod:
		return nil
	}
	return a.TypeError(i, "a function")
}

// newTimer implements after() and every().
func newTimer(name string, repeat bool) native.Func {
	return func(env value.Env, args []value.Value) (value.Value, error) {
		a, err := native.Check(name, args, 2, -1)
		if err != nil {
			return value.NilValue, err
		}
		secs, err := a.Number(0)
		if err != nil {
			return value.NilValue, err
		}
		if err := callable(a, 1); err != nil {
			return value.NilValue, err
		}
		switch {
		case secs != secs || secs < 0 || secs > 1e9:
			return value.NilValue, a.Errorf(value.ValueError, "%s seconds is not a usable delay", a.V[0].String())
		case repeat && secs < 0.001:
			return value.NilValue, a.Errorf(value.ValueError, "an interval must be at least 0.001 seconds, got %s", value.FormatFloat(secs))
		}
		d := time.Duration(secs * float64(time.Second))
		t := &timer{deadline: time.Now().Add(d), fn: args[1], args: append([]value.Value(nil), args[2:]...)}
		if repeat {
			t.interval, t.every = d, a.V[0].String()
		}
		l := For(env)
		l.push(t)
		l.active++
		return Timer.Object(t), nil
	}
}

func init() {
	native.Register(&native.Module{
		Name: "events",
		Doc:  "an event loop: timers, posted calls, events of other modules",
		Funcs: map[string]native.Func{
			// after(seconds, f, args..): call f(args..) once, that many
			// seconds from now. Gives an events::Timer.
			"after": newTimer("events::after", false),
			// every(seconds, f, args..): call f(args..) every that many
			// seconds, the first time after one interval.
			"every": newTimer("events::every", true),
			// post(f, args..): call f(args..) soon, after the events already
			// queued.
			"post": func(env value.Env, args []value.Value) (value.Value, error) {
				a, err := native.Check("events::post", args, 1, -1)
				if err != nil {
					return value.NilValue, err
				}
				if err := callable(a, 0); err != nil {
					return value.NilValue, err
				}
				fn, rest := args[0], append([]value.Value(nil), args[1:]...)
				For(env).Post(func() error {
					_, err := env.Call(fn, rest)
					return err
				})
				return value.NilValue, nil
			},
			// run(): dispatch events until stop() or until nothing is left.
			"run": func(env value.Env, args []value.Value) (value.Value, error) {
				a, err := native.Check("events::run", args, 0, 0)
				if err != nil {
					return value.NilValue, err
				}
				return value.NilValue, For(env).run(a)
			},
			// stop(): run() returns after the current handler.
			"stop": func(env value.Env, args []value.Value) (value.Value, error) {
				if _, err := native.Check("events::stop", args, 0, 0); err != nil {
					return value.NilValue, err
				}
				For(env).stopped = true
				return value.NilValue, nil
			},
			// onerror(f): f(e) receives the exceptions handlers do not
			// catch, and the loop goes on; onerror(nil) lets them leave
			// run() again.
			"onerror": func(env value.Env, args []value.Value) (value.Value, error) {
				a, err := native.Check("events::onerror", args, 1, 1)
				if err != nil {
					return value.NilValue, err
				}
				if !args[0].IsNil() && callable(a, 0) != nil {
					return value.NilValue, a.TypeError(0, "a function or nil")
				}
				For(env).onerror = args[0]
				return value.NilValue, nil
			},
			// pending(): the number of active timers, posted events and
			// holds: what keeps run() going.
			"pending": func(env value.Env, args []value.Value) (value.Value, error) {
				if _, err := native.Check("events::pending", args, 0, 0); err != nil {
					return value.NilValue, err
				}
				l := For(env)
				l.mu.Lock()
				n := l.active + len(l.posted) + l.holds
				l.mu.Unlock()
				return value.MakeInt(int64(n)), nil
			},
		},
		Classes: []*native.Class{Timer},
	})
}
