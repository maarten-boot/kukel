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
| Constructors | `new C(a, b)` creates the object with all data members `nil`, then calls `C::C(a, b)` if that exists. Passing arguments to a class with no constructor is an error. Base constructors are called explicitly as `Base::Base(args)` (Q3) |
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
- Q13 How a thrown runtime error looks: **an `Error` object with `message`, `kind` and `trace`**
- Q14 Can `print(obj)` call a user method such as `toString`? **No in v1**
- Q15 Closure capture: **by reference** (Lua/JS/Go style upvalues)
- Q16 Loop variables and closures: **fresh binding per iteration** for both for-each and `for (local ...)` (Go 1.22 semantics)
- Q17 `p->m` where `m` is a method: **bound method value**; assigning to it is an error
- Q18 Built-in names: **assigning to one, or defining a global/function/class/const with its name, is a compile error**
- Q19 Member function definitions: **shape must match the declaration** (parameter count incl. optional, rest or not); names may differ
- Q20 Case labels that skip a `local` initializer: **compile error** (C++ rule)

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
  internal/check/     semantic checks, scope resolution, constant folding
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

- [ ] AST node types for every rule in §1–3 of the grammar, each with a position
- [ ] Recursive descent for definitions and statements:
  - top level: class, function, member-function definitions, `global` and `const` declarations
  - class definition (no `;` after `}`), member declarations (`static`, data vs. function declarations, `..` in declared parameters, `=` in a declaration is an error)
  - function / member-function definitions with `parameter_part` (`params ; temporaries`, both optional)
  - all statements; `elseif` desugars to `else if`
  - a `{` at the start of a statement is always a block
  - labelled statement: IDENT followed by a `:` token **on the same line** (the lexer already separates `::`)
  - `case <constant expression at logical_or level>:` and `default:` are statements in their own right (they may appear nested, C style)
  - `for (` → after `local IDENT [, IDENT]` look ahead: `in` = for-each, otherwise a local list as for initializer
- [ ] Expressions: precedence climbing or one function per level, with this exact structure:
  - comma < assignment (right associative) < `?:` (middle part = full expression, last part = assignment) < `||` < `&&` < `|` < `^` < `&` < equality < relational < shift < additive < multiplicative < unary < postfix < primary
  - unary operators nest: `!!x`, `- -x`; `--x` is a decrement
  - fold `-` followed directly by an Integer/Float/Character literal into one literal (this allows `-9223372036854775808`)
  - postfix: call, index, `->name(args)` (method call), `->name` (member / bound method), `++`, `--`
  - arguments and vector elements may be spread: `e..`
  - primary: literals, `this`, `( )`, vector, map (only in expression position), `new C(args)` (postfix may follow), `fn (params) block`, `C::x`, identifier
- [ ] Good error messages ("expected ';' after expression at f.kkl:3:14"); stop at the first error (v1)
- [ ] `kukel ast` pretty printer
- [ ] Tests: parse every construct; check the trees against expected dumps; error cases from the grammar comments

## 5. Semantic checks + scope resolution (`internal/check`)

All the "Rule:" comments in syntax.ebnf:

