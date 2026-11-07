# B. Go in Kukel: host modules

[The booklet](README.md) · previous: [A. Embedding Kukel in Go](22-embedding.md) · next: [C. Code that is not trusted](24-untrusted.md)

Appendix A called Kukel from Go. The other way round, Kukel code calls Go:
the Go program gives it a *host module*, a module written in Go that the
Kukel code uses like any other (`use shop;`, then `shop::price("pen")`).
That is how a Go program lets scripts reach its data and its services,
and nothing else.

## Functions and constants

```go
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/maarten-boot/kukel"
)

var prices = map[string]float64{"pen": 1.5, "ink": 4.25}

func main() {
	shop := kukel.NewModule("shop").
		Const("currency", kukel.String("EUR")).
		Func("price", func(in *kukel.Interp, args []kukel.Value) (kukel.Value, error) {
			if len(args) != 1 {
				return kukel.Nil, kukel.Errorf("ArgumentError", "shop::price takes 1 argument, not %d", len(args))
			}
			name, ok := args[0].AsString()
			if !ok {
				return kukel.Nil, kukel.Errorf("TypeError", "shop::price: a product name must be a string")
			}
			p, found := prices[name]
			if !found {
				return kukel.Nil, kukel.Errorf("KeyError", "no product %q", name)
			}
			return kukel.Float(p), nil
		}).
		Func("each", func(in *kukel.Interp, args []kukel.Value) (kukel.Value, error) {
			for _, name := range []string{"ink", "pen"} { // calls a Kukel function for each product
				if _, err := in.CallValue(args[0], kukel.String(name), kukel.Float(prices[name])); err != nil {
					return kukel.Nil, err
				}
			}
			return kukel.Nil, nil
		})

	prog, err := kukel.CompileSource("order.kkl", `
use shop;

main() {
    println(shop::price("pen") * 3, shop::currency);
    shop::each(fn(name, price) { println(name, price); });

    try {
        shop::price("cake");
    } catch (e) {
        println(e->kind, e->message);
    }
}
`, kukel.CompileOptions{Modules: []*kukel.Module{shop}})
	if err != nil {
		log.Fatal(err)
	}
	in, err := kukel.New(prog, kukel.Options{Stdout: os.Stdout})
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()
	if _, err := in.Run(); err != nil {
		fmt.Println(err)
	}
}
```

```
$ go run .
4.5 EUR
ink 4.25
pen 1.5
KeyError no product "cake"
```

- `kukel.NewModule(name)` makes a module; `Func(name, f)` adds a function,
  `Const(name, value)` a constant. Give the modules to the compiler in
  `CompileOptions.Modules`: Kukel checks every name when it compiles, so
  a misspelt `shop::prise` is a compile error, as for any module.
- A function gets the interpreter that calls it and the arguments, and
  checks them itself.
- Returning `kukel.Errorf(kind, ...)` throws an exception of that kind in
  Kukel, which the script can catch. Any other Go error becomes a
  `HostError`, and so does a panic: the interpreter survives it.
- `in.CallValue(f, args...)` calls a Kukel function the script passed in: a
  callback.

## Classes written in Go

A host module can also hold classes whose objects carry a Go value, and
whose member functions are Go functions. Kukel code makes them with `new`,
and may even derive its own classes from them:

```go
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/maarten-boot/kukel"
)

type stock struct{ items map[string]int64 }

func main() {
	stockClass := kukel.NewClass("Stock").
		New(func(in *kukel.Interp, this *kukel.Object, args []kukel.Value) (kukel.Value, error) {
			this.SetState(&stock{items: map[string]int64{}})
			return kukel.Nil, nil
		}).
		Method("add", func(in *kukel.Interp, this *kukel.Object, args []kukel.Value) (kukel.Value, error) {
			name, _ := args[0].AsString()
			n, _ := args[1].AsInt()
			s := this.State().(*stock)
			s.items[name] += n
			if _, err := this.Call(in, "changed", kukel.String(name), kukel.Int(s.items[name])); err != nil {
				return kukel.Nil, err // a derived Kukel class may override changed
			}
			return this.Value(), nil
		}).
		Method("changed", func(in *kukel.Interp, this *kukel.Object, args []kukel.Value) (kukel.Value, error) {
			return kukel.Nil, nil
		}).
		Show(func(this *kukel.Object) string {
			return fmt.Sprintf("shop::Stock(%d products)", len(this.State().(*stock).items))
		})
	shop := kukel.NewModule("shop").Class(stockClass)

	prog, err := kukel.CompileSource("stock.kkl", `
use shop;

class Logged : shop::Stock {
    Logged();
    changed(name, n);
}

Logged::Logged() { // sets up the Go part
    shop::Stock::Stock();
}
Logged::changed(name, n) {
    println("now", n, name);
}

main() {
    local s = new shop::Stock();
    s->add("pen", 3)->add("ink", 1);
    println(s);
    new Logged()->add("pen", 5);
}
`, kukel.CompileOptions{Modules: []*kukel.Module{shop}})
	if err != nil {
		log.Fatal(err)
	}
	in, err := kukel.New(prog, kukel.Options{Stdout: os.Stdout})
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()
	if _, err := in.Run(); err != nil {
		log.Fatal(err)
	}
}
```

```
$ go run .
shop::Stock(2 products)
now 5 pen
```

- `kukel.NewClass(name)` makes a class: `New` is its constructor, which
  sets the object's Go value with `this.SetState`; `Method` adds member
  functions; `Show` says how the object prints. `module.Class(c)` puts it
  in a module.
- `this.State()` gives the Go value back in every member function.
- `this.Call(in, name, args...)` calls a member function the way Kukel's
  `p->name(...)` does, so an override in a derived Kukel class runs: here
  `Logged` reports every change.
- `this.Value()` is the object as a `kukel.Value`, to return it (so calls
  chain, `s->add(...)->add(...)`).

A member function `iter()` that returns `in.Generator(...)` makes the
objects walkable with `for`; [embedding.md](../embedding.md#2-values) shows
one.

## Rules of thumb

- Keep host modules small and specific: each function is a door from the
  script into the Go program.
- The arguments are valid during the call only; keep a copy
  (`v.ToGo()`) of what is needed later.
- Kukel workers (chapter 17) may call host functions from their own
  goroutines: make such functions safe for concurrent use.

[The booklet](README.md) · previous: [A. Embedding Kukel in Go](22-embedding.md) · next: [C. Code that is not trusted](24-untrusted.md)
