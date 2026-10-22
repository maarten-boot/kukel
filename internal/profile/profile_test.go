package profile_test

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	gprofile "github.com/google/pprof/profile"

	"github.com/maarten-boot/kukel/internal/builtins"
	"github.com/maarten-boot/kukel/internal/compiler"
	"github.com/maarten-boot/kukel/internal/loader"
	_ "github.com/maarten-boot/kukel/internal/modules/workermod"
	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/profile"
	"github.com/maarten-boot/kukel/internal/vm"
)

// profiled runs src (as p.kkl) profiled with a new Profiler, and gives the
// Profiler and the text report.
func profiled(t *testing.T, src string, cpu bool) (*vm.Profiler, string) {
	t.Helper()
	t.Chdir(t.TempDir())
	file := "p.kkl"
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := loader.LoadWith([]string{file}, &native.Config{})
	if err != nil {
		t.Fatal(err)
	}
	prog, err := compiler.Compile(files, builtins.Names())
	if err != nil {
		t.Fatal(err)
	}
	m, err := vm.NewWith(prog, builtins.Lookup, &native.Config{}, &bytes.Buffer{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := vm.NewProfiler(0, cpu)
	m.StartProfile(p, "main")
	if _, err := m.Run(nil); err != nil {
		t.Fatal(err)
	}
	m.StopProfile()
	var b strings.Builder
	if err := profile.Report(&b, p, nil); err != nil {
		t.Fatal(err)
	}
	return p, b.String()
}

// A program too quick to be sampled: the report says so, without an empty
// table of lines; the calls are there.
func TestReportNoSamples(t *testing.T) {
	_, r := profiled(t, "f() { return 1; }\nmain() { f(); }\n", false)
	if !strings.HasPrefix(r, "No samples: what was profiled ran for less than one interval (1ms).\n") ||
		strings.Contains(r, "Lines,") || !regexp.MustCompile(`(?m)^ +0\.000 s +- +0\.000 s +- +1  f \(.*p\.kkl:1\)$`).MatchString(r) {
		t.Errorf("report:\n%s", r)
	}
}

// The header: wall time, or running time (-cpu) without nested parentheses
// or the name of kukel's option (the Go API has none); the average
// interval.
func TestReportHeader(t *testing.T) {
	spin := "spin() { local t = clock(); while (clock() - t < 0.05) {} }\nmain() { spin(); }\n"
	header := regexp.MustCompile(`^[0-9.]+ s in [0-9]+ samples, one every [0-9.]+ ms on average \(([^()]*)\)\.\n`)
	for _, cpu := range []bool{false, true} {
		_, r := profiled(t, spin, cpu)
		m := header.FindStringSubmatch(r)
		want := "wall time, with waiting"
		if cpu {
			want = "running time: waiting left out"
		}
		if m == nil || m[1] != want {
			t.Errorf("cpu %v: report:\n%s", cpu, r)
		}
		if strings.Contains(r, "[main]") || strings.Contains(r, "By interpreter") {
			t.Errorf("cpu %v: marked with one interpreter:\n%s", cpu, r)
		}
	}
}

// A worker too quick to be sampled still makes the report name the
// interpreter of each function: its calls are there.
func TestReportMarking(t *testing.T) {
	src := `use worker;
f(n) { local t = 0; for (local i = 0; i < n; i++) t += i; return t; }
main() {
    worker::start(f, 1)->wait();
    local t = clock();
    while (clock() - t < 0.03) {}
}
`
	p, r := profiled(t, src, false)
	for _, want := range []string{"By interpreter", "1  f (", "p.kkl:2) [worker 1: f]", "p.kkl:3) [main]", "1  worker::start [main]"} {
		if !strings.Contains(r, want) {
			t.Errorf("no %q in the report:\n%s", want, r)
		}
	}
	var b bytes.Buffer
	if err := profile.Pprof(&b, p); err != nil {
		t.Fatal(err)
	}
	prof, err := gprofile.Parse(&b)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range prof.Sample {
		if root := s.Location[len(s.Location)-1].Line[0].Function.Name; root != "[main]" && root != "[worker 1: f]" {
			t.Errorf("a stack starts with %s", root)
		}
	}
	if prof.DurationNanos <= 0 || prof.DurationNanos > int64(5*time.Second) || prof.TimeNanos <= 0 {
		t.Errorf("duration %d, time %d", prof.DurationNanos, prof.TimeNanos)
	}
}

// More functions and Go functions than the report shows: the rest are
// counted ("... n more"). Recursion counts once in a function's total; a
// deep stack is profiled whole.
func TestReportLimits(t *testing.T) {
	var src strings.Builder
	for i := range profile.MaxFunctions + 5 {
		fmt.Fprintf(&src, "f%d() { return %d; }\n", i, i)
	}
	src.WriteString("down(n) { if (n == 0) { local t = clock(); while (clock() - t < 0.05) {} return 0; } return down(n - 1); }\n")
	src.WriteString("main() {\n")
	for i := range profile.MaxFunctions + 5 {
		fmt.Fprintf(&src, "    f%d();\n", i)
	}
	src.WriteString("    down(5000);\n}\n")
	p, r := profiled(t, src.String(), false)
	if !strings.Contains(r, "  ... 7 more\n") { // of the functions, with main and down
		t.Errorf("report:\n%s", r)
	}
	m := regexp.MustCompile(`(?m)^ +([0-9.]+) s +[0-9.]+% +([0-9.]+) s +[0-9.]+% +5001  down \(`).FindStringSubmatch(r)
	if m == nil {
		t.Fatalf("no down in:\n%s", r)
	}
	own, _ := strconv.ParseFloat(m[1], 64)
	total, _ := strconv.ParseFloat(m[2], 64)
	if own < 0.04 || total < own || total > 0.2 { // not 5001 times the time
		t.Errorf("down: own %.3f s, total %.3f s:\n%s", own, total, r)
	}
	deepest := 0
	for _, s := range p.Samples() {
		deepest = max(deepest, len(s.Stack))
	}
	if deepest != 5002 {
		t.Errorf("deepest stack: %d frames", deepest)
	}
}
