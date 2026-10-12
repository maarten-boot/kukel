package bytecode

import (
	"fmt"
	"io"
)

// Disassemble writes a readable listing of the program.
func Disassemble(w io.Writer, p *Program) error {
	dw := &disasm{w: w, prog: p}
	dw.printf("globals:\n")
	for i, g := range p.Globals {
		switch g.Kind {
		case GlobalVar:
			dw.printf("  %4d  var      %s\n", i, g.Name)
		case GlobalFunc:
			dw.printf("  %4d  function %s\n", i, g.Name)
		case GlobalBuiltin:
			dw.printf("  %4d  builtin  %s\n", i, g.Name)
		}
	}
	if p.Main >= 0 && p.Main < len(p.Globals) {
		dw.printf("main: global %d\n", p.Main)
	}
	if p.Init != nil {
		dw.function(p.Init)
	}
	for _, f := range p.Funcs {
		dw.function(f)
	}
	return dw.err
}

type disasm struct {
	w    io.Writer
	prog *Program
	err  error
}

func (d *disasm) printf(format string, args ...any) {
	if d.err == nil {
		_, d.err = fmt.Fprintf(d.w, format, args...)
	}
}

func (d *disasm) function(f *Function) {
	d.printf("\nfunction %s (%s:%d) params=%d slots=%d stack=%d\n",
		f.FuncName, f.File, f.Line, f.NumParams, f.NumSlots, f.MaxStack)
	for i, name := range f.SlotNames {
		d.printf("  slot %d: %s\n", i, name)
	}
	line := -1
	for pc := 0; pc < len(f.Code); {
		op := Op(f.Code[pc])
		info, ok := Info(op)
		if !ok {
			d.printf("  %5d  ?? 0x%02x\n", pc, f.Code[pc])
			return
		}
		lineCol := "    "
		if l := f.LineAt(pc); l != line {
			line = l
			lineCol = fmt.Sprintf("%4d", l)
		}
		d.printf("%s %5d  %-14s%s\n", lineCol, pc, info.Name, d.operand(f, op, pc))
		pc += op.Size()
	}
}

func (d *disasm) operand(f *Function, op Op, pc int) string {
	switch op {
	case OpConst:
		k := ReadU16(f.Code, pc+1)
		if k < len(f.Consts) {
			return fmt.Sprintf("%d  ; %s", k, f.Consts[k].Repr())
		}
		return fmt.Sprintf("%d  ; ?", k)
	case OpLoadLocal, OpStoreLocal:
		s := ReadU16(f.Code, pc+1)
		if s < len(f.SlotNames) {
			return fmt.Sprintf("%d  ; %s", s, f.SlotNames[s])
		}
		return fmt.Sprint(s)
	case OpLoadGlobal, OpStoreGlobal:
		g := ReadU16(f.Code, pc+1)
		if g < len(d.prog.Globals) {
			return fmt.Sprintf("%d  ; %s", g, d.prog.Globals[g].Name)
		}
		return fmt.Sprint(g)
	case OpCall:
		return fmt.Sprint(ReadU16(f.Code, pc+1))
	case OpJump, OpJumpIfFalse, OpJumpIfTrue:
		return fmt.Sprintf("-> %d", ReadU32(f.Code, pc+1))
	}
	return ""
}
