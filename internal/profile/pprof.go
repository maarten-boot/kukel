package profile

import (
	"io"
	"path/filepath"
	"strings"

	"github.com/google/pprof/profile"

	"github.com/maarten-boot/kukel/internal/bytecode"
	"github.com/maarten-boot/kukel/internal/vm"
)

// Pprof writes what p collected in Go's pprof format (gzipped protocol
// buffers), for go tool pprof: a function per Kukel function (with its file
// and first line, so that pprof can show the source), a location per line
// of it, and per sample the number of samples and the time (wall or cpu,
// in nanoseconds). Each sample has the label "interpreter" ("main",
// "worker 1: f"); with workers, each stack starts with a frame named after
// its interpreter, so that a flame graph shows them apart.
func Pprof(w io.Writer, p *vm.Profiler) error {
	samples := p.Samples()
	sum := p.Summary()
	kind := "wall"
	if p.CPU {
		kind = "cpu"
	}
	prof := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "samples", Unit: "count"},
			{Type: kind, Unit: "nanoseconds"},
		},
		DefaultSampleType: kind,
		PeriodType:        &profile.ValueType{Type: kind, Unit: "nanoseconds"},
		Period:            p.Interval.Nanoseconds(),
		DurationNanos:     sum.Duration.Nanoseconds(), // wall time, from the start of the first interpreter to the end of the last
	}
	if !sum.Start.IsZero() {
		prof.TimeNanos = sum.Start.UnixNano()
	}
	// One mapping, "kukel", that has its functions, files and lines already:
	// pprof then looks for no binary to read them from.
	mapping := &profile.Mapping{ID: 1, File: "kukel", HasFunctions: true, HasFilenames: true, HasLineNumbers: true}
	prof.Mapping = []*profile.Mapping{mapping}
	mark := marked(sum, p.Calls())
	funcs := map[*bytecode.Function]*profile.Function{}
	function := func(fn *bytecode.Function) *profile.Function {
		f := funcs[fn]
		if f == nil {
			file := fn.File
			if abs, err := filepath.Abs(file); err == nil && !strings.HasPrefix(file, "<") { // not <prelude>
				file = abs
			}
			f = &profile.Function{ID: uint64(len(prof.Function) + 1), Name: fn.FuncName, SystemName: fn.FuncName,
				Filename: file, StartLine: int64(fn.Line)}
			funcs[fn] = f
			prof.Function = append(prof.Function, f)
		}
		return f
	}
	type lineKey struct {
		fn   *bytecode.Function
		line int
	}
	locs := map[lineKey]*profile.Location{}
	location := func(fn *bytecode.Function, line int) *profile.Location {
		k := lineKey{fn, line}
		l := locs[k]
		if l == nil {
			l = &profile.Location{ID: uint64(len(prof.Location) + 1), Mapping: mapping,
				Line: []profile.Line{{Function: function(fn), Line: int64(line)}}}
			locs[k] = l
			prof.Location = append(prof.Location, l)
		}
		return l
	}
	roots := map[string]*profile.Location{} // the frames named after the interpreters
	root := func(interp string) *profile.Location {
		l := roots[interp]
		if l == nil {
			f := &profile.Function{ID: uint64(len(prof.Function) + 1), Name: "[" + interp + "]", SystemName: "[" + interp + "]"}
			prof.Function = append(prof.Function, f)
			l = &profile.Location{ID: uint64(len(prof.Location) + 1), Mapping: mapping, Line: []profile.Line{{Function: f}}}
			roots[interp] = l
			prof.Location = append(prof.Location, l)
		}
		return l
	}
	for _, s := range samples {
		var stack []*profile.Location // the innermost frame first, as pprof has it
		for i := len(s.Stack) - 1; i >= 0; i-- {
			stack = append(stack, location(s.Stack[i].Func, s.Stack[i].Line))
		}
		if mark {
			stack = append(stack, root(s.Interp))
		}
		prof.Sample = append(prof.Sample, &profile.Sample{
			Location: stack,
			Value:    []int64{s.Count, s.Time.Nanoseconds()},
			Label:    map[string][]string{"interpreter": {s.Interp}},
		})
	}
	if err := prof.CheckValid(); err != nil {
		return err
	}
	return prof.Write(w)
}
