# kukel

A simple programming language, just for fun: **Kukel**, with C-like syntax,
classes, closures and exceptions, compiled to bytecode for a small stack
machine written in Go.

```
// shapes.kkl
class Shape { name; Shape(name); area(); describe(); }
Shape::Shape(name) { this->name = name; }
Shape::describe() { return name + ": " + str(area()); }

class Circle : Shape { r; Circle(r); area(); }
Circle::Circle(r) { Shape::Shape("circle"); this->r = r; }
Circle::area() { return 3.14159 * r * r; }

class Square : Shape { side; Square(side); area(); }
Square::Square(side) { Shape::Shape("square"); this->side = side; }
Square::area() { return side * side; }

main(args) {
    local shapes = [new Circle(1), new Square(3), new Circle(2)];
    sort(shapes, fn(a, b) { return a->area() < b->area(); });
    for (local s in shapes) println(s->describe());

    try {
        println(shapes[5]);
    } catch (e) {
        println("caught", e->kind + ":", e->message);
    }
}
```

```
$ kukel run shapes.kkl
circle: 3.14159
square: 9
circle: 12.56636
caught RangeError: index 5 out of range for vector of length 3
```

## What is in the language

- ints (64-bit, wrapping), floats, strings (bytes), booleans, `nil`
- vectors and maps (insertion order) with literals, indexing and for-each
- functions with default and rest parameters, spread arguments (`f(v..)`)
- function literals (`fn (x) { ... }`) that capture variables by reference
- classes with single inheritance, constructors, static members, virtual
  member functions and bound methods (`p->method`)
- `try`/`catch`/`finally`, `throw`, and runtime errors as `Error` objects
- `switch` with fall-through, labelled `break`/`continue`
- modules: `use geometry;` loads `geometry.kkl`, reached as `geometry::name`;
  modules written in Go: `db` (SQL with SQLite), `events` (an event loop
  with timers), `json`,
  `strings` (a string builder), `time`, `worker` (interpreters that run in
  parallel and exchange messages) and `zmq` (ZeroMQ sockets, to talk with
  other programs and machines)
- 55 built-in functions (strings, collections, math, files, ...)

The full description is in [docs/reference.md](docs/reference.md); the
grammar is in [syntax.ebnf](syntax.ebnf) (and as a
[Lark](https://github.com/lark-parser/lark) grammar in
[syntax.lark](syntax.lark)). How programs are compiled and run (the virtual
machine, every instruction, the `.kkc` format) is specified in
[docs/vm.md](docs/vm.md).

## Installing

With Go 1.25 or newer:

```
go install github.com/maarten-boot/kukel/cmd/kukel@latest
```

or from a clone:

```
make            # vet, test and build bin/kukel
make install    # install kukel into $(go env GOPATH)/bin
```

## Using it

| Command | What it does |
|---|---|
| `kukel run a.kkl b.kkl [-- args]` | compile the files as one program and run it |
| `kukel check a.kkl ...` | compile only and report the first error |
| `kukel build [-o prog.kkc] a.kkl ...` | compile to a bytecode file |
| `kukel exec prog.kkc [--] [args]` | run a bytecode file |
| `kukel disasm prog.kkc` (or `a.kkl ...`) | show the bytecode |
| `kukel tokens a.kkl ...`, `kukel ast [-pos] a.kkl ...` | show the tokens or the syntax tree |
| `kukel version` | show the version and the modules written in Go |
| `kukel help` | show the commands |

`tests/run` has example programs: FizzBuzz, a JSON parser, word counting,
closures, exceptions and more. Each comes with its expected output.

## Embedding in Go

Go programs can run Kukel code, call its functions and give it Go functions
of their own:

```go
import "github.com/maarten-boot/kukel"

app := kukel.NewModule("app").Func("notify",
    func(in *kukel.Interp, args []kukel.Value) (kukel.Value, error) {
        log.Println(args[0])
        return kukel.Nil, nil
    })
prog, err := kukel.CompileFiles([]string{"rules.kkl"}, kukel.CompileOptions{
    Modules:      []*kukel.Module{app},   // rules.kkl: use app; ... app::notify(x);
    DenyBuiltins: kukel.SystemBuiltins,   // no files, no environment, no exit
})
in, err := kukel.New(prog, kukel.Options{StepLimit: 1_000_000})
result, err := in.CallContext(ctx, "discount", kukel.Int(250))
```

See [docs/embedding.md](docs/embedding.md) and the package documentation.

## How it works

```
source ──lexer──▶ tokens ──parser──▶ syntax tree ──compiler──▶ bytecode ──VM──▶ run
                                                                  │
                                                       .kkc file ◀┘ (build / exec)
```

| Package | Role |
|---|---|
| `kukel` (the repository root) | the Go API for embedding ([docs/embedding.md](docs/embedding.md)) |
| `internal/token`, `internal/diag` | token kinds and source positions; the error type all stages share |
| `internal/lexer`, `internal/parser`, `internal/ast` | source to syntax tree |
| `internal/loader` | finds and reads the modules a program uses |
| `internal/native`, `internal/modules/...` | modules written in Go (`db`, `events`, `json`, `strings`, `time`, `worker`, `zmq`) and native classes |
| `internal/compiler` | name resolution, the language's rules, code generation |
| `internal/bytecode` | instruction set, `.kkc` files, the verifier that checks loaded files |
| `internal/vm` | the stack machine: calls, objects, closures, exceptions (specified in [docs/vm.md](docs/vm.md)) |
| `internal/value`, `internal/builtins` | values with their operators, built-in functions |

[TODO.md](TODO.md) holds the plan the interpreter was built from, with all
design decisions (Q1–Q44). Ideas for later are in [docs/future.md](docs/future.md).

## Development

| Make target | |
|---|---|
| `make` | gofmt check, `go vet`, tests, build |
| `make install` | install `kukel` into `$(go env GOPATH)/bin` |
| `make fuzz FUZZTIME=1m` | fuzz the lexer, parser, compiler, bytecode loader and VM, `json::decode` and time parsing |
| `make bench` | benchmarks |
| `make golden` | rewrite the expected outputs in `tests/` after an intended change |

## License

MIT, see [LICENSE](LICENSE).
