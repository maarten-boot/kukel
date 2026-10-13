// Package stringsmod is the native module "strings": a Builder class that
// builds a long string piece by piece without copying it again for every
// piece (s += x copies s each time).
//
//	use strings;
//	local b = new strings::Builder();
//	for (local i = 0; i < 3; i++) b->add(i, ",");
//	println(b->str());          // 0,1,2,
package stringsmod

import (
	"fmt"
	"strings"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

// Builder is the class strings::Builder.
var Builder = &native.Class{
	Name: "Builder",
	// new strings::Builder([s]): an empty builder, or one starting with s.
	New: func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
		a, err := native.Check("strings::Builder::Builder", args, 0, 1)
		if err != nil {
			return value.NilValue, err
		}
		b := &strings.Builder{}
		if a.Has(0) {
			s, err := a.String(0)
			if err != nil {
				return value.NilValue, err
			}
			b.WriteString(s)
		}
		this.State = b
		return value.NilValue, nil
	},
	Methods: map[string]value.NativeMethod{
		// add(x..) appends each value as print shows it, and gives the
		// builder (so calls can be chained).
		"add": func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
			b := this.State.(*strings.Builder)
			for _, v := range args {
				b.WriteString(v.String())
			}
			return value.ObjectValue(this), nil
		},
		// len() is the length in bytes.
		"len": func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
			if _, err := native.Check("strings::Builder::len", args, 0, 0); err != nil {
				return value.NilValue, err
			}
			return value.MakeInt(int64(this.State.(*strings.Builder).Len())), nil
		},
		// str() is the string built so far.
		"str": func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
			if _, err := native.Check("strings::Builder::str", args, 0, 0); err != nil {
				return value.NilValue, err
			}
			return value.MakeString(this.State.(*strings.Builder).String()), nil
		},
		// reset() empties the builder.
		"reset": func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
			if _, err := native.Check("strings::Builder::reset", args, 0, 0); err != nil {
				return value.NilValue, err
			}
			this.State.(*strings.Builder).Reset()
			return value.NilValue, nil
		},
	},
	Show: func(o *value.ObjectObj) string {
		return fmt.Sprintf("strings::Builder(%d bytes)", o.State.(*strings.Builder).Len())
	},
}

func init() {
	native.Register(&native.Module{
		Name:    "strings",
		Doc:     "Builder: build long strings piece by piece",
		Classes: []*native.Class{Builder},
	})
}
