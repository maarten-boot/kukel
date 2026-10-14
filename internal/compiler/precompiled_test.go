package compiler

import (
	"bytes"
	"strings"
	"testing"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/builtins"
	"github.com/maarten-boot/kukel/internal/bytecode"
	"github.com/maarten-boot/kukel/internal/parser"
	"github.com/maarten-boot/kukel/internal/vm"
)

const animalSrc = `global start7 = 7;
class Animal { name; static count; Animal(name); speak(); static made(); }
Animal::Animal(name) { this->name = name; Animal::count = (Animal::count == nil ? 0 : Animal::count) + 1; }
Animal::speak() { return name + " makes a sound"; }
Animal::made() { return Animal::count; }
`

const zooSrc = `// zoo
use base;
const LEGS = 4;
global start = base::start7 * 2, counter = fn() { local n = 0; return fn() { n++; return n; }; }();
class Dog : base::Animal { tricks; Dog(name, tricks); speak(); static kind(); }
Dog::Dog(name, tricks = []) { base::Animal::Animal(name); this->tricks = tricks; }
Dog::speak() {
    return base::Animal::speak() + ", woof (" + str(len(tricks)) + " tricks)";
}
Dog::kind() { return "dog"; }
made() { return base::Animal::count; }
next() { return counter(); }
risky(x) {
    try {
        if (x < 0) throw new Error("negative", "ValueError");
        return sqrt(x);
    } catch (e) {
        return e->kind;
    }
}
`

const zooMain = `use zoo; use base;
main() {
    local d = new zoo::Dog("rex", ["sit"]);
    println(d->speak(), " ", zoo::Dog::kind(), " ", zoo::made(), " ", zoo::LEGS);
    println(zoo::start, " ", zoo::next(), zoo::next(), " ", zoo::risky(16), " ", zoo::risky(-1));
}
`

func parseAs(t *testing.T, module, name, src string) *ast.File {
	t.Helper()
	f, err := parser.ParseFile(name, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	f.Module = module
	return f
}

// compiled compiles module zoo on its own, through a .kkc file, and gives
// it as the loader does: its header with the code.
func compiled(t *testing.T, base string) *ast.File {
	t.Helper()
	files := []*ast.File{parseAs(t, "base", "base.kkl", base), parseAs(t, "zoo", "zoo.kkl", zooSrc)}
	m, err := CompileModule(files, []byte(zooSrc), builtins.Names(), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := bytecode.EncodeModule(m)
	if err != nil {
		t.Fatal(err)
	}
	if m, err = bytecode.DecodeModule(data); err != nil {
		t.Fatal(err)
	}
	if strings.Count(m.Header, "\n") != strings.Count(zooSrc, "\n") || strings.Contains(m.Header, "woof") ||
		strings.Contains(m.Header, "counter()") || !strings.Contains(m.Header, "global start , counter ;") {
		t.Errorf("header:\n%s", m.Header)
	}
	f := parseAs(t, "zoo", "zoo.kkc", m.Header)
	f.Compiled = m
	return f
}

func runProg(t *testing.T, files ...*ast.File) string {
	t.Helper()
	prog, err := Compile(files, builtins.Names())
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	machine, err := vm.New(prog, builtins.Lookup, &out, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := machine.Run(nil); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestCompiledModule(t *testing.T) {
	want := runProg(t, parseAs(t, "base", "base.kkl", animalSrc), parseAs(t, "zoo", "zoo.kkl", zooSrc), parseAs(t, "", "main.kkl", zooMain))
	if want != "rex makes a sound, woof (1 tricks)   dog   1   4\n14   1 2   4.0   ValueError\n" {
		t.Fatalf("from source: %q", want)
	}
	got := runProg(t, parseAs(t, "base", "base.kkl", animalSrc), compiled(t, animalSrc), parseAs(t, "", "main.kkl", zooMain))
	if got != want {
		t.Errorf("linked: %q, want %q", got, want)
	}

	// The program must have what the module was compiled against.
	link := func(base string) error {
		_, err := Compile([]*ast.File{parseAs(t, "base", "base.kkl", base), compiled(t, animalSrc), parseAs(t, "", "main.kkl", zooMain)}, builtins.Names())
		return err
	}
	for _, tc := range []struct{ base, msg string }{
		{strings.Replace(animalSrc, "{ name;", "{ age, name;", 1), "zoo.kkc:1:1: class zoo::Dog has changed since the module was compiled (compile module zoo again)"},
		{strings.Replace(animalSrc, "static count;", "static other, count;", 1), "class base::Animal has changed"},
		{strings.Replace(animalSrc, "Animal::speak() {", "Animal::speak2() {", 1) + "", "base.kkl"}, // speak declared but not defined in base: base's own error
	} {
		if err := link(tc.base); err == nil || !strings.Contains(err.Error(), tc.msg) {
			t.Errorf("got %v, want %s", err, tc.msg)
		}
	}
	// A .kkc file under the name of another module.
	f := compiled(t, animalSrc)
	f.Module = "other"
	if _, err := Compile([]*ast.File{parseAs(t, "base", "base.kkl", animalSrc), f, parseAs(t, "", "main.kkl", "main() {}")}, builtins.Names()); err == nil || !strings.Contains(err.Error(), "zoo.kkc is module zoo, not other") {
		t.Errorf("a renamed module: %v", err)
	}
}
