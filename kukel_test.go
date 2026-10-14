package kukel_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maarten-boot/kukel"
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

func TestCancel(t *testing.T) {
	in := interp(t, compile(t, `use time; use worker; use zmq; use db;
slowsql() { db::open("sqlite::memory:")->first("WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM n) SELECT count(*) FROM n"); }
loop() { while (true) {} }
nap() { time::sleep(60); }
listen() { local s = new zmq::Socket("PULL"); s->bind("inproc://nobody"); s->receive(); }
waitFor() { worker::start(loop)->wait(); }
quick() { return 1; }
main() {}`, kukel.CompileOptions{}), kukel.Options{})
	for _, name := range []string{"loop", "nap", "waitFor", "listen", "slowsql"} {
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
