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

From v1.0.0 on, what keeps working is in
[docs/compatibility.md](docs/compatibility.md).

## Unreleased

- Running programs: the module `proc` (written in Kukel, on a new module
  `process` written in Go). `proc::run(["git", "log", "-n", "5"],
  options)` runs a program with its arguments as a vector (nothing split,
  expanded or run by a shell) and gives its exit code, signal, output,
  errors and time; options `input`, `stdin`, `out`, `err` (capture,
  inherit, null, a file, errors into the output), `cwd`, `env`,
  `clearenv`, `timeout`, `check` and `max`. A program that fails is a
  `proc::ProcError` (with `command`, `code`, `signal` and `err`) unless
  `check` is false. `proc::which`, `proc::env`, `proc::exitcode`. Every
  program runs in a process group of its own, which a time limit, a
  cancellation or Ctrl-C ends with what it started: `kukel run` and `kukel
  exec` now end those processes on Ctrl-C, TERM and HUP before ending as
  they did. A Go program that embeds Kukel gets `process` only when it
  names it in `StandardModules` (`nil` leaves it out). docs/future.md §15,
  Q118.
- `proc::pipe([cmd1, cmd2, ...], options)`: a pipeline through the
  operating system's pipes, without a shell, with the exit codes of all
  (`pipefail`: any failure fails it, but not a program that the next one
  stopped reading). `proc::start(cmd, options)` gives a `proc::Process` to
  talk to: its `stdin()`, `stdout()` and `stderr()` pipes as `fs::File`s
  (`"pipe"` among the options), `wait([timeout])`, `kill([signal])`,
  `pid()` and `running()`; a program left running is waited for when
  `kukel run` ends. Q118.
- `proc::sh(script, {"args": [...]})`: a script for `/bin/sh -c`, the
  values only as `$1`, `$2`, ... (not on Windows). In the event loop,
  `p->onlines(f [, stream])` calls `f(line)` for each line a started
  program writes, and `p->onexit(f)` calls `f(result)` when it ends,
  after its last lines. Q118.
- `fs::glob(pattern)`: the paths that match a pattern, sorted. Q118.

## v1.6.0 (2026-11-19)

Syntax version 12 and bytecode format 13: programs of version 11 work
unchanged, but `.kkc` files must be built again (`kukel build`). Decimals:
a new type for prices and amounts, exact in base 10, in the language, in
`json`, `db` and `toml`, and in the Go package. A program with a top-level
name `decimal` must rename it, as `decimal` is now a built-in function.

- Decimals: a type `decimal` (`typeof` gives `"decimal"`), numbers in base
  10 that are exact for `+`, `-` and `*`. Q117, docs/future.md §14.
  - Literals with the suffix `d`: `12.50d`, `3d`, `.5d`, `1_000.00d`,
    `1.5e3d` (only base 10: `0x12d` stays a hex int).
  - The scale is kept as in SQL: `12.50d` prints as `12.50`; `+`, `-` and
    `%` give the larger scale, `*` the sum (`2.50d * 1.10d` is `2.7500`).
    Inside vectors and maps a decimal prints with its `d`.
  - `/` is exact up to 34 significant digits, at the preferred scale
    (`10.00d / 4` is `2.50`), else rounded half to even.
  - With an int exact; with a float in arithmetic a `TypeError` (write
    `1.1d`, or convert with `decimal(x)`). `==`, `<`, map keys and `case`
    values are exact across int, float and decimal: `2d`, `2` and `2.0`
    are one key, and `0.1d == 0.1` is false.
  - At most 1000 significant digits and a scale of 1000 (a `RangeError`
    beyond). Small decimals do not allocate; ints and floats are as fast
    as before.
  - The built-in `decimal(x [, scale])`, from an int, float, string or
    decimal; `round(x, n)` rounds to `n` digits after the point, halves
    away from zero (decimals, ints and floats); `int`, `float`, `abs`,
    `min`, `max`, `pow`, `floor`, `ceil` and `round` take decimals, while
    `sqrt` and the other float functions refuse them with a hint.
