# Changes

What changed in each release of kukel, newest first. The reasons and the
details are in [TODO.md](TODO.md), under the Q entries named here.

Two kinds of change need care when you upgrade:

- **Syntax versions.** A new syntax version may refuse old forms; the
  parser then says what to write instead. The history is at the top of
  [syntax.ebnf](syntax.ebnf).
- **Bytecode formats.** A `.kkc` file is read only by a kukel of the same
  format version; build it again from its source after such a release
  (`kukel build`). The history is in [docs/vm.md](docs/vm.md) §7.

## v0.38.0 (2026-10-30)

- The module `term`: the terminal a key at a time (`term::raw(f)`,
  `term::key([timeout])` with keys such as `"a"`, `"Enter"`, `"Up"`,
  `"Ctrl-S"`), `term::size()` and `term::isterm()`; keys also come from a
  pipe or a file, for tests. `examples/vi.kkl`: a small vi built on it.
  Q99.

## v0.37.0 (2026-10-30)

**Syntax versions 9 and 10, bytecode format 11**: programs of version 8
are unchanged (`from` is not a keyword); `.kkc` files must be built again.

- The escape `\u{...}` in strings: a character by its code (one to six
  hexadecimal digits), written as UTF-8: `"caf\u{E9}"` is `"café"`,
  `"\u{1F600}"` an emoji. A code above 10FFFF or a surrogate is an error;
  a character literal stays one byte and refuses it. Q94.
- `kukel fmt -strict`: braces around every statement of `if`, `else`,
  `for`, `while`, `do` and `with` that has none, and every function
  definition on several lines, also one that fits on one (function
  literals keep their lines). Comments stay; the output is checked to
  differ from the input only by the braces. Q95.
- Generators: `g->send(x)` resumes a generator and gives `x` to the `yield`
  it goes on from, written `local x = yield v;`, `x = yield v;` or
  `(yield v)`; `yield from x;` yields every value of a generator (or of
  anything `for` walks), passing sends on and closing it with the outer
  one. Host modules make generators with `Interp.Generator` (an `iter()`
  that gives one makes a host class iterable). `fs::memory([text])`: an
  `fs::File` in memory, with `f->text()`. Q96.
- The logo: the VS Code extension's icon (the shield), and at the top of
  each release's notes. Q97.
- A check of Q94–Q97 (Q98): a value sent to a `yield` that takes none is
  dropped (it was kept and given to a later one); `kukel fmt -strict`
  puts the `{` on the header's line also when the statement is on the
  next line, and keeps a comment before a closing brace with the statement
  before it; `kukel fmt` keeps the space in `yield from [1, 2]`.

## v0.36.0 (2026-10-30)

- A changed `.kkc` file whose `for` loop over a generator jumps back at
  its end is stopped by the step limit and cancellation again (a fuzz test
  hung on it). Q91.
- Kukel in Geany: `editors/geany/filetypes.Kukel.conf` gives highlighting,
  the symbol list and Build menu commands (check, format, run). Q92.
- The module `utf8`: text by character (Unicode code point) instead of by
  byte: `utf8::len`, `chars`, `codes`, `char`, `string`, `code`, `slice`,
  `upper`, `lower`, `trim`, `valid`, `fix`, and `offset` and `index`
  between character and byte positions. Bytes that are not UTF-8 are kept.
  `kukel version` shows the Unicode version. Q93.

## v0.35.0 (2026-10-27)

**Bytecode format 10**: `.kkc` files must be built again.

- The module `iter`, written in Kukel and built into kukel: `range`,
  `count`, `map`, `filter`, `take`, `drop`, `takewhile`, `dropwhile`,
  `enumerate`, `zip`, `chain`, `collect`, `reduce`, `first`, `any`, `all`;
  all generators, so a loop that stops early closes every layer. The
  first of the standard modules written in Kukel (reference.md §12). Q88.
