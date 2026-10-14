package bytecode

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// Module is a module compiled on its own (kukel build -module), for
// linking into programs that use it. Its code refers to globals, classes
// and other modules' functions by index into the name tables here; the
// compiler that links it finds them by name and relocates the code.
//
// The module file (Encode's format with flags 1):
//
//	"KUKL", uv format version, uv flags (1)
//	str name, str header
//	uv n, globals          str name, byte kind
//	uv n, classes          str name, uv n + str data members (with the
//	                       inherited ones), uv n + str static data members
//	uv n, functions        str name: functions of other modules (and of
//	                       the prelude) the code calls, as Code index len(Code)+i
//	uv init                index in Code
//	uv n, code             functions
//	u32 CRC-32 (IEEE) of everything before it
type Module struct {
	Name    string
	Header  string      // its declarations, with empty function bodies and no initializers (Kukel source)
	Globals []Global    // what global indexes in Code stand for: Name and Kind
	Classes []string    // what class indexes stand for: class names
	Layouts []Layout    // the classes' data members when the module was compiled
	Funcs   []string    // the functions of others, after Code in the index space
	Code    []*Function // the module's own functions
	Init    int         // the function in Code that initializes its global variables
}

// Layout is the data members of a class, which the code of member
// functions and of static data member accesses refers to by index: a
// program that links a module must have the same.
type Layout struct {
	Fields  []string // with the inherited ones first
	Statics []string
}

const flagModule = 1

// EncodeModule serializes a module.
func EncodeModule(m *Module) ([]byte, error) {
	e := &encoder{}
	e.buf = append(e.buf, magic...)
	e.uv(FormatVersion)
	e.uv(flagModule)
	e.str(m.Name)
	e.str(m.Header)
	e.uv(len(m.Globals))
	for _, g := range m.Globals {
		e.str(g.Name)
		e.buf = append(e.buf, byte(g.Kind))
	}
	e.uv(len(m.Classes))
	for i, name := range m.Classes {
		e.str(name)
		e.strs(m.Layouts[i].Fields)
		e.strs(m.Layouts[i].Statics)
	}
	e.strs(m.Funcs)
	e.uv(m.Init)
	e.uv(len(m.Code))
	for _, f := range m.Code {
		if err := e.function(f); err != nil {
			return nil, err
		}
	}
	return binary.LittleEndian.AppendUint32(e.buf, crc32.ChecksumIEEE(e.buf)), nil
}

// DecodeModule reads a module written by EncodeModule. Its code is
// checked when it is linked (Relocate, and Verify of the program).
func DecodeModule(data []byte) (*Module, error) {
	d, body, err := open(data)
	if err != nil {
		return nil, err
	}
	if flags := d.uv(); d.err == nil && flags == 0 {
		return nil, errors.New("this is a compiled program, not a module (compile the module with kukel build -module)")
	} else if d.err == nil && flags != flagModule {
		return nil, fmt.Errorf("unknown bytecode flags %#x", flags)
	}
	m := &Module{Name: d.str(), Header: d.str()}
	m.Globals = make([]Global, d.count())
	for i := range m.Globals {
		m.Globals[i] = Global{Name: d.str(), Kind: GlobalKind(d.byte())}
	}
	n := d.count()
	for range n {
		m.Classes = append(m.Classes, d.str())
		m.Layouts = append(m.Layouts, Layout{Fields: d.strs(), Statics: d.strs()})
	}
	m.Funcs = d.strs()
	m.Init = d.uv()
	m.Code = make([]*Function, d.count())
	for i := range m.Code {
		m.Code[i] = d.function()
	}
	if d.err != nil {
		return nil, d.err
	}
	if d.off != len(body) {
		return nil, errors.New("invalid bytecode file: unexpected data at the end")
	}
	if m.Init < 0 || m.Init >= len(m.Code) {
		return nil, errors.New("invalid bytecode file: bad init function")
	}
	return m, nil
}

// Relocate rewrites the global, class and function index operands of f's
// code with global, class and fn. A mapping can refuse an index with an
// error.
func Relocate(f *Function, global, class, fn func(int) (int, error)) error {
	code := f.Code
	for pc := 0; pc < len(code); {
		op := Op(code[pc])
		if _, ok := Info(op); !ok || pc+op.Size() > len(code) {
			return fmt.Errorf("function %s: bad instruction at %d", f.FuncName, pc)
		}
		var at int // the operand to rewrite, with m
		var m func(int) (int, error)
		switch op {
		case OpLoadGlobal, OpStoreGlobal:
			at, m = pc+1, global
		case OpLoadStatic, OpStoreStatic:
			at, m = pc+1, class
		case OpCallDirect, OpLoadFunc, OpClosure:
			at, m = pc+1, fn
		}
		if m != nil {
			x, err := m(ReadU16(code, at))
			if err != nil {
				return fmt.Errorf("function %s: %w", f.FuncName, err)
			}
			if x < 0 || x > 0xFFFF {
				return fmt.Errorf("function %s: index %d out of range", f.FuncName, x)
			}
			binary.LittleEndian.PutUint16(code[at:], uint16(x))
		}
		pc += op.Size()
	}
	return nil
}
