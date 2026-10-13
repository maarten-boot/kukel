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
- 57 built-in functions (strings, collections, math, files, ...)

The full description is in [docs/reference.md](docs/reference.md); the
grammar is in [syntax.ebnf](syntax.ebnf) (and as a
[Lark](https://github.com/lark-parser/lark) grammar in
[syntax.lark](syntax.lark)). How programs are compiled and run (the virtual
machine, every instruction, the `.kkc` format) is specified in
[docs/vm.md](docs/vm.md).

## Installing

With Go 1.22 or newer:

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
| `kukel check a.kkl ...` | compile only and report errors |
| `kukel build [-o prog.kkc] a.kkl ...` | compile to a bytecode file |
| `kukel exec prog.kkc [args]` | run a bytecode file |
| `kukel disasm prog.kkc` (or `a.kkl`) | show the bytecode |
| `kukel tokens a.kkl`, `kukel ast a.kkl` | show the tokens or the syntax tree |
| `kukel version` | show the version |

`tests/run` has example programs: FizzBuzz, a JSON parser, word counting,
closures, exceptions and more. Each comes with its expected output.

## How it works

```
source ──lexer──▶ tokens ──parser──▶ syntax tree ──compiler──▶ bytecode ──VM──▶ run
                                                                  │
                                                       .kkc file ◀┘ (build / exec)
```

| Package | Role |
|---|---|
| `internal/lexer`, `internal/parser`, `internal/ast` | source to syntax tree |
| `internal/compiler` | name resolution, the language's rules, code generation |
| `internal/bytecode` | instruction set, `.kkc` files, the verifier that checks loaded files |
| `internal/vm` | the stack machine: calls, objects, closures, exceptions (specified in [docs/vm.md](docs/vm.md)) |
| `internal/value`, `internal/builtins` | values with their operators, built-in functions |

[TODO.md](TODO.md) holds the plan the interpreter was built from, with all
design decisions (Q1–Q34). Ideas for later are in [docs/future.md](docs/future.md).

## Development

| Make target | |
|---|---|
| `make` | gofmt check, `go vet`, tests, build |
| `make fuzz FUZZTIME=1m` | fuzz the lexer, parser, compiler, bytecode loader and VM |
| `make bench` | benchmarks |
| `make golden` | rewrite the expected outputs in `tests/` after an intended change |

## License

MIT, see [LICENSE](LICENSE).