- Faster generators: a `for` loop runs a generator's steps in its own
  dispatch loop, as calls run, instead of in a nested one: a loop over a
  generator takes about a third less time, a chain of `iter` functions a
  quarter less. A generator's `return;` is a new instruction, `GEN_RETURN`.
  Other programs run as fast as before. Q89.
- A check of `iter` and the faster steps (Q90): `kukel dap` shows the code of `iter` (the
  `source` request); `iter`'s messages are those of the modules written in
  Go; the verifier refuses a plain return in a generator function.

## v0.34.1 (2026-10-27)

v0.34.0 was tagged but not published: two new tests of locks failed on
Windows, where a shared lock stops even its holder from writing and an
exclusive one stops others from reading (the tests, not kukel, were
wrong). v0.34.1 is v0.34.0 with those tests fixed; what v0.34.0 brings
follows.

## v0.34.0 (2026-10-27; not published, these changes came with v0.34.1)

**Syntax version 8 and bytecode format 9**: `yield` is a keyword now (a
variable or function called `yield` must be renamed), `Generator` is the
name of a class of the prelude (a top-level name or a module called
`Generator` must be renamed), and `.kkc` files must be built again.

- Generators: a function with `yield` in it gives a generator, which runs
  its body a step at a time; `for` walks it, and an object whose `iter()`
  gives one (`for (local x in tree)`); `g->next([otherwise])`,
  `g->done()`, `g->close()`. A loop left early closes the generator, so its
  `finally` blocks and `with` statements run. `fs::File` has `iter()`:
  `for (local line in f)`. Q86.
- Files: `fs::temp` and `fs::tempdir` (temporary files and directories,
  kept until removed), `fs::chmod(path, "rw-r--r--")`, `f->truncate`,
  and locks: `f->lock(["exclusive" | "shared" [, timeout]])`,
  `f->unlock()` (`flock`; `LockFileEx` on Windows). Q84.
- Windows: CI tests the fs module there (Q85); `lock()` of a file opened
  `"a"` works, and `fs::exists` of a loop of symbolic links is `false`
  there too.
- Cross-check 11 (Q87): a change of a lock's kind that cannot be had keeps
  the lock the file had (on Linux `flock` gave it up, while `lock` still
  took it as held); `f->unlock()` writes out what was written under the
  lock before giving it up, and `lock` writes out first and reads afresh;
  `f->truncate` drops what was read ahead of the new end; `main` cannot
  yield, and `Generator`'s member functions cannot be defined; the
  debugger stops where a generator's body runs, not where it is made.

## v0.33.3 (2026-10-27)

v0.33.2 was tagged on the open files but not published: the fuzz test ran
past its time (fixed here). v0.33.3 has:

- Open files: `fs::open(path [, mode])` gives an `fs::File` to read a line
  or a number of bytes at a time and write as the program goes
  (`readline`, `read`, `write`, `print`, `println`, `seek`, `tell`,
  `flush`, `close`; `with` closes it); `fs::stdin()`, `fs::stdout()` and
  `fs::stderr()` are the standard streams as such files. Files a program
  leaves open are written out and closed when it ends. Q83.
- `examples/grep.kkl` reads its files a line at a time.
- Fixes from a check of the open files and `program()`: Ctrl-C in `kukel
  profile` and the REPL's exit code with files left open, writes after
  reads on pipes and terminals, write errors of `fs::stdout()->flush()`,
  `readline` and a last `\r`; `print`'s write error without Go's words. A
  worker still running when the program ends loses what its open files
  hold (documented). Q83.

## v0.33.1 (2026-10-26)

v0.33.0 was tagged but not published: a new test failed in the tk build.
v0.33.1 has that test fixed, and adds:

- `program()`: the program's file as `kukel` got it, as C's argv[0]
  (`Options.Program` in the Go API). Q82.
- `uuid::v5(namespace, name)` and `uuid::v3`: UUIDs derived from a name,
  with the namespaces `uuid::DNS`, `uuid::URL`, `uuid::OID` and
  `uuid::X500`. Q82.

## v0.33.0 (2026-10-26; not published, these changes came with v0.33.1)

