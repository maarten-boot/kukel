// Package profile turns what a vm.Profiler collected into reports
// (TODO.md Q78): a text report of the functions and lines that took the
// time, for kukel profile and the Go API.
package profile

import (
	"cmp"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/maarten-boot/kukel/internal/bytecode"
	"github.com/maarten-boot/kukel/internal/debugger"
	"github.com/maarten-boot/kukel/internal/vm"
)

// MaxFunctions and MaxLines bound the rows of the report's tables.
const (
	MaxFunctions = 40
	MaxLines     = 20
)

// fnKey is a function in an interpreter.
type fnKey struct {
	interp string
	fn     *bytecode.Function
}

type fnRow struct {
	fnKey
	own, total time.Duration
	calls      int64
}

type lineKey struct {
	file string
	line int
}

// Report writes the text report of what p collected: the functions by
// their own time (and their total time, with the functions they call, and
// their calls), the slowest lines with their source, and the calls of
// built-ins and Go functions. src has the source lines of the program
// (files not in it are read; nil: all are read).
func Report(w io.Writer, p *vm.Profiler, src debugger.Sources) error {
	if src == nil {
		src = debugger.Sources{}
	}
	samples, total := p.Samples()
	calls := p.Calls()
	var count int64
	rows := map[fnKey]*fnRow{}
	row := func(k fnKey) *fnRow {
		r := rows[k]
		if r == nil {
			r = &fnRow{fnKey: k}
			rows[k] = r
		}
		return r
	}
	lines := map[lineKey]time.Duration{}
	interps := map[string]time.Duration{}
	for _, s := range samples {
		count += s.Count
		interps[s.Interp] += s.Time
		top := s.Stack[len(s.Stack)-1]
		row(fnKey{s.Interp, top.Func}).own += s.Time
		lines[lineKey{top.Func.File, top.Line}] += s.Time
		seen := map[*bytecode.Function]bool{} // a recursive function counts once
		for _, f := range s.Stack {
			if !seen[f.Func] {
				seen[f.Func] = true
				row(fnKey{s.Interp, f.Func}).total += s.Time
			}
		}
	}
	var goCalls []vm.Calls
	for k, n := range calls {
		if k.Func != nil {
			row(fnKey{k.Interp, k.Func}).calls = n
		} else {
			goCalls = append(goCalls, k)
		}
	}

	b := &strings.Builder{}
	kind := "wall time, with waiting"
	if p.CPU {
		kind = "running time (-cpu: waiting left out)"
	}
	fmt.Fprintf(b, "%s in %d samples, one every %v (%s).\n", seconds(total), count, p.Interval, kind)
	fmt.Fprintln(b, "The time of built-ins and Go functions counts on the line that calls them.")
	if len(interps) > 1 {
		names := slices.Sorted(maps.Keys(interps))
		for _, n := range names {
			fmt.Fprintf(b, "  %-24s %s\n", n, seconds(interps[n]))
		}
	}

	// Functions.
	list := make([]*fnRow, 0, len(rows))
	for _, r := range rows {
		if r.total == 0 && r.fn.FuncName == "<init>" {
			continue // global initializers that took no time: every program has them
		}
		list = append(list, r)
	}
	slices.SortFunc(list, func(a, b *fnRow) int {
		return cmp.Or(cmp.Compare(b.own, a.own), cmp.Compare(b.total, a.total), cmp.Compare(b.calls, a.calls),
			cmp.Compare(a.interp, b.interp), cmp.Compare(a.fn.FuncName, b.fn.FuncName))
	})
	fmt.Fprintln(b, "\nFunctions, by their own time (total: with the functions they call):")
	fmt.Fprintln(b, "        own      %      total      %       calls  function")
	for i, r := range list {
		if i == MaxFunctions {
			fmt.Fprintf(b, "  ... %d more\n", len(list)-i)
			break
		}
		where := ""
		if len(interps) > 1 {
			where = " [" + r.interp + "]"
		}
		fmt.Fprintf(b, "  %9s %6s  %9s %6s  %10d  %s (%s:%d)%s\n", seconds(r.own), percent(r.own, total),
			seconds(r.total), percent(r.total, total), r.calls, r.fn.FuncName, r.fn.File, r.fn.Line, where)
	}

	// Lines.
	keys := make([]lineKey, 0, len(lines))
	for k := range lines {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b lineKey) int {
		return cmp.Or(cmp.Compare(lines[b], lines[a]), cmp.Compare(a.file, b.file), cmp.Compare(a.line, b.line))
	})
	fmt.Fprintf(b, "\nLines, by their own time (the %d slowest):\n", min(MaxLines, len(keys)))
	fmt.Fprintln(b, "        own      %  line")
	for i, k := range keys {
		if i == MaxLines {
			break
		}
		text, _ := src.Line(k.file, k.line)
		row := fmt.Sprintf("  %9s %6s  %s:%d  %s", seconds(lines[k]), percent(lines[k], total), k.file, k.line, strings.TrimSpace(text))
		fmt.Fprintln(b, strings.TrimRight(row, " "))
	}

	// Built-ins and Go functions.
	if len(goCalls) > 0 {
		slices.SortFunc(goCalls, func(a, b vm.Calls) int {
			return cmp.Or(cmp.Compare(calls[b], calls[a]), cmp.Compare(a.Interp, b.Interp), cmp.Compare(a.Go, b.Go))
		})
		fmt.Fprintln(b, "\nBuilt-ins and Go functions, by calls:")
		fmt.Fprintln(b, "       calls  function")
		for i, k := range goCalls {
			if i == MaxFunctions {
				fmt.Fprintf(b, "  ... %d more\n", len(goCalls)-i)
				break
			}
			where := ""
			if len(interps) > 1 {
				where = " [" + k.Interp + "]"
			}
			fmt.Fprintf(b, "  %10d  %s%s\n", calls[k], k.Go, where)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// seconds shows d in seconds, with three decimals ("1.234 s").
func seconds(d time.Duration) string { return fmt.Sprintf("%.3f s", d.Seconds()) }

// percent shows d as a part of total ("12.5%").
func percent(d, total time.Duration) string {
	if total <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f%%", 100*float64(d)/float64(total))
}
