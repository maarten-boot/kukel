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
  modules written in Go: `db` (SQL with SQLite, PostgreSQL and MySQL, and
  objects in tables), `events` (an event loop with timers), `fs`
  (directories and files), `json`, `p9` (the 9P file protocol: a client,
  and a server of trees the program builds and of disk directories), `re`
  (regular expressions), `strings` (a string builder), `time`, `tk`
  (desktop windows with widgets, menus, dialogs and a canvas; in a kukel
  built with the tag `tk`), `uuid`, `worker` (interpreters that run in
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
make install    # install kukel into $GOBIN, else $(go env GOPATH)/bin
```

`kukel` holds everything (about 23 MB). Build tags leave parts out for a
smaller binary: `nopostgres`, `nomysql`, `nosqlite` (all three: no module
`db`), `nozmq` and `no9p`. With all five it is about 7 MB; with SQLite as
the only database about 15 MB:

```
go install -tags nopostgres,nomysql github.com/maarten-boot/kukel/cmd/kukel@latest
make small                    # bin/kukel-small, all five tags
make small TAGS=nozmq         # other tags
```

The module `tk` (desktop windows, see [examples/notes.kkl](examples/notes.kkl))
is the other way round: it is only in a kukel built with the tag `tk`, as
it adds about 19 MB (stripped, as the release archives are; 25 MB with
`go install`). It needs no C compiler and no Tcl/Tk on the system
(Tcl/Tk 9 come with it); its windows need a display:

```
go install -tags tk github.com/maarten-boot/kukel/cmd/kukel@latest
make tk                       # bin/kukel-tk
```

`kukel version` lists the modules (and databases) a binary has.

Each release on GitHub also has ready-made archives of `kukel`, and of
`kukel-tk` (with `tk`), for Linux, macOS and Windows (amd64 and arm64),
with `SHA256SUMS`.

### Docker

Each release is also a Docker image for linux/amd64 and linux/arm64,
`ghcr.io/maarten-boot/kukel` (tagged with the version, and `latest`). It
holds only `kukel` (on a distroless base, with CA certificates and time
zones); programs come in through a volume mounted at `/work`, the working
directory:

```
docker run --rm -v "$PWD:/work" ghcr.io/maarten-boot/kukel run prog.kkl -- args
docker run --rm -it -v "$PWD:/work" ghcr.io/maarten-boot/kukel repl
docker run --rm -p 5640:5640 -v "$PWD:/work" ghcr.io/maarten-boot/kukel run server.kkl
```

`kukel` runs as a user without privileges (uid 65532); to let a program
write files into the mounted directory, run it as yourself: `--user
"$(id -u):$(id -g)"`. A server inside (9P, ZeroMQ) listens on `tcp://*:port`
and is reached through `-p`. `make docker` builds the image for this
machine as `kukel:dev` (`IMAGE=...` names it otherwise).

## Using it

| Command | What it does |
|---|---|
| `kukel run a.kkl b.kkl [-- args]` | compile the files as one program and run it |
| `kukel a.kkl [args]` | the same as `kukel run a.kkl -- args`: a script that starts with `#!/usr/bin/env kukel` runs as a command |
| `kukel check a.kkl ...` | compile only and report the errors |
| `kukel build [-o prog.kkc] a.kkl ...` | compile to a bytecode file |
| `kukel build -module [-o m.kkc] m.kkl` | compile a module on its own; `use m;` finds `m.kkc` when there is no `m.kkl` |
| `kukel exec prog.kkc [--] [args]` | run a bytecode file |
| `kukel disasm prog.kkc` (or `a.kkl ...`) | show the bytecode |
| `kukel fmt [-w \| -l] a.kkl ...` | format source files (indentation, spacing, a blank line before `if`, `for`, ...) |
| `kukel debug a.kkl ... [-- args]` | run a program under the debugger (breakpoints, stepping, values) |
| `kukel repl [a.kkl ...]` | read declarations and statements and run them as they come |
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
| `internal/native`, `internal/modules/...` | modules written in Go (`db`, `events`, `fs`, `json`, `p9`, `re`, `strings`, `time`, `tk`, `uuid`, `worker`, `zmq`) and native classes |
| `internal/compiler` | name resolution, the language's rules, code generation |
| `internal/bytecode` | instruction set, `.kkc` files, the verifier that checks loaded files |
| `internal/vm` | the stack machine: calls, objects, closures, exceptions (specified in [docs/vm.md](docs/vm.md)) |
| `internal/value`, `internal/builtins` | values with their operators, built-in functions |
| `examples/` | demo programs, such as a `grep`, and a Go program that embeds kukel ([examples/README.md](examples/README.md)) |

[TODO.md](TODO.md) holds the plan the interpreter was built from, with all
design decisions (Q1–Q74). Ideas for later are in [docs/future.md](docs/future.md).

## Development

| Make target | |
|---|---|
| `make` | gofmt check, `go vet`, tests, build (each also on its own: `make vet`, `make test`, `make build`) |
| `make install` | install `kukel` into `$GOBIN`, else `$(go env GOPATH)/bin` |
| `make fmt`, `make clean` | `gofmt -w` the sources; remove `bin/` and `dist/`, and the cached test results |
| `make test-all` | everything: vet, the tests of the full, the smallest and the `tk` build, and the tests with the race detector |
| `make small`, `make test-small` | build `bin/kukel-small`, or run the tests, without the optional parts (`TAGS=...` picks other tags for `small`; `make vet` also vets the smallest build) |
| `make tk`, `make test-tk` | build `bin/kukel-tk`, or run the tests, with the module `tk` (`make vet` also vets it). Its tests need a display or Xvfb (else they skip), and click, type and drag with xdotool when it is there |
| `make fuzz FUZZTIME=1m` | fuzz the lexer, parser, compiler, bytecode loader and verifier (what they accept runs in the VM with a step limit), `json::decode`, time parsing and `kukel fmt` (each for 30s by default) |
| `make bench` | benchmarks |
| `make golden` | rewrite the expected outputs in `tests/` after an intended change |
| `make docker` | the Docker image for this machine, `kukel:dev` (`IMAGE=...` for another name) |
| `make dist VERSION=v1.2.3` | the archives of a release in `dist/`: `kukel` and `kukel-tk` for Linux, macOS and Windows on amd64 and arm64 (`PLATFORMS=...` picks others), with `SHA256SUMS` |

**Releases**: pushing a tag `v1.2.3` runs `.github/workflows/release.yml`,
which first runs the whole CI (`ci.yml`); when that passes, the archives
of `make dist` are published as the GitHub release of the tag, with notes
made from the commits since the previous tag. A release that already
exists (made by hand) gets the archives added, replacing files of the same
name. At the same time the Docker image is built for linux/amd64 and
linux/arm64 and pushed to `ghcr.io` as `:v1.2.3`, and as `:latest` when the
tag is the newest release (a tag with a `-`, such as `v1.3.0-rc1`, never
is). The base images and BuildKit are pinned to digests; Dependabot
proposes updates of the base images and of the actions (BuildKit, named in
a script of the workflow, is updated by hand).

The tests of `db` also run on database servers when `KUKEL_TEST_POSTGRES`
and `KUKEL_TEST_MYSQL` name one (a URL as `db::open` takes it; the tables
`item`, `tag`, `plain`, `customer`, `order`, `line` and `doc` there are dropped:
use a database for tests only), for example with Docker:

```
docker run -d --rm --name pg -e POSTGRES_PASSWORD=pw -p 5432:5432 postgres:16
docker run -d --rm --name maria -e MARIADB_ROOT_PASSWORD=pw -e MARIADB_DATABASE=test -p 3306:3306 mariadb:11
KUKEL_TEST_POSTGRES="postgres://postgres:pw@127.0.0.1:5432/postgres?sslmode=disable" \
KUKEL_TEST_MYSQL="mysql://root:pw@127.0.0.1:3306/test" go test ./internal/modules/dbmod
```

## License

MIT, see [LICENSE](LICENSE).
