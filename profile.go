package kukel

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/maarten-boot/kukel/internal/bytecode"
	"github.com/maarten-boot/kukel/internal/debugger"
	"github.com/maarten-boot/kukel/internal/profile"
	"github.com/maarten-boot/kukel/internal/vm"
)

// Profile collects where the time of interpreters' calls goes: which
// functions and lines of the program, with their calls (also of workers).
// Give it to New in Options.Profile; then the calls of that interpreter
// from Go (Run, Call, ...) are profiled, and WriteReport and WritePprof
// show what they gave (of a call or worker still running: its samples so
// far, its calls up to its latest sample). Several interpreters may share
// a Profile (the first is "main", the others "interp 2", ...); lines of
// their programs stay apart also when two have a file of the same name
// (the report then names the interpreter). Its methods are safe for
// concurrent use.
type Profile struct {
	p        *vm.Profiler
	mu       sync.Mutex
	n        int                        // the interpreters that use it
	programs []profiled                 // the programs they run
	of       map[*bytecode.Function]int // the program of each function (an index in programs)
	files    debugger.Sources           // the files read for programs without their source
}

// profiled is a program of a Profile: the interpreter that ran it first,
// and the source given to CompileSource (nil for a program of files).
type profiled struct {
	interp string
	source debugger.Sources
}

// ProfileOptions configure a Profile.
type ProfileOptions struct {
	// Interval is the time between samples (0 or less: a millisecond).
	Interval time.Duration
	// CPU leaves out the time spent waiting in the standard Go functions
	// that wait (time::sleep, events::run(), receive, readline, ...): by
	// default each sample weighs the time since the one before, so that
	// waiting shows on the line that waits. The time of the Go functions
	// of your modules counts in full, also when they wait.
	CPU bool
}

// NewProfile makes a Profile.
func NewProfile(opts ProfileOptions) *Profile {
	return &Profile{p: vm.NewProfiler(opts.Interval, opts.CPU), of: map[*bytecode.Function]int{}, files: debugger.Sources{}}
}

// use names the next interpreter that uses p, which runs prog.
func (p *Profile) use(prog *Program) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.n++
	name := "main"
	if p.n > 1 {
		name = fmt.Sprintf("interp %d", p.n)
	}
	var source debugger.Sources
	if prog.source != nil {
		source = debugger.Sources{}
		for file, src := range prog.source {
			source[file] = strings.Split(strings.TrimSuffix(src, "\n"), "\n")
		}
	}
	i := len(p.programs)
	p.programs = append(p.programs, profiled{name, source})
	for _, fn := range append([]*bytecode.Function{prog.prog.Init}, prog.prog.Funcs...) {
		if _, ok := p.of[fn]; !ok { // a program that runs in two interpreters: the first
			p.of[fn] = i
		}
	}
	return name
}

// source is the report's Source: the line from the source of fn's program,
// else from its file.
func (p *Profile) source(fn *bytecode.Function, line int) (string, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	i, ok := p.of[fn]
	if !ok { // a program a worker compiled (worker::startfile)
		text, _ := p.files.Line(fn.File, line)
		return text, ""
	}
	pr := p.programs[i]
	if pr.source != nil {
		if _, ok := pr.source[fn.File]; ok {
			text, _ := pr.source.Line(fn.File, line)
			return text, pr.interp
		}
	}
	text, _ := p.files.Line(fn.File, line)
	return text, pr.interp
}

// WriteReport writes the text report kukel profile writes: the functions
// by their own time and their total time (with what they call), with their
// calls; the slowest lines with their source; the calls of built-ins and Go
// functions.
func (p *Profile) WriteReport(w io.Writer) error {
	return profile.Report(w, p.p, p.source)
}

// WritePprof writes the profile in Go's pprof format, for go tool pprof
// (flame graphs, call graphs, the source with the time of each line).
func (p *Profile) WritePprof(w io.Writer) error { return profile.Pprof(w, p.p) }
