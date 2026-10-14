package native

import (
	"strings"
	"testing"

	"github.com/maarten-boot/kukel/internal/value"
)

func TestRegisterAndGlobal(t *testing.T) {
	fn := func(env value.Env, args []value.Value) (value.Value, error) { return value.MakeInt(1), nil }
	meth := func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
		return value.NilValue, nil
	}
	c := &Class{Name: "C", New: meth, Methods: map[string]value.NativeMethod{"m": meth}, Statics: map[string]Func{"s": fn}}
	Register(&Module{Name: "ntest", Funcs: map[string]Func{"f": fn}, Classes: []*Class{c}})

	if m, ok := Lookup("ntest"); !ok || m.Name != "ntest" {
		t.Fatal("Lookup")
	}
	for _, name := range []string{"ntest::f", "ntest::C", "ntest::C::s"} {
		if _, ok := Global(name); !ok {
			t.Errorf("Global(%q) not found", name)
		}
	}
	for _, name := range []string{"ntest::g", "ntest::C::m", "ntest::C::C", "nope::f", "ntest", ""} {
		if _, ok := Global(name); ok {
			t.Errorf("Global(%q) found", name)
		}
	}
	if v, _ := Global("ntest::C"); v.Kind() != value.Class || v.AsClass().Name != "ntest::C" || v.AsClass().Ctor == nil {
		t.Errorf("class: %v", v)
	}
	o := c.Object(42)
	if o.AsObject().State != 42 || o.AsObject().Class != c.ClassObj() {
		t.Errorf("Object: %v", o)
	}
	if o.String() != "ntest::C{}" {
		t.Errorf("an object without Show prints as %s", o.String())
	}

	mustPanic := func(what string, f func()) {
		defer func() {
			if recover() == nil {
				t.Errorf("%s: no panic", what)
			}
		}()
		f()
	}
	mustPanic("a second module ntest", func() { Register(&Module{Name: "ntest"}) })
	mustPanic("a static and a method with one name", func() {
		Register(&Module{Name: "ntest2", Classes: []*Class{{Name: "D", Methods: map[string]value.NativeMethod{"x": meth}, Statics: map[string]Func{"x": fn}}}})
	})
}

func TestArgs(t *testing.T) {
	i, f, s := value.MakeInt(3), value.MakeFloat(2.5), value.MakeString("x")
	tests := []struct {
		args     []value.Value
		min, max int
		msg      string
	}{
		{[]value.Value{i}, 1, 1, ""},
		{nil, 1, 1, "m::f expects 1 argument(s), got 0"},
		{[]value.Value{i, i, i}, 1, 2, "m::f expects 1 to 2 arguments, got 3"},
		{nil, 1, -1, "m::f expects at least 1 argument(s), got 0"},
		{[]value.Value{i, i, i, i}, 1, -1, ""},
	}
	for _, tc := range tests {
		_, err := Check("m::f", tc.args, tc.min, tc.max)
		if (err == nil) != (tc.msg == "") || (err != nil && !strings.Contains(err.Error(), tc.msg)) {
			t.Errorf("Check(%d args, %d, %d) = %v, want %q", len(tc.args), tc.min, tc.max, err, tc.msg)
		}
	}
	a, _ := Check("m::f", []value.Value{i, f, s}, 3, 3)
	if n, err := a.Number(0); n != 3 || err != nil {
		t.Errorf("Number(int) = %v, %v", n, err)
	}
	if n, err := a.Number(1); n != 2.5 || err != nil {
		t.Errorf("Number(float) = %v, %v", n, err)
	}
	if _, err := a.Int(1); err == nil || err.Error() != "m::f: argument 2 must be an int, not float" {
		t.Errorf("Int(float): %v", err)
	}
	if _, err := a.String(0); err == nil || !strings.Contains(err.Error(), "argument 1 must be a string, not int") {
		t.Errorf("String(int): %v", err)
	}
	if a.Has(3) || !a.Has(2) {
		t.Error("Has")
	}
	if err := a.Errorf(value.ValueError, "bad %d", 7); err.Error() != "m::f: bad 7" {
		t.Errorf("Errorf: %v", err)
	}
}

// A member function named like a keyword could never be called (p->local()
// does not parse): Prepare refuses it.
func TestKeywordMember(t *testing.T) {
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(string), "kw::C::local is a keyword") {
			t.Errorf("got %v", r)
		}
	}()
	Prepare(&Module{Name: "kw", Classes: []*Class{{Name: "C", Methods: map[string]value.NativeMethod{"local": nil}}}})
}
