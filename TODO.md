# Kukel interpreter: plan and TODO

Goal: a Go program that reads Kukel source (grammar: `syntax.ebnf`, version 2),
tokenizes and parses it, compiles it to bytecode for a stack machine, stores
that bytecode in a file, and executes it.

```
source files ──lexer──▶ tokens ──parser──▶ AST ──checker──▶ AST + scopes
            ──compiler──▶ bytecode image ──writer──▶ .kkc file
.kkc file ──loader──▶ bytecode image ──VM──▶ program run
```

---

## 0. Decisions made

| Topic | Decision |
|---|---|
| Implementation language | Go (module, standard library only) |
| Syntax | `syntax.ebnf` **version 2** (see §1b for the changes from version 1). No compatibility with version 1 is needed |
| Parser | Hand-written lexer and recursive-descent parser (`syntax.lark` is a tested reference only) |
| "Stored tokenized code" | Compile to a stack-machine bytecode image, saved as a binary `.kkc` file that runs without the source |
| File extensions | Source `.kkl`, compiled bytecode `.kkc`, binary `kukel` (`.kk` avoided: commonly associated with SyncCrypt ransomware) |
| Build/check | Makefile (targets for build, `go test ./...`, `go vet`) |
| Source files | Several files on the command line are compiled into one image |
| Entry point | After the global initializers run, call `main(args)`: `args` is a vector of strings (the parameter is optional). An integer return value becomes the exit code |
| Truthiness | `nil`, `false`, `0` and `0.0` are false; everything else is true, including `""`, `[]` and `{}` |
| Names | Globals are declared with `global`, constants with `const`. Every identifier must resolve at compile time; an unknown name is a compile error. Constants, functions, classes, built-ins and `this` cannot be assigned |
| Runtime errors | Catchable exceptions. Uncaught ones print a message and a stack trace (file:line) and exit with code 1 |
| Strings | Immutable byte strings (NUL allowed). `s[i]` gives the byte as an int; `s[i] = x` is an error |
| Integers | int64 with two's-complement wraparound. int op float gives float. int / int truncates toward zero. `/` or `%` by integer 0 throws. A negative or ≥64 shift count throws |
| Constructors | `new C(a, b)` creates the object with all data members `nil`, then calls `C::C(a, b)`; a class that declares no constructor uses its nearest base class's (Q33). Passing arguments when there is no constructor at all is an error. Base constructors are called explicitly as `Base::Base(args)` (Q3) |
| Objects | `p->x` reads/assigns a data member (all members are public). `p->m` without `(` gives a bound method |
| Closures | `fn (params) { ... }` literals capture by reference (upvalues). Loop variables are fresh per iteration |
| Built-ins (v1) | I/O, strings and collections, type and conversion, math and system (list in §8) |

---

## 1. Language decisions (all answered)

