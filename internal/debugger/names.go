package debugger

import (
	"strings"

	"github.com/maarten-boot/kukel/internal/value"
	"github.com/maarten-boot/kukel/internal/vm"
)

// Lookup finds a name as the function of frame i sees it: this, a
// variable, a data member of this, a static data member of its class, a
// global of its module, a global; or a qualified name: C::x (a static data
// member, of the module's class C first) or a module's global
// (util::count).
func Lookup(m *vm.VM, i int, name string) (value.Value, bool) {
	f := m.Frames()[i]
	module, class := scope(m, f)
	if prefix, member, ok := cutLast(name, "::"); ok {
		for _, c := range qualified(module, prefix) {
			if k, ok := m.Global(c); ok && k.Kind() == value.Class {
				return static(k.AsClass(), member)
			}
		}
		return m.Global(name)
	}
	if name == "this" {
		if f.This == nil {
			return value.NilValue, false
		}
		return value.ObjectValue(f.This), true
	}
	vars := m.Vars(i)
	for j := len(vars) - 1; j >= 0; j-- { // of two with the name, the inner one
		if vars[j].Name == name {
			return vars[j].Value, true
		}
	}
	if f.This != nil {
		if slot, ok := f.This.Class.FieldIndex[name]; ok {
			return f.This.Fields[slot], true
		}
	}
	if v, ok := static(class, name); ok {
		return v, true
	}
	if module != "" {
		if v, ok := m.Global(module + "::" + name); ok {
			return v, true
		}
	}
	return m.Global(name)
}

// Assign gives the variable name, as Lookup finds it in frame i, the value
// v: a variable of the frame, a data member of this, a static data member,
// a global variable. It reports whether there is one that can change.
func Assign(m *vm.VM, i int, name string, v value.Value) bool {
	f := m.Frames()[i]
	module, class := scope(m, f)
	if prefix, member, ok := cutLast(name, "::"); ok {
		for _, c := range qualified(module, prefix) {
			if k, ok := m.Global(c); ok && k.Kind() == value.Class {
				return setStatic(k.AsClass(), member, v)
			}
		}
		return m.SetGlobal(name, v)
	}
	if m.SetVar(i, name, v) {
		return true
	}
	if f.This != nil {
		if slot, ok := f.This.Class.FieldIndex[name]; ok {
			f.This.Fields[slot] = v
			return true
		}
	}
	if setStatic(class, name, v) {
		return true
	}
	if module != "" && m.SetGlobal(module+"::"+name, v) {
		return true
	}
	return m.SetGlobal(name, v)
}

// qualified gives the names a class written as prefix may have from
// module: the module's own class first.
func qualified(module, prefix string) []string {
	if module != "" {
		return []string{module + "::" + prefix, prefix}
	}
	return []string{prefix}
}

// scope gives the module and the class of a frame's function, from its
// name: util::A::m/fn@3 is in module util and class util::A.
func scope(m *vm.VM, f vm.DebugFrame) (module string, class *value.ClassObj) {
	name, _, _ := strings.Cut(f.Func.FuncName, "/")
	parts := strings.Split(name, "::")
	parts = parts[:len(parts)-1] // the function's own name
	if len(parts) > 0 {
		if k, ok := m.Global(strings.Join(parts, "::")); ok && k.Kind() == value.Class {
			class = k.AsClass()
			parts = parts[:len(parts)-1]
		}
	}
	if len(parts) > 0 {
		module = parts[0]
	}
	return module, class
}

// static gives the static data member name of class k or of a base class.
func static(k *value.ClassObj, name string) (value.Value, bool) {
	for ; k != nil; k = k.Base {
		for i, s := range k.StaticNames {
			if s == name {
				return k.Statics[i], true
			}
		}
	}
	return value.NilValue, false
}

func setStatic(k *value.ClassObj, name string, v value.Value) bool {
	for ; k != nil; k = k.Base {
		for i, s := range k.StaticNames {
			if s == name {
				k.Statics[i] = v
				return true
			}
		}
	}
	return false
}

// cutLast cuts s around the last sep.
func cutLast(s, sep string) (before, after string, found bool) {
	if i := strings.LastIndex(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):], true
	}
	return s, "", false
}
