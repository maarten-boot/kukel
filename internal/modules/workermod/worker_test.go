package workermod_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/builtins"
	"github.com/maarten-boot/kukel/internal/bytecode"
	"github.com/maarten-boot/kukel/internal/compiler"
	_ "github.com/maarten-boot/kukel/internal/modules/timemod"
	_ "github.com/maarten-boot/kukel/internal/modules/workermod"
	"github.com/maarten-boot/kukel/internal/parser"
	"github.com/maarten-boot/kukel/internal/vm"
)

func compile(t *testing.T, src string) *bytecode.Program {
	t.Helper()
	f, err := parser.ParseFile("t.kkl", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	prog, err := compiler.Compile([]*ast.File{f}, builtins.Names())
	if err != nil {
		t.Fatal(err)
	}
	return prog
}

// run runs src and gives its output (standard output, then standard error)
// and exit code.
func run(t *testing.T, src string, stepLimit int) (string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	m, err := vm.New(compile(t, src), builtins.Lookup, &out, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	m.SetStderr(&errOut)
	m.StepLimit = stepLimit
	code, err := m.Run(nil)
	if err != nil {
		t.Fatalf("%v\noutput: %s", err, out.String())
	}
	return out.String() + errOut.String(), code
}

func TestManyWorkersInParallel(t *testing.T) {
	out, _ := run(t, `use worker;
global calls = 0;
sum(from, to) {
    local s;
    calls++;
    s = 0;
    for (local i = from; i < to; i++) s += i;
    worker::send(calls);          // each worker has its own globals
    return s;
}
main() {
    local ws = [], total = 0;
    for (local i = 0; i < 20; i++) push(ws, worker::start(sum, i * 1000, (i + 1) * 1000));
    for (local w in ws) { if (w->receive() != 1) println("shared global!"); total += w->wait(); }
    println(total, calls);
}`, 0)
	if out != "199990000 0\n" {
		t.Errorf("got %q", out)
	}
}

func TestMessagesBothWays(t *testing.T) {
	out, _ := run(t, `use worker; use events;
pingpong() {
    for (local m = worker::receive(); m != nil; m = worker::receive()) {
        m["n"]++;
        worker::send(m);
    }
}
listener() {
    worker::onmessage(fn(m) { worker::send(len(m)); });
    events::run();
}
main() {
    local w = worker::start(pingpong), msg = {"n": 0, "keys": {1: 1.5}};
    for (local i = 0; i < 100; i++) { w->send(msg); msg = w->receive(); }
    w->stop();
    println(msg, w->receive(0.01), w->wait());

    local l = worker::start(listener), got = [];
    l->onmessage(fn(n) { push(got, n); if (len(got) == 3) l->stop(); });
    l->send("a"); l->send("bb"); l->send([1, 2, 3]);
    events::run();
    println(got, l->wait());
}`, 0)
	if want := "{\"n\": 100, \"keys\": {1: 1.5}} nil nil\n[1, 2, 3] nil\n"; out != want {
		t.Errorf("got %q, want %q", out, want)
	}
}

func TestReceiveTimeout(t *testing.T) {
	start := time.Now()
	out, _ := run(t, `use worker;
idle() { worker::receive(); }
main() { local w = worker::start(idle); println(w->receive(0.05)); w->stop(); w->wait(); }`, 0)
	if out != "nil\n" || time.Since(start) < 50*time.Millisecond {
		t.Errorf("got %q after %v", out, time.Since(start))
	}
}

// A timeout beyond what a Go duration holds (about 292 years) is no
// limit, not one that has passed.
func TestReceiveLongTimeout(t *testing.T) {
	out, _ := run(t, `use worker; use time;
later() { time::sleep(0.05); worker::send("late"); println(worker::receive(1e10)); }
main() {
    local w = worker::start(later);
    println(w->receive(1e300));
    time::sleep(0.05);
    w->send("later");
    w->wait();
}`, 0)
	if out != "late\nlater\n" {
		t.Errorf("got %q", out)
	}
}

func TestWorkerEnds(t *testing.T) {
	out, _ := run(t, `use worker;
quits() { exit(7); }
loops() { while (true) {} }
main() {
    println(worker::start(quits)->wait());     // exit ends only the worker
    try { worker::start(loops)->wait(); } catch (e) { println(e->kind, e->message); }
    println("main goes on");
}`, 10000)
	want := "7\nWorkerError worker loops failed: StepLimitError: more than 10000 steps\nmain goes on\n"
	if out != want {
		t.Errorf("got %q, want %q", out, want)
	}
}

// A worker still running when main returns cannot write any more.
func TestOutputClosedWhenMainEnds(t *testing.T) {
	out, _ := run(t, `use worker; use time;
late() { time::sleep(0.05); println("too late"); }
main() { worker::start(late); println("main done"); }`, 0)
	time.Sleep(100 * time.Millisecond)
	if out != "main done\n" {
		t.Errorf("got %q", out)
	}
}

func TestStartFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "child.kkl")
	os.WriteFile(src, []byte(`use worker; use helper;
main(args) { worker::send(helper::twice(args[0])); return len(args); }`), 0o644)
	os.WriteFile(filepath.Join(dir, "helper.kkl"), []byte(`twice(x) { return x * 2; }`), 0o644)
	os.WriteFile(filepath.Join(dir, "broken.kkl"), []byte(`main() { x = 1; }`), 0o644)
	kkc := filepath.Join(dir, "child.kkc")
	data, err := bytecode.Encode(compile(t, `use worker; main(args) { worker::send(args); }`))
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(kkc, data, 0o644)
	os.WriteFile(filepath.Join(dir, "bad.kkc"), []byte("KUKL junk"), 0o644)

	out, _ := run(t, `use worker;
main() {
    local w = worker::startfile("`+src+`", 21, "x");
    println(w->receive(), w->wait());
    w = worker::startfile("`+kkc+`", [1], {"a": nil});
    println(w->receive(), w->wait());
    local bad = ["`+filepath.Join(dir, "broken.kkl")+`", "`+filepath.Join(dir, "bad.kkc")+`", "`+filepath.Join(dir, "none.kkl")+`"];
    for (local b in bad) try { worker::startfile(b); } catch (e) { println(e->kind); }
}`, 0)
	if want := "42 2\n[[1], {\"a\": nil}] nil\nCompileError\nValueError\nIOError\n"; out != want {
		t.Errorf("got %q, want %q", out, want)
	}
}

