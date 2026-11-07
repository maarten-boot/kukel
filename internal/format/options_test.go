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
			"f(a) {\n    if (a) {\n        b(); // why\n    }\n\n    while (a) { // loop\n        a--;\n    }\n    /* c */ if (a) {\n        /* x */ b(); /* y */\n    } else if (a) {\n        c();\n    } else {\n        d();\n    }\n}\n"},
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
		// an else on the line after the "}" of its if goes up to it (not
		// over a comment); the comment at the end of a block on one line
		// goes to the line of its "{" (not when code follows it)
		{"f(a) {\n    if (a) {\n        b();\n    }\n    else c();\n    if (a) { b(); }\n    // keep\n    else c();\n}\ng() { return 1; } // one\nh() { return 2; } /* two */ \n",
			"f(a) {\n    if (a) {\n        b();\n    } else {\n        c();\n    }\n\n    if (a) {\n        b();\n    }\n    // keep\n    else {\n        c();\n    }\n}\ng() { // one\n    return 1;\n}\nh() { /* two */\n    return 2;\n}\n"},
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
		for _, opt := range []Options{{Strict: true}, {Vertical: true}, {Strict: true, LineLength: 40}} {
			once, err := SourceWith(file, src, opt)
			if err != nil {
				t.Errorf("%s %+v: %v", file, opt, err)
				continue
			}
			twice, err := SourceWith(file, once, opt)
			if err != nil || string(twice) != string(once) {
				t.Errorf("%s %+v: formatting again changes it (%v):\n%s", file, opt, err, twice)
			}
			if plain, err := Source(file, once); err != nil || string(plain) != string(once) {
				t.Errorf("%s %+v: kukel fmt changes the strict form (%v)", file, opt, err)
			}
		}
	}
}

func TestLists(t *testing.T) {
	strict, vertical, short := Options{Strict: true}, Options{Vertical: true}, Options{Strict: true, LineLength: 30}
	tests := []struct {
		opt     Options
		in, out string
	}{
		// more than 3 arguments: one per line, a "," after the last, the ")"
		// at the indentation of the line the call starts on
		{strict, "main() { local t = sum(1, 2, 3, 4); f(1, 2, 3); }\n",
			"main() {\n    local t = sum(\n        1,\n        2,\n        3,\n        4,\n    );\n    f(1, 2, 3);\n}\n"},
		// member calls and new; the call around a split one is split too,
		// not one inside a function literal's body
		{strict, "main() { f(p->m(1, 2, 3, 4), new C(1, 2, 3, 4)); g(fn() { h(1, 2, 3, 4); }); }\n",
			"main() {\n    f(\n        p->m(\n            1,\n            2,\n            3,\n            4,\n        ),\n        new C(\n            1,\n            2,\n            3,\n            4,\n        ),\n    );\n    g(fn() { h(\n        1,\n        2,\n        3,\n        4,\n    ); });\n}\n"},
		// comments stay with their items; a "," already after the last item
		// is kept; a call written over lines by hand is put one per line
		{strict, "main() {\n    f(1, /* two */ 2, 3,\n      4 // four\n    );\n    g(1, 2, 3, 4,);\n}\n",
			"main() {\n    f(\n        1,\n        /* two */ 2,\n        3,\n        4, // four\n    );\n    g(\n        1,\n        2,\n        3,\n        4,\n    );\n}\n"},
		// definitions are not split by count in strict, by length they are
		{strict, "add(a, b, c, d) { return a; }\nmain() {}\n", "add(a, b, c, d) {\n    return a;\n}\nmain() {}\n"},
		{short, "addition(firstnumber, secondnumber) { return firstnumber; }\nmain() { println(addition(1, 2), \"a long text\"); }\n",
			"addition(\n    firstnumber,\n    secondnumber,\n) {\n    return firstnumber;\n}\nmain() {\n    println(\n        addition(1, 2),\n        \"a long text\",\n    );\n}\n"},
		// vertical: every list of 2 or more, definitions too; one item, an
		// empty list and a function literal's parameters stay
		{vertical, "add(a, b) { return a + b; }\nmain() { println(add(1, 2)); f(); g(fn(x, y) { return x; }); }\n",
			"add(\n    a,\n    b,\n) {\n    return a + b;\n}\nmain() {\n    println(\n        add(\n            1,\n            2,\n        ),\n    );\n    f();\n    g(fn(x, y) { return x; });\n}\n"},
		// vector and map literals: by length with Strict (the outermost
		// first: the maps inside fit after that), every one of 2 or more
		// with Vertical; a block is not a map
		{Options{Strict: true, LineLength: 40}, "main() { local v = [{\"a\": 1, \"b\": 2}, {\"a\": 3, \"b\": 4}, [5, 6]]; if (v) { f([1, 2]); } }\n",
			"main() {\n    local v = [\n        {\"a\": 1, \"b\": 2},\n        {\"a\": 3, \"b\": 4},\n        [5, 6],\n    ];\n\n    if (v) {\n        f([1, 2]);\n    }\n}\n"},
		{vertical, "main() { local m = {\"a\": [1, 2], \"b\": {}}; local one = [1]; }\n",
			"main() {\n    local m = {\n        \"a\": [\n            1,\n            2,\n        ],\n        \"b\": {},\n    };\n    local one = [1];\n}\n"},
	}
	for _, tc := range tests {
		got, err := SourceWith("t.kkl", []byte(tc.in), tc.opt)
		if err != nil || string(got) != tc.out {
			t.Errorf("%+v format of\n%s\ngot (%v):\n%s\nwant:\n%s", tc.opt, tc.in, err, got, tc.out)
			continue
		}
		if again, err := SourceWith("t.kkl", got, tc.opt); err != nil || string(again) != string(got) {
			t.Errorf("%+v: formatting again changes\n%s\n(%v):\n%s", tc.opt, got, err, again)
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
		opt := Options{Strict: true, LineLength: 40} // short lines: many lists split
		if len(src)%2 == 1 {
			opt = Options{Vertical: true}
		}
		once, err := SourceWith("f.kkl", []byte(src), opt)
		if err != nil {
			if strings.Contains(err.Error(), "internal error") {
				t.Fatalf("%v for:\n%s", err, src)
			}
			return
		}
		twice, err := SourceWith("f.kkl", once, opt)
		if err != nil || string(twice) != string(once) {
			t.Fatalf("not idempotent (%v):\n%s\n----\n%s\n----\n%s", err, src, once, twice)
		}
	})
}
