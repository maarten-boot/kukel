package remod_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/builtins"
	"github.com/maarten-boot/kukel/internal/compiler"
	_ "github.com/maarten-boot/kukel/internal/modules/remod"
	"github.com/maarten-boot/kukel/internal/parser"
	"github.com/maarten-boot/kukel/internal/vm"
)

func run(t *testing.T, src string) string {
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
	if _, err := m.Run(nil); err != nil {
		t.Fatalf("%v\noutput: %s", err, out.String())
	}
	return out.String()
}

// Every function and member function, with its errors.
func TestRe(t *testing.T) {
	if got := run(t, reSrc); got != reOut {
		t.Errorf("got:\n%s\nwant:\n%s", got, reOut)
	}
}

const reSrc = "use re;\nmain() {\n    local mail = re::compile(\"(\\\\w+)@(\\\\w+)\\\\.com\", \"i\");\n    println(mail, mail->match(\"x ANN@example.COM y\"), mail->find(\"x ann@ex.com y\"), mail->find(\"none\"));\n    println(mail->groups(\"Mail ANN@example.com\"), mail->groups(\"no\"));\n    println(mail->replace(\"a@b.com, c@d.com\", \"$2:$1\"), mail->findall(\"a@b.com c@d.com e@f.com\", 2));\n    println(re::compile(\"(?P<key>\\\\w+)=(\\\\d+)?\")->names(), re::compile(\"(a)|(b)\")->groups(\"b\"));\n    println(re::compile(\"\\\\s*,\\\\s*\")->split(\"a , b,c ,d\", 3), re::match(\"^\\\\d+$\", \"123\"), re::quote(\"1.5*2\"));\n    println(re::compile(\"\\\\d+\")->replace(\"a1b22c333\", fn(m) { return \"<\" + str(len(m)) + \">\"; }));\n    local r = new re::Regexp(\"x+\");\n    println(r->pattern(), typeof(r));\n    for (local f in [fn() { re::compile(\"(unclosed\"); }, fn() { re::compile(\"a\", \"q\"); },\n                     fn() { r->replace(\"x\", 5); }, fn() { r->replace(\"x\", fn(m) { return 1; }); },\n                     fn() { r->replace(\"x\", fn(m) { throw new Error(\"inner\", \"KeyError\"); }); }])\n        try { f(); } catch (e) { println(e->kind + \":\", e->message); }\n}\n"

const reOut = "re::Regexp(\"(?i)(\\\\w+)@(\\\\w+)\\\\.com\") true ann@ex.com nil\n[\"ANN@example.com\", \"ANN\", \"example\"] nil\nb:a, d:c [\"a@b.com\", \"c@d.com\"]\n[\"key\", \"\"] [\"b\", nil, \"b\"]\n[\"a\", \"b\", \"c ,d\"] true 1\\.5\\*2\na<1>b<2>c<3>\nx+ object\nValueError: re::compile: error parsing regexp: missing closing ): `(unclosed`\nValueError: re::compile: unknown flag in \"q\" (the flags are i, m, s and U)\nTypeError: re::Regexp::replace: argument 2 must be a string or a function, not int\nTypeError: re::Regexp::replace: the replacement function gave int, not a string\nKeyError: inner\n"
