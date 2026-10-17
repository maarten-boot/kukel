# Embedding Kukel in Go programs

The package `github.com/maarten-boot/kukel` runs Kukel code inside a Go
program: compile a program once, run it in as many interpreters as needed,
call its functions, and give it Go functions of your own. The examples in
the package documentation (`go doc github.com/maarten-boot/kukel`) are
tested; this page explains the parts and how they fit.

```go
import "github.com/maarten-boot/kukel"

prog, err := kukel.CompileFiles([]string{"rules.kkl"}, kukel.CompileOptions{})
in, err := kukel.New(prog, kukel.Options{Stdout: os.Stdout})
discount, err := in.Call("discount", kukel.Int(250))
```

Contents:

1. [Programs and interpreters](#1-programs-and-interpreters)
2. [Values](#2-values)
3. [Go functions for Kukel: host modules](#3-go-functions-for-kukel-host-modules)
4. [Errors](#4-errors)
5. [Code that is not trusted](#5-code-that-is-not-trusted)
6. [Goroutines](#6-goroutines)

---

## 1. Programs and interpreters

A **`Program`** is compiled code. It never changes while it runs, so one
`Program` can serve any number of interpreters, also at the same time.

The package brings the standard modules written in Go (`StandardModules()`
lists them). The build tags of the README leave large ones out of a Go
program that imports it too: `go build -tags nopostgres,nomysql,nozmq,no9p`.

| Function | |
|---|---|
| `CompileFiles(files, opts)` | Compiles `.kkl` files as one program, with the modules they use (next to the files, then in `opts.Path`; a module compiled on its own, `name.kkc`, is linked in) |
| `CompileSource(name, src, opts)` | Compiles a program given as text; `name` appears in messages. Modules written in Kukel come from `opts.Path` only |

`CompileOptions` (all may be left zero): `Path`, the directories where
`use` finds `name.kkl` or `name.kkc`; `Modules`, host modules (section 3);
and the limits of section 5 (`DenyBuiltins`, `StandardModules`,
`NoFileModules`).

| Function | |
|---|---|
| `LoadProgram(data, opts)` | Reads a program from the bytes of a `.kkc` file (`kukel build`, or `Program.Encode`). `opts.Modules` must hold the host modules the program uses again: a `.kkc` file names their functions but does not contain them |
| `p.Encode()` | The program as `.kkc` bytes, to store and load later without compiling |

An **`Interp`** runs a program: it has its own global variables, output and
state, which last between calls.

| Method | |
|---|---|
| `New(p, opts)` | A new interpreter. `Options`: `Stdout`, `Stderr` (nil: discarded), `Stdin` (nil: empty), `StepLimit` |
| `in.Run(args...)` | Runs `main` (with `args` as its vector of strings if it has a parameter) and gives the exit code (1 together with the error if the program fails) |
| `in.Call(name, args...)` | Calls a top-level function (`"discount"`) or a module's (`"util::f"`, also private names such as `"util::_g"`, and `"json::encode"` of any Go module the program may use) and gives its result. Not reachable by name: static member functions (`"C::f"`) and built-ins (`"println"`): call a top-level function that calls them |
| `in.CallValue(fn, args...)` | Calls a function value, such as a callback a Go function received |
| `in.Global(name)` | The value of a global variable, function or class (also a module's). Constants (`const`) and static data members are not globals: an error names them as missing |
| `in.RunContext(ctx, ...)`, `in.CallContext(ctx, ...)` | The same, stopped when `ctx` ends (section 5) |
| `in.Close()` | Ends the interpreter: cancels the workers it started that still run and drops their later output; calls afterwards give an error |

The global initializers run once, before the first `Run`, `Call` or
`Global` (and count against that call's step budget). If they throw an
exception, every call gives that error: the globals are not usable. If the
step limit or a cancellation stops them, the next call runs them again
from the start. A program needs a
`main` (it may be empty) even when the Go program only calls other
functions.

`Run` does not wait for workers the program started: they may still run,
and write output, after it returns. Call `Close` when the interpreter is no
longer needed.

`Builtins()` and `StandardModules()` list the names of the built-in
functions and of the standard modules (`db`, `events`, `fs`, `json`, `p9`,
`re`, `strings`, `time`, `uuid`, `worker`, `zmq`).

## 2. Values

`kukel.Value` holds any Kukel value; the zero `Value` is `nil`
(`kukel.Nil`).

| Make | Read |
|---|---|
| `Bool(b)`, `Int(i)`, `Float(f)`, `String(s)` | `v.AsBool()`, `v.AsInt()`, `v.AsFloat()` (also of an int), `v.AsString()`, each with an `ok` |
| `Vector(elems...)`, `NewMap()`, `MapOf(map[string]Value)` (keys in sorted order) | `v.Len()`, `v.Index(i)`, `v.Get(key)`, `v.Keys()` |
| `v.Push(e)`, `v.SetKey(k, val)` | `v.Kind()` (as `typeof`), `v.IsNil()`, `v.Truthy()`, `v.String()` (as `print`) |
| `FromGo(x)`: nil, bools, integers, floats and strings (also named types such as `type S string`), `[]byte` (a string), slices and arrays, maps with string keys, nested | `v.ToGo()`: nil, `bool`, `int64`, `float64`, `string`, `[]any`, `map[string]any`; functions, classes and objects stay a `Value`. A map with keys that give the same Go key (`1` and `"1"`), or a value that contains itself, is an error. `FromGo` refuses an unsigned integer above the int range, a map whose keys are not strings, and other Go types (functions, channels, structs, ...) |

Vectors, maps and objects are shared, not copied: a `Value` from an
interpreter refers to its data, and changing it (`Push`, `SetKey`) changes
what the program sees. Use such values only while that interpreter is not
running on another goroutine; `ToGo` makes an independent copy.

## 3. Go functions for Kukel: host modules

Kukel checks every name when it compiles, so Go functions must be known
then. They come in **host modules**: modules written in Go that belong to
one program (not to the whole process), used like the standard modules.

```go
app := kukel.NewModule("app").
    Const("version", kukel.String("1.4")).
    Func("notify", func(in *kukel.Interp, args []kukel.Value) (kukel.Value, error) {
        log.Println("notify:", args[0])
        return kukel.Nil, nil
    })
prog, err := kukel.CompileFiles(files, kukel.CompileOptions{Modules: []*kukel.Module{app}})
```

```
// in Kukel
use app;
main() { app::notify("started " + app::version); }
```

- A function gets the calling interpreter and the arguments; it checks the
  arguments itself (count and types). The arguments are valid during the
  call only: keep copies (`ToGo`) of what is needed later. It may call back into Kukel with
  `in.CallValue(fn, ...)`, for example to call a function the program passed
  in. A panic in the function becomes a `"HostError"` exception; the
  interpreter stays usable.
- A constant (nil, a boolean, a number or a string) can be used in Kukel
  constant expressions.
- Add everything before compiling: a module cannot change once a program
  uses it. The same module may serve several programs.
- `NewModule`, `Func` and `Const` panic on a programming mistake: a name
  that is not an identifier (or is a keyword), a nil function, a name used
  twice, a constant that is not nil, a boolean, a number or a string, or an
  addition after the module is in use.
- A host module hides a standard module with the same name.
- A program's workers (`worker::start`) can use its host modules too: the
  function then gets the worker's interpreter, on the worker's goroutine.
  Make such functions safe for concurrent use.

**Classes written in Go.** A host module can also hold classes whose
objects carry a Go value (their *state*) and whose member functions are Go
functions. Kukel code makes objects with `new`, calls their member
functions, and may derive its own classes from them:

```go
type counter struct{ n int64 }

counterClass := kukel.NewClass("Counter").
    New(func(in *kukel.Interp, this *kukel.Object, args []kukel.Value) (kukel.Value, error) {
        this.SetState(&counter{})              // the constructor sets the state
        return kukel.Nil, nil
    }).
    Method("inc", func(in *kukel.Interp, this *kukel.Object, args []kukel.Value) (kukel.Value, error) {
        c := this.State().(*counter)
        c.n++
        if _, err := this.Call(in, "changed", kukel.Int(c.n)); err != nil { // overridable
            return kukel.Nil, err
        }
        return this.Value(), nil
    }).
    Method("changed", func(in *kukel.Interp, this *kukel.Object, args []kukel.Value) (kukel.Value, error) {
        return kukel.Nil, nil
    }).
    Show(func(this *kukel.Object) string {
        return fmt.Sprintf("app::Counter(%d)", this.State().(*counter).n)
    })
app := kukel.NewModule("app").Class(counterClass)
```

```
// in Kukel
use app;

class Loud : app::Counter { Loud(); changed(n); }
Loud::Loud() { app::Counter::Counter(); }        // sets up the Go state
Loud::changed(n) { println("now", n); }

main() {
    local c = new app::Counter();
    c->inc()->inc();
    println(c);                                  // app::Counter(2)
    new Loud()->inc();                           // now 1
}
```

| Method | |
|---|---|
| `NewClass(name)` | An empty class |
| `c.New(m)` | The constructor, for `new app::C(...)`: `m` gets the new object and sets its state (`this.SetState`); its result is not used. Without one, objects come from the module's functions (`c.Object`) and Kukel classes cannot derive from the class |
| `c.Method(name, m)` | An instance member function: `m(in, this, args)` |
| `c.Static(name, f)` | A static member function (`app::C::f(...)`), a `Func` |
| `c.Show(f)` | How `str` and `println` show an object (default `app::C{}`); an object of a Kukel class derived from it shows as Kukel objects do |
| `c.Object(state)` | A new object holding `state`, for a function to return (once a program that uses the module is compiled) |
| `c.StateOf(v)` | The state of `v` if it is an object of the class, or of a Kukel class derived from it that has set it up |
| `m.Class(c)` | Adds the class to module `m` |
| `this.State()`, `this.SetState(s)` | The object's Go value |
| `this.Call(in, name, args...)` | Calls a member function on the object as `p->name(...)` does: an override in a derived Kukel class runs. A name the object has no member function for is an `*Error` (`"MemberError"`, without a trace); an exception in the call comes back as for `CallValue` |
| `this.Value()`, `v.Object()`, `this.ClassName()` | The object as a `Value` and back, and the name of its class (a derived class's own). `v.Object()` also gives the objects of Kukel classes, whose state is `nil` |

- A member function gets `this` also for an object of a Kukel class
  derived from the Go class, whose state the Go constructor has set (the
  derived constructor calls `app::Counter::Counter(...)`). Before that, Kukel
  code that calls a Go member function gets a `ValueError`, not a call with
  a nil state.
- A Go member function calls other Go functions directly; to let a derived
  class override a step, call it with `this.Call`.
- As for functions, a panic becomes a `"HostError"` exception, and the same
  rules about names, nil functions and additions after use apply (they
  panic). A class belongs to one module (adding it to a second panics), a
  `Method` or `Static` cannot have the class's name (that is `New`), and
  `c.Object` panics until a program that uses the module is compiled
  (`c.StateOf` then gives `nil, false`).
- Workers may run the member functions in parallel, each on its own
  objects.

## 4. Errors

| Error | When |
|---|---|
| `*CompileError` | An error in the source: `File`, `Line`, `Col`, `Message`; when the compile found several, the others are in `More` and `Error()` gives them all, one per line (a missing file is the `os` error) |
| `*Error` | An exception nothing in the program caught: `Kind` (`"RangeError"`, ...), `Message`, `Value` (the thrown `Error` object), `Trace` (innermost call first). Also the step limit (`"StepLimitError"`) and cancellation (`"CancelledError"`), which the program cannot catch |
| `*ExitError` | The program called `exit(code)` during a `Call`, `CallContext`, `CallValue` or `Global` (in an initializer); `Run` gives the code as its result |
| other errors (`kukel: ...`) | Mistakes of the Go program: `Call` of a name the program does not have ("has no function", also for a module's constant), or of a global variable that holds no function ("is not a function"); `Global` of a name it does not have; any call after `Close`; `New` of a program that uses a built-in or module the options do not allow, or a host module that is missing |

`(*Error).Error()` gives `Kind: Message`, followed by the trace when there
is one (`Trace` is not empty: an exception of the program, also the step
limit and a cancellation during the call), one `  at f (file:line)` line per
call; use `Kind` and `Message` for a single line. Each `Trace` entry is a
`Frame`: `Func` (the function's name, `Shape::area`), `File` and `Line`.

A Go function reports a failure by returning an error; the Kukel program
sees an exception it can catch:

- `kukel.Errorf("KeyError", "no item %q", item)` gives an exception of that
  kind (`"HostError"` if the kind is `""`, also in its `Kind` and `Error()`);
- any other error gives an exception of kind `"HostError"` with the error's
  text;
- an `*Error` from a call back into Kukel (`in.CallValue`) goes on
  unchanged, so the program can catch its own exception. Return it as it
  is: dropping it would also drop a step limit or a cancellation that ended
  the call back;
- `&kukel.ExitError{Code: 3}` ends the program as `exit(3)` does.

## 5. Code that is not trusted

Scripts from users (plug-ins, rules typed into a form) can be limited in
what they reach and how long they run. Memory is not limited: Go has one
heap per process, and an approximate count would slow every program. Run
scripts in a separate process with an operating-system limit where memory
matters.

**What a program may use** is decided when compiling (a program that uses
more does not compile, so it never runs half-way):

```go
opts := kukel.CompileOptions{
    DenyBuiltins:    kukel.SystemBuiltins,          // readfile, writefile, appendfile, readline, getenv, exit
    StandardModules: []string{"json", "strings"},   // no db, fs, p9, zmq, events, worker, re, time, uuid
    NoFileModules:   true,                          // no program files: "use" finds only Go modules, no worker::startfile
}
```

- `DenyBuiltins` lists built-in functions the program may not use; using
  one is a compile error ("built-in function readfile is not available in
  this program"). The names stay reserved.
- `StandardModules` lists the standard modules allowed (nil: all;
  `[]string{}`: none). `fs`, `db` and `p9` reach files (as do the
  built-ins of `SystemBuiltins`; `p9::disk` serves a directory, and can
  let clients change it), `zmq` and `p9` the network. A name the build does not have is an error, so a
  Go program built with build tags (`nozmq`, ...) must not list those
  modules; `kukel.StandardModules()` gives the ones it has. `worker` in particular starts interpreters and reads
  program files (`startfile`); its workers follow the same limits.
- An unknown name in either list is an error, so that a misspelt name does
  not leave something allowed.
- `NoFileModules` keeps the program from reading program files: `use`
  finds only modules written in Go, and `worker::startfile` is refused.
- `LoadProgram` takes the same options; `New` refuses a `.kkc` program that
  uses more than they allow.

**How long a call runs:**

- `Options.StepLimit` bounds every call from Go: after that many steps
  (backward jumps, calls, throws, so every loop counts) the call ends with a
  `"StepLimitError"`. Each `Run`/`Call` from Go gets the full budget again;
  calls a Go function makes back into Kukel (`in.CallValue`) share the
  budget of the call that runs it. The interpreter stays usable. The same
  program always stops at the same point. A worker gets the same limit for
  its whole run.
- `in.CallContext(ctx, ...)` stops the call when `ctx` ends (a timeout, a
  cancelled request) with a `"CancelledError"`, also for a call a Go function
  makes back into Kukel. The program stops at its next step; Go functions
  that wait (`time::sleep`, `worker` `receive` and `wait`, `events::run`)
  return at once. Workers started during the call are cancelled if `ctx`
  ends while the call runs; `in.Close()` cancels the ones that outlive it.
- Neither interrupts a single long operation of a built-in function (sorting
  a huge vector, `split` of a huge string); it is noticed when the operation
  returns.

## 6. Goroutines

- An `Interp` is used by one goroutine at a time. Make one per goroutine:
  they can share a `Program`, and do not share any state. `Close` belongs
  to that goroutine too, after the last call; to stop a call from another
  goroutine, end its context (`RunContext`, `CallContext`).
- A Go function called by Kukel runs on the goroutine of the interpreter
  that calls it, and may call back into that interpreter.
- Kukel workers run on goroutines of their own; host functions they call
  must be safe for concurrent use.
- `Value`s that refer to vectors, maps or objects belong to their
  interpreter (section 2).
