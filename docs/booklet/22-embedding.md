# A. Embedding Kukel in Go

[The booklet](README.md) · previous: [21. Windows](21-windows.md) · next: [B. Go in Kukel: host modules](23-host-modules.md)

kukel is written in Go, and a Go program can run Kukel code itself:
rules a user writes ("orders over 100 get 10% off"), plug-ins, scripts in
a game, configuration that computes. The Go program compiles the Kukel
code once, runs it in an *interpreter*, calls its functions, and reads
the values they give back. This appendix and the next two are for Go
programmers; the full details are in [embedding.md](../embedding.md).

## A first program

```go
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/maarten-boot/kukel"
)

// The rules, as a user might write them: in a file, a database or a form.
const rules = `
discount(total) {
    if (total >= 500) {
        return 0.15;
    }

    if (total >= 100) {
        return 0.10;
    }
    return 0;
}

describe(order) {
    return order["customer"] + ": " + str(len(order["items"])) + " items";
}

main() {
    println("rules loaded");
}
`

func main() {
	prog, err := kukel.CompileSource("rules.kkl", rules, kukel.CompileOptions{})
	if err != nil {
		log.Fatal(err)
	}
	in, err := kukel.New(prog, kukel.Options{Stdout: os.Stdout})
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	if _, err := in.Run(); err != nil { // runs main
		log.Fatal(err)
	}
	for _, total := range []int64{50, 250, 800} {
		d, err := in.Call("discount", kukel.Int(total))
		if err != nil {
			log.Fatal(err)
		}
		f, _ := d.AsFloat()
		fmt.Printf("%d: %.0f%% off\n", total, f*100)
	}

	order, err := kukel.FromGo(map[string]any{"customer": "Ann", "items": []any{"pen", "ink"}})
	if err != nil {
		log.Fatal(err)
	}
	text, err := in.Call("describe", order)
	if err != nil {
		log.Fatal(err)
	}
	s, _ := text.AsString()
	fmt.Println(s)
}
```

```
$ go run .
rules loaded
50: 0% off
250: 10% off
800: 15% off
Ann: 2 items
```

- `kukel.CompileSource(name, source, options)` compiles a program from a
  string; `kukel.CompileFiles(paths, options)` from files (with the
  modules they `use`). A mistake in the source is a `*kukel.CompileError`
  with the file, line and column.
- `kukel.New(prog, options)` makes an interpreter. `Options` give it its
  standard output, error and input (without them, output is dropped and
  input is empty) and, for code you do not trust, a step limit
  (appendix C).
- `in.Run(args...)` runs `main`; `in.Call(name, args...)` calls any
  top-level function, and gives its result. An interpreter keeps its
  global variables from call to call.
- `in.Close()` ends it, closing what the program left open.

## Values

A `kukel.Value` is any Kukel value. Simple ones are made with
`kukel.Int`, `kukel.Float`, `kukel.String`, `kukel.Bool` and `kukel.Nil`,
and read with `AsInt`, `AsFloat`, `AsString`, `AsBool` (each with an `ok`).
Bigger ones go across whole:

```go
package main

import (
	"fmt"
	"log"

	"github.com/maarten-boot/kukel"
)

func main() {
	prog, err := kukel.CompileSource("stats.kkl", `
summary(v) {
    local total = 0;

    for (local x in v) {
        total += x;
    }
    return {"count": len(v), "total": total, "mean": total / float(len(v)), "max": max(v..)};
}

main() {}
`, kukel.CompileOptions{})
	if err != nil {
		log.Fatal(err)
	}
	in, err := kukel.New(prog, kukel.Options{})
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	numbers, _ := kukel.FromGo([]int{4, 8, 15, 16, 23, 42})
	result, err := in.Call("summary", numbers)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result.Kind(), result.Len(), result.Get(kukel.String("mean")))

	plain, err := result.ToGo() // map[string]any, with int64, float64, ...
	if err != nil {
		log.Fatal(err)
	}
	m := plain.(map[string]any)
	fmt.Println(m["count"], m["total"], m["max"])
}
```

```
$ go run .
map 4 18.0
6 108 42
```

`kukel.FromGo` turns Go values (numbers, strings, slices, maps with string
keys, nested) into Kukel ones; `v.ToGo()` turns Kukel values into plain Go
ones: `int64`, `float64`, `string`, `[]any`, `map[string]any`. A vector
or map that stays a `Value` is shared with the interpreter, as in Kukel
itself (chapter 2).

## When the Kukel code fails

An exception the Kukel code does not catch comes back to Go as a
`*kukel.Error`, with its kind, message and stack trace:

```go
package main

import (
	"errors"
	"fmt"
	"log"

	"github.com/maarten-boot/kukel"
)

func main() {
	prog, err := kukel.CompileSource("calc.kkl", `
divide(a, b) {
    return a / b;
}
main() {}
`, kukel.CompileOptions{})
	if err != nil {
		log.Fatal(err)
	}
	in, err := kukel.New(prog, kukel.Options{})
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	_, err = in.Call("divide", kukel.Int(1), kukel.Int(0))
	var e *kukel.Error
	if errors.As(err, &e) {
		fmt.Println(e.Kind, "|", e.Message, "|", e.Trace[0].Func, e.Trace[0].Line)
	}

	_, err = kukel.CompileSource("bad.kkl", "main() { println(x); }", kukel.CompileOptions{})
	var ce *kukel.CompileError
	if errors.As(err, &ce) {
		fmt.Println(ce.File, ce.Line, ce.Col, ce.Message)
	}
}
```

```
$ go run .
ZeroDivisionError | integer division by zero | divide 3
bad.kkl 1 18 undefined name x
```

The interpreter stays usable after an exception: the next call works as
before.

## One program, many interpreters

A compiled program never changes, so one `*kukel.Program` can serve any
number of interpreters, also at the same time on different goroutines.
An interpreter, though, is used by one goroutine at a time: a web server
makes one per request (or keeps a pool), from the same program.

[The booklet](README.md) · previous: [21. Windows](21-windows.md) · next: [B. Go in Kukel: host modules](23-host-modules.md)