- Decimals in modules (Q117):
  - `json::encode` writes a decimal's digits, and `json::decode(s,
    {"decimals": true})` reads numbers with a fraction or exponent (and
    ints beyond 64 bits) as exact decimals.
  - `db::open(url, {"decimals": true})`: `NUMERIC` and `DECIMAL` columns
    give decimals of their declared scale, also through the ORM; decimals
    are passed as their text. SQLite keeps 15 significant digits in such
    columns, PostgreSQL and MySQL all of them.
  - `toml::encode` writes a decimal as a float with all its digits.
  - Without the options, `json` and `db` read what they read before.
- The Go package: `kukel.Decimal(text)`, `v.AsDecimal()`, and `ToGo` gives
  a decimal's text. Q117.
- Documents: the reference (literals, types, arithmetic, the built-ins,
  `json`, `db`), syntax.ebnf and syntax.lark (version 12), vm.md (format
  13), docs/compatibility.md, the VS Code grammar and Geany's words; the
  booklet: "Decimals: exact amounts" in chapter 2, prices in JSON in
  chapter 14, "Amounts: decimals" in chapter 16. The design, with its
  questions F-64 to F-70, is docs/future.md §14. Q117.
- `examples/invoice.kkl`: an invoice priced exactly, with the VAT per
  rate rounded to cents, from JSON to text, JSON and a database. Q117.

## v1.5.0 (2026-11-19)

Syntax version 11 and bytecode format 12, as in v1.4: programs and `.kkc`
files work unchanged. New modules `toml`, `encoding`, `mail`, `logging`
and `os`; TLS and Unix datagram sockets in `net`; tabs, colours and dashed
lines in `tk`; and, in `examples/lmw/`, a client and a GUI for LMW-Synth
written in Kukel.

- A module `toml`: `toml::decode(s)` reads TOML 1.1 (and 1.0) into a map,
  its tables in the order of the text; `toml::encode(m)` writes a map as
  TOML. An offset datetime is a `time::Time`; a local date, time or
  datetime a string. Arrays and tables nest at most 32 deep. Reading uses
  [BurntSushi/toml](https://github.com/BurntSushi/toml). Q112.
- A module `encoding`: `encoding::base64(s [, width])` and
  `encoding::unbase64(s)` (which skips line breaks and takes text without
  padding), `encoding::hex(s)` and `encoding::unhex(s)`; and
  `net::hostname()`. Q113.
- TLS in `net`: `net::connect("tls://host:port" [, timeout [, options]])`
  is encrypted from the start, `c->starttls(servername [, options])`
  encrypts an open connection, `net::listen("tls://...", {"cert": ...,
  "key": ...})` hands out encrypted connections, and `c->tls()` tells
  what was agreed. Certificates are checked against the system's roots
  (options: `ca`, `servername`, `cert` and `key`, `insecure`); data that
  came before a STARTTLS handshake is refused (STARTTLS command
  injection). Q113.
- The module `mail`, written in Kukel: messages (`mail::Message`, with
  text and HTML bodies, attachments and inline parts, MIME and encoded
  headers made from what it holds) and transports by DSN (`smtp://` with
  STARTTLS required except on localhost, `smtps://`, `memory://`,
  `null://`; AUTH PLAIN and LOGIN). Q113. Safety:
  - line breaks in headers are refused (header injection), and
    `m->header` refuses every `Content-*` header, which the message makes
    itself;
  - line endings are made CRLF and dots doubled (SMTP smuggling);
  - a transport never shows its password. An `@` in the user or password
    is written `%40` (a DSN such as `smtp://u:p@ss@host` is refused), and
    the message of a DSN that cannot be read masks everything up to its
    last `@`;
  - a `MailError` for refused recipients has the `code` of the first
    refusal.
