package kukel

import (
	"fmt"
	"io"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/maarten-boot/kukel/internal/debugger"
	"github.com/maarten-boot/kukel/internal/profile"
	"github.com/maarten-boot/kukel/internal/vm"
)

// Profile collects where the time of interpreters' calls goes: which
// functions and lines of the program, with their calls (also of workers).
// Give it to New in Options.Profile; then the calls of that interpreter
// from Go (Run, Call, ...) are profiled, and WriteReport and WritePprof
// show what the finished calls gave. Several interpreters may share a
// Profile (the first is "main", the others "interp 2", ...). Its methods
// are safe for concurrent use.
type Profile struct {
	p      *vm.Profiler
	mu     sync.Mutex
	n      int              // the interpreters that use it
	source debugger.Sources // the programs' source given to CompileSource
}

// ProfileOptions configure a Profile.
type ProfileOptions struct {
	// Interval is the time between samples (0: a millisecond).
	Interval time.Duration
	// CPU leaves waiting out: by default each sample weighs the time since
	// the one before, so that time::sleep, events::run(), receive, ...
	// show on the line that waits.
	CPU bool
}

// NewProfile makes a Profile.
func NewProfile(opts ProfileOptions) *Profile {
	return &Profile{p: vm.NewProfiler(opts.Interval, opts.CPU), source: debugger.Sources{}}
}

// use names the next interpreter that uses p, which runs prog.
func (p *Profile) use(prog *Program) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	for name, src := range prog.source {
		p.source[name] = strings.Split(strings.TrimSuffix(src, "\n"), "\n")
	}
	p.n++
	if p.n == 1 {
		return "main"
	}
	return fmt.Sprintf("interp %d", p.n)
}

// WriteReport writes the text report kukel profile writes: the functions
// by their own time and their total time (with what they call), with their
// calls; the slowest lines with their source; the calls of built-ins and Go
// functions.
func (p *Profile) WriteReport(w io.Writer) error {
	p.mu.Lock()
	src := maps.Clone(p.source)
	p.mu.Unlock()
	return profile.Report(w, p.p, src)
}

// WritePprof writes the profile in Go's pprof format, for go tool pprof
// (flame graphs, call graphs, the source with the time of each line).
func (p *Profile) WritePprof(w io.Writer) error { return profile.Pprof(w, p.p) }