- Q1 CLI/binary name and file extensions: **`kukel`, source `.kkl`, bytecode `.kkc`**
- Q2 `==` on vectors, maps and objects: **identity** (numbers by value across int/float, strings by bytes)
- Q3 `Base::m(args)` inside a method of a derived class: **non-virtual call to Base's method with the current `this`** (C++ style; this is how base constructors get called). Elsewhere, `C::f()` calls a static function
- Q4 Value of `&&` / `||`: **`true`/`false`** (C style)
- Q5 `+` with strings: **string + string concatenates**; string + number is a type error (use `str()`)
- Q6 Map iteration order (`keys()`, for-each): **insertion order**
- Q7 Method lookup for `obj->m()`: **dynamic, by name, up the base-class chain** (all methods virtual)
- Q8 Unqualified `f()` inside a method, when `f` is a member function: **means `this->f()`** (follows the identifier resolution order in the EBNF)
- Q9 A `case` label inside a `try` block nested in a switch body (a jump into a try): **compile error**
- Q10 Relational operators: **numbers with numbers, strings with strings (byte order)**; any other mix throws
- Q11 Float formatting in `print`/`str`: **shortest round-trip (`strconv.FormatFloat(f,'g',-1,64)`), always showing a `.` or exponent**
- Q12 Bytecode compatibility: **version number in the header; a mismatch is refused** (no migration)
- Q13 How a thrown runtime error looks: **an `Error` object with `message`, `kind` and `trace`** (milestone 7: `Error` is a real class from a small prelude compiled in front of every program, `class Error { message, kind, trace; Error(message = nil, kind = nil); }`; programs may throw it and derive from it)
- Q14 Can `print(obj)` call a user method such as `toString`? **No in v1**
- Q15 Closure capture: **by reference** (Lua/JS/Go style upvalues)
- Q16 Loop variables and closures: **fresh binding per iteration** for both for-each and `for (local ...)` (Go 1.22 semantics)
- Q17 `p->m` where `m` is a method: **bound method value**; assigning to it is an error
- Q18 Built-in names: **assigning to one, or defining a global/function/class/const with its name, is a compile error**
- Q19 Member function definitions: **shape must match the declaration** (parameter count incl. optional, rest or not); names may differ
- Q20 Case labels that skip a `local` initializer: **compile error** (C++ rule)
- Q21 (decided in milestone 5) Reading a missing map key: **gives nil** (use `haskey` to tell a missing key from a nil value)
- Q22 (decided in milestone 5) Vector and string indexes: **ints from 0 to len-1 only** (no negative indexes; `v[len(v)] = x` is an error, use `push`)
- Q23 (decided in milestone 5) Built-in details: `push(v, x..)` returns the new length; `sort(v [, less])` sorts in place (stable) and returns nil, `less(a, b)` is true when a goes first; `join` turns non-strings into text like `str`; `split(s, "")` gives single bytes; `upper`/`lower`/`trim` work on ASCII only; `remove(v, i)` returns the removed element; `delete(m, k)` returns whether the key existed
- Q24 (decided in milestone 6) Inside member functions an unqualified name that equals the class's name means the class, not the constructor: constructors are not found by unqualified member lookup; call them with `new C(...)` or `Base::Base(...)`. `new X(...)` looks `X` up among locals and globals only
- Q25 (decided in milestone 6) A member function may be declared without being defined (like an abstract method): calling it is a run-time `MemberError` "C::m is declared but not defined"; `C::f` / `Base::m(...)` of such a function is a compile error
- Q26 (decided in milestone 6) A derived class may redeclare a member function of a base class (overriding it) with the same static-ness; redeclaring a data member, or turning a member function into a data member, is a compile error
- Q27 (decided in milestone 6) Objects print as `Point{x: 1, y: 2}` (also inside containers; an object inside itself prints `Point{...}`); bound methods print as `<method C::m>`; `p->m == p->m` is true (same object, same method)
- Q28 (decided in milestone 6) A static member function can also be called through an object (`p->f()`, without `this`); `p->f` of a static one gives the plain function. Spread arguments are not supported in `Base::m(...)` calls
- Q29 (decided in milestone 7) Any value can be thrown; `catch (e)` catches everything (there are no typed catch clauses; test with `instanceof(e, SomeError)` and rethrow with `throw e;`). The first time an `Error` object is thrown, the VM sets its `trace` (a vector of strings like `"f (file.kkl:3)"`, innermost call first) and, if `kind` is nil, sets `kind` to the name of its class; throwing it again keeps the original trace. An uncaught `Error` prints `kind: message` and the trace; any other uncaught value prints `uncaught exception: <value>`
- Q34 (decided 2026-10-13) Built-in details: `eprint` and `eprintln` (to standard error; both flush standard output first, as `readline` does); `floor`/`ceil`/`round` give ints (`RangeError` if the float does not fit; `round` takes halves away from zero); `pow` gives an int for two ints with a non-negative exponent (wrapping like `*`), else a float; `exit(code)` ends the program at once (not catchable, `finally` blocks do not run); `int("...")` reads literals (`42`, `-0x10`, `0b11`, `1_000`, surrounding spaces) and gives a `ValueError` for anything else; `min`/`max` take several numbers or strings, or one vector; random numbers come from a per-program source that `seed(n)` restarts
- Q33 (decided 2026-10-13, option A) A class that declares no constructor inherits the nearest base class's constructor: `class ValidationError : Error { }` makes `new ValidationError("msg")` run `Error::Error("msg")`. A class with its own constructor is unaffected (it calls `Base::Base(...)` itself)
- Q32 (decided in milestone 9) A call whose callee is known at compile time — a top-level function, a static member function (`C::f(...)` or unqualified inside the class), `Base::m(...)`, or `new C(...)` naming a class — with the wrong number of arguments is a compile error (the message is the same as at run time). Calls through variables, members, spread arguments and virtual calls (`p->m(...)`) are checked at run time. `new C(...)` of a class whose constructor is declared but not defined is a compile error too
- Q31 (decided in milestone 8) A function literal is named `<enclosing function>/fn@<line>` in traces and when printed (`main/fn@12`); closures are equal only to themselves
- Q30 (decided in milestone 7) An exception thrown in a `finally` block replaces the one that was pending; a `case`/`default` label inside a try, catch or finally block of its switch is a compile error (checked before the local-initialization rule)