- The module `logging`, written in Kukel: structured logging after PHP's
  Monolog. A logger per channel (`logging::get("shop")`), a member
  function per syslog level, `{name}` placeholders filled from the
  context map (PSR-3); handlers `Stderr` (the default), `File` (opened
  again after logrotate), `Syslog` (the local syslog over
  `unixgram:///dev/log` in RFC 3164, servers over `udp://`, `tcp://` and
  `tls://` in RFC 5424), `Memory` and `Null`; formatters `Line`, `JSON`,
  `RFC5424` and `RFC3164`; processors `logging::host` and `logging::pid`;
  `logging::configure(map)`, from TOML say. A call below the level costs
  about an empty function call; control characters in messages and
  values are escaped so that a record cannot be forged; a handler that
  fails sends its records to standard error. Q114.
- A module `os`, written in Go: `os::pid()`, the process id. Q114.
- `net::udp` also takes Unix datagram sockets: `"unixgram:///path"`
  binds one to a socket file (removed by `close`), `"unixgram://"` makes
  one that sends, to the local syslog's `"unixgram:///dev/log"` say. Not
  on Windows. Q114.
- `tk` (Q115):
  - `tk::Notebook`: pages with tabs. Its pages are frames made in it,
    added with `n->add(frame, title)`; `n->select(i)` (or a frame),
    `n->selected()`, and `n->onchange(f)` when the user picks a tab.
  - Colours: `tk::setcolors(palette)` sets the colours of all windows,
    widgets and menus (background, foreground, field, select,
    selectforeground, trough, border, active, disabled), `tk::colors()`
    gives them; a frame takes a `background`, a label a `background` and
    a `foreground`.
  - Canvas lines and the outlines of shapes take `dash` (a dash pattern
    such as `[6, 4]`), and `tk::Canvas` gains `focus()`, for the keys of a
    canvas.

