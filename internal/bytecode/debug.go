package bytecode

import "strings"

// Debuggable gives a copy of p for the debugger: every function has its own
// copy of its code, in which the first instruction of each source line is a
// BREAK; Function.Saved keeps the instruction it replaced. p itself does
// not change, and the copy runs as p does where no debug hook is set (the
// VM then runs the saved instruction at once).
func Debuggable(p *Program) *Program {
	q := *p
	q.Funcs = make([]*Function, len(p.Funcs))
	for i, f := range p.Funcs {
		q.Funcs[i] = debuggable(f, p.Funcs)
	}
	q.Init = debuggable(p.Init, p.Funcs)
	return &q
}

func debuggable(f *Function, funcs []*Function) *Function {
	g := *f
	g.Code = append([]byte(nil), f.Code...)
	g.Saved = map[int]Op{}
	for _, l := range f.Lines {
		if l.PC < len(g.Code) && g.Saved[l.PC] == 0 && Op(g.Code[l.PC]) != OpBreak && !initStep(f, funcs, l.PC) {
			g.Saved[l.PC] = Op(g.Code[l.PC])
			g.Code[l.PC] = byte(OpBreak)
		}
	}
	return &g
}

// initStep reports whether the instruction at pc of an initializer
// function is no place to stop: its end (a program without initializers
// would stop there first), or the call of the <init> function of another
// file (which stops at its own first line).
func initStep(f *Function, funcs []*Function, pc int) bool {
	if !isInit(f) {
		return false
	}
	switch Op(f.Code[pc]) {
	case OpReturnNil:
		return true
	case OpLoadFunc:
		if i := ReadU16(f.Code, pc+1); i < len(funcs) {
			return isInit(funcs[i])
		}
	}
	return false
}

func isInit(f *Function) bool { return strings.HasSuffix(f.FuncName, "<init>") }
