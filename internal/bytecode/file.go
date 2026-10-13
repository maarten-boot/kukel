package bytecode

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"math"

	"kukel/internal/value"
)

// The .kkc file format (little-endian; "uv" is an unsigned varint, "str" a
// uv length followed by the bytes):
//
//	"KUKL"                 magic
//	uv  format version     FormatVersion; other versions are refused (Q12)
//	uv  flags              0
//	uv  main               global index of main
//	uv  n, globals         str name, byte kind, uv func or class index
//	uv  n, classes         str name, uv base+1 (0: none), uv n + str fields,
//	                       uv n + str statics, uv n + methods
//	                       (str name, byte static, uv func+1 (0: not defined))
//	function               <init>
//	uv  n, functions       Program.Funcs
//	u32 CRC-32 (IEEE) of everything before it
//
// A function is: str name, str file, uv line, uv required, uv optional,
// byte rest, uv slots, uv max stack, uv caches, uv n + str slot names,
// uv n + upvalues (str name, byte from-local, uv index), uv n + code bytes,
// uv n + constants (byte tag, then: int zigzag uv, float 8 bytes, string str),
// uv n + line entries (uv pc, uv line).

// FormatVersion is the version of the .kkc format written by Encode.
const FormatVersion = 2 // 2: milestone 12 superinstructions

const magic = "KUKL"

// Constant tags.
const (
	tagNil = iota
	tagFalse
	tagTrue
	tagInt
	tagFloat
	tagString
)

// Encode serializes a program.
func Encode(p *Program) ([]byte, error) {
	e := &encoder{}
	e.buf = append(e.buf, magic...)
	e.uv(FormatVersion)
	e.uv(0) // flags
	e.uv(p.Main)
	e.uv(len(p.Globals))
	for _, g := range p.Globals {
		e.str(g.Name)
		e.buf = append(e.buf, byte(g.Kind))
		switch g.Kind {
		case GlobalFunc:
			e.uv(g.Func)
		case GlobalClass:
			e.uv(g.Class)
		default:
			e.uv(0)
		}
	}
	e.uv(len(p.Classes))
	for _, c := range p.Classes {
		e.str(c.Name)
		e.uv(c.Base + 1)
		e.strs(c.Fields)
		e.strs(c.Statics)
		e.uv(len(c.Methods))
		for _, m := range c.Methods {
			e.str(m.Name)
			e.flag(m.Static)
			e.uv(m.Func + 1)
		}
	}
	if err := e.function(p.Init); err != nil {
		return nil, err
	}
	e.uv(len(p.Funcs))
	for _, f := range p.Funcs {
		if err := e.function(f); err != nil {
			return nil, err
		}
	}
	return binary.LittleEndian.AppendUint32(e.buf, crc32.ChecksumIEEE(e.buf)), nil
}

type encoder struct{ buf []byte }

func (e *encoder) uv(x int)     { e.buf = binary.AppendUvarint(e.buf, uint64(x)) }
func (e *encoder) str(s string) { e.uv(len(s)); e.buf = append(e.buf, s...) }
func (e *encoder) flag(b bool) {
	if b {
		e.buf = append(e.buf, 1)
	} else {
		e.buf = append(e.buf, 0)
	}
}
func (e *encoder) strs(ss []string) {
	e.uv(len(ss))
	for _, s := range ss {
		e.str(s)
	}
}

func (e *encoder) function(f *Function) error {
	e.str(f.FuncName)
	e.str(f.File)
	e.uv(f.Line)
	e.uv(f.NumParams)
	e.uv(f.NumOptional)
	e.flag(f.HasRest)
	e.uv(f.NumSlots)
	e.uv(f.MaxStack)
	e.uv(f.NumCaches)
	e.strs(f.SlotNames)
	e.uv(len(f.Upvalues))
	for _, u := range f.Upvalues {
		e.str(u.Name)
		e.flag(u.FromLocal)
		e.uv(u.Index)
	}
	e.uv(len(f.Code))
	e.buf = append(e.buf, f.Code...)
	e.uv(len(f.Consts))
	for _, k := range f.Consts {
		switch k.Kind() {
		case value.Nil:
			e.buf = append(e.buf, tagNil)
		case value.Bool:
			if k.AsBool() {
				e.buf = append(e.buf, tagTrue)
			} else {
				e.buf = append(e.buf, tagFalse)
			}
		case value.Int:
			e.buf = append(e.buf, tagInt)
			e.buf = binary.AppendVarint(e.buf, k.AsInt())
		case value.Float:
			e.buf = append(e.buf, tagFloat)
			e.buf = binary.LittleEndian.AppendUint64(e.buf, math.Float64bits(k.AsFloat()))
		case value.String:
			e.buf = append(e.buf, tagString)
			e.str(k.AsString())
		default:
			return fmt.Errorf("function %s: cannot store a %s constant", f.FuncName, k.Kind())
		}
	}
	e.uv(len(f.Lines))
	for _, l := range f.Lines {
		e.uv(l.PC)
		e.uv(l.Line)
	}
	return nil
}

// ----------------------------------------------------------------------
// Decoding

// Limits that keep a hostile file from making the loader or the VM
// allocate absurd amounts of memory.
const (
	maxCount    = 1 << 16 // globals, classes, functions, members, constants, ...
	maxCodeSize = 1 << 24 // bytes of code in one function
	maxStack    = 1 << 20 // MaxStack of one function
)

