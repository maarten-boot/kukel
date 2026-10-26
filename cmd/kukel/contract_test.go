package main

// Contract tests (docs/checks.md §4, TODO.md task 48): they hold the
// subsystems to the conventions they share, so that a change to one part
// does not need a review of all the others.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/maarten-boot/kukel/internal/bytecode"
	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

// waitExempt are the functions of the modules that wait without
// native.Wait, and why that is right.
var waitExempt = map[string]string{
	"workermod/worker.go: pop": "its callers pass the wait to begin (native.Wait) as its argument wait",
	"p9mod/tree.go: call":      "it runs on a 9P server's goroutine, not the interpreter's: the interpreter does not wait",
}

// TestModulesWait checks that the Go functions of the modules that wait
// (a select without default, a channel receive, time.Sleep) call
// native.Wait, so that the program's output is written out first and
// kukel profile -cpu leaves the wait out. A function started with go is
// not the interpreter waiting, and is left out.
func TestModulesWait(t *testing.T) {
	files, err := filepath.Glob("../../internal/modules/*/*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no module sources: %v", err)
	}
	found := map[string]bool{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		short := strings.TrimPrefix(filepath.ToSlash(name), "../../internal/modules/")
		started := map[*ast.FuncLit]bool{} // the bodies of go statements
		ast.Inspect(file, func(n ast.Node) bool {
			if g, ok := n.(*ast.GoStmt); ok {
				if f, ok := g.Call.Fun.(*ast.FuncLit); ok {
					started[f] = true
				}
			}
			return true
		})
		check := func(what string, body *ast.BlockStmt) {
			var at token.Pos
			waits := false
			ast.Inspect(body, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.FuncLit:
					return false // checked on its own
				case *ast.SelectStmt:
					for _, c := range n.Body.List {
						if c.(*ast.CommClause).Comm == nil {
							return false // with default: it does not wait
						}
					}
					if at == token.NoPos {
						at = n.Pos()
					}
				case *ast.UnaryExpr:
					if n.Op == token.ARROW && at == token.NoPos {
						at = n.Pos()
					}
				case *ast.CallExpr:
					if s, ok := n.Fun.(*ast.SelectorExpr); ok {
						if x, ok := s.X.(*ast.Ident); ok {
							switch {
							case x.Name == "native" && s.Sel.Name == "Wait":
								waits = true
							case x.Name == "time" && s.Sel.Name == "Sleep" && at == token.NoPos:
								at = n.Pos()
							}
						}
					}
				}
				return true
			})
			if at == token.NoPos || waits {
				return
			}
			key := short + ": " + what
			if _, ok := waitExempt[key]; ok {
				found[key] = true
				return
			}
			t.Errorf("%s: %s waits without native.Wait (or add it to waitExempt with the reason)", fset.Position(at), what)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncDecl:
				if n.Body != nil {
					check(n.Name.Name, n.Body)
				}
			case *ast.FuncLit:
				if !started[n] {
					check("func literal", n.Body)
				}
			}
			return true
		})
	}
	for key := range waitExempt {
		if !found[key] {
			t.Errorf("waitExempt has %q, which no longer waits: take it out", key)
		}
	}
}

// arity is a number of arguments a function takes: min to max (max -1:
// no limit).
type arity struct{ min, max int }

func (a arity) String() string {
	switch {
	case a.max < 0:
		return fmt.Sprintf("%d or more", a.min)
	case a.min == a.max:
		return strconv.Itoa(a.min)
	}
	return fmt.Sprintf("%d to %d", a.min, a.max)
}

// covers reports whether a takes every number of arguments b takes.
func (a arity) covers(b arity) bool {
	return b.min >= a.min && (a.max < 0 || b.max >= 0 && b.max <= a.max)
}

// probeEnv is an Env whose every method panics: a function the probe
// calls with arguments it accepts stops there.
type probeEnv struct{ value.Env }

// expects reads the numbers of arguments from an ArgumentError of
// native.Check ("expects 1 to 3 arguments, got 0").
var expects = regexp.MustCompile(`expects (at least |at most )?(\d+)(?: to (\d+))? arguments?, got`)

