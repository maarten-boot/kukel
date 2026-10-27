// Package debugger is the core of Kukel's debuggers (TODO.md Q60, Q77):
// breakpoints, stepping, the stop at an exception nothing catches, and
// reading and changing the program's values. Its front ends are kukel debug
// (commands on a terminal) and kukel dap (the Debug Adapter Protocol, for
// editors).
//
// The program runs a copy of its code with a BREAK at the start of every
// line (bytecode.Debuggable); the VM calls Session.Hook there, and
// Session.UncaughtHook where an exception nothing catches is thrown. Where the
// session decides to stop, it calls its Front, on the program's goroutine:
// the program waits until Stopped returns. Only the main interpreter stops;
// workers run on.
package debugger

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/maarten-boot/kukel/internal/bytecode"
	"github.com/maarten-boot/kukel/internal/stdlib"
	"github.com/maarten-boot/kukel/internal/value"
	"github.com/maarten-boot/kukel/internal/vm"
)

// Front is what a debugger shows the user and asks.
type Front interface {
	// Stopped is called when the program stops (on the program's
	// goroutine); the program goes on when it returns: as one of Continue,
	// Step, Next and Finish said, or ended by the error it returns.
	Stopped(m *vm.VM, s Stop) error
	// Log shows the message of a logpoint.
	Log(msg string)
}

// Stop says why the program stopped.
type Stop struct {
	Reason string           // "entry", "step", "breakpoint", "function breakpoint", "pause" or "exception"
	Breaks []int            // the breakpoints it stopped at
	Thrown *vm.RuntimeError // for "exception"
}

// Breakpoint stops the program at a line of a file. A function breakpoint
// (Func names the function) stops where a call of it starts, at its first
// line, and not where a loop on that line comes back to it. With Cond, it
// stops only when the condition is true there; with Hit, only when the
// count of its hits so far meets it ("3": the 3rd time, ">= 3", "% 2",
// ...); with Log, it shows the message instead of stopping, with
// {expression}s in it replaced by their values.
type Breakpoint struct {
	ID             int
	File           string // the file of the function whose line it is
	Line           int
	Func           string
	Cond, Hit, Log string
	hits           int
	fn             *bytecode.Function // of a function breakpoint
}

// Session is the state of a debugger.
type Session struct {
	Prog *bytecode.Program

	front  Front
	mu     sync.Mutex // breaks and nextID: changed while the program runs (by an editor)
	breaks []*Breakpoint
	nextID int
	mode   string // "entry", "continue", "step", "next", "finish"
	depth  int    // the frame depth for next and finish
	pause  atomic.Bool
	caught atomic.Bool      // go on at an exception nothing catches (SetUncaught(false))
	thrown *value.ObjectObj // the exception it stopped for as uncaught
}

// New starts a session of prog (made by bytecode.Debuggable); with
// stopOnEntry it stops at the first line that runs.
func New(prog *bytecode.Program, front Front, stopOnEntry bool) *Session {
	s := &Session{Prog: prog, front: front, mode: "continue"}
	if stopOnEntry {
		s.mode = "entry"
	}
	return s
}

// Attach makes m stop as the session says.
func (s *Session) Attach(m *vm.VM) {
	m.Debug = s.Hook
	m.DebugUncaught = s.UncaughtHook
}

// Continue, Step, Next and Finish say how the program goes on after a
// stop, at the frame depth of the stop (len(m.Frames())).
func (s *Session) Continue()        { s.mode = "continue" }
func (s *Session) Step()            { s.mode = "step" }
func (s *Session) Next(depth int)   { s.mode, s.depth = "next", depth }
func (s *Session) Finish(depth int) { s.mode, s.depth = "finish", depth }
func (s *Session) Pause()           { s.pause.Store(true) } // from any goroutine: stop at the next line that runs
func (s *Session) Paused() bool     { return s.pause.Load() }

// SetUncaught says whether the program stops where an exception nothing
// catches is thrown (it does when the session starts); from any goroutine.
func (s *Session) SetUncaught(stop bool) { s.caught.Store(!stop) }

// Hook runs at the start of every line (VM.Debug): it stops where the mode,
// a pause or a breakpoint says.
func (s *Session) Hook(m *vm.VM) error {
	frames := m.Frames()
	top := frames[len(frames)-1]
	stop := Stop{}
	switch {
	case s.pause.Swap(false):
		stop.Reason = "pause"
	case s.mode == "entry":
		stop.Reason = "entry"
	case s.mode == "step",
		s.mode == "next" && len(frames) <= s.depth,
		s.mode == "finish" && len(frames) < s.depth:
		stop.Reason = "step"
	}
	for _, b := range s.at(top) {
		if !s.hit(m, len(frames)-1, b) {
			continue
		}
		stop.Breaks = append(stop.Breaks, b.ID)
		if stop.Reason == "" || stop.Reason == "step" {
			stop.Reason = "breakpoint"
			if b.Func != "" {
				stop.Reason = "function breakpoint"
			}
		}
	}
	if stop.Reason == "" {
		return nil
	}
	s.mode = "continue"
	return s.front.Stopped(m, stop)
}