// Decode reads a program written by Encode and verifies it (see Verify),
// so that running it cannot crash the VM.
func Decode(data []byte) (*Program, error) {
	if len(data) < len(magic) || string(data[:len(magic)]) != magic {
		return nil, errors.New("not a kukel bytecode file")
	}
	if len(data) < len(magic)+4 {
		return nil, errors.New("truncated bytecode file")
	}
	body, sum := data[:len(data)-4], binary.LittleEndian.Uint32(data[len(data)-4:])
	d := &decoder{buf: body, off: len(magic)}
	if v := d.uv(); d.err == nil && v != FormatVersion {
		return nil, fmt.Errorf("bytecode format version %d is not supported (this kukel reads version %d); compile the program again", v, FormatVersion)
	}
	if crc32.ChecksumIEEE(body) != sum {
		return nil, errors.New("corrupted bytecode file (checksum mismatch)")
	}
	if flags := d.uv(); d.err == nil && flags != 0 {
		return nil, fmt.Errorf("unknown bytecode flags %#x", flags)
	}
	p := &Program{Main: d.uv()}
	p.Globals = make([]Global, d.count())
	for i := range p.Globals {
		g := &p.Globals[i]
		g.Name = d.str()
		g.Kind = GlobalKind(d.byte())
		switch idx := d.uv(); g.Kind {
		case GlobalFunc:
			g.Func = idx
		case GlobalClass:
			g.Class = idx
		}
	}
	p.Classes = make([]*Class, d.count())
	for i := range p.Classes {
		c := &Class{Name: d.str(), Base: d.uv() - 1}
		c.Fields = d.strs()
		c.Statics = d.strs()
		c.Methods = make([]Method, d.count())
		for j := range c.Methods {
			c.Methods[j] = Method{Name: d.str(), Static: d.flag(), Func: d.uv() - 1}
		}
		p.Classes[i] = c
	}
	p.Init = d.function()
	p.Init.Index = -1
	p.Funcs = make([]*Function, d.count())
	for i := range p.Funcs {
		p.Funcs[i] = d.function()
		p.Funcs[i].Index = i
	}
	if d.err != nil {
		return nil, d.err
	}
	if d.off != len(body) {
		return nil, errors.New("invalid bytecode file: unexpected data at the end")
	}
	if err := Verify(p); err != nil {
		return nil, err
	}
	return p, nil
}

type decoder struct {
	buf []byte
	off int
	err error
}

func (d *decoder) fail(format string, args ...any) {
	if d.err == nil {
		d.err = fmt.Errorf("invalid bytecode file: "+format, args...)
	}
}

func (d *decoder) uv() int {
	if d.err != nil {
		return 0
	}
	x, n := binary.Uvarint(d.buf[d.off:])
	if n <= 0 || x > math.MaxInt32 {
		d.fail("bad number at offset %d", d.off)
		return 0
	}
	d.off += n
	return int(x)
}

// count reads a number of items, which cannot exceed the bytes left.
func (d *decoder) count() int {
	n := d.uv()
	if n > maxCount || n > len(d.buf)-d.off {
		d.fail("count %d too large at offset %d", n, d.off)
		return 0
	}
	return n
}

func (d *decoder) bytes(n int) []byte {
	if d.err != nil {
		return nil
	}
	if n > len(d.buf)-d.off {
		d.fail("truncated at offset %d", d.off)
		return nil
	}
	b := d.buf[d.off : d.off+n]
	d.off += n
	return b
}

func (d *decoder) byte() byte {
	if b := d.bytes(1); b != nil {
		return b[0]
	}
	return 0
}

func (d *decoder) flag() bool {
	b := d.byte()
	if b > 1 {
		d.fail("bad flag %d at offset %d", b, d.off-1)
	}
	return b == 1
}

func (d *decoder) str() string { return string(d.bytes(d.uv())) }

func (d *decoder) strs() []string {
	ss := make([]string, d.count())
	for i := range ss {
		ss[i] = d.str()
	}
	return ss
}

func (d *decoder) function() *Function {
	f := &Function{FuncName: d.str(), File: d.str(), Line: d.uv()}
	f.NumParams, f.NumOptional, f.HasRest = d.uv(), d.uv(), d.flag()
	f.NumSlots, f.MaxStack, f.NumCaches = d.uv(), d.uv(), d.uv()
	f.SlotNames = d.strs()
	f.Upvalues = make([]Upvalue, d.count())
	for i := range f.Upvalues {
		f.Upvalues[i] = Upvalue{Name: d.str(), FromLocal: d.flag(), Index: d.uv()}
	}
	n := d.uv()
	if n > maxCodeSize {
		d.fail("function %s: code too large", f.FuncName)
	}
	f.Code = append([]byte(nil), d.bytes(n)...)
	f.Consts = make([]value.Value, d.count())
	for i := range f.Consts {
		switch tag := d.byte(); tag {
		case tagNil:
		case tagFalse:
			f.Consts[i] = value.False
		case tagTrue:
			f.Consts[i] = value.True
		case tagInt:
			x, k := binary.Varint(d.buf[min(d.off, len(d.buf)):])
			if k <= 0 {
				d.fail("bad int constant at offset %d", d.off)
			}
			d.off += max(k, 0)
			f.Consts[i] = value.MakeInt(x)
		case tagFloat:
			if b := d.bytes(8); b != nil {
				f.Consts[i] = value.MakeFloat(math.Float64frombits(binary.LittleEndian.Uint64(b)))
			}
		case tagString:
			f.Consts[i] = value.MakeString(d.str())
		default:
			d.fail("bad constant tag %d", tag)
		}
	}
	f.Lines = make([]LineInfo, d.count())
	for i := range f.Lines {
		f.Lines[i] = LineInfo{PC: d.uv(), Line: d.uv()}
	}
	return f
}