// probe finds the number of arguments call takes from what it says when
// given none and when given many: every native function checks its
// arguments with native.Check before it does anything.
func probe(call func(args []value.Value) error) (arity, bool) {
	try := func(n int) []string {
		var err error
		func() {
			defer func() { recover() }()
			err = call(make([]value.Value, n))
		}()
		if e, ok := err.(*value.Error); ok && e.Kind == value.ArgumentError {
			return expects.FindStringSubmatch(e.Msg)
		}
		return nil
	}
	read := func(m []string) arity {
		a, _ := strconv.Atoi(m[2])
		switch {
		case m[1] == "at least ":
			return arity{a, -1}
		case m[1] == "at most ":
			return arity{0, a}
		case m[3] != "":
			b, _ := strconv.Atoi(m[3])
			return arity{a, b}
		}
		return arity{a, a}
	}
	if m := try(0); m != nil {
		return read(m), true
	}
	if m := try(60); m != nil {
		if r := read(m); r.min == 0 {
			return r, true
		}
		return arity{}, false
	}
	return arity{0, -1}, true
}

// docCall is a call or signature in an inline code span of the reference:
// "events::after(seconds, f, args...)", "new tk::Label(parent, text [,
// options])", "w->send(v)".
var docCall = regexp.MustCompile(`^(new )?([a-z][a-z0-9]*::[A-Za-z_][A-Za-z0-9_]*(?:::[A-Za-z_][A-Za-z0-9_]*)?|[a-z][A-Za-z0-9_]*->[A-Za-z_][A-Za-z0-9_]*)\((.*)\)$`)

