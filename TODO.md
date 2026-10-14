# Kukel interpreter: plan and TODO

Goal: a Go program that reads Kukel source (grammar: `syntax.ebnf`, now version 6),
tokenizes and parses it, compiles it to bytecode for a stack machine, stores
that bytecode in a file, and executes it.

```
source files ──lexer──▶ tokens ──parser──▶ AST
            ──compiler (names, rules, code)──▶ bytecode image ──writer──▶ .kkc file
.kkc file ──loader──▶ bytecode image ──VM──▶ program run
```

---

## 0. Decisions made

| Topic | Decision |
|---|---|
| Implementation language | Go ≥ 1.25 (module; the standard library, and pure Go libraries: `github.com/go-zeromq/zmq4` since Q43, `modernc.org/sqlite` since Q44) |
| Syntax | `syntax.ebnf` **version 2** (see §1b for the changes from version 1); version 3 added modules (Q35), version 4 removed temporaries (Q40), version 5 removed `elseif` and the comma operator outside `for` (Q41), version 6 added `with` (Q45). No compatibility with older versions is kept |
| Parser | Hand-written lexer and recursive-descent parser (`syntax.lark` is a tested reference only) |
| "Stored tokenized code" | Compile to a stack-machine bytecode image, saved as a binary `.kkc` file that runs without the source |
| File extensions | Source `.kkl`, compiled bytecode `.kkc`, binary `kukel` |
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
- Q13 How a thrown runtime error looks: **an `Error` object with `message`, `kind` and `trace`** (milestone 7: `Error` is a real class from a small prelude compiled in front of every program, `class Error { message; kind; trace; Error(message, kind); }` with `Error::Error(message = nil, kind = nil)`; programs may throw it and derive from it)
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
- Q29 (decided in milestone 7; replaced by Q41: only Error objects) Any value can be thrown; `catch (e)` catches everything (there are no typed catch clauses; test with `instanceof(e, SomeError)` and rethrow with `throw e;`). The first time an `Error` object is thrown, the VM sets its `trace` (a vector of strings like `"f (file.kkl:3)"`, innermost call first) and, if `kind` is nil, sets `kind` to the name of its class; throwing it again keeps the original trace. An uncaught `Error` prints `kind: message` and the trace; any other uncaught value prints `uncaught exception: <value>`
- Q30 (decided in milestone 7) An exception thrown in a `finally` block replaces the one that was pending; a `case`/`default` label inside a try, catch, finally or with block of its switch is a compile error (checked before the local-initialization rule)
- Q31 (decided in milestone 8) A function literal is named `<enclosing function>/fn@<line>` in traces and when printed (`main/fn@12`); closures are equal only to themselves
- Q32 (decided in milestone 9) A call whose callee is known at compile time — a top-level function, a static member function (`C::f(...)` or unqualified inside the class), `Base::m(...)`, or `new C(...)` naming a class — with the wrong number of arguments is a compile error (the message is the same as at run time). Calls through variables, members, spread arguments and virtual calls (`p->m(...)`) are checked at run time. `new C(...)` of a class whose constructor is declared but not defined is a compile error too
- Q33 (decided 2026-10-13, option A) A class that declares no constructor inherits the nearest base class's constructor: `class ValidationError : Error { }` makes `new ValidationError("msg")` run `Error::Error("msg")`. A class with its own constructor is unaffected (it calls `Base::Base(...)` itself)
- Q34 (decided 2026-10-13) Built-in details: `eprint` and `eprintln` (to standard error; both flush standard output first, as `readline` does); `floor`/`ceil`/`round` give ints (`RangeError` if the float does not fit; `round` takes halves away from zero); `pow` gives an int for two ints with a non-negative exponent (wrapping like `*`), else a float; `exit(code)` ends the program at once (not catchable, `finally` blocks do not run); `int("...")` reads literals (`42`, `-0x10`, `0b11`, `1_000`, surrounding spaces) and gives a `ValueError` for anything else; `min`/`max` take several numbers or strings, or one vector; random numbers come from a per-program source that `seed(n)` restarts
- Q35 (decided 2026-10-13, F-1/F-19/F-20 of docs/future.md) Modules: `use name;` at the top of a file loads `name.kkl` (next to the using file, else through `KUKEL_PATH`) as a module with its own name space, reached as `name::x`, `new name::C(...)`, `class D : name::C`, `name::C::x`; `use` holds per file; top-level names starting with `_` are private to their module; each module is loaded once, before the files that use it, and cycles are errors; source modules only: `kukel build` stores the program with all its modules in one `.kkc` (no format change; precompiled `.kkc` modules later). `use` is a keyword (syntax version 3)
- Q36 (decided 2026-10-13, F-3/F-7 of docs/future.md) Native classes and modules written in Go: a Go package registers a module (`internal/native`): functions, constants and native classes, whose objects hold a Go value and whose member functions (instance, static, constructor) are Go functions; no data members; native classes cannot be derived from yet (events will use callbacks); a native module wins over a `.kkl` file of the same name; module names may equal built-in names (`time::now()` next to `time()`). First modules: `json` (encode/decode; objects cannot be encoded), `strings` (`Builder`), `time` (`Time` with strftime-style formats, IANA zones carried in the binary). Bytecode format 3 adds the native global kind, linked by qualified name
- Q37 (decided 2026-10-13, F-5/F-6 of docs/future.md) The event loop is the native module `events`, one loop per interpreter (kept through `value.Env.Local`): `after`/`every` timers (seconds; a deadline heap in the loop, no goroutine per timer; a late `every` skips missed calls), `post`, `run`, `stop`, `onerror`, `pending`; `run()` returns when nothing is left (no active timer, nothing posted, no hold) or on `stop()`; an exception a handler does not catch leaves `run()` unless `events::onerror(f)` takes it; handlers run one at a time on the interpreter's goroutine; Go modules feed the loop with `eventsmod.For(env).Post(fn)` and `Hold()` from any goroutine. Callbacks only; coroutines (`await`) are not planned now
- Q38 (decided 2026-10-13, F-8/F-9/F-10 of docs/future.md) Workers first (embedding in Go later): the native module `worker`; `worker::start(f, args..)` runs a top-level or static member function in a new VM of the same program (own globals; works with `.kkc`), `worker::startfile(path, args..)` compiles a `.kkl` (with its modules) or loads a `.kkc` and runs its `main(vector of args)`; messages are copies of plain data (nil, bools, numbers, strings, vectors, maps; shape, sharing and cycles kept; `nil` cannot be sent, it marks the end), queued without limit; blocking `receive([timeout])` and `onmessage(f)` through the event loop, on both sides; `w->stop()`, `w->wait()` (result copy, or a `WorkerError`), `w->done()`; `exit` in a worker ends that worker; workers may start workers, no limit; when main returns (`kukel run`/`exec`; embedding: `Interp.Close`), running workers are abandoned and their output dropped. New error kinds `CompileError`, `WorkerError`
- Q39 (decided 2026-10-13, §2.1 of docs/future.md) Embedding: the public package is the repository root (`import "github.com/maarten-boot/kukel"`): `CompileFiles`, `CompileSource`, `LoadProgram`, `Program.Encode`; `New(prog, Options{Stdout, Stderr, Stdin, StepLimit})`; `Interp.Run`, `Call`, `CallValue`, `Global`, and `RunContext`/`CallContext` (cancellation through a Go context: a `CancelledError` the program cannot catch; waiting Go functions return at once; workers are cancelled too); `Value` with constructors, accessors and `FromGo`/`ToGo`. Go functions come in host modules (`NewModule(name).Func(...).Const(...)`, per program, `use app; app::f()`), errors map to exceptions (`Errorf(kind, ...)`, other errors `HostError`). Limits for untrusted code: a step budget per call from Go, `CompileOptions.DenyBuiltins` (`SystemBuiltins`: files, stdin, getenv, exit), `StandardModules` allow-list, `NoFileModules`; enforced by the compiler and again when a `.kkc` is linked; workers inherit them. No memory limit (documented). Internally a `native.Config` per program replaces the process-wide registry lookups. After the second cross-check: `Interp.Close` (cancels workers, closes output), contexts also for calls made from Go functions, a panic in a Go function is a `HostError`, a failed initializer fails every call, unknown names in the allow-lists are errors, `NoFileModules` also refuses `worker::startfile`
- Q40 (decided 2026-10-14) Temporaries (`f(a; t, u)`, from the first syntax) are removed: with `local` they are redundant (a local declared at the start of the body is exactly a temporary). Syntax version 4; the parser reports the old form with the advice to declare them with `local`
- Q41 (decided 2026-10-14) One way for each thing (Kukel v0.4, syntax version 5): `elseif` is removed (write `else if`; no longer a keyword); the comma operator only in the first and last part of a `for` loop; the built-ins `isnil` (write `x == nil`) and `substr` (write `slice`) are removed; only `Error` objects can be thrown (this replaces Q29's "any value": a literal that is not one is a compile error, another value a `TypeError`), so a caught `e` always has `kind` and `message`. The old forms get error messages that name the replacement. Bytecode format 4 (old `.kkc` files are refused, since `THROW` changed meaning). Kept on purpose: `++`/`--` next to `+=`, `#` comments, the `time()` built-in, and static member functions called through objects (`p->f()` picks the static of the object's class, which `C::f()` cannot)
- Q42 (decided 2026-10-14) Member functions stay declared in the class and defined outside it (`class C { m(a); }` and `C::m(a) { ... }`), not written inside the class body. Allowing bodies in the class as well would be two ways to define a member function (against Q41), and having only inline bodies would lose the class block as a compact interface overview and the splitting of a class's definitions over several files. The parameter lists written twice stay, checked by the compiler (Q19)
- Q43 (decided 2026-10-14, F-14/F-15 of docs/future.md) Communication: the native module `zmq` on the pure Go library `github.com/go-zeromq/zmq4` v0.17.0 (no C library; `go install` and cross-compiling keep working; the first dependency outside the standard library). `zmq::Socket` of type REQ/REP, PUB/SUB, PUSH/PULL, DEALER/ROUTER, PAIR (optional identity); `zmq::poll(sockets [, timeout])` (built on the pending reads: no message is lost; the earliest socket wins); `bind`, `connect`, `send` (a string, or a vector of strings for several parts), `receive([timeout])` (a timed-out read stays pending, so no message is lost), `onmessage(f)` through the event loop, `subscribe`/`unsubscribe`, `close`; network problems are `NetError`. Differences from C ZeroMQ: `connect` waits (about 3 seconds) for the other side, and `close` lingers 0.1 s after a send because the library only queues messages. Not done: PLAIN and CURVE security (CURVE would mean implementing the ZMTP-CURVE cryptography ourselves, since zmq4 only has NULL and PLAIN; encryption comes from an SSH tunnel or VPN instead)
- Q44 (decided 2026-10-14, F-16/F-18 of docs/future.md) SQL, plain first and SQLite first: the native module `db` on Go's `database/sql` with the pure Go driver `modernc.org/sqlite` v1.59.0 (about 6.5 MB more binary). This raises Kukel's minimum Go to 1.25 (the driver's releases need it; Go 1.22-1.24 are out of support; the last driver for 1.22 is from March 2025). `db::open("sqlite:file" | "sqlite::memory:")` gives a `db::Conn` with one database connection (so a transaction and a memory database are one session): `exec` (rows changed), `lastid`, `query` (a vector of maps), `first`, `cursor` (`db::Cursor` with `next`/`close`; while open, the connection does nothing else), `begin`/`commit`/`rollback`/`intransaction`, `close` (rolls back an open transaction). Parameters only (`?`); nil, bool, int, float, string and `time::Time` go in; BOOLEAN columns come back as booleans, DATE/DATETIME/TIMESTAMP as `time::Time`. Foreign keys on, a 5 s busy timeout. Database errors are the new kind `DBError`. Cancellation from an embedding program stops a running query. Later: PostgreSQL and MySQL drivers, then the ORM (§5.2)
- Q45 (decided 2026-10-14, F-4 of docs/future.md) The `with` statement (syntax version 6, `with` is a keyword): `with (local a = e, ...) statement` closes each local that is not nil with `close()` when the statement is left, the last one first; when the statement threw and a close throws too, the statement's exception wins (the close failure is dropped). Compiled as nested try blocks with a separate exception path whose close ignores failures
- Q46 (decided 2026-10-14) Several compile errors at once: the first syntax error of each file (the parser does not recover inside a file); in the compiler the first error of each definition and of each function (recovery at those points); errors in the definitions stop before the function bodies, to avoid errors that are only consequences; at most 10 shown (`diag.List`, `diag.MaxErrors`); the embedding API gives the others in `CompileError.More`
- Q47 (decided 2026-10-14) `kukel fmt [-w | -l]`: formats by tokens, not from the syntax tree, so line breaks and comments stay: re-indents (4 spaces by braces and brackets, brackets opened on one line count once, switch labels one level in and their statements two, a statement without braces under if/while/for/else/do one level deeper, an else with its if), normalizes the spaces between tokens (a trailing comment keeps its distance), at most one blank line in a row, no trailing spaces. Refuses files that do not parse or hold a NUL byte; checks that its output gives the same tokens and comments. Later (wanted): more breathing space, such as a blank line before if/while/do/for/switch (done: Q53)
- Q48 (decided 2026-10-14) `kukel repl [a.kkl ...]`: a compiler session (`compiler.Session`) adds each piece (declarations, or statements) to the same Program, and `vm.Extend` gives the VM the new globals, classes and functions, so state persists. A top-level `local` at the prompt is a session variable (a global; declaring it again reuses it). The value of a final expression statement (not an assignment or ++/--) is shown with `Repr`, unless nil. A one-line statement (or the last line of a piece) without its `;` gets one; a blank line ends an incomplete piece. A piece with a compile error adds nothing: the session compiles the pieces before it again, into the same indexes. Member functions may be defined in a later piece. `Program.Init` holds all the session's global initializers, for workers
- Q49 (decided 2026-10-14) Modules compiled on their own: `kukel build -module [-o m.kkc] m.kkl` writes a module file (the .kkc format with flags 1, format version unchanged; a program file and a module file each refuse to be read as the other). It holds a header (the module's declarations with function bodies and global initializers blanked out, lines kept) and its own functions, whose global/class/function operands refer to name tables. The loader takes `name.kkc` when a directory has no `name.kkl`; the compiler runs pass 1 on the header and links the code in pass 2 (relocating by name; the program's init calls the module's init where its initializers would run), then verifies the program. The data members of the module's classes, and of classes whose statics its code reads, are checked against the program's (else "compile module m again")
- Q50 (decided 2026-10-14) Performance, before more modules: jump tables for switches with at least 4 int cases whose range holds at most twice as many values (`SWITCH_TABLE`, a float with an integral value matches as `==` would); strings unboxed (length in `num`, a `*byte` in `obj`) with concatenation results in a growable buffer, so `s += x` appends in place (string bytes never change; strings sent to workers are detached); `while`/`for` loops test at the bottom; `ADD_LOCAL` for `local += x` as a statement when `x` assigns nothing; a size-preserving peephole pass (jump threading, a `JUMP` to a return becomes the return). Bytecode format 5. Tried and dropped: keeping the inline caches in a local of the dispatch loop (slower: the loop function is sensitive to added state)
- Q51 (decided 2026-10-14) Deriving from native classes, and native classes in the embedding API: a Kukel class may derive from a native class that has a constructor (others: compile error). The object holds the Go value next to its data members; the derived constructor calls the native constructor explicitly (`strings::Builder::Builder(...)`, as for Kukel bases; a class without its own constructor uses the native one), and a member function written in Go called before that is a `ValueError`. Native member functions are inherited and may be overridden; `Base::m(...)` and `Base::Base(...)` for a native base compile to `CALL_NATIVE` (by name; no spread arguments, as for `CALL_DIRECT`). Go code that takes an object of a native class also takes derived objects (`native.Args.Object`, `Class.Is`). Bytecode format 6 (`Class.Native`, `CALL_NATIVE`). Public API: `NewClass(name).New/Method/Static/Show`, `Module.Class`, `Class.Object/StateOf`, `Object` with `State/SetState/Call/Value/ClassName`, `Value.Object`; `Object.Call` calls a member function virtually, so a derived Kukel class can override steps of Go code. Found on the way: `zmq::poll`, `receive` and `onmessage` on a send-only socket (PUSH, PUB) are a `ValueError` (poll returned such a socket at once)
- Q52 (decided 2026-10-15, F-17 of docs/future.md) The object mapping, in module `db`, as a data mapper (the connection does the work; any Kukel class, no base class): data members are the columns, `id` is the primary key, the table is the class's local name in lower case; optional static member functions `table()` (another name) and `columns()` (SQL types and constraints by data member, also for `id`). `conn->migrate(C)` creates the table, or adds the columns an existing table lacks (never drops or changes one); `conn->save(p)` inserts (nil id with an INTEGER PRIMARY KEY: the database chooses it and `p->id` is set) or upserts by id (`ON CONFLICT(id) DO UPDATE`), so a key of another type (`TEXT PRIMARY KEY`, a UUID the program makes) works when the program sets `id`; `conn->get(C, id)`, `conn->delete(p)`; `conn->find(C)` gives a `db::Query` with `where(sql, args...)`, `orderby(sql)`, `limit(n [, offset])`, `all`, `first`, `count`, `delete` (each refinement gives a new query). Loaded objects are made without running the constructor. Later: relations, renaming/dropping columns, generated keys of other types (UUID)
- Q53 (decided 2026-10-15) `kukel fmt` breathing space, always on: a blank line before a statement that starts with `if`, `while`, `do`, `for`, `switch`, `try` or `with` (also after a label, `outer: for`); comments right above it go below the blank line. Not as the first statement of a block, after a `case`/`default` or other label, as the statement of `else` or a header without braces, as the `while` of a `do`, inside parentheses, brackets or a map literal, or where a blank line already is. All test programs and the documentation's examples are formatted this way. Fixed on the way: a block opened on a hanging line (`if (...) {` under a `for (...)` without braces) kept that line's extra level for its contents and its `}`
- Q54 (decided 2026-10-15, F-16 of docs/future.md) PostgreSQL and MySQL/MariaDB in module `db`, with the pure Go drivers `jackc/pgx/v5` (stdlib) and `go-sql-driver/mysql`: `db::open("postgres://...")` (also `postgresql://`) and `db::open("mysql://user:pw@host:port/db?options")` (turned into the driver's DSN, with `parseTime`). Programs write `?` everywhere; for PostgreSQL each `?` outside strings, quoted names, comments and `$$` text becomes `$n`. Numbers sent as text (MySQL, NUMERIC) come back as numbers by the column type. The ORM adapts: the default key (`INTEGER PRIMARY KEY` / `BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY` / `BIGINT AUTO_INCREMENT PRIMARY KEY`), quoting (`"x"`, MySQL `` `x` ``), the columns of a table (`pragma_table_info` / `information_schema`), inserts (PostgreSQL `RETURNING id`, since it has no last insert id; MySQL `() VALUES ()`), upserts (`ON CONFLICT` / `ON DUPLICATE KEY UPDATE`), and booleans that `columns()` declares (MySQL stores TINYINT). Passwords never print (`***` in a connection and in messages). One connection per `db::Conn`, as for SQLite. Tested by running one program on all three (TestServers; servers through `KUKEL_TEST_POSTGRES`/`KUKEL_TEST_MYSQL`, here with Docker postgres:16 and mariadb:11); the binary grows from 14 MB to 23 MB (about 3.5 MB for the MySQL driver, 5.5 MB for pgx)
- Q55 (decided 2026-10-15) Build tags leave out the large parts: `nopostgres`, `nomysql`, `nosqlite` (each driver in a file of its own; with all three, no module `db`) and `nozmq` (the module's code behind the tag, an empty package without it). A plain build (`go install`) has everything. `db::open` of a kind that is left out is a `ValueError` that names the tag; `kukel version` lists the modules and `db`'s databases. Sizes (MiB): 21.9 everything, 13.4 SQLite only, 7.8 no database, 6.1 all four tags. `make small` (TAGS=...), `make test-small` (tests that need a left-out part skip), `make vet` also vets the smallest build, and CI tests it
- Q56 (decided 2026-10-15) ORM relations: the foreign key is an ordinary data member (`orderid`); a static `relations()` names relation members: `db::many(C, key)` (the objects of `C` whose `key` holds this id) and `db::one(C, key)` (the object of `C` whose id this object's `key` holds); they give `db::Relation` objects. Relation members are not columns (`migrate` and `save` skip them; `save` stores the object only). Loading is explicit, since a data member cannot load on first use: `conn->load(p, names...)`, and `query->include(names...)` for all the results of a query with one query per relation (`IN (...)`, 500 values per query); `with` was not possible as a name (a keyword). No implicit constraints: a foreign key constraint goes in `columns()`. Found on the way: PostgreSQL and MySQL need a type for every column, so `migrate` there names a data member without one in `columns()`

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
15. Function literals / closures: `fn (params; temps) { ... }` (temporaries removed in version 4, Q40)
- New keywords: `const`, `fn`, `global`, `in`, `this`

---

## 2. Project setup (approved)

- [x] `go mod init` (module name `github.com/maarten-boot/kukel`), Go ≥ 1.22 (≥ 1.25 since Q44)
- [x] Layout (packages are created by the milestone that needs them):
  ```
  cmd/kukel/          CLI
  internal/token/     token kinds, positions
  internal/diag/      the error type all stages share
  internal/lexer/
  internal/ast/
  internal/parser/
  internal/compiler/  AST → bytecode
  internal/bytecode/  opcodes, image structs, (de)serialization, disassembler
  internal/value/     runtime value types
  internal/vm/        interpreter loop, call frames, upvalues, exceptions
  internal/builtins/
  internal/loader/    finds and reads the modules a program uses (Q35)
  internal/native/    modules written in Go, native classes, per-program limits (Q36, Q39)
  internal/modules/   dbmod, eventsmod, jsonmod, stringsmod, timemod, workermod, zmqmod (Q36-Q38, Q43, Q44)
  *.go (root)         the public package for embedding (Q39)
  tests/              .kkl programs + expected output
  docs/               reference.md (language), vm.md (VM and bytecode), embedding.md, future.md
  ```
- [x] CLI subcommands (all done; also `kukel version` and `kukel help`):
  - `kukel run a.kkl b.kkl [-- args]`: compile in memory and run
  - `kukel build -o prog.kkc a.kkl b.kkl`: compile to a file
  - `kukel exec prog.kkc [args]`: run a stored image
  - `kukel disasm prog.kkc`: dump the bytecode in readable form
  - `kukel check a.kkl ...`: compile only and report errors
  - `kukel tokens a.kkl` / `kukel ast [-pos] a.kkl`: debug dumps
- [x] Makefile: `all`, `build` (→ `bin/kukel`), `install`, `test`, `vet` (incl. gofmt check), `fmt`, `fuzz` (`FUZZTIME=30s`), `bench`, `golden`, `clean`

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
  - function / member-function definitions with `parameter_part` (`params ; temporaries`, both optional; temporaries removed in syntax version 4, Q40)
  - all statements (`elseif` desugared to `else if` until syntax version 5 removed it, Q41)
  - a `{` at the start of a statement is always a block
  - labelled statement: IDENT followed by a `:` token **on the same line** (the lexer already separates `::`)
  - `case <constant expression at logical_or level>:` and `default:` are statements in their own right (they may appear nested, C style)
  - `for (` → after `local IDENT [, IDENT]` look ahead: `in` = for-each, otherwise a local list as for initializer
- [x] Expressions: precedence climbing or one function per level, with this exact structure:
  - comma (only in a `for` loop's first and last part since Q41) < assignment (right associative) < `?:` (last part = assignment) < `||` < `&&` < `|` < `^` < `&` < equality < relational < shift < additive < multiplicative < unary < postfix < primary
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
- [x] `C::m` definition: class C must exist and declare `m`; shape matches the declaration: same number of parameters (required + optional) and a rest `..` in both or neither; names may differ (Q19)
- [x] `this` only in non-static member functions and in function literals nested in them
- [x] Parameters: once an optional parameter appears, all later ones need defaults (or are the rest parameter); the rest parameter is last; at most 255 optional parameters; a default may use earlier parameters (only those: a later name resolves as if the parameter did not exist) — milestone 4
- [x] Lvalues: the target of an assignment or `++`/`--` must be a variable (global, local, parameter, captured, member), `C::x`, `e[i]` or `e->x`; not a constant, function, class, built-in or `this`
- [x] Argument counts of callees known at compile time (Q32)
- [x] `kukel check a.kkl ...`: compile only, report the errors (Q46), run nothing (silent when fine)
- [x] `local`: only directly inside a block or in a for initializer, not directly in a switch body, no duplicate in the same block; the name is visible *after* its initializer
- [x] `catch (e)`: `e` is a new local of the catch block
- [x] `case`/`default`: only inside a switch body; case values are constant expressions; no two equal case values (1 and 1.0 are equal); at most one default
- [x] Q9: reject `case` labels that would jump into a try block (also catch and finally blocks)
- [x] Reject `case`/`default` labels that skip the initialization of a `local` still in scope at the label (Q20): "case label skips the initialization of local `y`"
- [x] `break`/`continue`: a plain break needs a loop or switch, a plain continue a loop; a label must name an enclosing statement (a loop, for continue) in the same function (they never cross a `fn` literal); a label may not repeat the label of an enclosing statement
- [x] `finally`: no `return`, and no break/continue to a target outside it
- [x] `new X(...)`: X is a class name or a variable (checked at run time if it's a variable)
- [x] Identifier resolution in order: block local → parameter → variable of an enclosing function (closure capture) → member of the method's class (incl. inherited) → top-level name. Unresolved = compile error. (Done by the compiler while generating code.)
- [x] Closure analysis: mark every local/parameter that is captured by a `fn` literal (these live in heap cells; the others stay in plain slots). Done by name: a function's locals whose names occur anywhere inside its nested literals get cells (a safe over-approximation; exact resolution happens during code generation)

## 6. Runtime values (`internal/value`)

- [x] `Value` representation: a tagged struct `{kind, num uint64, obj any}` (32 bytes), so numbers never allocate; since Q50 a string is its length in `num` and a pointer to its bytes in `obj`, so strings do not allocate a box either
- [x] Kinds (Nil, Bool, Int, Float, String, Vector, Map, Function, Closure, Builtin, Class, Object, BoundMethod; internal: Iterator, Cell, Internal; Error is a prelude class): Nil, Bool, Int, Float, String (immutable `string`), Vector (`*VectorObj`), Map, Class, Object, Function (code + captured upvalue cells), BoundMethod (object + method), Builtin; Error (Q13: `message`, `kind`, `trace`) is a prelude class, not a kind
- [x] `typeof`: Function, BoundMethod and Builtin all report `"function"`
- [x] Map: keys are strings and numbers only; 1 and 1.0 are the same key; the key keeps the form it was first stored with; insertion order kept (Q6); deleted entries are compacted when more than half are gone; NaN is not a key
- [x] Printing: containers inside themselves print as `[...]` / `{...}`
- [x] Equality, truthiness, ordering, `typeof` names, string conversion (Q2, Q10, Q11); int/float comparisons are exact (no rounding through float64); floats print as `1.0`, `1e+21`, `inf`, `-inf`, `nan`; inside containers strings are quoted with Kukel escapes
- [x] All operator semantics live in `internal/value` and are shared by the VM and the compiler's constant folding
- [x] Memory: rely on Go's GC

## 7. Bytecode + compiler (`internal/bytecode`, `internal/compiler`)

### Instruction set (1-byte opcode + fixed little-endian operands: u16 for indices and counts, u32 for absolute jump targets)
- [x] Stack: `CONST k`, `NIL`, `TRUE`, `FALSE`, `POP`, `DUP`, `DUP2` (`e[i] op= x` uses `DUP2`, and `e[i]++` as a value keeps the old value in a hidden slot); `SWAP`/`ROT` turned out not to be needed
- [x] Variables: `LOAD_LOCAL i`/`STORE_LOCAL i` (params, locals and catch variables share frame slots), `LOAD_GLOBAL g`/`STORE_GLOBAL g`, `LOAD_MEMBER i`/`STORE_MEMBER i` (on `this`, slot known at compile time), `LOAD_STATIC c,i`/`STORE_STATIC c,i`, `LOAD_THIS`
- [x] Fields on any object: `GET_MEMBER name` (data member, else bound method, else error), `SET_MEMBER name` (data member only); slot lookup by name per class, cached later. `this->x` and unqualified `x` of a known data member compile to `LOAD_MEMBER`/`STORE_MEMBER slot`
- [x] Closures: `CLOSURE fn` (the capture list, local slot or enclosing upvalue, is in the function's `Upvalues`), `LOAD_UPVALUE i`/`STORE_UPVALUE i`, `LOAD_CELL i`/`STORE_CELL i` / `NEW_CELL i` for captured locals in the defining frame
- [x] Arithmetic/logic: `ADD SUB MUL DIV MOD NEG NOT BITNOT BITAND BITOR BITXOR SHL SHR EQ NE LT LE GT GE INC DEC` (int fast paths in the VM)
- [x] Control: `JUMP off`, `JUMP_IF_FALSE off`, `JUMP_IF_TRUE off` (for `&&`, `||`, `?:`) (`&&`/`||` produce `true`/`false`, Q4)
- [x] Calls: `CALL argc`, `CALL_V`, `CALL_METHOD name argc`, `CALL_METHOD_V name`, `CALL_DIRECT func argc` (Q3: `Base::m(...)`, a non-virtual call with the current `this`), `LOAD_FUNC func` (static member functions are plain function values), `NEW argc` / `NEW_V` (the class is a value on the stack, so a class in a variable needs nothing extra), `RETURN`, `RETURN_NIL`
- [x] Spread: calls with a spread argument build an argument vector (`MAKE_VECTOR` + `APPEND`/`EXTEND`) and use `CALL_V` / `CALL_METHOD_V` / `NEW_V`; vector literals with spread use `EXTEND`
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
- [x] Compound assignment and `++`/`--` (pre and post) for each lvalue kind (local, cell, upvalue, global, member, this-field, static, index); the index and field forms evaluate `e` (and `i`) only once
- [x] Loops: `while`, `do-while`, `for` (an empty test = loop forever), for-each; break/continue targets, with labels, patched via jump lists; a label on any other statement lets `break label` leave it
- [x] Per-iteration loop variables (Q16): only when the variable is captured by a closure, give each iteration a new cell (copy the old value before the update for `for`); uncaptured variables keep a plain slot (no cost)
- [x] `switch`: evaluate the value into a hidden slot, then a chain of `EQ` + jump to each case label's address (labels may sit inside nested statements, Duff's-device style); jump to `default` or past the end. The dispatch code is placed after the body, so no patching per case is needed. Later, maybe: a jump table for dense int cases
  - A case label that a jump would reach past a `local` declaration still in scope is rejected by the checker (§5), so no slot clearing is needed
- [x] `try/catch/finally`: `TRY_PUSH` handler; the catch body binds the exception to a slot. `finally` is compiled inline on every way out (normal end, catch end, and break/continue/return passing through), plus a catch-all handler that runs the finally code and then does `RETHROW`
- [x] Classes in the image: name, base, data member names (instance slot layout incl. inherited members), static member slots, method table (name → code index, static flag)
- [x] Program init: functions, classes and built-ins bound to their global slots; then one generated init function runs the `global` initializers in program order; then `main` (built-ins get a global slot only when used)

### Bytecode file `.kkc`
- [x] Format (little-endian, uvarints; the layout is documented in `internal/bytecode/file.go`):
  `magic "KUKL"`, `format version` (2 since milestone 12, 3 since native modules, Q36; another version is refused, Q12), `flags`, main, globals, classes, init function, functions (code, constants, upvalue descriptors, line tables, slot names, source file names), CRC-32 at the end
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
- [x] Strings and collections (all done in milestone 5; built-ins can call back into Kukel through `Env.Call`, used by `sort`): `len(x)`, `find(s, sub [, from])`, `split(s, sep)`, `join(vec, sep)`, `upper`/`lower`, `trim`, `replace(s, old, new)`, `char(code)`, `ord(s [, i])`, `push(v, x)`, `pop(v)`, `insert(v, i, x)`, `remove(v, i)`, `slice(v, a, b)`, `sort(v [, cmpfn])` (cmpfn may be a closure or bound method), `keys(m)`, `values(m)`, `haskey(m, k)`, `delete(m, k)`
- [x] Type and conversion: `typeof(x)` (returns "nil", "bool", "int", "float", "string", "vector", "map", "class", "object", "function"), `int(x)`, `float(x)`, `str(x)`, `classof(obj)`, `instanceof(obj, C)`
- [x] Math and system: `abs`, `min`, `max`, `sqrt`, `pow`, `floor`, `ceil`, `round`, `sin`/`cos`/`tan`/`atan2`, `log`/`exp`, `random()` / `randint(a, b)` / `seed(n)`, `time()`, `clock()`, `exit(code)`, `getenv(name)`; command-line arguments come through `main(args)`
- [x] Built-in names live in the top-level name space; redefining or assigning one is a compile error (Q18)

## 9. Tests, examples, docs

- [x] `tests/run/*.kkl` + `.out` (expected stdout) and `.err` (expected stderr); `// args:` and `// exit:` comment lines; a Go test runs all of them through `run` and through `build`+`exec`, which must agree
- [x] Coverage list: every statement kind, every operator incl. wraparound and int/float mixing, default/rest parameters, globals and constants (incl. use before declaration, constant folding errors), classes/inheritance/static members/constructors, `p->x` and bound methods, labelled break/continue, switch fall-through, nested case labels and constant case values, for-each over vector/map/string (incl. mutation during the loop), spread in calls/new/vectors, closures (shared captured variables, counters, closures in loops), try/catch/finally with break/continue/return through the finally, runtime errors being caught, uncaught error trace, maps with 1/1.0 keys
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
12. [x] Performance (done 2026-10-13, branch `milestone-12-performance`), measured with `make bench` (median of 5 runs, milestone 11 → 12):

    | Benchmark | before | after | |
    |---|---|---|---|
    | fib(27) | 36 ms | 31 ms | −14% |
    | 1M-iteration loop | 43 ms | 31 ms | −28% |
    | 1M method calls | 83 ms | 55 ms | −34% |
    | 1M closure calls | 70 ms | 54 ms | −23% |
    | string building | 41 ms (600k allocations) | 22 ms (200k) | −46% |
    | maps and vectors | 32 ms (100k allocations) | 21 ms (85) | −34% |

    - superinstructions: `CMP_JUMP` (a comparison and the jump on it), `INC_LOCAL`/`DEC_LOCAL` (`i++` as a statement); a `for` loop runs 9 instead of 12 instructions per iteration
    - frames shrank from ~104 to ~56 bytes (`this` and the closure as pointers) and are filled in place
    - inline caches for `CALL_METHOD`, `GET_MEMBER`, `SET_MEMBER`: the last class seen and its method or data member slot; the caches live in the VM (a compiled program can be shared), each function has an index and each member instruction a cache slot
    - built-ins get their arguments as a view of the stack instead of a copy (they must not keep the slice); `str` of simple values without a builder
    - `.kkc` format version 2 (new instructions, cache counts); version 1 files are refused (Q12)
13. [x] Packaging (done 2026-10-13, branch `milestone-13-packaging`): module path `github.com/maarten-boot/kukel` (the GitHub repository), so `go install github.com/maarten-boot/kukel/cmd/kukel@latest` works; `kukel version` (git describe via `make`, the module version via `go install ...@vX`, commit, Go version, bytecode format); `make install`; README with an example (run by the docs test); GitHub Actions CI on Go 1.22 (the `go.mod` minimum, checked locally) and stable: vet, test, build, a short fuzz
14. [x] Modules from source files (Q35; branch `modules`): `use`, the loader, module name spaces, private `_` names, syntax version 3
15. [x] Native classes and modules written in Go (Q36; branch `native-classes`): `internal/native`, `json`, `strings`, `time`, bytecode format 3
16. [x] Event loop (Q37; branch `event-loop`): the native module `events`, `value.Env` gains `Local` and `Thrown`
17. [x] Workers (Q38; branch `workers`): the native module `worker`; `VM.Start`, `VM.ShareOutput`
18. [x] Embedding (Q39; branch `embedding`): the public package at the repository root, docs/embedding.md
19. [x] Remove temporaries (Q40; branch `remove-temporaries`): syntax version 4
20. [x] One way for each thing (Q41; branch `simplify`): no `elseif`, comma only in `for`, no `isnil`/`substr`, throw only `Error` objects; syntax version 5
21. [x] ZeroMQ (Q43; branch `zmq`): the native module `zmq`
22. [x] SQL with SQLite (Q44; branch `sql`): the native module `db`; Go ≥ 1.25
23. [x] Smaller steps (branch `small-steps`): the `with` statement (Q45), several compile errors at once (Q46), `kukel fmt` (Q47), `kukel repl` (Q48), modules compiled on their own (Q49), fmt breathing space (Q53, branch `fmt-space`)
24. [x] Performance (Q50; branch `performance`), measured with `make bench` (fastest of 8 interleaved runs, v0.7.0 → this branch; this machine varies ±10% between runs):

    | Benchmark | before | after | |
    |---|---|---|---|
    | 1M-iteration loop | 33 ms | 24 ms | −28% |
    | dense int switch, 1M times | 160 ms | 60 ms | −62% |
    | `s += "x"` 20,000 times, then indexing | 47 ms (211 MB) | 1.6 ms (0.1 MB) | −97% |
    | string building | 25 ms (200k allocations) | 21 ms (100k) | −16% |
    | 1M field reads and writes | 68 ms | 60 ms | −11% |
    | fib(27), 1M method calls, 1M closure calls, maps and vectors, 1M string compares | 32, 58, 57, 22, 55 ms | 34, 57, 57, 23, 54 ms | ±6% |

    - `SWITCH_TABLE` and per-function jump tables; `ADD_LOCAL`; loops test at the bottom; the peephole pass; strings without a box and with in-place appends (see Q50)
    - `.kkc` format version 5
25. [x] Deriving from native classes, and native classes in host modules (Q51; branch `native-derive`); bytecode format 6
26. [x] Objects in tables, the ORM (Q52; branch `orm`)
27. [x] PostgreSQL and MySQL/MariaDB in `db` (Q54; branch `sql-servers`)
28. [x] Build tags for the large parts (Q55; branch `build-tags`)
29. [x] ORM relations (Q56; branch `orm-relations`)
30. [ ] Later/optional (see also [docs/future.md](docs/future.md): events and Tk; relations in the ORM): a register-based or otherwise restructured dispatch loop, if call speed matters (the loop of `run` is at its limit for small changes)
