package bytecode_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/builtins"
	"github.com/maarten-boot/kukel/internal/bytecode"
	"github.com/maarten-boot/kukel/internal/compiler"
	"github.com/maarten-boot/kukel/internal/parser"
	"github.com/maarten-boot/kukel/internal/value"
	"github.com/maarten-boot/kukel/internal/vm"
)

// compilePrograms compiles every program in tests/run that compiles.
func compilePrograms(t testing.TB) map[string]*bytecode.Program {
	files, err := filepath.Glob("../../tests/run/*.kkl")
	if err != nil || len(files) == 0 {
		t.Fatalf("no programs: %v", err)
	}
	progs := map[string]*bytecode.Program{}
	for _, name := range files {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(name, src)
		if err != nil {
			t.Fatal(err)
		}
		p, err := compiler.Compile([]*ast.File{f}, builtins.Names())
		if err != nil {
			continue // compile_error.kkl
		}
		progs[filepath.Base(name)] = p
	}
	return progs
}

func TestCompiledProgramsVerify(t *testing.T) {
	// The verifier also checks the compiler: its stack depths and jumps.
	for name, p := range compilePrograms(t) {
		if err := bytecode.Verify(p); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	for name, p := range compilePrograms(t) {
		data, err := bytecode.Encode(p)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		q, err := bytecode.Decode(data)
		if err != nil {
			t.Fatalf("%s: decode: %v", name, err)
		}
		again, err := bytecode.Encode(q)
		if err != nil || !bytes.Equal(data, again) {
			t.Errorf("%s: encoding the decoded program gives different bytes", name)
		}
		var d1, d2 bytes.Buffer
		bytecode.Disassemble(&d1, p)
		bytecode.Disassemble(&d2, q)
		if d1.String() != d2.String() {
			t.Errorf("%s: disassembly differs after a round trip", name)
		}
	}
}

func sample(t *testing.T) []byte {
	p := compilePrograms(t)["fizzbuzz.kkl"]
	data, err := bytecode.Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// reseal fixes the checksum after a change, so that the change itself is
// what the decoder sees.
func reseal(data []byte) []byte {
	body := data[:len(data)-4]
	return binary.LittleEndian.AppendUint32(append([]byte(nil), body...), crc32.ChecksumIEEE(body))
}

func TestDecodeErrors(t *testing.T) {
	good := sample(t)
	version := append([]byte(nil), good...)
	version[4] = bytecode.FormatVersion + 1
	tests := []struct {
		name string
		data []byte
		msg  string
	}{
		{"empty", nil, "not a kukel bytecode file"},
		{"source code", []byte("main() { }"), "not a kukel bytecode file"},
		{"other version", reseal(version), fmt.Sprintf("bytecode format version %d is not supported (this kukel reads version %d)",
			bytecode.FormatVersion+1, bytecode.FormatVersion)},
		{"checksum", append(append([]byte(nil), good[:len(good)-1]...), good[len(good)-1]^1), "checksum mismatch"},
		{"flipped byte", func() []byte { d := append([]byte(nil), good...); d[len(d)/2] ^= 0x40; return d }(), "checksum mismatch"},
		{"truncated", reseal(good[:len(good)-20]), "invalid bytecode"},
		{"extra data", reseal(append(append([]byte(nil), good[:len(good)-4]...), 0, 0, 0, 0, 0)), "unexpected data at the end"},
	}
	for _, tt := range tests {
		_, err := bytecode.Decode(tt.data)
		if err == nil || !strings.Contains(err.Error(), tt.msg) {
			t.Errorf("%s: got %v, want ...%s...", tt.name, err, tt.msg)
		}
	}
}

// program builds a minimal program whose main has the given code.
func program(code []byte, consts ...value.Value) *bytecode.Program {
	main := &bytecode.Function{FuncName: "main", NumSlots: 1, MaxStack: 2, Code: code, Consts: consts, NumCaches: 1}
	return &bytecode.Program{
		Globals: []bytecode.Global{{Name: "main", Kind: bytecode.GlobalFunc}},
		Funcs:   []*bytecode.Function{main},
		Init:    &bytecode.Function{FuncName: "<init>", Code: []byte{byte(bytecode.OpReturnNil)}, Index: -1},
	}
}

func op(o bytecode.Op, operands ...int) []byte {
	b := []byte{byte(o)}
	info, _ := bytecode.Info(o)
	for i, w := range info.Operands {
		if w == 2 {
			b = binary.LittleEndian.AppendUint16(b, uint16(operands[i]))
		} else {
			b = binary.LittleEndian.AppendUint32(b, uint32(operands[i]))
		}
	}
	return b
}

func code(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

func TestVerifyRejects(t *testing.T) {
	ret := op(bytecode.OpReturnNil)
	tests := []struct {
		name string
		p    *bytecode.Program
		msg  string
	}{
		{"unknown opcode", program([]byte{250}), "unknown opcode"},
		{"cut-off instruction", program([]byte{byte(bytecode.OpConst), 0}), "is cut off"},
		{"falls off the end", program(op(bytecode.OpNil)), "runs past its end"},
		{"stack underflow", program(code(op(bytecode.OpPop), ret)), "needs 1 values on the stack, there are 0"},
		{"stack overflow", program(code(op(bytecode.OpNil), op(bytecode.OpNil), op(bytecode.OpNil), ret)), "exceeds the maximum 2"},
		{"bad constant", program(code(op(bytecode.OpConst, 3), ret)), "refers to constant 3 of 0"},
		{"bad slot", program(code(op(bytecode.OpLoadLocal, 1), ret)), "uses slot 1 of 1"},
		{"bad global", program(code(op(bytecode.OpLoadGlobal, 9), ret)), "refers to global 9 of 1"},
		{"jump into an instruction", program(code(op(bytecode.OpJump, 2), ret)), "not an instruction"},
		{"jump outside", program(code(op(bytecode.OpJump, 99), ret)), "not an instruction"},
		{"different depths at a join", program(code(
			op(bytecode.OpTrue), op(bytecode.OpJumpIfTrue, 12), op(bytecode.OpNil), // 0, 1, 6
			op(bytecode.OpJump, 12), op(bytecode.OpReturnNil), op(bytecode.OpReturnNil))), // 7, 12
			"stack depth"},
		{"member name is not a string", program(code(op(bytecode.OpNil), op(bytecode.OpGetMember, 0, 0), ret), value.MakeInt(1)), "needs a string constant"},
		{"bad cache slot", program(code(op(bytecode.OpNil), op(bytecode.OpGetMember, 0, 1), ret), value.MakeString("x")), "uses cache 1 of 1"},
		{"bad function", program(code(op(bytecode.OpLoadFunc, 5), ret)), "refers to function 5"},
		{"bad static", program(code(op(bytecode.OpLoadStatic, 0, 0), ret)), "static data member that does not exist"},
		{"bad upvalue", program(code(op(bytecode.OpLoadUpvalue, 0), ret)), "refers to upvalue 0 of 0"},
		{"bad handler kind", program(code(op(bytecode.OpTryPush, 7, 0), ret)), "unknown handler kind"},
		{"bad iterator count", program(code(op(bytecode.OpIterNext, 0, 3, 0), ret)), "pushes 3 values"},
		{"TRY_POP without a handler", program(code(op(bytecode.OpTryPop), ret)), "TRY_POP without a handler"},
		{"return inside a try", program(code(op(bytecode.OpTryPush, 0, 7), ret, ret)), "returns with 1 try handlers registered"},
		// The fused forms of Kukel v0.26.
		{"INDEX_LL slot", program(code(op(bytecode.OpIndexLL, 0, 1), ret)), "uses slot 1 of 1"},
		{"GET_MEMBER_L cache", program(code(op(bytecode.OpGetMemberL, 0, 0, 1), ret), value.MakeString("x")), "uses cache 1 of 1"},
		{"GET_MEMBER_L name", program(code(op(bytecode.OpGetMemberL, 0, 0, 0), ret), value.MakeInt(1)), "needs a string constant"},
		{"ARITH_LL operation", program(code(op(bytecode.OpArithLL, 99, 0, 0), ret)), "has unknown operation 99"},
		{"CMP_JUMP_LL comparison", program(code(op(bytecode.OpCmpJumpLL, 7, 0, 0, 11), ret)), "has unknown comparison 7"},
		{"ARITH_K constant", program(code(op(bytecode.OpNil), op(bytecode.OpArithK, int(bytecode.OpAdd), 3), ret)), "refers to constant 3 of 0"},
		{"CMP_JUMP_K target", program(code(op(bytecode.OpNil), op(bytecode.OpCmpJumpK, bytecode.CmpEq, 0, 99), ret), value.MakeInt(1)), "not an instruction"},
	}
	for _, tt := range tests {
		err := bytecode.Verify(tt.p)
		if err == nil || !strings.Contains(err.Error(), tt.msg) {
			t.Errorf("%s: got %v, want ...%s...", tt.name, err, tt.msg)
		}
	}
	// SWITCH_TABLE: a table that does not exist, a target that is not an instruction.
	sw := program(code(op(bytecode.OpNil), op(bytecode.OpSwitchTable, 0, 8), ret)) // 0, 1, 8
	if err := bytecode.Verify(sw); err == nil || !strings.Contains(err.Error(), "refers to jump table 0 of 0") {
		t.Errorf("missing jump table: %v", err)
	}
	sw.Funcs[0].Tables = []bytecode.JumpTable{{Min: 1, Targets: []int{8, -1, 3}}}
	if err := bytecode.Verify(sw); err == nil || !strings.Contains(err.Error(), "jump to 3, which is not an instruction") {
		t.Errorf("bad jump table target: %v", err)
	}
	sw.Funcs[0].Tables[0].Targets[2] = 8
	if err := bytecode.Verify(sw); err != nil {
		t.Errorf("a good jump table: %v", err)
	}

	bad := program(ret)
	bad.Main = 5
	if err := bytecode.Verify(bad); err == nil || !strings.Contains(err.Error(), "main is not a function") {
		t.Errorf("bad main: %v", err)
	}
}

// FuzzDecode feeds mutated .kkc files to the loader; whatever it accepts is
// run with a step limit. Nothing may panic.
func FuzzDecode(f *testing.F) {
	f.Setenv("KUKEL_TEST_TMP", f.TempDir()) // tests/run/files.kkl writes there, not into the sources
	for _, p := range compilePrograms(f) {
		data, err := bytecode.Encode(p)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 8 {
			data = reseal(data) // get past the checksum to reach the decoder
		}
		if m, err := bytecode.DecodeModule(data); err == nil { // a module's code is checked when it is linked
			same := func(i int) (int, error) { return i, nil }
			for _, f := range m.Code {
				bytecode.Relocate(f, same, same, same)
			}
		}
		p, err := bytecode.Decode(data)
		if err != nil {
			return
		}
		m, err := vm.New(p, builtins.Lookup, &bytes.Buffer{}, strings.NewReader(""))
		if err != nil {
			return
		}
		m.StepLimit = 10000
		m.Run(nil)
	})
}

// FuzzVerifyCode replaces the code of functions in real programs with
// arbitrary bytes: whatever the verifier accepts must run without a panic.
func FuzzVerifyCode(f *testing.F) {
	f.Setenv("KUKEL_TEST_TMP", f.TempDir())
	progs := compilePrograms(f)
	var names []string
	for name, p := range progs {
		names = append(names, name)
		for _, fn := range p.Funcs {
			f.Add(len(names)-1, fn.Code)
		}
	}
	f.Fuzz(func(t *testing.T, which int, code []byte) {
		if which < 0 || which >= len(names) {
			return
		}
		// Decode a fresh copy, so that runs do not share state.
		data, _ := bytecode.Encode(progs[names[which]])
		p, err := bytecode.Decode(data)
		if err != nil {
			t.Fatal(err)
		}
		fn := p.Funcs[len(code)%len(p.Funcs)]
		fn.Code = code
		if bytecode.Verify(p) != nil {
			return
		}
		m, err := vm.New(p, builtins.Lookup, &bytes.Buffer{}, strings.NewReader(""))
		if err != nil {
			return
		}
		m.StepLimit = 10000
		m.Run(nil)
	})
}

// TestSharedCacheSlot: a file that gives a CALL_METHOD and a GET_MEMBER the
// same cache slot (the compiler never does) is refused by the verifier, and
// the VM must not crash on it even so: it must not trust a data member slot
// that a method call left in the cache.
func TestSharedCacheSlot(t *testing.T) {
	src := "class K { m(); }\nK::m() {}\nmain() { local k = new K(); k->m(); local f = k->m; f(); }"
	f, err := parser.ParseFile("t.kkl", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	p, err := compiler.Compile([]*ast.File{f}, builtins.Names())
	if err != nil {
		t.Fatal(err)
	}
	var main *bytecode.Function
	for _, fn := range p.Funcs {
		if fn.FuncName == "main" {
			main = fn
		}
	}
	patched := false
	for pc := 0; pc < len(main.Code); pc += bytecode.Op(main.Code[pc]).Size() {
		switch bytecode.Op(main.Code[pc]) {
		case bytecode.OpGetMember:
			binary.LittleEndian.PutUint16(main.Code[pc+3:], 0) // the CALL_METHOD's slot
			patched = true
		case bytecode.OpGetMemberL: // k->m, k a local
			binary.LittleEndian.PutUint16(main.Code[pc+5:], 0)
			patched = true
		}
	}
	if !patched {
		t.Fatal("no GET_MEMBER or GET_MEMBER_L in main")
	}
	// The verifier refuses it (a shared entry could make one name use
	// another's method); the VM must not crash on it either.
	if err := bytecode.Verify(p); err == nil || !strings.Contains(err.Error(), "uses cache 0, as the instruction at") {
		t.Errorf("verify: %v", err)
	}
	m, err := vm.New(p, builtins.Lookup, &bytes.Buffer{}, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Run(nil); err != nil {
		t.Errorf("run: %v", err)
	}
}

// Debuggable copies the code with a BREAK at each line start and keeps the
// program as it was; a file with a BREAK is refused.
func TestDebuggable(t *testing.T) {
	for name, p := range compilePrograms(t) {
		d := bytecode.Debuggable(p)
		for i, f := range p.Funcs {
			g := d.Funcs[i]
			if &f.Code[0] == &g.Code[0] || f.Saved != nil {
				t.Fatalf("%s: %s shares its code with the copy", name, f.FuncName)
			}
			for _, l := range f.Lines {
				if l.Cont {
					continue
				}
				if bytecode.Op(g.Code[l.PC]) != bytecode.OpBreak || g.Saved[l.PC] != bytecode.Op(f.Code[l.PC]) {
					t.Fatalf("%s: %s: no BREAK at line %d (pc %d)", name, f.FuncName, l.Line, l.PC)
				}
			}
		}
		if err := bytecode.Verify(d); err == nil || !strings.Contains(err.Error(), "BREAK is only for the debugger") {
			t.Errorf("%s: a program with BREAK: %v", name, err)
		}
	}
}
