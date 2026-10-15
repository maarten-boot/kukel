package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/maarten-boot/kukel/internal/builtins"
	"github.com/maarten-boot/kukel/internal/bytecode"
	"github.com/maarten-boot/kukel/internal/value"
	"github.com/maarten-boot/kukel/internal/vm"
)

// kukel debug (TODO.md Q60): runs a program in a copy of its code with a
// BREAK at the start of every line (bytecode.Debuggable); the VM's debug
// hook decides there whether to stop and then reads commands, gdb-style.
// Only the main interpreter stops; workers run on.

const debugHelp = `commands:
  break file:line | line | function   stop there (b)
  delete [n]                          remove breakpoint n, or all (d)
  breaks                              list the breakpoints
  continue                            run to the next breakpoint (c)
  step                                to the next line, also into calls (s)
  next                                to the next line, over calls (n)
  finish                              until the current function returns (f)
  print path                          show a value: x, this->name, v[2], m["k"], util::count (p)
  locals                              the variables of the current frame
  backtrace                           the calls, innermost first (bt)
  frame n                             look at frame n of bt (for print and locals)
  list [line]                         the source around the line (l)
  quit                                end the program (q)
`

type breakpoint struct {
	id   int
	file string
	line int
}

// debugger is the state of a kukel debug session.
type debugger struct {
	in      *bufio.Reader
	out     *bufio.Writer // the program's output (flushed before the debugger writes)
	msg     io.Writer
	prog    *bytecode.Program
	mainFn  *bytecode.Function
	breaks  []breakpoint
	nextID  int
	mode    string // "start", "continue", "step", "next", "finish"
	depth   int    // the frame depth for next and finish
	frame   int    // the frame print and locals look at (an index into the frames)
	sources map[string][]string
}

// cmdDebug runs source files under the debugger: kukel debug a.kkl ... [-- args].
func cmdDebug(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	files, progArgs := args, []string(nil)
	for i, a := range args {
		if a == "--" {
			files, progArgs = args[:i], args[i+1:]
			break
		}
	}
	if len(files) == 0 {
		fmt.Fprintln(stderr, "usage: kukel debug a.kkl ... [-- args]")
		return 2
	}
	prog, err := compileFiles(files)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	prog = bytecode.Debuggable(prog)
	in := bufio.NewReader(stdin)
	out := bufio.NewWriter(stdout)
	defer out.Flush()
	machine, err := vm.NewWith(prog, builtins.Lookup, cliConfig(), out, in)
	if err != nil {
		fmt.Fprintln(stderr, "kukel:", err)
		return 1
	}
	machine.SetStderr(stderr)
	d := &debugger{in: in, out: out, msg: stdout, prog: prog, mode: "start", sources: map[string][]string{}}
	if g := prog.Globals[prog.Main]; g.Kind == bytecode.GlobalFunc {
		d.mainFn = prog.Funcs[g.Func]
	}
	machine.Debug = d.hook
	code, err := machine.Run(progArgs)
	out.Flush()
	if err != nil {
		fmt.Fprintln(stderr, err)
	}
	fmt.Fprintf(d.msg, "the program ended (exit code %d)\n", code)
	return code
}

