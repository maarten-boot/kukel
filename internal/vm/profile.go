package vm

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/maarten-boot/kukel/internal/bytecode"
	"github.com/maarten-boot/kukel/internal/value"
)

// Profiling (TODO.md Q78): a goroutine per interpreter asks for a sample
// every Interval by setting a flag; the VM looks at the flag where it
// already looks for cancellation (step: backward jumps, calls that take the
// slow path, throws) and after every call of a Go function, and there
// records its call stack, on its own goroutine. A program that is not
// profiled pays nothing for this. Calls are counted exactly.

// Profiler collects the samples and call counts of the interpreters it
// profiles: a program and its workers. Its methods are safe for concurrent
// use.
type Profiler struct {
	Interval time.Duration // between samples (0: 1 ms)
	CPU      bool          // each sample weighs Interval: waiting counts (almost) nothing

	mu       sync.Mutex
	samples  map[string]*Sample
	calls    map[Calls]int64 // by interpreter and function (Func, or Go for built-ins and Go functions)
	workers  int
	total    time.Duration
	nSamples int64
}

// Sample is the time of one call stack: the interpreter (as Interp names
// it), its frames outermost first, and the time (or the number of samples)
// that ended there.
type Sample struct {
	Interp string
	Stack  []ProfFrame
	Time   time.Duration
	Count  int64
}

// ProfFrame is a function of a sampled stack and the line it was at.
type ProfFrame struct {
	Func *bytecode.Function
	Line int
}

// NewProfiler makes a Profiler.
func NewProfiler(interval time.Duration, cpu bool) *Profiler {
	if interval <= 0 {
		interval = time.Millisecond
	}
	return &Profiler{Interval: interval, CPU: cpu, samples: map[string]*Sample{},
		calls: map[Calls]int64{}}
}

// profState is the profiling of one interpreter.
type profState struct {
	p       *Profiler
	name    string               // "main", "worker 1: f"
	want    atomic.Bool          // a sample is wanted (set by the ticker)
	last    time.Time            // of the sample before
	funcs   []*bytecode.Function // the program's Init and Funcs: counts[i] counts the calls of funcs[i]
	counts  []int64
	calls   map[*bytecode.Function]int64 // calls of other functions (of programs linked later)
	goCalls []goCount                    // calls of built-ins and of functions written in Go: the most called first
	stop    chan struct{}
	done    chan struct{}
}

// StartProfile profiles vm with p from now on, under the name interp,
// until StopProfile.
func (vm *VM) StartProfile(p *Profiler, interp string) {
	s := &profState{p: p, name: interp, last: time.Now(), calls: map[*bytecode.Function]int64{},
		stop: make(chan struct{}), done: make(chan struct{})}
	s.funcs = append([]*bytecode.Function{vm.prog.Init}, vm.prog.Funcs...)
	s.counts = make([]int64, len(s.funcs))
	vm.prof, vm.watched = s, true
	go func() {
		defer close(s.done)
		t := time.NewTicker(p.Interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				s.want.Store(true)
			case <-s.stop:
				return
			}
		}
	}()
}

// StopProfile ends the profiling of vm and adds its call counts to the
// Profiler. It is called on vm's goroutine, after it ran.
func (vm *VM) StopProfile() {
	s := vm.prof
	if s == nil {
		return
	}
	vm.prof = nil
	close(s.stop)
	<-s.done
	p := s.p
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, n := range s.counts {
		if n > 0 {
			p.calls[Calls{Interp: s.name, Func: s.funcs[i]}] += n
		}
	}
	for fn, n := range s.calls {
		p.calls[Calls{Interp: s.name, Func: fn}] += n
	}
	for _, c := range s.goCalls {
		if c.b != nil {
			p.calls[Calls{Interp: s.name, Go: c.b.Name}] += c.n
		} else {
			p.calls[Calls{Interp: s.name, Go: c.m.FullName()}] += c.n
		}
	}
}

// Profiler is the Profiler of vm, or nil.
func (vm *VM) Profiler() *Profiler {
	if vm.prof == nil {
		return nil
	}
	return vm.prof.p
}

// WorkerName names a new worker that runs fn: "worker 1: f".
func (p *Profiler) WorkerName(fn string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.workers++
	return fmt.Sprintf("worker %d: %s", p.workers, fn)
}

// count counts a call of fn.
func (s *profState) count(fn *bytecode.Function) {
	if i := fn.Index + 1; i >= 0 && i < len(s.funcs) && s.funcs[i] == fn {
		s.counts[i]++
		return
	}
	s.calls[fn]++
}

// goCount counts the calls of a built-in (b) or of a member function
// written in Go (m).
type goCount struct {
	b *value.BuiltinObj
	m *value.MethodObj
	n int64
}

// countGo counts a call of b or m. A program calls few of them, some often:
// a list in which each call moves its entry one place forward finds the
// often called ones in a step or two (cheaper than a map).
func (s *profState) countGo(b *value.BuiltinObj, m *value.MethodObj) {
	for i := range s.goCalls {
		if c := &s.goCalls[i]; c.b == b && c.m == m {
			c.n++
			if i > 0 && c.n > s.goCalls[i-1].n {
				s.goCalls[i], s.goCalls[i-1] = s.goCalls[i-1], s.goCalls[i]
			}
			return
		}
	}
	s.goCalls = append(s.goCalls, goCount{b, m, 1})
}

// The pc step takes for the top frame: where the VM is (pc >= 0), the top
// frame's own ip (pcTop: it was saved), or the instruction before it
// (pcCaller: the top frame called a function, its ip is past the call).
const (
	pcTop    = -1
	pcCaller = -2
)

// sample records the call stack, as a sample weighing the time since the
// one before (CPU: one interval). pc is where the top frame is (see
// pcTop).
func (vm *VM) sample(pc int) {
	s := vm.prof
	s.want.Store(false)
	now := time.Now()
	weight := now.Sub(s.last)
	if s.p.CPU {
		weight = s.p.Interval
	}
	s.last = now
	n := len(vm.frames)
	if n == 0 {
		return
	}
	stack := make([]ProfFrame, n)
	for i, f := range vm.frames {
		at := vm.framePC(i)
		if i == n-1 {
			switch {
			case pc >= 0:
				at = pc
			case pc == pcCaller && f.ip > 0:
				at = f.ip - 1
			default:
				at = f.ip
			}
		}
		stack[i] = ProfFrame{f.fn, f.fn.LineAt(at)}
	}
	var key strings.Builder
	key.WriteString(s.name)
	for _, f := range stack {
		fmt.Fprintf(&key, "\x00%p:%d", f.Func, f.Line)
	}
	p := s.p
	p.mu.Lock()
	defer p.mu.Unlock()
	smp := p.samples[key.String()]
	if smp == nil {
		smp = &Sample{Interp: s.name, Stack: stack}
		p.samples[key.String()] = smp
	}
	smp.Time += weight
	smp.Count++
	p.total += weight
	p.nSamples++
}

// Samples gives the samples so far, and their total time.
func (p *Profiler) Samples() ([]Sample, time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Sample, 0, len(p.samples))
	for _, s := range p.samples {
		out = append(out, *s)
	}
	return out, p.total
}

// Calls names what a call count counts: the calls in interpreter Interp of
// Kukel function Func, or (Func nil) of the built-in or Go function Go.
type Calls struct {
	Interp string
	Func   *bytecode.Function
	Go     string
}

// Calls gives the call counts of the interpreters that stopped profiling.
func (p *Profiler) Calls() map[Calls]int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[Calls]int64, len(p.calls))
	for k, n := range p.calls {
		out[k] = n
	}
	return out
}
