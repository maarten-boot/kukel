// Package bytecode defines the instruction set of the Kukel stack machine
// and the compiled program image.
//
// An instruction is a one-byte opcode followed by fixed-width little-endian
// operands: u16 for indices (constants, slots, globals, argument counts) and
// u32 for jump targets (absolute offsets in the function's code).
package bytecode

import (
	"encoding/binary"
	"sort"

	"kukel/internal/value"
)

// Op is an opcode.
type Op uint8

const (
	OpNop Op = iota

	// Stack.
	OpConst // k u16: push Consts[k]
	OpNil
	OpTrue
	OpFalse
	OpPop
	OpDup

	// Variables. Stores pop the value.
	OpLoadLocal   // slot u16
	OpStoreLocal  // slot u16
	OpLoadGlobal  // global u16
	OpStoreGlobal // global u16

	// Arithmetic and logic: pop operands, push the result.
	OpAdd
	OpSub
	OpMul
	OpDiv
	OpMod
	OpBitAnd
	OpBitOr
	OpBitXor
	OpShl
	OpShr
	OpNeg
	OpNot // push !truthy(x)
	OpBitNot
	OpEq
	OpNe
	OpLt
	OpLe
	OpGt
	OpGe
	OpInc // x + 1 (for ++)
	OpDec // x - 1 (for --)

	// Control.
	OpJump        // target u32
	OpJumpIfFalse // target u32: pop, jump if not truthy
	OpJumpIfTrue  // target u32: pop, jump if truthy

	// Calls. The callee is below its arguments on the stack.
	OpCall      // argc u16
	OpReturn    // return the top of the stack
	OpReturnNil // return nil

	numOps
)

// OpInfo describes an opcode for the disassembler and the loader.
type OpInfo struct {
	Name     string
	Operands []int // byte width of each operand
}

var u16 = []int{2}
var u32 = []int{4}

var opInfo = [numOps]OpInfo{
	OpNop:         {"NOP", nil},
	OpConst:       {"CONST", u16},
	OpNil:         {"NIL", nil},
	OpTrue:        {"TRUE", nil},
	OpFalse:       {"FALSE", nil},
	OpPop:         {"POP", nil},
	OpDup:         {"DUP", nil},
	OpLoadLocal:   {"LOAD_LOCAL", u16},
	OpStoreLocal:  {"STORE_LOCAL", u16},
	OpLoadGlobal:  {"LOAD_GLOBAL", u16},
	OpStoreGlobal: {"STORE_GLOBAL", u16},
	OpAdd:         {"ADD", nil},
	OpSub:         {"SUB", nil},
	OpMul:         {"MUL", nil},
	OpDiv:         {"DIV", nil},
	OpMod:         {"MOD", nil},
	OpBitAnd:      {"BITAND", nil},
	OpBitOr:       {"BITOR", nil},
	OpBitXor:      {"BITXOR", nil},
	OpShl:         {"SHL", nil},
	OpShr:         {"SHR", nil},
	OpNeg:         {"NEG", nil},
	OpNot:         {"NOT", nil},
	OpBitNot:      {"BITNOT", nil},
	OpEq:          {"EQ", nil},
	OpNe:          {"NE", nil},
	OpLt:          {"LT", nil},
	OpLe:          {"LE", nil},
	OpGt:          {"GT", nil},
	OpGe:          {"GE", nil},
	OpInc:         {"INC", nil},
	OpDec:         {"DEC", nil},
	OpJump:        {"JUMP", u32},
	OpJumpIfFalse: {"JUMP_IF_FALSE", u32},
	OpJumpIfTrue:  {"JUMP_IF_TRUE", u32},
	OpCall:        {"CALL", u16},
	OpReturn:      {"RETURN", nil},
	OpReturnNil:   {"RETURN_NIL", nil},
}

// Info returns the description of op; ok is false for an unknown opcode.
func Info(op Op) (OpInfo, bool) {
	if op >= numOps {
		return OpInfo{}, false
	}
	return opInfo[op], true
}

func (op Op) String() string {
	if info, ok := Info(op); ok {
		return info.Name
	}
	return "?"
}

// Size returns the size in bytes of an instruction with opcode op.
func (op Op) Size() int {
	n := 1
	for _, w := range opInfo[op].Operands {
		n += w
	}
	return n
}

// ReadU16 and ReadU32 decode operands.
func ReadU16(code []byte, at int) int { return int(binary.LittleEndian.Uint16(code[at:])) }
func ReadU32(code []byte, at int) int { return int(binary.LittleEndian.Uint32(code[at:])) }

// LineInfo maps code from PC onwards to a source line.
type LineInfo struct {
	PC   int
	Line int
}

// Function is a compiled function: its code and constants.
type Function struct {
	FuncName  string
	File      string // source file
	Line      int    // line of the definition
	NumParams int    // required parameters
	NumSlots  int    // parameters + temporaries + locals
	MaxStack  int    // operand stack depth needed above the slots
	Code      []byte
	Consts    []value.Value
	Lines     []LineInfo // sorted by PC
	SlotNames []string   // for the disassembler
}

// Name implements value.Named.
func (f *Function) Name() string { return f.FuncName }

// LineAt returns the source line of the instruction at pc.
func (f *Function) LineAt(pc int) int {
	i := sort.Search(len(f.Lines), func(i int) bool { return f.Lines[i].PC > pc })
	if i == 0 {
		return f.Line
	}
	return f.Lines[i-1].Line
}

// GlobalKind says what a global slot holds when the program starts.
type GlobalKind uint8

const (
	GlobalVar     GlobalKind = iota // a global variable (nil until initialized)
	GlobalFunc                      // a function: Funcs[Func]
	GlobalBuiltin                   // a built-in, looked up by name
)

// Global describes one global slot.
type Global struct {
	Name string
	Kind GlobalKind
	Func int // for GlobalFunc
}

// Program is a compiled program: global slots, functions, an init function
// that runs the global initializers, and the entry point.
type Program struct {
	Globals []Global
	Funcs   []*Function
	Init    *Function
	Main    int // global index of main
}
