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

// lineKey is a line of a program: programs that have a file of the same
// name keep their lines apart.
type lineKey struct {
	program string
	file    string
	line    int
}

// Source gives the text of a line of fn's file, and names the program fn
// is of ("" when there is only one, or it is not known): the interpreter
// that runs it.
type Source func(fn *bytecode.Function, line int) (text, program string)

// Files is the Source that reads the files (once each).
func Files() Source {
	src := debugger.Sources{}
	return func(fn *bytecode.Function, line int) (string, string) {
		text, _ := src.Line(fn.File, line)
		return text, ""
	}
}

// Report writes the text report of what p collected: the functions by
// their own time (and their total time, with the functions they call, and
// their calls), the slowest lines with their source, and the calls of
// built-ins and Go functions. src gives the source lines (nil: Files()).
func Report(w io.Writer, p *vm.Profiler, src Source) error {
	if src == nil {
		src = Files()
	}
	tree := p.Tree()
	sum := p.Summary()
	calls := p.Calls()
	mark := marked(sum, calls)

	// The time under each node (sub), by interpreter, function and line.
	sub := make([]time.Duration, len(tree))
	for i := len(tree) - 1; i >= 0; i-- {
		sub[i] += tree[i].Time
		if tree[i].Parent >= 0 {
			sub[tree[i].Parent] += sub[i]
		}
	}
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
	texts := map[lineKey]string{}
	interps := map[string]time.Duration{}
	for i, n := range tree {
		if n.Func == nil {
			interps[n.Interp] += sub[i]
			continue
		}
		if n.Count > 0 {
			row(fnKey{n.Interp, n.Func}).own += n.Time
			text, program := src(n.Func, n.Line)
			k := lineKey{program, n.Func.File, n.Line}
			lines[k] += n.Time
			texts[k] = text
		}
	}
	// A function's total time: the time under its outermost nodes (a
	// recursive function counts once in a stack).
	for i, t := range totals(tree, sub) {
		if t > 0 {
			row(fnKey{tree[i].Interp, tree[i].Func}).total += t
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
	total := sum.Total

	b := &strings.Builder{}
	if sum.Samples == 0 {
		fmt.Fprintf(b, "No samples: what was profiled ran for less than one interval (%v).\n", p.Interval)
	} else {
		kind := "wall time, with waiting"
		if p.CPU {
			kind = "running time: waiting left out"
		}
		every := sum.Elapsed / time.Duration(sum.Samples)
		fmt.Fprintf(b, "%s in %d samples, one every %.2f ms on average (%s).\n", seconds(total), sum.Samples,
			float64(every)/float64(time.Millisecond), kind)
		fmt.Fprintln(b, "The time of built-ins and Go functions counts on the line that calls them.")
	}
	if mark {
		fmt.Fprintf(b, "By interpreter (the percentages are of their time together; %s from the start of the first to the end of the last):\n",
			seconds(sum.Duration))
		for _, n := range slices.Sorted(maps.Keys(interps)) {
			fmt.Fprintf(b, "  %-24s %s\n", n, seconds(interps[n]))
		}
	}
	for _, n := range sum.Running {
		fmt.Fprintf(b, "%s still runs: its calls are counted up to its latest sample.\n", n)
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
	if len(list) > 0 {
		fmt.Fprintln(b, "\nFunctions, by their own time (total: with the functions they call):")
		fmt.Fprintln(b, "        own      %      total      %       calls  function")
		for i, r := range list {
			if i == MaxFunctions {
				fmt.Fprintf(b, "  ... %d more\n", len(list)-i)
				break
			}
			where := ""
			if mark {
				where = " [" + r.interp + "]"
			}
			fmt.Fprintf(b, "  %9s %6s  %9s %6s  %10d  %s (%s:%d)%s\n", seconds(r.own), percent(r.own, total),
				seconds(r.total), percent(r.total, total), r.calls, r.fn.FuncName, r.fn.File, r.fn.Line, where)
		}
	}

	// Lines.
	keys := make([]lineKey, 0, len(lines))
	for k := range lines {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b lineKey) int {
		return cmp.Or(cmp.Compare(lines[b], lines[a]), cmp.Compare(a.file, b.file), cmp.Compare(a.line, b.line),
			cmp.Compare(a.program, b.program))
	})
	programs := map[string]map[string]bool{} // the programs that have a file of that name
	for k := range lines {
		if programs[k.file] == nil {
			programs[k.file] = map[string]bool{}
		}
		programs[k.file][k.program] = true
	}
	if len(keys) > 0 {
		fmt.Fprintf(b, "\nLines, by their own time (the %d slowest):\n", min(MaxLines, len(keys)))
		fmt.Fprintln(b, "        own      %  line")
		for i, k := range keys {
			if i == MaxLines {
				break
			}
			where := fmt.Sprintf("%s:%d", k.file, k.line)
			if len(programs[k.file]) > 1 && k.program != "" {
				where += " [" + k.program + "]" // two programs have a file of this name
			}
			row := fmt.Sprintf("  %9s %6s  %s  %s", seconds(lines[k]), percent(lines[k], total), where, strings.TrimSpace(texts[k]))
			fmt.Fprintln(b, strings.TrimRight(row, " "))
		}
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
			if mark {
				where = " [" + k.Interp + "]"
			}
			fmt.Fprintf(b, "  %10d  %s%s\n", calls[k], k.Go, where)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// marked tells whether the report names the interpreter of each function:
// when one is not "main" (a worker, or a second interpreter of the Go
// API), whether it has samples or only calls.
func marked(sum vm.ProfileSummary, calls map[vm.Calls]int64) bool {
	for _, n := range sum.Interps {
		if n != "main" {
			return true
		}
	}
	for k := range calls {
		if k.Interp != "main" {
			return true
		}
	}
	return false
}

// totals gives for each node of tree the time under it (from sub) if it is
// the outermost node of its function on its path, else 0: the sum over a
// function's nodes is its total time, in which recursion counts once. It
// walks the tree depth first, counting the nodes of each function on the
// path.
func totals(tree []vm.Node, sub []time.Duration) []time.Duration {
	out := make([]time.Duration, len(tree))
	first := make([]int, len(tree)) // first child, +1 (0: none)
	next := make([]int, len(tree))  // next sibling, +1
	for i := len(tree) - 1; i >= 0; i-- {
		if q := tree[i].Parent; q >= 0 {
			next[i], first[q] = first[q], i+1
		}
	}
	onPath := map[*bytecode.Function]int{}
	type visit struct {
		node int
		exit bool
	}
	var stack []visit
	for i, n := range tree {
		if n.Parent < 0 {
			stack = append(stack, visit{node: i})
		}
	}
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		fn := tree[v.node].Func
		if v.exit {
			onPath[fn]--
			continue
		}
		if fn != nil {
			if onPath[fn] == 0 {
				out[v.node] = sub[v.node]
			}
			onPath[fn]++
			stack = append(stack, visit{v.node, true})
		}
		for c := first[v.node]; c > 0; c = next[c-1] {
			stack = append(stack, visit{node: c - 1})
		}
	}
	return out
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
