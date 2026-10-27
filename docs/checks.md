# Checking changes

Kukel has grown too big to review in full after every change. This file
splits it into subsystems with a clear interface, says what else to check
when one of them changes, and when a full cross-check (all code against all
documents, and back) is still worth it.

Contents:

1. [Subsystems](#1-subsystems)
2. [What to check after a change](#2-what-to-check-after-a-change)
3. [Shared conventions](#3-shared-conventions)
4. [Three kinds of checks](#4-three-kinds-of-checks)
5. [Running the tests](#5-running-the-tests)

## 1. Subsystems

Each subsystem is a group of packages that others reach only through its
interface. The import graph (`go list -f '{{.Imports}}' ./...`) holds them
apart: for example, a module sees only `native` and `value` (with the
exceptions in the table).

| # | Subsystem | Packages | Interface | Documents | Tests |
|---|---|---|---|---|---|
| 1 | Front end | `token`, `diag`, `lexer`, `parser`, `ast`, `format` | the syntax tree; `syntax.ebnf`, `syntax.lark` | reference.md §2, §4-§9 | lexer, parser, format; `tests/tokens`, `tests/ast`; TestLarkGrammar |
| 2 | Compiler | `compiler`, `loader` | a `bytecode.Program` | reference.md §4 (modules), vm.md §6 | compiler (rules, modules, precompiled), loader |
| 3 | Bytecode | `bytecode` | the instruction set, `.kkc` files, the verifier | vm.md §3, §5, §7, §8 | bytecode (files, fuzz, the opcodes in vm.md) |
| 4 | VM core | `vm`, `value`, `builtins` | values, calls, the hooks: debug, profile, cancellation, `WaitStart`/`WaitEnd`, `FlushOutput`, `End` | vm.md §2, §4; reference.md §3, §10 | vm, `tests/run` (through cmd/kukel) |
| 5 | Native API | `native` | `Args`, `Errorf`, `Flush`, `Wait`, `Duration`, `IOMessage`, `NetMessage`, `NotSetUp`, `SetProgram`/`Program`, the module registry | reference.md §11 (start), embedding.md §3 | native |
| 6 | Modules | each `modules/*mod` | its reference.md section | reference.md §11 (`db` ... `zmq`) | each module's own tests; `tests/run/native_*` |
| 7 | Tools | `debugger`, `profile`; in cmd/kukel `debug.go`, `dap.go`, `profile.go`; `editors/vscode` | the VM hooks; DAP; pprof | debugging.md; reference.md §1 (debugger, profiler); vm.md §4.12, §4.13 | debugger, profile; cmd/kukel `Debug`, `Dap`, `Profile` tests |
| 8 | CLI and REPL | the rest of `cmd/kukel` | the commands, their options and exit codes | reference.md §1; README | cmd/kukel (main, lineedit, doc); `examples/*.tests` |
| 9 | Go API | the repository root, `examples/embed` | the exported API | embedding.md | the root package's tests and examples; examples/embed |

The modules that reach beyond `native` and `value`:

- `workermod` uses the compiler, the loader and the VM (a worker is a new VM
  of the same program);
- `dbmod` uses `timemod` and `uuidmod`; `fsmod` uses `timemod`; `p9mod` uses
  `eventsmod`, `fsmod` and `timemod`; `zmqmod` uses `eventsmod`;
- `tkmod` uses only `native`, but owns the process's main thread and joins
  the event loop through `Loop.Poll`.

## 2. What to check after a change

Check the changed subsystem in full (code, its documents, its tests), and
then what this table names. "Look at" means a short pass over the named
places, not a full review.

| Changed | Check as well |
|---|---|
| a module | its reference.md section and `tests/run/native_*`; the modules that import it (`timemod`: db, fs, p9; `eventsmod`: zmq, p9, worker, tk; `fsmod`: p9) |
| `native` | look at every module for the convention that changed (section 3) |
| `value` or `builtins` | the VM; reference.md §3 and §10; look at the modules that build or read the changed kind of value |
| `vm` | the debugger and the profiler (the hooks), `workermod`, the Go API; vm.md §4; speed (section 3) |
| `bytecode` | the compiler and the VM; vm.md §3, §5, §7, §8; the debugger (`Debuggable`, BREAK); old `.kkc` files still load or are refused clearly |
| `compiler` or `loader` | the VM; the debugger (`Locals`, line tables); `workermod` (`startfile`); vm.md §6 |
| `lexer`, `parser` or `ast` | the compiler; `kukel fmt`; the debugger's expressions (`parser.ParseExpr`); the REPL; `syntax.ebnf` and `syntax.lark`; the VS Code grammar |
| a language feature | all of the row above, and reference.md, tutorial.md where it shows that part, the examples that would use it, and the `kukel fmt` layout of it |
| `debugger` | `kukel debug` and `kukel dap`; debugging.md; reference.md "The debugger" |
| `profile` or the VM's profiling | `kukel profile`; the Go API's `Profile`; reference.md "The profiler", embedding.md §7, vm.md §4.13 |
| cmd/kukel | the CLI tests; reference.md §1; README's command table; `examples/*.tests` |
| the root package | embedding.md; examples/embed; `example_test.go` |
| a document only | the code it describes (the document may be the one that is right) |

## 3. Shared conventions

Most findings that crossed subsystems in cross-checks 6 to 10 were about
these conventions, not about the interfaces. A change to one of them is a
change to every subsystem that follows it.

- **Messages.** A message about no source place starts with `kukel:`.
  Messages leave out Go's operation names (`nope.kkl: no such file or
  directory`, not `open nope.kkl: ...`): `native.IOError`, `IOMessage`,
  `NetMessage`. An object whose Go part is not set up gets one form
  (`Args.NotSetUp`). Argument counts are in proper plural ("at most 1
  argument").
- **Waiting.** A Go function that waits (for time, a message, an event,
  input, the network) calls `defer native.Wait(env).Done()`: it writes out
  the program's output first, and `kukel profile -cpu` leaves the wait out.
  It stops when the program is cancelled (`env.Done()`).
- **Durations.** Delays and timeouts in seconds go through
  `native.Duration` (one rule for limits, `inf`, and very long times).
- **Errors.** Bad arguments are a `ValueError` or `TypeError`; files an
  `IOError`; networks a `NetError`; a module's own failures its own kind
  (`DBError`, `TkError`).
- **Ending.** What a module keeps open for a program and must finish
  when the program ends (the files of `fs::open`) is a `Local` value with
  an `End() error` method; `VM.End` calls it (the command, a worker, the
  Go API's `Close`). A new place that runs a program to its end calls
  `VM.End` too.
- **Threads.** Kukel code of an interpreter runs on its goroutine only;
  other goroutines post to its event loop. Tk runs on the process's main
  thread only.
- **Speed.** `callGo` stays inlinable (`go build -gcflags=-m ./internal/vm
  2>&1 | grep callGo`); new `VM` fields go last; a change to a hot path is
  measured (GOMAXPROCS=1, user CPU time, best of 9 alternating runs, old
  against new).

## 4. Three kinds of checks

**A scoped check, after each change.** Review the changed subsystem and its
row in section 2, code and documents both ways, and run its tests and
those of the subsystems named there (section 5). One reviewer is usually
enough. This replaces a full cross-check after a feature.

**Contract tests, always.** Tests that hold the documents and the code
together catch drift without a review. There are:

- the programs and sessions of the reference, the tutorial and the README
  replayed (`cmd/kukel` doc tests, `tests/reference.golden`), and kept
  `kukel fmt` clean (TestDocsFormatted);
- every opcode documented once in vm.md, as the instruction set has it
  (`internal/bytecode`);
- `syntax.lark` checked against the test programs (TestLarkGrammar);
- the examples' `.tests` files;
- the debugger's and the profiler's front ends driven end to end (`Dap`,
  `Debug`, `Profile` tests);
- in `cmd/kukel/contract_test.go` (TODO.md task 48):
  - TestModulesWait: every Go function of a module that waits (a
    `select` without `default`, a channel receive, `time.Sleep`) calls
    `native.Wait`, or is in `waitExempt` with the reason;
  - TestReferenceArity: the numbers of arguments reference.md §11 shows
    for each function, constructor and member function are those it takes
    (found by calling it with none and with many, which `native.Check`
    refuses with the range); run it also with `-tags tk`;
  - TestObjectsNotSetUp: an object whose Go part is not set up, passed to
    any module function, static member function, constructor or member
    function (alone or in a vector), gives an exception of Kukel's own,
    never a crash; the member functions are called on a working object that
    `working` says how to make (a new class with member functions must be
    added there), except those of `tk`, whose widgets need a display and
    the process's main thread;
  - TestMessagesWithoutGoOperations: file and network failures of the
    built-ins, the modules and the command carry no Go operation name;
  - TestVMListings: the listings of vm.md §6 against `kukel disasm` of a
    program kept in an HTML comment before each (`<!-- disasm [exact] f
    ...`): their instructions in that order, and in an exact listing each
    line as disasm writes it.

**A full cross-check, as an integration test.** Before a release with many
changes, or after several feature branches. With the contract tests in
place it can look mostly at what crosses subsystems: workers with the
modules, the event loop with zmq, tk and p9, the debugger and the profiler
with workers, the Go API, and the conventions of section 3.

## 5. Running the tests

| What | Command |
|---|---|
| everything (without tk) | `go test ./...` |
| one subsystem | `go test ./internal/vm ./internal/builtins` (the packages of section 1) |
| the programs in `tests/run` and the examples | `go test ./cmd/kukel` |
| tk (needs Xvfb or `$DISPLAY`; xdotool for clicks) | `go test -tags tk ./internal/modules/tkmod` |
| data races | `go test -race` on the changed packages and cmd/kukel |
| the grammar test | `go test ./cmd/kukel -run Lark` with `lark` installed (it skips without) |
| PostgreSQL and MySQL | set `KUKEL_TEST_POSTGRES` and `KUKEL_TEST_MYSQL`, then `go test ./internal/modules/dbmod` |
| style | `gofmt -l .` (empty) and `go vet ./...` |
| Windows | CI's `windows` job (`go test ./internal/modules/fsmod` and a `kukel.exe` run); here only `GOOS=windows go vet` and `GOOS=windows go test -c` of a package |

Build files go to `GOTMPDIR` when `/tmp` is short of space (for example
`GOTMPDIR=$HOME/.cache/kukel-tmp`).