- `examples/lmw/`: a client of LMW-Synth over 9P: the module `lmw`
  (connecting, parameters, cables, ports, patch files, changes, the
  synth's `/serial` and `/keys` to play notes), a fake synth in Kukel for
  its tests, and the command-line client `lmwctl` (with `serve seconds`,
  a fake synth for the GUI, and `-fakeat address`). The example tests
  also run programs of several files in a directory of their own. Q115.
- `examples/lmw/lmwgui.kkl`: a GUI for LMW-Synth (module `tk`) that fits a
  1024 by 768 screen. Q115.
  - Tabs: the synth's module panels (Synth), the reverb (Effects), the
    cables as a matrix of inputs and outputs (Matrix), the synth drawn as
    its own Schematic tab draws it, live (Schematic), and the patch's
    notes (Notes); `-tab name` starts on a tab.
  - Faders with the synth's value texts (typed values too), choices as
    drop-down lists; writes at most every 20 ms while a fader moves, and
    a poll every 200 ms that shows changes made elsewhere.
  - Cables put in and taken out with a click in the matrix, levels set by
    dragging, typing or the keys; in the schematic they are put in, moved
    and taken out by dragging between ports, with the keys or with a
    port's menu.
  - An on-screen keyboard of 88 keys, played with the mouse (velocity by
    where a key is clicked) or with the computer keys.
  - Load and Save of patch files (also Ctrl+O and Ctrl+S), and a list of
    the running synths to switch to another.
  - When the synth goes away the window is greyed, with "Connect again";
    notes typed but not yet written are kept and written when it is back.
  - The contrast presets AA, AAA, Max and a custom ratio, kept in
    `lmw-gui.toml` (`lmwtheme.kkl` has the synth's contrast rules and unit
    colours, `contrast.kkl` the pairs of a preset, checked).
- [docs/future.md](docs/future.md): designs for Kukel-MVC, applications
  built on the schemas of the designer (§10, Q111; not built), email
  (§11), structured logging (§12) and the GUI for LMW-Synth (§13), the
  last three built in this release.
- CI fuzzes for a number of executions instead of seconds (`make fuzz
  FUZZTIME=200000x FUZZSLOW=30000x`), since Go can report the end of a
  time as a failure (golang/go#75804). Q116.

## v1.4.1 (2026-11-09)

Syntax version 11 and bytecode format 12, as in v1.4: programs and `.kkc`
files work unchanged. Documentation and releases only: the reference
shows correctly on GitHub again (it did not from v1.2.0 on), the booklet
is corrected after a comparison with the reference, and a release's notes
are its section of this file.

- Booklet chapter 1: the `kukel version` sample is that of the v1.4.0
  release (go1.27.3, Unicode 17.0.0), and says what each part of its
  first line means and that the Go and Unicode versions depend on the Go
  that built kukel.
- Releases: the notes of a GitHub release are the tag's section of this
  file, with a link to the commits since the previous tag
  (`tools/release-notes.sh`); before, GitHub's generated notes had only
  the link, as they list merged pull requests. A tag without a section
  here is not released. Q108.
- The booklet's PDF shows `|` in tables again, not `\|` (`||`, `|`, `|=`
  in chapter 5 and appendix D). Q109.
- Booklet corrections, from a comparison with the reference (Q109):
  - **Appendix C:** a nil `StandardModules` (the default) allows every
    standard module; only `[]string{}` allows none.
  - **Chapter 10:** the trace of the example names the right lines.
  - **Chapter 4:** globals and other top-level names are shared by the
    whole program, not one file.
  - **Chapter 7:** only an int from `main` is the exit code.
  - **Chapter 16:** `save` of an object with an id inserts it when its row
    is not there.
  - **Appendix D:** `|`, `^` and `&` are three precedence levels; the
    error kinds as the reference has them, with `InternalError`; named
    arguments among the statements.
  - **More complete:**
    - equality of classes and bound member functions (chapter 2);
    - `for` over an object with `iter()` (chapter 6);
    - `fs::walk` also reports its start, and never follows symbolic
      links (chapter 13);
    - the `re` flags `s` and `U` (chapter 14);
    - `vi.kkl`'s size (chapter 19).
- The reference now also has what only the booklet said: what `x++` and
  `++x` give, that `?:` evaluates only the side it chooses, what `!x` and
  `~x` give (§6), the order in which a name is looked up (§4), absolute
  paths on Windows (`fs`) and more ANSI codes (`term`). Q109.
- Booklet: chapters 10, 12, 13 and 20 explain what their examples use and
  link to the reference (and appendix D) for the full lists and rules,
  instead of repeating them: the kinds of errors, the rest of `fs`,
  `fmt`'s rules, the debugger's other commands, the remaining rules of
  modules. Chapter 12 also says that a compiled `name.kkc` is found as
  `name.kkl` is. Q109.
- On GitHub, the reference showed everything from the middle of `db` on
  wrongly, code as text and text as code, since v1.2.0: a code block in
  a list item had lost its indent. The links to its sections of modules
  (`#fs`, `#time`, ...) work again, and a test checks the code blocks of
  every document. Q110.

## v1.4.0 (2026-11-07)

Syntax version 11 and bytecode format 12: programs of version 10 work
unchanged, but `.kkc` files must be built again (`kukel build`).

- Named arguments: `box(10, 20, label: "crate")`, `new Point(y: 2, x: 1)`.
  After the positional arguments, an argument can be given by the name of
  its parameter; optional parameters may be skipped anywhere (their
  defaults run). Checked when compiling, in the calls whose function the
  compiler knows: functions (also of modules written in Kukel), static
  member functions, `Base::m(...)` and constructors; member function
  calls (`p->m(...)`), built-ins and functions written in Go refuse them
  with a message that says why. The reference (§7) and the booklet
  (chapter 7) describe them; the VM's dispatch loop is unchanged, so other
  calls are as fast as before. Q107.
- Documentation: what `kukel fmt -strict` splits around a split list is
  described as it works (vector and map literals too, outward), and
  docs/future.md §9 and §9.5 say what is built and what is not. Q106,
  Q107.

## v1.3.0 (2026-11-07)

Syntax version 10 and bytecode format 11, as in v1.2: programs and `.kkc`
files work unchanged. `kukel fmt -strict` lays out literals and top-level
declarations differently than before.

- `kukel fmt -strict` splits a vector or map literal one element per line
  when its line is longer than `-linelength`, and `-vertical` every
  literal of 2 or more elements, as they do argument lists. Q106.
- `kukel fmt -strict` puts each top-level declaration on a line of its
  own (`use json; f() {} main() {}` becomes three). The example programs,
  `iter.kkl`, and the Kukel code inside the booklet's Go programs are laid
  out by it, as the documents are. Q106.
- The open questions of [docs/future.md](docs/future.md) answered: named
  arguments (§9) for calls known when compiling first, positional
  arguments before named ones; no `ffi` module for now; v2 to move
  built-in functions into modules. Q107.

## v1.2.0 (2026-11-07)

Syntax version 10 and bytecode format 11, as in v1.1: programs and `.kkc`
files work unchanged. `kukel fmt -strict` lays code out differently than
before (formatting is not among what v1 keeps the same:
[docs/compatibility.md](docs/compatibility.md)).

- `kukel fmt -strict` also puts the arguments of a call one per line when
  there are more than 3 of them, or when the line is longer than 120
  characters (`-linelength n` sets another length), with a `,` after the
  last one and the `)` on a line of its own; `kukel fmt -vertical` does
  that for every call and definition with 2 or more items. Q106.
- `kukel fmt -strict` keeps comments with what they are about: a comment
  after a statement that gets braces stays on its line, and a comment
  after a block written on one line goes to the line of its `{`; an `else`
  on the line after a `}` is joined to it (`} else {`). The examples and
  every document's code (the booklet's too, instead of `-vertical`) are
  laid out by `kukel fmt -strict`. Q106.