## 1b. Syntax changes in version 2 (all adopted)

1. `this` is a keyword (an expression), not an identifier
2. `p->x` data member access (read/assign), `p->m` bound method; `->` + `(` is always a method call, `(p->f)(x)` calls a function stored in a member
3. `global x, y = 0;` top-level declarations; any use of an undeclared name is a compile error
4. An unclosed `/*` comment is an error
5. Only the defined escapes: `\n \t \r \0 \\ \" \' \xH[H]`; others are errors
6. `local` in a `for` initializer: `for (local i = 0; ...)`, scoped to the loop
7. For-each: `for (local e in v)`, `for (local k, v in m)`, also over strings (bytes)
8. Spread `v..` in call arguments and vector literals
9. `const NAME = constant-expression;` at top level, usable in `case` labels (case values are constant expressions)
10. `new C()->m()` is allowed (postfix after `new`)
11. Nested unary operators: `!!x`, `- -x`, `!-x`
12. Binary literals `0b1010` and `_` digit separators (`1_000_000`, `0xFF_FF`)
13. `'''` is an error; the quote character is `'\''`
14. Map keys stay evaluated (`{name: 1}` uses the variable); documented in the EBNF and the language reference
15. Function literals / closures: `fn (params; temps) { ... }`
- New keywords: `const`, `fn`, `global`, `in`, `this`

---

## 2. Project setup (approved)

- [x] `go mod init` (module name `kukel`), Go ≥ 1.22
- [x] Layout (packages are created by the milestone that needs them):
  ```
  cmd/kukel/          CLI
  internal/token/     token kinds, positions
  internal/lexer/
  internal/ast/
  internal/parser/
  internal/compiler/  AST → bytecode
  internal/bytecode/  opcodes, image structs, (de)serialization, disassembler
  internal/value/     runtime value types
  internal/vm/        interpreter loop, call frames, upvalues, exceptions
  internal/builtins/
  tests/              .kkl programs + expected output
  docs/               language reference
  ```
- [x] CLI subcommands (`tokens` works; the others report the milestone that adds them):
  - `kukel run a.kkl b.kkl [-- args]`: compile in memory and run
  - `kukel build -o prog.kkc a.kkl b.kkl`: compile to a file
  - `kukel exec prog.kkc [args]`: run a stored image
  - `kukel disasm prog.kkc`: dump the bytecode in readable form
  - `kukel check a.kkl ...`: compile only and report errors
  - `kukel tokens a.kkl` / `kukel ast a.kkl`: debug dumps
- [x] Makefile: `all`, `build` (→ `bin/kukel`), `test`, `vet` (incl. gofmt check), `fmt`, `fuzz` (`FUZZTIME=30s`), `bench`, `golden`, `clean`

---

## 3. Lexer (`internal/lexer`), following §4 of syntax.ebnf (approved)

- [x] Token struct: kind, text/value, file, line, column
- [x] Skip white space (space, \t, \n, \r, \f, \v) and comments:
  - `/* ... */` not nested; an unclosed one is an error "unterminated comment" (reported at its start)
  - `// ...` and `# ...` up to the end of the line or file (so `#!` works on line 1)
- [x] Identifiers `[A-Za-z_][A-Za-z0-9_]*`, at most 255 characters (error otherwise); keyword table → keyword tokens (incl. `const fn global in this`)
- [x] Integers: decimal (keep the value as uint64 + a flag, because 2^63 is valid only after a unary minus, see §4), hex `0x` with 1–16 digits and binary `0b` with 1–64 digits, both taken as a 64-bit pattern (`0xFFFFFFFFFFFFFFFF` = -1). Leading zeros are decimal
  - `_` only between two digits, ignored (`1_000`, `0xFF_FF`); `1__0`, `1_`, `0x_1` are errors; digit counts exclude `_`
- [x] Floats: `1.5  1.  .5  2e10  1.5E-3  1_000.25`; "Number too large" if out of range; at most 255 characters
  - Careful: `1..` is `1.` then `.` (an error), following the longest-match rule; `v..` after an identifier/`]`/`)` is the spread token