// Fixes from the third cross-check.
func TestWorkerChecks3(t *testing.T) {
	out, _ := run(t, `use worker;
quiet() { throw new Error(nil, "K"); }
gives() { return [1]; }
idle() { worker::receive(); }
main() {
    try { worker::start(quiet)->wait(); } catch (e) { println(e->message); }   // no ": " after the kind
    local g = worker::start(gives), a = g->wait();
    push(a, 2);
    println(g->wait());                                                         // a new copy each time
    local w = worker::start(idle);
    try { w->onmessage(5); } catch (e) { println(e->kind, e->message); }
    w->stop();
    w->wait();
}`, 0)
	want := "worker quiet failed: K\n[1]\nTypeError worker::Worker::onmessage: argument 1 must be a function, not int\n"
	if out != want {
		t.Errorf("got %q, want %q", out, want)
	}
}

// Messages of cross-check 10: files without Go's names of the operations,
// objects by their class.
func TestWorkerMessages10(t *testing.T) {
	t.Chdir(t.TempDir())
	os.Mkdir("dir.kkc", 0o777)
	out, _ := run(t, `use worker;
class P { x; }
object() { return new P(); }
main() {
    for (local f in [fn() { worker::startfile("nope.kkl"); }, fn() { worker::startfile("nope.kkc"); }, fn() { worker::startfile("dir.kkc"); },
                     fn() { worker::start(object)->wait(); }, fn() { worker::start(new P()); }, fn() { worker::start(object, [new P()]); }])
        try { f(); } catch (e) { println(e->kind, e->message); }
}`, 0)
	want := `IOError worker::startfile: nope.kkl: no such file or directory
IOError worker::startfile: nope.kkc: no such file or directory
IOError worker::startfile: dir.kkc: is a directory
WorkerError worker object failed: TypeError: its result, P, cannot go back to the parent (only nil, booleans, numbers, strings, vectors and maps)
TypeError worker::start: argument 1 must be a top-level or static member function, not P (a closure, bound method or built-in cannot run in another interpreter)
TypeError worker::start: cannot send P to another interpreter (only nil, booleans, numbers, strings, vectors and maps)
`
	if out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
}
