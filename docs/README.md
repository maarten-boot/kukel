# Kukel's documents

| Document | What it is for |
|---|---|
| [tutorial.md](tutorial.md) | learning Kukel: a tour from the first program to modules, events and workers |
| [reference.md](reference.md) | the whole language and tool: commands, the REPL, the debugger, the profiler, syntax, types, statements, classes, exceptions, the built-in functions, and every module written in Go (§11: `db`, `events`, `fs`, `json`, `p9`, `re`, `strings`, `time`, `tk`, `uuid`, `worker`, `zmq`) |
| [debugging.md](debugging.md) | debugging in an editor with `kukel dap`: Visual Studio Code, Neovim, Helix, Emacs |
| [embedding.md](embedding.md) | using Kukel from a Go program: interpreters, values, Go functions for Kukel, errors, untrusted code, goroutines, profiling |
| [vm.md](vm.md) | how programs run: the virtual machine, every instruction, how the compiler translates programs, the `.kkc` file format |
| [checks.md](checks.md) | for developers: the subsystems, what to check after a change, and the tests |
| [future.md](future.md) | ideas for later, and the questions they raise |

Elsewhere in the repository:

- [README.md](../README.md): what Kukel is, installing, the commands;
- [CHANGELOG.md](../CHANGELOG.md): what changed in each release;
- [TODO.md](../TODO.md): the plan and every design decision (the Q entries);
- [syntax.ebnf](../syntax.ebnf) and [syntax.lark](../syntax.lark): the
  grammar;
- [examples/README.md](../examples/README.md): the example programs;
- [editors/vscode/README.md](../editors/vscode/README.md): the Visual Studio
  Code extension.
