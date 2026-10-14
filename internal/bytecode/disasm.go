package bytecode

import (
	"fmt"
	"io"
	"strings"

	"github.com/maarten-boot/kukel/internal/value"
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
		case GlobalClass:
			dw.printf("  %4d  class    %s\n", i, g.Name)
		case GlobalNative:
			dw.printf("  %4d  native   %s\n", i, g.Name)
		}
	}
	for i, c := range p.Classes {
		base := ""
		if c.Base >= 0 && c.Base < len(p.Classes) {
			base = " : " + p.Classes[c.Base].Name
		}
		if c.Native != "" {
			base = " : " + c.Native + " (native)"
		}
		dw.printf("class %d: %s%s fields=%v statics=%v\n", i, c.Name, base, c.Fields, c.Statics)
		for _, m := range c.Methods {
			kind := "method"
			if m.Static {
				kind = "static"
			}
			dw.printf("  %s %s -> function %d\n", kind, m.Name, m.Func)
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
	params := fmt.Sprint(f.NumParams)
	if f.NumOptional > 0 {
		params += fmt.Sprintf("+%d optional", f.NumOptional)
	}
	if f.HasRest {
		params += "+rest"
	}
	d.printf("\nfunction %s (%s:%d) params=%s slots=%d stack=%d\n",
		f.FuncName, f.File, f.Line, params, f.NumSlots, f.MaxStack)
	for i, name := range f.SlotNames {
		d.printf("  slot %d: %s\n", i, name)
	}
	for i, u := range f.Upvalues {
		from := "upvalue"
		if u.FromLocal {
			from = "slot"
		}
		d.printf("  upvalue %d: %s (from the enclosing function's %s %d)\n", i, u.Name, from, u.Index)
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

func (d *disasm) constName(f *Function, k int) string {
	if k < len(f.Consts) && f.Consts[k].Kind() == value.String {
		return f.Consts[k].AsString()
	}
	return fmt.Sprintf("const %d", k)
}

func (d *disasm) funcName(i int) string {
	if i < len(d.prog.Funcs) {
		return fmt.Sprintf("%d  ; %s", i, d.prog.Funcs[i].FuncName)
	}
	return fmt.Sprint(i)
}

func (d *disasm) operand(f *Function, op Op, pc int) string {
	switch op {
	case OpConst:
		k := ReadU16(f.Code, pc+1)
		if k < len(f.Consts) {
			return fmt.Sprintf("%d  ; %s", k, f.Consts[k].Repr())
		}
		return fmt.Sprintf("%d  ; ?", k)
	case OpLoadUpvalue, OpStoreUpvalue:
		u := ReadU16(f.Code, pc+1)
		if u < len(f.Upvalues) {
			return fmt.Sprintf("%d  ; %s", u, f.Upvalues[u].Name)
		}
		return fmt.Sprint(u)
	case OpClosure:
		return d.funcName(ReadU16(f.Code, pc+1))
	case OpLoadLocal, OpStoreLocal, OpNewCell, OpLoadCell, OpStoreCell, OpIncLocal, OpDecLocal, OpAddLocal:
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
	case OpIterNext:
		slot := ReadU16(f.Code, pc+1)
		name := ""
		if slot < len(f.SlotNames) {
			name = "  ; " + f.SlotNames[slot]
		}
		return fmt.Sprintf("%d, %d -> %d%s", slot, ReadU16(f.Code, pc+3), ReadU32(f.Code, pc+5), name)
	case OpGetMember, OpSetMember, OpCallMethodV:
		return fmt.Sprintf("%s  ; cache %d", d.constName(f, ReadU16(f.Code, pc+1)), ReadU16(f.Code, pc+3))
	case OpCallMethod:
		return fmt.Sprintf("%s, %d  ; cache %d", d.constName(f, ReadU16(f.Code, pc+1)), ReadU16(f.Code, pc+3), ReadU16(f.Code, pc+5))
	case OpCallNative:
		g := ReadU16(f.Code, pc+1)
		s := fmt.Sprintf("%d, %s, %d", g, d.constName(f, ReadU16(f.Code, pc+3)), ReadU16(f.Code, pc+5))
		if g < len(d.prog.Globals) {
			s += "  ; " + d.prog.Globals[g].Name
		}
		return s
	case OpCallDirect:
		fn := ReadU16(f.Code, pc+1)
		s := fmt.Sprintf("%d, %d", fn, ReadU16(f.Code, pc+3))
		if fn < len(d.prog.Funcs) {
			s += "  ; " + d.prog.Funcs[fn].FuncName
		}
		return s
	case OpLoadFunc:
		return d.funcName(ReadU16(f.Code, pc+1))
	case OpLoadStatic, OpStoreStatic:
		c, slot := ReadU16(f.Code, pc+1), ReadU16(f.Code, pc+3)
		if c < len(d.prog.Classes) && slot < len(d.prog.Classes[c].Statics) {
			return fmt.Sprintf("%d, %d  ; %s::%s", c, slot, d.prog.Classes[c].Name, d.prog.Classes[c].Statics[slot])
		}
		return fmt.Sprintf("%d, %d", c, slot)
	case OpLoadMember, OpStoreMember:
		return fmt.Sprint(ReadU16(f.Code, pc+1))
	case OpCall, OpMakeVector, OpMakeMap, OpNew:
		return fmt.Sprint(ReadU16(f.Code, pc+1))
	case OpTryPush:
		kind := "catch"
		if ReadU16(f.Code, pc+1) == 1 {
			kind = "finally"
		}
		return fmt.Sprintf("%s -> %d", kind, ReadU32(f.Code, pc+3))
	case OpCmpJump:
		k := ReadU16(f.Code, pc+1)
		when := "unless"
		if k&CmpJumpIfTrue != 0 {
			when = "if"
		}
		name := "?"
		if c := k &^ CmpJumpIfTrue; c < len(cmpNames) {
			name = cmpNames[c]
		}
		return fmt.Sprintf("%s %s -> %d", when, name, ReadU32(f.Code, pc+3))
	case OpJumpIfArg:
		return fmt.Sprintf("%d -> %d", ReadU16(f.Code, pc+1), ReadU32(f.Code, pc+3))
	case OpSwitchTable:
		t := ReadU16(f.Code, pc+1)
		s := fmt.Sprintf("%d, miss -> %d", t, ReadU32(f.Code, pc+3))
		if t < len(f.Tables) {
			var cases []string
			for i, x := range f.Tables[t].Targets {
				if x >= 0 {
					cases = append(cases, fmt.Sprintf("%d -> %d", f.Tables[t].Min+int64(i), x))
				}
			}
			s += "  ; " + strings.Join(cases, ", ")
		}
		return s
	case OpJump, OpJumpIfFalse, OpJumpIfTrue:
		return fmt.Sprintf("-> %d", ReadU32(f.Code, pc+1))
	}
	return ""
}
