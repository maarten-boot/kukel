# The Kukel virtual machine and bytecode

This document specifies how Kukel programs are compiled and executed: the
values, the program image, the execution model of the stack machine, every
instruction, how the compiler translates the language's constructs, the
`.kkc` file format and what the loader guarantees.

It describes bytecode format version **3** (kukel v0.2). The language
itself is described in [reference.md](reference.md), its grammar in
[syntax.ebnf](../syntax.ebnf).

Contents:

1. [Overview](#1-overview)
2. [Values](#2-values)
3. [The program image](#3-the-program-image)
4. [Execution model](#4-execution-model)
5. [Instruction set](#5-instruction-set)
6. [How the compiler translates programs](#6-how-the-compiler-translates-programs)
7. [The .kkc file format](#7-the-kkc-file-format)
8. [Loading: the verifier and run-time guards](#8-loading-the-verifier-and-run-time-guards)

---

## 1. Overview

```
source files ──loader (lexer, parser)──▶ syntax trees ──compiler──▶ program image ──VM──▶ run
                                                                           │    ▲
                                                               Encode      ▼    │ Decode + Verify
                                                                       .kkc file
```

| Package | Role |
|---|---|
| `internal/lexer`, `internal/parser`, `internal/ast` | source text to syntax trees |
| `internal/loader` | reads the program's files and the modules they use (`use`), in compile order |
| `kukel` (repository root) | the public Go API for embedding ([embedding.md](embedding.md)) |
| `internal/token`, `internal/diag` | token kinds and source positions; the error type all stages share |
| `internal/compiler` | name resolution, the language's rules, constant evaluation, code generation; the prelude |
| `internal/bytecode` | instructions, the program image, `.kkc` encoding and decoding, the verifier, the disassembler |
| `internal/vm` | the stack machine |
| `internal/value` | run-time values and the semantics of the operators |
| `internal/builtins` | built-in functions |
| `internal/native`, `internal/modules/...` | modules written in Go (`events`, `json`, `strings`, `time`, `worker`) and their native classes |

The compiler checks everything the language requires at compile time; the
VM checks the rest at run time and turns every failure into a Kukel
exception. A program image from the compiler, or from a `.kkc` file that
passed the verifier, cannot crash the VM (section 8).

---

## 2. Values

A value is a small struct: a kind, 64 bits of payload (the bits of a bool,
int or float) and a reference to an object (a string, vector, map, ...; a
Go interface value, two words). Numbers and booleans never allocate.

| Kind | Payload | Notes |
|---|---|---|
| nil | — | the zero value; slots and data members start as nil |
| bool | 0 or 1 | |
| int | int64 | arithmetic wraps around |
| float | IEEE-754 bits | |
| string | Go string | immutable bytes |
| vector | `*VectorObj` (a slice) | shared, mutable |
| map | `*MapObj` | insertion-ordered; keys normalized so 1 and 1.0 are one key |
| function | `*bytecode.Function` | a top-level or static member function |
| closure | `*ClosureObj` | function + captured cells + `this` |
| builtin | `*BuiltinObj` | a Go function (a built-in, or a function of a native module) |
| class | `*ClassObj` | run-time class (section 4.7) |
| object | `*ObjectObj` | class + data member slots; an object of a native class holds a Go value instead (`State`) |
| bound method | object + method | the value of `p->m` |
| *iterator* | internal | state of a for-each loop, only in hidden slots |
| *cell* | internal | a captured variable, only in slots (section 4.8) |
| *internal* | internal | a pending exception, only in hidden slots (section 4.9) |

The last three never reach a Kukel program. The operators' semantics
(arithmetic, comparison, equality, truthiness, conversion to text) are in
`internal/value` and shared by the VM and the compiler's constant
evaluation; [reference.md](reference.md) describes them.

---

## 3. The program image

`bytecode.Program` is what the compiler produces, what a `.kkc` file holds,
and what the VM runs:

| Part | Contents |
|---|---|
| `Globals` | the global slots: name and kind (variable, function, built-in, class, native) and, for functions and classes, an index into `Funcs` or `Classes`. A *native* global is a function, static member function or class of a module written in Go, named by its qualified name (`json::encode`, `time::Time::parse`, `time::Time`). Names of a module's top-level names are qualified (`geometry::hypot`) |
| `Classes` | name (`module::Class` for a class of a module), base class index (a base always comes before its derived classes), own data members, own static data members, own member functions (name, static flag, function index or −1 if declared but not defined) |
| `Funcs` | all functions: top-level functions, member functions, function literals |
| `Init` | the function that runs the global initializers |
| `Main` | the global index of `main` |

A **function** (`bytecode.Function`) has:

| Field | Meaning |
|---|---|
| `FuncName`, `File`, `Line` | for traces: `main`, `Shape::area`, `main/fn@12` |
| `NumParams`, `NumOptional`, `HasRest` | required parameters, optional ones (with defaults), a rest parameter |
| `NumSlots` | parameters + temporaries + locals + hidden slots (slot numbers are reused by sibling blocks) |
| `MaxStack` | the deepest the operand stack gets above the slots |
| `Code` | the instructions (section 5) |
| `Consts` | constants: nil, booleans, ints, floats, strings |
| `Lines` | (pc, line) pairs: from that pc on, the code belongs to that source line |
| `SlotNames` | names of the slots, for the disassembler |
| `Upvalues` | for a function literal: where each captured cell comes from (section 4.8) |
| `NumCaches` | the number of inline cache slots its member instructions use (section 4.10) |
| `Index` | its position in `Funcs` (−1 for `Init`); not stored in files |

The **prelude** is Kukel source compiled in front of every program. It
defines class `Error` (data members `message`, `kind`, `trace`; constructor
`Error(message = nil, kind = nil)`), which the VM uses for runtime errors.

---

## 4. Execution model

### 4.1 State

A VM holds:

- the **value stack**, a growing array of values (it starts with 1024
  entries and doubles when a call needs more);
- the **frames**, one per active call (at most 10000: a deeper call raises
  `StackOverflowError`);
- the **globals**, one value per global slot;
- the **classes**, built from the image at start (section 4.7), with their
  static data members;
- the **handlers** of the active `try` blocks (section 4.9);
- the **inline caches** of every function (section 4.10);
- standard input, output and error, a random source and the start time
  (for the built-ins).

Several VMs may run the same program image: everything a VM changes lives
in the VM.

### 4.2 Frames and the stack

```
                      ┌──────────────┐
                      │ operands ... │  ← sp (top)
                      ├──────────────┤
 base + NumSlots      │              │
                      │ slots        │  parameters, temporaries, locals, hidden slots
 base                 ├──────────────┤
 base − 1             │ callee       │  the function (or class, or receiver) being called
                      ├──────────────┤
                      │ caller's     │
                      │ operands ... │
```

A frame records: the function, its next instruction (`ip`, saved while it
calls another function), `base`, the number of arguments actually passed
(`argc`), the object `this` (or none), the closure being run (or none) and
whether it is a constructor call.

Instructions address slots relative to `base` and the operand stack
relative to `sp`. Statements leave the operand stack empty; only
expressions use it.

### 4.3 Calling convention

To call, the caller pushes the callee, then the arguments, and executes a
call instruction with the argument count. For a Kukel function:

1. The argument count is checked: at least `NumParams`, and at most
   `NumParams + NumOptional` unless `HasRest` (else `ArgumentError`, e.g.
   `f expects 1 to 3 arguments, got 0`).
2. With a rest parameter, arguments beyond the named ones are collected
   into a new vector, which goes into the rest parameter's slot (an empty
   vector when there are none).
3. Slots from the last argument up to `NumSlots` are set to nil.
4. A frame is pushed with `base` = the first argument's position.

Missing optional arguments are filled in by the callee's own code: its
prologue has, for each optional parameter `i`, `JUMP_IF_ARG i` over the code
of the default value (section 6.5). So a default runs only when the
argument is missing, and may use the parameters before it.

Returning (`RETURN`, `RETURN_NIL`) drops the frame's slots, its operands and
the callee, and pushes the result where the callee was. A constructor
frame returns its object instead of the returned value.

What a call instruction calls:

| Callee | Effect |
|---|---|
| function | a frame without `this` |
| closure | a frame with the closure's cells and its `this` |
| bound method | the method on the bound object (a static method without `this`) |
| built-in | the Go function runs at once; its result replaces callee and arguments |
| bound method of a native class | the same, with the object as `this` (the Go function reads its Go value, `State`) |
| class | `TypeError` (use `new`) |
| anything else | `TypeError: cannot call ...` |

**Built-ins** (and member functions of native classes) receive their
arguments as a view of the stack (no copy).
They must not keep that slice after returning. A built-in can call back
into Kukel (`sort` calls its comparator): that runs a nested execution on
top of the same stack (section 4.9 explains how exceptions cross it).

### 4.4 Program start

1. Functions, built-ins, classes and native names are bound to their global
   slots (built-ins and native names by name); global variables are nil.
2. `Init` runs: the global initializers, in program order.
3. `main` is called, with one argument (a vector of the command-line
   arguments as strings) if it has any parameter.
4. If `main` returns an int, that is the exit code; else 0. An uncaught
   exception gives exit code 1; `exit(code)` ends the program at once with
   `code`.

### 4.5 Globals, locals and constants

Globals live in the VM's global array, addressed by index. Locals and
parameters live in slots. Constants (`const`) do not exist
at run time: the compiler puts their values into the code.

### 4.6 Statics and member functions

A static data member is a slot in its class's `Statics`. A static member
function is an ordinary function (a value of kind function, `LOAD_FUNC`).
A non-static member function is a function that runs with `this` set.

### 4.7 Classes and objects

At start the VM builds a run-time class for each class of the image, base
classes first:

- **data member slots**: the base class's, followed by the class's own, so
  a data member has the same slot in every derived class;
- **method table**: a copy of the base class's table, with the class's own
  member functions added or overriding (so all member functions are
  virtual, and finding one is a single lookup);
- **constructor**: the class's own non-static member function named like
  the class (for `geometry::Circle`, the part after the last `::`:
  `Circle`), or else the base class's constructor (inherited), or none;
- **statics**: the class's own static data members, all nil.

An object is its class plus an array of data member slots (all nil when
created). `new C(args)` (`NEW`) creates the object, puts it in the callee's
place and calls the constructor with `this` = the object; without a
constructor it takes no arguments. The constructor's frame returns the
object.

**Native classes** (written in Go, package `internal/native`) are not in the
image: a native global names them, and the VM uses the run-time class the
Go module built once, shared by all programs. Such a class has no data
members and no statics. Its member functions are Go functions:
`CALL_METHOD`, `GET_MEMBER` and bound methods run them like built-ins, with
the object; a static one is a built-in (`p->parse` gives it). Its
constructor is a Go function that `NEW` runs at once to fill in the
object's Go value, after which `NEW` leaves the object (there is no
constructor frame). A native class without a constructor cannot be used
with `new`; its objects come from its module's functions. The compiler
refuses a native class as a base class.

Go code reaches the interpreter through `value.Env` (the VM): output and
input, `Call` to call a Kukel function (a nested execution, section 4.3),
`Local` for state a module keeps per interpreter (the `events` module keeps
its event loop there), and `Thrown` to get the value of a Kukel exception
that `Call` returned as an error.

**Several VMs** can run one program image at once: the image is not
changed while it runs (inline caches belong to the VM), and each VM has its
own globals, stack, classes, output state and native-module state. The
`worker` module uses this: `worker::start` makes a new VM for the running
program on its own goroutine (`VM.Start` runs the global initializers, then
any function), and `startfile` one for another program. VMs share only
immutable values (strings, the native classes); messages between them are
copies. Workers write to the parent's output through `VM.ShareOutput`,
which puts a lock around it. `VM.Run` (what `kukel run` and `exec` use)
flushes and closes that output when `main` returns, so workers still
running write nothing more; a Go program that embeds Kukel does the same
with `Interp.Close`, which also cancels the workers.

### 4.8 Closures and cells

A local that a function literal captures lives in a **cell**: a small heap
object holding one value. Its slot holds the cell, and the frame reads and
writes it with `LOAD_CELL`/`STORE_CELL`. Locals that are not captured stay
plain slots.

`CLOSURE f` builds a closure of function `f`. For each entry of
`f.Upvalues` it takes a cell either from a slot of the current frame
(`FromLocal`) or from the current closure's own cells, which passes cells
through several levels of nesting. The closure also keeps the current
`this`. Inside the literal, `LOAD_UPVALUE`/`STORE_UPVALUE i` use cell `i`.

`NEW_CELL` makes a new cell each time a declaration runs. That is how every
loop iteration gets its own variables (section 6.6).

### 4.9 Exceptions

`TRY_PUSH kind, handler` registers a **handler**: the current frame, the
handler's address and the current stack depth. `TRY_POP` removes the
innermost one. A frame may only return with no handlers of its own left
(the verifier checks this; the VM also drops leftovers as a safety net).

A **throw** (`THROW`, `RETHROW`, or a runtime error in any instruction or
built-in):

1. A runtime error becomes an `Error` object with `message` and `kind`;
   `THROW` of anything but an `Error` object (of class `Error` or derived)
   throws a `TypeError` instead, so every thrown value is an `Error` object.
   When an `Error` object is thrown for the first time, its `trace` is set (a vector of strings `"f (file:line)"`,
   innermost first) and, if its `kind` is nil, `kind` becomes its class's
   name. Throwing it again keeps the original trace.
2. The innermost handler is taken out. The frames above its frame are
   dropped, the stack depth is restored, and execution continues at the
   handler with one value pushed:
   - a **catch** handler (kind 0) gets the thrown value;
   - a **finally** handler (kind 1) gets the *pending exception* (an internal
     value with the value and its trace), which `RETHROW` throws again
     unchanged after the finally code.
3. Without a handler, the exception ends the run: `Run` returns it, and
   `kukel` prints `kind: message` with the trace.

A built-in that calls Kukel code starts a nested execution. That execution
only uses handlers registered by its own frames. An exception it does not
catch returns to the built-in as an error, then continues from the
built-in's call in the outer execution. So an exception thrown in a `sort`
comparator can be caught by the caller of `sort`.

`exit(code)` (and the step limit and cancellation) raise **fatal** exceptions,
which skip all handlers: `finally` blocks do not run.

### 4.10 Inline caches

`CALL_METHOD`, `CALL_METHOD_V`, `GET_MEMBER` and `SET_MEMBER` each have a
cache slot (an operand). The slot remembers the class of the last object
the instruction saw, and what it found there: the member function for the
call instructions, the data member's slot for `GET_MEMBER`/`SET_MEMBER`.
The next time an object of the same class comes along, the lookup is
skipped. (`GET_MEMBER` of a member function, which makes a bound method,
is not cached.) The caches belong to the VM, not to the image.

### 4.11 Limits

| Limit | Value |
|---|---|
| nested calls | 10000 (`StackOverflowError`) |
| constants, slots, globals, cache slots per function | 65536 (u16 operands) |
| functions of a program, member functions of a class | 65536 (compile error beyond) |
| nesting of expressions and statements in the source | 1000 levels (compile error beyond) |
| arguments of one call | 65535 |
| optional parameters of one function | 255 |
| code per function, `MaxStack` | 16 MiB, 2<sup>20</sup> in `.kkc` files |
| step limit | off; when set (tests, or a Go program that embeds Kukel: per call from Go), backward jumps, calls of Kukel functions and thrown exceptions are counted, and exceeding it is fatal |
| cancellation | off; `VM.Cancellable` gives a `Canceller` that any goroutine can use to stop the program at its next step (a fatal `CancelledError`); waiting Go functions watch `Env.Done()` |

---

## 5. Instruction set

An instruction is a one-byte opcode followed by its operands, all
little-endian: **u16** for indexes and counts, **u32** for jump targets.
Jump targets are absolute offsets in the function's code.

In the table, *Pops* and *Pushes* are the number of operand stack values
an instruction takes and leaves. They are written in terms of its operands
(`argc`, `n`). For `ITER_NEXT`, *Pushes* applies when the loop goes on; at
the end it pushes nothing and jumps. A test checks this table against the
code: the numbers, names, operands and stack effects.

| # | Instruction | Operands | Pops | Pushes | Effect |
|---|---|---|---|---|---|
| 0 | `NOP` | | 0 | 0 | nothing |
| 1 | `CONST` | k:u16 | 0 | 1 | push `Consts[k]` |
| 2 | `NIL` | | 0 | 1 | push nil |
| 3 | `TRUE` | | 0 | 1 | push true |
| 4 | `FALSE` | | 0 | 1 | push false |
| 5 | `POP` | | 1 | 0 | drop the top value |
| 6 | `DUP` | | 1 | 2 | `a` → `a a` |
| 7 | `DUP2` | | 2 | 4 | `a b` → `a b a b` |
| 8 | `LOAD_LOCAL` | slot:u16 | 0 | 1 | push the slot |
| 9 | `STORE_LOCAL` | slot:u16 | 1 | 0 | pop into the slot |
| 10 | `LOAD_GLOBAL` | global:u16 | 0 | 1 | push the global |
| 11 | `STORE_GLOBAL` | global:u16 | 1 | 0 | pop into the global |
| 12 | `ADD` | | 2 | 1 | `a + b` (also joins two strings) |
| 13 | `SUB` | | 2 | 1 | `a - b` |
| 14 | `MUL` | | 2 | 1 | `a * b` |
| 15 | `DIV` | | 2 | 1 | `a / b` (`ZeroDivisionError` for integer 0) |
| 16 | `MOD` | | 2 | 1 | `a % b` (`ZeroDivisionError` for integer 0) |
| 17 | `BITAND` | | 2 | 1 | `a & b` (ints) |
| 18 | `BITOR` | | 2 | 1 | `a \| b` (ints) |
| 19 | `BITXOR` | | 2 | 1 | `a ^ b` (ints) |
| 20 | `SHL` | | 2 | 1 | `a << b` (`RangeError` unless 0 ≤ b ≤ 63) |
| 21 | `SHR` | | 2 | 1 | `a >> b`, arithmetic (`RangeError` unless 0 ≤ b ≤ 63) |
| 22 | `NEG` | | 1 | 1 | `-a` |
| 23 | `NOT` | | 1 | 1 | true if `a` is false (nil, false, 0, 0.0), else false |
| 24 | `BITNOT` | | 1 | 1 | `~a` (ints) |
| 25 | `EQ` | | 2 | 1 | `a == b` |
| 26 | `NE` | | 2 | 1 | `a != b` |
| 27 | `LT` | | 2 | 1 | `a < b` (`TypeError` unless numbers or strings) |
| 28 | `LE` | | 2 | 1 | `a <= b` |
| 29 | `GT` | | 2 | 1 | `a > b` |
| 30 | `GE` | | 2 | 1 | `a >= b` |
| 31 | `INC` | | 1 | 1 | `a + 1` (numbers; for `++`) |
| 32 | `DEC` | | 1 | 1 | `a - 1` (numbers; for `--`) |
| 33 | `JUMP` | target:u32 | 0 | 0 | continue at `target` |
| 34 | `JUMP_IF_FALSE` | target:u32 | 1 | 0 | pop; jump if it is false |
| 35 | `JUMP_IF_TRUE` | target:u32 | 1 | 0 | pop; jump if it is true |
| 36 | `JUMP_IF_ARG` | n:u16, target:u32 | 0 | 0 | jump if argument `n` was passed (`n` < the frame's argument count) |
| 37 | `CALL` | argc:u16 | argc+1 | 1 | call the value below the `argc` arguments (section 4.3) |
| 38 | `CALL_V` | | 2 | 1 | like `CALL`, with the arguments in a vector (spread calls) |
| 39 | `RETURN` | | 1 | 0 | return the top value |
| 40 | `RETURN_NIL` | | 0 | 0 | return nil |
| 41 | `MAKE_VECTOR` | n:u16 | n | 1 | a new vector of the top `n` values (the deepest first) |
| 42 | `APPEND` | | 2 | 1 | `v x` → `v`, with `x` appended to vector `v` |
| 43 | `EXTEND` | | 2 | 1 | `v w` → `v`, with the elements of vector `w` appended (`TypeError` if `w` is not a vector) |
| 44 | `MAKE_MAP` | n:u16 | 2*n | 1 | a new map of `n` key/value pairs (`k1 v1 k2 v2 ...`); a later equal key replaces an earlier one |
| 45 | `INDEX_GET` | | 2 | 1 | `x i` → `x[i]` (vector element, string byte, map value or nil) |
| 46 | `INDEX_SET` | | 3 | 1 | `x i v` → `v`, after `x[i] = v` |
| 47 | `ITER_INIT` | | 1 | 1 | `x` → an iterator over vector, map or string `x` (`TypeError` otherwise) |
| 48 | `ITER_NEXT` | slot:u16, n:u16, end:u32 | 0 | n | advance the iterator in `slot`; push 1 or 2 values (section 6.7), or jump to `end` |
| 49 | `LOAD_THIS` | | 0 | 1 | push `this` (nil without one) |
| 50 | `LOAD_MEMBER` | field:u16 | 0 | 1 | push data member slot `field` of `this` |
| 51 | `STORE_MEMBER` | field:u16 | 1 | 0 | pop into data member slot `field` of `this` |
| 52 | `GET_MEMBER` | name:u16, cache:u16 | 1 | 1 | `p` → `p->name`: a data member, else a bound method (a static method: its function), else `MemberError` |
| 53 | `SET_MEMBER` | name:u16, cache:u16 | 2 | 1 | `p v` → `v`, after `p->name = v` (data members only) |
| 54 | `CALL_METHOD` | name:u16, argc:u16, cache:u16 | argc+1 | 1 | call `p->name(args)` on the object below the arguments (a static method without `this`) |
| 55 | `CALL_METHOD_V` | name:u16, cache:u16 | 2 | 1 | like `CALL_METHOD`, with the arguments in a vector |
| 56 | `CALL_DIRECT` | func:u16, argc:u16 | argc+1 | 1 | call `Funcs[func]` with `this` = the value below the arguments (non-virtual: `Base::m(...)`) |
| 57 | `LOAD_FUNC` | func:u16 | 0 | 1 | push `Funcs[func]` as a function value (static member functions) |
| 58 | `LOAD_STATIC` | class:u16, slot:u16 | 0 | 1 | push a static data member |
| 59 | `STORE_STATIC` | class:u16, slot:u16 | 1 | 0 | pop into a static data member |
| 60 | `NEW` | argc:u16 | argc+1 | 1 | a new object of the class below the arguments; runs the constructor (section 4.7) |
| 61 | `NEW_V` | | 2 | 1 | like `NEW`, with the arguments in a vector |
| 62 | `TRY_PUSH` | kind:u16, handler:u32 | 0 | 0 | register a handler (kind 0: catch, 1: finally) at `handler` (section 4.9) |
| 63 | `TRY_POP` | | 0 | 0 | remove the innermost handler |
| 64 | `THROW` | | 1 | 0 | throw the popped value, which must be an `Error` object (else a `TypeError` is thrown instead) |
| 65 | `RETHROW` | | 1 | 0 | throw the popped pending exception again (after a finally block) |
| 66 | `NEW_CELL` | slot:u16 | 1 | 0 | pop into a new cell in `slot` |
| 67 | `LOAD_CELL` | slot:u16 | 0 | 1 | push the value in `slot`'s cell |
| 68 | `STORE_CELL` | slot:u16 | 1 | 0 | pop into `slot`'s cell |
| 69 | `LOAD_UPVALUE` | index:u16 | 0 | 1 | push the value of the closure's cell `index` |
| 70 | `STORE_UPVALUE` | index:u16 | 1 | 0 | pop into the closure's cell `index` |
| 71 | `CLOSURE` | func:u16 | 0 | 1 | push a closure of `Funcs[func]` (section 4.8) |
| 72 | `CMP_JUMP` | cmp:u16, target:u32 | 2 | 0 | `a b`: compare; jump if the result equals the wanted one (below) |
| 73 | `INC_LOCAL` | slot:u16 | 0 | 0 | `slot = slot + 1` (statement `i++`) |
| 74 | `DEC_LOCAL` | slot:u16 | 0 | 0 | `slot = slot - 1` |

`CMP_JUMP`'s `cmp` operand is the comparison in its low byte (0 `<`,
1 `<=`, 2 `>`, 3 `>=`, 4 `==`, 5 `!=`), plus 0x100 to jump when the
comparison holds (without it: jump when it does not). It is `LT` (and the
others) followed by `JUMP_IF_TRUE` or `JUMP_IF_FALSE` in one instruction.

**Errors**: arithmetic, comparison and indexing raise the errors described
in the reference (`TypeError`, `ZeroDivisionError`, `RangeError`); member
instructions raise `TypeError` for a value that is not an object and
`MemberError` for an unknown member; call instructions raise
`ArgumentError`, `TypeError` and `StackOverflowError`. Every error becomes
an exception (section 4.9).

**Fast paths**: arithmetic and comparisons on two ints, `CMP_JUMP`,
`INC_LOCAL`, and member instructions with a cache hit avoid the general
code. They behave exactly like it.

---

## 6. How the compiler translates programs

The listings below are real compiler output (`kukel disasm`), shortened.

### 6.1 Names and slots

The compiler resolves every name while it generates code. In order: a
local (innermost block first), a parameter, a captured
variable of an enclosing function, a member of the current class
(inherited ones too; not constructors), a top-level name of the file's
program or module, a built-in. `m::x` is a top-level name of module `m`
when the file uses `m`. Anything else is a compile error. The result
decides the instruction:

| Name is | Read | Write |
|---|---|---|
| local, parameter | `LOAD_LOCAL` | `STORE_LOCAL` |
| the same, captured by a literal | `LOAD_CELL` | `STORE_CELL` |
| a captured variable inside a literal | `LOAD_UPVALUE` | `STORE_UPVALUE` |
| a data member of `this` | `LOAD_MEMBER` | `STORE_MEMBER` |
| a static data member | `LOAD_STATIC` | `STORE_STATIC` |
| a member function | `LOAD_THIS; GET_MEMBER` (bound) or `LOAD_FUNC` (static) | error |
| a global variable, function, class or built-in (also `m::x` of a module) | `LOAD_GLOBAL` | `STORE_GLOBAL` (variables only) |
| a constant | `CONST`/`NIL`/`TRUE`/`FALSE` with its value | error |

Parameters take slots 0, 1, ... in order, then locals.
A block's locals get the next free slots and give them back at the end of
the block, so sibling blocks share slots. Hidden slots (named in
parentheses by the disassembler) hold a switch value, a for-each iterator,
a pending exception, or a temporary value (below).

Modules need nothing from the VM. The loader puts each module before the
files that use it, and the compiler compiles all of them into one program
image: a module's names become ordinary globals with qualified names, and
its global initializers come first in `Init`. A `.kkc` file therefore
holds the program together with all its modules.

### 6.2 Expressions and the stack depth

An expression leaves exactly one value; a statement leaves none (the
compiler checks this after every statement). The compiler tracks the depth
after each instruction (`bytecode.StackUse`) and records the maximum as
`MaxStack`. Where paths join, it sets the depth explicitly (for example
after `cond ? a : b`).

An expression statement is the expression and `POP`, except for
assignments and `++`/`--` to variables, which then skip keeping their
value: `x = 1;` is `CONST 1; STORE_LOCAL x`, and `i++;` on a plain local is
one `INC_LOCAL`. (`INDEX_SET` and `SET_MEMBER` always leave the value, so
`v[i] = x;` and `p->x = 1;` end with `POP`.)

### 6.3 Conditions

`if`, `while`, `for` and `do` compile their condition into jumps, never
into a boolean. A comparison becomes one `CMP_JUMP`; `&&`, `||` and `!`
become jumps between their operands:

```
if (a && !b) return 1;          0  LOAD_LOCAL    0  ; a
                                3  JUMP_IF_FALSE -> 20
                                8  LOAD_LOCAL    1  ; b
                               11  JUMP_IF_TRUE  -> 20
                               16  CONST         0  ; 1
                               19  RETURN
```

Where a value is needed (`return a || b;`), `&&` and `||` jump to `TRUE`
or `FALSE`, so they always give a boolean.

### 6.4 Assignments

`x[i] op= y` evaluates `x` and `i` once: `DUP2` keeps them for the store.
For `x[i]++` and `p->x++` used as a value, the old value is kept in a
temporary slot:

```
v[i] += 1;                      LOAD_LOCAL v; LOAD_LOCAL i; DUP2; INDEX_GET
                                CONST 1; ADD; INDEX_SET; POP
return v[i]++;                  LOAD_LOCAL v; LOAD_LOCAL i; DUP2; INDEX_GET
                                DUP; STORE_LOCAL (temp); INC; INDEX_SET; POP
                                LOAD_LOCAL (temp); RETURN
```

`INDEX_SET` and `SET_MEMBER` leave the assigned value, so `a = v[i] = b`
needs nothing extra.

### 6.5 Functions

A function's code starts with its **prologue**, one parameter after the
other: for an optional parameter `i`, `JUMP_IF_ARG i` over the code of its
default value; for a captured parameter, `LOAD_LOCAL i; NEW_CELL i` to put
it in a cell (so a later default sees the cell). Every function ends with
`RETURN_NIL`.

```
defs(a, b = a * 2, rest..) {     0  JUMP_IF_ARG   1 -> 17
    return [a, b, rest..];        7  LOAD_LOCAL    0  ; a
}                                10  CONST         0  ; 2
                                 13  MUL
                                 14  STORE_LOCAL   1  ; b
                                 17  LOAD_LOCAL    0  ; a
                                 20  LOAD_LOCAL    1  ; b
                                 23  MAKE_VECTOR   2
                                 26  LOAD_LOCAL    2  ; rest
                                 29  EXTEND
                                 30  RETURN
```

A call with spread arguments builds a vector with `MAKE_VECTOR`, `APPEND`
and `EXTEND`, then uses `CALL_V`, `CALL_METHOD_V` or `NEW_V`.

### 6.6 Loops

```
for (local i = 0; i < n; i++) {     6  CONST 0 ; STORE_LOCAL i
    if (i == 2) continue;          12  LOAD_LOCAL i; LOAD_LOCAL n
    s += i;                        18  CMP_JUMP unless < -> 61
}                                  25  LOAD_LOCAL i; CONST 2
                                   31  CMP_JUMP unless == -> 43
                                   38  JUMP -> 53            (continue)
                                   43  LOAD_LOCAL s; LOAD_LOCAL i; ADD; STORE_LOCAL s
                                   53  INC_LOCAL i
                                   56  JUMP -> 12
```

`break` and `continue` are jumps, patched once the loop's end and its
continue point are known (for `for`, the update; for `while`, the
condition; for `do`, the condition at the bottom). Labels name which loop
or statement they mean.

**Fresh variables per iteration**: a `local` declaration in a loop body
runs `NEW_CELL` each time if a literal captures it, so each iteration's
closures get their own cell. For a captured `for (local i ...)` variable,
the compiler adds `LOAD_CELL i; NEW_CELL i` just before the update: the next
iteration gets a new cell that starts with the current value.

### 6.7 For-each

```
for (local k, v in m) ...           LOAD_LOCAL m; ITER_INIT; STORE_LOCAL (iterator)
                              top:  ITER_NEXT (iterator), 2 -> end
                                    STORE_LOCAL v; STORE_LOCAL k
                                    ...body...
                                    JUMP top
                              end:
```

`ITER_NEXT` pushes two values (index and element; key and value; index and
byte) for two names, one for one name (the element of a vector, the key of
a map, the byte of a string). A vector is walked while the index is below
its current length; a map over a snapshot of its keys, skipping keys deleted
since.

### 6.8 Switch

The body comes first and the dispatch after it, because `case` labels may
sit anywhere inside the body:

```
switch (x) {                     LOAD_LOCAL x; STORE_LOCAL (switch); JUMP dispatch
  case 1: return "one";      L1: CONST "one"; RETURN
  case 2: case 3: ...        L2: ...
  default: ...               L3: ...
}                                JUMP end
                       dispatch: LOAD_LOCAL (switch); CONST 1; EQ; JUMP_IF_TRUE L1
                                 LOAD_LOCAL (switch); CONST 2; EQ; JUMP_IF_TRUE L2
                                 LOAD_LOCAL (switch); CONST 3; EQ; JUMP_IF_TRUE L2
                                 JUMP L3            (default; without one: fall to end)
                            end:
```

### 6.9 Try, catch and finally

A `finally` block is copied onto every way out of the try: the normal end,
the end of the catch block, every `break`, `continue` or `return` that
leaves it, and a catch-all handler that runs it and rethrows:

```
try { return f(); }            TRY_PUSH finally -> F
catch (e) { return e; }        TRY_PUSH catch -> C
finally { println("done"); }   LOAD_LOCAL f; CALL 0; STORE_LOCAL (temp)
                               TRY_POP; TRY_POP; <println("done")>
                               LOAD_LOCAL (temp); RETURN         (return through finally)
                               ...
                            C: STORE_LOCAL e                     (the catch handler got the value)
                               LOAD_LOCAL e; STORE_LOCAL (temp)
                               TRY_POP; <println("done")>
                               LOAD_LOCAL (temp); RETURN
                               ...
                            F: STORE_LOCAL (exception)           (the pending exception)
                               <println("done")>
                               LOAD_LOCAL (exception); RETHROW
```

A `return` inside a try computes its value first, keeps it in a temporary
slot while the `finally` copies run, then returns it. A `break` or
`continue` removes the handlers of the try blocks it leaves (`TRY_POP`) and
runs their `finally` copies, innermost first.

### 6.10 Classes and closures

```
b->m();          LOAD_LOCAL b; CALL_METHOD m, 0 (cache 0)          virtual call
A::m() + 1       LOAD_THIS; CALL_DIRECT A::m, 0; CONST 1; ADD      Base::m(...) on this
new B()          LOAD_GLOBAL B; NEW 0
this->x, x       LOAD_MEMBER 0                                     a known data member: its slot
p->x = 2         LOAD_LOCAL p; CONST 2; SET_MEMBER x (cache 1)     by name, cached
```

```
counter() {
    local n = 0;                 CONST 0; NEW_CELL n                (n is captured: it lives in a cell)
    return fn() { return ++n; }; CLOSURE counter/fn@8; RETURN
}
counter/fn@8:                    LOAD_UPVALUE n; INC; DUP; STORE_UPVALUE n; RETURN
                                 (upvalue 0: n, from the enclosing function's slot 0)
```

Each function literal becomes a function of its own (named
`<enclosing>/fn@<line>`), placed after the top-level and member functions in
`Funcs`. A local gets a cell when its name occurs anywhere inside a function
literal nested in its function. This is a safe over-approximation: at worst
a variable gets a cell it does not need.

---

## 7. The .kkc file format

All numbers are unsigned varints (`uv`, as in Go's `encoding/binary`) unless
noted; `str` is a `uv` length followed by that many bytes.

```
"KUKL"                       4 bytes: magic
uv   format version          3
uv   flags                   0
uv   main                    global index of main
uv   n, then n globals       str name, byte kind (0 variable, 1 function, 2 built-in, 3 class, 4 native),
                             uv function or class index (0 for the others)
uv   n, then n classes       str name, uv base+1 (0: none),
                             uv n + str (own data members), uv n + str (own static data members),
                             uv n + methods: str name, byte static (0/1), uv function+1 (0: not defined)
function                     the init function
uv   n, then n functions     Funcs, in order
u32  CRC-32 (IEEE) of everything before it, little-endian
```

A function:

```
str  name, str file, uv line
uv   required, uv optional, byte rest (0/1)
uv   slots, uv max stack, uv cache slots
uv   n + str                 slot names
uv   n + upvalues            str name, byte from-local (0/1), uv index
uv   n + bytes               code
uv   n + constants           byte tag: 0 nil, 1 false, 2 true,
                               3 int (a zigzag varint), 4 float (8 bytes, IEEE bits), 5 string (str)
uv   n + line entries        uv pc, uv line
```

**Versions**: the format version changes whenever the instruction set or the
layout changes (version 2: milestone 12's superinstructions and inline
cache slots; version 3: native globals). A reader refuses any version other
than its own, telling the user to compile the program again. Built-ins and
the names of native modules are referred to by name, so a program that uses
one the running `kukel` does not have is refused when the VM is set up to
run it (`kukel exec`), as is a program without the prelude's `Error` class;
`Decode` and `kukel disasm` accept such files. A program's modules written
in Kukel are inside the file (section 6.1).

---

## 8. Loading: the verifier and run-time guards

`Decode` checks the magic, version and checksum, refuses counts larger than
the file or the limits, and then runs `Verify`. A program that passes cannot
make the VM crash or read outside its data.

**The verifier checks:**

- globals, classes and methods refer to functions and classes that exist; a
  base class comes before its derived class; `main` is a function;
  functions have their own index;
- per function: parameter, slot, stack and cache counts are in range;
- every instruction is known and complete, and its operands are in range:
  constants, slots, globals, functions, classes and their statics, upvalues,
  cache slots; member names are string constants; `CMP_JUMP` comparisons,
  `TRY_PUSH` kinds and `ITER_NEXT` counts are valid;
- each inline cache slot is used by one instruction only (as the compiler
  does), so one member name can never get the method found for another;
- jump targets land on the start of an instruction;
- following every path from the start and from every handler:
  - the stack depth is the same at an instruction on all paths, never too
    low for what it pops, and never above `MaxStack`;
  - the number of registered handlers is the same on all paths, `TRY_POP`
    has one to remove, and returns happen with none left;
  - no path runs past the end of the code.

The compiler's output always passes. The test suite verifies every program
it compiles, which also checks the compiler's own bookkeeping.

**What only the VM can check** raises an `InternalError` exception instead
of crashing: a value of the wrong kind where an instruction expects a cell,
an iterator, a vector being built or a pending exception; `LOAD_MEMBER` or
`STORE_MEMBER` without `this` or beyond the object's data members;
`LOAD_UPVALUE`/`STORE_UPVALUE` outside a closure or beyond its cells;
`CLOSURE` capturing a slot that holds no cell, or an upvalue that is not
there; `TRY_POP` whose innermost handler belongs to another frame; an
unknown opcode. Inline caches cannot make the VM crash: an entry is only
trusted for the class and the kind of member it was filled for. A fatal
exception (the step limit, cancellation, `exit`) drops the try handlers of
the frames it leaves, so a VM can be used again afterwards (an embedded
interpreter is). `new` of a native class without a constructor is a
`TypeError`, also through a variable.

The fuzz tests (`make fuzz`) feed mutated files and arbitrary code to the
loader and run whatever it accepts under a step limit, looking for crashes.
