package compiler

import (
	"strings"
	"testing"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/parser"
	"github.com/maarten-boot/kukel/internal/value"
)

// The native module "nat" for the tests: a function, a constant, a class
// with a constructor (Box) and one without (Handle).
func init() {
	nop := func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
		return value.NilValue, nil
	}
	fn := func(env value.Env, args []value.Value) (value.Value, error) { return value.NilValue, nil }
	native.Register(&native.Module{
		Name:   "nat",
		Funcs:  map[string]native.Func{"f": fn},
		Consts: map[string]value.Value{"K": value.MakeInt(7)},
		Classes: []*native.Class{
			{Name: "Box", New: nop, Methods: map[string]value.NativeMethod{"get": nop}, Statics: map[string]native.Func{"make": fn}},
			{Name: "Handle", Methods: map[string]value.NativeMethod{"close": nop}},
		},
	})
}

func TestNativeModules(t *testing.T) {
	ok := []string{
		"use nat; main() { nat::f(1, 2); local b = new nat::Box(1); b->get(); b->get; nat::Box::make(); local m = nat::Box::make; m(); }",
		"use nat; const A = nat::K * 2; main() { println(A); local k = nat::Box; new k(); }",
		"use nat; main() { local h = nat::Handle; println(h); }",
		// deriving (Q51): the native constructor, inherited and overridden member functions
		"use nat; class B : nat::Box { x; B(); get(); } B::B() { nat::Box::Box(1); } B::get() { return nat::Box::get(); } main() { new B()->get(); }",
		"use nat; class B : nat::Box {} class C : B { C(); } C::C() { nat::Box::Box(2); } main() { new B(1); new C(); B::make(); }",
	}
	for _, src := range ok {
		if err := compileSrc(t, src); err != nil {
			t.Errorf("%s: %v", src, err)
		}
	}
	bad := []struct{ src, msg string }{
		{"use nat; class B : nat::Handle {} main() {}", "1:25: class B cannot derive from native class nat::Handle: it has no constructor"},
		{"use nat; class B : nat::Box { get; } main() {}", "1:31: member get conflicts with member function get of native base class nat::Box"},
		{"use nat; class B : nat::Box { static get(); } main() {}", "member get conflicts with member function get of native base class nat::Box"},
		{"use nat; class B : nat::Box { f(); } B::f() { nat::Box::get([1]..); } main() {}", "spread arguments are not supported in a nat::Box::get(...) call"},
		{"use nat; main() { nat::Box::Box(1); }", "nat::Box::Box is not static"},
		{"use nat; main() { new nat::Handle(); }", "1:19: native class nat::Handle has no constructor: its objects come from the functions of its module"},
		{"use nat; main() { nat::g(); }", "1:24: module nat has no g"},
		{"use nat; main() { nat::Box::nope(); }", "1:29: class nat::Box has no member nope"},
		{"use nat; main() { nat::f = 1; }", "1:24: cannot assign to built-in function nat::f"},
		{"use nat; main() { nat::K = 1; }", "1:24: cannot assign to constant nat::K"},
		{"use nat; main() { nat::Box::get(); }", "1:29: nat::Box::get is not static"},
		{"use nat; nat::Box::get() {} main() {}", "expected '(' after function name"},
	}
	for _, tc := range bad {
		err := compileSrcOrParse(t, tc.src)
		if err == nil || !strings.Contains(err.Error(), tc.msg) {
			t.Errorf("%s:\n got %v\nwant %s", tc.src, err, tc.msg)
		}
	}
}

// compileSrcOrParse compiles src, also reporting a parse error.
func compileSrcOrParse(t *testing.T, src string) error {
	if _, err := parseOnly(src); err != nil {
		return err
	}
	return compileSrc(t, src)
}

func parseOnly(src string) (any, error) { return parser.ParseFile("t.kkl", []byte(src)) }
