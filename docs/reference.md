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
11. [Modules written in Go](#11-modules-written-in-go): db, events, json, strings, time, worker, zmq

---

## 1. Running programs

| Command | What it does |
|---|---|
| `kukel run a.kkl b.kkl [-- args]` | Compile the files as one program and run it; the arguments after `--` go to `main` |
| `kukel check a.kkl ...` | Compile only and report the first error; prints nothing when the program is fine |
| `kukel build [-o prog.kkc] a.kkl ...` | Compile to a bytecode file (default name: the first file with `.kkc`) |
| `kukel exec prog.kkc [--] [args]` | Run a bytecode file (a first `--` is skipped) |
| `kukel disasm prog.kkc` or `kukel disasm a.kkl ...` | Show the bytecode in readable form |
| `kukel tokens a.kkl ...`, `kukel ast [-pos] a.kkl ...` | Show the tokens or the syntax tree of each file (for debugging; `-pos` adds positions) |
| `kukel version` | Show the version, the bytecode format and the modules written in Go |
| `kukel help` | Show the commands |

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

---

## 2. Lexical structure

**Comments**: `// to the end of the line`, `# to the end of the line` (so a
first line `#!/usr/bin/env ...` is allowed) and `/* ... */` (not nested; a
`/*` without `*/` is an error).

**Identifiers**: letters, digits and `_`, not starting with a digit, at most
255 characters; case matters.

**Nesting**: expressions and statements may nest at most 1000 levels deep
(parentheses, blocks, ...); deeper is a compile error.

**Keywords**: `break case catch class const continue default do else
false finally fn for global if in local new nil return static switch this
throw true try use while with`.

**Integers**: `42`, `1_000_000` (`_` only between digits), `0xFF_FF`,
`0b1010`. A leading zero does not mean octal. Decimal literals go up to
9223372036854775807 (and `-9223372036854775808`); hexadecimal and binary
literals may use all 64 bits (`0xFFFFFFFFFFFFFFFF` is -1): at most 16 hex
or 64 binary digits, leading zeros included.

**Floats**: `1.5`, `1.`, `.5`, `2e10`, `1.5E-3`. A value beyond the float
range is an error. A number literal is at most 255 characters long.

**Characters**: `'a'`, `'\n'`, `'\''` — one byte, whose value is an int (its
code): `'A'` is 65.

**Strings**: `"text"`, which may span lines. Escapes: `\n \t \r \0 \\ \" \'`
and `\x41` (one or two hex digits). Any other escape is an error.
Strings are sequences of bytes; UTF-8 text passes through unchanged. (A raw
NUL byte in the source ends its line, as if the rest were a comment.)

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
`<function main>`, `<function P::m>` or `<function main/fn@4>` (a function
literal on line 4), built-ins as `<builtin println>`, classes as
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
  a constant, function, class or built-in is a compile error.
- A constant's value must be computable when compiling: literals, constants
  defined earlier, and the unary, binary and `?:` operators on them (not
  calls). Every part is checked, also the part that
  `&&`, `||` or `?:` skips.
- Global initializers run in program order (files in command-line order)
  before `main`. A global can be used anywhere, also above its declaration;
  an initializer that runs earlier sees `nil`.
- The program starts at `main()` or `main(args)`, where `args` is a vector
  of the command-line arguments (strings).

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
  skipped, not taken as the current directory).
- A module is an ordinary source file with its own name space. Its
  top-level names are reached as `name::x`: functions (`geometry::hypot(3,
  4)`), constants (also in constant expressions), globals (which can be
  assigned), and classes (`new geometry::Circle(1)`, `class Ring :
  geometry::Circle`, `geometry::Circle::x` for a static member).
- `use` holds for the file that contains it. A module sees its own names,
  the built-ins, `Error`, and the modules it uses itself; it does not see the
  program's names. A program and its modules may define the same names.
- Top-level names that start with `_` are private: `geometry::_square` is a
  compile error outside the module. Members of a module's classes are not
  private (`p->_m()` works).