- [x] Character `'c'`: exactly one possibly escaped character; value = int code; `'''` is an error (write `'\''`)
- [x] String `"..."`: any length, literal newlines allowed, NUL allowed; adjacent strings are not joined
- [x] Escapes: `\n \t \r \0 \\ \" \'`, `\x` + 1–2 hex digits; any other escape is an error
- [x] Operators with longest match: `<<=` before `<<` before `<`, `::` before `:`, `..` before `.`; a lone `.` is an error
- [x] A raw NUL byte in a source line ends that line
- [x] Errors with file:line:col
- [x] Beyond the grammar: a number directly followed by a letter, digit or `_` is a lexer error (`12abc`, `0x1g`, `0b12`), and non-ASCII bytes outside strings and comments are errors
- [x] `kukel tokens` golden test: `tests/tokens/*.kkl` → `.tokens` (`make golden` rewrites them)
- [x] Tests: a table-driven test for each token kind plus the edge cases above; a Go fuzz test (`FuzzLexer`) that must never panic

## 4. AST + parser (`internal/ast`, `internal/parser`)

- [x] AST node types for every rule in §1–3 of the grammar, each with a position
- [x] Recursive descent for definitions and statements:
  - top level: class, function, member-function definitions, `global` and `const` declarations
  - class definition (no `;` after `}`), member declarations (`static`, data vs. function declarations, `..` in declared parameters, `=` in a declaration is an error)
  - function / member-function definitions with `parameter_part` (`params ; temporaries`, both optional)
  - all statements; `elseif` desugars to `else if`
  - a `{` at the start of a statement is always a block
  - labelled statement: IDENT followed by a `:` token **on the same line** (the lexer already separates `::`)
  - `case <constant expression at logical_or level>:` and `default:` are statements in their own right (they may appear nested, C style)
  - `for (` → after `local IDENT [, IDENT]` look ahead: `in` = for-each, otherwise a local list as for initializer
- [x] Expressions: precedence climbing or one function per level, with this exact structure:
  - comma < assignment (right associative) < `?:` (middle part = full expression, last part = assignment) < `||` < `&&` < `|` < `^` < `&` < equality < relational < shift < additive < multiplicative < unary < postfix < primary
  - unary operators nest: `!!x`, `- -x`; `--x` is a decrement
  - fold `-` followed directly by an Integer/Float/Character literal into one literal (this allows `-9223372036854775808`); not when a postfix operator follows the literal (`-5[0]` is `-(5[0])`)
  - postfix: call, index, `->name(args)` (method call), `->name` (member / bound method), `++`, `--`
  - arguments and vector elements may be spread: `e..`
  - primary: literals, `this`, `( )`, vector, map (only in expression position), `new C(args)` (postfix may follow), `fn (params) block`, `C::x`, identifier
- [x] Good error messages ("expected ';' after expression at f.kkl:3:14"); stop at the first error (v1)
- [x] `kukel ast [-pos]` tree printer (`-pos` adds `@line:col` to each node)
- [x] Nesting limit: more than 1000 levels of statements or expressions is a syntax error ("nesting too deep") instead of a Go stack overflow
- [x] Shared error type `internal/diag` (`file:line:col: message`) for lexer, parser and later passes
- [x] Tests: precedence/associativity table (S-expressions), literal folding values, statement shapes, 35 error cases with positions, `FuzzParser`; golden `tests/ast/*.kkl` → `.ast` (`make golden`)

## 5. Semantic checks + scope resolution (done in `internal/compiler`)

All the "Rule:" comments in syntax.ebnf. The checks run while the compiler resolves names and generates code, so there is no separate checker pass. `internal/compiler/rules_test.go` has valid and invalid programs for every rule with a "Rule:" comment, and a test that fails when syntax.ebnf gets a rule without test cases.