- Contract tests (Q80, [docs/checks.md](docs/checks.md) §4): waits through
  `native.Wait`, the arguments the reference shows, objects not set up,
  messages without Go's operation names, and vm.md's listings against
  `kukel disasm`.
- Messages of the command name the file without Go's operation:
  `kukel: a.kkl: no such file or directory`.
- The REPL reports output it cannot write (exit code 1).
- `kukel debug`: `break line` uses the file of the frame selected with
  `frame`.
- Debuggers: a byte that is not valid UTF-8 shows as `\xNN`; in `kukel dap`
  the elements of an expression are named in parentheses (`(a + b)[0]`),
  and a failed hover or watch is not reported as an error.
- Profiler: returning from a deep recursion shows on the recursive call,
  not on the caller's next line; two programs with a file of the same name
  that share a `Profile` keep their lines apart (Q81).

## v0.32.0 (2026-10-25)

- Documents only: [docs/tutorial.md](docs/tutorial.md), a fifteen-minute
  tour (its programs are run by the tests); [docs/README.md](docs/README.md),
  the list of documents; this file.

## v0.31.0 (2026-10-25)

- **Language:** a comma may end a list closed by a bracket: arguments,
  parameters, vector and map literals (`f(a, b,)`, `[1, 2,]`). Syntax
  version 7; old programs are unchanged.
- `kukel profile`: `-cpu` now counts the work of built-ins and leaves
  only the waiting out; deep recursion costs far less (a call tree);
  calls from the Go API shorter than the interval are sampled; Ctrl-C
  writes the report.
- `zmq::Socket::send(msg [, timeout])`: a message no peer takes in time is
  a `NetError` and closes the socket.
- `kukel dap`: terminate and disconnect end a stopped program; no crashes on
  bad requests; frame ids start at 1. Both debuggers: shadowed variables,
  function breakpoints once per call, conditions checked when set, long
  values shortened.
- REPL, `kukel fmt`, CLI, tk and module fixes, clearer messages (cross-check
  10, Q79). [docs/checks.md](docs/checks.md): how to check a change.

## v0.30.0 (2026-10-20)

- `kukel profile`: which functions and lines take the time, with call
  counts, also of workers; `-pprof` for `go tool pprof`; profiling from the
  Go API (`Options.Profile`). Q78.

## v0.29.0 (2026-10-20)

- `p9::disk`: its error message without Go's operation name.

## v0.28.0 (2026-10-20)

- `kukel dap`: the Debug Adapter Protocol, for Visual Studio Code, Neovim,
  Helix and Emacs: conditional and hit-count breakpoints, logpoints,
  changing values. A VS Code extension (`make vscode`) with syntax
  highlighting; [docs/debugging.md](docs/debugging.md). Q77.

## v0.27.0 (2026-10-19)

- Cross-check 9 (Q76): output is written out before a program waits; the
  debugger shows locals in scope and stops at exceptions nothing catches;
  fused compound assignments (`x -= 1` 23-32% faster); `kukel fmt -`;
  `-nofuse`; new examples `chat.kkl`, `logstat.kkl`, a new `todo.kkl`.
- A 16-byte value was tried and not kept (Q75).

## v0.26.0 (2026-10-19)

- More fused instructions, chosen by measuring (typical programs 5-36%
  faster). **Bytecode format 8.** Q74.

## v0.25.0 (2026-10-18)

- Examples: a parallel `wc` with workers, kukel embedded in Go (Q72); the
  loose ends of cross-check 8 (Q73).

## v0.24.0 (2026-10-17)

- Cross-check 8 (Q71): a fast path for calls from the Go API, tk item
  bindings, REPL and CLI fixes.

## v0.23.0 (2026-10-17)

- tk: check and radio menu entries, pop-up menus, fonts and themes, `Scale`
  and `Combobox`, images, canvas arcs, tags and item bindings. Q70.

## v0.22.0 (2026-10-17)

