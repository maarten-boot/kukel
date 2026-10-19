// Command embed is an example of a Go program that runs Kukel code (see
// docs/embedding.md): it prices orders, and the discount of each order comes
// from rules that users write as a Kukel script.
//
//	go run ./examples/embed examples/embed/rules.kkl examples/embed/orders.json
//
// The script has a function discount(order), which gives the percentage off
// (0 to 50). It gets the order as a map, and a host module, shop, with what
// the rules may ask the Go program:
//
//	shop::customer(name)  the customer as a map: "name", "since" (a year),
//	                      "vip"; a KeyError for a customer the shop does not
//	                      know
//	shop::note(text)      adds text to the notes printed with the order
//	shop::year            the year now (a constant)
//
// The script is not trusted: it may not read or write files or end the
// program (kukel.SystemBuiltins), may use only the modules json and strings,
// and each order gets a step limit and a time limit. A rule that fails, runs
// too long or gives a wrong discount fails only its order.
//
// The exit code is 0, 1 when an order failed, 2 when the rules or the
// orders cannot be loaded.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"

	"github.com/maarten-boot/kukel"
)

// The limits of each call of discount.
const (
	stepLimit = 100_000
	timeLimit = time.Second
	maxOff    = 50
)

type order struct {
	ID       string `json:"id"`
	Customer string `json:"customer"`
	Items    []item `json:"items"`
}

type item struct {
	SKU   string  `json:"sku"`
	Qty   int64   `json:"qty"`
	Price float64 `json:"price"`
}

type customer struct {
	since int64
	vip   bool
}

// The shop's customers; a real program would look them up in a database.
var customers = map[string]customer{
	"ann": {since: 2024},
	"bob": {since: 2019, vip: true},
	"cy":  {since: 2020},
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 {
		fmt.Fprintln(stderr, "usage: embed rules.kkl orders.json")
		return 2
	}
	e, err := load(args[0], stderr)
	if err != nil {
		fmt.Fprintln(stderr, err) // a CompileError gives each error as file:line:col: message
		return 2
	}
	defer e.in.Close()
	data, err := os.ReadFile(args[1])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	var orders []order
	if err := json.Unmarshal(data, &orders); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", args[1], err)
		return 2
	}
	code := 0
	for _, o := range orders {
		gross := 0.0
		for _, it := range o.Items {
			gross += float64(it.Qty) * it.Price
		}
		off, notes, err := e.discount(o)
		if err != nil {
			fmt.Fprintf(stdout, "%-4s %-5s %8.2f  failed: %v\n", o.ID, o.Customer, gross, err)
			code = 1
			continue
		}
		net := math.Round(gross*(100-off)) / 100
		line := fmt.Sprintf("%-4s %-5s %8.2f %3.0f%% %8.2f  %s", o.ID, o.Customer, gross, off, net, strings.Join(notes, ", "))
		fmt.Fprintln(stdout, strings.TrimRight(line, " "))
	}
	return code
}

// engine runs the rules: one interpreter for all orders, so the script's
// global variables last from one order to the next.
type engine struct {
	in    *kukel.Interp
	notes []string // shop::note's, for the order being priced
}

// load compiles the rules in file, with the module shop and the limits for
// code that is not trusted. What the rules print goes to stderr, so that it
// does not mix with the prices.
func load(file string, stderr io.Writer) (*engine, error) {
	e := &engine{}
	shop := kukel.NewModule("shop").
		Const("year", kukel.Int(2026)).
		Func("customer", func(in *kukel.Interp, args []kukel.Value) (kukel.Value, error) {
			if len(args) != 1 {
				return kukel.Nil, kukel.Errorf("TypeError", "customer(name) takes 1 argument, not %d", len(args))
			}
			name, ok := args[0].AsString()
			if !ok {
				return kukel.Nil, kukel.Errorf("TypeError", "a customer's name is a string, not %s", args[0].Kind())
			}
			c, known := customers[name]
			if !known {
				return kukel.Nil, kukel.Errorf("KeyError", "no customer %q", name)
			}
			return kukel.MapOf(map[string]kukel.Value{
				"name": kukel.String(name), "since": kukel.Int(c.since), "vip": kukel.Bool(c.vip),
			}), nil
		}).
		Func("note", func(in *kukel.Interp, args []kukel.Value) (kukel.Value, error) {
			for _, a := range args {
				e.notes = append(e.notes, a.String())
			}
			return kukel.Nil, nil
		})
	prog, err := kukel.CompileFiles([]string{file}, kukel.CompileOptions{
		Modules:         []*kukel.Module{shop},
		DenyBuiltins:    kukel.SystemBuiltins,
		StandardModules: []string{"json", "strings"},
		NoFileModules:   true,
	})
	if err != nil {
		return nil, err
	}
	e.in, err = kukel.New(prog, kukel.Options{Stdout: stderr, Stderr: stderr, StepLimit: stepLimit})
	if err != nil {
		return nil, err
	}
	return e, nil
}

// discount calls the rules' discount(order), and gives the percentage off
// and the notes the rules added.
func (e *engine) discount(o order) (float64, []string, error) {
	items := make([]any, len(o.Items))
	for i, it := range o.Items {
		items[i] = map[string]any{"sku": it.SKU, "qty": it.Qty, "price": it.Price}
	}
	v, err := kukel.FromGo(map[string]any{"id": o.ID, "customer": o.Customer, "items": items})
	if err != nil {
		return 0, nil, err
	}
	e.notes = nil
	ctx, cancel := context.WithTimeout(context.Background(), timeLimit)
	defer cancel()
	r, err := e.in.CallContext(ctx, "discount", v)
	if err != nil {
		return 0, nil, explain(err)
	}
	off, ok := r.AsFloat()
	if !ok {
		return 0, nil, fmt.Errorf("the rules gave a %s, not a number", r.Kind())
	}
	if off < 0 || off > maxOff {
		return 0, nil, fmt.Errorf("the rules gave %s off, not 0 to %d", r, maxOff)
	}
	return off, e.notes, nil
}

// explain gives an error of the rules in one line.
func explain(err error) error {
	var ke *kukel.Error
	if !errors.As(err, &ke) {
		return err // "kukel: ... has no function discount", ...
	}
	switch ke.Kind {
	case "StepLimitError":
		return fmt.Errorf("the rules took more than %d steps", stepLimit)
	case "CancelledError":
		return fmt.Errorf("the rules took more than %v", timeLimit)
	}
	if len(ke.Trace) > 0 {
		f := ke.Trace[0]
		return fmt.Errorf("%s: %s (%s:%d in %s)", ke.Kind, ke.Message, f.File, f.Line, f.Func)
	}
	return fmt.Errorf("%s: %s", ke.Kind, ke.Message)
}
