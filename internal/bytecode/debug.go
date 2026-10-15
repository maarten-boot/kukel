package bytecode

// Debuggable gives a copy of p for the debugger: every function has its own
// copy of its code, in which the first instruction of each source line is a
// BREAK; Function.Saved keeps the instruction it replaced. p itself does
// not change, and the copy runs as p does where no debug hook is set (the
// VM then runs the saved instruction at once).
func Debuggable(p *Program) *Program {
	q := *p
	q.Funcs = make([]*Function, len(p.Funcs))
	for i, f := range p.Funcs {
		q.Funcs[i] = debuggable(f)
	}
	q.Init = debuggable(p.Init)
	return &q
}

func debuggable(f *Function) *Function {
	g := *f
	g.Code = append([]byte(nil), f.Code...)
	g.Saved = map[int]Op{}
	for _, l := range f.Lines {
		if l.PC < len(g.Code) && g.Saved[l.PC] == 0 && Op(g.Code[l.PC]) != OpBreak {
			g.Saved[l.PC] = Op(g.Code[l.PC])
			g.Code[l.PC] = byte(OpBreak)
		}
	}
	return &g
}
