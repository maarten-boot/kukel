package vm

import (
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/maarten-boot/kukel/internal/bytecode"
	"github.com/maarten-boot/kukel/internal/value"
)

// Profiling (TODO.md Q78): a goroutine per Profiler asks the interpreters
// that run for a sample every Interval by setting a flag; the VM looks at
// the flag where it already looks for cancellation (step: backward jumps,
// calls that take the slow path, throws), at each return, and after every
// call of a Go function, and there records its call stack, on its own goroutine, in a
// call tree. A program that is not profiled pays nothing for this. Calls
// are counted exactly; the counts go to the Profiler at each sample and
// when the profiling pauses or stops.

// Profiler collects the samples and call counts of the interpreters it
// profiles: a program and its workers. Its methods are safe for concurrent
// use.
type Profiler struct {
	Interval time.Duration // between samples (0 or less: 1 ms)
	CPU      bool          // leave out the time spent waiting in Go functions (see VM.WaitStart)

	mu       sync.Mutex
	roots    map[string]*profNode // the call tree of each interpreter
	nodes    map[nodeKey]*profNode
	order    []*profNode     // all nodes, each after its parent
	calls    map[Calls]int64 // by interpreter and function (Func, or Go for built-ins and Go functions)
	interps  []string        // the interpreters profiled, in the order they started
	running  map[*profState]bool
	ticking  bool // the goroutine that asks for samples runs
	workers  int
	total    time.Duration // the time of the samples
	elapsed  time.Duration // the wall time between the samples (with CPU, also what was left out)
	nSamples int64
	start    time.Time // when the first interpreter started
	end      time.Time // when the last one paused or stopped
}

// profNode is a node of the call tree: a function at a line, called from
// its parent (a root, of an interpreter, has no function). Its time and
// count are of the samples that ended there.
type profNode struct {
	parent *profNode
	fn     *bytecode.Function
	line   int
	interp string
	time   time.Duration
	count  int64
}

type nodeKey struct {
	parent *profNode
	fn     *bytecode.Function
	line   int
}

// NewProfiler makes a Profiler.
func NewProfiler(interval time.Duration, cpu bool) *Profiler {
	if interval <= 0 {
		interval = time.Millisecond
	}
	return &Profiler{Interval: interval, CPU: cpu, roots: map[string]*profNode{}, nodes: map[nodeKey]*profNode{},
		calls: map[Calls]int64{}, running: map[*profState]bool{}}
}

// profState is the profiling of one interpreter.
type profState struct {
	p       *Profiler
	name    string    // "main", "worker 1: f"
	root    *profNode // of its call tree
	want    atomic.Bool
	last    time.Time            // of the sample before (or of ResumeProfile)
	pending time.Duration        // running time before the last PauseProfile, not in a sample yet
	waited  atomic.Int64         // nanoseconds spent waiting in Go functions since the sample before (CPU)
	path    []pathStep           // the stack of the sample before, to find its nodes again
	funcs   []*bytecode.Function // the program's Init and Funcs: counts[i] counts the calls of funcs[i]
	counts  []int64
	calls   map[*bytecode.Function]int64 // calls of other functions (of programs linked later)
	goCalls []goCount                    // calls of built-ins and of functions written in Go: the most called first
}

// pathStep is a frame of the sample before: its function, where it was,
// and its node.
type pathStep struct {
	fn   *bytecode.Function
	pc   int
	node *profNode
}

// StartProfile profiles vm with p from now on, under the name interp,
// until StopProfile (PauseProfile and ResumeProfile leave out the time
// between).
func (vm *VM) StartProfile(p *Profiler, interp string) {
	s := &profState{p: p, name: interp, calls: map[*bytecode.Function]int64{}}
	s.funcs = append([]*bytecode.Function{vm.prog.Init}, vm.prog.Funcs...)
	s.counts = make([]int64, len(s.funcs))
	p.mu.Lock()
	s.root = p.roots[interp]
	if s.root == nil {
		s.root = &profNode{interp: interp}
		p.roots[interp] = s.root
		p.order = append(p.order, s.root)
		p.interps = append(p.interps, interp)
	}
	p.mu.Unlock()
	vm.prof, vm.watched = s, true
	vm.ResumeProfile()
}

