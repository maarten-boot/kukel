package vm_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/builtins"
	"github.com/maarten-boot/kukel/internal/compiler"
	"github.com/maarten-boot/kukel/internal/parser"
	"github.com/maarten-boot/kukel/internal/vm"
)

// A profile: where the time went (the stack of each sample, with lines)
// and how often each function was called.
func TestProfile(t *testing.T) {
	src := `spin(n) { local t = clock(); while (clock() - t < n) {} }
fib(n) { if (n < 2) return n; return fib(n - 1) + fib(n - 2); }
main() {
    spin(0.2);
    fib(20);
}
`
	f, err := parser.ParseFile("p.kkl", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	prog, err := compiler.Compile([]*ast.File{f}, builtins.Names())
	if err != nil {
		t.Fatal(err)
	}
	m, err := vm.New(prog, builtins.Lookup, &bytes.Buffer{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := vm.NewProfiler(time.Millisecond, false)
	m.StartProfile(p, "main")
	start := time.Now()
	if _, err := m.Run(nil); err != nil {
		t.Fatal(err)
	}
	m.StopProfile()
	took := time.Since(start)

	samples, total := p.Samples(), p.Summary().Total
	if total < 150*time.Millisecond || total > took {
		t.Errorf("total %v, the run took %v", total, took)
	}
	var inSpin time.Duration
	for _, s := range samples {
		if s.Interp != "main" || len(s.Stack) == 0 || s.Stack[0].Func.FuncName != "main" {
			t.Errorf("a sample: %+v", s)
			continue
		}
		top := s.Stack[len(s.Stack)-1]
		if top.Func.FuncName == "spin" {
			if len(s.Stack) != 2 || s.Stack[0].Line != 4 || top.Line != 1 {
				t.Errorf("a sample in spin: %+v", s.Stack)
			}
			inSpin += s.Time
		}
	}
	if inSpin < 150*time.Millisecond {
		t.Errorf("%v in spin, of %v", inSpin, total)
	}
	byName := map[string]int64{}
	for k, n := range p.Calls() {
		if k.Interp != "main" {
			t.Errorf("calls in %q", k.Interp)
		}
		if k.Func != nil {
			byName[k.Func.FuncName] = n
		} else {
			byName["go "+k.Go] = n
		}
	}
	if byName["main"] != 1 || byName["spin"] != 1 || byName["fib"] != 21891 || byName["go clock"] < 2 {
		t.Errorf("calls: %v", byName)
	}

	// Not profiled: nothing is recorded, and Profiler is nil.
	m2, _ := vm.New(prog, builtins.Lookup, &bytes.Buffer{}, nil)
	if _, err := m2.Run(nil); err != nil || m2.Profiler() != nil {
		t.Errorf("without profiling: %v, %v", err, m2.Profiler())
	}
}

// The cost of profiling: the same programs, profiled and not.
func benchmarkProfiled(b *testing.B, src string, on bool) {
	f, err := parser.ParseFile("b.kkl", []byte(src))
	if err != nil {
		b.Fatal(err)
	}
	prog, err := compiler.Compile([]*ast.File{f}, builtins.Names())
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m, _ := vm.New(prog, builtins.Lookup, &bytes.Buffer{}, nil)
		if on {
			m.StartProfile(vm.NewProfiler(time.Millisecond, false), "main")
		}
		if _, err := m.Run(nil); err != nil {
			b.Fatal(err)
		}
		m.StopProfile()
	}
}

const (
	profFib  = `fib(n) { if (n < 2) return n; return fib(n - 1) + fib(n - 2); } main() { fib(25); }`
	profLoop = `main() { local v = [], t = 0; for (local i = 0; i < 200000; i++) push(v, i % 1000); for (local i = 0; i < len(v); i++) if (v[i] > 500) t += v[i]; }`
)

func BenchmarkProfileFibOff(b *testing.B)  { benchmarkProfiled(b, profFib, false) }
func BenchmarkProfileFibOn(b *testing.B)   { benchmarkProfiled(b, profFib, true) }
func BenchmarkProfileLoopOff(b *testing.B) { benchmarkProfiled(b, profLoop, false) }
func BenchmarkProfileLoopOn(b *testing.B)  { benchmarkProfiled(b, profLoop, true) }
