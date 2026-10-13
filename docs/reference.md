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

---

## 1. Running programs

| Command | What it does |
|---|---|
| `kukel run a.kkl b.kkl [-- args]` | Compile the files as one program and run it; the arguments after `--` go to `main` |
| `kukel check a.kkl ...` | Compile only and report the first error; prints nothing when the program is fine |
| `kukel build [-o prog.kkc] a.kkl ...` | Compile to a bytecode file (default name: the first file with `.kkc`) |
| `kukel exec prog.kkc [args]` | Run a bytecode file |
| `kukel disasm prog.kkc` or `kukel disasm a.kkl ...` | Show the bytecode in readable form |
| `kukel tokens a.kkl`, `kukel ast [-pos] a.kkl` | Show the tokens or the syntax tree (for debugging) |

The exit code is `main`'s result when it returns an int, else 0. A compile
error or an uncaught exception gives exit code 1; `exit(code)` ends the
program with that code. A wrong command line (an unknown command, a missing
file name) gives exit code 2.

Files used as modules (`use name;`) are found next to the file that uses
them or through `KUKEL_PATH`; see [Modules](#modules). The operating system keeps only the low 8
bits of an exit code: `return 263` exits with 7, `return -1` with 255.

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

**Keywords**: `break case catch class const continue default do else elseif
false finally fn for global if in local new nil return static switch this
throw true try use while`.

**Integers**: `42`, `1_000_000` (`_` only between digits), `0xFF_FF`,
`0b1010`. A leading zero does not mean octal. Decimal literals go up to
9223372036854775807 (and `-9223372036854775808`); hexadecimal and binary
literals may use all 64 bits (`0xFFFFFFFFFFFFFFFF` is -1): at most 16 hex
or 64 binary digits, leading zeros included.

**Floats**: `1.5`, `1.`, `.5`, `2e10`, `1.5E-3`. A value beyond the float
range is an error.

**Characters**: `'a'`, `'\n'`, `'\''` — one byte, whose value is an int (its
code): `'A'` is 65.

**Strings**: `"text"`, which may span lines. Escapes: `\n \t \r \0 \\ \" \'`
and `\x41` (one or two hex digits). Any other escape is an error.
Strings are sequences of bytes; UTF-8 text passes through unchanged.

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
and objects strings are quoted. Floats always show a `.` or an exponent
(`1.0`, `1e+21`, `inf`, `nan`). Objects print as `Point{x: 1, y: 2}`; a
container inside itself prints as `[...]`, `{...}` or `Point{...}`.

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
  calls, not the comma operator). Every part is checked, also the part that
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
  `KUKEL_PATH` (separated by `:`, or `;` on Windows).
- A module is an ordinary source file with its own name space. Its
  top-level names are reached as `name::x`: functions (`geometry::hypot(3,
  4)`), constants (also in constant expressions), globals (which can be
  assigned), and classes (`new geometry::Circle(1)`, `class Ring :
  geometry::Circle`, `geometry::Circle::x` for a static member).
- `use` holds for the file that contains it. A module sees its own names,
  the built-ins, `Error`, and the modules it uses itself; it does not see the
  program's names. A program and its modules may define the same names.
- Names that start with `_` are private: `geometry::_square` is a compile
  error outside the module.
- Each module is loaded once, however many files use it, and its global
  initializers run before those of the files that use it. A module name
  stands for one file in the whole program. Modules may not use each other
  in a cycle, and a file given on the command line cannot also be a module.
- A module's name cannot be the name of a built-in or `Error`, nor a
  top-level name of the program or module whose file uses it.
- A class of a module is printed and named in messages with its module:
  `geometry::Circle`. Its constructor is still `Circle`.
- `kukel build` stores the program and all its modules in one `.kkc` file;
  `kukel exec` does not need the module files.

---

## 5. Statements