// ResumeProfile goes on profiling vm after PauseProfile.
func (vm *VM) ResumeProfile() {
	s := vm.prof
	if s == nil {
		return
	}
	p := s.p
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running[s] {
		return
	}
	now := time.Now()
	s.last = now
	if p.start.IsZero() {
		p.start = now
	}
	p.running[s] = true
	if !p.ticking {
		p.ticking = true
		go p.tick()
	}
}

// PauseProfile stops profiling vm until ResumeProfile: for the Go API,
// whose calls are profiled but not the time between them. The call
// counts go to the Profiler. It is called on vm's goroutine.
func (vm *VM) PauseProfile() {
	s := vm.prof
	if s == nil {
		return
	}
	p := s.p
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.running[s] {
		return
	}
	delete(p.running, s)
	now := time.Now()
	s.pending += now.Sub(s.last)
	s.want.Store(false)
	p.end = now
	s.publish()
}

// StopProfile ends the profiling of vm, and gives its call counts to the
// Profiler. It is called on vm's goroutine, after it ran.
func (vm *VM) StopProfile() {
	vm.PauseProfile()
	vm.prof = nil
}

// tick asks the running interpreters for a sample every interval, until
// none runs.
func (p *Profiler) tick() {
	t := time.NewTicker(p.Interval)
	defer t.Stop()
	for range t.C {
		p.mu.Lock()
		if len(p.running) == 0 {
			p.ticking = false
			p.mu.Unlock()
			return
		}
		for s := range p.running {
			s.want.Store(true)
		}
		p.mu.Unlock()
	}
}

