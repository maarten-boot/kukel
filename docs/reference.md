# The Kukel language

Kukel is a small, dynamically typed language with C-like syntax, classes,
closures and exceptions. `kukel` compiles Kukel source (`.kkl` files) to
bytecode for a stack machine and runs it, or stores the bytecode in a `.kkc`
file to run later.

The formal grammar is in [`syntax.ebnf`](../syntax.ebnf); this document
describes what programs mean. How they are compiled and executed is
specified in [vm.md](vm.md).

```
// hello.kkl
main(args) {
    println("hello,", len(args) > 0 ? args[0] : "world");
}
```

```
$ kukel run hello.kkl -- Kukel
hello, Kukel
```

## Contents

1. [Running programs](#1-running-programs)
2. [Lexical structure](#2-lexical-structure)
3. [Values and types](#3-values-and-types)
4. [Programs and definitions](#4-programs-and-definitions)
5. [Statements](#5-statements)
6. [Expressions](#6-expressions)
7. [Functions and closures](#7-functions-and-closures)
8. [Classes and objects](#8-classes-and-objects)
9. [Exceptions](#9-exceptions)
10. [Built-in functions](#10-built-in-functions)
11. [Modules written in Go](#11-modules-written-in-go): db, events, fs, json, p9, re, strings, time, tk (with the build tag tk), uuid, worker, zmq

---

## 1. Running programs

| Command | What it does |
|---|---|
| `kukel run a.kkl b.kkl [-- args]` | Compile the files as one program and run it; the arguments after `--` go to `main` |
| `kukel a.kkl [args]`, `kukel prog.kkc [args]` | The same as `kukel run a.kkl -- args` (`kukel exec` for a `.kkc` file): every argument after the file goes to `main`, also a `--`. So a script whose first line is `#!/usr/bin/env kukel` runs as a command (`./script a b`); a file whose name ends in `.kkl` or `.kkc`, or that is a regular file, is taken for this, a command name first |
| `kukel check a.kkl ...` | Compile only and report the errors; prints nothing when the program is fine |
| `kukel build [-o prog.kkc] a.kkl ...` | Compile to a bytecode file (default name: the first file with `.kkc`). `-o` naming a `.kkl` file or one of the source files is a usage error |
| `kukel build -module [-o m.kkc] m.kkl` | Compile module `m` on its own into `m.kkc`, which programs can use instead of `m.kkl` (see [Modules](#modules)). The file name must be the module's name (`util.kkl`, not `my-util.kkl`); a file with a NUL byte is refused. `-o` and `-module` (also `--module`) may come anywhere among the arguments |
| `kukel exec prog.kkc [--] [args]` | Run a bytecode file (a first `--` is skipped). A module compiled on its own is not a program: `exec` and `disasm` refuse it |
| `kukel disasm prog.kkc` or `kukel disasm a.kkl ...` | Show the bytecode in readable form |
| `kukel run -nofuse ...`, `kukel build -nofuse ...`, `kukel disasm -nofuse ...` | Compile without the instructions that do the work of several (`-nofuse` comes first), to compare; see [docs/vm.md](vm.md) |
| `kukel repl [a.kkl ...]` | Read declarations and statements and run them as they come (see [The REPL](#the-repl)); with files, their declarations come first (`main` is not called) |
| `kukel debug a.kkl ... [-- args]` | Run the program under the debugger: breakpoints, stepping, values (see [The debugger](#the-debugger)) |
| `kukel profile [-o report.txt] [-pprof prof.pb.gz] [-cpu] a.kkl ... [-- args]` | Run the program as `run` does, then report where its time went: the functions and lines that took it, with call counts, also of workers (see [The profiler](#the-profiler)); `-pprof` also for `go tool pprof`; Ctrl-C stops the program and writes the report |
| `kukel dap` | The debugger for editors: the Debug Adapter Protocol on standard input and output, which an editor starts (see [docs/debugging.md](debugging.md): Visual Studio Code, Neovim, Helix, Emacs) |
| `kukel fmt [-w \| -l] a.kkl ...`, `kukel fmt -` | Format source files: indentation (4 spaces; `case` labels one level into the switch, their statements two) and the spaces between tokens (`{ }` becomes `{}`; a comment at the end of a line keeps its distance); line breaks and comments stay, at most one blank line in a row; CRLF line ends become LF (not inside a string or a `/* */` comment: those stay as written). A line that continues a statement outside brackets (`return c &&` and then `d;`) is indented one level more than the statement's first line. A line that starts with a closing bracket lines up with the line that opened it, also when more follows (`}, 1);` after `f(fn() {`); a comma at the end of a list stays, so a list with one item per line and its closing bracket on a line of its own formats as in Go. A statement that starts a line with `if`, `while`, `do`, `for`, `switch`, `try` or `with` (also after a label: `outer: for`, and with the label on a line of its own, where the blank line goes above the label) gets a blank line before it (above the comments right above it), except as the first statement of a block, after a label on its own line or a `case`, as the statement of `else` or a header without braces, or as the `while` of a `do`. The output is checked to give the same tokens and comments as the input; a file it cannot format safely is refused with an "internal error".  Prints the result, or rewrites the files (`-w`), or lists the files whose formatting differs (`-l`; `-w` and `-l` together are a usage error). A file with a syntax error or a NUL byte is left alone, and the exit code is 1. `kukel fmt -` formats standard input to standard output (for editors; with `-w`, `-l` or files it is a usage error) |
| `kukel tokens a.kkl ...`, `kukel ast [-pos] a.kkl ...` | Show the tokens or the syntax tree of each file (for debugging; `-pos`, before the files, adds positions) |
| `kukel version` (also `-version`, `--version`) | Show the version (with the commit and the Go version), the bytecode format and the modules written in Go, with the databases `db` can open (`db (SQLite, PostgreSQL, MySQL)`): a `kukel` built with tags may lack some (see the README) |
| `kukel help` (also `-h`, `-help`, `--help`) | Show the commands (`kukel` without a command shows them too, with exit code 2) |

A compile reports several errors at once, in the order of the source: the
first syntax error of each file, and otherwise the first error of each
definition, of each function and of each global initializer (at most 10
are shown, with a count of the rest). Errors in the definitions (names, classes, constants) are reported
without compiling the function bodies, whose errors could only be their
consequences.

The exit code is `main`'s result when it returns an int, else 0. A compile
error or an uncaught exception gives exit code 1; `exit(code)` ends the
program with that code. A wrong command line (an unknown command, a missing
file name, `-o` without a name or naming a source file, an option the
command does not have, such as `kukel fmt -d`: a file whose name starts
with `-` is given as `./-name`; `-` as a file anywhere but in `kukel fmt -`,
a `.kkc` file where source files go (only `exec` and `disasm` take one),
arguments after `kukel help` or `kukel version`) gives exit code 2.
Messages that are not about a place in the source start with `kukel:`
(`kukel: a.kkl: no such file or directory`). Output that cannot be
written (standard output on a full disk, say) is such an error, with exit
code 1: `kukel: write /dev/stdout: no space left on device`. The operating system keeps only the low 8 bits
of an exit code: `return 263` exits with 7, `return -1` with 255.

Files used as modules (`use name;`) are found next to the file that uses
them or through `KUKEL_PATH`; see [Modules](#modules).

A `.kkc` file starts with a format version; a file written by another
version of the format is refused (compile the program again). Files are
checked when they are loaded, so a damaged file gives an error instead of
running.

### The REPL

`kukel repl` reads the program piece by piece. A piece is either
declarations (functions, member function definitions, classes, `global`,
`const`, `use`) or statements, as in a function body; it ends when it parses
completely, so a piece can span several lines. A blank line ends a piece
that is still incomplete (and shows its error); the end of the input ends
the session. `readline()` in a piece reads the next line of the same
input. Files given to `kukel repl` that do not compile end it at once
(exit code 1). On a terminal it shows a banner and prompts (`>`, `...` for
more lines of a piece); with input from a pipe or file it shows neither.
A piece runs as soon as it is read: the initializers of its
global variables, or its statements. Every piece shares the program and its
state with the pieces before it:

```
> local n = 10
> sq(x) {
...     return x * x;
... }
> sq(n) + 1
101
```

- A statement without its final `;` gets one when that completes the
  piece. A line that is the header of an `if`, `else`, `while`, `for` or
  `with` (`if (x > 0)` alone) is not taken as a statement with an empty
  body: its statement comes on the next line. A definition's `{` goes on the line of
  its header (`f(x)` alone is a call).
- An `if` without an `else` waits for one more line. A line that starts
  with `else` joins it; an empty line (or the end of the input) runs it at
  once; any other line runs it and is then read as the next piece (or by
  a `readline()` in the `if`). Comment lines in between stay with the `if`.
  In the same way a `try` with a `catch` but no `finally` waits for a line
  that starts with `finally`.

```
> local x = 3
> if (x > 5) {
...     println("big");
... }
... else {
...     println("small");
... }
small
> if (x == 3) println("three")
... println("next")
three
next
```

- A `local` variable declared at the top level of a piece lives as long as
  the session: it is a global variable of the program (declared again, it
  is the same variable). Locals inside blocks are local as usual.
- When the last statement of a piece is an expression (not an assignment or
  `++`/`--`), its value is shown, unless it is `nil`; strings are quoted.
- A member function declared in a class can be defined in a later piece;
  until then, calling it is an error.
- A piece with a compile error adds nothing; a runtime error stops the piece
  but not the session. `exit(code)` ends the session with that code.
- `use name;` finds a module file in the current directory or through
  `KUKEL_PATH`. Positions in messages name the piece: `<input 3>:1:5`.
- A worker sees the global variables of the session with their initial
  values, as a worker of a program does; session `local`s are `nil` there.
- On a terminal the input line can be edited: the arrow keys, Home and
  End, Ctrl-A and Ctrl-E (start and end), Ctrl-K and Ctrl-U (delete to the
  end, to the start), Ctrl-W (the word before). Up and down go through the
  lines typed before, also in earlier sessions: the last 1000 are kept in
  `~/.kukel_history`, or in the file `KUKEL_HISTORY` names
  (`KUKEL_HISTORY=` keeps none; blank lines are not kept). Ctrl-C drops
  the line or the incomplete piece; Ctrl-D on an empty line ends the
  session. A program's `readline()` reads its line with the same editing
  (a line typed ahead goes to the program, not to the next prompt; the
  lines it reads are not kept), and Ctrl-C or Ctrl-D there is the end of
  its input. At other times while a piece runs, Ctrl-C stops `kukel`.

### The debugger

`kukel debug prog.kkl [-- args]` runs the program and stops before the
first line that runs, where it reads commands: the first line of `main`,
or of a global initializer when the program has one (also one of a
module). For this `shop.kkl`:

```
// A small shop: a cart of items and prices.
class Cart { items; Cart(); add(name, price); total(); }

Cart::Cart() { items = {}; }
Cart::add(name, price) {
    items[name] = price;
}
Cart::total() { local t = 0; for (local k, p in items) t += p; return t; }

// The program.
main() {
    local cart = new Cart();
    cart->add("pen", 1.5);
    println(cart->total());
}
```

a session goes like this:

```
$ kukel debug shop.kkl
main at shop.kkl:12
   12      local cart = new Cart();
(kdb) b Cart::add
breakpoint 1 at shop.kkl:6
(kdb) c
breakpoint 1, Cart::add at shop.kkl:6
    6      items[name] = price;
(kdb) p name
"pen"
(kdb) p this->items
{}
(kdb) bt
*0 Cart::add (shop.kkl:6)
 1 main (shop.kkl:13)
(kdb) finish
main at shop.kkl:14
   14      println(cart->total());
(kdb) c
1.5
the program ended (exit code 0)
```

| Command | |
|---|---|
| `break file:line`, `break line`, `break function` (`b`) | Stop at that line (a line alone: of the file of the frame `frame` selected, else of the current one), or where a call of a function starts (`main`, `Shape::area`, `util::f`; once per call, also when a loop begins on its first line); a line without code moves the breakpoint to the next line with code. With `if condition` after it (`b 12 if i == 3`), only when the condition (an expression as `print` takes) holds there: one that `print` would refuse is refused here, and one that fails when checked (an unknown name, an index out of range) stops, with the error |
| `delete [n]` (`d`), `breaks` | Remove breakpoint `n`, or all; list them (with the function of a function breakpoint) |
| `continue` (`c`) | Run to the next breakpoint |
| `step` (`s`), `next` (`n`) | To the start of the next line: also into a call, or over calls |
| `finish` (`f`) | Until the current function has returned (to the next line of its caller) |
| `print expression` (`p`) | The value of an expression without side effects: literals, names (a variable (parameter, local, captured), `this`, a data member of `this`, a static data member (`count` in a member function of its class, `C::count` anywhere), or a global (`count`, `util::count`; in a function of a module its own globals come first)), the operators, `?:`, indexing and members (`p v[i] + 1`, `p m["key"]`, `p this->items`, `p n > 0 ? "yes" : "no"`). Calls, assignments, `++`/`--`, `new` and function literals are refused: they could change the program. Constants (`const`) are not found: the compiler puts their values into the code. A long value is shortened: at most 100 elements of a vector or map and about 1000 characters, `…` for the rest; a byte of a string that is not part of valid UTF-8 shows as `\xNN` |
| `locals` | The variables of the frame: `this` in a member function, captured variables, then the parameters and locals in scope (of two with one name, the later one is the one the code sees). Static data members are not listed: `print` them |
| `backtrace` (`bt`), `frame n` | The calls, innermost first; look at frame `n` (for `print`, `locals`, `list` and `break line`) |
| `list [line]` (`l`) | The source around the current line, or around `line` |
| `quit` (`q`) | End the program, with exit code 0 (also the end of the input) |
| `help` (`h`) | The commands |

- It stops at the start of lines only (a line with several statements is
  one stop), also in global initializers and in event handlers.
- Looking never changes the program: `print` reads, it calls nothing.
  (`kukel dap`, the debugger for editors, can change values: see
  [docs/debugging.md](debugging.md).)
  A function shows the locals in scope where it is; one from a `.kkc`
  module, compiled without that information, shows all its slots, and a
  slot that two blocks share has both names (`i/w`).
- Only the main interpreter stops; workers run on. The program reads its
  input (`readline`) from the same input as the debugger.
- An exception that no `try` catches (a `finally` does not count) stops
  the program where it is thrown, before it ends: `uncaught Error: boom
  (continuing ends the program)`, then the line. `bt`, `frame`, `locals`
  and `print` show the state; `continue` (or `step`, `next`, `finish`)
  lets the program end as it would without the debugger, with the trace.
- When the program ends, `kukel debug` says so, with the exit code.

### The profiler

`kukel profile prog.kkl [-- args]` runs the program as `kukel run` does,
and then reports where its time went: which functions and lines, and how
often each function was called. For this `primes.kkl`:

```
use time;

// isPrime tests by dividing: slow on purpose.
isPrime(n) {
    for (local d = 2; d * d <= n; d++)
        if (n % d == 0) return false;
    return n > 1;
}

main() {
    local count = 0;

    for (local n = 0; n < 200000; n++)
        if (isPrime(n)) count++;
    time::sleep(0.1);                    // waiting counts too
    println(count, "primes");
}
```

the report (on standard error) is:

```
0.430 s in 313 samples, one every 1.38 ms on average (wall time, with waiting).
The time of built-ins and Go functions counts on the line that calls them.

Functions, by their own time (total: with the functions they call):
        own      %      total      %       calls  function
    0.313 s  72.7%    0.313 s  72.7%      200000  isPrime (primes.kkl:4)
    0.118 s  27.3%    0.430 s 100.0%           1  main (primes.kkl:10)

Lines, by their own time (the 4 slowest):
        own      %  line
    0.313 s  72.7%  primes.kkl:5  for (local d = 2; d * d <= n; d++)
    0.101 s  23.4%  primes.kkl:14  time::sleep(0.1);                    // waiting counts too
    0.015 s   3.5%  primes.kkl:12  for (local n = 0; n < 200000; n++)
    0.002 s   0.5%  primes.kkl:13  if (isPrime(n)) count++;

Built-ins and Go functions, by calls:
       calls  function
           1  println
           1  time::sleep
```

| Option | |
|---|---|
| `-o report.txt` | The report to that file instead of standard error |
| `-pprof prof.pb.gz` | Also the profile in Go's pprof format: `go tool pprof -http=: prof.pb.gz` shows it as a flame graph or a call graph, `-list isPrime` the source with the time of each line, `-top` the functions |
| `-cpu` | Running time only: the time spent waiting in `time::sleep`, `events::run()`, a worker's `receive` and `wait`, `readline` (also of `fs::stdin()`, pipes and terminals), and the waits of modules `zmq` and `p9` is left out. All other time counts, also the work of built-ins and Go functions (`sort`); so does the time of reading and writing files |

- It samples: every millisecond the VM notes where the program is, at
  the next point where it looks anyway (where a loop jumps back, at a
  call, after a built-in or Go function returns). The times are close for
  what runs long enough (a tenth of a second or more); the calls are
  counted exactly.
- Each sample weighs the time since the one before (with `-cpu`, less the
  waiting meanwhile), so the times add up also when samples come late:
  after a long built-in, or with `GOMAXPROCS=1` (one CPU), where they come
  about every 20 ms instead of every millisecond. The header gives the
  real average time between samples; with few samples the times are
  rougher.
- A function's own time is the time it was the innermost call; its total
  time adds what it called. A recursive function counts once in a stack.
  Deep stacks are kept whole.
- The time is wall time: waiting shows on the line that waits (above: the
  `time::sleep`), as does the time of a built-in or Go function (`sort` of
  a large vector): on the line that calls it.
- Lines: the time inside a loop shows on the loop's own line, where it
  jumps back (above: line 5 has the time of line 6 too); calls and
  built-ins show on their lines. For finer lines, move the work into a
  function.
- Workers are profiled too: their functions and Go functions are marked
  with the worker's name (`[worker 1: f]`), and a list gives the time of
  each interpreter. The percentages are then of the time of all
  interpreters together, which can be more than the wall time of the run
  (the list gives that too). A worker still running when the report is
  written has its calls counted up to its latest sample; the report says
  so. Event handlers show under the `events::run()` that runs them.
- The report also comes when the program fails or calls `exit`; the exit
  code is the program's. On Ctrl-C (SIGINT) the program is stopped (its
  workers too), the report (and the `-pprof` file) is written, and kukel
  ends as an interrupted program does (exit status 130 in a shell). A
  program that does not stop within a second, because it waits where it
  cannot be stopped (`readline`), gets its report while it waits.
- Profiling slows a program by up to about 10%; a program that is not
  profiled pays nothing for it.

## 2. Lexical structure

**Comments**: `// to the end of the line`, `# to the end of the line` (so a
first line `#!/usr/bin/env ...` is allowed) and `/* ... */` (not nested; a
`/*` without `*/` is an error).

**Identifiers**: ASCII letters, digits and `_`, not starting with a digit,
at most 255 characters; case matters. (Other characters, also UTF-8
letters, are an error outside strings and comments.)

**White space**: spaces, tabs, newlines, carriage returns, form feeds and
vertical tabs separate tokens.

**Nesting**: expressions and statements may nest at most 1000 levels deep
(parentheses, blocks, ...); deeper is a compile error.

**Keywords**: `break case catch class const continue default do else
false finally fn for global if in local new nil return static switch this
throw true try use while with`.

**Integers**: `42`, `1_000_000` (`_` only between digits), `0xFF_FF`,
`0b1010` (also `0X`, `0B`). A leading zero does not mean octal. Decimal literals go up to
9223372036854775807 (and `-9223372036854775808`); hexadecimal and binary
literals may use all 64 bits (`0xFFFFFFFFFFFFFFFF` is -1): at most 16 hex
or 64 binary digits, leading zeros included.

**Floats**: `1.5`, `1.`, `.5`, `2e10`, `1.5E-3`. A value beyond the float
range is an error; one too small to represent is 0.0 (`1e-400`). A number
literal is at most 255 characters long.

**Characters**: `'a'`, `'\n'`, `'\''` — one byte, whose value is an int (its
code): `'A'` is 65.

**Strings**: `"text"`, which may span lines. Escapes: `\n \t \r \0 \\ \" \'`
and `\x41` (one or two hex digits). Any other escape is an error.
Strings are sequences of bytes; UTF-8 text passes through unchanged. (A raw
NUL byte in the source ends its line, as if the rest were a comment;
`kukel fmt` and `kukel build -module` refuse such a file.)

---

## 3. Values and types

| Type | `typeof` | Examples |
|---|---|---|
| nil | `"nil"` | `nil` |
| bool | `"bool"` | `true`, `false` |
| int | `"int"` | `42`, `-7`, `'a'` (64-bit, wrapping around) |
| float | `"float"` | `1.5`, `1e300` (64-bit IEEE) |
| string | `"string"` | `"hello"` (immutable bytes) |
| vector | `"vector"` | `[1, "two", [3]]` |
| map | `"map"` | `{"a": 1, 2: "b"}` |
| function | `"function"` | `main`, `fn(x) { return x; }`, `p->method`, `println` |
| class | `"class"` | `Point` |
| object | `"object"` | `new Point(1, 2)` |

**Truth**: `nil`, `false`, `0` and `0.0` are false; everything else is true,
including `""`, `[]` and `{}`.

**Sharing**: vectors, maps and objects are shared, not copied:
`local w = v; push(w, 1);` changes `v` too. Use `slice(v, 0)` for a copy.

**Equality** (`==`, `!=`): numbers by value (`1 == 1.0`), strings by
content, `nil` and booleans by value; vectors, maps, objects, classes and
functions only when they are the same one (`[1] == [1]` is false); bound
methods when they are the same member function of the same object
(`p->m == p->m` is true). Values of different types are never equal (except
int and float).

**Printing**: `print`/`str` show strings as they are; inside vectors, maps
and objects strings are quoted. Floats always show a `.` or an exponent,
in the shortest form that reads back as the same float: an exponent below
1e-4 and from 1e6 on (`1.0`, `123456.0`, `1e+06`, `1.5e-05`, `inf`,
`nan`). Objects print as `Point{x: 1, y: 2}`; a container inside itself
prints as `[...]`, `{...}` or `Point{...}`. Functions print as
`<function main>`, `<function P::f>` (a static member function) or
`<function main/fn@4>` (a function literal on line 4), built-ins as `<builtin println>`, classes as
`<class P>` and bound methods as `<method P::m>`.

---

## 4. Programs and definitions

A program is one or more files; each holds definitions, in any order:

```
global count = 0, names;        // global variables (nil without a value)
const MAX = 100, HALF = MAX / 2; // constants, computed when compiling
square(x) { return x * x; }      // a function
class Point { x, y; Point(x, y); } // a class (see section 8)
Point::Point(x, y) { this->x = x; this->y = y; }

main(args) { println(square(MAX), HALF, count, names, new Point(1, 2)); }
```

- All top-level names (globals, constants, functions, classes) of the
  program, the built-in functions and the class `Error` share one name
  space (modules have their own, see [Modules](#modules)): each name
  is defined once, and the built-in names cannot be redefined or assigned.
  A local, parameter or member may reuse a built-in's name; it hides the
  built-in where it is visible.
- Using a name that is not defined anywhere is a compile error. Assigning to
  a constant, function, class, built-in or member function is a compile
  error (assigning to `p->m` where `m` is a member function is a
  `MemberError` when it runs).
- A constant's value must be computable when compiling: literals, constants
  defined earlier, and the unary, binary and `?:` operators on them (not
  calls). Every part is checked, also the part that
  `&&`, `||` or `?:` skips.
- Global initializers run in program order (files in command-line order)
  before `main`. A global can be used anywhere, also above its declaration;
  an initializer that runs earlier sees `nil`.
- The program starts at `main()` or `main(args)`, where `args` is a vector
  of the command-line arguments (strings). `main` has at most one
  parameter, without a default and not a rest parameter.
- Limits (the number of elements of a literal, of globals, of data
  members, ...) are in [vm.md, section 4.11](vm.md#411-limits); going
  beyond one is a compile error.

### Modules

A file can use other source files as *modules*:

```
// geometry.kkl
const PI = 3.14159;

class Circle { r; Circle(r); area(); }
Circle::Circle(r) { this->r = r; }
Circle::area() { return PI * r * r; }

_square(x) { return x * x; }             // private to the module
hypot(a, b) { return sqrt(_square(a) + _square(b)); }
```

```
// main.kkl
use geometry;

class Ring : geometry::Circle { inner; Ring(r, inner); area(); }
Ring::Ring(r, inner) { geometry::Circle::Circle(r); this->inner = inner; }
Ring::area() { return geometry::Circle::area() - geometry::PI * inner * inner; }

main() {
    println(geometry::hypot(3, 4), round(new Ring(2, 1)->area()));
    println(new geometry::Circle(1));
}
```

```
$ kukel run main.kkl
5.0 9
geometry::Circle{r: 1}
```

- `use name;` at the top of a file, before its definitions, loads module
  `name`: the file `name.kkl` in the directory of the file with the `use`,
  else in one of the directories listed in the environment variable
  `KUKEL_PATH` (separated by `:`, or `;` on Windows; empty entries are
  skipped, not taken as the current directory). In each directory a
  compiled `name.kkc` counts when there is no `name.kkl`.
- A module is an ordinary source file with its own name space. Its
  top-level names are reached as `name::x`: functions (`geometry::hypot(3,
  4)`), constants (also in constant expressions), globals (which can be
  assigned), and classes (`new geometry::Circle(1)`, `class Ring :
  geometry::Circle`, `geometry::Circle::x` for a static member).
- `use` holds for the file that contains it; a file uses a module once, and
  a module cannot use itself. A module sees its own names,
  the built-ins, `Error`, and the modules it uses itself; it does not see the
  program's names. A program and its modules may define the same names.
- Top-level names that start with `_` are private: `geometry::_square` is a
  compile error outside the module. Members of a module's classes are not
  private (`p->_m()` works).
- Each module is loaded once, however many files use it, and its global
  initializers run before those of the files that use it. A module name
  stands for one file in the whole program. Modules may not use each other
  in a cycle, and a file given on the command line cannot also be a module.
- A standard module that a `kukel` was built without (build tags, see the
  README) is not found; the message names the tags.
- A module's name cannot be `Error`, nor a top-level name of the program or
  module whose file uses it. It may be a built-in's name: `time::now()` and
  `time()` do not mix up.
- A class of a module is printed and named in messages with its module:
  `geometry::Circle`. Its constructor is still `Circle`.
- `kukel build` stores the program and all its modules in one `.kkc` file;
  `kukel exec` does not need the module files.
- `kukel build -module util.kkl` compiles a module on its own into
  `util.kkc` (the modules it uses must be there when it is compiled; the
  file keeps the module's name, so a `util.kkc` renamed to `other.kkc` is
  an error for `use other;`). A
  program that uses `util` can then be compiled without `util.kkl`: the
  compiler links the module's code into the program. The `.kkc` file holds
  the module's declarations without function bodies and initializers (its
  "header", which the compiler reads as it reads a source module) and the
  compiled code. When the modules it was compiled against have changed in
  a way its code depends on (a class's data members, a function it calls
  that is gone), linking it is an error that says to compile the module
  again. A `.kkc` module is not a program: `kukel exec` refuses it.

With the two files above:

```
$ kukel build -module geometry.kkl     # writes geometry.kkc
$ rm geometry.kkl                      # (or move it away)
$ kukel run main.kkl                   # uses geometry.kkc
5.0 9
geometry::Circle{r: 1}
$ kukel build -o prog.kkc main.kkl     # the program with geometry's code
$ kukel exec prog.kkc
5.0 9
geometry::Circle{r: 1}
```

---

## 5. Statements

```
{ local a = 1, b; a = 2; }         // a block; locals are visible after their initializer
if (x > 0) f(); else if (x < 0) g(); else h();
while (i < 10) i++;
do { i--; } while (i > 0);
for (local i = 0; i < n; i++) ...  // the locals belong to the loop
for (i = 0, j = n; i < j; i++, j--) ...  // the comma operator: only in a for loop's first and last part
for (;;) { if (done) break; }      // no condition: until break
for (local x in v) ...             // elements of a vector, keys of a map, bytes of a string
for (local i, x in v) ...          // index and element; key and value; index and byte
switch (x) { case 1: case 2: ...; break; default: ... }
outer: for (...) { for (...) { if (...) continue outer; break outer; } }
return value;
throw value;
try { ... } catch (e) { ... } finally { ... }
```

**Locals**: `local` declares variables in the innermost block. A declaration
must be a statement directly in a block or a `for` initializer (not, say,
the body of an `if` without braces), and may not repeat a name of the same
block.

**For-each**: a vector is walked by index while the index is below its
current length (elements added during the loop are visited); a map is
walked over the keys it had when the loop started, in insertion order,
skipping keys deleted in the meantime. Anything else is a `TypeError`.

**Switch**: the value is compared with `==` to each `case` (`1` and `1.0`
match); control falls through to the next label until `break`. Case values
are constant expressions (with `?:` in parentheses: `case (A ? 1 : 2):`);
duplicates and a second `default` are errors.
`case` labels may appear inside nested statements (as in C), but not where
jumping to them would enter a try or with block or skip a `local`'s
initialization.
A `local` directly in a switch body is not allowed: put it in a block.

**Labels**: `name:` before a statement (on the same line as the name); a
label may not reuse the name of a label around it. `break name` leaves that
statement; `continue name` continues that loop (a label of a switch or a
block is an error there).
A plain `break` leaves the innermost loop or switch, a plain `continue`
continues the innermost loop. Neither crosses a function literal.

**Finally**: a `finally` block runs however its try block is left: at the
end, by `return`, `break` or `continue`, or by an exception. `return` in a
finally block, and `break`/`continue` out of it, are compile errors.

**With**: `with` closes what it opens, however its statement is left:

```
use db;

main() {
    with (local conn = db::open("sqlite::memory:")) {
        conn->exec("CREATE TABLE t (x)");
        println(conn->first("SELECT count(*) AS n FROM t"));   // {"n": 0}
    }                                     // conn->close() has been called here
}
```

With a class of your own: anything with a `close()` member function.

```
class Log { name; Log(name); write(s); close(); }
Log::Log(name) { this->name = name; println("open", name); }
Log::write(s) { println(name + ":", s); }
Log::close() { println("close", name); }

main() {
    with (local a = new Log("a"), b = new Log("b")) {
        a->write("one");
        b->write("two");
    }
}
```

```
$ kukel run log.kkl
open a
open b
a: one
b: two
close b
close a
```

- `with (local a = e1, b = e2) statement` declares the locals (each needs a
  value) for the statement. When the statement is left (at its end, by
  `return`, `break`, `continue` or an exception), `x->close()` is called for
  each of them that is not `nil`, the last one first.
- Any value with a `close()` member function works: `fs::File`, `db::Conn`,
  `db::Cursor`, `zmq::Socket`, `p9::Client`, `p9::Server`, and objects of
  your own classes. A
  value without one gives a `TypeError` or `MemberError` when it is closed.
- If the statement throws and a `close()` throws too, the statement's
  exception goes on and the close failure is dropped. If only a `close()`
  throws, its exception goes on after the other locals are closed. If an
  initializer throws, the locals before it are closed.
- As with `try`, a `case` or `default` label may not be inside a `with` that
  is inside its switch.

---

## 6. Expressions

Operators, from lowest to highest precedence:

| Operators | Associativity | Notes |
|---|---|---|
| `,` | left | only in the first and last part of a `for` loop: evaluates both, gives the right one |
| `= += -= *= /= %= &= \|= ^= <<= >>=` | right | the value of an assignment is the assigned value |
| `c ? a : b` | right | |
| `\|\|` | left | gives `true` or `false`; stops when the left side is true |
| `&&` | left | gives `true` or `false`; stops when the left side is false |
| `\|` | left | bitwise or, ints only |
| `^` | left | bitwise exclusive or, ints only |
| `&` | left | bitwise and, ints only |
| `==` `!=` | left | see equality in section 3 |
| `<` `<=` `>` `>=` | left | numbers with numbers, strings with strings (byte order) |
| `<<` `>>` | left | ints; the count must be 0..63; `>>` keeps the sign |
| `+` `-` | left | `+` also joins two strings |
| `*` `/` `%` | left | |
| `-x` `!x` `~x` `++x` `--x` | right | |
| `f(x)` `v[i]` `p->x` `p->m(x)` `x++` `x--` | left | |
| `new C(...)`, `fn (...) { ... }`, `C::x`, `m::x`, literals, `(e)` | | primary expressions: they bind tightest (`new C()->m()` is `(new C())->m()`) |

**Numbers**: ints wrap around on overflow. An int with a float gives a
float. `int / int` truncates toward zero and `%` has the sign of the left
side; `/` or `%` by integer 0 is a `ZeroDivisionError` (a float division by
zero gives `inf` or `nan`). `%` with a float is the float remainder
(`7.5 % 2` is `1.5`; `7.0 % 0` is `nan`). Comparing an int with a float is
exact.

**Strings**: `+` needs two strings: `"n = " + str(n)`. `s[i]` gives a byte
as an int; strings cannot be changed (`s[i] = x` is an error).

```
main() {
    println(7 / 2, -7 / 2, 7 % 3, -7 % 3, 7.0 / 2, 7.5 % 2);
    println(1 << 4, -16 >> 2, 5 & 3, 5 | 3, 5 ^ 3);
    println("n = " + str(42), "ab" + "cd");
}
```

```
$ kukel run ops.kkl
3 -3 1 -1 3.5 1.5
16 -4 1 7 6
n = 42 abcd
```

**Indexing**: `v[i]` with an int from 0 to `len(v) - 1` (no negative
indexes); assigning `v[len(v)]` is an error (use `push`). `m[k]` gives
`nil` for a missing key (use `haskey` to tell a stored nil apart). Map keys
are strings and numbers (not `nan`); `1` and `1.0` are the same key.

**Literals**: `[a, b, v..]` (`v..` inserts the elements of vector `v`),
`{key: value, ...}` — keys are expressions: `{name: 1}` uses the variable
`name`; write `{"name": 1}` for a string key. A `{` that starts a statement
is a block, never a map.

**A comma at the end**: a list closed by a bracket may end with a comma:
the arguments of a call, a method call or `new`, the parameters of a
function, a member function's declaration or a function literal, and the
elements of a vector or map literal. So a list written one item per line
can end every line with a comma, and lines can be added or moved freely:

```
main() {
    local colours = [
        "red",
        "green",
    ];
    local sizes = {"s": 1, "m": 2,};

    println(len(colours), sizes["m"]);   // 2 2
}
```

A lone comma (`f(,)`, `[,]`) is an error. The lists that end with `;`
(`global`, `const`, `local`, data members), the variables of `with` and of
a `for` loop, and the comma operator take no comma at the end. (Syntax
version 7, Kukel v0.31.)

**Assignable**: variables, a module's globals (`m::x`), `C::x` (static data member), `v[i]`, `p->x`. In
`v[f()] += 1` and `p->x++`, `v`, `f()` and `p` are evaluated once.

---

## 7. Functions and closures

```
area(w, h = w) { return w * h; }               // h is optional, its default may use w
report(level, parts..) { println(level, parts..); } // parts: a vector of the rest
sum(v) {
    local total = 0;

    for (local x in v) total += x;
    return total;
}

main() {
    report("info:", area(3), area(2, 5), sum([1, 2, 3]));   // info: 9 10 6
}
```

- Required parameters come first, then optional ones (with defaults), then
  at most one rest parameter (`name..`). Parameter names must differ; a
  `local` in the body may reuse one, and hides it from there on. A default runs only when the
  argument is missing (an explicit `nil` stays `nil`) and may use the
  parameters before it.
- A function without `return value` returns `nil`.
- Variables a function needs are `local` declarations in its body. (Up to
  syntax version 3, names after a `;` in the parameter list, `f(a; t)`,
  declared them; that form is now a compile error that says so.)
- `f(a, v.., b)` passes the elements of vector `v` as separate arguments
  (`v..` is allowed in argument lists and vector literals only).
- A call with the wrong number of arguments is a compile error when the
  function is known when compiling (a function written in Kukel, also of a
  module; a static member function; `Base::m(...)`; `new C(...)`), and an
  `ArgumentError` otherwise: calls through variables and `p->m(...)`, and
  built-ins and functions of modules written in Go, which check their
  arguments when they run.
- Functions are values: `local f = square; f(3);`.
- Recursion is limited to 10000 nested calls (`StackOverflowError`).

**Function literals** (closures):

```
counter() {
    local n = 0;
    return fn() { return ++n; };   // captures n by reference
}

main() {
    local next = counter();
    next(); next();
    println(next());                // 3

    local fs = [];

    for (local i = 0; i < 3; i++) push(fs, fn() { return i; });
    println(fs[0](), fs[1](), fs[2]());   // 0 1 2: a new i for each iteration
}
```

- A literal captures the variables of the functions around it by
  reference: changes are seen both ways, and the variables live as long as
  a closure needs them.
- Loop variables are new in every iteration (`for (local ...)`, for-each,
  and `local`s in a loop body), so closures made in a loop each see their
  own. In `for (local i ...)` the next iteration's `i` starts with the value
  at the end of the previous one, before the update.
- In a member function, a literal also sees `this` and the members.
- In stack traces a literal is called `<function>/fn@<line>`. The global
  initializers of a file run in a function called `<init>`: an exception
  there shows `at <init> (file.kkl:line)`. The class `Error` is written in
  Kukel, in a part of every program called the prelude, so a trace can
  show its frames as `<prelude>`: `new Error(5)` gives
  `at Error::Error (<prelude>:9)`, above the caller's frame.

---

## 8. Classes and objects

```
class Shape {
    name;                   // a data member (nil in a new object)
    static count;           // a static data member, shared by the class
    Shape(name);            // the constructor (a member function named like the class)
    area();                 // declared only: derived classes define it
    describe();
    static total();         // a static member function (no this)
}

Shape::Shape(name) { this->name = name; count = count == nil ? 1 : count + 1; }
Shape::describe() { return name + " " + str(area()); }   // area() is this->area()
Shape::total() { return count; }

class Rect : Shape {
    w, h;
    Rect(w, h);
    area();
}

Rect::Rect(w, h) { Shape::Shape("rect"); this->w = w; this->h = h; }
Rect::area() { return w * h; }

class Square : Rect {}     // no constructor: uses Rect's

main() {
    local s = new Square(3, 3);
    println(s->describe(), Shape::total(), s);   // rect 9 1 Square{name: "rect", w: 3, h: 3}
}
```

- A class declares its members; member functions are defined outside as
  `Class::name(params) { ... }` with the same number of parameters (default
  values go in the definition, never in the declaration). Each name is
  declared once in a class (no overloading), and each member function is
  defined once. The base class
  must be defined before the classes derived from it. It may also be a
  class written in Go ([section 11](#11-modules-written-in-go)).
- `new C(args)` makes an object with all data members `nil` and runs the
  constructor: `C::C`, or, when `C` declares none, the nearest base class's.
  A constructor cannot be static.
  Without any constructor `new C()` takes no arguments. `new` also works
  with a class in a variable: `local k = Rect; new k(1, 2)`.
- Base constructors are not called automatically: call `Base::Base(...)`.
- Inside a member function, a name is looked up as a local, parameter,
  captured variable, then a member of the class (also inherited
  ones), then a top-level name. `x` means `this->x`, `f()` means
  `this->f()`. A static member function has no `this`.
- `p->m(...)` calls the member function of `p`'s class (all member
  functions are virtual). `Base::m(...)` calls `Base`'s version on the
  current `this` (not virtual; spread arguments are not allowed there).
  `C::f(...)` calls a static member function; `C::x` is a static data
  member. `C::m(...)` of a member function that is not static is allowed
  only in member functions of `C` and of classes derived from it, and not
  in a static one.
- A derived class may redeclare a member function of its base (overriding
  it) if both are static or both are not; the parameters may differ.
  Redeclaring a data member is an error, as is a data member named like a
  member function of a base class, or the reverse. A definition
  `C::m(...)` needs a member function `m` declared in `C` itself (not a
  data member, not one only declared in a base class).
- Inside a member function, the class's own name means the class
  (`return A;`, `new A()`); constructors are not found by an unqualified
  name (call `Base::Base(...)`): `A(...)`, anywhere, would call the class,
  so it is a compile error ("A(...) calls the class; make an object with
  new A(...)"; also `m::C(...)` for a module's class). In a member
  function of `A` or of a class derived from it, the message also names
  the constructor `new A(...)` would run, `A`'s own or the nearest base
  class's ("call its constructor as A::A(...)"). A class in a variable is
  called only when it runs, a `TypeError` ("cannot call class A").
  `p->C(...)` calls the constructor `C` again on an existing object `p`,
  as any member function: the constructor's body runs again on `p`, so the
  members it assigns change and the others keep their values.
- `p->x` reads or assigns a data member (all members are public). Static
  data members are reached as `C::x`, or in a member function of the class
  by their name alone (`count++`), never as `p->x` (a `MemberError` that
  says so: "c is a static data member: use A::c"); a static member function can
  also be called as `p->f(...)` (the static of `p`'s class), and `p->f`
  without a call gives that function itself, not a bound method.
  `p->m` without a call is a *bound method*: a function value that calls
  `m` on `p` later (`sort(v, p->less)`). `(p->f)(x)` calls a function held
  in a data member.
- A member function may be declared without a definition. Calling it is a
  compile error where the compiler knows the callee (`new C(...)`,
  `C::f(...)`, `Base::m(...)`), else a `MemberError` ("declared but not
  defined") when the call runs (`p->m(...)`).

---

## 9. Exceptions

```
class ParseError : Error { line; ParseError(msg, line); }
ParseError::ParseError(msg, line) { Error::Error(msg); this->line = line; }

main() {
    try {
        throw new ParseError("unexpected '}'", 12);
    } catch (e) {
        if (instanceof(e, ParseError)) println(e->kind, e->message, e->line);
        else throw e;                       // rethrow: keeps the original trace
    } finally {
        println("cleanup");
    }
}
```

- Only `Error` objects can be thrown: objects of the built-in class `Error`
  or of a class derived from it, with the data members `message`, `kind`
  and `trace` (`throw new Error("bad input", "ValueError")`). Throwing
  another value is a compile error for a literal (`throw "x"`), else a
  `TypeError`. `catch (e)` catches everything, so `e->kind` and
  `e->message` always work.
- Runtime errors throw `Error` objects too; programs derive their own
  classes from it (`new Error([message [, kind]])`). The message and the
  kind are strings or `nil`: when `new` has made an `Error`, or an object
  of a class derived from it, another value in `message` or `kind` is a
  `TypeError` ("Error: the message must be a string or nil, not int"; the
  class's own name for a derived class). This is checked when `new` makes
  the object only: `e->message = 5` later is not checked.
- The first time an `Error` is thrown, `trace` gets the stack trace (a
  vector of strings such as `"f (prog.kkl:3)"`, innermost call first) and,
  if `kind` is nil or `""`, `kind` becomes the name of its class.
- An exception thrown in a `finally` block replaces the one in progress.
- An uncaught exception ends the program with exit code 1, printing
  `kind: message` (just `kind` when the message is nil or empty) and the
  stack trace; a trace of more than 21 calls shows the first and last 10
  (`e->trace` keeps all of them).
- `exit(code)` ends the program at once; it cannot be caught and `finally`
  blocks do not run.

Kinds of runtime errors:

| Kind | When |
|---|---|
| `TypeError` | an operation on values of the wrong type (`1 + "a"`, calling an int, ...) |
| `ZeroDivisionError` | integer `/` or `%` by zero |
| `RangeError` | an index out of range, a shift count outside 0..63, a number that does not fit, an argument outside the range a built-in takes (`char(300)`, `randint(5, 1)`, `pop([])`) |
| `ArgumentError` | a wrong number of arguments; an argument a built-in cannot work with (`replace` of `""`, `min` of no values) |
| `MemberError` | an unknown data member or member function, a call of a member function declared but not defined (when the compiler could not see it) |
| `ValueError` | a value a function cannot work with (a string that is not a number for `int` or `float`, invalid JSON, a date that does not exist, ...), or a call that is not possible in the current state (`events::run` from a handler, `send` to a stopped worker, ...) |
| `IOError` | a file or input/output error |
| `StackOverflowError` | more than 10000 nested calls |
| `CompileError` | `worker::startfile` of a program with a compile error |
| `WorkerError` | `w->wait()` of a worker that failed (the message holds the worker's error) |
| `DBError` | an error the database reports: SQL that is not valid, a broken constraint (UNIQUE, CHECK, FOREIGN KEY), a database that cannot be opened |
| `NetError` | a network problem in `zmq` or `p9`: an address that cannot be bound, listened on or reached, a server that does not answer, a connection that is lost, a message that cannot be sent or received |
| `HostError` | a function of a Go program that embeds Kukel failed ([embedding.md](embedding.md)) |
| `CancelledError` | the Go program that runs Kukel stopped it (a time limit); cannot be caught |
| `StepLimitError` | the step limit set by a Go program that runs Kukel was reached; cannot be caught |
| `InternalError` | a damaged `.kkc` file that the loader's checks did not catch (should not happen) |

---

## 10. Built-in functions

`isnil` and `substr` were removed in v0.4 (the compiler says so, as for
the removed keyword `elseif`): write `x == nil` and `slice(s, start, end)`.

Argument positions in error messages count from 1. Unless noted, a wrong
type is a `TypeError`, a wrong count an `ArgumentError`, a number or index
outside what the function takes a `RangeError`, and another value it cannot
work with an `ArgumentError`.

### Output and input

| Function | Description |
|---|---|
| `print(x, ...)` | Writes the values separated by single spaces (strings as they are); no values write nothing. Output that cannot be written is an `IOError` (also for the others below). Output is buffered, so a failure may show only later; what fails to be written when the program ends is reported by `kukel` (`kukel: write ...`, exit code 1) |
| `println(x, ...)` | Like `print`, then a newline |
| `eprint(x, ...)` | Like `print`, to standard error |
| `eprintln(x, ...)` | Like `println`, to standard error |
| `readline()` | The next line of standard input without its line ending, or `nil` at the end |

`kukel` buffers standard output; `eprint`, `eprintln` and `readline` write
out what is buffered first, so a prompt appears before the input is read
and the order of output and errors stays as written. So do the
functions of modules that wait (`time::sleep`, `events::run`, `receive`,
`wait` and `poll` of `worker` and `zmq`, `send` and `connect` of `zmq`,
the calls of a `p9` client, `db::open` and the calls that run SQL): what a
program printed before a wait is seen during it.

| Function | Description |
|---|---|
| `readfile(path)` | The whole file as a string (`IOError` on failure) |
| `writefile(path, s)` | Writes `s` to the file, replacing it (`IOError` on failure) |
| `appendfile(path, s)` | Appends `s` to the file, creating it if needed (`IOError` on failure) |

### Types and conversion

| Function | Description |
|---|---|
| `typeof(x)` | `"nil"`, `"bool"`, `"int"`, `"float"`, `"string"`, `"vector"`, `"map"`, `"function"`, `"class"` or `"object"` |
| `str(x)` | `x` as text, as `print` writes it |
| `int(x)` | An int: floats are truncated toward zero; strings in decimal, `0x` or `0b` form with an optional sign (`"42"`, `"-0x10"`, `"0b11"`), surrounding spaces allowed and every `_` ignored (`"1_000"`); `true` is 1, `false` 0. A string that is not a number is a `ValueError`, one too large a `RangeError`; hexadecimal and binary use all 64 bits, as literals do (`int("0xffffffffffffffff")` is -1) |
| `float(x)` | A float from an int, float or string (`"2.5"`, `"1e3"`, `"inf"`, also Go's other forms such as `"0x1p3"`; surrounding spaces are allowed and every `_` is ignored, as for `int`: `" 1_000.5 "`, also `"_1_"`). A string beyond the float range is a `RangeError`; one too small gives 0.0 |
| `classof(obj)` | The class of an object |
| `instanceof(x, C)` | Whether `x` is an object of class `C` or of a class derived from it |

```
main() {
    println(int("42") + 1, int(" -0x10 "), int("1_000"), int(3.9), int(-3.9));
    println(float("2.5") * 2, float(" 1e3 "), str(1.0 / 4) + "!");

    try { int("12abc"); } catch (e) { println(e->kind, e->message); }
}
```

```
$ kukel run conv.kkl
43 -16 1000 3 -3
5.0 1000.0 0.25!
ValueError int: "12abc" is not an integer
```

### Strings

| Function | Description |
|---|---|
| `len(x)` | Length of a string (in bytes), vector or map |
| `find(s, sub [, from])` | Index of the first `sub` at or after `from`, or -1 |
| `split(s, sep)` | Vector of the parts between the `sep`s; with `""`, the single bytes |
| `join(v [, sep])` | The elements joined with `sep` (default `""`); non-strings as `str` gives them |
| `upper(s)`, `lower(s)` | ASCII letters in upper or lower case |
| `trim(s)` | `s` without white space at both ends |
| `replace(s, old, new)` | Every `old` replaced by `new` (`ArgumentError` if `old` is `""`) |
| `char(code)` | A one-byte string (code 0..255) |
| `ord(s [, i])` | The byte at `i` (default 0) as an int |

### Vectors and maps

| Function | Description |
|---|---|
| `push(v, x, ...)` | Appends the values (at least one); returns the new length |
| `pop(v)` | Removes and returns the last element |
| `insert(v, i, x)` | Inserts `x` at index `i` (0..`len(v)`); returns `nil` |
| `remove(v, i)` | Removes and returns the element at `i` |
| `slice(x, start [, end])` | A new vector (or string) of the elements (bytes) from `start` up to, not including, `end` (to the end without it): `slice("hello", 1, 3)` is `"el"` |
| `sort(v [, less])` | Sorts `v` in place (stable); `less(a, b)` says whether `a` goes before `b` (default `<`, so values that `<` cannot compare are a `TypeError`). An exception in `less` comes out of `sort`; after any error `v` is unchanged |
| `keys(m)`, `values(m)` | Vectors of a map's keys or values, in insertion order |
| `haskey(m, k)` | Whether `k` is a key of `m` |
| `delete(m, k)` | Removes a key; returns whether it was there |

```
main() {
    local v = [5, 2, 8, 1];
    sort(v);
    println(v, slice(v, 1, 3), slice("hello", 1));
    local people = [{"name": "Ann", "age": 41}, {"name": "Bo", "age": 25}];
    sort(people, fn(a, b) { return a["age"] < b["age"]; });
    println(people[0]["name"], len(people));
}
```

```
$ kukel run vectors.kkl
[1, 2, 5, 8] [2, 5] ello
Bo 2
```

### Math

| Function | Description |
|---|---|
| `abs(x)` | Absolute value |
| `min(a, b, ...)`, `max(a, b, ...)` | Smallest or largest of numbers or of strings (mixing them is a `TypeError`; of equal values, the first); also `min(vector)` (`ArgumentError` for no values); `min(x)` of one number or string is `x` |
| `pow(x, y)` | `x` to the power `y`: an int for ints with `y >= 0` (wrapping around), else a float |
| `sqrt(x)`, `exp(x)`, `log(x)` | Square root, e<sup>x</sup>, natural logarithm (floats) |
| `sin(x)`, `cos(x)`, `tan(x)`, `atan2(y, x)` | Trigonometry (radians) |
| `floor(x)`, `ceil(x)`, `round(x)` | Rounded to an int (`round`: halves away from zero); `RangeError` if it does not fit |
| `random()` | A float from 0 up to (not including) 1; every run starts from a different seed |
| `randint(a, b)` | An int from `a` to `b`, both included (`RangeError` if `a > b`) |
| `seed(n)` | Restarts the random numbers from seed `n` (the same seed gives the same numbers) |

### System

| Function | Description |
|---|---|
| `time()` | Seconds since 1970-01-01 UTC, as a float |
| `clock()` | Seconds since the program started (for an embedded interpreter: since it was made), as a float |
| `program()` | The program's file as `kukel` was given it, as C's `argv[0]`: `"a.kkl"` for `kukel run a.kkl` (the first file when there are several) and `kukel a.kkl`, `"prog.kkc"` for `kukel exec prog.kkc`, the script's path for a script started by its `#!/usr/bin/env kukel` line (`"./tool"`, or the path the shell found it at). For usage messages and files next to the program (`fs::join(fs::dir(program()), "data.txt")`). A worker of `worker::start` gives its parent's, one of `worker::startfile(path)` that `path`; the REPL `""`, and an embedding Go program what it sets ([embedding.md](embedding.md)) |
| `getenv(name)` | The environment variable, or `nil` |
| `exit([code])` | Ends the program at once with `code` (default 0; the system keeps its low 8 bits, as for `main`'s result) |

---

## 11. Modules written in Go

These modules are part of `kukel` (`kukel version` lists them). A program
uses them like modules written in Kukel (see [Modules](#modules)): `use
json;`, then `json::encode(v)`. No file is involved, and a file `json.kkl`
does not replace them.

Their classes are *native classes*: written in Go, their objects hold Go
data and have member functions but no data members. `typeof` gives
`"object"`, `classof` and `instanceof` work as for other classes, and `==`
compares whether two values are the same object. A native class without
a constructor has no `new`: its objects come from the module's functions.

A class written in Kukel may derive from a native class that has a
constructor:

```
use strings;

class Lines : strings::Builder {
    count;
    Lines(first);
    line(s);
}

Lines::Lines(first) {
    strings::Builder::Builder(first);       // sets up the Go part
    count = 0;
}

Lines::line(s) {
    count++;
    return add(s, "\n");                    // a member function written in Go
}

main() {
    local l = new Lines("# log\n");
    l->line("one")->line("two");
    print(l->str());                        // # log, one, two (one per line)
    println(l->count, instanceof(l, strings::Builder));   // 2 true
}
```

- The object holds the Go part of the native class next to its own data
  members. The derived constructor sets the Go part up by calling the
  native constructor, `strings::Builder::Builder(...)`, as for any base
  class; a class without its own constructor uses the native one. Calling
  a member function written in Go before that is a `ValueError` ("the
  strings::Builder part of this Lines object is not set up").
- The member functions of the native class are inherited, and the derived
  class may override them; `strings::Builder::add(...)` calls the native
  one (without spread arguments). The Go code itself calls its own member
  functions, not the overrides.
- Where a module's function takes an object of the native class (`time`
  values for `sub`, sockets for `zmq::poll`, ...), it also takes objects of
  classes derived from it.
- A member of the derived class may not have the name of a member function
  of the native class, except a member function with the same static-ness
  (an override). A derived object prints as Kukel objects do
  (`Lines{count: 2}`), not as the native class prints its objects.
- The number of arguments of a native constructor or member function is
  checked when it runs (an `ArgumentError`), also for `new D(...)` with
  the native constructor and for `strings::Builder::str(...)`.
- Calling the native constructor again (`strings::Builder::Builder("x")`
  from a member function) sets up a new Go part, dropping the old one.
- Member functions and static member functions written in Go give objects
  of the native class, not of the derived one: for `class T : time::Time`,
  `t->add(60)` and `T::parse(...)` give `time::Time` objects.

### db

SQL databases: SQLite, a whole database in one file (or in memory) with
nothing to install, and PostgreSQL and MySQL (or MariaDB) servers. `kukel`
carries all three drivers. A program works the same on each: the same
member functions, `?` for parameters everywhere, and the same values back.

```
use db;

main() {
    local conn = db::open("sqlite::memory:");      // or "sqlite:shop.db"
    conn->exec("CREATE TABLE items (name TEXT, price REAL)");
    conn->exec("INSERT INTO items VALUES (?, ?)", "pen", 1.5);

    for (local row in conn->query("SELECT * FROM items WHERE price < ?", 10))
        println(row["name"], row["price"]);         // pen 1.5
    conn->close();
}
```

Values always go into SQL as parameters: each `?` takes the next argument
(too few arguments are a `DBError`; so are extra ones, except in SQLite,
which ignores them). PostgreSQL
numbers its parameters (`$1`, `$2`); the module turns each `?` into the
next one, except a `?` inside a quoted string (also `E'...'`) or name, a
comment or `$$` text. `$1` written directly works too; a `?` in the
same statement then counts on from the highest `$n` written
(`"WHERE a = $1 OR b = ?"` takes two arguments). PostgreSQL's JSON operators that
are written with `?` must use their function form there
(`jsonb_exists(doc, 'key')`).
They are never glued into the SQL text, so text from a user cannot change
the statement (SQL injection).

| Kukel | SQL | back in Kukel |
|---|---|---|
| `nil` | NULL | `nil` |
| int, float | INTEGER, REAL | int, float (an int in a REAL column comes back as a float) |
| string | TEXT | string (bytes; also for BLOB columns) |
| bool | BOOLEAN (SQLite and MySQL store 1 or 0) | `true`/`false` for a column declared `BOOLEAN` or `BOOL`; from MySQL, whose BOOLEAN is a TINYINT, 1 or 0 (objects get booleans, see below) |
| `time::Time` | a timestamp | `time::Time` for a column declared `DATETIME`, `DATE` or `TIMESTAMP` (PostgreSQL: `TIMESTAMP`, `TIMESTAMPTZ`, `DATE`; a value there that is not a date comes back as a string, in SQLite); store times in such columns, as a TEXT column keeps a text form only |

Numbers that a server sends as text (MySQL, PostgreSQL's `NUMERIC`) come
back as ints or floats by the column's type. For floats that mean the same
everywhere, declare columns `DOUBLE PRECISION` (PostgreSQL's `REAL` has only
about 7 digits).

Other values (vectors, maps, objects) are a `TypeError`. A row is a map
from column names to values, in the order of the columns; columns with the
same name (from a join) keep the last value, so name them with `AS`.

| Function or member function | Description |
|---|---|
| `db::open(url)` | A `db::Conn`: `"sqlite:path"` opens the file `path` (relative to the current directory; any characters, also `?`, `#` and `%`), creating it if needed; `"sqlite::memory:"` is a database in memory, gone when closed. `"postgres://user:password@host:5432/database?sslmode=disable"` (also `postgresql://`; the options are PostgreSQL's) and `"mysql://user:password@host:3306/database"` (the port defaults to 3306; options such as `?tls=true` are the MySQL driver's) connect to a server; such a URL needs a host. A database that cannot be opened or reached is a `DBError` whose message has the URL (without the password) and the reason (`db::open: postgres://u:***@db.example/shop: connection refused`, `host x not found`); another URL a `ValueError`, also one with options the driver does not take (`sslmode=bogus`) and one with a `@` in the user name or password (write it as `%40`), as is a kind of database this `kukel` was built without ("this kukel was built without PostgreSQL (build tag nopostgres)"; `kukel version` lists the ones it has). A `kukel` built without any of the three has no module `db`: `use db;` says which build tags left it out. In SQLite, foreign keys are enforced and a database another program is writing is waited for up to 5 seconds |
| `conn->exec(sql, args...)` | Runs a statement that gives no rows (`CREATE`, `INSERT`, `UPDATE`, `DELETE`, ...) and gives the number of rows it touched (for an `UPDATE`, the rows its `WHERE` found, also in MySQL; 0 for a statement that changes no rows, such as `CREATE`) |
| `conn->lastid()` | The id the database chose for the row the last `INSERT` (or `REPLACE`) added (SQLite's `last_insert_rowid`, MySQL's `LAST_INSERT_ID`), or `nil`: after an `exec` of another statement (one that does not start with `INSERT` or `REPLACE`: so also an `INSERT` after a `WITH`) or of one that added no row (`INSERT OR IGNORE` of a row already there, `ON CONFLICT DO NOTHING`, `INSERT ... SELECT` that selects nothing), after the object calls `migrate`, `delete(p)` and `query->delete()` (they run statements of their own), and in MySQL for a row whose id the statement gave (`query`, `first` and `cursor`, `get`, `find` and `count`, and an `exec` that fails, leave it as it was). PostgreSQL has none for `exec` (write `INSERT ... RETURNING id` and use `first`); after `save` it is the id of the saved row, new or not, on every database |
| `conn->query(sql, args...)` | All rows of a query, as a vector of maps |
| `conn->first(sql, args...)` | The first row of a query, or `nil` |
| `conn->cursor(sql, args...)` | A `db::Cursor` for results too large to hold at once: `cursor->next()` gives the next row, or `nil` at the end (which closes it); `cursor->close()` stops early, and `cursor->closed()` tells whether it is closed. While a cursor is open, the other calls that run SQL (`exec`, `query`, `first`, `cursor`, `begin`, `commit`, `rollback`, and those of objects: `migrate`, `save`, `delete`, `get`, `find`, `load`, and the member functions of `db::Query`, also those that only refine it) are a `ValueError`; `lastid`, `intransaction` and `close` still work |
| `conn->begin()` | Starts a transaction: the statements until `commit()` or `rollback()` count together, or not at all. Transactions do not nest (`ValueError`) |
| `conn->commit()`, `conn->rollback()` | Ends the transaction, keeping or undoing its changes (`ValueError` without one) |
| `conn->intransaction()` | Whether a transaction is open |
| `conn->close()` | Closes the connection; an open transaction is rolled back. Closing again has no effect; any other call afterwards is a `ValueError`, except `closed()` |
| `conn->closed()` | Whether the connection is closed (also while a cursor is open) |

Errors the database reports are a `DBError` with its message, after the
member function's name and with SQLite's error code
(`"db::Conn::exec: constraint failed: UNIQUE constraint failed:
items.name (2067)"`). A connection prints as `db::Conn(sqlite:shop.db)`, or
`db::Conn(sqlite:shop.db, closed)` once closed (the password of a server's
URL shows as `***`, also in messages); a cursor as
`db::Cursor(open)` or `db::Cursor(done)`. A Go program that embeds Kukel can keep a program away
from databases (and the files they are stored in) by leaving `db` out of
`StandardModules`.

**Objects in tables.** A connection can also store objects of Kukel classes,
one row each, by convention: the data members are the columns of the
class's table, `id` is the primary key, and the table is the class's name
in lower case (`Item` is `item`; for `shop::Item` too). Two optional static
member functions change that: `table()` gives another table name, and
`columns()` gives columns their SQL types and constraints.

```
use db;

class Item {
    id, name, price, instock;
    static columns();
}
Item::columns() { return {"name": "TEXT NOT NULL", "price": "REAL", "instock": "BOOLEAN"}; }

main() {
    local conn = db::open("sqlite::memory:");
    conn->migrate(Item);                         // CREATE TABLE item (...)

    local pen = new Item();
    pen->name = "pen"; pen->price = 1.5; pen->instock = true;
    conn->save(pen);                             // INSERT; sets pen->id
    pen->price = 1.75;
    conn->save(pen);                             // UPDATE
    local ink = new Item();
    ink->name = "ink"; ink->price = 3.0; ink->instock = false;
    conn->save(ink);

    for (local i in conn->find(Item)->where("price < ?", 5)->orderby("name")->all())
        println(i->id, i->name, i->price, i->instock);   // 2 ink 3.0 false, then 1 pen 1.75 true
    println(conn->get(Item, 1)->name, conn->find(Item)->count());   // pen 2
    conn->delete(pen);
}
```

| Member function | Description |
|---|---|
| `conn->migrate(C)` | Creates the table of class `C` if there is none: `id` (unless `columns()` says otherwise, a key the database fills in: `INTEGER PRIMARY KEY` in SQLite, `BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY` in PostgreSQL, `BIGINT AUTO_INCREMENT PRIMARY KEY` in MySQL), then the other data members, in order, with the types `columns()` gives (none for the others). For an existing table it adds a column for each data member the table lacks. It never drops or changes a column |
| `conn->save(p)` | Stores object `p` and gives it. With `p->id` nil it inserts a row, and `p->id` gets the id the database chose; with an id it inserts the row, or updates the row that has that id (a row with another id that the new values clash with, on a `UNIQUE` column, is a `DBError`, never changed). Each data member must be a value the database can store (`TypeError`) |
| `conn->get(C, id)` | The object of class `C` stored with that id, or `nil` |
| `conn->delete(p)` | Deletes the row of `p`; gives whether there was one |
| `conn->find(C)` | A `db::Query` for all objects of class `C` |
| `conn->load(p, name, ...)` | Fills the relation members `name`, ... of `p` (below); gives `p` |
| `q->where(sql, args...)` | A query for the rows that also meet the SQL condition (`"price < ?"`); each `?` takes the next argument |
| `q->orderby(sql)` | A query that sorts by the SQL text (`"name"`, `"price DESC, name"`) |
| `q->limit(n [, offset])` | A query for at most `n` rows, after skipping `offset` |
| `q->include(name, ...)` | A query that also fills the relation members `name`, ... of the objects it finds: one more query per relation, not one per object |
| `q->all()`, `q->first()` | The objects the query finds (a vector), or the first one (or `nil`, also after `limit(0)`, which finds none) |
| `q->count()` | How many rows it finds |
| `q->delete()` | Deletes the rows `where` selects; gives how many (`orderby` and `limit` are a `ValueError` here) |

- An object loaded from a table is made without running the constructor;
  its data members get the columns of the same names (columns the class
  does not have are left out, data members the table lacks stay `nil`).
  Values come back as for `query`: a column declared `BOOLEAN` gives
  booleans (also from MySQL when `columns()` declares it `BOOLEAN`), one declared `DATETIME` (or `DATE`, `TIMESTAMP`) gives
  `time::Time` values; `columns()` is where to declare them.
- The types in `columns()` are the database's own (`DATETIME` exists in
  SQLite and MySQL, PostgreSQL has `TIMESTAMP`); types that all three
  understand (`VARCHAR(100)`, `DOUBLE PRECISION`, `BOOLEAN`, `TIMESTAMP`)
  keep a class usable with each.
- `"UUID"` in `columns()` is the ORM's own type, for a column that holds
  UUIDs (`uuid` module): PostgreSQL's `UUID`, else `CHAR(36)`. For `id`
  it makes the key a version 7 UUID that `save` makes when `p->id` is
  `nil`; an `id` that is not a UUID is then a `ValueError`. Keys that point
  to such objects are declared `"UUID"` too:

  ```
  class Doc { id, title, parentid, parent; static columns(); static relations(); }
  Doc::columns() { return {"id": "UUID", "title": "VARCHAR(40)", "parentid": "UUID"}; }
  Doc::relations() { return {"parent": db::one(Doc, "parentid")}; }
  ```

- The database fills in `id` when its type (from `columns()`, else the
  default above) is the default, or has `AUTO_INCREMENT`, `IDENTITY` or
  `SERIAL` in it, or in SQLite starts with `INTEGER PRIMARY KEY`. Any other
  type makes the program choose the keys (a name, a UUID it makes):
  `columns()` returning `{"id": "TEXT PRIMARY KEY"}` (in MySQL a
  `VARCHAR(n) PRIMARY KEY`); `p->id` must then be set before the first
  `save` (`ValueError` otherwise). The type replaces the whole definition
  of `id`, so it must say `PRIMARY KEY` itself.
- `save` and the others take part in a transaction (`begin`). An insert
  that is rolled back leaves `p->id` set: saving `p` again stores it under
  that id.
- `where` and `orderby` take SQL text: put values in with `?`, never by
  joining strings. A query is never changed by its member functions (they
  give new queries), so it can be refined in several ways and reused. Its
  values are checked by `where` (one the database cannot store is a
  `TypeError` there); that there is one for each `?` only when it runs
  (`all`, `first`, `count`, `delete`). `first` reads one row whatever the `limit`; `count` counts
  what `limit` and the offset leave. A negative limit or offset is a
  `ValueError`.
- `delete(p)` of an object without an id gives `false`; after a delete
  `p->id` keeps its value, so saving `p` again stores it under that id.
- A class needs a data member `id` (`ValueError`); `table()` must give a
  name of letters, digits and `_` (`ValueError`), and `columns()` a map from
  data members to strings. A `table()`, `columns()` or `relations()` that
  is not a static member function with a definition, or that gives a value
  of the wrong kind, is a `TypeError`. A class derived from another maps to
  its own table, with the inherited data members. Classes written in Go,
  and Kukel classes derived from them (their Go part cannot be stored),
  are a `TypeError`.
- On PostgreSQL and MySQL every column needs a type: `migrate` names a
  data member that `columns()` gives none (SQLite takes any value in an
  untyped column).
- Migrations that rename or drop columns are the program's own SQL.
- A query prints as its SQL: `db::Query("item" WHERE (price < ?) ORDER BY
  name)`.

**Relations.** A key that points to another object is an ordinary data
member that holds its id (`orderid`). A static member function
`relations()` names the data members that hold related objects instead:
`db::many(C, key)` for the objects of class `C` whose data member `key` holds
this object's id, `db::one(C, key)` for the object of class `C` whose id this
object's data member `key` holds.

```
use db;

class Order { id, customerid, day, customer, lines; static relations(); }
Order::relations() {
    return {"customer": db::one(Customer, "customerid"), "lines": db::many(Line, "orderid")};
}

class Customer { id, name; }
class Line { id, orderid, product, qty; }

main() {
    local conn = db::open("sqlite::memory:");

    for (local c in [Customer, Order, Line]) conn->migrate(c);

    local ann = new Customer();
    ann->name = "Ann";
    conn->save(ann);
    local o = new Order();
    o->customerid = ann->id; o->day = "mon";
    conn->save(o);

    for (local p in ["pen", "ink"]) {
        local l = new Line();
        l->orderid = o->id; l->product = p; l->qty = 1;
        conn->save(l);
    }

    local back = conn->get(Order, o->id);
    println(back->customer, back->lines);                 // nil nil: not loaded
    conn->load(back, "customer", "lines");
    println(back->customer->name, len(back->lines));      // Ann 2

    for (local x in conn->find(Order)->include("customer", "lines")->all())
        println(x->day, x->customer->name, x->lines[0]->product);   // mon Ann pen
}
```

- Relation members are not columns: `migrate` makes no column for them,
  and `save` stores the object only, not the related ones (save those
  one by one, with their key set). They hold what `load` or `include` put
  there: a vector of objects (`db::many`, in the order of their ids; empty
  when there are none), or an object or `nil` (`db::one`).
- Related objects are not loaded when they are read: a data member cannot
  load on first use, so `load` and `include` say when. `include` loads the
  related objects of all the results of a query with one query per
  relation (`WHERE orderid IN (...)`, in parts of 500 values).
- The key data member must exist on the right side and may not be `id`,
  and a relation member
  may not be named in `columns()` (`ValueError`); an unknown name for
  `load` or `include` is a `ValueError` too, and `db::many`/`db::one` of
  something that is not a class a `TypeError`. Keys match by value (`"1"`
  matches id 1), but give the key column the type of `id`: PostgreSQL
  does not compare text with a number. `load` and `include` fill one level:
  the lines of an order, not the products of its lines. A foreign key constraint
  is the program's choice, in `columns()`: `{"orderid": "BIGINT REFERENCES
  \"order\"(id) ON DELETE CASCADE"}`.
- `db::many` and `db::one` give a `db::Relation`, which prints as
  `db::many(Line, "orderid")`.

### events

An event loop: it calls Kukel functions when timers expire, when calls are
posted, or when other modules deliver events (windows of `tk`, sockets of
`zmq`, ...).

```
use events;

main() {
    local n = 0, tick;
    tick = events::every(0.5, fn() {
        n++;
        println("tick", n);

        if (n == 3) tick->cancel();
    });
    events::after(1.2, println, "after 1.2 s");
    events::run();              // tick 1, tick 2, after 1.2 s, tick 3
    println("done");
}
```

Handlers run one at a time, inside `events::run()`, so they never need to
lock anything. `run()` returns when nothing is left to do (no active timer,
nothing posted, no open source) or after `events::stop()`; it can be
called again later. Durations are seconds (ints or floats). A handler that
throws an exception it does not catch ends `run()` with that exception
(timers and posted calls stay for the next `run()`), unless
`events::onerror` has a handler for it. An error of a source goes the same
way (a `NetError` of a `zmq` socket whose connection is lost).

| Function | Description |
|---|---|
| `events::after(seconds, f, args...)` | Calls `f(args...)` once, that many seconds (0 or more) from now; gives an `events::Timer`. A delay beyond about 292 years (also `inf`) never comes: the timer keeps `run()` going until it is cancelled. `f` must be a function (`TypeError`) |
| `events::every(seconds, f, args...)` | Calls `f(args...)` every that many seconds (0.001 or more; beyond about 292 years, as for `after`), the first time after one interval; a timer that falls behind skips the missed calls. A delay out of range (or `nan`) is a `ValueError`, an `f` that is not a function a `TypeError` (also for `after`) |
| `events::post(f, args...)` | Calls `f(args...)` soon, after the calls already posted (a call posted by a handler runs in the next round, after the timers that are due); `f` must be a function |
| `events::run()` | Dispatches events as described above. Calling it from a handler is a `ValueError` |
| `events::stop()` | Makes `run()` return after the current handler (outside `run()` it has no effect) |
| `events::onerror(f)` | `f(e)` receives the exceptions handlers do not catch, and the loop goes on; an exception `f` itself does not catch ends `run()`. `events::onerror(nil)` lets them end `run()` again. An `f` that is not a function (or nil) is a `TypeError` |
| `events::pending()` | The number of active timers, posted calls and open sources: what keeps `run()` going |

Timers fire in the order of their deadlines (timers with the same deadline
in the order they were made). A late timer fires late, never early.

| Member function | Description |
|---|---|
| `t->cancel()` | The timer does not fire again (no effect on a timer that is done) |
| `t->active()` | Whether the timer will still fire |

A timer prints as `events::Timer(once)`, `events::Timer(every 0.5s)` (the interval as written),
`events::Timer(fired)` or `events::Timer(cancelled)`.

### fs

Directories and files of the local file system: open files to read and
write a piece at a time, the program's standard streams, directories,
entries and paths. Paths are strings, as for `readfile`; relative ones
start at the current directory.

```
use fs;

main() {
    for (local e in fs::readdir("."))
        println(e["name"], e["dir"] ? "(directory)" : e["size"]);
    local n = 0;
    fs::walk(".", fn(path, e) {
        if (fs::ext(path) == ".kkl") n++;
        return e["name"] != ".git";              // false: not into this directory
    });
    println(n, "Kukel files");
}
```

An **entry** is a map: `name` (the last part of the path), `size` (bytes),
`dir` and `link` (a symbolic link), `mode` (as `ls` shows it:
`"-rw-r--r--"`) and `time` (when it was last changed, a `time::Time`).

| Function | Description |
|---|---|
| `fs::readdir(path)` | The entries of a directory, by name. A symbolic link is an entry of its own (`link` true, and `dir` false), not what it points to |
| `fs::stat(path)` | The entry of a file or directory; for a symbolic link, of what it points to, with `link` true |
| `fs::exists(path)` | Whether there is a file or directory there (a link to nothing, or a loop of links, does not count). It is `false` when the path, or a directory on the way, is missing or a file (`"notes.txt/x"`); another failure, such as a directory that cannot be searched, is an `IOError` |
| `fs::walk(path, f)` | Calls `f(p, entry)` for `path` and everything below it, depth first and by name; `p` is `path` joined with the names. When `f` returns `false` for a directory, `walk` does not go into it. Symbolic links are reported, never followed (so a link cannot make a loop), also when `path` itself is one. A directory that cannot be read ends the walk with an `IOError`; an exception in `f` ends it too, and is passed on unchanged |
| `fs::mkdir(path)`, `fs::mkdirall(path)` | Makes a directory; `mkdirall` also the ones above it that are missing (and is fine when it exists) |
| `fs::remove(path)` | Removes a file or an empty directory |
| `fs::removeall(path)` | Removes a file, or a directory with everything in it |
| `fs::rename(from, to)` | Renames (moves) a file or directory |
| `fs::chmod(path, mode)` | Sets the permissions of a file or directory, written as `ls` shows them and as an entry's `mode` has them: nine letters (`"rw-r--r--"`) or the ten of an entry (`"-rw-r--r--"`, whose first letter is ignored), so `fs::chmod(b, fs::stat(a)["mode"])` gives `b` the permissions of `a`. `s`, `S`, `t` and `T` set setuid, setgid and sticky, as `ls` shows them. Another form is a `ValueError`. On Windows only the read-only flag changes: a file is read-only when no one has `w` |
| `fs::join(part, ...)`, `fs::base(path)`, `fs::dir(path)`, `fs::ext(path)` | Paths as text: the parts joined with the system's separator and cleaned (`fs::join("a", "..", "b")` is `"b"`); the last part; all but the last; the extension of the last (`".kkl"`, or `""`) |

**Open files.** `fs::open` gives an `fs::File`, to read a file a line or
a number of bytes at a time and to write it as the program goes, so that
large files and logs need not be read or written whole (as `readfile` and
`writefile` do):

```
use fs;

main() {
    with (local out = fs::open("notes.txt", "w")) {
        out->println("first line");
        out->write("second ", "line\n");
    }
    local n = 0, line;

    with (local f = fs::open("notes.txt")) {
        while ((line = f->readline()) != nil) {
            n++;
            println(n, line);
        }
    }

    with (local more = fs::open("notes.txt", "a")) more->println("third line");
    println(len(readfile("notes.txt")), "bytes");
}
```

```
$ kukel run notes.kkl
1 first line
2 second line
34 bytes
```

| Function | Description |
|---|---|
| `fs::open(path [, mode])` | Opens a file and gives an `fs::File`. The mode is as in C: `"r"` (the default) reads; `"w"` writes, emptying the file first (or making it); `"a"` appends, making the file if needed; `"x"` makes a new file, and is an `IOError` if there is one (`file exists`). With `+` (`"r+"`, `"w+"`, `"a+"`, `"x+"`) the file is also open the other way. Any other mode is a `ValueError`. New files get the mode `-rw-r--r--` less what the umask takes away, as for `writefile`. A directory is an `IOError` (`fs::open: src: is a directory`) |
| `fs::temp([pattern [, dir]])` | Makes a new, empty file and gives it as an `fs::File` open `"w+"`. Its name is `pattern` (by default `"kukel-*"`) with the last `*` replaced by random digits, or with them at the end when there is no `*` (`"report-*.txt"`); it is made in `dir`, or else in the system's directory for temporary files (`$TMPDIR`, `/tmp`). `f->name()` gives its path. It stays until the program removes it (`fs::remove(f->name())`), so another program can be given its path. A pattern with a path separator is a `ValueError` (the directory is the second argument). A temporary file in the directory of the file it will replace can then replace it at once with `fs::rename` (example below) |
| `fs::tempdir([pattern [, dir]])` | Makes a new, empty directory in the same way and gives its path; `fs::removeall` removes it |
| `fs::stdin()`, `fs::stdout()`, `fs::stderr()` | The program's standard input, output and error as `fs::File`s (the same object at each call). They share the program's own: the built-in `readline()` and the member function `readline()` of standard input read from the same input, what `println` and standard output's `write` write keeps its order, and standard error writes out standard output first, as `eprint` does. So a function can take any `fs::File` and be given a file or a standard stream |

An `fs::File`'s member functions; strings are bytes, so there is no text
or binary mode:

| Function | Description |
|---|---|
| `f->readline()` | The next line without its `\n` (or `\r\n`), or `nil` at the end, as `readline()` reads standard input; a last line without `\n` is a line too |
| `f->read([n])` | Up to `n` bytes (fewer only at the end of the file), or without `n` the rest of the file; `nil` when nothing is left. `n` of 0 gives `""`, a negative one is a `ValueError` |
| `f->write(s, ...)` | Writes the strings, with nothing between them, and gives `f` (so calls can be chained). Any other value is a `TypeError`: `print` and `println` turn values into text |
| `f->print([x, ...])`, `f->println([x, ...])` | Write as the built-ins `print` and `println` do |
| `f->seek(offset [, whence])` | Moves the position to `offset` bytes from `whence`: `"start"` (the default), `"current"` or `"end"`; gives the new position. A position before the start is an `IOError` |
| `f->tell()` | The position, in bytes from the start |
| `f->flush()` | Writes out what is buffered |
| `f->truncate([size])` | Cuts the file to `size` bytes, by default to the position; a larger `size` makes it longer, with zero bytes. Writes out what is buffered first; the position stays. A file not open for writing is a `ValueError`, a standard stream or a pipe an `IOError` |
| `f->lock([kind [, timeout]])` | Locks the whole file, `"exclusive"` (the default) or `"shared"`, waiting until no other lock is in the way, or at most `timeout` seconds (0: only try). Gives `true`, or `false` when the time ran out. A file that has a lock gets the other kind in its place. Below |
| `f->unlock()` | Gives up the lock (a file without one: nothing). `close` does it too |
| `f->close()` | Writes out what is buffered and closes the file; closing it again does nothing. A standard stream is only flushed, and stays open |
| `f->closed()`, `f->name()`, `f->mode()` | Whether it is closed; the path as given to `fs::open` (`"<stdin>"`, `"<stdout>"`, `"<stderr>"` for the standard streams); the mode (`"r"` for standard input, `"w"` for the others) |

- Reads and writes are buffered (64 KiB), so writing many small pieces is
  fast; `flush` writes them out when another program must see them at once
  (a log that someone follows). A file open both ways may switch between
  reading and writing at any time, without a `seek` in between (unlike C):
  what is written goes where the reading stopped (on a pipe or a
  terminal, `"/dev/tty"`, reading and writing are apart: what was read
  ahead stays to be read). In mode `"a"` and `"a+"` every write goes to the
  end.
- Close files when done with them: `with` does it (as in the example). A
  file the program did not close is written out and closed when the
  program ends (also by `exit()`, an exception it does not catch, or
  Ctrl-C in `kukel profile`; not when the process is killed); a worker's
  when the worker ends (a worker still running when the program ends is
  cut off, and what its open files hold is lost: a worker closes its files,
  or the program `wait()`s for it); in a Go program that embeds Kukel, at
  `Close`. If
  it cannot be written out then, `kukel` reports it (`kukel: log.txt: no
  space left on device`) and ends with exit code 1, and a worker's
  `wait()` throws a `WorkerError` that names it (`worker job failed:
  IOError: log.txt: ...`).
- After `close`, the other member functions are a `ValueError`
  (`fs::File::read: notes.txt is closed`), as are reading a file not open
  for reading and writing one not open for writing (`... is not open for
  writing`). What the system refuses is an `IOError` that names the
  function and the file: `fs::File::write: log.txt: no space left on
  device`. A standard stream has no position: `seek` and `tell` are an
  `IOError`, as is `seek` on a pipe.
- Reading standard input, a pipe, a terminal or a device waits, as
  `readline()` does (`kukel profile -cpu` leaves it out); reading and
  writing a file on disk is work, not waiting. Such a wait cannot be cut
  short.
- Locks are the system's: `flock` on Linux, macOS and the BSDs, where they
  are *advisory* (only programs that lock see them; reading and writing
  are not stopped), and `LockFileEx` on Windows, where they are
  *mandatory*: others cannot read or write a file locked exclusively, and
  no one, the holder included, can write a file with a shared lock.
  They hold between processes, and between two `fs::open`s of one file in
  a program and its workers; any number of shared locks, or one exclusive
  lock. Waiting for one is waiting (`kukel profile -cpu` leaves it out),
  and stops when the program is cancelled. A lock on a standard stream is
  an `IOError`. Over a network file system they hold only as far as its
  server supports them.

```
use fs;

main() {
    // A new version next to the old one, then swapped: a reader sees the
    // old file or the new one, never half of it.
    with (local tmp = fs::temp("config-*.json", ".")) {
        tmp->write("{\"version\": 2}\n");
        fs::chmod(tmp->name(), "rw-r--r--");
        fs::rename(tmp->name(), "config.json");
    }

    // One copy of the program at a time appends.
    with (local out = fs::open("app.log", "a")) {
        out->lock();
        out->println("started");
    }                                       // close unlocks
    print(readfile("config.json"));
    print(readfile("app.log"));
    println(fs::stat("config.json")["mode"]);
}
```

```
$ kukel run swap.kkl
{"version": 2}
started
-rw-r--r--
```

- An `fs::File` prints as `fs::File("notes.txt", "r")`,
  `fs::File("notes.txt", closed)` or `fs::File(<stdin>)`. There is no `new
  fs::File`: files come from `fs::open`, so no class derives from it.

What the file system refuses (a missing path, a directory that is not
empty for `remove`, no permission) is an `IOError` whose message names the
function and the path (`fs::readdir: nope: no such file or directory`;
`fs::rename: old -> new: ...`). A Go program that embeds Kukel keeps a program away
from files by leaving `fs` out of `StandardModules` (and the file
built-ins in `DenyBuiltins`).

### json

```
use json;

main() {
    local text = json::encode({"name": "Kukel", "tags": ["fun"], "ratio": 2.0});
    println(text);                          // {"name":"Kukel","tags":["fun"],"ratio":2.0}
    println(json::decode(text)["tags"][0]); // fun
}
```

| Function | Description |
|---|---|
| `json::encode(v [, indent])` | `v` as JSON text: `nil` is `null`, vectors are arrays, maps are objects in their order (number keys become their text, so keys `1` and `"1"` in one map give the same JSON key twice; a key `inf` or `nan` is a `ValueError`). Floats keep a `.0` so they decode as floats again. With `indent` (spaces and tabs, such as `"  "`; anything else is a `ValueError`) the text spreads over lines. A function, class or object is a `TypeError`; `inf`, `nan`, a string that is not UTF-8, a value that contains itself, or vectors and maps nested more than 10000 deep is a `ValueError` |
| `json::decode(s)` | The value JSON text `s` stands for: objects become maps (in the order of the text; a repeated key keeps its first place and its last value), arrays vectors, a number without fraction or exponent that fits in 64 bits an int, any other number a float (one too large for a float is `inf` or `-inf`). Invalid UTF-8 and lone surrogates (`"\ud800"`) become U+FFFD. Invalid JSON is a `ValueError` whose message names the byte (counted from 1) where the text goes wrong: `invalid JSON at byte 7: invalid character '}' where a value should be`, or `invalid JSON: unexpected end of text`. Arrays and objects nested more than 10000 deep are a `ValueError` too ("arrays and objects nested more than 10000 deep") |

### p9

The 9P file protocol of Plan 9 and Linux, in both its versions: 9P2000
and 9P2000.L. A client reads and writes the files of a 9P server; a server
serves a tree of directories and files that the program builds, whose
files hold a string or call a Kukel function, or a directory of the local
disk. It is the protocol that
Linux mounts with `mount -t 9p` and that Plan 9 and plan9port use for
their files.

```
use p9;

main() {
    local t = new p9::Tree(), visits = 0, notes = [];
    t->file("/motd", "hello\n");
    t->file("/stats/visits", fn() { visits++; return str(visits); });
    t->file("/notes", "", fn(data) { push(notes, data); });
    local s = p9::serve(t, "tcp://127.0.0.1:0");   // port 0: any free port

    local c = p9::dial(s->endpoint());     // could as well be another program

    for (local e in c->readdir("/"))
        println(e["name"], e["dir"] ? "(directory)" : e["size"]);   // motd 6, notes 0, stats (directory)
    print(c->read("/motd"));                // hello
    println(c->read("/stats/visits"), c->read("/stats/visits"));   // 1 2
    c->write("/notes", "remember the milk");
    println(c->version(), notes);          // 9P2000.L ["remember the milk"]
    c->close();
    s->close();
}
```

Addresses are `"tcp://host:port"` (the port is 564 when left out) and
`"unix:///path"` (a Unix socket file; `"unix://name"` is a path relative
to the current directory). For `p9::serve`, `"tcp://*:5640"` or
`"tcp://:5640"` listens on every interface (`"tcp://"` alone: on port 564),
and port 0 picks a free port.
Paths on the server start at the directory the client attached to; `"a/b"`
and `"/a/b"` are the same, and `..` stops at the top.

| Function or member function | Description |
|---|---|
| `p9::dial(address [, options])` | Connects to a 9P server and gives a `p9::Client`. The options are a map: `"version"` (`"9P2000.L"` or `"9P2000"`; without it 9P2000.L is tried first, then 9P2000), `"aname"` (the tree to attach to, for a server that has several; for a Kukel server, a directory of its tree), `"user"` (the user name sent with 9P2000; the user running the program by default) and `"timeout"` (seconds for the connection, the agreement on the version and the attach together, more than 0, 10 by default; beyond about 292 years: no limit; the requests after `dial` have none: a server that stops answering keeps the program waiting until it is stopped). A server that cannot be reached is a `NetError` (`p9::dial: tcp://127.0.0.1:1: connection refused`, `host x not found`), and so is one that takes the connection but gives no 9P answer in time ("no 9P answer within 10 seconds"); one that refuses the attach (an `aname` it does not have, or one that names a file: the top must be a directory) an `IOError`; a wrong address (also a port beyond 65535, or a socket path longer than the system takes, 107 bytes on Linux) or option a `ValueError` (`TypeError` for an option of the wrong type) |
| `c->readdir(path)` | The entries of a directory, by name. An **entry** is a map as for `fs`: `name`, `size`, `dir`, `link`, `mode` (`"-rw-r--r--"`) and `time` (a `time::Time`; 9P2000 has whole seconds only). `link` is true only for a symbolic link a server shows as one: a Kukel server follows links |
| `c->stat(path)`, `c->exists(path)` | The entry of a file or directory (the top is named `"/"`); whether there is one (`false` only for "not there", also for a path through a file: other failures are exceptions, as for `stat`) |
| `c->read(path)` | The content of a file, as a string |
| `c->write(path, s)` | Makes the content of a file `s`: the file is truncated first, or made if it is not there (when the server allows that) |
| `c->mkdir(path)`, `c->remove(path)` | Makes a directory; removes a file or an empty directory |
| `c->rename(from, to)` | Renames (moves) a file or directory. 9P2000 can only rename within a directory (another directory is an `IOError`); 9P2000.L can also move. Its messages name both paths (`p9::Client::rename: /a.txt -> /nodir/x: no such file or directory`) |
| `c->version()` | The version spoken: `"9P2000.L"` or `"9P2000"` |
| `c->close()` | Closes the connection (closing again has no effect). Using a closed client is a `ValueError`, except `version()`, `close()` and `closed()` |
| `c->closed()` | Whether the client is closed: by `close()`, or because the connection was lost |
| `new p9::Tree()` | An empty tree, for `p9::serve` |
| `t->file(path, content [, write])` | A file at `path`; the directories above it are made. `content` is a string, or a function that gives the content as a string; it is called each time a client opens the file. With `write`, a function, clients may write the file: when a client closes it, `write(data)` gets what the client wrote. A file already at `path` is replaced (clients see the new content the next time they open it). A path through a file, a file where a directory is (or the other way round), `"/"` as a file, and removing `"/"` or what is not there are a `ValueError` |
| `t->dir(path)` | A directory at `path`, and those above it |
| `t->remove(path)` | Takes the file or directory (with everything in it) out of the tree. A client that has it open goes on reading what it read |
| `p9::disk(dir [, options])` | The directory `dir` of the local disk, as a `p9::Disk` for `serve` and `mount`. It is read-only for clients unless the options say `{"readonly": false}`. A directory that is not there, or a file, is an `IOError`; another option a `ValueError` |
| `t->mount(path, disk)` | The disk directory appears at `path` in the tree (the directories above it are made; what was at `path` is replaced). `"/"` mounts it on a tree that is still empty. The tree's own `file`, `dir` and `remove` do not reach into a mounted directory (`ValueError`) |
| `p9::serve(tree, address)` | Serves the tree (or a `p9::Disk` on its own) at `address`, in both versions (each client picks one), and gives a `p9::Server`. An address that cannot be listened on (in use, no permission) is a `NetError` (`p9::serve: tcp://127.0.0.1:564: address already in use`); a wrong address a `ValueError`, as for `dial`; so is a tree or disk of a derived class whose constructor did not call `p9::Tree::Tree()` |
| `s->endpoint()` | The address served, with the port that port 0 picked (`"tcp://127.0.0.1:43127"`); for every interface, the address the system reports (`"tcp://[::]:5640"`) |
| `s->close()` | Stops the server: it no longer listens, and closes each connection once the request it is answering is answered (so a function of the tree may close its own server). Closing again has no effect |
| `s->closed()` | Whether the server is closed |

A file whose content is a string has that size; a file with a content
function has size 0 (it has no content until it is opened), as files that
a program makes up have on Plan 9 and in `/proc`. Clients cannot make,
rename or remove files of a Kukel tree, nor write files without a `write`
function: that is a "permission denied" `IOError` for them. Directories
show as `dr-xr-xr-x`, files as `-r--r--r--`, files with a `write`
function as `-rw-rw-rw-`; they belong to the user running the server.

The functions of a tree are Kukel functions and run one at a time on the
interpreter's goroutine: inside `events::run()`, which keeps going while a
server is open, or while a client of the same program waits for an answer
(so a program can read its own server, as above). A server program usually
ends with `events::run()`; a function of the tree can call `s->close()` to
end it. An exception in a content or `write` function fails the client's
request: a 9P2000 client gets the exception's message, a 9P2000.L client
an "input/output error"; the server goes on. A content function must give
a string.

What a server refuses (a missing file, no permission) is an `IOError`
whose message names the function and the path, with the server's message:
`p9::Client::read: /nope: file does not exist`. 9P2000.L carries only an
error number, so its messages are the system's (`no such file or
directory`). Writing, making, renaming or removing the top directory
(`"/"`) is an `IOError` in both versions. A connection that is lost (the
server went away) is a `NetError` (`p9::Client::read: /a: the connection is
lost: connection reset by peer`, or `... the server closed it`), and the
client is closed from then on.

In a **disk directory** clients see the files as they are on the disk,
with their sizes, permissions and times; they belong to the user running
the server (whatever their owner on the disk). Clients cannot get out of
the directory: `..` stops at its top, and a symbolic link that leads
outside it (or is absolute, or leads nowhere) is left out of listings and
cannot be opened; links that stay inside are followed. A read-only disk
(the default) refuses every change with "read-only file system" and shows
no write permissions. A writable one lets clients write, make, rename and
remove files and directories and change permissions and times, as the
user running the server may; owners and groups cannot change, and
symbolic links, hard links and device files cannot be made. A server
that shares a disk directory gives everyone who can reach it that
access: there is no authentication.

```
use p9;

main() {
    local t = new p9::Tree();
    t->file("/README", "the files are in /files\n");
    t->mount("/files", p9::disk("."));     // the current directory, read-only
    local s = p9::serve(t, "tcp://127.0.0.1:0");

    local c = p9::dial(s->endpoint());
    println(c->exists("/files/.."), c->stat("/files")["dir"]);   // true true

    try {
        c->write("/files/new.txt", "x");
    } catch (e) {
        println(e->message);               // p9::Client::write: /files/new.txt: read-only file system
    }

    c->close();
    s->close();
}
```

A client prints as `p9::Client(tcp://localhost:564, 9P2000.L)`, a tree as
`p9::Tree(3 files)`, a disk as `p9::Disk(/srv/data, read-only)` (or
without `read-only`), a server as `p9::Server(tcp://127.0.0.1:43127)`, each
with `closed` when closed. A tree has no notion of closed: it can be
served again after its server is closed. There is no authentication or encryption: use 9P
on a trusted network, or over an SSH tunnel or a VPN. A Go program that
embeds Kukel keeps a program off the network by leaving `p9` out of
`StandardModules`.

### re

Regular expressions with Go's RE2 syntax
([syntax](https://pkg.go.dev/regexp/syntax)). A match takes time linear in
the length of the text, whatever the pattern: there is no backtracking, and
so no back references (`\1`).

```
use re;

main() {
    local mail = re::compile("(\\w+)@(\\w+)\\.com", "i");
    println(mail->match("Mail ANN@Example.COM"));            // true
    println(mail->groups("Mail ann@example.com"));           // ["ann@example.com", "ann", "example"]
    println(mail->replace("a@b.com, c@d.com", "$2:$1"));     // b:a, d:c
    println(re::compile("\\d+")->findall("1 22 333"));      // ["1", "22", "333"]
}
```

In a Kukel string, a backslash is written `\\`: `"\\d+"` is the pattern
`\d+`.

| Function or member function | Description |
|---|---|
| `re::compile(pattern [, flags])`, `new re::Regexp(pattern [, flags])` | A `re::Regexp`. `flags` are letters: `i` ignores case, `m` makes `^` and `$` match at line ends, `s` lets `.` match `\n`, `U` makes repetition lazy. A pattern that is not valid, or another flag, is a `ValueError` |
| `re::match(pattern, s)` | Whether `pattern` matches somewhere in `s` (compiles it each time) |
| `re::quote(s)` | `s` with the characters that mean something in a pattern escaped: `re::quote("1.5*2")` is `1\.5\*2` |
| `r->match(s)` | Whether `r` matches somewhere in `s` (anchor it with `^...$` for all of `s`) |
| `r->find(s)` | The first match, or `nil` |
| `r->findall(s [, n])` | All the matches (a vector, empty for none), or the first `n` (0 gives none, a negative `n` all) |
| `r->groups(s)` | The first match and its groups: `[match, group1, ...]` (a group that took no part is `nil`), or `nil` for no match |
| `r->names()` | The names of the groups, `""` for one without (`(?P<year>\d+)` is named `year`). The whole match has no entry: `names()[k]` names `groups(s)[k + 1]` |
| `r->replace(s, repl)` | `s` with every match replaced: `repl` is a string in which `$1`, `${1}` and `${name}` stand for groups (`$$` for a `$`; a name goes on as far as letters, digits and `_` go, so write `${1}x`, not `$1x`, which names the group `1x`), or a function that gets the match and gives its replacement (a string) |
| `r->split(s [, n])` | The parts of `s` between the matches (at most `n`, the last one holding the rest; 0 gives none, a negative `n` all) |
| `r->pattern()` | The pattern, with the flags in front (`(?i)...`) |

A `re::Regexp` prints as `re::Regexp("(?i)a+")`. Strings are bytes: `.`
and the classes match UTF-8 characters, and the positions are in bytes.

### strings

```
use strings;

main() {
    local b = new strings::Builder();

    for (local i = 0; i < 3; i++) b->add(i, ";");
    println(b->str(), b->len());            // 0;1;2; 6
}
```

A builder collects text from many parts. (`s += x` in a loop is fast too:
it appends to `s` in place when nothing else was added after `s`.)

| Member function | Description |
|---|---|
| `new strings::Builder([s])` | An empty builder, or one starting with string `s` |
| `b->add(values...)` | Appends the values as `print` writes them (none: nothing); gives `b`, so calls can be chained |
| `b->str()` | The string built so far |
| `b->len()` | Its length in bytes |
| `b->reset()` | Empties the builder; gives `b`, as `add` does |

A builder prints as `strings::Builder(6 bytes)`.

### time

```
use time;

main() {
    local t = new time::Time(2026, 10, 14, 9, 30, 0);       // UTC
    println(t->format("%d.%m.%Y %H:%M"), t->weekday());    // 14.10.2026 09:30 3
    local xmas = time::Time::parse("2026-12-25", "%Y-%m-%d");
    println(floor(xmas->sub(t) / 86400), "days");          // 71 days
    println(t->inzone("Asia/Tokyo"));                       // time::Time(2026-10-14T18:30:00+09:00)
}
```

A `time::Time` is an instant together with a time zone. Time zones are
names from the IANA database (`"Europe/Amsterdam"`, `"UTC"`, `"Local"`
for the system's zone); `kukel` carries the database, so they work on
every system. Durations are seconds (ints or floats). A time prints as
`time::Time(2026-10-14T09:30:00Z)` (RFC 3339, with a fraction of a second
when it has one: `2026-10-14T09:30:01.5Z`).

| Function | Description |
|---|---|
| `time::now()` | The current time, in the local time zone (its `zone()` is `"Local"`, unless the environment variable `TZ` names a zone; when `TZ` is set but empty, the zone is `"UTC"`) |
| `time::sleep(seconds)` | Waits that long (`ValueError` if negative or `nan`; beyond about 292 years it waits as long as the program runs) |
| `new time::Time(year, month, day [, hour [, minute [, second [, zone]]]])` | That time in `zone` (default `"UTC"`, as is `""`; an unknown zone is a `ValueError`); `year` to `minute` are ints, `second` may have a fraction. Values out of range carry over (month 13 is January of the next year); a `second` of `nan` or `inf`, or a time beyond about 2.9e11 years from 1970, is a `ValueError` |
| `time::Time::parse(text [, format [, zone]])` | The time `text` stands for: without `format` in RFC 3339 (`"2026-10-14T09:30:00+02:00"`), else by `format` (below). A text without an offset is in `zone` (default `"UTC"`; `zone` needs a `format`, since RFC 3339 text has an offset; an offset in the text, `%z`, wins over `zone`). A text that does not match, or a date that does not exist (`"2026-02-30"`), is a `ValueError`, as is a year beyond about 2.9e11 years from 1970. A local time that a daylight-saving change skips or repeats is not an error: it gets the offset of one of the two sides (in Europe/Amsterdam, 02:30 on the day clocks jump from 02:00 to 03:00 becomes 03:30); which side is not specified |
| `time::Time::fromunix(seconds)` | The time that many seconds after 1970-01-01 00:00 UTC, in UTC (`nan`, `inf` and values beyond about 2.9e11 years are a `ValueError`, as for `add`) |

| Member function | Description |
|---|---|
| `t->year()`, `t->month()`, `t->day()` | The date: 2026, 1 to 12, 1 to 31 |
| `t->hour()`, `t->minute()`, `t->second()`, `t->nanosecond()` | The time of day |
| `t->weekday()` | 0 (Sunday) to 6 (Saturday) |
| `t->yearday()` | 1 to 366 |
| `t->zone()` | The zone's name (`"Europe/Amsterdam"`, or an offset such as `"+02:00"` for a time parsed with one; `"UTC"` for an offset of zero) |
| `t->offset()` | Seconds east of UTC (7200 for CEST) |
| `t->unix()` | Seconds since 1970-01-01 00:00 UTC, as a float |
| `t->format([format])` | The time as text: without `format` in RFC 3339, else by `format` (below) |
| `t->add(seconds)` | The time that many seconds later (earlier if negative); `nan`, `inf`, and a value or result beyond about 2.9e11 years from 1970, are a `ValueError` |
| `t->sub(u)` | The seconds from `u` to `t`, as a float (exact also over centuries) |
| `t->before(u)`, `t->after(u)`, `t->equal(u)` | Compare instants (`equal` ignores the zone; `==` compares objects) |
| `t->compare(u)` | -1, 0 or 1 |
| `t->utc()`, `t->inlocal()`, `t->inzone(zone)` | The same instant in UTC, the local zone, or `zone` |

**Formats** use `%` directives; any other character stands for itself:

| | | |
|---|---|---|
| `%Y` year (2026) | `%m` month (01-12) | `%d` day (01-31) |
| `%e` day, padded with a space | `%H` hour (00-23) | `%I` hour (01-12) |
| `%M` minute (00-59) | `%S` second (00-60) | `%f` microseconds (000000-999999) |
| `%p` AM or PM | `%y` year (00-99: the last two digits; 95 for the year -5) | `%j` day of the year (001-366) |
| `%b` month (Jan) | `%B` month (January) | `%a` weekday (Mon) |
| `%A` weekday (Monday) | `%z` offset (+0200) | `%Z` zone abbreviation (CEST; empty for a time parsed with an offset) |
| `%%` a `%` | | |

`parse` takes the same directives, except `%Z` (abbreviations are
ambiguous; use `%z`). Numbers may have fewer digits than shown (`%Y` may have a sign, and
reads at most 4 digits when another directive follows at once, as in
`%Y%m%d`, else all of them, so that a year after 9999 is read back), `%f` reads 1 to 9 digits as a fraction of
a second, `%z` also reads `Z`, `+02` and `+02:00` (hours up to 23, minutes
up to 59), `%y` reads 69-99 as 1969-1999 and 00-68 as 2000-2068, and names
are read in any case. `%I` (01-12) and `%p` come together; `%H` is the
hour 00-23. A weekday (`%a`, `%A`), and a day of the year (`%j`) next to a
month and day, must match the date. With a format, second 60 (a leap
second) is accepted and counts as the first second of the next minute
(RFC 3339 without a format does not allow it). Parts the format leaves out
are those of 1970-01-01 00:00:00. A text that does not match, or an
unknown directive, is a `ValueError`.

### tk

Desktop windows with Tk widgets, whose event handlers are Kukel functions
(closures, bound methods). Tk comes with kukel itself (Tcl/Tk 9, through
the Go package `modernc.org/tk9.0`, without a C compiler or Tcl/Tk on the
system); it is unpacked to the user's cache directory the first time.

The module adds about 19 MB to kukel, so it is only in a kukel built with
the build tag `tk` (`go build -tags tk ./cmd/kukel`, `make tk`, or the
`kukel-tk` archives of a release). In another kukel, `use tk;` is a
compile error that says so.

```
use tk;
use events;

main() {
    local w = new tk::Window("Counter"), n = 0;
    local label = new tk::Label(w, "clicked 0 times", {"width": 20});
    local button = new tk::Button(w, "Click me");
    button->onclick(fn() {
        n++;
        label->settext("clicked " + str(n) + " times");
    });
    label->pack({"padx": 10, "pady": 5});
    button->pack({"pady": 5});
    events::run();          // until the window is closed
}
```

**How it runs.** Handlers run in `events::run()` (see [events](#events)),
one at a time like all handlers, so timers, `zmq` sockets and workers mix
with the windows. Each open window keeps `run()` going; closing the first
window (by the user, or with `close()`) closes all windows and ends Tk for
the rest of the program. After that, making a window, a widget, a menu, a
`tk::Radiogroup` or a `tk::Image`, a dialog, the theme functions, and
changing a group are a `TkError` (`new tk::Image` reads its file first:
one it cannot read is still an `IOError`, and one that is not PNG or GIF a
`ValueError`); what only
reads what the program already has still works (`w->title()`,
`w->closed()`, `g->value()`, printing). An exception a handler does not
catch goes to the event loop like that of any handler (`events::onerror`),
also when the handler closed the last window. Tk runs on the main thread of
the process, so only the program `kukel` runs can use it, not a worker; a
Go program that embeds kukel calls `Run` in its main goroutine (on Linux
anything else is a `ValueError`; elsewhere it is not checked, and the
process may crash). Without a display (`$DISPLAY` on Linux), the first use
of Tk is a `TkError`; `tk::available()` tells beforehand.

The widgets are Tk's themed widgets (they look like the desktop's), except
`Text`, `Listbox` and `Canvas`, which Tk has only in its classic form. Once
its window is closed, a widget or menu cannot be used any more (a
`ValueError`). Member functions that change something give the object
itself, so calls chain: `new tk::Label(f, "Name:")->grid({"row": 0})`;
`close()` gives nil.

Strings for Tk (texts, titles, labels, lines, file types) may not hold NUL,
vertical tab or form feed (`"\x00"`, `"\x0b"`, `"\x0c"`): a `ValueError`.
Tcl splits words at the latter two, which would let such a string add its
own options to a Tk command.

**Options.** Widgets take options in a map, as the last argument of the
constructor or in `w->configure(options)`: `new tk::Label(w, "Name:",
{"anchor": "w", "width": 10})`. Each class has its own (in the tables
below); an unknown option, or a value of the wrong kind, is a `ValueError`
that says what is allowed (options that are not a map: a `TypeError`).
Sizes are pixels, except the `width` of the widgets that show text (labels,
buttons, entries, check and radio buttons, texts, list boxes, comboboxes), which counts
characters (0 to 1000), and the `height` of a text or list box, which
counts lines (1 to 1000). Colours are names (`"red"`, `"light blue"`) or
`"#rgb"`, `"#rrggbb"` (also `"#rrrrggggbbbb"`); an unknown name is a
`TkError`. The text of a label or button is not an option: `settext`.

Widgets with text (labels, buttons, entries, check and radio buttons,
texts, list boxes, comboboxes) and canvas text take a `font`: a map with
some of `family` (a font family such as `"DejaVu Sans"`, at most 200
bytes), `size` (points, 1 to
500), `bold`, `italic` and `underline` (`true` or `false`), as in
`{"family": "Helvetica", "size": 12, "bold": true}`; what is left out is
the platform's default. A family the system does not have gives Tk's
nearest one.

**Themes.** On Linux and the BSDs kukel uses Tk's `clam` theme (its
`default` looks plain); macOS and Windows keep their native themes.
`tk::themes()` lists the themes there are, `tk::theme()` gives the one in
use, and `tk::settheme(name)` changes it for all windows.

**Layout.** A widget appears once its parent places it with `pack` or
`grid`; each window or frame uses one of the two for its widgets (mixing
them is a `ValueError`). Frames hold widgets, and nest.

| Layout | Description |
|---|---|
| `w->pack([options])` | Places the widget after the ones packed before it. Options: `side` (`"top"`, the default, `"bottom"`, `"left"`, `"right"`), `fill` (`"none"`, `"x"`, `"y"`, `"both"`), `expand` (`true`: the widget gets a share of extra space), `padx`, `pady` |
| `w->grid([options])` | Places the widget in a cell of a grid: `row`, `column` (from 0), `rowspan`, `columnspan`, `sticky` (the sides of the cell it sticks to, some of `n`, `s`, `e`, `w`: `"w"`, `"ew"`, `"nsew"`), `padx`, `pady` |
| `c->rowconfigure(i, options)`, `c->columnconfigure(i, options)` | Of a window or frame: how row or column `i` of its grid grows. `weight` (a share of extra space; 0, the default: it does not grow), `minsize`, `uniform` (rows or columns with the same name get the same size) |

```
use tk;
use events;

main() {
    local w = new tk::Window("Login");
    local f = new tk::Frame(w, {"padding": 10});
    f->pack({"fill": "both", "expand": true});
    new tk::Label(f, "Name:")->grid({"row": 0, "column": 0, "sticky": "w"});
    local name = new tk::Entry(f, {"width": 20});
    name->grid({"row": 0, "column": 1, "sticky": "ew"});
    new tk::Label(f, "Password:")->grid({"row": 1, "column": 0, "sticky": "w"});
    local password = new tk::Entry(f, {"show": "*"});
    password->grid({"row": 1, "column": 1, "sticky": "ew"});
    f->columnconfigure(1, {"weight": 1});
    local ok = new tk::Button(f, "Log in");
    ok->grid({"row": 2, "column": 1, "sticky": "e", "pady": 5});
    ok->onclick(fn() {
        println("hello,", name->text());
        w->close();
    });
    name->focus();
    events::run();
}
```

**Events.** Besides the handlers of the widgets (`onclick`, `onenter`,
`onselect`, `onchange`), `w->bind(sequence, f)` calls `f(e)` for an event
of a window or any widget, with Tk's event names: `"<Key>"`,
`"<Key-Return>"`, `"<Control-Key-s>"`, `"<Button-1>"` (the left mouse
button), `"<Double-Button-1>"`, `"<ButtonRelease-1>"`, `"<B1-Motion>"`
(moving with the button down), `"<Motion>"`, `"<Enter>"`, `"<Leave>"`,
`"<FocusIn>"`, `"<Configure>"` (a new size), and virtual events such as
`"<<Copy>>"`. `e` is a map: `key` (the key's name, as `"Return"`, `"a"`)
and `char` (the character typed) for key events, nil for others; `x` and
`y` (the mouse, relative to the widget; on a canvas, canvas coordinates;
for `<Configure>`, the widget's position in its parent); `xroot` and
`yroot` (the mouse on the screen: where a pop-up menu goes); `delta` (the
mouse wheel; 0 for other events); `width` and `height` (for
`<Configure>`, else nil). Binding a sequence again replaces the handler,
and `bind(sequence, nil)` removes it. A sequence Tk does not know
(`"<Foo>"`) is a `ValueError`. A binding on a window also gets the
events of the widgets in it (a key binding there works wherever the focus
is in the window).

An event goes first to the binding of the widget, then to what the widget
does itself (a text or entry takes the typed key; Ctrl+K deletes to the end
of the line, and in a text Ctrl+O makes a new line), then to the binding of
its window. So in a `<Key>` handler of an entry, `text()` does not have the
key yet (`<KeyRelease>` comes after it). A handler that returns `false`
stops the event there: what comes after it does not run (as `onclose`
returning `false` keeps a window open). Any other result (nil, `true`, ...)
lets the event go on. A shortcut that must not reach a text or entry is
bound on that widget too, with a handler that returns `false`:
`text->bind("<Control-Key-o>", fn(e) { open(); return false; })`.

The handlers of the widgets run for what the user does, not for the
program's own changes: `setchecked`, `setvalue` and `setselected` do not
call them. `invoke` of a button or menu entry calls the handler also when
the button or entry is disabled (it is the program's own call). `onenter`
runs before a `bind("<Return>")` handler of the entry, and `onselect`
before a `bind("<<ListboxSelect>>")` handler of the list box, so such a
handler that returns `false` does not stop them. Note that
a button takes the keyboard focus when clicked; an entry gets it back with
`focus()`.

| Function | Description |
|---|---|
| `tk::available()` | Whether Tk can be used; it starts Tk. False without a display, after Tk ended, and off the main thread (in a worker) |
| `tk::themes()`, `tk::theme()`, `tk::settheme(name)` | The themes there are (a vector of names), the one in use, and choosing one (it gives nil; an unknown name is a `ValueError`); the fonts given to widgets stay |
| `tk::messagebox(text [, options])` | Shows a message and waits for a button; gives its name (`"ok"`, `"cancel"`, `"yes"`, `"no"`, `"retry"`, `"abort"`, `"ignore"`). Options: `title`, `detail` (a second, smaller text), `icon` (`"info"`, `"warning"`, `"error"`, `"question"`), `buttons` (`"ok"`, `"okcancel"`, `"yesno"`, `"yesnocancel"`, `"retrycancel"`, `"abortretryignore"`), `default` (the button Enter presses: one of those of `buttons`, else a `ValueError`), `parent` (an open window it shows over) |
| `tk::openfile([options])` | Lets the user choose a file to open; gives its path, or nil when cancelled. Options: `title`, `dir` (where it starts), `file`, `types` (`[["Kukel", ".kkl"], ["All files", "*"]]`: a name and patterns), `multiple` (`true`: a vector of paths), `parent` |
| `tk::savefile([options])` | Lets the user choose where to save; gives the path (the file need not exist; the dialog asks before an existing one is chosen), or nil. Options: `title`, `dir`, `file`, `types`, `ext` (added to a name without one, as `".txt"`), `parent` |
| `tk::choosedir([options])` | Lets the user choose a directory; gives its path, or nil. Options: `title`, `dir`, `mustexist`, `parent` |

The dialogs wait for the user: until the user answers, the program's other
windows take no input (they still redraw), and timers and other events of
the event loop wait. What the program printed before a dialog is written
out when it opens.

**Windows and frames.**

| Constructor or member function | Description |
|---|---|
| `new tk::Window([title])` | A window (the title is `"kukel"` when left out). The first one is Tk's main window: closing it closes all |
| `w->title()`, `w->settitle(s)` | The title |
| `w->onclose(f)` | `f()` is called when the user closes the window; when it returns `false`, the window stays open (to ask first). Nil: close at once (the default). When `f` throws, the window closes and the exception goes to the event loop |
| `w->close()` | Closes the window (and, the first window, all); a second `close()` does nothing |
| `w->closed()` | Whether the window is closed |
| `w->bind(sequence, f)`, `w->rowconfigure(i, options)`, `w->columnconfigure(i, options)` | See above |
| `new tk::Frame(parent [, options])` | A container for widgets in a window or frame. Options: `width`, `height`, `padding` (pixels inside the edge: one number, or `[x, y]`, or `[left, top, right, bottom]`), `relief` (`"flat"`, `"raised"`, `"sunken"`, `"groove"`, `"ridge"`, `"solid"`), `borderwidth` |
| `f->configure(options)`, `f->pack(...)`, `f->grid(...)`, `f->bind(...)`, `f->rowconfigure(...)`, `f->columnconfigure(...)` | See above |

A window prints as `tk::Window("title")` (and `tk::Window("title",
closed)`), a frame as `tk::Frame`.

**Widgets.** All widgets have `configure(options)`, `pack`, `grid` and
`bind`; the parent is a window or a frame.

| Constructor or member function | Description |
|---|---|
| `new tk::Label(parent, text [, options])` | A text. Options: `width` (characters), `font`, `image`, `compound` (below), `anchor` (where the text sits: `"w"`, `"center"`, `"ne"`, ...), `justify` (`"left"`, `"center"`, `"right"`: of several lines), `wraplength` (pixels before a line wraps), `padding`, `state` (`"normal"`, `"disabled"`) |
| `new tk::Button(parent, text [, options])` | A button. Options: `width`, `padding`, `state`, `font`, `image`, `compound` (below) |
| `b->onclick(f)` | `f()` is called when the button is clicked (nil: nothing) |
| `b->invoke()` | Calls the `onclick` handler as a click would, at once: its result and exception come back |
| `new tk::Entry(parent [, text] [, options])` | A field for a line of text. Options: `width`, `font`, `justify`, `show` (a character shown for each typed one, as `"*"` for passwords; `""` shows the text), `state` (also `"readonly"`) |
| `e->onenter(f)` | `f(text)` is called when Enter is pressed in the entry |
| `e->focus()` | The keys typed go to the entry |
| `x->text()`, `x->settext(s)` | The text of a label, button, entry, check button or radio button |
| `new tk::Checkbutton(parent, text [, options])` | A box that is checked or not. Options: `width`, `padding`, `state`, `font` |
| `c->checked()`, `c->setchecked(b)` | Whether it is checked |
| `c->onchange(f)` | `f(checked)` is called when the user checks or unchecks it |
| `new tk::Radiogroup([value])` | Not a widget: the radio buttons of which one is chosen (`value`: the one chosen at first) |
| `g->value()`, `g->setvalue(s)` | The value of the radio button chosen (nil: none); `setvalue(nil)` chooses none |
| `g->onchange(f)` | `f(value)` is called when the user chooses a radio button of the group |
| `new tk::Radiobutton(parent, text, group, value [, options])` | A radio button of `group`, chosen when the group's value is `value` (a string that is not `""`). Options: `width`, `padding`, `state`, `font` |
| `new tk::Text(parent [, text] [, options])` | Several lines of text, which the user can edit. Options: `width` (characters), `height` (lines), `font`, `wrap` (`"none"`, `"char"`, `"word"`), `state` (`"disabled"`: the user cannot change it; the program can), and in the constructor `scroll` (`"x"`, `"y"`, `"both"`: scrollbars; `"none"`, the default) |
| `t->text()`, `t->settext(s)` | The whole text |
| `t->append(s)` | Adds `s` at the end, and shows the end (for a log) |
| `new tk::Listbox(parent [, lines] [, options])` | Lines to choose from (a vector of strings). Options: `width`, `height` (lines), `font`, `selectmode` (`"browse"`, the default: one line; `"extended"`: several, with Shift and Control; `"single"`, `"multiple"`), `state`, and in the constructor `scroll` |
| `l->items()`, `l->setitems(v)` | The lines |
| `l->selected()`, `l->setselected(v)` | The indexes of the lines chosen, a vector (`[]`: none); an index that is not a line's is a `ValueError` |
| `l->onselect(f)` | `f(selected)` is called when the user changes the lines chosen |
| `new tk::Scale(parent [, options])` | A slider for a number. Options: `from` and `to` (the ends: 0 and 100 when left out; numbers from -1e9 to 1e9), `step` (the values are `from` plus a multiple of it, with as many decimals as `step` and `from` have: in steps of 0.1, 0.3 and not 0.30000000000000004; 0, the default: any value), `orient` (`"horizontal"`, the default, `"vertical"`), `length` (pixels), `state` (`"disabled"` greys the label and the value too); and in the constructor only, `label` (a text above the slider) and `showvalue` (`true`: the value beside it, as an int or with as many decimals as the step has, 2 without a step; as wide as the wider end, also when `configure` changes them). With either, the slider sits in a frame of its own with them, which `pack` and `grid` place |
| `s->value()`, `s->setvalue(x)` | The value (at first `from`): an int when a `step` is given and `from` and `step` are whole numbers, else a float; `setvalue` puts `x` (any number) within the range and on a step (the end of the range is a value only when the range holds whole steps: from 0 to 10 in steps of 3, the largest is 9) |
| `s->onchange(f)` | `f(value)` is called when the user moves the slider to another value |
| `new tk::Combobox(parent [, lines] [, options])` | A field with a drop-down list of lines to choose from (a vector of strings). Options: `width` (characters), `height` (lines in the list), `font`, `justify`, `state` (`"normal"`, the default: the text can also be typed; `"readonly"`: only chosen; `"disabled"`) |
| `c->text()`, `c->settext(s)`, `c->items()`, `c->setitems(v)` | The text in the field; the lines of the list |
| `c->onselect(f)` | `f(text)` is called when the user chooses a line |

A widget prints as its class and text: `tk::Label("Name:")`,
`tk::Entry("typed")` (also once its window is closed: the last text),
`tk::Checkbutton("Bold", true)`, `tk::Radiobutton("Red", "red")`,
`tk::Radiogroup("red")`, `tk::Listbox(3 lines)` (`1 line`), `tk::Text`,
`tk::Scale(7)`, `tk::Combobox("green")`.

A settings window, opened from the main one (one at a time); closing it
without OK asks first:

```
use tk;
use events;

main() {
    local w = new tk::Window("Main");
    local settings = {"sound": true, "size": "medium", "volume": 50};
    local summary = new tk::Label(w, str(settings), {"padding": 10});
    local open = new tk::Button(w, "Settings...");
    summary->pack();
    open->pack({"pady": 5});
    local s = nil;
    open->onclick(fn() {
        if (s != nil && !s->closed()) return;   // one at a time
        s = new tk::Window("Settings");
        local f = new tk::Frame(s, {"padding": 10});
        f->pack({"fill": "both", "expand": true});
        local sound = new tk::Checkbutton(f, "Sound")->setchecked(settings["sound"]);
        sound->grid({"row": 0, "column": 0, "sticky": "w"});
        local size = new tk::Radiogroup(settings["size"]), row = 1;

        for (local name in ["small", "medium", "large"]) {
            new tk::Radiobutton(f, name, size, name)->grid({"row": row, "column": 0, "sticky": "w"});
            row = row + 1;
        }
        local volume = new tk::Scale(f, {"from": 0, "to": 100, "step": 5, "length": 200, "label": "Volume", "showvalue": true});
        volume->setvalue(settings["volume"])->grid({"row": 4, "column": 0, "sticky": "ew"});
        local ok = new tk::Button(f, "OK");
        ok->grid({"row": 5, "column": 0, "sticky": "e", "pady": 5});
        ok->onclick(fn() {
            settings = {"sound": sound->checked(), "size": size->value(), "volume": volume->value()};
            summary->settext(str(settings));
            s->close();
        });
        s->onclose(fn() {
            return tk::messagebox("Close without saving?", {"buttons": "yesno", "icon": "question", "parent": s}) == "yes";
        });
    });
    events::run();
}
```

**Menus.**

| Constructor or member function | Description |
|---|---|
| `new tk::Menu(window [, options])` | The menu bar of the window (one per window); with the option `{"popup": true}`, a pop-up menu (any number), shown by `popup` |
| `m->submenu(label)` | A `tk::Menu` that opens from this one (in a menu bar: a menu of it) |
| `m->item(label, f [, options])` | An entry that calls `f()`. Option: `accelerator`, a key shown next to it (`"Ctrl+S"`); the key itself needs a `bind` on the window |
| `m->check(label, f [, checked [, options]])` | An entry that is checked or not (`checked`: at first; `false` when left out); `f(checked)` is called when the user changes it (nil: nothing) |
| `m->checked(label)`, `m->setchecked(label, b)` | Whether a check entry is checked (`setchecked` calls no handler) |
| `m->radio(label, group, value [, options])` | An entry of a `tk::Radiogroup`, chosen when the group's value is `value`; a group may hold radio buttons and entries together. The group's `onchange` handler is called when the user chooses it |
| `m->separator()` | A line between entries |
| `m->enable(label, on)` | Whether the entry can be chosen |
| `m->invoke(label)` | Does what choosing the entry does, at once (for a key binding): an item calls its handler, a check entry is toggled and calls its handler, a radio entry is chosen and calls its group's; the handler's result comes back |
| `m->popup(x, y)` | Shows a pop-up menu with its corner at `x, y` on the screen (ints, from -10000 to 100000): `w->bind("<Button-3>", fn(e) { m->popup(e["xroot"], e["yroot"]); })` |

Entries are named by their labels, which are unique in a menu (another
entry with the same label, or a label `""`, is a `ValueError`; so is
`invoke` or `enable` of a label the menu does not have, `invoke` of a
submenu, and `checked` of an entry that is not a check entry). `invoke` of
a radio entry calls the group's `onchange` also when that entry was
chosen already, as clicking it does. A menu bar prints as `tk::Menu`, a
menu in it as `tk::Menu("File")`, a pop-up menu as `tk::Menu(popup)`.

```
use tk;
use events;

main() {
    local w = new tk::Window("Editor");
    local text = new tk::Text(w, {"width": 60, "height": 20, "scroll": "y"});
    text->pack({"fill": "both", "expand": true});
    local file = new tk::Menu(w)->submenu("File");
    file->item("Save as...", fn() {
        local path = tk::savefile({"ext": ".txt", "parent": w});

        if (path != nil) writefile(path, text->text());
    }, {"accelerator": "Ctrl+S"});
    file->separator();
    file->item("Quit", fn() { w->close(); });
    w->bind("<Control-Key-s>", fn(e) { file->invoke("Save as..."); });
    events::run();
}
```

**Images.**

| Constructor or member function | Description |
|---|---|
| `new tk::Image(path)` | The picture in a PNG or GIF file (a regular file, at most 64 MB). The file is read by kukel: an `IOError` when it cannot be (or is not a regular file, such as a pipe or a device), a `ValueError` when it is larger than 64 MB or not PNG or GIF (or Tk cannot read it) |
| `img->width()`, `img->height()` | Its size in pixels |
| `img->free()` | Frees the picture in Tk (a program that makes many images frees those it no longer shows). Labels, buttons and canvas items that show it go blank (a canvas item stays, without a picture); giving it to a widget or canvas afterwards is a `ValueError`; `width()` and `height()` still give its size. Freeing again has no effect; after Tk ended (the picture went with Tk) it only marks the image freed (it prints as freed) |

Labels and buttons show an image with the option `image`, and the option
`compound` says where it goes next to the text: `"left"`, `"right"`,
`"top"`, `"bottom"`, `"center"`, `"image"` (the image only), `"text"`
(the text only), or `"none"` (the image instead of the text). On a canvas,
`c->image(x, y, img)`. An image prints as `tk::Image("path", 40x30)`, once
freed as `tk::Image("path", freed)`.

A label with a picture, and a canvas that shows one picture after the
other; each picture is freed once it is no longer shown (the files are in
kukel's `examples/testdata`: run it there):

```
use tk;
use events;

main() {
    local w = new tk::Window("Pictures");
    local files = ["testdata/sun.png", "testdata/moon.png"], shown = 0;
    new tk::Label(w, "Day and night", {"image": new tk::Image(files[0]), "compound": "left"})->pack();
    local c = new tk::Canvas(w, {"width": 100, "height": 100, "background": "white"});
    c->pack();
    local picture = new tk::Image(files[0]);
    local item = c->image(50, 50, picture);
    local next = new tk::Button(w, "Next");
    next->pack({"pady": 5});
    next->onclick(fn() {
        local old = picture;
        shown = (shown + 1) % len(files);
        picture = new tk::Image(files[shown]);
        c->configureitem(item, {"image": picture});
        old->free();
    });
    events::run();
}
```

**Canvas.** A surface to draw on. Each item drawn has an id (an int);
coordinates are numbers (pixels, from -1e6 to 1e6), points are vectors
`[x1, y1, x2, y2, ...]`.

| Constructor or member function | Description |
|---|---|
| `new tk::Canvas(parent [, options])` | Options: `width`, `height`, `background` (a colour), and in the constructor `scroll` (the scrollbars reach everything drawn) |
| `c->line(points [, options])` | A line through at least 2 points. Options: `color`, `width` (pixels, 0 to 1000), `arrow` (`"none"`, `"first"`, `"last"`, `"both"`), `smooth` (`true`: a curve) |
| `c->rect(x1, y1, x2, y2 [, options])`, `c->oval(x1, y1, x2, y2 [, options])` | A rectangle, or the oval in it. Options: `fill`, `outline` (colours; `""`: none), `width` (of the outline: pixels, 0 to 1000) |
| `c->arc(x1, y1, x2, y2 [, options])` | A part of the oval in that rectangle. Options as for `rect`, and `start` (degrees from -360 to 360, counterclockwise from 3 o'clock; 0 when left out), `extent` (degrees from -360 to 360; 90 when left out, negative: clockwise), `style` (`"pieslice"`, the default, `"chord"`, `"arc"`: the curve only) |
| `c->polygon(points [, options])` | A polygon of at least 3 points; options as for `rect` |
| `c->text(x, y, s [, options])` | A text at `x, y`. Options: `color`, `font`, `anchor` (which point of the text is at `x, y`: `"center"`, the default, `"nw"`, ...), `justify`, `width` (pixels before a line wraps) |
| `c->image(x, y, img [, options])` | A `tk::Image` at `x, y`. Option: `anchor` (as for `text`) |
| `c->coords(id)`, `c->setcoords(id, points)`, `c->setcoords(id, x1, y1 [, x2, y2])` | An item's coordinates (a vector); set as the item was made (a vector of points, or the numbers) |
| `c->move(item, dx, dy)` | Moves an item (`dx`, `dy`: numbers from -1e6 to 1e6, and the item must stay within the coordinates); `item` is an id, or a tag for all the items with it (none: nothing happens) |
| `c->configureitem(item, options)` | Changes the options of an item, or of the items with a tag; also `text`, a text item's text, and `image`, an image item's `tk::Image`. Each option must be one of every item's kind (else a `ValueError` that names the item); with a tag that no item has, nothing changes, but an option no kind of item has is still a `ValueError` |
| `c->raise(item)`, `c->lower(item)` | Puts an item (or the items with a tag, in their order) above, or below, all the others |
| `c->remove(item)`, `c->clear()` | Removes an item (or the items with a tag), or all |
| `c->tag(id, tag)`, `c->untag(id, tag)`, `c->tagged(tag)` | Gives an item a tag, or takes it away; the ids of the items with a tag (bottom to top). A tag is a name of letters, digits and `_` not starting with a digit; an item may have several |
| `c->binditem(item, sequence, f)` | Calls `f(e)` for events of `sequence` on an item, or on any item with a tag (nil: none); `e` also has `item`, the item's id. The item is the topmost one under the mouse, or while a mouse button is down, the one it went down on (so a drag follows the item). Item bindings are for mouse events; `"<Enter>"` and `"<Leave>"` are the item's: when the mouse moves onto it or off it, also into or out of the canvas, or when the items change under the mouse (one moved away, raised, made, removed); while a mouse button is down the item does not change. Item handlers run after the canvas's own bindings of the same event (`c->bind`), and neither hides the other; one that returns `false` stops the event, also for the item handlers after it (and so do a removed item, and a closed window). The item's `"<Enter>"` and `"<Leave>"` are the exception: they follow the mouse before the canvas's own bindings (of `"<Enter>"`, `"<Leave>"`, `"<Motion>"`), which cannot stop them; at a button's release they come after the release's handlers |
| `c->items()` | The ids, from the bottom to the top |
| `c->itemat(x, y)` | The id of the topmost item at that point (on a rectangle, oval, polygon or arc's slice, near a line, on a text or image), or nil |

An unknown colour name is a `TkError`, and changes nothing. Mouse events
come from `bind` on the canvas, or from `binditem` on items; dragging an
item:

```
use tk;
use events;

main() {
    local w = new tk::Window("Drag");
    local c = new tk::Canvas(w, {"width": 300, "height": 200, "background": "white"});
    c->pack();
    c->rect(20, 20, 100, 70, {"fill": "orange"});
    c->oval(150, 50, 250, 150, {"fill": "light blue"});
    local item = nil, last = nil;
    c->bind("<Button-1>", fn(e) {
        item = c->itemat(e["x"], e["y"]);
        last = e;

        if (item != nil) c->raise(item);
    });
    c->bind("<B1-Motion>", fn(e) {
        if (item != nil) c->move(item, e["x"] - last["x"], e["y"] - last["y"]);
        last = e;
    });
    events::run();
}
```

Item bindings with a tag apply to every item that has it, also to items
made later: a drawing program, where a double click adds a circle of the
size and colour chosen below the canvas, a drag moves one, and the right
button removes it:

```
use tk;
use events;

main() {
    local w = new tk::Window("Shapes");
    local c = new tk::Canvas(w, {"width": 300, "height": 200, "background": "white"});
    local colour = new tk::Combobox(w, ["orange", "light blue", "pink"], {"state": "readonly"});
    local size = new tk::Scale(w, {"from": 10, "to": 60, "step": 5});
    c->pack();
    colour->settext("orange")->pack({"fill": "x"});
    size->setvalue(30)->pack({"fill": "x"});
    local last = nil;
    c->bind("<Double-Button-1>", fn(e) {      // a new circle
        local r = size->value();
        c->tag(c->oval(e["x"] - r, e["y"] - r, e["x"] + r, e["y"] + r, {"fill": colour->text()}), "shape");
    });
    c->binditem("shape", "<Button-1>", fn(e) {
        c->raise(e["item"]);
        last = e;
    });
    c->binditem("shape", "<B1-Motion>", fn(e) {
        c->move(e["item"], e["x"] - last["x"], e["y"] - last["y"]);
        last = e;
    });
    c->binditem("shape", "<Button-3>", fn(e) { c->remove(e["item"]); });
    colour->onselect(fn(s) { c->configureitem("shape", {"fill": s}); });   // all of them
    events::run();
}
```

An id the canvas does not have is a `ValueError`. A canvas prints as
`tk::Canvas(2 items)` (`1 item`).

### uuid

```
use uuid;

main() {
    local id = uuid::v7();
    println(len(id), uuid::valid(id), uuid::valid("not a uuid"));   // 36 true false
    println(uuid::v5(uuid::DNS, "example.com"));   // cfbff0d1-9375-5685-968c-48ce8b15ae17, always
}
```

| Function | Description |
|---|---|
| `uuid::v7()` | A new version 7 UUID, as a string such as `"019a1b2c-3d4e-7f60-8a9b-0c1d2e3f4a5b"` (lower case). Its first 48 bits are the time in milliseconds, so later ones sort after earlier ones (in one program always, also within a millisecond; from different programs, not necessarily within a millisecond), and as database keys new rows land at the end of the index |
| `uuid::v4()` | A new version 4 UUID: entirely random |
| `uuid::v5(namespace, name)` | The version 5 UUID of string `name` in `namespace` (a UUID, in either case): from the SHA-1 hash of the two, as RFC 9562 defines it. The same namespace and name always give the same UUID, so ids can be derived from names, such as DNS names in `uuid::DNS`. A namespace that is not a UUID is a `ValueError` |
| `uuid::v3(namespace, name)` | The same with MD5 (version 3), for ids that other systems made that way; new ones should use version 5 |
| `uuid::valid(s)` | Whether string `s` is a UUID in that form: 32 hexadecimal digits (either case) in groups of 8, 4, 4, 4 and 12, joined by `-` |

The standard namespaces of RFC 9562 are constants: `uuid::DNS` (DNS names,
`"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`), `uuid::URL` (URLs), `uuid::OID`
(ISO object identifiers) and `uuid::X500` (X.500 names). Any UUID can be
a namespace, also one of the program's own (`uuid::v5(uuid::v5(uuid::DNS,
"example.com"), "user 17")`).

### worker

Workers are interpreters that run in parallel, each on its own thread, and
talk to the interpreter that started them only through messages. They
share nothing: each has its own globals, and a message is a copy of plain
data, so no locking is needed.

```
use worker;

square(id) {                                // runs in the worker
    for (local m = worker::receive(); m != nil; m = worker::receive())
        worker::send([id, m * m]);
    return "worker " + str(id) + " done";
}

main() {
    local w = worker::start(square, 1);
    w->send(7);
    println(w->receive());                  // [1, 49]
    w->stop();                              // its receive() now gives nil
    println(w->wait());                     // worker 1 done
}
```

A worker runs either a function of the same program (`start`: a new
interpreter with its own globals, so it also works with `kukel exec`), or
another program (`startfile`). It ends when its function (or `main`)
returns; `exit(code)` in a worker ends only that worker, and `wait()` gives
`code`. A worker's standard input is empty (`readline()` gives `nil`); it
writes to the same output as its parent. A worker that fails reports
nothing by itself: its error comes from `wait()`. When the main program
ends, workers still running end with it (they can no longer write output).

**Messages** (also the arguments of `start` and `startfile`, and what
`wait()` gives) are copies of `nil`, booleans, numbers, strings, and
vectors and maps of those; a part that occurs twice, or contains itself, is
copied once, so the copy has the same shape. Objects, functions and
classes cannot be sent (`TypeError`). `nil` cannot be sent with `send`
either: a `receive()` that gives `nil` means there are no more messages
(`nil` is fine inside a message, and as an argument of `start`). Messages
queue up (`send` never waits, unlike `zmq::Socket::send`) and arrive in the order they were sent. A
result that cannot be copied (an object) makes the worker fail: `wait()`
gives a `WorkerError` (`worker f failed: TypeError: its result, geo::Point,
cannot go back to the parent (...)`).

| Function | Description |
|---|---|
| `worker::start(f, args...)` | Runs `f(args...)` in a new interpreter of this program; `f` is a top-level or static member function (not a function literal, bound method or built-in: `TypeError`). Gives a `worker::Worker` |
| `worker::startfile(path, args...)` | Compiles the program in `path` (a `.kkl` file with the modules it uses, found as `kukel run` finds them; or a `.kkc` file) and runs its `main` with the vector of the arguments. `path` is relative to the current directory, as for `readfile`. A compile error (also a module not found) is a `CompileError`, a missing file an `IOError` (`worker::startfile: nope.kkl: no such file or directory`), a damaged `.kkc` file or a module compiled on its own (`kukel build -module`) a `ValueError`; so is `startfile` in a program run by a Go program that does not allow it to read program files ([embedding.md](embedding.md)) |
| `worker::send(v)` | In a worker: a copy of `v` goes to the parent |
| `worker::receive([timeout])` | In a worker: the next message from the parent; waits for it, at most `timeout` seconds if given (beyond about 292 years: no limit). `nil` after the parent's `stop()` (once the messages sent before are taken) or when the time is up. A negative or `nan` timeout is a `ValueError`, and so is `receive()` after `worker::onmessage` |
| `worker::onmessage(f)` | In a worker: the event loop (`events`) calls `f(message)` for each message from the parent, and `events::run()` keeps going until the parent's `stop()`. `f` must be a function (`TypeError`); a second `onmessage` is a `ValueError` |
| `worker::isworker()` | Whether this interpreter is a worker |

Outside a worker, `worker::send`, `receive` and `onmessage` are a
`ValueError`.

| Member function | Description |
|---|---|
| `w->send(v)` | A copy of `v` goes to the worker (`ValueError` after `stop()` or when the worker has ended) |
| `w->receive([timeout])` | The next message from the worker; `nil` when it has ended (and its messages are taken) or when the time is up (a timeout beyond about 292 years: no limit). A negative or `nan` timeout is a `ValueError` |
| `w->onmessage(f)` | The event loop calls `f(message)` for each message from the worker, and `events::run()` keeps going until the worker ends. Afterwards `receive()` is a `ValueError`, as is a second `onmessage`; `f` must be a function (`TypeError`) |
| `w->stop()` | No more messages to the worker: its `receive()` gives `nil` once it has taken those sent before |
| `w->wait()` | Waits until the worker ends and gives a copy of what its function returned (a new copy each time). A worker that failed gives a `WorkerError` with the worker's error in its message |
| `w->done()` | Whether the worker has ended |

A worker prints as `worker::Worker(square, running)` (or `done`, `failed`;
the file's path for `startfile`).

### zmq

ZeroMQ sockets, to talk with other programs (in any language that has a
ZeroMQ library) and other machines. (Not available: the STREAM, XPUB and
XSUB socket types, and security; see below.) `kukel` carries a ZeroMQ implementation
written in Go, so no C library is needed.

```
use zmq;
use worker;

server() {                                 // could as well be another program
    local rep = new zmq::Socket("REP");
    rep->bind("tcp://127.0.0.1:0");        // port 0: any free port
    worker::send(rep->endpoint());
    rep->send("hello, " + rep->receive());
    rep->close();
}

main() {
    local w = worker::start(server);
    local req = new zmq::Socket("REQ");
    req->connect(w->receive());            // the server's endpoint
    req->send("Kukel");
    local reply = req->receive(5);         // the reply, or nil after 5 seconds
    println(reply == nil ? "no answer" : reply);   // hello, Kukel
    req->close();
    w->wait();
}
```

| Type | Use |
|---|---|
| `"REQ"`, `"REP"` | request and reply: a REQ socket sends a request and receives its reply, in turn; a REP socket receives a request and sends its reply (the turns are not checked: keep to them) |
| `"PUB"`, `"SUB"` | publish and subscribe: a PUB socket sends to every SUB socket connected to it whose subscriptions match the start of the message |
| `"PUSH"`, `"PULL"` | a pipeline: each message pushed goes to one of the PULL sockets |
| `"DEALER"`, `"ROUTER"` | many peers without the strict turn-taking of REQ/REP: a ROUTER receives each message with the identity of the peer it came from as its first part (and the empty separator part of a REQ peer), and sends a message to the peer named in its first part; a DEALER sends and receives freely |
| `"PAIR"` | exactly two sockets, talking both ways |

A message is a string (bytes), or a vector of strings for a message of
several parts; `receive` gives a vector only for a message of more than one
part. Structured data travels as text, for example with `json::encode` and
`json::decode`. Endpoints are `"tcp://host:port"` (`"tcp://*:5555"` to bind
on every interface), `"ipc://path"` (a Unix socket file) or `"inproc://name"`
(between interpreters of one process, such as workers).

| Function or member function | Description |
|---|---|
| `new zmq::Socket(type [, identity])` | A socket of that type (`ValueError` for another type). The identity (1 to 255 bytes) is how a ROUTER the socket connects to names it; without one the ROUTER makes one up |
| `s->bind(endpoint)` | Listens on `endpoint`. Port 0 (`"tcp://127.0.0.1:0"`) picks a free port, which `endpoint()` then shows. A busy or forbidden address is a `NetError` |
| `s->connect(endpoint)` | Connects to a socket bound at `endpoint`. Unlike C ZeroMQ, it waits for the other side: it retries for about 2.5 seconds, then gives a `NetError` (`zmq::Socket::connect: tcp://localhost:5555: connection refused`), so bind first. A Go program that cancels the call meanwhile closes the socket |
| `s->send(message [, timeout])` | Sends a string, or a vector of strings as one message of several parts (`TypeError` for other values, `ValueError` for an empty vector). A PUSH, DEALER, PAIR or ROUTER socket waits until a peer takes the message (one is connected, and the connection takes more data): with `timeout`, at most that many seconds (as for `receive`). A message that cannot leave in time is a `NetError` (`no peer took the message within 5 seconds: it was not sent, and the socket is closed`): the socket is closed, so that the message never leaves later (C ZeroMQ keeps the socket). A PULL or SUB socket cannot send (`ValueError`); a REQ socket that has no peer gives a `NetError` at once, and a PUB socket drops a message no one subscribes to |
| `s->receive([timeout])` | The next message; waits for it, at most `timeout` seconds if given (beyond about 292 years: no limit), then gives `nil`. A message that arrives after the timeout is kept for the next `receive`. A negative or `nan` timeout is a `ValueError`, as is `receive` on a PUSH or PUB socket (they only send); a broken connection a `NetError`, and so is `receive` on a REQ socket that has not sent a request |
| `s->onmessage(f)` | The event loop (`events`) calls `f(message)` for each message, and `events::run()` keeps going until the socket is closed. Afterwards `receive()` is a `ValueError`, as is a second `onmessage`, and `onmessage` on a PUSH or PUB socket; `f` must be a function (`TypeError`). When a handler closes the socket, messages that arrived but were not handled yet are dropped. A REQ socket's handler may be set before the first request: it gets each reply. A read that fails (a lost connection) is a `NetError` of the event loop: `events::run()` throws it, or `events::onerror` gets it |
| `s->subscribe(prefix)`, `s->unsubscribe(prefix)` | A SUB socket receives the messages that start with one of its prefixes (`""`: all of them); other socket types give a `ValueError`. Subscriptions take a moment to reach the PUB socket: messages published right after `subscribe` may be missed |
| `s->type()`, `s->endpoint()` | The type, and the last endpoint bound to or connected to (`""` if none; after a bind, the address as bound, with the port that port 0 picked; for every interface, the address the system reports: `"tcp://[::]:5555"` after `bind("tcp://*:5555")`) |
| `zmq::poll(sockets [, timeout])` | Waits until one of the sockets (a vector) has a message and gives that socket, whose `receive()` then gives the message at once; `nil` after `timeout` seconds (beyond about 292 years: no limit). When several have one, the earliest in the vector wins. No message is lost: the others stay with their sockets. An empty vector, a closed socket, one with an `onmessage` handler, or a PUSH or PUB socket is a `ValueError` |
| `s->close()` | Closes the socket (closing again has no effect). If the socket sent a message less than 0.1 seconds before, `close` waits until 0.1 seconds have passed, so that the message still leaves. Using a closed socket is a `ValueError`, except `type()`, `endpoint()`, `close()` and `closed()` |
| `s->closed()` | Whether the socket is closed |

A socket prints as `zmq::Socket(REQ, tcp://localhost:5555)`, without an
endpoint as `zmq::Socket(REQ)`, and once closed as `zmq::Socket(REQ,
closed)`. An endpoint without `://` (`"localhost:5555"`), or with another
transport than `tcp`, `ipc` and `inproc`, is a `ValueError` for `bind` and
`connect`, and so is a `tcp://` endpoint without a port or with one beyond
65535, and an `ipc://` path longer than the system takes (107 bytes on
Linux); a host that cannot be found is a `NetError`. Close sockets when done; a program that ends closes
nothing for you. A Go program that embeds Kukel can keep the network away
from a program by leaving `zmq` out of `StandardModules`.

There is no security (ZeroMQ's PLAIN or CURVE): the messages are not
encrypted or authenticated. Use `zmq` on a trusted network, or carry it
over an encrypted channel such as an SSH tunnel or a VPN.