// docArity counts the parameters of a signature: those in brackets are
// optional ("msg [, timeout]", "[timeout]"), and one ending in ".." or
// "..." takes any number of arguments.
func docArity(params string) arity {
	var a arity
	depth, opt := 0, 0 // nesting of (), {} and vector literals; of optional []
	rest := false
	var cur strings.Builder
	flush := func() {
		p := strings.TrimSpace(cur.String())
		cur.Reset()
		if p == "" {
			return
		}
		if strings.HasSuffix(p, "..") {
			rest = true
			return
		}
		if opt > 0 {
			a.max++
		} else {
			a.min++
			a.max++
		}
	}
	inString := false
	for i := 0; i < len(params); i++ {
		c := params[i]
		switch {
		case inString:
			cur.WriteByte(c)
			if c == '\\' && i+1 < len(params) {
				i++
				cur.WriteByte(params[i])
			} else if c == '"' {
				inString = false
			}
		case c == '"':
			inString = true
			cur.WriteByte(c)
		case c == '(' || c == '{':
			depth++
			cur.WriteByte(c)
		case c == ')' || c == '}':
			depth--
			cur.WriteByte(c)
		case c == '[' && depth == 0 && optionalAt(params[i+1:]):
			flush()
			opt++
		case c == '[':
			depth++
			cur.WriteByte(c)
		case c == ']' && depth == 0 && opt > 0:
			flush()
			opt--
		case c == ']':
			depth--
			cur.WriteByte(c)
		case c == ',' && depth == 0:
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	if rest {
		a.max = -1
	}
	return a
}

// optionalAt reports whether the "[" before s opens optional parameters,
// not a vector literal: "[, timeout]", "[timeout]" (names only, up to
// the matching "]").
func optionalAt(s string) bool {
	t := strings.TrimLeft(s, " ")
	if strings.HasPrefix(t, ",") {
		return true
	}
	depth := 0
	for i := 0; i < len(t); i++ {
		switch c := t[i]; {
		case c == '[':
			depth++
		case c == ']' && depth == 0:
			return i > 0
		case c == ']':
			depth--
		case c == '_' || c == '.' || c == ' ' || c == ',' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return false
}

// referenceCalls gives the calls and signatures in the inline code of
// docs/reference.md section 11, by module (the "### name" section they
// are in), outside code blocks.
func referenceCalls(t *testing.T) map[string][][2]string {
	text, err := os.ReadFile("../../docs/reference.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(text)
	doc = doc[strings.Index(doc, "\n## 11. "):]
	calls := map[string][][2]string{}
	module := ""
	inBlock := false
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "```") {
			inBlock = !inBlock
			continue
		}
		if inBlock {
			continue
		}
		if name, ok := strings.CutPrefix(line, "### "); ok {
			module = strings.Fields(name)[0]
			continue
		}
		parts := strings.Split(line, "`")
		for i := 1; i < len(parts); i += 2 {
			if m := docCall.FindStringSubmatch(parts[i]); m != nil && m[3] != "..." { // f->pack(...): as shown before
				calls[module] = append(calls[module], [2]string{m[1] + m[2], m[3]})
			}
		}
	}
	return calls
}

// TestReferenceArity checks the numbers of arguments that docs/reference.md
// shows for the functions, constructors and member functions of the
// modules written in Go against what they take.
func TestReferenceArity(t *testing.T) {
	calls := referenceCalls(t)
	env := probeEnv{}
	for _, name := range native.Names() {
		m, _ := native.Lookup(name)
		actual := map[string]arity{} // by the name the reference writes
		methods := map[string][]arity{}
		add := func(key string, call func([]value.Value) error) {
			a, ok := probe(call)
			if !ok {
				t.Errorf("%s: cannot tell how many arguments it takes", key)
				return
			}
			actual[key] = a
		}
		for f, call := range m.Funcs {
			add(name+"::"+f, func(args []value.Value) error { _, err := call(env, args); return err })
		}
		for _, c := range m.Classes {
			q := c.QualifiedName()
			this := &value.ObjectObj{Class: c.ClassObj()}
			if c.New != nil {
				add("new "+q, func(args []value.Value) error { _, err := c.New(env, this, args); return err })
			}
			for s, call := range c.Statics {
				add(q+"::"+s, func(args []value.Value) error { _, err := call(env, args); return err })
			}
			for meth, call := range c.Methods {
				a, ok := probe(func(args []value.Value) error { _, err := call(env, this, args); return err })
				if !ok {
					t.Errorf("%s::%s: cannot tell how many arguments it takes", q, meth)
					continue
				}
				methods[meth] = append(methods[meth], a)
			}
		}
		docs := map[string][]arity{}
		for _, c := range calls[name] {
			key := c[0]
			if i := strings.Index(key, "->"); i >= 0 {
				key = "->" + key[i+2:]
			}
			docs[key] = append(docs[key], docArity(c[1]))
		}
		keys := make([]string, 0, len(actual))
		for k := range actual {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			checkArity(t, k, []arity{actual[k]}, docs[k])
		}
		for meth, as := range methods {
			checkArity(t, name+": ->"+meth, as, docs["->"+meth])
		}
	}
}

// checkArity checks the forms the reference shows for a function (shown)
// against what it takes (one arity; several for member functions of the
// same name in several classes of a module).
func checkArity(t *testing.T, what string, actual, shown []arity) {
	t.Helper()
	if len(shown) == 0 {
		return // TestReferenceDocumentsNativeModules reports what is missing
	}
	for _, s := range shown {
		ok := false
		for _, a := range actual {
			ok = ok || a.covers(s)
		}
		if !ok {
			t.Errorf("%s: the reference shows it with %s arguments, but it takes %v", what, s, actual)
		}
	}
	if len(actual) != 1 {
		return
	}
	all := shown[0]
	for _, s := range shown[1:] {
		all.min = min(all.min, s.min)
		if all.max >= 0 && (s.max < 0 || s.max > all.max) {
			all.max = s.max
		}
	}
	if all != actual[0] {
		t.Errorf("%s takes %s arguments, but the reference shows %s", what, actual[0], all)
	}
}

// TestObjectsNotSetUp passes an object whose Go part is not set up (of a
// Kukel class derived from a native class whose constructor does not call
// the native one) to every function, static member function and
// constructor of the modules, in each argument position, alone and in a
// vector. Each must refuse it with an exception of Kukel's own (a
// TypeError, or the ValueError of Args.NotSetUp), not crash or give an
// InternalError or HostError.
func TestObjectsNotSetUp(t *testing.T) {
	t.Chdir(t.TempDir())
	var uses, classes, objs strings.Builder
	for i, name := range native.Names() {
		m, _ := native.Lookup(name)
		fmt.Fprintf(&uses, "use %s;\n", name)
		for _, c := range m.Classes {
			if c.New != nil {
				fmt.Fprintf(&classes, "class D%d%s : %s { D%d%s(); }\nD%d%s::D%d%s() {}\n", i, c.Name, c.QualifiedName(), i, c.Name, i, c.Name, i, c.Name)
				fmt.Fprintf(&objs, "new D%d%s(), ", i, c.Name)
			}
		}
	}
	prelude := uses.String() + classes.String() + `
class Item { id; name; }

idle() { return 0; }

check(f, n) {
    local objs = [` + strings.TrimSuffix(objs.String(), ", ") + `];

    for (local o in objs) {
        for (local i = 0; i < n; i++) {
            for (local wrap in [false, true]) {
                local args = [];

                for (local j = 0; j < n; j++) push(args, nil);
                args[i] = wrap ? [o] : o;

                try {
                    f(args..);
                } catch (e) {
                    if (e->kind == "InternalError" || e->kind == "HostError") println(str(o) + " at " + str(i + 1) + ": " + e->kind + ": " + e->message);
                }
            }
        }
    }
}
`
	env := probeEnv{}
	try := func(what, setup, call string, a arity) {
		n := a.max
		if n < 0 || n > 6 {
			n = min(a.min+1, 6)
		}
		if n == 0 {
			return
		}
		src := prelude + fmt.Sprintf("main() {\n    %s\n    check(%s, %d);\n}\n", setup, call, n)
		if err := os.WriteFile("ns.kkl", []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr strings.Builder
		code := 0
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("%s crashes on an object that is not set up: %v", what, p)
				}
			}()
			code = run([]string{"run", "ns.kkl"}, strings.NewReader(""), &stdout, &stderr)
		}()
		if code != 0 || stdout.Len() > 0 {
			t.Errorf("%s: exit %d\n%s%s", what, code, stdout.String(), stderr.String())
		}
	}
	for _, name := range native.Names() {
		m, _ := native.Lookup(name)
		for f, call := range m.Funcs {
			if a, ok := probe(func(args []value.Value) error { _, err := call(env, args); return err }); ok {
				try(name+"::"+f, "", name+"::"+f, a)
			}
		}
		for _, c := range m.Classes {
			q := c.QualifiedName()
			for s, call := range c.Statics {
				if a, ok := probe(func(args []value.Value) error { _, err := call(env, args); return err }); ok {
					try(q+"::"+s, "", q+"::"+s, a)
				}
			}
			if c.New != nil {
				this := &value.ObjectObj{Class: c.ClassObj()}
				if a, ok := probe(func(args []value.Value) error { _, err := c.New(env, this, args); return err }); ok {
					try("new "+q, "", "fn(args..) { return new "+q+"(args..); }", a)
				}
			}
			if len(c.Methods) == 0 || name == "tk" { // (a tk widget needs a display and the process's main thread, not go test's)
				continue
			}
			setup, ok := working[q]
			if !ok {
				t.Errorf("%s: add to working how to make one, so that its member functions are tried", q)
				continue
			}
			for meth, call := range c.Methods {
				this := &value.ObjectObj{Class: c.ClassObj()}
				if a, ok := probe(func(args []value.Value) error { _, err := call(env, this, args); return err }); ok {
					try(q+"::"+meth, setup, "fn(args..) { return r->"+meth+"(args..); }", a)
				}
			}
		}
	}
}

// working makes a working object r of each native class with member
// functions, for TestObjectsNotSetUp (Item and idle are in its prelude).
var working = map[string]string{
	"db::Conn":         `local r = db::open("sqlite::memory:");`,
	"db::Cursor":       `local r = db::open("sqlite::memory:")->cursor("select 1");`,
	"db::Query":        `local c = db::open("sqlite::memory:"); c->migrate(Item); local r = c->find(Item);`,
	"events::Timer":    `local r = events::after(1000, idle);`,
	"p9::Tree":         `local r = new p9::Tree();`,
	"p9::Server":       `local r = p9::serve(new p9::Tree(), "tcp://127.0.0.1:0");`,
	"p9::Client":       `local s = p9::serve(new p9::Tree(), "tcp://127.0.0.1:0"); local r = p9::dial(s->endpoint());`,
	"re::Regexp":       `local r = new re::Regexp("a+");`,
	"strings::Builder": `local r = new strings::Builder("x");`,
	"time::Time":       `local r = time::now();`,
	"worker::Worker":   `local r = worker::start(idle);`,
	"zmq::Socket":      `local r = new zmq::Socket("PAIR");`,
}

// goOperation finds Go's name of an operation at the start of a message or
// of a part of one ("open a.kkl: ...", "dial tcp ...": docs/checks.md §3),
// and fmt's marks of a bad format.
var goOperation = regexp.MustCompile(`(^|: )(open|read|write|stat|lstat|fstat|mkdir|remove|unlinkat|rename|readdirent|readlink|chdir|chmod|dial|listen|accept|lookup|close) |%!|<nil>`)

// TestMessagesWithoutGoOperations makes the built-ins, the modules and the
// command fail on files and networks, and checks that no message carries
// Go's name of the operation that failed.
func TestMessagesWithoutGoOperations(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir("adir", 0o755); err != nil {
		t.Fatal(err)
	}
	failing := []struct{ module, call string }{ // the module it needs ("": none)
		{"", `readfile("nope/x")`}, {"", `writefile("nope/x", "a")`}, {"", `appendfile("nope/x", "a")`}, {"", `readfile("adir")`},
		{"fs", `fs::readdir("nope")`}, {"fs", `fs::stat("nope")`}, {"fs", `fs::mkdir("adir")`}, {"fs", `fs::remove("nope")`},
		{"fs", `fs::rename("nope", "x")`}, {"fs", `fs::walk("nope", fn(e) { return true; })`},
		{"worker", `worker::startfile("nope.kkl")`}, {"worker", `worker::startfile("adir")`},
		{"p9", `p9::dial("tcp://127.0.0.1:1")`}, {"p9", `p9::disk("nope")`}, {"p9", `p9::serve(new p9::Tree(), "unix:///nope/dir/sock")`},
		{"zmq", `new zmq::Socket("REP")->bind("ipc:///nope/dir/sock")`},
		{"db", `db::open("sqlite:nope/dir/x.db")`}, {"db", `db::open("sqlite:adir")`},
	}
	var src strings.Builder
	used := map[string]bool{}
	n := 0
	for _, f := range failing {
		if _, ok := native.Lookup(f.module); f.module != "" && !ok {
			continue // a kukel built without it (make test-small)
		}
		if f.module != "" && !used[f.module] {
			used[f.module] = true
			fmt.Fprintf(&src, "use %s;\n", f.module)
		}
	}
	src.WriteString("\nmain() {\n")
	for _, f := range failing {
		if _, ok := native.Lookup(f.module); f.module != "" && !ok {
			continue
		}
		n++
		fmt.Fprintf(&src, "    try {\n        %s;\n        println(%q);\n    } catch (e) {\n        println(e->kind + \": \" + e->message);\n    }\n", f.call, "no error: "+f.call)
	}
	src.WriteString("}\n")
	if err := os.WriteFile("m.kkl", []byte(src.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	if code := run([]string{"run", "m.kkl"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	messages := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(messages) != n {
		t.Fatalf("%d messages for %d calls:\n%s", len(messages), n, stdout.String())
	}
	for _, args := range [][]string{{"run", "nope.kkl"}, {"nope.kkl"}, {"run", "adir"}, {"check", "nope.kkl"}, {"exec", "nope.kkc"},
		{"fmt", "nope.kkl"}, {"disasm", "nope.kkl"}, {"tokens", "nope.kkl"}, {"ast", "nope.kkl"},
		{"build", "-o", "nope/x.kkc", "m.kkl"}, {"build", "-module", "-o", "nope/x.kkc", "m.kkl"},
		{"profile", "-o", "nope/r.txt", "m.kkl"}} {
		stderr.Reset()
		run(args, strings.NewReader(""), &stdout, &stderr)
		messages = append(messages, "kukel "+strings.Join(args, " ")+": "+strings.TrimSpace(stderr.String()))
	}
	for _, m := range messages {
		if strings.HasPrefix(m, "no error") {
			t.Errorf("%s", m)
		} else if goOperation.MatchString(m) {
			t.Errorf("Go's name of an operation in %q", m)
		}
	}
}

// disasmLine reads a line of kukel disasm: the source line (only where it
// changes), the offset, the instruction and its operands.
var disasmLine = regexp.MustCompile(`^\s*(?:\d+\s+)?(\d+)\s+([A-Z][A-Z0-9_]*)\b\s*(.*?)\s*$`)

// listingLine finds an instruction written as kukel disasm writes it in a
// line of a listing, after the source on its left.
var listingLine = regexp.MustCompile(`(?:^|\s)(\d+)\s+([A-Z][A-Z0-9_]+)\b\s*(.*?)\s*$`)

// TestVMListings checks the listings of docs/vm.md section 6 against the
// compiler. Before each listing, an HTML comment holds a program and names
// the functions the listing shows: "<!-- disasm [exact] f g ...", the
// program, "-->". The listing's instructions must come in that order in
// the code of those functions (a listing may leave instructions out); in
// an exact listing, each line that has an offset must be the line kukel
// disasm writes.
func TestVMListings(t *testing.T) {
	text, err := os.ReadFile("../../docs/vm.md")
	if err != nil {
		t.Fatal(err)
	}
	opcodes := map[string]bool{}
	for op := 0; op < 256; op++ {
		if info, ok := bytecode.Info(bytecode.Op(op)); ok {
			opcodes[info.Name] = true
		}
	}
	t.Chdir(t.TempDir())
	doc := string(text)
	n := 0
	for {
		i := strings.Index(doc, "<!-- disasm ")
		if i < 0 {
			break
		}
		doc = doc[i+len("<!-- disasm "):]
		head, rest, _ := strings.Cut(doc, "\n")
		program, rest, _ := strings.Cut(rest, "-->")
		_, rest, _ = strings.Cut(rest, "```\n")
		listing, _, _ := strings.Cut(rest, "```")
		names := strings.Fields(head)
		exact := len(names) > 0 && names[0] == "exact"
		if exact {
			names = names[1:]
		}
		n++
		if err := os.WriteFile("l.kkl", []byte(program), 0o644); err != nil {
			t.Fatal(err)
		}
		var out, errs strings.Builder
		if code := run([]string{"disasm", "l.kkl"}, strings.NewReader(""), &out, &errs); code != 0 {
			t.Errorf("listing %d (%s): the program does not compile: %s", n, head, errs.String())
			continue
		}
		code := map[string][]string{} // the lines of each function
		fn := ""
		for _, line := range strings.Split(out.String(), "\n") {
			if f, ok := strings.CutPrefix(line, "function "); ok {
				fn = strings.Fields(f)[0]
				continue
			}
			if disasmLine.MatchString(line) {
				code[fn] = append(code[fn], line)
			}
		}
		var real []string // the instructions of the functions, in order
		lines := map[string]string{}
		for _, name := range names {
			if code[name] == nil {
				t.Errorf("listing %d: no function %s in the program", n, name)
			}
			for _, line := range code[name] {
				m := disasmLine.FindStringSubmatch(line)
				real = append(real, m[2])
				lines[name+" "+m[1]] = strings.Join(strings.Fields(m[2]+" "+m[3]), " ")
			}
		}
		var shown []string
		for _, line := range strings.Split(listing, "\n") {
			prev := ""
			for _, w := range regexp.MustCompile(`[A-Z][A-Z0-9_]+`).FindAllString(line, -1) {
				if opcodes[w] && !strings.HasPrefix(prev, "ARITH_") { // not ADD in "ARITH_K ADD 1"
					shown = append(shown, w)
				}
				prev = w
			}
			if m := listingLine.FindStringSubmatch(line); exact && m != nil {
				want := strings.Join(strings.Fields(m[2]+" "+m[3]), " ")
				found := false
				for _, name := range names {
					found = found || lines[name+" "+m[1]] == want
				}
				if !found {
					t.Errorf("listing %d (%s): %q at %s, but kukel disasm gives\n%s", n, head, want, m[1], out.String())
				}
			}
		}
		j := 0
		for _, op := range real {
			if j < len(shown) && op == shown[j] {
				j++
			}
		}
		if j < len(shown) {
			t.Errorf("listing %d (%s): %s is not where the listing has it; kukel disasm gives\n%s", n, head, shown[j], strings.Join(real, " "))
		}
	}
	if n < 8 {
		t.Errorf("found %d listings with their programs in docs/vm.md", n)
	}
}
