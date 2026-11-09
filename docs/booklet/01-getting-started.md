# 1. Getting started

[The booklet](README.md) · next: [2. Values and types](02-values.md)

This chapter installs kukel, runs a first program, and shows the commands
you will use every day.

## Installing

There are two ways.

**A ready-made program.** Each release on
[GitHub](https://github.com/maarten-boot/kukel/releases) has archives for
Linux, macOS and Windows, on amd64 and arm64. There are two kinds:

- `kukel`: the language and every module, except windows;
- `kukel-tk`: the same, plus the module `tk` for windows on the desktop
  (Part II). It is larger, and its windows need a display.

Unpack the archive and put `kukel` (or `kukel-tk`, renamed to `kukel` if
you like) in a directory on your `PATH`.

**From the source**, with Go 1.25 or newer:

```text
go install github.com/maarten-boot/kukel/cmd/kukel@latest
go install -tags tk github.com/maarten-boot/kukel/cmd/kukel@latest   # with tk
```

Check what you have:

```
$ kukel version
kukel v1.4.0 (2b9169674f2c), go1.27.3, bytecode format 12, Unicode 17.0.0
modules written in Go: db (SQLite, PostgreSQL, MySQL), events, fs, json, net, p9, re, strings, term, time, toml, utf8, uuid, worker, zmq
modules written in Kukel: iter
```

The first line gives the version, the commit it was built from, the Go
that built it, the bytecode format (`.kkc` files of another format must
be built again) and the version of Unicode that the module `utf8` knows.
The Go and Unicode versions depend on the Go that built your kukel, so
yours may differ from these.

The modules listed are the ones this kukel has; Part II describes them.
If you have more than one kukel (an old `go install` and a new archive),
`kukel version` also tells you which one your shell finds first.

## A first program

Put this in a file `hello.kkl`:

```
// hello.kkl: greets whoever is named on the command line.
main(args) {
    local name = len(args) > 0 ? args[0] : "world";
    println("hello,", name);
}
```

and run it:

```
$ kukel run hello.kkl -- Kukel
hello, Kukel
```

What is in it:

- A line from `//` to its end is a comment (so is `/* ... */`, and a line
  from `#`).
- A program starts at its function `main`. The parameter `args` is a
  vector (a list) of the words after `--` on the command line. Without
  them it is empty, and the program greets the world.
- `local name = ...;` makes a variable that lives until the end of its
  block, the `{ ... }` around it.
- `c ? a : b` is `a` when `c` is true, else `b`: here, the first
  argument if there is one.
- `println` writes its arguments separated by spaces, and then a new line.
- Statements end with `;`. Blocks are in braces, as in C, Java or
  JavaScript.

`kukel hello.kkl Kukel` is short for `kukel run hello.kkl -- Kukel`. That
makes a Kukel program a command of its own on Linux and macOS: give the
file a first line

```
#!/usr/bin/env kukel
```

make it executable (`chmod +x hello.kkl`), and run it as `./hello.kkl
Kukel`.

## When something is wrong

kukel compiles the whole program before it runs any of it, so many
mistakes are found before the program starts. A file `greet.kkl` with a
`;` missing:

```
greet(name) {
    println("hello,", name)
}
```

```
$ kukel check greet.kkl
greet.kkl:3:1: expected ';' after expression, found '}'
```

The message names the file, the line and the column where kukel noticed.
`kukel check` compiles without running; `kukel run` says the same and
runs nothing.

A mistake only running can find, such as an index past the end of a
vector, stops the program with an *exception*: its kind, a message, and
the calls that led to it, innermost first:

```
$ kukel run index.kkl
RangeError: index 3 out of range for vector of length 3
  at main (index.kkl:3)
```

Chapter 10 shows how a program catches exceptions.

## Trying things out

`kukel repl` reads a line at a time and runs each one at once; an
expression shows its value. It is a quick way to try what this booklet
shows:

```
$ kukel repl
> 7 / 2
3
> 7.0 / 2
3.5
> square(n) { return n * n; }
> square(12)
144
```

A blank line or the end of the input (Ctrl-D) ends it.

## The commands

| Command | What it does |
|---|---|
| `kukel run a.kkl [b.kkl ...] [-- args]` | compiles the files as one program and runs it |
| `kukel a.kkl [args]` | the same, for scripts |
| `kukel check a.kkl ...` | compiles only, and reports the errors |
| `kukel fmt -w a.kkl` | lays the file out the standard way: every program in this booklet is laid out like that |
| `kukel repl` | a line at a time, as above |
| `kukel debug a.kkl` | runs under the debugger: stop at a line, look at values (Part II) |
| `kukel build a.kkl` | compiles to a bytecode file `a.kkc`, which `kukel exec a.kkc` runs |
| `kukel help` | all the commands |

## Where kukel looks for help

- This booklet, from the next chapter on.
- The [reference](../reference.md): everything, in full detail.
- The [examples](../../examples/README.md): longer programs, such as a
  `grep`, a small `vi`, a chat over the network and a to-do list in a
  database.

[The booklet](README.md) · next: [2. Values and types](02-values.md)