- [ ] One top-level name space: globals, constants, functions, classes and built-ins; each name defined once (Q18)
- [ ] Constants: evaluate in program order at compile time (literals, earlier constants, unary/binary/`?:`); result must be nil/bool/number/string; errors (e.g. division by zero) are compile errors
- [ ] The base class must already be defined (in file order, across files in command-line order)
- [ ] `C::m` definition: class C must exist and declare `m`; shape matches the declaration: same number of parameters (required + optional; temporaries not counted) and a rest `..` in both or neither; names may differ (Q19)
- [ ] `this` only in non-static member functions and in function literals nested in them
- [ ] Parameters: once an optional parameter appears, all later ones need defaults (or are the rest parameter); the rest parameter is last; at most 255 optional parameters; a default may use earlier parameters
- [ ] Lvalues: the target of an assignment or `++`/`--` must be a variable (global, local, parameter, temporary, captured, member), `C::x`, `e[i]` or `e->x`; not a constant, function, class, built-in or `this`
- [ ] `local`: only directly inside a block or in a for initializer, not directly in a switch body, no duplicate in the same block; the name is visible *after* its initializer
- [ ] `catch (e)`: `e` is a new local of the catch block
- [ ] `case`/`default`: only inside a switch body; case values are constant expressions; no two equal case values (1 and 1.0 are equal); at most one default
- [ ] Q9: reject `case` labels that would jump into a try block
- [ ] Reject `case`/`default` labels that skip the initialization of a `local` still in scope at the label (Q20): "case label skips the initialization of local `y`"
- [ ] `break`/`continue`: a plain break needs a loop or switch, a plain continue a loop; a label must name an enclosing statement (a loop, for continue) in the same function (they never cross a `fn` literal); a label may not repeat the label of an enclosing statement
- [ ] `finally`: no `return`, and no break/continue to a target outside it
- [ ] `new X(...)`: X is a class name or a variable (checked at run time if it's a variable)
- [ ] Identifier resolution in order: block local → parameter → temporary → variable of an enclosing function (closure capture) → member of the method's class (incl. inherited) → top-level name. Unresolved = compile error. Record the result on the AST node (local slot, upvalue index, member, static, global index, constant value)
- [ ] Closure analysis: mark every local/parameter/temporary that is captured by a `fn` literal (these live in heap cells; the others stay in plain slots)

## 6. Runtime values (`internal/value`)

- [ ] `Value` representation: a tagged struct (kind + int64/float64 + pointer) to keep allocation low, or a Go interface (simpler; start with this and measure)
- [ ] Kinds: Nil, Bool, Int, Float, String (immutable `string`), Vector (`*[]Value`), Map, Class, Object, Function (code + captured upvalue cells), BoundMethod (object + method), Builtin, Error (Q13: `message`, `kind`, `trace`)
- [ ] `typeof`: Function, BoundMethod and Builtin all report `"function"`
- [ ] Map: keys are strings and numbers only; a float key with an integral value is normalized to an int key (so 1 and 1.0 are the same key); insertion order kept (Q6)
- [ ] Equality, truthiness, ordering, `typeof` names, string conversion (Q2, Q10, Q11)
- [ ] Memory: rely on Go's GC

## 7. Bytecode + compiler (`internal/bytecode`, `internal/compiler`)

### Instruction set (first draft; 1-byte opcode + operands as uvarint or fixed u16/u32)
- [ ] Stack: `CONST k`, `NIL`, `TRUE`, `FALSE`, `POP`, `DUP`, `DUP2`, `SWAP`/`ROT` (for compound assignment to `e[i]` and `e->x`)
- [ ] Variables: `LOAD_LOCAL i`/`STORE_LOCAL i` (params, temporaries, locals and catch variables share frame slots), `LOAD_GLOBAL g`/`STORE_GLOBAL g`, `LOAD_MEMBER i`/`STORE_MEMBER i` (on `this`, slot known at compile time), `LOAD_STATIC c,i`/`STORE_STATIC c,i`, `LOAD_THIS`
- [ ] Fields on any object: `GET_FIELD name` (data member, else bound method, else error), `SET_FIELD name` (data member only); slot lookup by name per class, cached later
- [ ] Closures: `CLOSURE fn, n` + capture list (local slot or enclosing upvalue), `LOAD_UPVALUE i`/`STORE_UPVALUE i`, `LOAD_CELL i`/`STORE_CELL i` / `NEW_CELL i` for captured locals in the defining frame
- [ ] Arithmetic/logic: `ADD SUB MUL DIV MOD NEG NOT BITNOT AND OR XOR SHL SHR EQ NE LT LE GT GE`
- [ ] Control: `JUMP off`, `JUMP_IF_FALSE off`, `JUMP_IF_TRUE off` (for `&&`, `||`, `?:`) (`&&`/`||` produce `true`/`false`, Q4)
- [ ] Calls: `CALL argc`, `CALL_METHOD name argc`, `CALL_SUPER class,name argc` (Q3), `CALL_STATIC class,fn argc`, `NEW class argc`, `NEW_DYN argc` (class in a variable), `RETURN`, `RETURN_NIL`
- [ ] Spread: calls with a spread argument build an argument vector (`MAKE_VECTOR` + `EXTEND`) and use `CALL_V` / `CALL_METHOD_V` / `NEW_V`; vector literals with spread use `EXTEND`
- [ ] Collections: `MAKE_VECTOR n`, `MAKE_MAP n`, `EXTEND`, `INDEX_GET`, `INDEX_SET`
- [ ] For-each: `ITER_INIT` (vector: index; map: key snapshot; string: index; else throw), `ITER_NEXT off` (pushes 1 or 2 values, or jumps to `off` at the end)
- [ ] Exceptions: `TRY_PUSH handler_off`, `TRY_POP`, `THROW`, `RETHROW`
- [ ] Every instruction maps to a source line (a run-length line table per function) for stack traces

### Compiler
- [ ] One code object per function/method/fn literal: name, class, params (required count, optional count, has-rest), number of slots, upvalue descriptors, max stack depth (computed), code, constants, line table
- [ ] Function prologue: check argc (too few required args, or too many without a rest parameter → error); evaluate defaults for missing optional args in order; pack extra args into a vector for the rest parameter
- [ ] Statements leave the stack empty; expression statement = expression + `POP`
- [ ] Short-circuit `&&`, `||`, `?:`
- [ ] Comma operator: evaluate and `POP` all but the last
- [ ] Constants are inlined as `CONST` (folded by the checker); `-literal` folded
- [ ] Compound assignment and `++`/`--` (pre and post) for each lvalue kind (local, cell, upvalue, global, member, static, index, field); the index and field forms evaluate `e` (and `i`) only once
- [ ] Loops: `while`, `do-while`, `for` (an empty test = loop forever), for-each; break/continue targets, with labels, patched via jump lists
- [ ] Per-iteration loop variables (Q16): only when the variable is captured by a closure, give each iteration a new cell (copy the old value before the update for `for`); uncaptured variables keep a plain slot (no cost)
- [ ] `switch`: evaluate the value into a hidden slot, then a chain of `EQ` + jump to each case label's address (labels may sit inside nested statements, Duff's-device style); jump to `default` or past the end. Later, maybe: a jump table for dense int cases
  - A case label that a jump would reach past a `local` declaration still in scope is rejected by the checker (§5), so no slot clearing is needed
