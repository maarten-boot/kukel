package kukel_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/pprof/profile"

	"github.com/maarten-boot/kukel"
	"github.com/maarten-boot/kukel/internal/modules/dbmod"
)

func compile(t *testing.T, src string, opts kukel.CompileOptions) *kukel.Program {
	t.Helper()
	p, err := kukel.CompileSource("t.kkl", src, opts)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func interp(t *testing.T, p *kukel.Program, opts kukel.Options) *kukel.Interp {
	t.Helper()
	in, err := kukel.New(p, opts)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func TestCallAndRun(t *testing.T) {
	p := compile(t, `
global calls = 0, greeting = "hi";
add(a, b) { calls++; return a + b; }
sum(v) { local s; s = 0; for (local x in v) s += x; return s; }
main(args) { println(greeting, args); return len(args); }`, kukel.CompileOptions{})
	var out bytes.Buffer
	in := interp(t, p, kukel.Options{Stdout: &out})

	if r, err := in.Call("add", kukel.Int(2), kukel.Float(0.5)); err != nil || r.String() != "2.5" {
		t.Errorf("add: %v, %v", r, err)
	}
	in.Call("add", kukel.String("a"), kukel.String("b"))
	if c, _ := in.Global("calls"); c.String() != "2" {
		t.Errorf("globals are kept between calls: calls = %v", c)
	}
	if r, err := in.Call("sum", kukel.Vector(kukel.Int(1), kukel.Int(2), kukel.Int(3))); err != nil || r.String() != "6" {
		t.Errorf("sum: %v, %v", r, err)
	}
	if code, err := in.Run("x", "y"); err != nil || code != 2 || out.String() != "hi [\"x\", \"y\"]\n" {
		t.Errorf("Run: %d, %v, %q", code, err, out.String())
	}
	if _, err := in.Call("nosuch"); err == nil || err.Error() != "kukel: the program has no function nosuch" {
		t.Errorf("unknown function: %v", err)
	}
	if _, err := in.Call("calls"); err == nil || err.Error() != "kukel: calls is not a function" {
		t.Errorf("not a function: %v", err)
	}
}

func TestHostModule(t *testing.T) {
	var logged []string
	app := kukel.NewModule("app").
		Const("version", kukel.String("1.4")).
		Func("log", func(in *kukel.Interp, args []kukel.Value) (kukel.Value, error) {
			logged = append(logged, args[0].String())
			return kukel.Nil, nil
		}).
		Func("each", func(in *kukel.Interp, args []kukel.Value) (kukel.Value, error) {
			for i := 0; i < args[0].Len(); i++ { // a callback into Kukel
				if _, err := in.CallValue(args[1], args[0].Index(i)); err != nil {
					return kukel.Nil, err
				}
			}
			return kukel.Nil, nil
		}).
		Func("fail", func(in *kukel.Interp, args []kukel.Value) (kukel.Value, error) {
			if args[0].Truthy() {
				return kukel.Nil, kukel.Errorf("ValueError", "bad input %v", args[0])
			}
			return kukel.Nil, errors.New("disk full")
		})
	p := compile(t, `use app;
const V = "v" + app::version;
main() {
    app::log(V);
    app::each([1, 2], fn(x) { app::log(x * 10); });
    try { app::fail(true); } catch (e) { app::log(e->kind + ": " + e->message); }
    try { app::fail(false); } catch (e) { app::log(e->kind + ": " + e->message); }
    try { app::each([1], fn(x) { throw new Error("inner", "KeyError"); }); } catch (e) { app::log(e->kind); }
}`, kukel.CompileOptions{Modules: []*kukel.Module{app}})
	if _, err := interp(t, p, kukel.Options{}).Run(); err != nil {
		t.Fatal(err)
	}
	want := []string{"v1.4", "10", "20", "ValueError: bad input true", "HostError: disk full", "KeyError"}
	if !reflect.DeepEqual(logged, want) {
		t.Errorf("got %q, want %q", logged, want)
	}
}

func TestErrors(t *testing.T) {
	_, err := kukel.CompileSource("bad.kkl", "main() {\n  x = 1;\n}", kukel.CompileOptions{})
	var ce *kukel.CompileError
	if !errors.As(err, &ce) || ce.File != "bad.kkl" || ce.Line != 2 || ce.Col != 3 {
		t.Errorf("compile error: %#v", err)
	}

	in := interp(t, compile(t, `
f(v) { return v[5]; }
g() { local v = [1, 2]; throw v; }
h() { exit(4); }
main() {}`, kukel.CompileOptions{}), kukel.Options{})
	_, err = in.Call("f", kukel.Vector())
	var ke *kukel.Error
	if !errors.As(err, &ke) || ke.Kind != "RangeError" || len(ke.Trace) != 1 || ke.Trace[0].Func != "f" || ke.Trace[0].Line != 2 {
		t.Errorf("exception: %#v", err)
	}
	if _, err = in.Call("g"); !errors.As(err, &ke) || ke.Kind != "TypeError" || !strings.Contains(ke.Message, "only Error objects can be thrown") {
		t.Errorf("a thrown vector: %#v", err)
	}
	var xe *kukel.ExitError
	if _, err = in.Call("h"); !errors.As(err, &xe) || xe.Code != 4 {
		t.Errorf("exit: %#v", err)
	}
}

func TestAllowLists(t *testing.T) {
	deny := kukel.CompileOptions{DenyBuiltins: kukel.SystemBuiltins, StandardModules: []string{"json"}}
	for _, tc := range []struct{ src, msg string }{
		{`main() { readfile("/etc/passwd"); }`, "t.kkl:1:10: built-in function readfile is not available in this program"},
		{`main() { local f = getenv; }`, "built-in function getenv is not available"},
		{`use worker; main() {}`, "t.kkl:1:5: module worker is not available in this program"},
		{`use util; main() {}`, "module util not found"},
	} {
		_, err := kukel.CompileSource("t.kkl", tc.src, deny)
		if err == nil || !strings.Contains(err.Error(), tc.msg) {
			t.Errorf("%s: got %v, want %q", tc.src, err, tc.msg)
		}
	}
	if _, err := kukel.CompileSource("t.kkl", `use json; main() { println(json::encode([1])); }`, deny); err != nil {
		t.Errorf("an allowed module: %v", err)
	}
	// Built-ins are still reserved names when denied.
	if _, err := kukel.CompileSource("t.kkl", `readfile() {} main() {}`, deny); err == nil {
		t.Error("a denied built-in's name could be redefined")
	}

	// A program compiled without limits is refused when loaded with them.
	free := compile(t, `use worker; main() { readfile("x"); }`, kukel.CompileOptions{})
	data, err := free.Encode()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := kukel.LoadProgram(data, deny)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kukel.New(loaded, kukel.Options{}); err == nil || !strings.Contains(err.Error(), "not available in this program") {
		t.Errorf("New: %v", err)
	}

	// NoFileModules: no file is read, even when it exists.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "util.kkl"), []byte("f() {}"), 0o644)
	main := filepath.Join(dir, "main.kkl")
	os.WriteFile(main, []byte("use util; main() { util::f(); }"), 0o644)
	if _, err := kukel.CompileFiles([]string{main}, kukel.CompileOptions{}); err != nil {
		t.Errorf("with file modules: %v", err)
	}
	if _, err := kukel.CompileFiles([]string{main}, kukel.CompileOptions{NoFileModules: true}); err == nil || !strings.Contains(err.Error(), "only use modules written in Go") {
		t.Errorf("NoFileModules: %v", err)
	}
	// CompileSource looks only in Path.
	if _, err := kukel.CompileSource("s.kkl", "use util; main() {}", kukel.CompileOptions{Path: []string{dir}}); err != nil {
		t.Errorf("Path: %v", err)
	}
}

func TestStepLimitPerCall(t *testing.T) {
	in := interp(t, compile(t, `
spin(n) { for (local i = 0; i < n; i++) {} return n; }
forever() { try { while (true) {} } catch (e) { return "caught"; } }
main() {}`, kukel.CompileOptions{}), kukel.Options{StepLimit: 1000})
	for i := 0; i < 3; i++ { // each call has its own budget
		if r, err := in.Call("spin", kukel.Int(500)); err != nil || r.String() != "500" {
			t.Fatalf("call %d: %v, %v", i, r, err)
		}
	}
	var ke *kukel.Error
	if _, err := in.Call("forever"); !errors.As(err, &ke) || ke.Kind != "StepLimitError" {
		t.Errorf("forever: %v", err)
	}
	if _, err := in.Call("spin", kukel.Int(10)); err != nil {
		t.Errorf("after the limit: %v", err)
	}
}

var cancelRuns int

func TestCancel(t *testing.T) {
	uses, defs := "use time; use worker;\n", `
loop() { while (true) {} }
nap() { time::sleep(60); }
waitFor() { worker::start(loop)->wait(); }
quick() { return 1; }
main() {}
`
	names := []string{"loop", "nap", "waitFor"}
	if slices.Contains(kukel.StandardModules(), "zmq") { // not when built with the tag nozmq
		uses += "use zmq;\n"
		// An address of its own for each run: the cancelled socket is not
		// closed, so a second run (-count) would find the address in use.
		cancelRuns++
		defs += fmt.Sprintf(`listen() { local s = new zmq::Socket("PULL"); s->bind("inproc://nobody%d"); s->receive(); }`, cancelRuns) + "\n"
		// A send that waits for a peer, and a connect that retries.
		defs += fmt.Sprintf(`pushing() { local s = new zmq::Socket("PUSH"); s->bind("inproc://nopeer%d"); s->send("x"); }`, cancelRuns) + "\n"
		defs += `dial() { new zmq::Socket("REQ")->connect("tcp://127.0.0.1:1"); }` + "\n"
		names = append(names, "listen", "pushing", "dial")
	}
	if slices.Contains(kukel.StandardModules(), "p9") { // not when built with the tag no9p
		// A server that takes the connection and never answers: dial
		// waits for its 9P answer (go test -race: the dial that goes on
		// after the cancellation does not touch the client).
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				defer c.Close()
			}
		}()
		uses += "use p9;\n"
		defs += `silent9p() { p9::dial("tcp://` + ln.Addr().String() + `"); }` + "\n"
		names = append(names, "silent9p")
	}
	if dbmod.Has("SQLite") { // not when built with the tag nosqlite
		uses += "use db;\n"
		defs += `slowsql() { db::open("sqlite::memory:")->first("WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM n) SELECT count(*) FROM n"); }` + "\n"
		names = append(names, "slowsql")
	}
	in := interp(t, compile(t, uses+defs, kukel.CompileOptions{}), kukel.Options{})
	for _, name := range names {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		start := time.Now()
		_, err := in.CallContext(ctx, name)
		cancel()
		var ke *kukel.Error
		if !errors.As(err, &ke) || ke.Kind != "CancelledError" || !strings.Contains(ke.Message, "deadline exceeded") {
			t.Errorf("%s: %v", name, err)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("%s: took %v", name, d)
		}
	}
	if r, err := in.Call("quick"); err != nil || r.String() != "1" {
		t.Errorf("after cancelling: %v, %v", r, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := in.CallContext(ctx, "quick"); err == nil {
		t.Error("a call with an ended context ran")
	}
}

// One Program, several interpreters at once, each with its own globals.
func TestParallelInterps(t *testing.T) {
	p := compile(t, `global n = 0; bump(k) { for (local i = 0; i < k; i++) n++; return n; } main() {}`, kukel.CompileOptions{})
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			in, err := kukel.New(p, kukel.Options{})
			if err != nil {
				t.Error(err)
				return
			}
			for i := 1; i <= 10; i++ {
				if r, err := in.Call("bump", kukel.Int(100)); err != nil || r.String() != kukel.Int(int64(i*100)).String() {
					t.Errorf("bump: %v, %v", r, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestValues(t *testing.T) {
	v, err := kukel.FromGo(map[string]any{"b": []int{1, 2}, "a": 1.5, "c": nil, "d": true, "e": uint8(7)})
	if err != nil {
		t.Fatal(err)
	}
	if v.String() != `{"a": 1.5, "b": [1, 2], "c": nil, "d": true, "e": 7}` || v.Kind() != "map" || v.Len() != 5 {
		t.Errorf("FromGo: %s", v)
	}
	g, err := v.ToGo()
	want := map[string]any{"a": 1.5, "b": []any{int64(1), int64(2)}, "c": nil, "d": true, "e": int64(7)}
	if err != nil || !reflect.DeepEqual(g, want) {
		t.Errorf("ToGo: %#v, %v", g, err)
	}
	if x := v.Get(kukel.String("b")).Index(1); x.String() != "2" {
		t.Errorf("Get/Index: %v", x)
	}
	if n, ok := kukel.Int(3).AsFloat(); !ok || n != 3 {
		t.Error("AsFloat of an int")
	}
	if _, ok := kukel.String("3").AsInt(); ok {
		t.Error("AsInt of a string")
	}
	vec := kukel.Vector()
	vec.Push(vec)
	if _, err := vec.ToGo(); err == nil {
		t.Error("ToGo of a vector that contains itself")
	}
	if _, err := kukel.FromGo(struct{}{}); err == nil {
		t.Error("FromGo of a struct")
	}
	m := kukel.NewMap()
	if err := m.SetKey(kukel.Vector(), kukel.Nil); err == nil {
		t.Error("a vector as a map key")
	}
}

func TestModuleRules(t *testing.T) {
	mustPanic := func(what string, f func()) {
		defer func() {
			if recover() == nil {
				t.Errorf("%s: no panic", what)
			}
		}()
		f()
	}
	fn := func(in *kukel.Interp, args []kukel.Value) (kukel.Value, error) { return kukel.Nil, nil }
	mustPanic("a module name that is a keyword", func() { kukel.NewModule("while") })
	mustPanic("a function name with a dash", func() { kukel.NewModule("m").Func("a-b", fn) })
	mustPanic("a name defined twice", func() { kukel.NewModule("m").Func("f", fn).Const("f", kukel.Int(1)) })
	mustPanic("a vector constant", func() { kukel.NewModule("m").Const("v", kukel.Vector()) })
	m := kukel.NewModule("m").Func("f", fn)
	kukel.CompileSource("t.kkl", "use m; main() { m::f(); }", kukel.CompileOptions{Modules: []*kukel.Module{m}})
	mustPanic("adding after use", func() { m.Func("g", fn) })
}

// Fixes from the cross-check: each of these left the interpreter broken or
// let something through before.
func TestCrossCheckFixes(t *testing.T) {
	boom := kukel.NewModule("boom").
		Func("panic", func(in *kukel.Interp, args []kukel.Value) (kukel.Value, error) { panic("oops") }).
		Func("nested", func(in *kukel.Interp, args []kukel.Value) (kukel.Value, error) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := in.CallContext(ctx, "quick") // an ended context counts also inside a Func
			return kukel.Nil, err
		})
	p := compile(t, `use boom; use time; use worker;
spin(n) { for (local i = 0; i < n; i++) {} return n; }
quick() { return 1; }
crash() { boom::panic(); }
nested() { boom::nested(); }
sleeper() { time::sleep(10); }
startSleeper() { worker::start(sleeper); return "started"; }
main() {}`, kukel.CompileOptions{Modules: []*kukel.Module{boom}})

	in := interp(t, p, kukel.Options{StepLimit: 100})
	var ke *kukel.Error
	// A panicking Func is a HostError; the interpreter stays usable.
	if _, err := in.Call("crash"); !errors.As(err, &ke) || ke.Kind != "HostError" || !strings.Contains(ke.Message, "boom::panic panicked: oops") {
		t.Errorf("crash: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := in.Call("spin", kukel.Int(60)); err != nil {
			t.Errorf("spin after a panic: %v", err)
		}
	}
	if _, err := in.Call("nested"); !errors.As(err, &ke) || ke.Kind != "CancelledError" {
		t.Errorf("nested CallContext: %v", err)
	}

	// Close cancels workers that are still running.
	in2 := interp(t, p, kukel.Options{})
	if r, err := in2.Call("startSleeper"); err != nil || r.String() != "started" {
		t.Fatalf("startSleeper: %v %v", r, err)
	}
	in2.Close()
	if _, err := in2.Call("quick"); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Errorf("a call after Close: %v", err)
	}

	// A failed initializer fails every call, not only the first.
	bad := interp(t, compile(t, `global g = h(); h() { throw new Error("bad init"); } f() { return g; } main() {}`, kukel.CompileOptions{}), kukel.Options{})
	for i := 0; i < 2; i++ {
		if _, err := bad.Call("f"); err == nil || !strings.Contains(err.Error(), "bad init") {
			t.Errorf("call %d after a failed init: %v", i, err)
		}
	}

	// A Go module's function the program does not call itself.
	js := interp(t, compile(t, `use json; main() {}`, kukel.CompileOptions{}), kukel.Options{})
	if r, err := js.Call("json::encode", kukel.Vector(kukel.Int(1))); err != nil || r.String() != "[1]" {
		t.Errorf("json::encode: %v, %v", r, err)
	}

	// Misspelt allow-list names are errors.
	if _, err := kukel.CompileSource("t.kkl", "main() {}", kukel.CompileOptions{DenyBuiltins: []string{"readfiel"}}); err == nil || !strings.Contains(err.Error(), "no built-in function readfiel") {
		t.Errorf("DenyBuiltins typo: %v", err)
	}
	if _, err := kukel.CompileSource("t.kkl", "main() {}", kukel.CompileOptions{StandardModules: []string{"jsn"}}); err == nil || !strings.Contains(err.Error(), "no standard module jsn") {
		t.Errorf("StandardModules typo: %v", err)
	}

	// NoFileModules also refuses worker::startfile.
	w := interp(t, compile(t, `use worker; f(p) { worker::startfile(p); } main() {}`, kukel.CompileOptions{NoFileModules: true}), kukel.Options{})
	if _, err := w.Call("f", kukel.String("x.kkl")); !errors.As(err, &ke) || !strings.Contains(ke.Message, "may not read program files") {
		t.Errorf("startfile with NoFileModules: %v", err)
	}

	if v, _ := kukel.FromGo([]byte("ab")); v.Kind() != "string" || v.String() != "ab" {
		t.Errorf("FromGo([]byte): %v %s", v, v.Kind())
	}
}

// Fixes from the third cross-check.
func TestCrossCheck3(t *testing.T) {
	// A step limit during the initializers does not break the interpreter:
	// the next call runs them again.
	p := compile(t, `global g = spin(); spin() { for (local i = 0; i < 50; i++) {} return 1; } f() { return g; } main() {}`, kukel.CompileOptions{})
	in := interp(t, p, kukel.Options{StepLimit: 30})
	var ke *kukel.Error
	if _, err := in.Call("f"); !errors.As(err, &ke) || ke.Kind != "StepLimitError" {
		t.Fatalf("first call: %v", err)
	}
	in2 := interp(t, p, kukel.Options{StepLimit: 1000})
	if r, err := in2.Call("f"); err != nil || r.String() != "1" {
		t.Errorf("with a budget that fits: %v, %v", r, err)
	}

	// An Error with an empty kind gets the class name.
	q := interp(t, compile(t, `class Oops : Error {} f() { local e = new Oops("m"); e->kind = ""; throw e; } main() {}`, kukel.CompileOptions{}), kukel.Options{})
	if _, err := q.Call("f"); !errors.As(err, &ke) || ke.Kind != "Oops" || err.Error()[:7] != "Oops: m" {
		t.Errorf("empty kind: %v", err)
	}

	// Named string and bool types.
	type S string
	type B bool
	if v, err := kukel.FromGo(map[string]any{"s": S("x"), "b": B(true), "a": [2]int{1, 2}}); err != nil || v.String() != `{"a": [1, 2], "b": true, "s": "x"}` {
		t.Errorf("FromGo: %v, %v", v, err)
	}
	// Keys 1 and "1" give the same Go key.
	m := kukel.NewMap()
	m.SetKey(kukel.Int(1), kukel.String("int"))
	m.SetKey(kukel.String("1"), kukel.String("str"))
	if _, err := m.ToGo(); err == nil || !strings.Contains(err.Error(), "give the same Go key") {
		t.Errorf("ToGo of clashing keys: %v", err)
	}
}

func TestSeveralCompileErrors(t *testing.T) {
	_, err := kukel.CompileSource("t.kkl", "f() { a; }\ng() { b; }\nmain() {}", kukel.CompileOptions{})
	var ce *kukel.CompileError
	if !errors.As(err, &ce) || ce.Line != 1 || len(ce.More) != 1 || ce.More[0].Line != 2 || strings.Count(err.Error(), "\n") != 1 {
		t.Errorf("got %#v", err)
	}
}

// A class written in Go, used and derived from in Kukel.
func TestHostClass(t *testing.T) {
	type counter struct{ n int64 }
	counterClass := kukel.NewClass("Counter").
		New(func(in *kukel.Interp, this *kukel.Object, args []kukel.Value) (kukel.Value, error) {
			start := int64(0)
			if len(args) > 0 {
				var ok bool
				if start, ok = args[0].AsInt(); !ok {
					return kukel.Nil, kukel.Errorf("TypeError", "Counter: the start must be an int")
				}
			}
			this.SetState(&counter{n: start})
			return kukel.Nil, nil
		}).
		Method("inc", func(in *kukel.Interp, this *kukel.Object, args []kukel.Value) (kukel.Value, error) {
			c := this.State().(*counter)
			c.n++
			// Tell the object: a derived Kukel class may override changed.
			if _, err := this.Call(in, "changed", kukel.Int(c.n)); err != nil {
				return kukel.Nil, err
			}
			return this.Value(), nil
		}).
		Method("changed", func(in *kukel.Interp, this *kukel.Object, args []kukel.Value) (kukel.Value, error) {
			return kukel.Nil, nil
		}).
		Method("value", func(in *kukel.Interp, this *kukel.Object, args []kukel.Value) (kukel.Value, error) {
			return kukel.Int(this.State().(*counter).n), nil
		}).
		Static("zero", func(in *kukel.Interp, args []kukel.Value) (kukel.Value, error) {
			return kukel.Int(0), nil
		}).
		Show(func(this *kukel.Object) string {
			return fmt.Sprintf("app::Counter(%d)", this.State().(*counter).n)
		})
	var app *kukel.Module
	app = kukel.NewModule("app").
		Class(counterClass).
		Func("make", func(in *kukel.Interp, args []kukel.Value) (kukel.Value, error) {
			return counterClass.Object(&counter{n: 100}), nil
		}).
		Func("total", func(in *kukel.Interp, args []kukel.Value) (kukel.Value, error) {
			sum := int64(0)
			for _, a := range args {
				s, ok := counterClass.StateOf(a)
				if !ok {
					return kukel.Nil, kukel.Errorf("TypeError", "app::total: not a counter: %v", a)
				}
				sum += s.(*counter).n
			}
			return kukel.Int(sum), nil
		})
	var out bytes.Buffer
	p := compile(t, `use app;
class Loud : app::Counter { log; Loud(start); changed(n); }
Loud::Loud(start) { app::Counter::Counter(start); log = []; }
Loud::changed(n) { push(log, n); }

class Lazy : app::Counter { Lazy(); }
Lazy::Lazy() { }

main() {
    local c = new app::Counter(5);
    c->inc()->inc();
    local l = new Loud(10);
    l->inc()->inc();
    local m = app::make();
    println(c, c->value(), l->value(), l->log, l, m, app::Counter::zero(), Loud::zero());
    println(app::total(c, l, m), instanceof(l, app::Counter));
    try { new Lazy()->inc(); } catch (e) { println(e->kind + ":", e->message); }
    try { app::total(new Lazy()); } catch (e) { println(e->kind + ":", e->message); }
    try { new app::Counter("x"); } catch (e) { println(e->kind + ":", e->message); }
}`, kukel.CompileOptions{Modules: []*kukel.Module{app}})
	if _, err := interp(t, p, kukel.Options{Stdout: &out}).Run(); err != nil {
		t.Fatal(err)
	}
	want := `app::Counter(7) 7 12 [11, 12] Loud{log: [11, 12]} app::Counter(100) 0 0
119 true
ValueError: app::Counter::inc: the app::Counter part of this Lazy object is not set up: its constructor must call app::Counter::Counter(...)
TypeError: app::total: not a counter: Lazy{}
TypeError: Counter: the start must be an int
`
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}
	mustPanic := func(what string, f func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s: no panic", what)
			}
		}()
		f()
	}
	f := func(*kukel.Interp, *kukel.Object, []kukel.Value) (kukel.Value, error) { return kukel.Nil, nil }
	mustPanic("a method named like the class", func() { kukel.NewClass("C").Method("C", f) })
	mustPanic("a nil method", func() { kukel.NewClass("C").Method("m", nil) })
	mustPanic("a member defined twice", func() { kukel.NewClass("C").Method("m", f).Method("m", f) })
	mustPanic("a class name that is not a name", func() { kukel.NewClass("a-b") })
	mustPanic("a class added after use", func() { app.Class(kukel.NewClass("Late")) })
	mustPanic("a class in two modules", func() { kukel.NewModule("other").Class(counterClass) })
}

// Workers use the same host classes in parallel.
func TestHostClassWorkers(t *testing.T) {
	type box struct{ n int64 }
	boxClass := kukel.NewClass("Box").
		New(func(in *kukel.Interp, this *kukel.Object, args []kukel.Value) (kukel.Value, error) {
			this.SetState(&box{})
			return kukel.Nil, nil
		}).
		Method("add", func(in *kukel.Interp, this *kukel.Object, args []kukel.Value) (kukel.Value, error) {
			b := this.State().(*box)
			n, _ := args[0].AsInt()
			b.n += n
			return kukel.Int(b.n), nil
		})
	app := kukel.NewModule("app").Class(boxClass)
	var out bytes.Buffer
	p := compile(t, `use app;
use worker;
class Tally : app::Box { Tally(); }
Tally::Tally() { app::Box::Box(); }
count(n) {
    local b = new Tally();
    local r = 0;
    for (local i = 0; i < n; i++) r = b->add(1);
    return r;
}
main() {
    local ws = [];
    for (local i = 0; i < 4; i++) push(ws, worker::start(count, 2000));
    local total = 0;
    for (local w in ws) total += w->wait();
    println(total);
}`, kukel.CompileOptions{Modules: []*kukel.Module{app}})
	if _, err := interp(t, p, kukel.Options{Stdout: &out}).Run(); err != nil {
		t.Fatal(err)
	}
	if out.String() != "8000\n" {
		t.Errorf("got %q", out.String())
	}
}

// A transaction begun in one call with a context lives on after it: the
// commit in a later call works (cross-check 5).
func TestTransactionAcrossCalls(t *testing.T) {
	if !dbmod.Has("SQLite") {
		t.Skip("built without SQLite (tag nosqlite)")
	}
	in := interp(t, compile(t, `use db;
global conn = db::open("sqlite::memory:");
setup() { conn->exec("CREATE TABLE t (a)"); conn->begin(); conn->exec("INSERT INTO t VALUES (1)"); }
finish() { conn->commit(); return conn->first("SELECT COUNT(*) AS n FROM t")["n"]; }
main() {}`, kukel.CompileOptions{}), kukel.Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := in.CallContext(ctx, "setup"); err != nil {
		t.Fatal(err)
	}
	if n, err := in.CallContext(ctx, "finish"); err != nil || n.String() != "1" {
		t.Errorf("commit after the call that began: %v, %v", n, err)
	}
}

// A worker's onmessage handler set in one call with a context, and the
// event loop run in a later one: the handler's goroutine does not read
// the interpreter's canceller, which each call changes (go test -race;
// cross-check 10).
func TestWorkerOnmessageAcrossCalls(t *testing.T) {
	in := interp(t, compile(t, `use worker; use events;
global w, got = [];
echo() { for (local m = worker::receive(); m != nil; m = worker::receive()) worker::send(m); }
setup() {
    w = worker::start(echo);
    w->onmessage(fn(m) { push(got, m); if (len(got) == 3) w->stop(); });
    for (local i = 1; i <= 3; i++) w->send(i);
}
loop() { events::run(); return got; }
main() {}`, kukel.CompileOptions{}), kukel.Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := in.CallContext(ctx, "setup"); err != nil {
		t.Fatal(err)
	}
	if got, err := in.CallContext(ctx, "loop"); err != nil || got.String() != "[1, 2, 3]" {
		t.Errorf("got %v, %v", got, err)
	}
}

// The Go version go.mod requires is the one the README promises and the
// one CI tests as the oldest: a dependency update must not raise it
// unnoticed (go get does that when a new version needs a newer Go).
func TestGoVersion(t *testing.T) {
	mod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	var minor string
	for _, line := range strings.Split(string(mod), "\n") {
		if v, ok := strings.CutPrefix(line, "go "); ok {
			parts := strings.Split(v, ".")
			if len(parts) < 2 {
				t.Fatalf("go.mod: %q", line)
			}
			minor = parts[0] + "." + parts[1]
		}
	}
	readme, _ := os.ReadFile("README.md")
	ci, _ := os.ReadFile(".github/workflows/ci.yml")
	if !strings.Contains(string(readme), "With Go "+minor+" or newer") {
		t.Errorf("go.mod requires Go %s, but README.md does not say \"With Go %s or newer\"", minor, minor)
	}
	if !strings.Contains(string(ci), `"`+minor+`.x"`) {
		t.Errorf("go.mod requires Go %s, but the CI matrix does not test \"%s.x\"", minor, minor)
	}
}

// BenchmarkCallFib27 runs fib(27) through the Go API: its interpreters can
// be cancelled, and must still get the VM's fast call path (TODO.md Q71).
func BenchmarkCallFib27(b *testing.B) {
	p, err := kukel.CompileSource("b.kkl", `fib(n) { if (n < 2) return n; return fib(n - 1) + fib(n - 2); } main() {}`, kukel.CompileOptions{})
	if err != nil {
		b.Fatal(err)
	}
	in, err := kukel.New(p, kukel.Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer in.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := in.Call("fib", kukel.Int(27)); err != nil {
			b.Fatal(err)
		}
	}
}

// Errorf with no kind is a HostError, also in its text.
func TestErrorfNoKind(t *testing.T) {
	if e := kukel.Errorf("", "plain %d", 1); e.Kind != "HostError" || e.Error() != "HostError: plain 1" {
		t.Errorf("kind %q, text %q", e.Kind, e.Error())
	}
}

// A Profile: the calls of an interpreter from Go are profiled (not the time
// between them), waiting in a Go function counts on the line that calls
// it, calls are counted, and interpreters can share a Profile.
func TestProfile(t *testing.T) {
	app := kukel.NewModule("app").Func("wait", func(in *kukel.Interp, args []kukel.Value) (kukel.Value, error) {
		time.Sleep(60 * time.Millisecond)
		return kukel.Nil, nil
	})
	prog, err := kukel.CompileSource("rules.kkl", `use app;
step(n) {
    local t = 0;
    for (local i = 0; i < n; i++) t += i;
    app::wait();
    return t;
}
main() {}`, kukel.CompileOptions{Modules: []*kukel.Module{app}})
	if err != nil {
		t.Fatal(err)
	}
	p := kukel.NewProfile(kukel.ProfileOptions{})
	in, err := kukel.New(prog, kukel.Options{Profile: p})
	if err != nil {
		t.Fatal(err)
	}
	in2, _ := kukel.New(prog, kukel.Options{Profile: p})
	for range 2 {
		if _, err := in.Call("step", kukel.Int(1000)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(150 * time.Millisecond) // between the calls: not counted
	}
	if _, err := in2.Call("step", kukel.Int(10)); err != nil {
		t.Fatal(err)
	}
	var report strings.Builder
	if err := p.WriteReport(&report); err != nil {
		t.Fatal(err)
	}
	r := report.String()
	for _, want := range []string{"2  step (rules.kkl:2) [main]", "1  step (rules.kkl:2) [interp 2]", "2  app::wait [main]", "rules.kkl:5  app::wait();"} {
		if !strings.Contains(r, want) {
			t.Errorf("no %q in the report:\n%s", want, r)
		}
	}
	// About 3 waits of 60 ms, not the 300 ms between the calls.
	var total float64
	fmt.Sscanf(r, "%f s", &total)
	if total < 0.15 || total > 0.30 {
		t.Errorf("%.3f s in all:\n%s", total, r)
	}
	var pp bytes.Buffer
	if err := p.WritePprof(&pp); err != nil {
		t.Fatal(err)
	}
	if prof, err := profile.Parse(&pp); err != nil || len(prof.Sample) == 0 {
		t.Errorf("pprof: %v, %v", err, prof)
	}
}

// Calls shorter than the interval: the profiling goes on from call to
// call, so that many short calls are profiled as a long one would be.
func TestProfileShortCalls(t *testing.T) {
	prog := compile(t, "work(n) { local t = 0; for (local i = 0; i < n; i++) t += i % 7; return t; }\nmain() {}", kukel.CompileOptions{})
	p := kukel.NewProfile(kukel.ProfileOptions{Interval: 5 * time.Millisecond})
	in, err := kukel.New(prog, kukel.Options{Profile: p})
	if err != nil {
		t.Fatal(err)
	}
	var took time.Duration
	for range 2000 {
		start := time.Now()
		if _, err := in.Call("work", kukel.Int(2000)); err != nil {
			t.Fatal(err)
		}
		took += time.Since(start)
		if took > 2*time.Second {
			break // a slow machine
		}
	}
	var report strings.Builder
	if err := p.WriteReport(&report); err != nil {
		t.Fatal(err)
	}
	var total float64
	fmt.Sscanf(report.String(), "%f s", &total)
	if total < took.Seconds()/2 || total > took.Seconds()+0.01 {
		t.Errorf("%.3f s in all, the calls took %.3f s:\n%s", total, took.Seconds(), report.String())
	}
}

// Two programs with a file of the same name share a Profile: their lines
// stay apart, each with its own source, named by their interpreters.
func TestProfileSameFileName(t *testing.T) {
	p := kukel.NewProfile(kukel.ProfileOptions{})
	for _, src := range []string{
		"spin(n) { local t = 0; for (local i = 0; i < n; i++) t += i % 7; return t; } // the first\nmain() {}",
		"spin(n) { local t = 1; for (local i = 0; i < n; i++) t += i % 5; return t; } // the second\nmain() {}",
	} {
		prog := compile(t, src, kukel.CompileOptions{})
		in, err := kukel.New(prog, kukel.Options{Profile: p})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := in.Call("spin", kukel.Int(3_000_000)); err != nil {
			t.Fatal(err)
		}
	}
	var report strings.Builder
	if err := p.WriteReport(&report); err != nil {
		t.Fatal(err)
	}
	r := report.String()
	for _, want := range []string{"t.kkl:1 [main]", "// the first", "t.kkl:1 [interp 2]", "// the second"} {
		if !strings.Contains(r, want) {
			t.Errorf("no %q in the report:\n%s", want, r)
		}
	}
}

// A report while a worker runs: its time shows, its calls up to its latest
// sample, and the report says so.
func TestProfileWorkerRunning(t *testing.T) {
	prog := compile(t, `use worker;
spin(s) { local t = clock(); while (clock() - t < s) {} return 1; }
begin() { worker::start(spin, 2); }
main() {}`, kukel.CompileOptions{})
	p := kukel.NewProfile(kukel.ProfileOptions{})
	in, err := kukel.New(prog, kukel.Options{Profile: p})
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close() // (cancels the worker)
	if _, err := in.Call("begin"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	var report strings.Builder
	if err := p.WriteReport(&report); err != nil {
		t.Fatal(err)
	}
	r := report.String()
	for _, want := range []string{"worker 1: spin still runs: its calls are counted up to its latest sample.", "1  begin (t.kkl:3) [main]",
		"1  spin (t.kkl:2) [worker 1: spin]"} {
		if !strings.Contains(r, want) {
			t.Errorf("no %q in the report:\n%s", want, r)
		}
	}
}
