package main

import (
	"bytes"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const profiled = `use time;
use worker;

fib(n) {
    if (n < 2) return n;
    return fib(n - 1) + fib(n - 2);
}

work() {
    local t = 0;
    for (local i = 0; i < 300000; i++) t += i % 7;
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
	if code := run([]string{"profile", "p.kkl"}, nil, &out, &errOut); code != 0 || out.String() != "2584\n899997\n" {
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
	for _, args := range [][]string{{"profile"}, {"profile", "-o"}, {"profile", "-x", "p.kkl"}, {"profile", "-cpu"}} {
		errOut.Reset()
		if code := run(args, nil, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "usage: kukel profile") {
			t.Errorf("%v: exit %d, %q", args, code, errOut.String())
		}
	}
}