```
{ local a = 1, b; a = 2; }         // a block; locals are visible after their initializer
if (x > 0) f(); elseif (x < 0) g(); else h();
while (i < 10) i++;
do { i--; } while (i > 0);
for (local i = 0; i < n; i++) ...  // the locals belong to the loop
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
are constant expressions; duplicates and a second `default` are errors.
`case` labels may appear inside nested statements (as in C), but not where
jumping to them would enter a try block or skip a `local`'s initialization.
A `local` directly in a switch body is not allowed: put it in a block.

**Labels**: `name:` before a statement (on the same line as the name).
`break name` leaves that statement; `continue name` continues that loop.
A plain `break` leaves the innermost loop or switch, a plain `continue`
continues the innermost loop. Neither crosses a function literal.

**Finally**: a `finally` block runs however its try block is left: at the
end, by `return`, `break` or `continue`, or by an exception. `return` in a
finally block, and `break`/`continue` out of it, are compile errors.

---

## 6. Expressions

Operators, from lowest to highest precedence:

| Operators | Associativity | Notes |
|---|---|---|
| `,` | left | evaluates both, gives the right one |
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

**Numbers**: ints wrap around on overflow. An int with a float gives a
float. `int / int` truncates toward zero and `%` has the sign of the left
side; `/` or `%` by integer 0 is a `ZeroDivisionError` (a float division by
zero gives `inf` or `nan`). Comparing an int with a float is exact.

**Strings**: `+` needs two strings: `"n = " + str(n)`. `s[i]` gives a byte
as an int; strings cannot be changed (`s[i] = x` is an error).

**Indexing**: `v[i]` with an int from 0 to `len(v) - 1` (no negative
indexes); assigning `v[len(v)]` is an error (use `push`). `m[k]` gives
`nil` for a missing key (use `haskey` to tell a stored nil apart). Map keys
are strings and numbers; `1` and `1.0` are the same key.

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
sum(v; total) {                                 // total: a temporary (a local that starts as nil)
    total = 0;
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
- `f(a, v.., b)` passes the elements of vector `v` as separate arguments.
- A call with the wrong number of arguments is a compile error when the
  function is known when compiling (a function, a static member function,
  `Base::m(...)`, `new C(...)`), and an `ArgumentError` otherwise.
- Functions are values: `local f = square; f(3);`.
- Recursion is limited to 10000 nested calls (`StackOverflowError`).

**Function literals** (closures):

```
counter(; n) {
    n = 0;
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
  Without any constructor `new C()` takes no arguments. `new` also works
  with a class in a variable: `local k = Rect; new k(1, 2)`.
- Base constructors are not called automatically: call `Base::Base(...)`.
- Inside a member function, a name is looked up as a local, parameter,
  temporary, captured variable, then a member of the class (also inherited
  ones), then a top-level name. `x` means `this->x`, `f()` means
  `this->f()`. A static member function has no `this`.
- `p->m(...)` calls the member function of `p`'s class (all member
  functions are virtual). `Base::m(...)` calls `Base`'s version on the
  current `this` (not virtual). `C::f(...)` calls a static member function;
  `C::x` is a static data member.
- A derived class may redeclare a member function of its base (overriding
  it); redeclaring a data member is an error.
- `p->x` reads or assigns a data member (all members are public). Static
  data members are reached as `C::x` only; a static member function can
  also be called as `p->f(...)`.
  `p->m` without a call is a *bound method*: a function value that calls
  `m` on `p` later (`sort(v, p->less)`). `(p->f)(x)` calls a function held
  in a data member.
- A member function may be declared without a definition; calling it is a
  `MemberError` ("declared but not defined").

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

- Any value can be thrown; `catch (e)` catches everything.
- Runtime errors throw objects of the built-in class `Error`, which has the
  data members `message`, `kind` and `trace`. Programs throw it and derive
  from it too (`new Error([message [, kind]])`).
- The first time an `Error` is thrown, `trace` gets the stack trace (a
  vector of strings such as `"f (prog.kkl:3)"`, innermost call first) and,
  if `kind` is nil, `kind` becomes the name of its class.
- An exception thrown in a `finally` block replaces the one in progress.
- An uncaught exception ends the program with exit code 1, printing
  `kind: message` (just `kind` when the message is nil; `uncaught exception:
  value` for values that are not `Error` objects) and the stack trace.
- `exit(code)` ends the program at once; it cannot be caught and `finally`
  blocks do not run.

Kinds of runtime errors:

| Kind | When |
|---|---|
| `TypeError` | an operation on values of the wrong type (`1 + "a"`, calling an int, ...) |
| `ZeroDivisionError` | integer `/` or `%` by zero |
| `RangeError` | an index out of range, a shift count outside 0..63, a number that does not fit |
| `ArgumentError` | a wrong number of arguments; an argument a built-in cannot work with (`replace` of `""`, `min` of no values) |
| `MemberError` | an unknown data member or member function, a function declared but not defined |
| `ValueError` | a string that is not a number (`int`, `float`) |
| `IOError` | a file or input/output error |
| `StackOverflowError` | more than 10000 nested calls |
| `InternalError` | a damaged `.kkc` file that the loader's checks did not catch (should not happen) |

---

## 10. Built-in functions

Argument positions in error messages count from 1. Unless noted, a wrong
type is a `TypeError` and a wrong count, or a value a function cannot work
with, an `ArgumentError`.

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
| `isnil(x)` | Whether `x` is `nil` |
| `classof(obj)` | The class of an object |
| `instanceof(x, C)` | Whether `x` is an object of class `C` or of a class derived from it |

### Strings

| Function | Description |
|---|---|
| `len(x)` | Length of a string (in bytes), vector or map |
| `substr(s, start [, n])` | `n` bytes from `start` (to the end without `n`) |
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
| `slice(x, start [, end])` | A new vector (or string) of the elements from `start` up to `end` |
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
| `randint(a, b)` | An int from `a` to `b`, both included |
| `seed(n)` | Restarts the random numbers from seed `n` (the same seed gives the same numbers) |

### System

| Function | Description |
|---|---|
| `time()` | Seconds since 1970-01-01 UTC, as a float |
| `clock()` | Seconds since the program started, as a float |
| `getenv(name)` | The environment variable, or `nil` |
| `exit([code])` | Ends the program at once with `code` (default 0) |
