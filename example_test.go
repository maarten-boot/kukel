package kukel_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/maarten-boot/kukel"
)

// Compile a program once and call its functions from Go.
func Example() {
	prog, err := kukel.CompileSource("pricing.kkl", `
const VAT = 0.21;
price(net, qty) {
    local total = net * qty;

    if (qty >= 10) total *= 0.9;       // volume discount
    return round(total * (1 + VAT) * 100) / 100.0;
}
main() {}`, kukel.CompileOptions{})
	if err != nil {
		fmt.Println(err)
		return
	}
	in, err := kukel.New(prog, kukel.Options{})
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, qty := range []int64{1, 10} {
		p, err := in.Call("price", kukel.Float(9.99), kukel.Int(qty))
		fmt.Println(qty, p, err)
	}
	// Output:
	// 1 12.09 <nil>
	// 10 108.79 <nil>
}

// Give Kukel code functions written in Go, in a module of its own.
func ExampleNewModule() {
	stock := map[string]int64{"pen": 12, "ink": 0}
	shop := kukel.NewModule("shop").
		Const("currency", kukel.String("EUR")).
		Func("stock", func(in *kukel.Interp, args []kukel.Value) (kukel.Value, error) {
			item, ok := args[0].AsString()
			if !ok {
				return kukel.Nil, kukel.Errorf("TypeError", "an item is a string")
			}
			n, known := stock[item]
			if !known {
				return kukel.Nil, kukel.Errorf("KeyError", "no item %q", item)
			}
			return kukel.Int(n), nil
		})
	prog, err := kukel.CompileSource("shop.kkl", `use shop;
main() {
    for (local item in ["pen", "ink", "cap"]) {
        try {
            println(item, shop::stock(item) > 0 ? "in stock" : "sold out");
        } catch (e) {
            println(item, e->kind, e->message);
        }
    }
    println("prices in", shop::currency);
}`, kukel.CompileOptions{Modules: []*kukel.Module{shop}})
	if err != nil {
		fmt.Println(err)
		return
	}
	in, _ := kukel.New(prog, kukel.Options{Stdout: os.Stdout})
	in.Run()
	// Output:
	// pen in stock
	// ink sold out
	// cap KeyError no item "cap"
	// prices in EUR
}

// Run code that is not trusted: no files, no workers, a step budget and a
// time limit.
func ExampleInterp_CallContext() {
	opts := kukel.CompileOptions{
		DenyBuiltins:    kukel.SystemBuiltins,
		StandardModules: []string{"json", "strings"},
		NoFileModules:   true,
	}
	if _, err := kukel.CompileSource("evil.kkl", `main() { println(readfile("/etc/passwd")); }`, opts); err != nil {
		fmt.Println(err)
	}

	prog, _ := kukel.CompileSource("slow.kkl", `spin() { while (true) {} } main() {}`, opts)
	in, _ := kukel.New(prog, kukel.Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := in.CallContext(ctx, "spin")
	fmt.Println(strings.SplitN(err.Error(), "\n", 2)[0])
	// Output:
	// evil.kkl:1:18: built-in function readfile is not available in this program
	// CancelledError: the program was cancelled: context deadline exceeded
}

// Convert between Go and Kukel values.
func ExampleFromGo() {
	v, _ := kukel.FromGo(map[string]any{"name": "Kukel", "tags": []string{"small", "fun"}})
	fmt.Println(v, v.Get(kukel.String("tags")).Len())
	g, _ := v.ToGo()
	fmt.Printf("%v\n", g)
	// Output:
	// {"name": "Kukel", "tags": ["small", "fun"]} 2
	// map[name:Kukel tags:[small fun]]
}

// A class written in Go: Kukel code uses it and derives a class from it
// that overrides a step (changed).
func ExampleNewClass() {
	type counter struct{ n int64 }
	counterClass := kukel.NewClass("Counter").
		New(func(in *kukel.Interp, this *kukel.Object, args []kukel.Value) (kukel.Value, error) {
			this.SetState(&counter{}) // the constructor sets the state
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

	prog, err := kukel.CompileSource("main.kkl", `use app;

class Loud : app::Counter { Loud(); changed(n); }
Loud::Loud() { app::Counter::Counter(); }        // sets up the Go state
Loud::changed(n) { println("now", n); }

main() {
    local c = new app::Counter();
    c->inc()->inc();
    println(c);                                  // app::Counter(2)
    new Loud()->inc();                           // now 1
}`, kukel.CompileOptions{Modules: []*kukel.Module{app}})
	if err != nil {
		fmt.Println(err)
		return
	}
	in, err := kukel.New(prog, kukel.Options{Stdout: os.Stdout})
	if err != nil {
		fmt.Println(err)
		return
	}
	in.Run()
	// Output:
	// app::Counter(2)
	// now 1
}

// A step limit stops code that runs too long (each call from Go gets the
// whole budget), and a panic in a Go function is a HostError for Kukel.
func ExampleOptions_stepLimit() {
	app := kukel.NewModule("app").Func("crash", func(in *kukel.Interp, args []kukel.Value) (kukel.Value, error) {
		var m map[string]int
		m["x"] = 1 // panics
		return kukel.Nil, nil
	})
	prog, err := kukel.CompileSource("limits.kkl", `use app;
spin() { while (true) {} }
count(n) { local i = 0; while (i < n) i++; return i; }
crash() { try { app::crash(); } catch (e) { return e->kind; } }
main() {}`, kukel.CompileOptions{Modules: []*kukel.Module{app}})
	if err != nil {
		fmt.Println(err)
		return
	}
	in, _ := kukel.New(prog, kukel.Options{StepLimit: 10000})
	_, err = in.Call("spin")
	var e *kukel.Error
	if errors.As(err, &e) {
		fmt.Println(e.Kind + ": " + e.Message)
	}
	fmt.Println(in.Call("count", kukel.Int(5000))) // a new budget for this call
	fmt.Println(in.Call("crash"))
	// Output:
	// StepLimitError: more than 10000 steps
	// 5000 <nil>
	// HostError <nil>
}