- Each module is loaded once, however many files use it, and its global
  initializers run before those of the files that use it. A module name
  stands for one file in the whole program. Modules may not use each other
  in a cycle, and a file given on the command line cannot also be a module.
- A module's name cannot be `Error`, nor a top-level name of the program or
  module whose file uses it. It may be a built-in's name: `time::now()` and
  `time()` do not mix up.
- A class of a module is printed and named in messages with its module:
  `geometry::Circle`. Its constructor is still `Circle`.
- `kukel build` stores the program and all its modules in one `.kkc` file;
  `kukel exec` does not need the module files.

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
statement; `continue name` continues that loop.
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
  `db::Cursor`, `zmq::Socket`, and objects of your own classes. A
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
  at most one rest parameter (`name..`). A default runs only when the
  argument is missing (an explicit `nil` stays `nil`) and may use the
  parameters before it.
- A function without `return value` returns `nil`.
- Variables a function needs are `local` declarations in its body. (Up to
  syntax version 3, names after a `;` in the parameter list, `f(a; t)`,
  declared them; that form is now a compile error that says so.)
- `f(a, v.., b)` passes the elements of vector `v` as separate arguments.
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

class Square : Rect { }     // no constructor: uses Rect's

main() {
    local s = new Square(3, 3);
    println(s->describe(), Shape::total(), s);   // rect 9 1 Square{name: "rect", w: 3, h: 3}
}
```

- A class declares its members; member functions are defined outside as
  `Class::name(params) { ... }` with the same number of parameters (default
  values go in the definition, never in the declaration). The base class
  must be defined before the classes derived from it.
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
  member.
- A derived class may redeclare a member function of its base (overriding
  it) if both are static or both are not; the parameters may differ.
  Redeclaring a data member is an error.
- `p->x` reads or assigns a data member (all members are public). Static
  data members are reached as `C::x` only; a static member function can
  also be called as `p->f(...)`.
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
| `NetError` | a network problem in `zmq`: an address that cannot be bound or reached, a message that cannot be sent or received |
| `HostError` | a function of a Go program that embeds Kukel failed ([embedding.md](embedding.md)) |
| `CancelledError` | the Go program that runs Kukel stopped it (a time limit); cannot be caught |
| `StepLimitError` | the step limit set by a Go program that runs Kukel was reached; cannot be caught |
| `InternalError` | a damaged `.kkc` file that the loader's checks did not catch (should not happen) |

---

## 10. Built-in functions

`isnil` and `substr` were removed in v0.4 (the compiler says so): write
`x == nil` and `slice(s, start, end)`.

Argument positions in error messages count from 1. Unless noted, a wrong
type is a `TypeError`, a wrong count an `ArgumentError`, a number or index
outside what the function takes a `RangeError`, and another value it cannot
work with an `ArgumentError`.

### Output and input

| Function | Description |
|---|---|
| `print(x, ...)` | Writes the values separated by single spaces (strings as they are) |
| `println(x, ...)` | Like `print`, then a newline |
| `eprint(x, ...)` | Like `print`, to standard error |
| `eprintln(x, ...)` | Like `println`, to standard error |
| `readline()` | The next line of standard input without its line ending, or `nil` at the end |
| `readfile(path)` | The whole file as a string (`IOError` on failure) |
| `writefile(path, s)` | Writes `s` to the file, replacing it |
| `appendfile(path, s)` | Appends `s` to the file, creating it if needed |

### Types and conversion

| Function | Description |
|---|---|
| `typeof(x)` | `"nil"`, `"bool"`, `"int"`, `"float"`, `"string"`, `"vector"`, `"map"`, `"function"`, `"class"` or `"object"` |
| `str(x)` | `x` as text, as `print` writes it |
| `int(x)` | An int: floats are truncated toward zero; strings in decimal, `0x` or `0b` form with an optional sign (`"42"`, `"-0x10"`, `"0b11"`), surrounding spaces allowed and every `_` ignored (`"1_000"`); `true` is 1, `false` 0 |
| `float(x)` | A float from an int, float or string (`"2.5"`, `"1e3"`, `"inf"`, also Go's other forms such as `"0x1p3"`) |
| `classof(obj)` | The class of an object |
| `instanceof(x, C)` | Whether `x` is an object of class `C` or of a class derived from it |

### Strings

| Function | Description |
|---|---|
| `len(x)` | Length of a string (in bytes), vector or map |
| `find(s, sub [, from])` | Index of the first `sub` at or after `from`, or -1 |
| `split(s, sep)` | Vector of the parts between the `sep`s; with `""`, the single bytes |
| `join(v [, sep])` | The elements joined with `sep`; non-strings as `str` gives them |
| `upper(s)`, `lower(s)` | ASCII letters in upper or lower case |
| `trim(s)` | `s` without white space at both ends |
| `replace(s, old, new)` | Every `old` replaced by `new` (`ArgumentError` if `old` is `""`) |
| `char(code)` | A one-byte string (code 0..255) |
| `ord(s [, i])` | The byte at `i` (default 0) as an int |

### Vectors and maps

| Function | Description |
|---|---|
| `push(v, x, ...)` | Appends the values; returns the new length |
| `pop(v)` | Removes and returns the last element |
| `insert(v, i, x)` | Inserts `x` at index `i` (0..`len(v)`) |
| `remove(v, i)` | Removes and returns the element at `i` |
| `slice(x, start [, end])` | A new vector (or string) of the elements (bytes) from `start` up to, not including, `end` (to the end without it): `slice("hello", 1, 3)` is `"el"` |
| `sort(v [, less])` | Sorts `v` in place (stable); `less(a, b)` says whether `a` goes before `b` (default `<`) |
| `keys(m)`, `values(m)` | Vectors of a map's keys or values, in insertion order |
| `haskey(m, k)` | Whether `k` is a key of `m` |
| `delete(m, k)` | Removes a key; returns whether it was there |

### Math

| Function | Description |
|---|---|
| `abs(x)` | Absolute value |
| `min(a, b, ...)`, `max(a, b, ...)` | Smallest or largest of numbers or of strings; also `min(vector)` (`ArgumentError` for no values) |
| `pow(x, y)` | `x` to the power `y`: an int for ints with `y >= 0` (wrapping around), else a float |
| `sqrt(x)`, `exp(x)`, `log(x)` | Square root, e<sup>x</sup>, natural logarithm (floats) |
| `sin(x)`, `cos(x)`, `tan(x)`, `atan2(y, x)` | Trigonometry (radians) |
| `floor(x)`, `ceil(x)`, `round(x)` | Rounded to an int (`round`: halves away from zero); `RangeError` if it does not fit |
| `random()` | A float from 0 up to (not including) 1 |
| `randint(a, b)` | An int from `a` to `b`, both included (`RangeError` if `a > b`) |
| `seed(n)` | Restarts the random numbers from seed `n` (the same seed gives the same numbers) |

### System

| Function | Description |
|---|---|
| `time()` | Seconds since 1970-01-01 UTC, as a float |
| `clock()` | Seconds since the program started, as a float |
| `getenv(name)` | The environment variable, or `nil` |
| `exit([code])` | Ends the program at once with `code` (default 0) |

---

## 11. Modules written in Go

These modules are part of `kukel` (`kukel version` lists them). A program
uses them like modules written in Kukel (see [Modules](#modules)): `use
json;`, then `json::encode(v)`. No file is involved, and a file `json.kkl`
does not replace them.

Their classes are *native classes*: written in Go, their objects hold Go
data and have member functions but no data members. `typeof` gives
`"object"`, `classof` and `instanceof` work as for other classes, and `==`
compares whether two values are the same object. A native class cannot
(yet) be used as a base class, and a native class without a constructor
has no `new`: its objects come from the module's functions.

### db

SQL databases. The first one is SQLite: a whole database in one file (or
in memory), nothing to install; `kukel` carries it.

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

Values always go into SQL as parameters: each `?` takes the next argument.
They are never glued into the SQL text, so text from a user cannot change
the statement (SQL injection).

| Kukel | SQL | back in Kukel |
|---|---|---|
| `nil` | NULL | `nil` |
| int, float | INTEGER, REAL | int, float (an int in a REAL column comes back as a float) |
| string | TEXT | string (bytes; also for BLOB columns) |
| bool | stored as 1 or 0 | `true`/`false` for a column declared `BOOLEAN` or `BOOL`, else 1 or 0 |
| `time::Time` | a timestamp | `time::Time` for a column declared `DATETIME`, `DATE` or `TIMESTAMP` (a value there that is not a date comes back as a string); store times in such columns, as a TEXT column keeps a text form only |

Other values (vectors, maps, objects) are a `TypeError`. A row is a map
from column names to values, in the order of the columns; columns with the
same name (from a join) keep the last value, so name them with `AS`.

| Function or member function | Description |
|---|---|
| `db::open(url)` | A `db::Conn`: `"sqlite:path"` opens the file `path` (relative to the current directory), creating it if needed; `"sqlite::memory:"` is a database in memory, gone when closed. A file that cannot be opened is a `DBError`, another URL a `ValueError`. Foreign keys are enforced, and a database another program is writing is waited for up to 5 seconds |
| `conn->exec(sql, args...)` | Runs a statement that gives no rows (`CREATE`, `INSERT`, `UPDATE`, `DELETE`, ...) and gives the number of rows it changed |
| `conn->lastid()` | The row id of the last row an `INSERT` added (SQLite's `last_insert_rowid`), or `nil` |
| `conn->query(sql, args...)` | All rows of a query, as a vector of maps |
| `conn->first(sql, args...)` | The first row of a query, or `nil` |
| `conn->cursor(sql, args...)` | A `db::Cursor` for results too large to hold at once: `cursor->next()` gives the next row, or `nil` at the end (which closes it); `cursor->close()` stops early. While a cursor is open, other calls on the connection are a `ValueError` |
| `conn->begin()` | Starts a transaction: the statements until `commit()` or `rollback()` count together, or not at all. Transactions do not nest (`ValueError`) |
| `conn->commit()`, `conn->rollback()` | Ends the transaction, keeping or undoing its changes (`ValueError` without one) |
| `conn->intransaction()` | Whether a transaction is open |
| `conn->close()` | Closes the connection; an open transaction is rolled back. Closing again has no effect; any other call afterwards is a `ValueError` |

Errors the database reports are a `DBError` with its message (`"UNIQUE
constraint failed: items.name"`). A connection prints as
`db::Conn(sqlite:shop.db)` (or `closed`), a cursor as `db::Cursor(open)` or
`db::Cursor(done)`. A Go program that embeds Kukel can keep a program away
from databases (and the files they are stored in) by leaving `db` out of
`StandardModules`.

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
| `json::encode(v [, indent])` | `v` as JSON text: `nil` is `null`, vectors are arrays, maps are objects in their order (number keys become their text, so keys `1` and `"1"` in one map give the same JSON key twice). Floats keep a `.0` so they decode as floats again. With `indent` (spaces and tabs, such as `"  "`; anything else is a `ValueError`) the text spreads over lines. A function, class or object is a `TypeError`; `inf`, `nan`, a string that is not UTF-8, a value that contains itself, or vectors and maps nested more than 10000 deep is a `ValueError` |
| `json::decode(s)` | The value JSON text `s` stands for: objects become maps (in the order of the text; a repeated key keeps its first place and its last value), arrays vectors, a number without fraction or exponent that fits in 64 bits an int, any other number a float (one too large for a float is `inf` or `-inf`). Invalid UTF-8 and lone surrogates (`"\ud800"`) become U+FFFD. Invalid JSON, or arrays and objects nested more than 10000 deep, is a `ValueError` |

