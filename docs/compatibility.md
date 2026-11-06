# Compatibility

From v1.0.0 on, kukel's versions follow [semantic versioning](https://semver.org):
within v1 (v1.0.0, v1.1.0, v1.2.3, ...) what this page calls stable keeps
working. A release that breaks it is v2.

- **v1.x.y → v1.x.(y+1)**: fixes only.
- **v1.x → v1.(x+1)**: additions: new built-in functions, modules, methods,
  options, commands and syntax. A program or Go program that worked keeps
  working.

## Stable

- **The language**: programs of syntax version 10, as
  [docs/reference.md](reference.md) and [syntax.ebnf](../syntax.ebnf)
  describe them, keep compiling and mean the same. New syntax adds forms; it
  does not refuse or change old ones. New words get their meaning from
  where they stand, as `from` in `yield from` does, so a name a program
  uses does not become a keyword.
- **The built-in functions and the standard modules** (`kukel version`
  lists them): their names, arguments, results, and the kinds of the errors
  they throw (`TypeError`, `ValueError`, ...). New optional arguments and
  new functions may come.
- **The commands** of `kukel help`, their options, and how `kukel a.kkl args`
  runs a script; the exit code: the program's own (`main`'s result or
  `exit(n)`), else 0, and 1 after an error.
- **The Go package** `github.com/maarten-boot/kukel` (see
  [embedding.md](embedding.md)): what it exports keeps its meaning. New
  fields, methods and functions may come; a struct is best built with field
  names (`kukel.Options{StepLimit: 1000}`).

## Not stable

- **The bytecode format.** A `.kkc` file is read only by a kukel of the same
  format version (11 in v1.0.0). A minor release may change the format;
  then `kukel build` must run again from the source, and the CHANGELOG says
  so. Source is the stable form; [vm.md](vm.md) §7 has the history.
- **Error messages**, warnings and other text meant for people: their words
  may become clearer. Test for the kind of an error, not its message.
- **Output meant for people or tools to look at**: `kukel fmt`'s layout,
  `kukel disasm`, `kukel tokens`, `kukel ast`, the profiler's report, the
  debugger's display, the REPL. (The pprof file of `kukel profile -pprof`
  stays a pprof file.)
- **Timing** and how fast things run.
- **What is under `internal/`**, and the instruction set of
  [vm.md](vm.md).

## Bugs

A fix may change what a program does when kukel did something other than
its documents say. When the documents are unclear, the behavior of the last
release counts, and the documents are made clear.