// at gives the breakpoints at the frame's line.
func (s *Session) at(f vm.DebugFrame) []*Breakpoint {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*Breakpoint
	for _, b := range s.breaks {
		if b.fn != nil {
			if f.Func == b.fn && f.PC == entry(b.fn) {
				out = append(out, b)
			}
		} else if b.Line == f.Line && SameFile(f.Func.File, b.File) {
			out = append(out, b)
		}
	}
	return out
}

// hit tells whether the program stops at breakpoint b: its condition holds
// and its hit count meets Hit, and it is not a logpoint (whose message it
// shows). A condition that fails to evaluate stops, with the error shown.
func (s *Session) hit(m *vm.VM, frame int, b *Breakpoint) bool {
	if b.Cond != "" {
		v, err := Eval(m, frame, b.Cond)
		if err != nil {
			s.front.Log(fmt.Sprintf("breakpoint %d: condition %s: %v", b.ID, b.Cond, err))
			return true
		}
		if !v.Truthy() {
			return false
		}
	}
	s.mu.Lock()
	b.hits++
	n := b.hits
	s.mu.Unlock()
	if b.Hit != "" {
		if hc, err := parseHit(b.Hit); err == nil && !hc.meets(n) {
			return false
		}
	}
	if b.Log != "" {
		s.front.Log(Interpolate(m, frame, b.Log))
		return false
	}
	return true
}

// hitCond is a hit count condition: op and a number.
type hitCond struct {
	op string
	k  int
}

// parseHit reads a hit count condition: "3" (exactly), "== 3", "!= 3",
// "> 3", ">= 3", "< 3", "<= 3" or "% 3" (every 3rd).
func parseHit(cond string) (hitCond, error) {
	c := strings.TrimSpace(cond)
	op := "=="
	for _, o := range []string{"==", "!=", ">=", "<=", ">", "<", "%"} {
		if strings.HasPrefix(c, o) {
			op, c = o, strings.TrimSpace(c[len(o):])
			break
		}
	}
	k, err := strconv.Atoi(c)
	if err != nil {
		return hitCond{}, fmt.Errorf("a hit count is a number, or ==, !=, >, >=, <, <= or %% and a number, not %q", cond)
	}
	if op == "%" && k <= 0 {
		return hitCond{}, fmt.Errorf("a hit count %% n needs an n above 0, not %q", cond)
	}
	return hitCond{op, k}, nil
}

// meets tells whether n hits meet the condition.
func (h hitCond) meets(n int) bool {
	switch h.op {
	case "!=":
		return n != h.k
	case ">":
		return n > h.k
	case ">=":
		return n >= h.k
	case "<":
		return n < h.k
	case "<=":
		return n <= h.k
	case "%":
		return n%h.k == 0
	}
	return n == h.k
}

// CheckHit reports what is wrong with a hit count condition, or nil.
func CheckHit(cond string) error {
	_, err := parseHit(cond)
	return err
}