- A faster VM: a fast call path and fused instructions (calls 22-40%
  faster). **Bytecode format 7.** Q69.

## v0.21.0 (2026-10-17)

- Cross-check 7 (Q68): a Tcl injection in tk closed, lost handler
  exceptions, the REPL's input and history.

## v0.20.0 (2026-10-17)

- The module `tk`: desktop windows, widgets, menus, dialogs and a canvas,
  in a kukel built with the tag `tk` (release archives `kukel-tk-...`).
  Q67.
- `kukel a.kkl [args]` runs a file, so scripts can start with
  `#!/usr/bin/env kukel`.

## v0.19.0 - v0.19.2 (2026-10-16)

- The REPL edits lines and keeps a history on a terminal; cross-check 6
  (Q65).
- A Docker image on ghcr.io with each release (Q66).

## v0.18.0 (2026-10-16)

- Releases are published by CI for each tag whose tests pass.

## v0.17.0 (2026-10-16)

- The module `p9`: a 9P client and server (9P2000 and 9P2000.L), trees
  built by the program and directories of the disk. Q62, Q63.

## v0.16.0 (2026-10-16)

- The module `fs` (directories and files); `examples/grep.kkl -r`. Q61.

## v0.15.0 (2026-10-15)

- `kukel debug`: a debugger with breakpoints, stepping and values. Q60.

## v0.14.0 (2026-10-15)

- The module `re` (regular expressions) and `examples/grep.kkl`. Q59.

## v0.13.0 (2026-10-15)

- The module `uuid`, and UUID keys in the ORM; examples for features that
  had none. Q58.

## v0.12.0 (2026-10-15)

- Cross-check 5 (Q57).

## v0.11.0 (2026-10-15)

- ORM relations: `db::many`, `db::one`, `load` and `include`. Q56.

## v0.10.0 - v0.10.1 (2026-10-15)

- `db`: PostgreSQL and MySQL/MariaDB (Q54); build tags to leave large parts
  out (`nopostgres`, `nomysql`, `nosqlite`, `nozmq`; Q55).

## v0.9.0 - v0.9.2 (2026-10-15)

- Classes can derive from native classes; host modules can define classes.
  **Bytecode format 6.** Q51.
- The ORM in `db`: objects in tables (Q52).
- `kukel fmt` puts a blank line before `if`, `while`, `for`, ... (Q53).

## v0.8.0 (2026-10-14)

- Speed: jump tables for `switch`, strings without boxing, in-place `s +=
  x`, a peephole pass. **Bytecode format 5.** Q50.

## v0.7.0 (2026-10-14)

- The `with` statement (syntax version 6), several compile errors at once,
  `kukel fmt`, `kukel repl`, modules compiled on their own (`.kkc`
  modules). Q45-Q49.

## v0.6.0 (2026-10-14)

- The module `db`: SQL with SQLite. Go 1.25 is now the minimum. Q44.

## v0.5.0 (2026-10-14)

- The module `zmq`: ZeroMQ sockets in pure Go. Q43.

## v0.4.0 (2026-10-14)

- One way for each thing: `elseif` is gone (write `else if`), the comma
  operator only in a `for` loop's first and last part, no `isnil` and
  `substr`, only `Error` objects in `throw` (syntax version 5, Q41).
  **Bytecode format 4.**

## v0.3.0 (2026-10-14)

- Temporaries after `;` in a parameter list are gone: declare them with
  `local` (syntax version 4, Q40).

## v0.2.0 - v0.2.1 (2026-10-13)

- Modules: `use name;` and `name::x` (syntax version 3); modules written in
  Go (`json`, `strings`, `time`); the event loop `events`; `worker`; the Go
  API for embedding kukel. **Bytecode format 3.** Q35-Q39.

## v0.1.0 - v0.1.2 (2026-10-13)

- The first release: the language with classes, closures and exceptions,
  the compiler, the VM, `.kkc` files (`build`, `exec`, `disasm`), the
  built-ins, the reference and [docs/vm.md](docs/vm.md). **Bytecode format
  2.**
