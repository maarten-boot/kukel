package main

import (
	"bytes"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/pprof/profile"
)

const profiled = `use time;
use worker;

fib(n) {
    if (n < 2) return n;
    return fib(n - 1) + fib(n - 2);
}

work() {
    local t = 0;
    for (local i = 0; i < 2000000; i++) t += i % 7;
    return t;
}

main() {
    local w = worker::start(work);
    println(fib(18));
    time::sleep(0.2);
    println(w->wait());
}
`

// lineTime reads the own time of a source line from a report.
func lineTime(t *testing.T, report, line string) float64 {
	t.Helper()
	m := regexp.MustCompile(`(?m)^ +([0-9.]+) s +[0-9.]+%  \S+:\d+  ` + regexp.QuoteMeta(line) + `$`).FindStringSubmatch(report)
	if m == nil {
		t.Fatalf("no line %q in the report:\n%s", line, report)
	}
	v, _ := strconv.ParseFloat(m[1], 64)
	return v
}

// kukel profile: waiting counts on the line that waits (not with -cpu),
// calls are counted exactly, a worker is shown under its own name, and the
// report also comes for a program that fails or calls exit.
func TestProfile(t *testing.T) {
	t.Chdir(t.TempDir())
	os.WriteFile("p.kkl", []byte(profiled), 0o644)
	var out, errOut bytes.Buffer
	if code := run([]string{"profile", "p.kkl"}, nil, &out, &errOut); code != 0 || out.String() != "2584\n5999995\n" {
		t.Fatalf("exit %d, %q, %s", code, out.String(), errOut.String())
	}
	report := errOut.String()
	for _, want := range []string{"wall time, with waiting", "  8361  fib (p.kkl:4) [main]", "work (p.kkl:9) [worker 1: work]",
		"1  time::sleep [main]", "Lines, by their own time"} {
		if !strings.Contains(report, want) {
			t.Errorf("no %q in the report:\n%s", want, report)
		}
	}
	if s := lineTime(t, report, "time::sleep(0.2);"); s < 0.15 {
		t.Errorf("%.3f s on the sleep line:\n%s", s, report)
	}

	// -cpu, with the report in a file: the sleep counts (almost) nothing.
	out.Reset()
	errOut.Reset()
	if code := run([]string{"profile", "-cpu", "-o", "r.txt", "p.kkl"}, nil, &out, &errOut); code != 0 || errOut.Len() != 0 {
		t.Fatalf("-cpu: exit %d, %s", code, errOut.String())
	}
	data, _ := os.ReadFile("r.txt")
	if !strings.Contains(string(data), "waiting left out") {
		t.Errorf("-cpu report:\n%s", data)
	}
	if s := lineTime(t, string(data), "time::sleep(0.2);"); s > 0.05 {
		t.Errorf("-cpu: %.3f s on the sleep line", s)
	}

	// -pprof: Go's pprof format, read back with its package.
	errOut.Reset()
	if code := run([]string{"profile", "-pprof", "p.pb.gz", "p.kkl"}, nil, &out, &errOut); code != 0 {
		t.Fatalf("-pprof: exit %d, %s", code, errOut.String())
	}
	f, err := os.Open("p.pb.gz")
	if err != nil {
		t.Fatal(err)
	}
	prof, err := profile.Parse(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(prof.SampleType) != 2 || prof.SampleType[0].Type != "samples" || prof.SampleType[1].Type != "wall" || prof.SampleType[1].Unit != "nanoseconds" {
		t.Errorf("sample types: %v", prof.SampleType)
	}
	names := map[string]string{}
	for _, fn := range prof.Function {
		names[fn.Name] = fn.Filename
	}
	if !strings.HasSuffix(names["main"], "p.kkl") || !strings.HasSuffix(names["work"], "p.kkl") { // (fib is too quick to be sampled for sure)
		t.Errorf("functions: %v", names)
	}
	if _, ok := names["[worker 1: work]"]; !ok {
		t.Errorf("no root frame for the worker: %v", names)
	}
	var wall int64
	interps := map[string]bool{}
	for _, s := range prof.Sample {
		wall += s.Value[1]
		interps[s.Label["interpreter"][0]] = true
		if root := s.Location[len(s.Location)-1].Line[0].Function.Name; root != "["+s.Label["interpreter"][0]+"]" {
			t.Errorf("a sample's stack starts with %s", root)
		}
	}
	if !interps["main"] || !interps["worker 1: work"] || wall < 150e6 {
		t.Errorf("samples of %v, %d ns", interps, wall)
	}

	// A program that fails, and one that calls exit: the report comes too.
	os.WriteFile("f.kkl", []byte("main() {\n    local v = [];\n    for (local i = 0; i < 1000; i++) push(v, i);\n    v[2000];\n}\n"), 0o644)
	errOut.Reset()
	if code := run([]string{"profile", "f.kkl"}, nil, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "RangeError") ||
		!strings.Contains(errOut.String(), "1000  push") {
		t.Errorf("a failing program: exit %d:\n%s", code, errOut.String())
	}
	os.WriteFile("e.kkl", []byte("main() {\n    exit(3);\n}\n"), 0o644)
	errOut.Reset()
	if code := run([]string{"profile", "e.kkl"}, nil, &out, &errOut); code != 3 || !strings.Contains(errOut.String(), "1  exit") {
		t.Errorf("exit(3): exit %d:\n%s", code, errOut.String())
	}

	// Usage errors.
	for _, args := range [][]string{{"profile"}, {"profile", "-o"}, {"profile", "-pprof"}, {"profile", "-x", "p.kkl"}, {"profile", "-cpu"}} {
		errOut.Reset()
		if code := run(args, nil, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "usage: kukel profile") {
			t.Errorf("%v: exit %d, %q", args, code, errOut.String())
		}
	}

	// A compiled program: the same kind of message as kukel run gives.
	errOut.Reset()
	if code := run([]string{"profile", "p.kkc"}, nil, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "p.kkc is a compiled program") {
		t.Errorf("p.kkc: exit %d, %q", code, errOut.String())
	}
}

// kukel profile -cpu: the work of built-ins counts (also of a native
// constructor, whose calls are counted), the waiting does not.
func TestProfileCPU(t *testing.T) {
	t.Chdir(t.TempDir())
	os.WriteFile("c.kkl", []byte(`use strings;
use time;

main() {
    local v = [];
    for (local i = 0; i < 200000; i++) push(v, (i * 7919) % 200003);
    for (local i = 0; i < 100; i++) new strings::Builder("x");
    sort(v);
    time::sleep(0.2);
}
`), 0o644)
	var out, errOut bytes.Buffer
	if code := run([]string{"profile", "-cpu", "c.kkl"}, nil, &out, &errOut); code != 0 {
		t.Fatalf("exit %d, %s", code, errOut.String())
	}
	report := errOut.String()
	if !strings.Contains(report, "       100  strings::Builder::Builder\n") {
		t.Errorf("no calls of the constructor:\n%s", report)
	}
	if s := lineTime(t, report, "time::sleep(0.2);"); s > 0.05 {
		t.Errorf("%.3f s on the sleep line:\n%s", s, report)
	}
	if s := lineTime(t, report, "sort(v);"); s < 0.02 {
		t.Errorf("%.3f s on the sort line:\n%s", s, report)
	}
}

// The time of returning from a deep recursion shows on the recursive call,
// not on the line of the caller that runs next (the VM also samples on
// the way back).
func TestProfileDeepReturn(t *testing.T) {
	t.Chdir(t.TempDir())
	os.WriteFile("d.kkl", []byte(`down(n) {
    if (n == 0) return 0;
    return down(n - 1) + 1;
}

main() {
    local t = 0;

    for (local i = 0; i < 300; i++) t += down(9000);
    println(t);
}
`), 0o644)
	var out, errOut bytes.Buffer
	if code := run([]string{"profile", "d.kkl"}, nil, &out, &errOut); code != 0 {
		t.Fatalf("exit %d, %s", code, errOut.String())
	}
	report := errOut.String()
	if m := regexp.MustCompile(`(?m)^ +[0-9.]+ s +([0-9.]+)%  d\.kkl:9 `).FindStringSubmatch(report); m != nil {
		if pct, _ := strconv.ParseFloat(m[1], 64); pct > 10 {
			t.Errorf("%.1f%% on the caller's loop:\n%s", pct, report)
		}
	}
	if s := lineTime(t, report, "return down(n - 1) + 1;"); s <= 0 {
		t.Errorf("no time on the recursive call:\n%s", report)
	}
}

// Ctrl-C: the program stops, the report is written, and kukel ends as
// interrupted. A program that waits where it cannot be cancelled gets its
// report after a second, while it waits.
func TestProfileInterrupt(t *testing.T) {
	t.Chdir(t.TempDir())
	signals := make(chan chan<- os.Signal, 1)
	notify, stop, die, exit := notifyInterrupt, stopInterrupt, dieInterrupted, exitProcess
	defer func() { notifyInterrupt, stopInterrupt, dieInterrupted, exitProcess = notify, stop, die, exit }()
	notifyInterrupt = func(c chan<- os.Signal) { signals <- c }
	stopInterrupt = func(chan<- os.Signal) {}
	dieInterrupted = func() int { return 130 }
	exited := make(chan int, 1)
	exitProcess = func(code int) { exited <- code }

	os.WriteFile("loop.kkl", []byte("main() {\n    println(\"start\");\n    while (true) {}\n}\n"), 0o644)
	var out, errOut bytes.Buffer
	go func() {
		c := <-signals
		time.Sleep(50 * time.Millisecond)
		c <- os.Interrupt
	}()
	if code := run([]string{"profile", "-pprof", "l.pb.gz", "loop.kkl"}, nil, &out, &errOut); code != 130 || out.String() != "start\n" {
		t.Fatalf("exit %d, %q:\n%s", code, out.String(), errOut.String())
	}
	if r := errOut.String(); !strings.Contains(r, "main (loop.kkl:1)") || strings.Contains(r, "CancelledError") {
		t.Errorf("report:\n%s", r)
	}
	if _, err := os.Stat("l.pb.gz"); err != nil {
		t.Error(err)
	}

	// A file the program left open is written out: it was cancelled, not
	// killed.
	os.WriteFile("file.kkl", []byte("use fs;\n\nmain() {\n    fs::open(\"kept.txt\", \"w\")->write(\"kept\");\n    while (true) {}\n}\n"), 0o644)
	errOut.Reset()
	go func() {
		c := <-signals
		time.Sleep(50 * time.Millisecond)
		c <- os.Interrupt
	}()
	if code := run([]string{"profile", "file.kkl"}, nil, &out, &errOut); code != 130 {
		t.Fatalf("file.kkl: exit %d:\n%s", code, errOut.String())
	}
	if data, _ := os.ReadFile("kept.txt"); string(data) != "kept" {
		t.Errorf("kept.txt: %q", data)
	}

	// readline waits for input that does not come: no cancelling it.
	os.WriteFile("read.kkl", []byte("main() {\n    readline();\n}\n"), 0o644)
	in, w := io.Pipe()
	errOut.Reset()
	go func() {
		c := <-signals
		time.Sleep(50 * time.Millisecond)
		c <- os.Interrupt
		<-exited // the report is written
		w.Close()
	}()
	if code := run([]string{"profile", "read.kkl"}, in, &out, &errOut); code != 130 {
		t.Fatalf("exit %d:\n%s", code, errOut.String())
	}
	if r := errOut.String(); strings.Count(r, "main still runs") != 1 { // (no sample yet, so no calls)
		t.Errorf("report:\n%s", r)
	}
}
