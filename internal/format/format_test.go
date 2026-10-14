package format

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormat(t *testing.T) {
	tests := []struct{ in, want string }{
		// indentation by braces and brackets; spacing around operators
		{"main(){\nlocal x=1+2*3;\nif(x>5){println( x );}\n}\n",
			"main() {\n    local x = 1 + 2 * 3;\n\n    if (x > 5) { println(x); }\n}\n"},
		// a statement without braces on the next line hangs one level deeper
		{"f() {\nif (a)\nif (b)\nx();\nelse\ny();\nz();\n}\n",
			"f() {\n    if (a)\n        if (b)\n            x();\n        else\n            y();\n    z();\n}\n"},
		// unary and binary minus, prefix and postfix, spread, members
		{"f(v) { return -a - -b - --c + d++ + !e + ~ f + g(v ..)->h + C :: k + v [ 0 ]; }\n",
			"f(v) { return -a - -b - --c + d++ + !e + ~f + g(v..)->h + C::k + v[0]; }\n"},
		// map literals: no space inside the braces, ": " after keys; ?: and class ":" spaced
		{"main() { local m = { \"a\" : 1 , 2:[ ] }; x = a?b:c; }\nclass A:B { }\n",
			"main() { local m = {\"a\": 1, 2: []}; x = a ? b : c; }\nclass A : B {}\n"},
		// switch: labels one level in, statements two; a block on a case line
		{"f(x) {\nswitch (x) {\ncase 1:\ny();\nbreak;\ncase 2: {\nz();\n}\ndefault:\n;\n}\n}\n",
			"f(x) {\n    switch (x) {\n        case 1:\n            y();\n            break;\n        case 2: {\n            z();\n        }\n        default:\n            ;\n    }\n}\n"},
		// comments stay (a trailing one keeps its distance), blank lines at most one,
		// trailing spaces go, a function literal argument indents once
		{"// head\n\n\n\nmain() {   \n    x = 1;      // set x   \n\n\n  events::post(fn() {\nprintln(1);\n});\n}\n",
			"// head\n\nmain() {\n    x = 1;      // set x\n\n    events::post(fn() {\n        println(1);\n    });\n}\n"},
		// labels and the colon of a label; with; multi-line strings stay as they are
		{"main() {\nouter: for (;;) { break outer; }\nwith (local c = open()) c->x();\ns = \"a\n  b\";\n}\n",
			"main() {\n    outer: for (;;) { break outer; }\n\n    with (local c = open()) c->x();\n    s = \"a\n  b\";\n}\n"},
	}
	// a block or brackets opened on a hanging line keep its extra level
	tests = append(tests, struct{ in, want string }{"f(v) {\nfor (local x in v)\nif (x) {\nfoo(1,\n2);\nif (y)\nwhile (z) {\nq(\n);\n}\n} else {\nbar();\n}\nlocal m = [1,\n2];\n}\n", "f(v) {\n    for (local x in v)\n        if (x) {\n            foo(1,\n                2);\n\n            if (y)\n                while (z) {\n                    q(\n                    );\n                }\n        } else {\n            bar();\n        }\n    local m = [1,\n        2];\n}\n"})
	for _, tc := range tests {
		got, err := Source("t.kkl", []byte(tc.in))
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if string(got) != tc.want {
			t.Errorf("format of\n%s\ngot:\n%s\nwant:\n%s", tc.in, got, tc.want)
		}
	}
}

// Breathing space: a blank line before if, while, do, for, switch, try and
// with, but not at the start of a block, after a label, under else or a
// header without braces, before the while of a do, or inside a map.
func TestBreathe(t *testing.T) {
	got, err := Source("t.kkl", []byte(breatheIn))
	if err != nil || string(got) != breatheOut {
		t.Errorf("got (%v):\n%s\nwant:\n%s", err, got, breatheOut)
	}
}

const breatheIn = "f(x, v) {\n    local n = 0;\n    if (x) n++;\n    for (local i = 0; i < 3; i++) {\n        if (i == 1) continue;\n        n += i;\n        while (n > 10) n--;\n    }\n    // explains the switch\n    switch (x) {\n        case 1:\n            if (n) n = 0;\n            break;\n        default:\n            n = 1;\n            for (;;) break;\n    }\n    outer: for (local a in v) break outer;\n    if (n)\n        n = 1;\n    else if (x)\n        n = 2;\n    else\n        if (v) n = 3;\n    do n--; while (n > 0);\n    do\n        n++;\n    while (n < 3);\n    local m = {\n        \"a\": 1\n    };\n\n    try { n = 1; } catch (e) { }\n    with (local z = nil) n = 2;\n    return n;\n}\nmain() {\n    if (true) f(1, []);\n}\n"

const breatheOut = "f(x, v) {\n    local n = 0;\n\n    if (x) n++;\n\n    for (local i = 0; i < 3; i++) {\n        if (i == 1) continue;\n        n += i;\n\n        while (n > 10) n--;\n    }\n\n    // explains the switch\n    switch (x) {\n        case 1:\n            if (n) n = 0;\n            break;\n        default:\n            n = 1;\n\n            for (;;) break;\n    }\n\n    outer: for (local a in v) break outer;\n\n    if (n)\n        n = 1;\n    else if (x)\n        n = 2;\n    else\n        if (v) n = 3;\n\n    do n--; while (n > 0);\n\n    do\n        n++;\n    while (n < 3);\n    local m = {\n        \"a\": 1\n    };\n\n    try { n = 1; } catch (e) {}\n\n    with (local z = nil) n = 2;\n    return n;\n}\nmain() {\n    if (true) f(1, []);\n}\n"

func TestRefused(t *testing.T) {
	if _, err := Source("t.kkl", []byte("main() { x = ; }")); err == nil || !strings.Contains(err.Error(), "expected expression") {
		t.Errorf("a syntax error: %v", err)
	}
	if _, err := Source("t.kkl", []byte("main() {}\x00")); err == nil || !strings.Contains(err.Error(), "NUL") {
		t.Errorf("a NUL byte: %v", err)
	}
}

// Every example formats, and formatting twice changes nothing more.
func TestExamplesIdempotent(t *testing.T) {
	files, _ := filepath.Glob("../../tests/run/*.kkl")
	more, _ := filepath.Glob("../../tests/run/lib/*.kkl")
	files = append(files, append(more, "../../tests/ast/all.kkl", "../../tests/tokens/sample.kkl")...)
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		once, err := Source(file, src)
		if err != nil {
			t.Errorf("%s: %v", file, err)
			continue
		}
		twice, err := Source(file, once)
		if err != nil || string(twice) != string(once) {
			t.Errorf("%s: formatting again changes it (%v):\n%s", file, err, twice)
		}
	}
}

// FuzzFormat: a program that parses formats without an internal error (the
// tokens stay the same), and formatting again changes nothing more.
func FuzzFormat(f *testing.F) {
	files, _ := filepath.Glob("../../tests/run/*.kkl")
	for _, file := range files {
		if src, err := os.ReadFile(file); err == nil {
			f.Add(string(src))
		}
	}
	f.Fuzz(func(t *testing.T, src string) {
		once, err := Source("f.kkl", []byte(src))
		if err != nil {
			if strings.Contains(err.Error(), "internal error") {
				t.Fatalf("%v for:\n%s", err, src)
			}
			return
		}
		twice, err := Source("f.kkl", once)
		if err != nil || string(twice) != string(once) {
			t.Fatalf("not idempotent (%v):\n%s\n----\n%s\n----\n%s", err, src, once, twice)
		}
	})
}