### strings

```
use strings;

main() {
    local b = new strings::Builder();
    for (local i = 0; i < 3; i++) b->add(i, ";");
    println(b->str(), b->len());            // 0;1;2; 6
}
```

`s += x` copies `s` every time; a builder does not.

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
`time::Time(2026-10-14T09:30:00Z)` (RFC 3339).

| Function | Description |
|---|---|
| `time::now()` | The current time, in the local time zone |
| `time::sleep(seconds)` | Waits that long (`ValueError` if negative or `nan`; beyond about 292 years it waits as long as the program runs) |
| `new time::Time(year, month, day [, hour [, minute [, second [, zone]]]])` | That time in `zone` (default `"UTC"`); `second` may have a fraction. Values out of range carry over (month 13 is January of the next year); a `second` of `nan` or `inf`, or a time beyond about 2.9e11 years from 1970, is a `ValueError` |
| `time::Time::parse(text [, format [, zone]])` | The time `text` stands for: without `format` in RFC 3339 (`"2026-10-14T09:30:00+02:00"`), else by `format` (below). A text without an offset is in `zone` (default `"UTC"`). A text that does not match, or a date that does not exist (`"2026-02-30"`), is a `ValueError`. A local time that a daylight-saving change skips or repeats is not an error: it gets the offset of one of the two sides (02:30 on the day clocks jump from 02:00 to 03:00 becomes 03:30); which side is not specified |
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
| `t->utc()`, `t->local()`, `t->inzone(zone)` | The same instant in UTC, the local zone, or `zone` |

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
| `worker::startfile(path, args...)` | Compiles the program in `path` (a `.kkl` file with the modules it uses, found as `kukel run` finds them; or a `.kkc` file) and runs its `main` with the vector of the arguments. `path` is relative to the current directory, as for `readfile`. A compile error (also a module not found) is a `CompileError`, a missing file an `IOError`, a damaged `.kkc` file a `ValueError`; so is `startfile` in a program run by a Go program that does not allow it to read program files ([embedding.md](embedding.md)) |
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
| `"REQ"`, `"REP"` | request and reply: a REQ socket sends a request and receives its reply, in turn; a REP socket receives a request and sends its reply |
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
| `s->send(message)` | Sends a string, or a vector of strings as one message of several parts (`TypeError` for other values, `ValueError` for an empty vector) |
| `s->receive([timeout])` | The next message; waits for it, at most `timeout` seconds if given, then gives `nil`. A message that arrives after the timeout is kept for the next `receive`. A negative or `nan` timeout is a `ValueError`; a broken connection a `NetError` |
| `s->onmessage(f)` | The event loop (`events`) calls `f(message)` for each message, and `events::run()` keeps going until the socket is closed. Afterwards `receive()` is a `ValueError`, as is a second `onmessage`; `f` must be a function (`TypeError`) |
| `s->subscribe(prefix)`, `s->unsubscribe(prefix)` | A SUB socket receives the messages that start with one of its prefixes (`""`: all of them); other socket types give a `ValueError`. Subscriptions take a moment to reach the PUB socket: messages published right after `subscribe` may be missed |
| `s->type()`, `s->endpoint()` | The type, and the last endpoint bound to or connected to (`""` if none) |
| `zmq::poll(sockets [, timeout])` | Waits until one of the sockets (a vector) has a message and gives that socket, whose `receive()` then gives the message at once; `nil` after `timeout` seconds. When several have one, the earliest in the vector wins. No message is lost: the others stay with their sockets. A closed socket, or one with an `onmessage` handler, is a `ValueError` |
| `s->close()` | Closes the socket (closing again has no effect). If the socket sent a message less than 0.1 seconds before, `close` waits until 0.1 seconds have passed, so that the message still leaves. Using a closed socket is a `ValueError` |

A socket prints as `zmq::Socket(REQ, tcp://localhost:5555)` (or without an
endpoint, or `closed`). Close sockets when done; a program that ends closes
nothing for you. A Go program that embeds Kukel can keep the network away
from a program by leaving `zmq` out of `StandardModules`.

There is no security (ZeroMQ's PLAIN or CURVE): the messages are not
encrypted or authenticated. Use `zmq` on a trusted network, or carry it
over an encrypted channel such as an SSH tunnel or a VPN.

