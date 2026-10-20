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
// is one at its function's first line (Func names it). With Cond, it stops
// only when the condition is true there; with Hit, only when the count of
// its hits so far meets it ("3": the 3rd time, ">= 3", "% 2", ...); with
// Log, it shows the message instead of stopping, with {expression}s in it
// replaced by their values.
type Breakpoint struct {
	ID             int
	File           string // the file of the function whose line it is
	Line           int
	Func           string
	Cond, Hit, Log string
	hits           int
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
		if b.Line == f.Line && SameFile(f.Func.File, b.File) {
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
	if b.Hit != "" && !hitCount(b.Hit, n) {
		return false
	}
	if b.Log != "" {
		s.front.Log(Interpolate(m, frame, b.Log))
		return false
	}
	return true
}

// hitCount tells whether n hits meet cond: "3" (exactly), "== 3", "!= 3",
// "> 3", ">= 3", "< 3", "<= 3" or "% 3" (every 3rd).
func hitCount(cond string, n int) bool {
	cond = strings.TrimSpace(cond)
	op := "=="
	for _, o := range []string{"==", "!=", ">=", "<=", ">", "<", "%"} {
		if strings.HasPrefix(cond, o) {
			op, cond = o, strings.TrimSpace(cond[len(o):])
			break
		}
	}
	k, err := strconv.Atoi(cond)
	if err != nil {
		return true // checked when set (CheckHit)
	}
	switch op {
	case "!=":
		return n != k
	case ">":
		return n > k
	case ">=":
		return n >= k
	case "<":
		return n < k
	case "<=":
		return n <= k
	case "%":
		return k > 0 && n%k == 0
	}
	return n == k
}

// CheckHit reports what is wrong with a hit count condition, or nil.
func CheckHit(cond string) error {
	c := strings.TrimSpace(cond)
	for _, o := range []string{"==", "!=", ">=", "<=", ">", "<", "%"} {
		c = strings.TrimSpace(strings.TrimPrefix(c, o))
	}
	if _, err := strconv.Atoi(c); err != nil {
		return fmt.Errorf("a hit count is a number, or ==, !=, >, >=, <, <= or %% and a number, not %q", cond)
	}
	return nil
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
			end := strings.IndexByte(msg[i:], '}')
			if end < 0 {
				b.WriteString(msg[i:])
				return b.String()
			}
			if v, err := Eval(m, frame, msg[i+1:i+end]); err != nil {
				b.WriteString("<" + err.Error() + ">")
			} else if v.Kind() == value.String {
				b.WriteString(v.AsString())
			} else {
				b.WriteString(v.Repr())
			}
			i += end
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
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
// first line. A note says when the line moved.
func (s *Session) SetBreak(b Breakpoint) (bp *Breakpoint, note string, err error) {
	if b.Func != "" {
		fn := s.Function(b.Func)
		if fn == nil || len(fn.Lines) == 0 {
			return nil, "", fmt.Errorf("no function %q", b.Func)
		}
		b.File, b.Line = fn.File, fn.Lines[0].Line
	}
	if b.Hit != "" {
		if err := CheckHit(b.Hit); err != nil {
			return nil, "", err
		}
	}
	file, line, err := s.CodeLine(b.File, b.Line)
	if err != nil {
		return nil, "", err
	}
	if line != b.Line && b.Line != 0 {
		note = fmt.Sprintf("(no code on line %d: the next is %d)", b.Line, line)
	}
	b.File, b.Line = file, line
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	b.ID = s.nextID
	bp = &b
	s.breaks = append(s.breaks, bp)
	return bp, note, nil
}

// CodeLine gives the first line with code at or after line in file, and
// the file's name as the program has it.
func (s *Session) CodeLine(file string, line int) (string, int, error) {
	best := 0
	for _, fn := range append([]*bytecode.Function{s.Prog.Init}, s.Prog.Funcs...) {
		if !SameFile(fn.File, file) {
			continue
		}
		file = fn.File
		if fn.Name() == "<init>" && len(fn.Code) == 1 && bytecode.Op(fn.Code[0]) == bytecode.OpReturnNil {
			continue // no initializers: nothing runs on its line
		}
		for _, l := range fn.Lines {
			if l.Line >= line && (best == 0 || l.Line < best) {
				best = l.Line
			}
		}
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

// Line gives line n (1-based) of file.
func (src Sources) Line(file string, n int) (string, bool) {
	lines, ok := src[file]
	if !ok {
		data, err := os.ReadFile(file)
		if err == nil {
			lines = strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
		}
		src[file] = lines
	}
	if n < 1 || n > len(lines) {
		return "", false
	}
	return lines[n-1], true
}