// hook runs at the start of every line: it stops where the mode or a
// breakpoint says, and then reads commands until one resumes.
func (d *debugger) hook(m *vm.VM) error {
	frames := m.Frames()
	top := frames[len(frames)-1]
	stop := false
	switch d.mode {
	case "start":
		stop = top.Func == d.mainFn
	case "step":
		stop = true
	case "next":
		stop = len(frames) <= d.depth
	case "finish":
		stop = len(frames) < d.depth
	}
	hit := 0
	for _, b := range d.breaks {
		if b.line == top.Line && sameFile(top.Func.File, b.file) {
			stop, hit = true, b.id
		}
	}
	if !stop {
		return nil
	}
	d.out.Flush()
	d.frame = len(frames) - 1
	if hit > 0 {
		fmt.Fprintf(d.msg, "breakpoint %d, ", hit)
	}
	d.where(top)
	for {
		fmt.Fprint(d.msg, "(kdb) ")
		line, err := d.in.ReadString('\n')
		if err != nil && line == "" {
			fmt.Fprintln(d.msg)
			return &value.ExitError{Code: 0} // the end of the input: quit
		}
		cmd, arg, _ := strings.Cut(strings.TrimSpace(line), " ")
		arg = strings.TrimSpace(arg)
		switch cmd {
		case "":
		case "c", "continue":
			d.mode = "continue"
			return nil
		case "s", "step":
			d.mode = "step"
			return nil
		case "n", "next":
			d.mode, d.depth = "next", len(frames)
			return nil
		case "f", "finish":
			d.mode, d.depth = "finish", len(frames)
			return nil
		case "q", "quit":
			return &value.ExitError{Code: 0}
		case "b", "break":
			d.addBreak(arg, top)
		case "d", "delete":
			d.deleteBreak(arg)
		case "breaks":
			if len(d.breaks) == 0 {
				fmt.Fprintln(d.msg, "no breakpoints")
			}
			for _, b := range d.breaks {
				fmt.Fprintf(d.msg, "%d: %s:%d\n", b.id, b.file, b.line)
			}
		case "p", "print":
			v, err := d.eval(m, frames, arg)
			if err != nil {
				fmt.Fprintln(d.msg, err)
			} else {
				fmt.Fprintln(d.msg, v.Repr())
			}
		case "locals":
			vars := m.Vars(d.frame)
			if len(vars) == 0 {
				fmt.Fprintln(d.msg, "no variables")
			}
			for _, v := range vars {
				fmt.Fprintf(d.msg, "%s = %s\n", v.Name, v.Value.Repr())
			}
		case "bt", "backtrace":
			for i := len(frames) - 1; i >= 0; i-- {
				mark := " "
				if i == d.frame {
					mark = "*"
				}
				fmt.Fprintf(d.msg, "%s%d %s (%s:%d)\n", mark, len(frames)-1-i, frames[i].Func.FuncName, frames[i].Func.File, frames[i].Line)
			}
		case "frame":
			n, err := strconv.Atoi(arg)
			if err != nil || n < 0 || n >= len(frames) {
				fmt.Fprintf(d.msg, "no frame %q (0 to %d, as bt numbers them)\n", arg, len(frames)-1)
				continue
			}
			d.frame = len(frames) - 1 - n
			d.where(frames[d.frame])
		case "l", "list":
			fr := frames[d.frame]
			center := fr.Line
			if arg != "" {
				if n, err := strconv.Atoi(arg); err == nil {
					center = n
				}
			}
			d.list(fr.Func.File, center, fr.Line)
		case "h", "help":
			fmt.Fprint(d.msg, debugHelp)
		default:
			fmt.Fprintf(d.msg, "unknown command %q (help lists them)\n", cmd)
		}
	}
}

// where shows a frame's place and its source line.
func (d *debugger) where(f vm.DebugFrame) {
	fmt.Fprintf(d.msg, "%s at %s:%d\n", f.Func.FuncName, f.Func.File, f.Line)
	if text, ok := d.sourceLine(f.Func.File, f.Line); ok {
		fmt.Fprintf(d.msg, "%5d  %s\n", f.Line, text)
	}
}

func (d *debugger) list(file string, center, current int) {
	for l := max(1, center-3); l <= center+3; l++ {
		text, ok := d.sourceLine(file, l)
		if !ok {
			break
		}
		mark := " "
		if l == current {
			mark = ">"
		}
		fmt.Fprintf(d.msg, "%s%4d  %s\n", mark, l, text)
	}
}

func (d *debugger) sourceLine(file string, line int) (string, bool) {
	lines, ok := d.sources[file]
	if !ok {
		data, err := os.ReadFile(file)
		if err == nil {
			lines = strings.Split(string(data), "\n")
		}
		d.sources[file] = lines
	}
	if line < 1 || line > len(lines) {
		return "", false
	}
	return lines[line-1], true
}

// addBreak sets a breakpoint: file:line, a line of the current file, or a
// function's first line. A line without code moves to the next one with.
func (d *debugger) addBreak(arg string, top vm.DebugFrame) {
	file, line := top.Func.File, 0
	if f, l, ok := strings.Cut(arg, ":"); ok && !strings.Contains(l, ":") {
		n, err := strconv.Atoi(l)
		if err != nil {
			fmt.Fprintf(d.msg, "%q: the line must be a number\n", arg)
			return
		}
		file, line = f, n
	} else if n, err := strconv.Atoi(arg); err == nil {
		line = n
	} else {
		fn := d.function(arg)
		if fn == nil || len(fn.Lines) == 0 {
			fmt.Fprintf(d.msg, "no function %q\n", arg)
			return
		}
		file, line = fn.File, fn.Lines[0].Line
	}
	// The first line with code at or after line, in that file.
	best := 0
	for _, fn := range append([]*bytecode.Function{d.prog.Init}, d.prog.Funcs...) {
		if !sameFile(fn.File, file) {
			continue
		}
		file = fn.File
		for _, l := range fn.Lines {
			if l.Line >= line && (best == 0 || l.Line < best) {
				best = l.Line
			}
		}
	}
	if best == 0 {
		fmt.Fprintf(d.msg, "no code at or after %s:%d\n", file, line)
		return
	}
	if best != line && line != 0 {
		fmt.Fprintf(d.msg, "(no code on line %d: the next is %d)\n", line, best)
	}
	d.nextID++
	d.breaks = append(d.breaks, breakpoint{d.nextID, file, best})
	fmt.Fprintf(d.msg, "breakpoint %d at %s:%d\n", d.nextID, file, best)
}

