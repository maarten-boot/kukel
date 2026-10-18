package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maarten-boot/kukel"
)

func TestRules(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"rules.kkl", "orders.json"}, &stdout, &stderr)
	want := `A1   ann      10.25   0%    10.25
A2   bob      18.00  20%    14.40  bulk, vip, customer since 2019
A3   cy       12.00   5%    11.40  customer since 2020
A4   zed       1.50  failed: KeyError: no customer "zed" (rules.kkl:9 in discount)
`
	if code != 1 || stdout.String() != want || stderr.Len() != 0 {
		t.Errorf("exit %d, stdout:\n%s\nstderr:\n%s\nwant exit 1, stdout:\n%s", code, &stdout, &stderr, want)
	}
}

// Rules that go wrong, for one order of ann's: an error fails the order
// (exit 1), and rules that use more than they may do not load (exit 2).
func TestBadRules(t *testing.T) {
	tests := []struct {
		name, rules    string
		code           int
		stdout, stderr string // what they contain
	}{
		{"println", `discount(o) { println("pricing", o["id"]); return 10; } main() {}`,
			0, "A1   ann      10.25  10%     9.23", "pricing A1"},
		{"loop", `discount(o) { while (true) {} } main() {}`,
			1, "failed: the rules took more than 100000 steps", ""},
		{"too much off", `discount(o) { return 80; } main() {}`,
			1, "failed: the rules gave 80 off, not 0 to 50", ""},
		{"no number", `discount(o) { return "lots"; } main() {}`,
			1, "failed: the rules gave a string, not a number", ""},
		{"throw", `discount(o) { throw new Error("no prices on Sunday", "ValueError"); } main() {}`,
			1, "failed: ValueError: no prices on Sunday (", ""},
		{"no discount", `main() {}`,
			1, "failed: kukel: ", ""},
		{"file", `discount(o) { return int(readfile("off.txt")); } main() {}`,
			2, "", "built-in function readfile is not available in this program"},
		{"exit", `discount(o) { exit(3); } main() {}`,
			2, "", "built-in function exit is not available in this program"},
		{"module", "use fs;\ndiscount(o) { return 0; } main() {}",
			2, "", "fs"},
	}
	dir := t.TempDir()
	orders := filepath.Join(dir, "orders.json")
	if err := os.WriteFile(orders, []byte(`[{"id": "A1", "customer": "ann", "items": [{"sku": "pen", "qty": 2, "price": 1.5}, {"sku": "ink", "qty": 1, "price": 7.25}]}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rules := filepath.Join(dir, strings.ReplaceAll(tt.name, " ", "_")+".kkl")
			if err := os.WriteFile(rules, []byte(tt.rules), 0o644); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			code := run([]string{rules, orders}, &stdout, &stderr)
			if code != tt.code || !strings.Contains(stdout.String(), tt.stdout) || !strings.Contains(stderr.String(), tt.stderr) {
				t.Errorf("exit %d, stdout:\n%s\nstderr:\n%s\nwant exit %d, stdout with %q, stderr with %q",
					code, &stdout, &stderr, tt.code, tt.stdout, tt.stderr)
			}
		})
	}
}

// A call that runs out of time ends with a CancelledError; the step limit
// usually comes first, so this checks only how it is explained.
func TestExplainCancelled(t *testing.T) {
	if got := explain(kukel.Errorf("CancelledError", "the program was cancelled")).Error(); got != "the rules took more than 1s" {
		t.Errorf("explain gives %q", got)
	}
}

func TestUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 || !strings.HasPrefix(stderr.String(), "usage:") {
		t.Errorf("exit %d, stderr %q", code, &stderr)
	}
}
