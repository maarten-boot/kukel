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

	"github.com/maarten-boot/min-kukel/internal/token"
	"github.com/maarten-boot/min-kukel/internal/value"
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
	OpDup2 // a b -> a b a b

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
	OpJumpIfArg   // n u16, target u32: jump if argument n was passed (n < argc)

	// Calls. The callee is below its arguments on the stack.
	OpCall      // argc u16
	OpCallV     // call with the arguments in a vector on top of the callee
	OpReturn    // return the top of the stack
	OpReturnNil // return nil

	// Vectors (milestone 4 uses them to build spread argument lists).
	OpMakeVector // n u16: pop n values, push a vector of them
	OpAppend     // pop x, append it to the vector below
	OpExtend     // pop vector x, append its elements to the vector below

	// Collections.
	OpMakeMap  // n u16: pop n key/value pairs, push a map of them
	OpIndexGet // x i -> x[i]
	OpIndexSet // x i v -> v, after x[i] = v
	OpIterInit // x -> an iterator over x
	OpIterNext // slot u16, n u16, end u32: advance the iterator in slot; push n values, or jump to end

	// Classes and objects. Member names are string constants (u16 index).
	OpLoadThis    // push this
	OpLoadMember  // field u16: push this.fields[field]
	OpStoreMember // field u16: pop into this.fields[field]
	OpGetMember   // name u16, cache u16: obj -> obj->name (data member or bound method)
	OpSetMember   // name u16, cache u16: obj v -> v, after obj->name = v
	OpCallMethod  // name u16, argc u16, cache u16: obj args -> result of obj->name(args)
	OpCallMethodV // name u16, cache u16: obj argvector -> result
	OpCallDirect  // func u16, argc u16: obj args -> result of Funcs[func] with this = obj (Base::m(...))
	OpLoadFunc    // func u16: push Funcs[func] (a static member function)
	OpLoadStatic  // class u16, slot u16: push a static data member
	OpStoreStatic // class u16, slot u16: pop into a static data member
	OpNew         // argc u16: class args -> new object (the constructor runs)
	OpNewV        // class argvector -> new object

	// Exceptions.
	OpTryPush // kind u16, handler u32: register a handler (kind 0: catch, 1: finally)
	OpTryPop  // remove the innermost handler
	OpThrow   // pop a value and throw it
	OpRethrow // pop a pending exception (from a finally handler) and throw it again

	// Closures. A captured local lives in a cell held by its slot.
	OpNewCell      // slot u16: pop a value into a new cell in the slot
	OpLoadCell     // slot u16: push the value in the slot's cell
	OpStoreCell    // slot u16: pop into the slot's cell
	OpLoadUpvalue  // index u16: push the value of the closure's captured cell
	OpStoreUpvalue // index u16: pop into the closure's captured cell
	OpClosure      // func u16: push a closure of Funcs[func] (see Function.Upvalues)

	// Superinstructions (milestone 12): common pairs fused into one.
	OpCmpJump  // cmp u16, target u32: pop b, a; jump if (a cmp b) == want (see CmpJump*)
	OpIncLocal // slot u16: slot++ (a statement: no value is pushed)
	OpDecLocal // slot u16: slot--

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
	OpNop:          {"NOP", nil},
	OpConst:        {"CONST", u16},
	OpNil:          {"NIL", nil},
	OpTrue:         {"TRUE", nil},
	OpFalse:        {"FALSE", nil},
	OpPop:          {"POP", nil},
	OpDup:          {"DUP", nil},
	OpDup2:         {"DUP2", nil},
	OpLoadLocal:    {"LOAD_LOCAL", u16},
	OpStoreLocal:   {"STORE_LOCAL", u16},
	OpLoadGlobal:   {"LOAD_GLOBAL", u16},
	OpStoreGlobal:  {"STORE_GLOBAL", u16},
	OpAdd:          {"ADD", nil},
	OpSub:          {"SUB", nil},
	OpMul:          {"MUL", nil},
	OpDiv:          {"DIV", nil},
	OpMod:          {"MOD", nil},
	OpBitAnd:       {"BITAND", nil},
	OpBitOr:        {"BITOR", nil},
	OpBitXor:       {"BITXOR", nil},
	OpShl:          {"SHL", nil},
	OpShr:          {"SHR", nil},
	OpNeg:          {"NEG", nil},
	OpNot:          {"NOT", nil},
	OpBitNot:       {"BITNOT", nil},
	OpEq:           {"EQ", nil},
	OpNe:           {"NE", nil},
	OpLt:           {"LT", nil},
	OpLe:           {"LE", nil},
	OpGt:           {"GT", nil},
	OpGe:           {"GE", nil},
	OpInc:          {"INC", nil},
	OpDec:          {"DEC", nil},
	OpJump:         {"JUMP", u32},
	OpJumpIfFalse:  {"JUMP_IF_FALSE", u32},
	OpJumpIfTrue:   {"JUMP_IF_TRUE", u32},
	OpJumpIfArg:    {"JUMP_IF_ARG", []int{2, 4}},
	OpCall:         {"CALL", u16},
	OpCallV:        {"CALL_V", nil},
	OpReturn:       {"RETURN", nil},
	OpReturnNil:    {"RETURN_NIL", nil},
	OpMakeVector:   {"MAKE_VECTOR", u16},
	OpAppend:       {"APPEND", nil},
	OpExtend:       {"EXTEND", nil},
	OpMakeMap:      {"MAKE_MAP", u16},
	OpIndexGet:     {"INDEX_GET", nil},
	OpIndexSet:     {"INDEX_SET", nil},
	OpIterInit:     {"ITER_INIT", nil},
	OpIterNext:     {"ITER_NEXT", []int{2, 2, 4}},
	OpLoadThis:     {"LOAD_THIS", nil},
	OpLoadMember:   {"LOAD_MEMBER", u16},
	OpStoreMember:  {"STORE_MEMBER", u16},
	OpGetMember:    {"GET_MEMBER", []int{2, 2}},
	OpSetMember:    {"SET_MEMBER", []int{2, 2}},
	OpCallMethod:   {"CALL_METHOD", []int{2, 2, 2}},
	OpCallMethodV:  {"CALL_METHOD_V", []int{2, 2}},
	OpCallDirect:   {"CALL_DIRECT", []int{2, 2}},
	OpLoadFunc:     {"LOAD_FUNC", u16},
	OpLoadStatic:   {"LOAD_STATIC", []int{2, 2}},
	OpStoreStatic:  {"STORE_STATIC", []int{2, 2}},
	OpNew:          {"NEW", u16},
	OpNewV:         {"NEW_V", nil},
	OpTryPush:      {"TRY_PUSH", []int{2, 4}},
	OpTryPop:       {"TRY_POP", nil},
	OpThrow:        {"THROW", nil},
	OpRethrow:      {"RETHROW", nil},
	OpNewCell:      {"NEW_CELL", u16},
	OpLoadCell:     {"LOAD_CELL", u16},
	OpStoreCell:    {"STORE_CELL", u16},
	OpLoadUpvalue:  {"LOAD_UPVALUE", u16},
	OpStoreUpvalue: {"STORE_UPVALUE", u16},
	OpClosure:      {"CLOSURE", u16},
	OpCmpJump:      {"CMP_JUMP", []int{2, 4}},
	OpIncLocal:     {"INC_LOCAL", u16},
	OpDecLocal:     {"DEC_LOCAL", u16},
}

