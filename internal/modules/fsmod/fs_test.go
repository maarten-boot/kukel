package fsmod_test

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/builtins"
	"github.com/maarten-boot/kukel/internal/compiler"
	"github.com/maarten-boot/kukel/internal/modules/fsmod"
	"github.com/maarten-boot/kukel/internal/parser"
	"github.com/maarten-boot/kukel/internal/vm"
)

func run(t *testing.T, src string, args ...string) string {
	t.Helper()
	f, err := parser.ParseFile("t.kkl", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	prog, err := compiler.Compile([]*ast.File{f}, builtins.Names())
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	m, err := vm.New(prog, builtins.Lookup, &out, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Run(args); err != nil {
		t.Fatalf("%v\noutput: %s", err, out.String())
	}
	return out.String()
}

// Every function of fs, with its errors, in a directory of the test.
func TestFS(t *testing.T) {
	if got := run(t, fsSrc, t.TempDir()); got != fsOut {
		t.Errorf("got:\n%s\nwant:\n%s", got, fsOut)
	}
}

// Symbolic links are entries of their own, and walk does not follow them
// (a link to the directory above would be a loop).
func TestLinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink("/nonexistent", filepath.Join(dir, "dangling")); err != nil {
		t.Skip("no symbolic links here:", err)
	}
	os.Mkdir(filepath.Join(dir, "d"), 0o777)
	os.Symlink("..", filepath.Join(dir, "d", "up"))
	want := "d false true\ndangling true false\n4 false true true\n"
	if got := run(t, linksSrc, dir); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

const fsSrc = "use fs;\nmain(args) {\n    local root = fs::join(args[0], \"tree\");\n    fs::mkdirall(fs::join(root, \"a\", \"b\"));\n    writefile(fs::join(root, \"a\", \"one.txt\"), \"1\");\n    writefile(fs::join(root, \"a\", \"b\", \"two.kkl\"), \"22\");\n    writefile(fs::join(root, \"top.kkl\"), \"333\");\n    fs::mkdir(fs::join(root, \"empty\"));\n    for (local e in fs::readdir(root)) println(e[\"name\"], e[\"dir\"], e[\"size\"] > 0 || e[\"dir\"], e[\"link\"], typeof(e[\"time\"]), len(e[\"mode\"]));\n    local s = fs::stat(fs::join(root, \"top.kkl\"));\n    println(s[\"name\"], s[\"size\"], s[\"dir\"], fs::exists(root), fs::exists(fs::join(root, \"nope\")));\n    fs::walk(root, fn(p, e) { println(slice(p, len(args[0]) + 1), e[\"dir\"] ? \"dir\" : e[\"size\"]); return e[\"name\"] != \"b\"; });\n    fs::rename(fs::join(root, \"top.kkl\"), fs::join(root, \"moved.kkl\"));\n    println(fs::exists(fs::join(root, \"moved.kkl\")), fs::base(\"/x/y.kkl\"), fs::dir(\"/x/y.kkl\"), fs::ext(\"/x/y.kkl\"), fs::join(\"a\", \"..\", \"b\", \"c\"));\n    for (local f in [fn() { fs::remove(fs::join(root, \"a\")); }, fn() { fs::mkdir(root); }, fn() { fs::readdir(fs::join(root, \"nope\")); },\n                     fn() { fs::walk(root, 5); }, fn() { fs::walk(root, fn(p, e) { throw new Error(\"stop\", \"KeyError\"); }); },\n                     fn() { fs::removeall(fs::join(root, \"nope\")); }])\n        try { f(); } catch (e) { println(e->kind + \":\", slice(e->message, 0, find(e->message, \":\") + 1)); }\n    fs::removeall(root);\n    println(fs::exists(root));\n}\n"

const fsOut = "a true true false object 10\nempty true true false object 10\ntop.kkl false true false object 10\ntop.kkl 3 false true false\ntree dir\ntree/a dir\ntree/a/b dir\ntree/a/one.txt 1\ntree/empty dir\ntree/top.kkl 3\ntrue y.kkl /x .kkl b/c\nIOError: fs:\nIOError: fs:\nIOError: fs:\nTypeError: fs:\nKeyError: \nIOError: fs:\nfalse\n"

const linksSrc = "use fs;\nmain(a) {\n    for (local e in fs::readdir(a[0])) println(e[\"name\"], e[\"link\"], e[\"dir\"]);\n    local n = 0;\n    fs::walk(a[0], fn(p, e) { n++; });\n    println(n, fs::exists(a[0] + \"/dangling\"), fs::stat(a[0] + \"/d/up\")[\"link\"], fs::stat(a[0] + \"/d/up\")[\"dir\"]);\n}\n"

func TestModeString(t *testing.T) {
	for m, want := range map[fs.FileMode]string{
		0o644:                  "-rw-r--r--",
		fs.ModeDir | 0o755:     "drwxr-xr-x",
		fs.ModeSymlink | 0o777: "lrwxrwxrwx",
		fs.ModeDevice | fs.ModeCharDevice | 0o666: "crw-rw-rw-",
		fs.ModeDevice | 0o660:                     "brw-rw----",
		fs.ModeNamedPipe | 0o600:                  "prw-------",
		fs.ModeSocket | 0o755:                     "srwxr-xr-x",
		fs.ModeSetuid | 0o755:                     "-rwsr-xr-x",
		fs.ModeSetuid | 0o644:                     "-rwSr--r--",
		fs.ModeSetgid | 0o750:                     "-rwxr-s---",
		fs.ModeDir | fs.ModeSticky | 0o777:        "drwxrwxrwt",
		fs.ModeDir | fs.ModeSticky | 0o776:        "drwxrwxrwT",
	} {
		if got := fsmod.ModeString(m); got != want {
			t.Errorf("%v: %s, want %s", m, got, want)
		}
	}
}
