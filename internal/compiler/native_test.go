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
	}
	for _, src := range ok {
		if err := compileSrc(t, src); err != nil {
			t.Errorf("%s: %v", src, err)
		}
	}
	bad := []struct{ src, msg string }{
		{"use nat; class B : nat::Box {} main() {}", "1:25: class B cannot derive from native class nat::Box (native classes cannot be derived from yet)"},
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