// publish adds the call counts of s to the Profiler's and starts them
// again from zero (p.mu is held).
func (s *profState) publish() {
	p := s.p
	for i, n := range s.counts {
		if n > 0 {
			p.calls[Calls{Interp: s.name, Func: s.funcs[i]}] += n
			s.counts[i] = 0
		}
	}
	for fn, n := range s.calls {
		p.calls[Calls{Interp: s.name, Func: fn}] += n
	}
	clear(s.calls)
	for i := range s.goCalls {
		c := &s.goCalls[i]
		if c.n == 0 {
			continue
		}
		if c.b != nil {
			p.calls[Calls{Interp: s.name, Go: c.b.Name}] += c.n
		} else {
			p.calls[Calls{Interp: s.name, Go: c.m.FullName()}] += c.n
		}
		c.n = 0
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

// WaitStart tells the VM that a Go function it called begins to wait (for
// time, a message, an event, input): see native.Wait. It gives the time
// for WaitEnd, or the zero time when the VM does not need it (it is not
// profiled with CPU). It is called on vm's goroutine.
func (vm *VM) WaitStart() time.Time {
	if s := vm.prof; s != nil && s.p.CPU {
		return time.Now()
	}
	return time.Time{}
}

// WaitEnd tells the VM that the wait WaitStart gave start for ended: with
// CPU, the next sample leaves its time out.
func (vm *VM) WaitEnd(start time.Time) {
	if s := vm.prof; s != nil && !start.IsZero() {
		s.waited.Add(int64(time.Since(start)))
	}
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

// The pc sample takes for the top frame: where the VM is (pc >= 0), the
// top frame's own ip (pcTop: it was saved), or the instruction before it
// (pcCaller: the top frame called a function, its ip is past the call).
const (
	pcTop    = -1
	pcCaller = -2
)

// sample records the call stack, as a sample weighing the time since the
// one before (CPU: less the time spent waiting in Go functions meanwhile).
// pc is where the top frame is (see pcTop). The stack is a path in the
// call tree: the frames that are as at the sample before keep their nodes,
// the others are looked up (or added), so that a stack seen before costs
// no memory.
func (vm *VM) sample(pc int) {
	s := vm.prof
	s.want.Store(false)
	now := time.Now()
	elapsed := s.pending + now.Sub(s.last)
	s.last, s.pending = now, 0
	weight := elapsed
	if s.p.CPU {
		weight = max(weight-time.Duration(s.waited.Swap(0)), 0)
	}
	p := s.p
	p.mu.Lock()
	defer p.mu.Unlock()
	s.publish()
	n := len(vm.frames)
	if n == 0 {
		return
	}
	node, same := s.root, true
	for i := range n {
		f := &vm.frames[i]
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
		if same && i < len(s.path) && s.path[i].fn == f.fn && s.path[i].pc == at {
			node = s.path[i].node
			continue
		}
		if same {
			same, s.path = false, s.path[:i]
		}
		k := nodeKey{node, f.fn, f.fn.LineAt(at)}
		child := p.nodes[k]
		if child == nil {
			child = &profNode{parent: node, fn: f.fn, line: k.line, interp: s.name}
			p.nodes[k] = child
			p.order = append(p.order, child)
		}
		node = child
		s.path = append(s.path, pathStep{f.fn, at, node})
	}
	s.path = s.path[:n]
	node.time += weight
	node.count++
	p.total += weight
	p.elapsed += elapsed
	p.nSamples++
}

// Node is a node of the call tree of the samples: the function Func at
// line Line, called from node Parent (an index into the nodes). A root
// (Parent -1, Func nil) is an interpreter. Time and Count are of the
// samples whose stack ended at the node.
type Node struct {
	Parent int
	Interp string
	Func   *bytecode.Function
	Line   int
	Time   time.Duration
	Count  int64
}

// Tree gives the call tree of the samples so far: each node after its
// parent.
func (p *Profiler) Tree() []Node {
	p.mu.Lock()
	defer p.mu.Unlock()
	index := make(map[*profNode]int, len(p.order))
	out := make([]Node, len(p.order))
	for i, n := range p.order {
		index[n] = i
		parent := -1
		if n.parent != nil {
			parent = index[n.parent]
		}
		out[i] = Node{Parent: parent, Interp: n.interp, Func: n.fn, Line: n.line, Time: n.time, Count: n.count}
	}
	return out
}

// Sample is the time of one call stack: the interpreter (as Interp names
// it), its frames outermost first, and the time (and the number of
// samples) that ended there.
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

// Samples gives the stacks of the samples so far (from the call tree).
func (p *Profiler) Samples() []Sample {
	tree := p.Tree()
	var out []Sample
	for i, n := range tree {
		if n.Count == 0 || n.Func == nil {
			continue
		}
		var stack []ProfFrame
		for j := i; tree[j].Func != nil; j = tree[j].Parent {
			stack = append(stack, ProfFrame{tree[j].Func, tree[j].Line})
		}
		slices.Reverse(stack)
		out = append(out, Sample{Interp: n.Interp, Stack: stack, Time: n.Time, Count: n.Count})
	}
	return out
}

// ProfileSummary sums up what a Profiler collected.
type ProfileSummary struct {
	Total    time.Duration // the time of the samples
	Elapsed  time.Duration // the wall time they cover (with CPU, also what was left out)
	Samples  int64
	Interps  []string      // the interpreters profiled, in the order they started
	Running  []string      // those that run: their calls are counted up to their latest sample
	Start    time.Time     // when the first interpreter started (zero: none did)
	Duration time.Duration // from Start to when the last one stopped (or now, while one runs)
}

// Summary sums up what p collected so far.
func (p *Profiler) Summary() ProfileSummary {
	p.mu.Lock()
	defer p.mu.Unlock()
	sum := ProfileSummary{Total: p.total, Elapsed: p.elapsed, Samples: p.nSamples, Interps: slices.Clone(p.interps), Start: p.start}
	for s := range p.running {
		sum.Running = append(sum.Running, s.name)
	}
	slices.Sort(sum.Running)
	if !p.start.IsZero() {
		end := p.end
		if len(p.running) > 0 {
			end = time.Now()
		}
		sum.Duration = end.Sub(p.start)
	}
	return sum
}

// Calls names what a call count counts: the calls in interpreter Interp of
// Kukel function Func, or (Func nil) of the built-in or Go function Go.
type Calls struct {
	Interp string
	Func   *bytecode.Function
	Go     string
}

// Calls gives the call counts: exact for the interpreters that stopped
// (or paused), up to their latest sample for those that run.
func (p *Profiler) Calls() map[Calls]int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[Calls]int64, len(p.calls))
	for k, n := range p.calls {
		out[k] = n
	}
	return out
}
