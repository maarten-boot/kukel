# D. Tables

[The booklet](README.md) · previous: [C. Code that is not trusted](24-untrusted.md)

The short forms of what the chapters explain, to look things up. The
[reference](../reference.md) has every detail, and
[syntax.ebnf](../../syntax.ebnf) the whole grammar.

## Keywords

```
break  case  catch  class  const  continue  default  do  else  false
finally  fn  for  global  if  in  local  new  nil  return  static
switch  this  throw  true  try  use  while  with  yield
```

`from` is not a keyword: it means something only right after `yield` (chapter 11).

## Operators

From the loosest to the tightest:

| Operators | |
|---|---|
| `=` `+=` `-=` `*=` `/=` `%=` `&=` `\|=` `^=` `<<=` `>>=` | assigning, right to left |
| `c ? a : b` | choosing, right to left |
| `\|\|` | or (gives `true` or `false`) |
| `&&` | and (gives `true` or `false`) |
| `\|` `^` `&` | bitwise or, exclusive or, and (ints) |
| `==` `!=` | equal: numbers by value, strings by text, the rest by identity |
| `<` `<=` `>` `>=` | numbers with numbers, strings with strings |
| `<<` `>>` | shifts (ints) |
| `+` `-` | adding; `+` joins two strings |
| `*` `/` `%` | multiplying; `/` of two ints drops the fraction |
| `-x` `!x` `~x` `++x` `--x` | one operand |
| `f(x)` `v[i]` `p->x` `x++` `x--` | calls, indexes, members |

In a `for` loop's first and last part, `,` joins expressions.

## Statements

```
local x = 1, y;                     global g;         const MAX = 10;
if (c) s; else if (d) t; else u;
while (c) s;      do s; while (c);      for (local i = 0; i < n; i++) s;
for (local x in v) s;               for (local i, x in v) s;
switch (x) { case 1: case 2: s; break; default: t; }
name: for (...) { ... break name; ... continue name; }
return x;         throw new Error("message", "Kind");
try { ... } catch (e) { ... } finally { ... }
with (local f = open(...)) { ... }  // f->close() at the end
yield x;          local y = yield x;          yield from g;
```

## Escapes in strings

| Escape | |
|---|---|
| `\n` `\t` `\r` `\0` | new line, tab, carriage return, the byte 0 |
| `\\` `\"` `\'` | a backslash, a double and a single quote |
| `\x41` | a byte by its code (one or two hexadecimal digits) |
| `\u{E9}` | a character by its Unicode code, written as UTF-8 |

## Built-in functions

| Kind | Functions |
|---|---|
| Output and input | `print`, `println`, `eprint`, `eprintln`, `readline` |
| Whole files | `readfile`, `writefile`, `appendfile` |
| Types | `typeof`, `str`, `int`, `float`, `classof`, `instanceof` |
| Strings | `len`, `find`, `split`, `join`, `slice`, `upper`, `lower`, `trim`, `replace`, `char`, `ord` |
| Vectors and maps | `push`, `pop`, `insert`, `remove`, `slice`, `sort`, `keys`, `values`, `haskey`, `delete` |
| Math | `abs`, `min`, `max`, `pow`, `sqrt`, `exp`, `log`, `sin`, `cos`, `tan`, `atan2`, `floor`, `ceil`, `round`, `random`, `randint`, `seed` |
| System | `time`, `clock`, `program`, `getenv`, `exit` |

## Kinds of errors

| Kind | When |
|---|---|
| `TypeError` | a value of the wrong type: `1 + "a"`, calling a number |
| `ValueError` | a value a function cannot work with: `int("x")`, invalid JSON |
| `RangeError` | an index, or a number, out of range |
| `ZeroDivisionError` | an int divided by the int 0 |
| `ArgumentError` | a wrong number of arguments, found while running |
| `MemberError` | a member an object does not have |
| `IOError` | a file that cannot be read or written |
| `NetError` | a network problem (`net`, `zmq`, `p9`) |
| `DBError` | an error the database reports |
| `WorkerError` | a worker that failed, from `w->wait()` |
| `CompileError` | `worker::startfile` of a program that does not compile |
| `StackOverflowError` | more than 10000 nested calls |
| `HostError` | a Go function of the embedding program failed |
| `StepLimitError`, `CancelledError` | the embedding program's limits (cannot be caught) |

## The standard modules

| Module | For | Chapter |
|---|---|---|
| `fs` | files and directories | 13 |
| `utf8`, `re`, `json`, `strings` | characters, patterns, JSON, building text | 14 |
| `time`, `uuid` | dates and times, identifiers | 15 |
| `db` | SQLite, PostgreSQL, MySQL | 16 |
| `events`, `worker` | the event loop, work in parallel | 17 |
| `net`, `zmq`, `p9` | TCP and UDP, messages, files over the network | 18 |
| `term` | the terminal a key at a time | 19 |
| `tk` | windows (in `kukel-tk`) | 21 |
| `iter` | generators to make and combine sequences (written in Kukel) | 11 |

## What stays the same

From version 1.0.0 on, kukel keeps what [compatibility.md](../compatibility.md)
calls stable, within version 1: programs that work keep working, the
built-in functions and standard modules keep their names and meanings,
and the commands keep their options. New versions 1.x only add. What may
change is the bytecode format (rebuild `.kkc` files from their source when
the CHANGELOG says so), the words of error messages, and how fast things
run.

[The booklet](README.md) · previous: [C. Code that is not trusted](24-untrusted.md)