- [x] One top-level name space: globals, constants, functions, classes and built-ins; each name defined once (Q18) — done in the compiler (milestone 3)
- [x] Constants: evaluate in program order at compile time (literals, earlier constants, unary/binary/`?:`); result must be nil/bool/number/string; errors (e.g. division by zero) are compile errors
- [x] The base class must already be defined (in file order, across files in command-line order)
- [x] `C::m` definition: class C must exist and declare `m`; shape matches the declaration: same number of parameters (required + optional; temporaries not counted) and a rest `..` in both or neither; names may differ (Q19)
- [x] `this` only in non-static member functions and in function literals nested in them
- [x] Parameters: once an optional parameter appears, all later ones need defaults (or are the rest parameter); the rest parameter is last; at most 255 optional parameters; a default may use earlier parameters (only those: a later name resolves as if the parameter did not exist) — milestone 4
- [x] Lvalues: the target of an assignment or `++`/`--` must be a variable (global, local, parameter, temporary, captured, member), `C::x`, `e[i]` or `e->x`; not a constant, function, class, built-in or `this`
- [x] Argument counts of callees known at compile time (Q32)
- [x] `kukel check a.kkl ...`: compile only, report the first error, run nothing (silent when fine)
- [x] `local`: only directly inside a block or in a for initializer, not directly in a switch body, no duplicate in the same block; the name is visible *after* its initializer
- [x] `catch (e)`: `e` is a new local of the catch block
- [x] `case`/`default`: only inside a switch body; case values are constant expressions; no two equal case values (1 and 1.0 are equal); at most one default
- [x] Q9: reject `case` labels that would jump into a try block (also catch and finally blocks)
- [x] Reject `case`/`default` labels that skip the initialization of a `local` still in scope at the label (Q20): "case label skips the initialization of local `y`"
- [x] `break`/`continue`: a plain break needs a loop or switch, a plain continue a loop; a label must name an enclosing statement (a loop, for continue) in the same function (they never cross a `fn` literal); a label may not repeat the label of an enclosing statement
- [x] `finally`: no `return`, and no break/continue to a target outside it
- [x] `new X(...)`: X is a class name or a variable (checked at run time if it's a variable)
- [x] Identifier resolution in order: block local → parameter → temporary → variable of an enclosing function (closure capture) → member of the method's class (incl. inherited) → top-level name. Unresolved = compile error. (Done by the compiler while generating code.)
- [x] Closure analysis: mark every local/parameter/temporary that is captured by a `fn` literal (these live in heap cells; the others stay in plain slots). Done by name: a function's locals whose names occur anywhere inside its nested literals get cells (a safe over-approximation; exact resolution happens during code generation)

## 6. Runtime values (`internal/value`)

- [x] `Value` representation: a tagged struct `{kind, num uint64, obj any}` (32 bytes), so numbers never allocate
- [x] Kinds (Nil, Bool, Int, Float, String, Vector, Map, Function, Closure, Builtin, Class, Object, BoundMethod; internal: Iterator, Cell, Internal; Error is a prelude class): Nil, Bool, Int, Float, String (immutable `string`), Vector (`*[]Value`), Map, Class, Object, Function (code + captured upvalue cells), BoundMethod (object + method), Builtin, Error (Q13: `message`, `kind`, `trace`)
- [x] `typeof`: Function, BoundMethod and Builtin all report `"function"`
- [x] Map: keys are strings and numbers only; a float key with an integral value is normalized to an int key (so 1 and 1.0 are the same key; an existing key keeps its original form); insertion order kept (Q6); deleted entries are compacted when more than half are gone; NaN is not a key
- [x] Printing: containers inside themselves print as `[...]` / `{...}`
- [x] Equality, truthiness, ordering, `typeof` names, string conversion (Q2, Q10, Q11); int/float comparisons are exact (no rounding through float64); floats print as `1.0`, `1e+21`, `inf`, `-inf`, `nan`; inside containers strings are quoted with Kukel escapes
- [x] All operator semantics live in `internal/value` and are shared by the VM and the compiler's constant folding
- [x] Memory: rely on Go's GC

## 7. Bytecode + compiler (`internal/bytecode`, `internal/compiler`)

### Instruction set (1-byte opcode + fixed little-endian operands: u16 for indices and counts, u32 for absolute jump targets)
- [ ] Stack: `CONST k`, `NIL`, `TRUE`, `FALSE`, `POP`, `DUP`, `DUP2` (done; `e[i] op= x` uses `DUP2`, and `e[i]++` as a value keeps the old value in a hidden slot), `SWAP`/`ROT` if `e->x` needs them
- [x] Variables: `LOAD_LOCAL i`/`STORE_LOCAL i` (params, temporaries, locals and catch variables share frame slots), `LOAD_GLOBAL g`/`STORE_GLOBAL g`, `LOAD_MEMBER i`/`STORE_MEMBER i` (on `this`, slot known at compile time), `LOAD_STATIC c,i`/`STORE_STATIC c,i`, `LOAD_THIS`
- [x] Fields on any object: `GET_MEMBER name` (data member, else bound method, else error), `SET_MEMBER name` (data member only); slot lookup by name per class, cached later. `this->x` and unqualified `x` of a known data member compile to `LOAD_MEMBER`/`STORE_MEMBER slot`
- [x] Closures: `CLOSURE fn` (the capture list, local slot or enclosing upvalue, is in the function's `Upvalues`), `LOAD_UPVALUE i`/`STORE_UPVALUE i`, `LOAD_CELL i`/`STORE_CELL i` / `NEW_CELL i` for captured locals in the defining frame
- [x] Arithmetic/logic: `ADD SUB MUL DIV MOD NEG NOT BITNOT BITAND BITOR BITXOR SHL SHR EQ NE LT LE GT GE INC DEC` (int fast paths in the VM)
- [x] Control: `JUMP off`, `JUMP_IF_FALSE off`, `JUMP_IF_TRUE off` (for `&&`, `||`, `?:`) (`&&`/`||` produce `true`/`false`, Q4)
- [x] Calls: `CALL argc`, `CALL_V`, `CALL_METHOD name argc`, `CALL_METHOD_V name`, `CALL_DIRECT func argc` (Q3: `Base::m(...)`, a non-virtual call with the current `this`), `LOAD_FUNC func` (static member functions are plain function values), `NEW argc` / `NEW_V` (the class is a value on the stack, so a class in a variable needs nothing extra), `RETURN`, `RETURN_NIL`
- [ ] Spread: calls with a spread argument build an argument vector (`MAKE_VECTOR` + `APPEND`/`EXTEND`) and use `CALL_V` (done) / `CALL_METHOD_V` / `NEW_V`; vector literals with spread use `EXTEND` (done)
- [x] Collections: `MAKE_VECTOR n`, `APPEND`, `EXTEND`, `MAKE_MAP n`, `INDEX_GET`, `INDEX_SET` (leaves the value, so `x = v[i] = y` works)
- [x] For-each: `ITER_INIT` (vector: index; map: key snapshot; string: index; else throw), `ITER_NEXT slot, n, off` (the iterator lives in a hidden slot; pushes 1 or 2 values, or jumps to `off` at the end)
- [x] Exceptions: `TRY_PUSH kind, handler` (kind catch: the handler gets the thrown value; kind finally: it gets the pending exception, which `RETHROW` throws again unchanged), `TRY_POP`, `THROW`, `RETHROW`
- [x] Every instruction maps to a source line (a run-length line table per function) for stack traces

### Compiler
- [x] One code object per function/method/fn literal: name, class, params (required count, optional count, has-rest), number of slots, upvalue descriptors, max stack depth (computed), code, constants, line table
- [x] Function prologue: check argc (too few required args, or too many without a rest parameter → error); evaluate defaults for missing optional args in order (`JUMP_IF_ARG n` skips a default whose argument was passed, so an explicit `nil` stays `nil`); pack extra args into a vector for the rest parameter (done by the VM's call)
- [x] Statements leave the stack empty (checked after every statement); expression statement = expression + `POP`, except assignments and `++`/`--`, which skip the value
- [x] Short-circuit `&&`, `||`, `?:`; conditions compile to jumps (no boolean is built for `if (a && !b)`)
- [x] Comma operator: evaluate and `POP` all but the last
- [x] Constants are inlined as `CONST` (evaluated by the compiler); `-literal` folded
- [x] Compound assignment and `++`/`--` (pre and post) for each lvalue kind (local, cell, upvalue, global, member, this-field, static, index) (local, cell, upvalue, global, member, static, index, field); the index and field forms evaluate `e` (and `i`) only once
- [x] Loops: `while`, `do-while`, `for` (an empty test = loop forever), for-each; break/continue targets, with labels, patched via jump lists; a label on any other statement lets `break label` leave it
- [x] Per-iteration loop variables (Q16): only when the variable is captured by a closure, give each iteration a new cell (copy the old value before the update for `for`); uncaptured variables keep a plain slot (no cost)
- [x] `switch`: evaluate the value into a hidden slot, then a chain of `EQ` + jump to each case label's address (labels may sit inside nested statements, Duff's-device style); jump to `default` or past the end. The dispatch code is placed after the body, so no patching per case is needed. Later, maybe: a jump table for dense int cases
  - A case label that a jump would reach past a `local` declaration still in scope is rejected by the checker (§5), so no slot clearing is needed
- [x] `try/catch/finally`: `TRY_PUSH` handler; the catch body binds the exception to a slot. `finally` is compiled inline on every way out (normal end, catch end, and break/continue/return passing through), plus a catch-all handler that runs the finally code and then does `RETHROW`
- [x] Classes in the image: name, base, data member names (instance slot layout incl. inherited members), static member slots, method table (name → code index, static flag)
- [x] Program init: functions, classes and built-ins bound to their global slots; then one generated init function runs the `global` initializers in program order; then `main` (built-ins get a global slot only when used)

### Bytecode file `.kkc`
- [x] Format (little-endian, uvarints; the layout is documented in `internal/bytecode/file.go`):
  `magic "KUKL"`, `format version` (1; another version is refused, Q12), `flags`, main, globals, classes, init function, functions (code, constants, upvalue descriptors, line tables, slot names, source file names), CRC-32 at the end
- [x] Writer + reader with a round-trip test (compile → write → read → write gives the same bytes, and the same disassembly) for every program in `tests/run`
- [x] Validate on load: opcodes, operand ranges, jump targets inside the code (never trust the file). `bytecode.Verify` also follows every path: the stack depth and the number of try handlers must agree at each instruction, never underflow, stay within MaxStack; no function may run off its end or return with a handler registered. Every program the compiler makes is verified in the tests (a cross-check of the compiler). The VM adds cheap guards for what the verifier cannot see (a slot holding the wrong kind of value)
- [x] `kukel disasm` (of `.kkl` sources and `.kkc` files)
- [x] `kukel build [-o prog.kkc] a.kkl ...` (default output: the first file with `.kkc`) and `kukel exec prog.kkc [--] [args]`; `*.kkc` is in `.gitignore`

## 8. VM (`internal/vm`) + built-ins (`internal/builtins`)

### VM
- [x] Value stack (grows as needed, sized by each function's MaxStack), frames (function/closure, ip, base pointer, `this`), a global array, static member storage per class
- [x] Main loop: `switch op` dispatch; keep the hot path free of allocations (milestone 3: fib(27) ≈ 29 ms, 1M-iteration loop ≈ 40 ms)
- [x] Calls: user functions, closures, bound methods, built-ins, classes via `new` (allocate the object, then call the constructor if one exists; the constructor's frame returns the object)
- [x] Upvalues: captured variables live in heap cells shared by the frame and the closures (simple and GC-friendly in Go; no open/closed upvalue juggling needed)
- [x] Method dispatch by name up the class chain (Q7): each class gets a flattened method table at load time, so dispatch is one map lookup (milestone 6: ~40 ns per method call)
- [x] Exceptions: a handler stack (handlers record their frame, target and stack depth; exceptions also cross built-ins such as `sort`'s comparator); unwind frames; runtime errors (type errors, bad index, division by zero, unknown method or member, wrong argument count, spread of a non-vector, for-each over a non-iterable, ...) become thrown values (Q13)
- [x] Uncaught exception: print the message and trace (function, file:line for each frame) to stderr; exit code 1 (milestone 3: every runtime error is uncaught; a trace longer than 21 frames shows the first and last 10)
- [x] Deep recursion: frame depth limit (10000 frames) → a catchable "stack overflow" error
- [x] Integer semantics: wraparound; `MinInt64 / -1` and `% -1` don't panic in Go (handle them explicitly); a negative or ≥64 shift count throws

### Built-ins (v1; all done, documented in `docs/reference.md` section 10)
- [x] I/O (arguments are separated by one space, strings unquoted): `print(...)` (no newline), `println(...)`, `readline()` (nil at EOF), `readfile(path)`, `writefile(path, s)`, `appendfile(path, s)`, `eprint(...)`, `eprintln(...)`
- [x] Strings and collections (all done in milestone 5; built-ins can call back into Kukel through `Env.Call`, used by `sort`): `len(x)`, `substr(s, start [, len])`, `find(s, sub [, from])`, `split(s, sep)`, `join(vec, sep)`, `upper`/`lower`, `trim`, `replace(s, old, new)`, `char(code)`, `ord(s [, i])`, `push(v, x)`, `pop(v)`, `insert(v, i, x)`, `remove(v, i)`, `slice(v, a, b)`, `sort(v [, cmpfn])` (cmpfn may be a closure or bound method), `keys(m)`, `values(m)`, `haskey(m, k)`, `delete(m, k)`
- [x] Type and conversion: `typeof(x)` (returns "nil", "bool", "int", "float", "string", "vector", "map", "class", "object", "function"), `int(x)`, `float(x)`, `str(x)`, `isnil(x)`, `classof(obj)`, `instanceof(obj, C)`
- [x] Math and system: `abs`, `min`, `max`, `sqrt`, `pow`, `floor`, `ceil`, `round`, `sin`/`cos`/`tan`/`atan2`, `log`/`exp`, `random()` / `randint(a, b)` / `seed(n)`, `time()`, `clock()`, `exit(code)`, `getenv(name)`; command-line arguments come through `main(args)`
- [x] Built-in names live in the top-level name space; redefining or assigning one is a compile error (Q18)

## 9. Tests, examples, docs

- [x] `tests/run/*.kkl` + `.out` (expected stdout) and `.err` (expected stderr); `// args:` and `// exit:` comment lines; a Go test runs all of them through `run` and through `build`+`exec`, which must agree
- [x] Coverage list: every statement kind, every operator incl. wraparound and int/float mixing, default/rest parameters, temporaries, globals and constants (incl. use before declaration, constant folding errors), classes/inheritance/static members/constructors, `p->x` and bound methods, labelled break/continue, switch fall-through, nested case labels and constant case values, for-each over vector/map/string (incl. mutation during the loop), spread in calls/new/vectors, closures (shared captured variables, counters, closures in loops), try/catch/finally with break/continue/return through the finally, runtime errors being caught, uncaught error trace, maps with 1/1.0 keys
- [x] Error tests for every compile-time rule (unknown name, assignment to a constant, unterminated comment, bad escape, `'''`, `1__0`, ...)
- [x] Example programs (in `tests/run`, all golden-tested): fizzbuzz, primes, word count, word frequencies from stdin (`.in` files are a test's standard input), a class hierarchy, a JSON parser and pretty-printer, closures (events, compose, memoize), exceptions, control flow
- [x] Fuzzing (`make fuzz`): lexer, parser, compiler, bytecode loader (`FuzzDecode`: mutated files) and verifier + VM (`FuzzVerifyCode`: arbitrary code inside real programs, run with `vm.StepLimit`); found and fixed: TRY_POP without a handler, loops the step limit did not count
- [x] Benchmarks (`make bench`): fib(27), 1M-iteration loop, 1M method calls, 1M closure calls, string building, maps and vectors. Milestone 11 numbers: 36 / 43 / 83 / 70 / 41 / 32 ms; every built-in call allocates a copy of its arguments (600k allocations in the string benchmark) — a target for milestone 12
- [x] `docs/reference.md`: language reference (incl. the evaluated map keys, `->` rules, closure semantics, for-each mutation rules). Tests keep it honest: every built-in must be described, and every example with a `main` must run

## 10. Milestones (suggested order)

1. [x] Setup + lexer + `kukel tokens` (done 2026-10-12)
2. [x] Parser + AST + `kukel ast` (done 2026-10-12, branch `milestone-2-parser`)
3. [x] Values + compiler + VM for expressions, globals, constants, functions, `if`/`while`/`for`, `print`: run fizzbuzz (done 2026-10-13, branch `milestone-3-vm`; also basic locals, parameters and temporaries, `do`, `break`/`continue`, `kukel disasm` of sources)
4. [x] Default/rest parameters, spread, labelled break/continue, switch (done 2026-10-13, branch `milestone-4-control`; basic locals, for-initializer locals and temporaries were done in milestone 3)
5. [x] Vectors, maps, strings + their built-ins; for-each (done 2026-10-13, branch `milestone-5-collections`)
6. [x] Classes, objects, methods, `this`, `p->x`, bound methods, static members, constructors, inheritance (done 2026-10-13, branch `milestone-6-classes`; frames got bigger: fib(27) 29 → 35 ms, to win back in milestone 12)
7. [x] Exceptions: try/catch/finally, runtime errors as exceptions, stack traces (done 2026-10-13, branch `milestone-7-exceptions`)
8. [x] Closures: fn literals, captured cells, per-iteration loop variables (done 2026-10-13, branch `milestone-8-closures`; ~37 ns per closure call)
9. [x] Semantic checker complete (all "Rule:" items) (done 2026-10-13, branch `milestone-9-checker`: rules audit test, compile-time argument counts, `kukel check`)
10. [x] `.kkc` writer/reader/validator + `build`/`exec`/`disasm` (done 2026-10-13, branch `milestone-10-kkc`, on top of milestone 9; also Q33 constructor inheritance)
11. [x] Remaining built-ins, test suite, fuzzing, benchmarks, language reference (done 2026-10-13, branch `milestone-11-builtins`)
12. [ ] Later/optional: peephole optimizer, inline caches for methods and fields, jump tables for switch, a REPL, multiple error reporting