func (d *debugger) deleteBreak(arg string) {
	if arg == "" {
		d.breaks = nil
		fmt.Fprintln(d.msg, "all breakpoints deleted")
		return
	}
	n, _ := strconv.Atoi(arg)
	for i, b := range d.breaks {
		if b.id == n {
			d.breaks = append(d.breaks[:i], d.breaks[i+1:]...)
			fmt.Fprintf(d.msg, "breakpoint %d deleted\n", n)
			return
		}
	}
	fmt.Fprintf(d.msg, "no breakpoint %q\n", arg)
}

// function finds a function by its name ("main", "Shape::area", "util::f").
func (d *debugger) function(name string) *bytecode.Function {
	for _, fn := range d.prog.Funcs {
		if fn.FuncName == name {
			return fn
		}
	}
	return nil
}

// sameFile tells whether the file of a function is the one a breakpoint
// names: the same path, or the same name.
func sameFile(file, name string) bool {
	return file == name || filepath.Base(file) == name || strings.HasSuffix(file, string(filepath.Separator)+name) ||
		filepath.Clean(file) == filepath.Clean(name)
}

// eval reads a path: a variable of the frame (or this, a data member of
// this, a global such as util::count), then ->member and [index] steps
// with int or string indexes. Reading only: nothing runs.
func (d *debugger) eval(m *vm.VM, frames []vm.DebugFrame, path string) (value.Value, error) {
	path = strings.TrimSpace(path)
	i := 0
	for i < len(path) && (isIdentByte(path[i]) || i+1 < len(path) && path[i] == ':' && path[i+1] == ':' || i > 0 && path[i] == ':' && path[i-1] == ':') {
		i++
	}
	name, rest := path[:i], path[i:]
	if name == "" {
		return value.NilValue, fmt.Errorf("print what? (a name, such as x or this->name)")
	}
	v, ok := d.lookup(m, frames[d.frame], name)
	if !ok {
		return value.NilValue, fmt.Errorf("no variable %s here", name)
	}
	for rest = strings.TrimSpace(rest); rest != ""; rest = strings.TrimSpace(rest) {
		switch {
		case strings.HasPrefix(rest, "->"):
			j := 2
			for j < len(rest) && isIdentByte(rest[j]) {
				j++
			}
			member, err := value.Member(v, rest[2:j], value.MakeFunction)
			if err != nil {
				return value.NilValue, err
			}
			v, rest = member, rest[j:]
		case strings.HasPrefix(rest, "["):
			j := strings.IndexByte(rest, ']')
			if j < 0 {
				return value.NilValue, fmt.Errorf("a [ without ]")
			}
			key, err := literal(strings.TrimSpace(rest[1:j]))
			if err != nil {
				return value.NilValue, err
			}
			x, err := value.Index(v, key)
			if err != nil {
				return value.NilValue, err
			}
			v, rest = x, rest[j+1:]
		default:
			return value.NilValue, fmt.Errorf("unexpected %q (paths are name, ->member and [index])", rest)
		}
	}
	return v, nil
}

func (d *debugger) lookup(m *vm.VM, f vm.DebugFrame, name string) (value.Value, bool) {
	if name == "this" {
		if f.This == nil {
			return value.NilValue, false
		}
		return value.ObjectValue(f.This), true
	}
	vars := m.Vars(d.frame)
	for i := len(vars) - 1; i >= 0; i-- {
		if vars[i].Name == name {
			return vars[i].Value, true
		}
	}
	if f.This != nil {
		if slot, ok := f.This.Class.FieldIndex[name]; ok {
			return f.This.Fields[slot], true
		}
	}
	return m.Global(name)
}

// literal reads an index: an int, a float or a "string".
func literal(s string) (value.Value, error) {
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return value.MakeInt(n), nil
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return value.MakeFloat(f), nil
	}
	if u, err := strconv.Unquote(s); err == nil && strings.HasPrefix(s, `"`) {
		return value.MakeString(u), nil
	}
	return value.NilValue, fmt.Errorf("an index must be a number or a \"string\", not %s", s)
}

func isIdentByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
