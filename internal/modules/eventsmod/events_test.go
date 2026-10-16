package eventsmod_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/builtins"
	"github.com/maarten-boot/kukel/internal/compiler"
	"github.com/maarten-boot/kukel/internal/modules/eventsmod"
	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/parser"
	"github.com/maarten-boot/kukel/internal/value"
	"github.com/maarten-boot/kukel/internal/vm"
)

// The module "evtest" acts as an event source in another goroutine, the
// way a GUI or a socket would: evtest::spawn(n, f) holds the loop, posts
// n calls f(i) from a goroutine, then releases the loop.
func init() {
	native.Register(&native.Module{
		Name: "evtest",
		Funcs: map[string]native.Func{
			// poll(n, f): holds the loop and polls: the loop calls f(i)
			// from a poll, for i = 1..n, then releases it.
			"poll": func(env value.Env, args []value.Value) (value.Value, error) {
				a, err := native.Check("evtest::poll", args, 2, 2)
				if err != nil {
					return value.NilValue, err
				}
				n, err := a.Int(0)
				if err != nil {
					return value.NilValue, err
				}
				fn, loop, i := args[1], eventsmod.For(env), int64(0)
				release := loop.Hold()
				var remove func()
				remove = loop.Poll(func() error {
					i++
					if i == n {
						remove()
						release()
					}
					_, err := env.Call(fn, []value.Value{value.MakeInt(i)})
					return err
				}, 5*time.Millisecond)
				return value.NilValue, nil
			},
			"spawn": func(env value.Env, args []value.Value) (value.Value, error) {
				a, err := native.Check("evtest::spawn", args, 2, 2)
				if err != nil {
					return value.NilValue, err
				}
				n, err := a.Int(0)
				if err != nil {
					return value.NilValue, err
				}
				fn := args[1]
				loop := eventsmod.For(env)
				release := loop.Hold()
				go func() {
					defer release()
					for i := int64(1); i <= n; i++ {
						time.Sleep(2 * time.Millisecond)
						loop.Post(func() error {
							_, err := env.Call(fn, []value.Value{value.MakeInt(i)}) // on the interpreter's goroutine
							return err
						})
					}
				}()
				return value.NilValue, nil
			},
		},
	})
}

func run(t *testing.T, src string) string {
	t.Helper()
	f, err := parser.ParseFile("t.kkl", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	prog, err := compiler.Compile([]*ast.File{f}, builtins.Names())
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	m, err := vm.New(prog, builtins.Lookup, &out, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Run(nil); err != nil {
		t.Fatalf("%v\noutput: %s", err, out.String())
	}
	return out.String()
}

func TestEventsFromOtherGoroutines(t *testing.T) {
	got := run(t, `use events; use evtest;
main() {
    local sum = 0;
    evtest::spawn(5, fn(i) { sum += i; });
    evtest::spawn(3, fn(i) { sum += 100 * i; });
    events::run();     // waits for both sources to release the loop
    println(sum, events::pending());
}`)
	if got != "615 0\n" {
		t.Errorf("got %q", got)
	}
}

// A hold keeps run() going even without timers; stop() still ends it.
func TestHoldAndStop(t *testing.T) {
	got := run(t, `use events; use evtest;
main() {
    evtest::spawn(50, fn(i) { if (i == 3) events::stop(); });
    events::run();
    println("stopped");
}`)
	if got != "stopped\n" {
		t.Errorf("got %q", got)
	}
}

// Each interpreter has its own loop.
func TestOneLoopPerInterpreter(t *testing.T) {
	src := `use events; main() { events::after(0, println, "x"); events::run(); println(events::pending()); }`
	for i := 0; i < 2; i++ {
		if got := run(t, src); got != "x\n0\n" {
			t.Errorf("run %d: got %q", i, got)
		}
	}
}

// A poll is called regularly while events::run() runs, also next to
// timers; its exceptions go to onerror like those of handlers.
func TestPoll(t *testing.T) {
	got := run(t, `use events; use evtest;
main() {
    local seen = [];
    events::onerror(fn(e) { push(seen, "error " + e->message); });
    events::after(0.012, fn() { push(seen, "timer"); });
    evtest::poll(5, fn(i) { push(seen, i); if (i == 3) throw new Error("three"); });
    events::run();
    println(len(seen), seen[0], seen[len(seen) - 1], find(str(seen), "timer") > 0, find(str(seen), "error three") > 0);
}`)
	if got != "7 1 5 true true\n" {
		t.Errorf("got %q", got)
	}
}
