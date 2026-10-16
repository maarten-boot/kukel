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
11. [Modules written in Go](#11-modules-written-in-go): db, events, fs, json, p9, re, strings, time, uuid, worker, zmq

---

## 1. Running programs

| Command | What it does |
|---|---|
| `kukel run a.kkl b.kkl [-- args]` | Compile the files as one program and run it; the arguments after `--` go to `main` |
| `kukel a.kkl [args]`, `kukel prog.kkc [args]` | The same as `kukel run a.kkl -- args` (`kukel exec` for a `.kkc` file): every argument after the file goes to `main`, also a `--`. So a script whose first line is `#!/usr/bin/env kukel` runs as a command (`./script a b`); a file whose name ends in `.kkl` or `.kkc`, or that exists, is taken for this, a command name first |
| `kukel check a.kkl ...` | Compile only and report the errors; prints nothing when the program is fine |
| `kukel build [-o prog.kkc] a.kkl ...` | Compile to a bytecode file (default name: the first file with `.kkc`) |
| `kukel build -module [-o m.kkc] m.kkl` | Compile module `m` on its own into `m.kkc`, which programs can use instead of `m.kkl` (see [Modules](#modules)). The file name must be the module's name (`util.kkl`, not `my-util.kkl`); a file with a NUL byte is refused. `-o` and `-module` (also `--module`) may come anywhere among the arguments |
| `kukel exec prog.kkc [--] [args]` | Run a bytecode file (a first `--` is skipped). A module compiled on its own is not a program: `exec` and `disasm` refuse it |
| `kukel disasm prog.kkc` or `kukel disasm a.kkl ...` | Show the bytecode in readable form |
| `kukel repl [a.kkl ...]` | Read declarations and statements and run them as they come (see [The REPL](#the-repl)); with files, their declarations come first (`main` is not called) |
| `kukel debug a.kkl ... [-- args]` | Run the program under the debugger: breakpoints, stepping, values (see [The debugger](#the-debugger)) |
| `kukel fmt [-w \| -l] a.kkl ...` | Format source files: indentation (4 spaces; `case` labels one level into the switch, their statements two) and the spaces between tokens (`{ }` becomes `{}`; a comment at the end of a line keeps its distance); line breaks and comments stay, at most one blank line in a row. A statement that starts a line with `if`, `while`, `do`, `for`, `switch`, `try` or `with` (also after a label: `outer: for`) gets a blank line before it (above the comments right above it), except as the first statement of a block, after a label on its own line or a `case`, as the statement of `else` or a header without braces, or as the `while` of a `do`. The output is checked to give the same tokens and comments as the input; a file it cannot format safely is refused with an "internal error".  Prints the result, or rewrites the files (`-w`), or lists the files whose formatting differs (`-l`; `-w` and `-l` together are a usage error). A file with a syntax error or a NUL byte is left alone, and the exit code is 1 |
| `kukel tokens a.kkl ...`, `kukel ast [-pos] a.kkl ...` | Show the tokens or the syntax tree of each file (for debugging; `-pos`, before the files, adds positions) |
| `kukel version` (also `-version`, `--version`) | Show the version (with the commit and the Go version), the bytecode format and the modules written in Go, with the databases `db` can open (`db (SQLite, PostgreSQL, MySQL)`): a `kukel` built with tags may lack some (see the README) |
| `kukel help` (also `-h`, `-help`, `--help`) | Show the commands (`kukel` without a command shows them too, with exit code 2) |

A compile reports several errors at once, in the order of the source: the
first syntax error of each file, and otherwise the first error of each
definition and of each function (at most 10 are shown, with a count of the
rest). Errors in the definitions (names, classes, constants) are reported
without compiling the function bodies, whose errors could only be their
consequences.

The exit code is `main`'s result when it returns an int, else 0. A compile
error or an uncaught exception gives exit code 1; `exit(code)` ends the
program with that code. A wrong command line (an unknown command, a missing
file name, `-o` without a name) gives exit code 2. The operating system
keeps only the low 8 bits of an exit code: `return 263` exits with 7,
`return -1` with 255.

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

- A statement without its final `;` gets one when it is one line, or the
  last line of the piece.
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
  (`KUKEL_HISTORY=` keeps none). Ctrl-C drops the line or the incomplete
  piece; Ctrl-D on an empty line ends the session. While a piece runs, the
  terminal is as usual (`readline()` reads a plain line, and Ctrl-C stops
  `kukel`).

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
| `break file:line`, `break line`, `break function` (`b`) | Stop at that line (of the current file), or at the first line of a function (`main`, `Shape::area`, `util::f`); a line without code moves the breakpoint to the next line with code |
| `delete [n]` (`d`), `breaks` | Remove breakpoint `n`, or all; list them |
| `continue` (`c`) | Run to the next breakpoint |
| `step` (`s`), `next` (`n`) | To the start of the next line: also into a call, or over calls |
| `finish` (`f`) | Until the current function has returned (to the next line of its caller) |
| `print path` (`p`) | A value: a variable (parameter, local, captured), `this`, a data member of `this`, or a global (`count`, `util::count`), followed by `->member` and `[index]` steps (`p v[2]`, `p m["key"]`, `p this->items`) |
| `locals` | The variables of the frame |
| `backtrace` (`bt`), `frame n` | The calls, innermost first; look at frame `n` (for `print`, `locals` and `list`) |
| `list [line]` (`l`) | The source around the current line, or around `line` |
| `quit` (`q`) | End the program, with exit code 0 (also the end of the input) |
| `help` (`h`) | The commands |

- It stops at the start of lines only (a line with several statements is
  one stop), also in global initializers and in event handlers.
- Looking never changes the program: `print` reads, it calls nothing.
  The locals of a function are its slots: a slot that two blocks share
  shows the name of the later one.
- Only the main interpreter stops; workers run on. The program reads its
  input (`readline`) from the same input as the debugger.
- When the program ends, `kukel debug` says so, with the exit code.

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

- `with (local a = e1, b = e2) statement` declares the locals (each needs a
  value) for the statement. When the statement is left (at its end, by
  `return`, `break`, `continue` or an exception), `x->close()` is called for
  each of them that is not `nil`, the last one first.
- Any value with a `close()` member function works: `db::Conn`,
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
zero gives `inf` or `nan`). Comparing an int with a float is exact.

**Strings**: `+` needs two strings: `"n = " + str(n)`. `s[i]` gives a byte
as an int; strings cannot be changed (`s[i] = x` is an error).

**Indexing**: `v[i]` with an int from 0 to `len(v) - 1` (no negative
indexes); assigning `v[len(v)]` is an error (use `push`). `m[k]` gives
`nil` for a missing key (use `haskey` to tell a stored nil apart). Map keys
are strings and numbers (not `nan`); `1` and `1.0` are the same key.

**Literals**: `[a, b, v..]` (`v..` inserts the elements of vector `v`),
`{key: value, ...}` — keys are expressions: `{name: 1}` uses the variable
`name`; write `{"name": 1}` for a string key. A `{` that starts a statement
is a block, never a map.

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
- In stack traces a literal is called `<function>/fn@<line>`.

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
  name (call `Base::Base(...)`).
- `p->x` reads or assigns a data member (all members are public). Static
  data members are reached as `C::x` only; a static member function can
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
  classes from it (`new Error([message [, kind]])`).
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
| `print(x, ...)` | Writes the values separated by single spaces (strings as they are); no values write nothing. Output that cannot be written is an `IOError` (also for the others below) |
| `println(x, ...)` | Like `print`, then a newline |
| `eprint(x, ...)` | Like `print`, to standard error |
| `eprintln(x, ...)` | Like `println`, to standard error |
| `readline()` | The next line of standard input without its line ending, or `nil` at the end |

`kukel` buffers standard output; `eprint`, `eprintln` and `readline` write
out what is buffered first, so a prompt appears before the input is read
and the order of output and errors stays as written.

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
| `float(x)` | A float from an int, float or string (`"2.5"`, `"1e3"`, `"inf"`, also Go's other forms such as `"0x1p3"`). A string beyond the float range is a `RangeError`; one too small gives 0.0 |
| `classof(obj)` | The class of an object |
| `instanceof(x, C)` | Whether `x` is an object of class `C` or of a class derived from it |

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
comment or `$$` text (`$1` written directly works too). PostgreSQL's JSON operators that
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
| `db::open(url)` | A `db::Conn`: `"sqlite:path"` opens the file `path` (relative to the current directory; any characters, also `?`, `#` and `%`), creating it if needed; `"sqlite::memory:"` is a database in memory, gone when closed. `"postgres://user:password@host:5432/database?sslmode=disable"` (also `postgresql://`; the options are PostgreSQL's) and `"mysql://user:password@host:3306/database"` (the port defaults to 3306; options such as `?tls=true` are the MySQL driver's) connect to a server; such a URL needs a host. A database that cannot be opened or reached is a `DBError`, another URL a `ValueError`, as is a kind of database this `kukel` was built without ("this kukel was built without PostgreSQL (build tag nopostgres)"; `kukel version` lists the ones it has). A `kukel` built without any of the three has no module `db`: `use db;` says which build tags left it out. In SQLite, foreign keys are enforced and a database another program is writing is waited for up to 5 seconds |
| `conn->exec(sql, args...)` | Runs a statement that gives no rows (`CREATE`, `INSERT`, `UPDATE`, `DELETE`, ...) and gives the number of rows it touched (for an `UPDATE`, the rows its `WHERE` found, also in MySQL; 0 for a statement that changes no rows, such as `CREATE`) |
| `conn->lastid()` | The id the database chose for the row the last `INSERT` (or `REPLACE`) added (SQLite's `last_insert_rowid`, MySQL's `LAST_INSERT_ID`), or `nil`: after an `exec` of another statement, and in MySQL for a row whose id the statement gave (`query`, `first` and `cursor`, and an `exec` that fails, leave it as it was). PostgreSQL has none for `exec` (write `INSERT ... RETURNING id` and use `first`); after `save` it is the new row's id on every database |
| `conn->query(sql, args...)` | All rows of a query, as a vector of maps |
| `conn->first(sql, args...)` | The first row of a query, or `nil` |
| `conn->cursor(sql, args...)` | A `db::Cursor` for results too large to hold at once: `cursor->next()` gives the next row, or `nil` at the end (which closes it); `cursor->close()` stops early. While a cursor is open, the other calls that run SQL (`exec`, `query`, `first`, `cursor`, `begin`, `commit`, `rollback`, and those of objects: `migrate`, `save`, `delete`, `get`, `find`, `load` and running a `db::Query`) are a `ValueError`; `lastid`, `intransaction` and `close` still work |
| `conn->begin()` | Starts a transaction: the statements until `commit()` or `rollback()` count together, or not at all. Transactions do not nest (`ValueError`) |
| `conn->commit()`, `conn->rollback()` | Ends the transaction, keeping or undoing its changes (`ValueError` without one) |
| `conn->intransaction()` | Whether a transaction is open |
| `conn->close()` | Closes the connection; an open transaction is rolled back. Closing again has no effect; any other call afterwards is a `ValueError` |

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
| `q->all()`, `q->first()` | The objects the query finds (a vector), or the first one (or `nil`) |
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
  values are checked when it runs (`all`, `first`, `count`, `delete`), not
  by `where`. `first` reads one row whatever the `limit`; `count` counts
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
- The key data member must exist on the right side, and a relation member
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
posted, or when other modules deliver events (later: windows, sockets).

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
`events::onerror` has a handler for it.

| Function | Description |
|---|---|
| `events::after(seconds, f, args...)` | Calls `f(args...)` once, that many seconds (0 to 1e9) from now; gives an `events::Timer`. `f` must be a function (`TypeError`) |
| `events::every(seconds, f, args...)` | Calls `f(args...)` every that many seconds (0.001 to 1e9), the first time after one interval; a timer that falls behind skips the missed calls. A delay out of range (or `nan`) is a `ValueError`, an `f` that is not a function a `TypeError` (also for `after`) |
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

A timer prints as `events::Timer(once)`, `events::Timer(every 0.5s)`,
`events::Timer(fired)` or `events::Timer(cancelled)`.

### fs

Directories and files of the local file system. Paths are strings, as for
`readfile`; relative ones start at the current directory.

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
| `fs::exists(path)` | Whether there is a file or directory there (a link to nothing does not count) |
| `fs::walk(path, f)` | Calls `f(p, entry)` for `path` and everything below it, depth first and by name; `p` is `path` joined with the names. When `f` returns `false` for a directory, `walk` does not go into it. Symbolic links are reported, never followed (so a link cannot make a loop), also when `path` itself is one. A directory that cannot be read ends the walk with an `IOError`, as does an exception in `f`, which goes on |
| `fs::mkdir(path)`, `fs::mkdirall(path)` | Makes a directory; `mkdirall` also the ones above it that are missing (and is fine when it exists) |
| `fs::remove(path)` | Removes a file or an empty directory |
| `fs::removeall(path)` | Removes a file, or a directory with everything in it |
| `fs::rename(from, to)` | Renames (moves) a file or directory |
| `fs::join(part, ...)`, `fs::base(path)`, `fs::dir(path)`, `fs::ext(path)` | Paths as text: the parts joined with the system's separator and cleaned (`fs::join("a", "..", "b")` is `"b"`); the last part; all but the last; the extension of the last (`".kkl"`, or `""`) |

What the file system refuses (a missing path, a directory that is not
empty for `remove`, no permission) is an `IOError` whose message names the
function and the path. A Go program that embeds Kukel keeps a program away
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
`"unix:///path"` (a Unix socket file). For `p9::serve`, `"tcp://*:5640"` or
`"tcp://:5640"` listens on every interface (`"tcp://"` alone: on port 564),
and port 0 picks a free port.
Paths on the server start at the directory the client attached to; `"a/b"`
and `"/a/b"` are the same, and `..` stops at the top.

| Function or member function | Description |
|---|---|
| `p9::dial(address [, options])` | Connects to a 9P server and gives a `p9::Client`. The options are a map: `"version"` (`"9P2000.L"` or `"9P2000"`; without it 9P2000.L is tried first, then 9P2000), `"aname"` (the tree to attach to, for a server that has several; for a Kukel server, a directory of its tree), `"user"` (the user name sent with 9P2000; the user running the program by default) and `"timeout"` (seconds for the connection, the agreement on the version and the attach together, 10 by default). A server that cannot be reached, or does not answer in time, is a `NetError`; one that refuses the attach (an `aname` it does not have) an `IOError`; a wrong address or option a `ValueError` (`TypeError` for an option of the wrong type) |
| `c->readdir(path)` | The entries of a directory, by name. An **entry** is a map as for `fs`: `name`, `size`, `dir`, `link`, `mode` (`"-rw-r--r--"`) and `time` (a `time::Time`; 9P2000 has whole seconds only). `link` is true only for a symbolic link a server shows as one: a Kukel server follows links |
| `c->stat(path)`, `c->exists(path)` | The entry of a file or directory (the top is named `"/"`); whether there is one (`false` only for "not there": other failures are exceptions, as for `stat`) |
| `c->read(path)` | The content of a file, as a string |
| `c->write(path, s)` | Makes the content of a file `s`: the file is truncated first, or made if it is not there (when the server allows that) |
| `c->mkdir(path)`, `c->remove(path)` | Makes a directory; removes a file or an empty directory |
| `c->rename(from, to)` | Renames (moves) a file or directory. 9P2000 can only rename within a directory (another directory is an `IOError`); 9P2000.L can also move |
| `c->version()` | The version spoken: `"9P2000.L"` or `"9P2000"` |
| `c->close()` | Closes the connection (closing again has no effect). Using a closed client is a `ValueError`, except `version()` and `close()` |
| `new p9::Tree()` | An empty tree, for `p9::serve` |
| `t->file(path, content [, write])` | A file at `path`; the directories above it are made. `content` is a string, or a function that gives the content as a string; it is called each time a client opens the file. With `write`, a function, clients may write the file: when a client closes it, `write(data)` gets what the client wrote. A file already at `path` is replaced (clients see the new content the next time they open it). A path through a file, a file where a directory is (or the other way round), `"/"` as a file, and removing `"/"` or what is not there are a `ValueError` |
| `t->dir(path)` | A directory at `path`, and those above it |
| `t->remove(path)` | Takes the file or directory (with everything in it) out of the tree. A client that has it open goes on reading what it read |
| `p9::disk(dir [, options])` | The directory `dir` of the local disk, as a `p9::Disk` for `serve` and `mount`. It is read-only for clients unless the options say `{"readonly": false}`. A directory that is not there, or a file, is an `IOError`; another option a `ValueError` |
| `t->mount(path, disk)` | The disk directory appears at `path` in the tree (the directories above it are made; what was at `path` is replaced). `"/"` mounts it on a tree that is still empty. The tree's own `file`, `dir` and `remove` do not reach into a mounted directory (`ValueError`) |
| `p9::serve(tree, address)` | Serves the tree (or a `p9::Disk` on its own) at `address`, in both versions (each client picks one), and gives a `p9::Server`. An address that cannot be listened on (in use, no permission) is a `NetError` |
| `s->endpoint()` | The address served, with the port that port 0 picked (`"tcp://127.0.0.1:43127"`); for every interface, the address the system reports (`"tcp://[::]:5640"`) |
| `s->close()` | Stops the server: it no longer listens, and closes each connection once the request it is answering is answered (so a function of the tree may close its own server). Closing again has no effect |

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
server went away) is a `NetError`, and the client is closed from then on.

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
with `closed` when closed. There is no authentication or encryption: use 9P
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
| `r->names()` | The names of the groups, `""` for one without (`(?P<year>\d+)` is named `year`) |
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
| `b->add(x, ...)` | Appends the values as `print` writes them; gives `b`, so calls can be chained |
| `b->str()` | The string built so far |
| `b->len()` | Its length in bytes |
| `b->reset()` | Empties the builder |

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
| `time::now()` | The current time, in the local time zone (its `zone()` is `"Local"`, unless the environment variable `TZ` names a zone) |
| `time::sleep(seconds)` | Waits that long (`ValueError` if negative or `nan`; beyond about 292 years it waits as long as the program runs) |
| `new time::Time(year, month, day [, hour [, minute [, second [, zone]]]])` | That time in `zone` (default `"UTC"`, as is `""`; an unknown zone is a `ValueError`); `year` to `minute` are ints, `second` may have a fraction. Values out of range carry over (month 13 is January of the next year); a `second` of `nan` or `inf`, or a time beyond about 2.9e11 years from 1970, is a `ValueError` |
| `time::Time::parse(text [, format [, zone]])` | The time `text` stands for: without `format` in RFC 3339 (`"2026-10-14T09:30:00+02:00"`), else by `format` (below). A text without an offset is in `zone` (default `"UTC"`; `zone` needs a `format`, since RFC 3339 text has an offset; an offset in the text, `%z`, wins over `zone`). A text that does not match, or a date that does not exist (`"2026-02-30"`), is a `ValueError`. A local time that a daylight-saving change skips or repeats is not an error: it gets the offset of one of the two sides (in Europe/Amsterdam, 02:30 on the day clocks jump from 02:00 to 03:00 becomes 03:30); which side is not specified |
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
| `%p` AM or PM | `%y` year (00-99) | `%j` day of the year (001-366) |
| `%b` month (Jan) | `%B` month (January) | `%a` weekday (Mon) |
| `%A` weekday (Monday) | `%z` offset (+0200) | `%Z` zone abbreviation (CEST; empty for a time parsed with an offset) |
| `%%` a `%` | | |

`parse` takes the same directives, except `%Z` (abbreviations are
ambiguous; use `%z`). Numbers may have fewer digits than shown (`%Y` at
most 4, and it may have a sign), `%f` reads 1 to 9 digits as a fraction of
a second, `%z` also reads `Z`, `+02` and `+02:00` (hours up to 23, minutes
up to 59), `%y` reads 69-99 as 1969-1999 and 00-68 as 2000-2068, and names
are read in any case. `%I` (01-12) and `%p` come together; `%H` is the
hour 00-23. A weekday (`%a`, `%A`), and a day of the year (`%j`) next to a
month and day, must match the date. With a format, second 60 (a leap
second) is accepted and counts as the first second of the next minute
(RFC 3339 without a format does not allow it). Parts the format leaves out
are those of 1970-01-01 00:00:00. A text that does not match, or an
unknown directive, is a `ValueError`.

### uuid

```
use uuid;

main() {
    local id = uuid::v7();
    println(len(id), uuid::valid(id), uuid::valid("not a uuid"));   // 36 true false
}
```

| Function | Description |
|---|---|
| `uuid::v7()` | A new version 7 UUID, as a string such as `"019a1b2c-3d4e-7f60-8a9b-0c1d2e3f4a5b"` (lower case). Its first 48 bits are the time in milliseconds, so later ones sort after earlier ones (in one program always, also within a millisecond; from different programs, not necessarily within a millisecond), and as database keys new rows land at the end of the index |
| `uuid::v4()` | A new version 4 UUID: entirely random |
| `uuid::valid(s)` | Whether string `s` is a UUID in that form: 32 hexadecimal digits (either case) in groups of 8, 4, 4, 4 and 12, joined by `-` |

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
queue up (`send` never waits) and arrive in the order they were sent. A
result that cannot be copied (an object) makes the worker fail: `wait()`
gives a `WorkerError`.

| Function | Description |
|---|---|
| `worker::start(f, args...)` | Runs `f(args...)` in a new interpreter of this program; `f` is a top-level or static member function (not a function literal, bound method or built-in: `TypeError`). Gives a `worker::Worker` |
| `worker::startfile(path, args...)` | Compiles the program in `path` (a `.kkl` file with the modules it uses, found as `kukel run` finds them; or a `.kkc` file) and runs its `main` with the vector of the arguments. `path` is relative to the current directory, as for `readfile`. A compile error (also a module not found) is a `CompileError`, a missing file an `IOError`, a damaged `.kkc` file or a module compiled on its own (`kukel build -module`) a `ValueError`; so is `startfile` in a program run by a Go program that does not allow it to read program files ([embedding.md](embedding.md)) |
| `worker::send(v)` | In a worker: a copy of `v` goes to the parent |
| `worker::receive([timeout])` | In a worker: the next message from the parent; waits for it, at most `timeout` seconds if given. `nil` after the parent's `stop()` (once the messages sent before are taken) or when the time is up. A negative or `nan` timeout is a `ValueError`, and so is `receive()` after `worker::onmessage` |
| `worker::onmessage(f)` | In a worker: the event loop (`events`) calls `f(message)` for each message from the parent, and `events::run()` keeps going until the parent's `stop()`. `f` must be a function (`TypeError`); a second `onmessage` is a `ValueError` |
| `worker::isworker()` | Whether this interpreter is a worker |

Outside a worker, `worker::send`, `receive` and `onmessage` are a
`ValueError`.

| Member function | Description |
|---|---|
| `w->send(v)` | A copy of `v` goes to the worker (`ValueError` after `stop()` or when the worker has ended) |
| `w->receive([timeout])` | The next message from the worker; `nil` when it has ended (and its messages are taken) or when the time is up. A negative or `nan` timeout is a `ValueError` |
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
| `s->connect(endpoint)` | Connects to a socket bound at `endpoint`. Unlike C ZeroMQ, it waits for the other side: it retries for about 3 seconds, then gives a `NetError`, so bind first |
| `s->send(message)` | Sends a string, or a vector of strings as one message of several parts (`TypeError` for other values, `ValueError` for an empty vector). A PULL or SUB socket cannot send (`ValueError`); a REQ socket that has no peer gives a `NetError` |
| `s->receive([timeout])` | The next message; waits for it, at most `timeout` seconds if given, then gives `nil`. A message that arrives after the timeout is kept for the next `receive`. A negative or `nan` timeout is a `ValueError`, as is `receive` on a PUSH or PUB socket (they only send); a broken connection a `NetError` |
| `s->onmessage(f)` | The event loop (`events`) calls `f(message)` for each message, and `events::run()` keeps going until the socket is closed. Afterwards `receive()` is a `ValueError`, as is a second `onmessage`, and `onmessage` on a PUSH or PUB socket; `f` must be a function (`TypeError`) |
| `s->subscribe(prefix)`, `s->unsubscribe(prefix)` | A SUB socket receives the messages that start with one of its prefixes (`""`: all of them); other socket types give a `ValueError`. Subscriptions take a moment to reach the PUB socket: messages published right after `subscribe` may be missed |
| `s->type()`, `s->endpoint()` | The type, and the last endpoint bound to or connected to (`""` if none; after a bind, the address as bound, with the port that port 0 picked) |
| `zmq::poll(sockets [, timeout])` | Waits until one of the sockets (a vector) has a message and gives that socket, whose `receive()` then gives the message at once; `nil` after `timeout` seconds. When several have one, the earliest in the vector wins. No message is lost: the others stay with their sockets. An empty vector, a closed socket, one with an `onmessage` handler, or a PUSH or PUB socket is a `ValueError` |
| `s->close()` | Closes the socket (closing again has no effect). If the socket sent a message less than 0.1 seconds before, `close` waits until 0.1 seconds have passed, so that the message still leaves. Using a closed socket is a `ValueError`, except `type()`, `endpoint()` and `close()` |

A socket prints as `zmq::Socket(REQ, tcp://localhost:5555)`, without an
endpoint as `zmq::Socket(REQ)`, and once closed as `zmq::Socket(REQ,
closed)`. An endpoint without `://` (`"localhost:5555"`), or with another
transport than `tcp`, `ipc` and `inproc`, is a `ValueError` for `bind` and
`connect`; a host that cannot be found is a `NetError`. Close sockets when done; a program that ends closes
nothing for you. A Go program that embeds Kukel can keep the network away
from a program by leaving `zmq` out of `StandardModules`.

There is no security (ZeroMQ's PLAIN or CURVE): the messages are not
encrypted or authenticated. Use `zmq` on a trusted network, or carry it
over an encrypted channel such as an SSH tunnel or a VPN.