// The comparison operand of CMP_JUMP: one of these, plus CmpJumpIfTrue to
// jump when the comparison holds (else when it does not).
const (
	CmpLt = iota
	CmpLe
	CmpGt
	CmpGe
	CmpEq
	CmpNe
	CmpJumpIfTrue = 0x100
)

var cmpNames = [...]string{CmpLt: "<", CmpLe: "<=", CmpGt: ">", CmpGe: ">=", CmpEq: "==", CmpNe: "!="}

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
	FuncName    string
	File        string // source file
	Line        int    // line of the definition
	NumParams   int    // required parameters
	NumOptional int    // optional parameters (with a default), after the required ones
	HasRest     bool   // a rest parameter follows the optional ones
	NumSlots    int    // parameters + temporaries + locals
	MaxStack    int    // operand stack depth needed above the slots
	Code        []byte
	Consts      []value.Value
	Lines       []LineInfo // sorted by PC
	SlotNames   []string   // for the disassembler
	Upvalues    []Upvalue  // a function literal's captured variables
	NumCaches   int        // inline cache slots of member instructions (the VM keeps the caches)

	// Index is the function's position in Program.Funcs (-1 for Init). It is
	// not stored in files: the compiler and Decode set it.
	Index int
}

// Upvalue says where a closure gets a captured cell when it is made: from a
// slot of the function making it, or from that function's own upvalues.
type Upvalue struct {
	Name      string // for the disassembler
	FromLocal bool
	Index     int // slot or upvalue index
}

// ParamSlots is the number of slots that hold parameters.
func (f *Function) ParamSlots() int {
	n := f.NumParams + f.NumOptional
	if f.HasRest {
		n++
	}
	return n
}

// Pos returns the position of the function's definition.
func (f *Function) Pos() token.Pos { return token.Pos{File: f.File, Line: f.Line, Col: 1} }

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
	GlobalClass                     // a class: Classes[Class]
)

// Global describes one global slot.
type Global struct {
	Name  string
	Kind  GlobalKind
	Func  int // for GlobalFunc
	Class int // for GlobalClass
}

// Class describes a class. A base class always comes before the classes
// derived from it in Program.Classes.
type Class struct {
	Name    string
	Base    int      // index in Program.Classes, or -1
	Fields  []string // own instance data members (the base's come first in an object)
	Statics []string // own static data members
	Methods []Method // own member functions
}

// Method is a member function of a class.
type Method struct {
	Name   string
	Static bool
	Func   int // index in Program.Funcs, or -1 when declared but not defined
}

// Program is a compiled program: global slots, functions, an init function
// that runs the global initializers, and the entry point.
type Program struct {
	Globals []Global
	Classes []*Class
	Funcs   []*Function
	Init    *Function
	Main    int // global index of main
}