- [ ] `try/catch/finally`: `TRY_PUSH` handler; the catch body binds the exception to a slot. `finally` is compiled inline on every way out (normal end, catch end, and break/continue/return passing through), plus a catch-all handler that runs the finally code and then does `RETHROW`
- [ ] Classes in the image: name, base, data member names (instance slot layout incl. inherited members), static member slots, method table (name → code index, static flag)
- [ ] Program init: functions, classes and built-ins bound to their global slots; then one generated init function runs the `global` initializers in program order; then `main`

### Bytecode file `.kkc`
- [ ] Format (little-endian, uvarints):
  `magic "KUKL"`, `format version`, `flags`, string table, constant pool (int/float/string), globals table (names), classes, functions (code, upvalue descriptors, line tables, source file names), init function, entry info, optional CRC32 at the end
- [ ] Writer + reader with a round-trip test (compile → write → read → deep-equal)
- [ ] Validate on load: opcodes, operand ranges, jump targets inside the code (never trust the file)
- [ ] `kukel disasm`

## 8. VM (`internal/vm`) + built-ins (`internal/builtins`)

### VM
- [ ] Value stack, frames (function/closure, ip, base pointer, `this`), a global array, static member storage per class
- [ ] Main loop: `switch op` dispatch; keep the hot path free of allocations
- [ ] Calls: user functions, closures, bound methods, built-ins, classes via `new` (allocate the object, then call the constructor if one exists)
- [ ] Upvalues: captured variables live in heap cells shared by the frame and the closures (simple and GC-friendly in Go; no open/closed upvalue juggling needed)
- [ ] Method dispatch by name up the class chain (Q7); cache lookups per class later
- [ ] Exceptions: a handler stack per frame; unwind frames; runtime errors (type errors, bad index, division by zero, unknown method or member, wrong argument count, spread of a non-vector, for-each over a non-iterable, ...) become thrown values (Q13)
- [ ] Uncaught exception: print the message and trace (function, file:line for each frame) to stderr; exit code 1
- [ ] Deep recursion: frame depth limit → a catchable "stack overflow" error
- [ ] Integer semantics: wraparound; `MinInt64 / -1` and `% -1` don't panic in Go (handle them explicitly); a negative or ≥64 shift count throws

