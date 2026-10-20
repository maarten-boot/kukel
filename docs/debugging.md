# Debugging Kukel programs

Kukel has two debuggers with the same core:

- `kukel debug a.kkl ...` on a terminal, with commands such as `break`,
  `step` and `print` (see the reference, [The debugger](reference.md#the-debugger));
- `kukel dap` for editors: it speaks the
  [Debug Adapter Protocol](https://microsoft.github.io/debug-adapter-protocol/)
  on standard input and output, which editors start themselves. Visual
  Studio Code, Neovim, Helix and Emacs can use it (setup below).

Both stop at breakpoints and at an exception that nothing catches, step
through the program, and show and evaluate values. Only the main
interpreter stops; workers run without the debugger.

Contents:

1. [What kukel dap does](#1-what-kukel-dap-does)
2. [The launch configuration](#2-the-launch-configuration)
3. [Expressions](#3-expressions)
4. [Visual Studio Code](#4-visual-studio-code)
5. [Neovim](#5-neovim)
6. [Helix](#6-helix)
7. [Emacs](#7-emacs)

## 1. What kukel dap does

| Feature | |
|---|---|
| Launch | Runs a program from source files, with its arguments, working directory, environment and standard input (section 2). It cannot attach to a running program |
| Breakpoints | On a line (a line without code moves to the next line with code; the editor shows where), on a function (by name: `main`, `Shape::area`, `util::f`), with a condition (an expression, section 3), a hit count, or a log message |
| Hit counts | `3` stops the 3rd time only; `== 3`, `!= 3`, `> 3`, `>= 3`, `< 3`, `<= 3` compare the count, `% 3` stops every 3rd time. The count goes up only when the condition (if any) holds |
| Log messages | A logpoint writes its message to the debug console instead of stopping; `{expression}` in it is replaced by its value, `{{` and `}}` are a brace |
| Exceptions | The filter "Uncaught exceptions" (on by default) stops where an exception is thrown that no `try` catches, before the program ends; going on then ends it, with the trace on standard error |
| Stepping | Continue, step over, step into, step out, and pause (the program stops at the next line it starts: a program that waits, in `events::run()` or `time::sleep`, stops when it goes on) |
| Stack and variables | The calls, innermost first; for each, its locals (with `this` in a member function) and the program's globals (also those of modules: `util::count`). Vectors, maps and objects can be opened; long vectors come in parts |
| Changing values | A variable, global, element of a vector or map, or data member of an object gets the value of an expression |
| Evaluate | The debug console, watches and hovers evaluate expressions (section 3) in the frame chosen |
| Output | What the program writes to standard output and standard error appears in the debug console |
| Ending | Stop (terminate) ends the program; disconnecting does too |

`kukel dap` serves one program and ends with it. Messages about the
adapter itself (not the program) go to its standard error, which editors
show in their log.

## 2. The launch configuration

| Attribute | |
|---|---|
| `program` | The `.kkl` file with `main` (required) |
| `files` | More source files of the program (modules are found by `use`, as with `kukel run`) |
| `args` | The program's arguments: `main(args)` gets them |
| `cwd` | Its working directory (the adapter's when left out) |
| `stdin` | A file the program reads as its standard input (`readline()`); without one the input is empty. The debug console cannot type to the program |
| `env` | More environment variables, such as `KUKEL_PATH` for modules elsewhere |
| `stopOnEntry` | `true`: stop at the first line that runs (a global initializer, or `main`) |
| `noDebug` | `true`: run without stopping (editors set it for "Run Without Debugging") |

```json
{
  "type": "kukel",
  "request": "launch",
  "name": "Debug grep",
  "program": "${workspaceFolder}/examples/grep.kkl",
  "args": ["-n", "error", "testdata/log.txt"],
  "cwd": "${workspaceFolder}/examples"
}
```

A program that does not compile is not launched: the editor shows the
compile errors.

## 3. Expressions

Conditions, log messages, watches, hovers, the debug console and new
values are Kukel expressions without side effects, evaluated as the
function of the frame sees its names:

- literals, also vectors and maps (`[1, 2]`, `{"k": v}`);
- names: a local, `this`, a data member of `this`, a static data member of
  the class, a global of the function's module, a global; qualified names
  `C::x` (a static data member) and `util::count`;
- the operators (`+`, `==`, `&&`, `<<`, `!`, ...), `?:`, indexing `v[i]`
  and members `p->x`.

Calls, assignments, `++`/`--`, `new` and function literals are not
evaluated: they could change the program. `kukel debug`'s `print` takes the
same expressions, and its `break` a condition: `b 12 if i == 3`.

## 4. Visual Studio Code

The extension in `editors/vscode` registers the debug type `kukel` (it
runs `kukel dap`) and highlights `.kkl` files. Build it (Node.js needed)
and install it:

```
make vscode
code --install-extension dist/kukel-vscode-0.28.0.vsix
```

`kukel` must be in your `PATH`, or named in the setting `kukel.path`. F5
debugs the `.kkl` file in the editor; for more, "Add Configuration..." in
`launch.json` offers "Kukel: Launch" (section 2).

## 5. Neovim

With [nvim-dap](https://github.com/mfussenegger/nvim-dap):

```lua
vim.filetype.add({ extension = { kkl = "kukel" } })

local dap = require("dap")
dap.adapters.kukel = { type = "executable", command = "kukel", args = { "dap" } }
dap.configurations.kukel = {
  {
    type = "kukel",
    request = "launch",
    name = "Debug the Kukel program",
    program = "${file}",
    cwd = "${fileDirname}",
  },
}
```

## 6. Helix

In `languages.toml`:

```toml
[[language]]
name = "kukel"
scope = "source.kukel"
file-types = ["kkl"]
comment-token = "//"
indent = { tab-width = 4, unit = "    " }

[language.debugger]
name = "kukel"
transport = "stdio"
command = "kukel"
args = ["dap"]

[[language.debugger.templates]]
name = "launch"
request = "launch"
completion = [{ name = "program", completion = "filename", default = "main.kkl" }]
args = { program = "{0}" }
```

## 7. Emacs

With [dape](https://github.com/svaante/dape) (in GNU ELPA, Emacs 29 and
later):

```elisp
(add-to-list 'auto-mode-alist '("\\.kkl\\'" . prog-mode))
(with-eval-after-load 'dape
  (add-to-list 'dape-configs
               '(kukel
                 modes (prog-mode)
                 command "kukel"
                 command-args ("dap")
                 :type "kukel"
                 :request "launch"
                 :program dape-buffer-default
                 :cwd dape-cwd)))
```

With [dap-mode](https://emacs-lsp.github.io/dap-mode/):

```elisp
(require 'dap-mode)
(dap-register-debug-provider
 "kukel"
 (lambda (conf)
   (plist-put conf :dap-server-path '("kukel" "dap"))
   (unless (plist-get conf :program)
     (plist-put conf :program (buffer-file-name)))
   conf))
(dap-register-debug-template
 "Kukel: debug the program"
 (list :type "kukel" :request "launch" :name "Kukel"))
```
