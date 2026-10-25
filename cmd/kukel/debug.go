package main

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/maarten-boot/kukel/internal/builtins"
	"github.com/maarten-boot/kukel/internal/bytecode"
	"github.com/maarten-boot/kukel/internal/debugger"
	"github.com/maarten-boot/kukel/internal/value"
	"github.com/maarten-boot/kukel/internal/vm"
)

// kukel debug (TODO.md Q60): the terminal front end of package debugger,
// gdb-style: where the program stops, it reads commands until one lets it
// go on.

const debugHelp = `commands:
  break file:line | line | function [if condition]
                                      stop there (b); with a condition, only when it holds
  delete [n]                          remove breakpoint n, or all (d)
  breaks                              list the breakpoints
  continue                            run to the next breakpoint (c)
  step                                to the next line, also into calls (s)
  next                                to the next line, over calls (n)
  finish                              until the current function returns (f)
  print expression                    show a value: x, this->name, v[i] + 1, util::count, C::x (p);
                                      calls, assignments and ++ are not evaluated
  locals                              the variables of the current frame
  backtrace                           the calls, innermost first (bt)
  frame n                             look at frame n of bt (for print, locals and list)
  list [line]                         the source around the line (l)
  quit                                end the program, exit code 0 (q)
  help                                these commands (h)
`