### Built-ins (v1). Final names to be agreed; proposal:
- [ ] I/O: `print(...)` (no newline), `println(...)`, `readline()` (nil at EOF), `readfile(path)`, `writefile(path, s)`, `appendfile(path, s)`, `eprint(...)`
- [ ] Strings and collections: `len(x)`, `substr(s, start [, len])`, `find(s, sub [, from])`, `split(s, sep)`, `join(vec, sep)`, `upper`/`lower`, `trim`, `replace(s, old, new)`, `char(code)`, `ord(s [, i])`, `push(v, x)`, `pop(v)`, `insert(v, i, x)`, `remove(v, i)`, `slice(v, a, b)`, `sort(v [, cmpfn])` (cmpfn may be a closure or bound method), `keys(m)`, `values(m)`, `haskey(m, k)`, `delete(m, k)`
- [ ] Type and conversion: `typeof(x)` (returns "nil", "bool", "int", "float", "string", "vector", "map", "class", "object", "function"), `int(x)`, `float(x)`, `str(x)`, `isnil(x)`, `classof(obj)`, `instanceof(obj, C)`
- [ ] Math and system: `abs`, `min`, `max`, `sqrt`, `pow`, `floor`, `ceil`, `round`, `sin`/`cos`/`tan`/`atan2`, `log`/`exp`, `random()` / `randint(a, b)` / `seed(n)`, `time()`, `clock()`, `exit(code)`, `getenv(name)`; command-line arguments come through `main(args)`
- [ ] Built-in names live in the top-level name space; redefining or assigning one is a compile error (Q18)

## 9. Tests, examples, docs

- [ ] `tests/*.kkl` + `tests/*.out` (expected stdout, and `.err` / exit code for error tests); a Go test runs all of them through both `run` and `build`+`exec`
- [ ] Coverage list: every statement kind, every operator incl. wraparound and int/float mixing, default/rest parameters, temporaries, globals and constants (incl. use before declaration, constant folding errors), classes/inheritance/static members/constructors, `p->x` and bound methods, labelled break/continue, switch fall-through, nested case labels and constant case values, for-each over vector/map/string (incl. mutation during the loop), spread in calls/new/vectors, closures (shared captured variables, counters, closures in loops), try/catch/finally with break/continue/return through the finally, runtime errors being caught, uncaught error trace, maps with 1/1.0 keys
- [ ] Error tests for every compile-time rule (unknown name, assignment to a constant, unterminated comment, bad escape, `'''`, `1__0`, ...)
- [ ] Example programs: fizzbuzz, recursive fib, word count (file + map), a small class hierarchy, a JSON-ish pretty-printer, sort with a closure comparator
- [ ] Fuzzing: lexer, parser, bytecode loader (malformed `.kkc` must never crash the VM)
- [ ] Benchmarks: fib(30), loop of 10M additions, string building, closure calls; compare before and after optimizations
- [ ] `docs/reference.md`: language reference (incl. the evaluated map keys, `->` rules, closure semantics, for-each mutation rules)

## 10. Milestones (suggested order)

1. [x] Setup + lexer + `kukel tokens` (done 2026-10-12)
2. [ ] Parser + AST + `kukel ast`
3. [ ] Values + compiler + VM for expressions, globals, constants, functions, `if`/`while`/`for`, `print`: run fizzbuzz
4. [ ] Locals (incl. in for initializers), temporaries, default/rest parameters, spread, labelled break/continue, switch
5. [ ] Vectors, maps, strings + their built-ins; for-each
6. [ ] Classes, objects, methods, `this`, `p->x`, bound methods, static members, constructors, inheritance
7. [ ] Exceptions: try/catch/finally, runtime errors as exceptions, stack traces
8. [ ] Closures: fn literals, captured cells, per-iteration loop variables
9. [ ] Semantic checker complete (all "Rule:" items)
10. [ ] `.kkc` writer/reader/validator + `build`/`exec`/`disasm`
11. [ ] Remaining built-ins, test suite, fuzzing, benchmarks, language reference
12. [ ] Later/optional: peephole optimizer, inline caches for methods and fields, jump tables for switch, a REPL, multiple error reporting
