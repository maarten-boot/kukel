package format

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStrict(t *testing.T) {
	tests := []struct{ in, want string }{
		// braces for if and else, every function on several lines
		{"f(a) { if (a) b(); else c(); return 1; }\n",
			"f(a) {\n    if (a) {\n        b();\n    } else {\n        c();\n    }\n    return 1;\n}\n"},
		// comments stay: after a statement, between a header and its
		// statement (the "{" goes before them), before a statement
		{"f(a) {\n    if (a) b(); // why\n    while (a) // loop\n        a--;\n    /* c */ if (a) /* x */ b(); /* y */ else if (a) c(); else d();\n}\n",
			"f(a) {\n    if (a) {\n        b();\n    } // why\n\n    while (a) { // loop\n        a--;\n    }\n    /* c */ if (a) {\n        /* x */ b();\n    } /* y */ else if (a) {\n        c();\n    } else {\n        d();\n    }\n}\n"},
		// nested headers, a statement that starts with "(", do, with, a
		// label, an empty statement
		{"f(v) { for (local x in v) if (x) (x)->g(); do x++; while (x < 3); with (local z = nil) z; outer: for (;;) break outer; if (v) ; }\n",
			"f(v) {\n    for (local x in v) {\n        if (x) {\n            (x)->g();\n        }\n    }\n\n    do {\n        x++;\n    } while (x < 3);\n\n    with (local z = nil) {\n        z;\n    }\n\n    outer: for (;;) {\n        break outer;\n    }\n\n    if (v) {\n        ;\n    }\n}\n"},
		// function literals get braces but keep their line; switch, try,
		// member functions; empty bodies and classes stay
		{"global g = 1;\nh(v) { sort(v, fn(a, b) { if (a) return 1; return 0; }); switch (v) { case 1: if (v) x(); break; default: } try { a(); } catch (e) { b(); } }\nmain() {}\nclass A { m(); }\nA::m() { return 1; }\n",
			"global g = 1;\nh(v) {\n    sort(v, fn(a, b) { if (a) { return 1; } return 0; });\n\n    switch (v) {\n        case 1:\n            if (v) {\n                x();\n            }\n            break;\n        default:\n    }\n\n    try {\n        a();\n    } catch (e) {\n        b();\n    }\n}\nmain() {}\nclass A { m(); }\nA::m() {\n    return 1;\n}\n"},
		// a local declaration may not be the statement of a header: no
		// braces, so the program stays an error (kukel check says so)
		{"f(a) { if (a) local x = 1; }\n", "f(a) {\n    if (a) local x = 1;\n}\n"},
		// a statement on the line after its header: the "{" goes on the
		// header's line; a comment before a closing brace stays with the
		// statement before it
		{"f(a) { if (a)\n b(); else\n c(); }\ng() { b(); /* m */ }\n",
			"f(a) {\n    if (a) {\n        b();\n    } else {\n        c();\n    }\n}\ng() {\n    b(); /* m */\n}\n"},
		// yield from a literal keeps its space (it is not an index or call)
		{"h(from) { yield from [1,2]; yield from (from); yield from {1:2}; local a = from[0]; yield (from)(1); }\n",
			"h(from) {\n    yield from [1, 2];\n    yield from (from);\n    yield from {1: 2};\n    local a = from[0];\n    yield (from)(1);\n}\n"},
		// a statement of several lines, a function literal inside it
		{"f(a) {\n    if (a) g(1,\n        fn() { return 2; });\n}\n",
			"f(a) {\n    if (a) {\n        g(1,\n            fn() { return 2; });\n    }\n}\n"},
	}
	for _, tc := range tests {
		got, err := SourceWith("t.kkl", []byte(tc.in), Options{Strict: true})
		if err != nil || string(got) != tc.want {
			t.Errorf("strict format of\n%s\ngot (%v):\n%s\nwant:\n%s", tc.in, err, got, tc.want)
		}
	}
}

// Every example formats strictly, and formatting again (strictly or not)
// changes nothing more.
func TestStrictExamples(t *testing.T) {
	files, _ := filepath.Glob("../../tests/run/*.kkl")
	more, _ := filepath.Glob("../../tests/run/lib/*.kkl")
	examples, _ := filepath.Glob("../../examples/*.kkl")
	files = append(files, append(append(more, examples...), "../../tests/ast/all.kkl", "../../tests/tokens/sample.kkl")...)
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		once, err := SourceWith(file, src, Options{Strict: true})
		if err != nil {
			t.Errorf("%s: %v", file, err)
			continue
		}
		twice, err := SourceWith(file, once, Options{Strict: true})
		if err != nil || string(twice) != string(once) {
			t.Errorf("%s: formatting again changes it (%v):\n%s", file, err, twice)
		}
		if plain, err := Source(file, once); err != nil || string(plain) != string(once) {
			t.Errorf("%s: kukel fmt changes the strict form (%v)", file, err)
		}
	}
}

// FuzzStrict: a program that parses formats strictly without an internal
// error (only braces are added), and formatting again changes nothing more.
func FuzzStrict(f *testing.F) {
	files, _ := filepath.Glob("../../tests/run/*.kkl")
	for _, file := range files {
		if src, err := os.ReadFile(file); err == nil {
			f.Add(string(src))
		}
	}
	f.Fuzz(func(t *testing.T, src string) {
		once, err := SourceWith("f.kkl", []byte(src), Options{Strict: true})
		if err != nil {
			if strings.Contains(err.Error(), "internal error") {
				t.Fatalf("%v for:\n%s", err, src)
			}
			return
		}
		twice, err := SourceWith("f.kkl", once, Options{Strict: true})
		if err != nil || string(twice) != string(once) {
			t.Fatalf("not idempotent (%v):\n%s\n----\n%s\n----\n%s", err, src, once, twice)
		}
	})
}