// cliFront is the state of a kukel debug session.
type cliFront struct {
	s       *debugger.Session
	in      *bufio.Reader
	m       *vm.VM // its output is flushed before the debugger writes
	msg     io.Writer
	frame   int // the frame print and locals look at (an index into the frames)
	sources debugger.Sources
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
	if badFiles("debug", files, "kukel debug a.kkl ... [-- args]", stderr) {
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
	d := &cliFront{in: in, m: machine, msg: stdout, sources: debugger.Sources{}}
	d.s = debugger.New(prog, d, true)
	d.s.Attach(machine)
	code, err := machine.Run(progArgs)
	out.Flush()
	if err != nil {
		fmt.Fprintln(stderr, err)
	}
	fmt.Fprintf(d.msg, "the program ended (exit code %d)\n", code)
	return code
}

// Stopped shows where the program stopped and why, and reads commands
// until one lets it go on. At an exception nothing catches, any command
// that resumes ends the program.
func (d *cliFront) Stopped(m *vm.VM, st debugger.Stop) error {
	m.FlushOutput()
	frames := m.Frames()
	d.frame = len(frames) - 1
	switch {
	case st.Reason == "exception":
		what := st.Thrown.Kind
		if st.Thrown.Msg != "" {
			what += ": " + st.Thrown.Msg
		}
		fmt.Fprintf(d.msg, "uncaught %s (continuing ends the program)\n", what)
	case len(st.Breaks) > 0:
		fmt.Fprintf(d.msg, "breakpoint %d, ", st.Breaks[len(st.Breaks)-1])
	}
	d.where(frames[d.frame])
	return d.commands(m, frames)
}

// Log shows a logpoint's message (kukel debug sets none) or why a
// condition failed.
func (d *cliFront) Log(msg string) {
	d.m.FlushOutput()
	fmt.Fprintln(d.msg, msg)
}

// commands reads commands until one resumes the program.
func (d *cliFront) commands(m *vm.VM, frames []vm.DebugFrame) error {
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
			d.s.Continue()
			return nil
		case "s", "step":
			d.s.Step()
			return nil
		case "n", "next":
			d.s.Next(len(frames))
			return nil
		case "f", "finish":
			d.s.Finish(len(frames))
			return nil
		case "q", "quit":
			return &value.ExitError{Code: 0}
		case "b", "break":
			d.addBreak(arg, frames[d.frame])
		case "d", "delete":
			d.deleteBreak(arg)
		case "breaks":
			breaks := d.s.Breaks()
			if len(breaks) == 0 {
				fmt.Fprintln(d.msg, "no breakpoints")
			}
			for _, b := range breaks {
				fmt.Fprintf(d.msg, "%d: ", b.ID)
				if b.Func != "" {
					fmt.Fprintf(d.msg, "%s at ", b.Func)
				}
				fmt.Fprintf(d.msg, "%s:%d", b.File, b.Line)
				if b.Cond != "" {
					fmt.Fprintf(d.msg, " if %s", b.Cond)
				}
				fmt.Fprintln(d.msg)
			}
		case "p", "print":
			v, err := debugger.Eval(m, d.frame, arg)
			if err != nil {
				fmt.Fprintln(d.msg, err)
			} else {
				fmt.Fprintln(d.msg, debugger.Show(v))
			}
		case "locals":
			vars := m.Vars(d.frame)
			if this := frames[d.frame].This; this != nil {
				vars = append([]vm.DebugVar{{Name: "this", Value: value.ObjectValue(this)}}, vars...)
			}
			if len(vars) == 0 {
				fmt.Fprintln(d.msg, "no variables")
			}
			for _, v := range vars {
				fmt.Fprintf(d.msg, "%s = %s\n", v.Name, debugger.Show(v.Value))
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
func (d *cliFront) where(f vm.DebugFrame) {
	fmt.Fprintf(d.msg, "%s at %s:%d\n", f.Func.FuncName, f.Func.File, f.Line)
	if text, ok := d.sources.Line(f.Func.File, f.Line); ok {
		fmt.Fprintf(d.msg, "%5d  %s\n", f.Line, text)
	}
}

func (d *cliFront) list(file string, center, current int) {
	if _, ok := d.sources.Line(file, max(1, center-3)); !ok || center < 1 {
		fmt.Fprintf(d.msg, "no line %d in %s (it has %d)\n", center, file, d.sources.Count(file))
		return
	}
	for l := max(1, center-3); l <= center+3; l++ {
		text, ok := d.sources.Line(file, l)
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

// addBreak sets a breakpoint: file:line, a line of the current file, or a
// function's first line, with "if condition" after it for a conditional
// one. A line without code moves to the next one with.
func (d *cliFront) addBreak(arg string, fr vm.DebugFrame) {
	where, cond, hasIf := strings.Cut(arg+" ", " if ")
	where, cond = strings.TrimSpace(where), strings.TrimSpace(cond)
	if hasIf && cond == "" {
		fmt.Fprintln(d.msg, "break if what? (a condition after if, such as i == 3)")
		return
	}
	b := debugger.Breakpoint{File: fr.Func.File, Cond: cond} // a line alone: of the selected frame's file
	if f, l, ok := strings.Cut(where, ":"); ok && !strings.Contains(l, ":") {
		n, err := strconv.Atoi(l)
		if err != nil {
			fmt.Fprintf(d.msg, "%q: the line must be a number\n", where)
			return
		}
		b.File, b.Line = f, n
	} else if n, err := strconv.Atoi(where); err == nil {
		b.Line = n
	} else if where == "" {
		fmt.Fprintln(d.msg, "break where? (file:line, line or function)")
		return
	} else {
		b.Func = where
	}
	bp, note, err := d.s.SetBreak(b)
	if err != nil {
		fmt.Fprintln(d.msg, err)
		return
	}
	if note != "" {
		fmt.Fprintln(d.msg, note)
	}
	fmt.Fprintf(d.msg, "breakpoint %d at %s:%d\n", bp.ID, bp.File, bp.Line)
}

func (d *cliFront) deleteBreak(arg string) {
	if arg == "" {
		d.s.Clear(func(*debugger.Breakpoint) bool { return true })
		fmt.Fprintln(d.msg, "all breakpoints deleted")
		return
	}
	n, _ := strconv.Atoi(arg)
	found := false
	d.s.Clear(func(b *debugger.Breakpoint) bool {
		found = found || b.ID == n
		return b.ID == n
	})
	if found {
		fmt.Fprintf(d.msg, "breakpoint %d deleted\n", n)
		return
	}
	fmt.Fprintf(d.msg, "no breakpoint %q\n", arg)
}