// Interpolate gives msg with each {expression} in it replaced by its value
// (as print shows it), or the error; {{ and }} are a { and a }.
func Interpolate(m *vm.VM, frame int, msg string) string {
	var b strings.Builder
	for i := 0; i < len(msg); i++ {
		c := msg[i]
		switch {
		case c == '{' && i+1 < len(msg) && msg[i+1] == '{', c == '}' && i+1 < len(msg) && msg[i+1] == '}':
			b.WriteByte(c)
			i++
		case c == '{':
			end := closing(msg[i:])
			if end < 0 {
				b.WriteString(msg[i:])
				return b.String()
			}
			if v, err := Eval(m, frame, msg[i+1:i+end]); err != nil {
				b.WriteString("<" + err.Error() + ">")
			} else if v.Kind() == value.String {
				b.WriteString(cut(v.AsString(), showChars))
			} else {
				b.WriteString(Show(v))
			}
			i += end
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// closing gives the index of the } that closes the { s starts with: braces
// of map literals in it pair up, and those in string and character
// literals do not count. -1: there is none.
func closing(s string) int {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '{':
			depth++
		case '}':
			if depth--; depth == 0 {
				return i
			}
		case '"', '\'':
			for i++; i < len(s) && s[i] != c; i++ {
				if s[i] == '\\' {
					i++
				}
			}
		}
	}
	return -1
}

// UncaughtHook stops where an exception is thrown that nothing catches
// (VM.DebugUncaught), unless SetUncaught(false): the frames can be looked
// at; the program ends when the front end lets it go on.
func (s *Session) UncaughtHook(m *vm.VM, re *vm.RuntimeError) error {
	if s.caught.Load() {
		return nil
	}
	if re.Value.Kind() == value.Object {
		if re.Value.AsObject() == s.thrown {
			return nil // on its way out of a call from Go (sort's comparator, ...)
		}
		s.thrown = re.Value.AsObject()
	}
	if len(m.Frames()) == 0 {
		return nil
	}
	return s.front.Stopped(m, Stop{Reason: "exception", Thrown: re})
}

// SetBreak sets a breakpoint at a line of a file (the first line with code
// at or after it: the breakpoint's Line), or with a function's name at its
// first line. A note says when the line moved. A condition must be one
// Eval can evaluate (Check), a hit count one CheckHit takes.
func (s *Session) SetBreak(b Breakpoint) (bp *Breakpoint, note string, err error) {
	if b.Cond != "" {
		if err := Check(b.Cond); err != nil {
			return nil, "", fmt.Errorf("the condition %s: %v", b.Cond, err)
		}
	}
	if b.Hit != "" {
		if err := CheckHit(b.Hit); err != nil {
			return nil, "", err
		}
	}
	if b.Func != "" {
		fn := s.Function(b.Func)
		if fn == nil || entry(fn) < 0 {
			return nil, "", fmt.Errorf("no function %q", b.Func)
		}
		b.fn, b.File, b.Line = fn, fn.File, fn.LineAt(entry(fn))
	} else {
		if b.Line < 1 {
			return nil, "", fmt.Errorf("no line %d: lines start at 1", b.Line)
		}
		file, line, err := s.CodeLine(b.File, b.Line)
		if err != nil {
			return nil, "", err
		}
		if line != b.Line {
			note = fmt.Sprintf("(no code on line %d: the next is %d)", b.Line, line)
		}
		b.File, b.Line = file, line
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	b.ID = s.nextID
	bp = &b
	s.breaks = append(s.breaks, bp)
	return bp, note, nil
}

// entry gives the pc where a call of fn first stops (its first BREAK), or
// -1 if it has none.
func entry(fn *bytecode.Function) int {
	for _, l := range fn.Lines {
		if _, ok := fn.Saved[l.PC]; ok {
			return l.PC
		}
	}
	return -1
}

// CodeLine gives the first line with code at or after line in file, and
// the file's name as the program has it.
func (s *Session) CodeLine(file string, line int) (string, int, error) {
	best, known := 0, false
	for _, fn := range append([]*bytecode.Function{s.Prog.Init}, s.Prog.Funcs...) {
		if !SameFile(fn.File, file) {
			continue
		}
		file, known = fn.File, true
		if fn.Name() == "<init>" && len(fn.Code) == 1 && bytecode.Op(fn.Code[0]) == bytecode.OpReturnNil {
			continue // no initializers: nothing runs on its line
		}
		for _, l := range fn.Lines {
			if l.Line >= line && (best == 0 || l.Line < best) {
				best = l.Line
			}
		}
	}
	if !known {
		return file, 0, fmt.Errorf("%s is not a file of the program", file)
	}
	if best == 0 {
		return file, 0, fmt.Errorf("no code at or after %s:%d", file, line)
	}
	return file, best, nil
}

// Clear removes the breakpoints for which drop is true.
func (s *Session) Clear(drop func(b *Breakpoint) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.breaks[:0]
	for _, b := range s.breaks {
		if !drop(b) {
			kept = append(kept, b)
		}
	}
	s.breaks = kept
}

// Breaks gives the breakpoints, in the order they were set.
func (s *Session) Breaks() []Breakpoint {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Breakpoint, len(s.breaks))
	for i, b := range s.breaks {
		out[i] = *b
	}
	return out
}

// Function finds a function by its name ("main", "Shape::area", "util::f").
func (s *Session) Function(name string) *bytecode.Function {
	for _, fn := range s.Prog.Funcs {
		if fn.FuncName == name {
			return fn
		}
	}
	return nil
}

// SameFile tells whether the file of a function is the one a breakpoint
// names: the same path, or the same name.
func SameFile(file, name string) bool {
	return file == name || filepath.Base(file) == name || strings.HasSuffix(file, string(filepath.Separator)+name) ||
		filepath.Clean(file) == filepath.Clean(name) || absolute(file) == absolute(name)
}

func absolute(path string) string {
	if p, err := filepath.Abs(path); err == nil {
		return p
	}
	return path
}

// Sources reads source lines for showing, once per file.
type Sources map[string][]string

// Count gives the number of lines of file (0 if it cannot be read).
func (src Sources) Count(file string) int { return len(src.lines(file)) }

func (src Sources) lines(file string) []string {
	lines, ok := src[file]
	if !ok {
		data, err := os.ReadFile(file)
		if std, ok := stdlib.SourceOf(file); ok { // a standard module written in Kukel
			data, err = std, nil
		}
		if err == nil {
			lines = strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
		}
		src[file] = lines
	}
	return lines
}

// Line gives line n (1-based) of file.
func (src Sources) Line(file string, n int) (string, bool) {
	lines := src.lines(file)
	if n < 1 || n > len(lines) {
		return "", false
	}
	return lines[n-1], true
}