- `make booklet-proof`: a copy of the booklet to proofread, with margins of
  0.5 cm and a box around each page; CI keeps it with the PDF. Only
  programs get line numbers in the PDF (not keyword lists or sample
  output). Q105.
- The booklet's PDF: code without a background (in its box), and line
  numbers in the programs. Q105.
- A proposal for named arguments (`f(1, z: 3)`), not built yet:
  [docs/future.md](docs/future.md) §9. Q107.

## v1.1.1 (2026-11-06)

Documentation only: kukel itself is as in v1.1.0.

- The booklet's appendices: embedding Kukel in Go, Go in Kukel (host
  modules), code that is not trusted, and tables to look things up; their
  Go programs are run by the tests too. The tutorial is now a ten-minute
  tour that leads into the booklet. In the PDF, code stands in a box on a
  white background. Q104, Q105.

## v1.1.0 (2026-11-06)

Syntax version 10 and bytecode format 11, as in v1.0.0: programs and
`.kkc` files work unchanged.

- The module `net`: TCP connections and connections over Unix sockets
  (`net::connect`, `net::listen`; a connection reads and writes as an
  `fs::File`, with `timeout`, `closewrite` and `ondata` for the event
  loop), UDP packets (`net::udp`: `sendto`, `receive`, `onmessage`) and
  `net::lookup`. Q103.
- The booklet ([docs/booklet](docs/booklet/README.md)): Part I, the
  language, in twelve chapters, from getting started to modules, with a
  picture of kukel's components; Part II, the modules, in nine chapters,
  from files to windows. Every program in it is run by the tests. Q104.
- The booklet as one PDF, `kukel-booklet-<version>.pdf`, with each
  release (`make booklet`: a title page, contents with page numbers,
  links within the PDF). Q105.

## v1.0.0 (2026-11-06)

The first stable release: [docs/compatibility.md](docs/compatibility.md)
says what keeps working within v1 (the language of syntax version 10, the
built-in functions and standard modules, the commands, the Go package)
and what may change (the bytecode format, messages). Q102.

- A `use` of a module that is neither found as a file nor a standard
  module now says so, and that `kukel version` lists the standard
  modules: a kukel older than the module (`use term;` in v0.37) said only
  that `term.kkl` was missing. Q100.

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
