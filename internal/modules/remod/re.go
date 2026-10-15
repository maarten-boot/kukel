// Package remod is the native module "re": regular expressions, with Go's
// RE2 syntax (TODO.md Q59). Matching takes time linear in the text: there
// is no backtracking (so no back references), and no pattern can make a
// match run away.
//
//	use re;
//	local mail = re::compile("(\\w+)@(\\w+)\\.com", "i");
//	println(mail->groups("Mail ANN@example.com"));   // ["ANN@example.com", "ANN", "example"]
//	println(mail->replace("a@b.com", "$2:$1"));        // b:a
package remod

import (
	"regexp"
	"strings"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

// Regexp is the class re::Regexp: a compiled pattern.
var Regexp = &native.Class{Name: "Regexp"}

func init() {
	Regexp.New = func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
		r, err := compile(native.Args{Name: "re::Regexp::Regexp", V: args})
		if err != nil {
			return value.NilValue, err
		}
		this.State = r
		return value.NilValue, nil
	}
	Regexp.Methods = map[string]value.NativeMethod{
		"match": method("match", 1, 1, func(a native.Args, env value.Env, r *regexp.Regexp) (value.Value, error) {
			s, err := a.String(0)
			if err != nil {
				return value.NilValue, err
			}
			return value.MakeBool(r.MatchString(s)), nil
		}),
		"find": method("find", 1, 1, func(a native.Args, env value.Env, r *regexp.Regexp) (value.Value, error) {
			s, err := a.String(0)
			if err != nil {
				return value.NilValue, err
			}
			loc := r.FindStringIndex(s)
			if loc == nil {
				return value.NilValue, nil
			}
			return value.MakeString(s[loc[0]:loc[1]]), nil
		}),
		"findall": method("findall", 1, 2, func(a native.Args, env value.Env, r *regexp.Regexp) (value.Value, error) {
			s, err := a.String(0)
			if err != nil {
				return value.NilValue, err
			}
			n, err := count(a, 1)
			if err != nil {
				return value.NilValue, err
			}
			return strs(r.FindAllString(s, n)), nil
		}),
		"groups": method("groups", 1, 1, func(a native.Args, env value.Env, r *regexp.Regexp) (value.Value, error) {
			s, err := a.String(0)
			if err != nil {
				return value.NilValue, err
			}
			loc := r.FindStringSubmatchIndex(s)
			if loc == nil {
				return value.NilValue, nil
			}
			out := make([]value.Value, len(loc)/2)
			for i := range out {
				if loc[2*i] >= 0 { // a group that took no part stays nil
					out[i] = value.MakeString(s[loc[2*i]:loc[2*i+1]])
				}
			}
			return value.MakeVector(out), nil
		}),
		"replace": method("replace", 2, 2, replace),
		"split": method("split", 1, 2, func(a native.Args, env value.Env, r *regexp.Regexp) (value.Value, error) {
			s, err := a.String(0)
			if err != nil {
				return value.NilValue, err
			}
			n, err := count(a, 1)
			if err != nil {
				return value.NilValue, err
			}
			return strs(r.Split(s, n)), nil
		}),
		"names": method("names", 0, 0, func(a native.Args, env value.Env, r *regexp.Regexp) (value.Value, error) {
			return strs(r.SubexpNames()[1:]), nil
		}),
		"pattern": method("pattern", 0, 0, func(a native.Args, env value.Env, r *regexp.Regexp) (value.Value, error) {
			return value.MakeString(r.String()), nil
		}),
	}
	Regexp.Show = func(o *value.ObjectObj) string {
		return "re::Regexp(" + value.MakeString(o.State.(*regexp.Regexp).String()).Repr() + ")"
	}
	native.Register(&native.Module{
		Name: "re",
		Doc:  "regular expressions (Go's RE2 syntax)",
		Funcs: map[string]native.Func{
			"compile": func(env value.Env, args []value.Value) (value.Value, error) {
				r, err := compile(native.Args{Name: "re::compile", V: args})
				if err != nil {
					return value.NilValue, err
				}
				return Regexp.Object(r), nil
			},
			"match": func(env value.Env, args []value.Value) (value.Value, error) {
				a, err := native.Check("re::match", args, 2, 2)
				if err != nil {
					return value.NilValue, err
				}
				r, err := compile(native.Args{Name: a.Name, V: args[:1]})
				if err != nil {
					return value.NilValue, err
				}
				s, err := a.String(1)
				if err != nil {
					return value.NilValue, err
				}
				return value.MakeBool(r.MatchString(s)), nil
			},
			"quote": func(env value.Env, args []value.Value) (value.Value, error) {
				a, err := native.Check("re::quote", args, 1, 1)
				if err != nil {
					return value.NilValue, err
				}
				s, err := a.String(0)
				if err != nil {
					return value.NilValue, err
				}
				return value.MakeString(regexp.QuoteMeta(s)), nil
			},
		},
		Classes: []*native.Class{Regexp},
	})
}

// compile reads (pattern [, flags]): flags are letters of "imsU" (ignore
// case, ^ and $ at line ends, . matches \n too, lazy repetition).
func compile(a native.Args) (*regexp.Regexp, error) {
	a, err := native.Check(a.Name, a.V, 1, 2)
	if err != nil {
		return nil, err
	}
	pattern, err := a.String(0)
	if err != nil {
		return nil, err
	}
	if a.Has(1) {
		flags, err := a.String(1)
		if err != nil {
			return nil, err
		}
		if flags != "" {
			if strings.Trim(flags, "imsU") != "" {
				return nil, a.Errorf(value.ValueError, "unknown flag in %q (the flags are i, m, s and U)", flags)
			}
			pattern = "(?" + flags + ")" + pattern
		}
	}
	r, err := regexp.Compile(pattern)
	if err != nil {
		return nil, a.Errorf(value.ValueError, "%v", err)
	}
	return r, nil
}

// replace is r->replace(s, repl): every match replaced by repl, a string in
// which $1, ${1} and ${name} stand for groups ($$ for a $), or a function
// that gets the match and gives its replacement.
func replace(a native.Args, env value.Env, r *regexp.Regexp) (value.Value, error) {
	s, err := a.String(0)
	if err != nil {
		return value.NilValue, err
	}
	switch f := a.V[1]; f.Kind() {
	case value.String:
		return value.MakeString(r.ReplaceAllString(s, f.AsString())), nil
	case value.Function, value.Closure, value.Builtin, value.BoundMethod:
		var failed error
		out := r.ReplaceAllStringFunc(s, func(m string) string {
			if failed != nil {
				return m
			}
			v, err := env.Call(f, []value.Value{value.MakeString(m)})
			switch {
			case err != nil:
				failed = err
			case v.Kind() != value.String:
				failed = a.Errorf(value.TypeError, "the replacement function gave %s, not a string", v.Kind())
			default:
				return v.AsString()
			}
			return m
		})
		if failed != nil {
			return value.NilValue, failed
		}
		return value.MakeString(out), nil
	}
	return value.NilValue, a.TypeError(1, "a string or a function")
}

// count reads the optional maximum number of results at i (default: all).
func count(a native.Args, i int) (int, error) {
	if !a.Has(i) {
		return -1, nil
	}
	n, err := a.Int(i)
	if err != nil {
		return 0, err
	}
	if n < 0 {
		return -1, nil
	}
	return int(min(n, 1<<31-1)), nil
}

func strs(ss []string) value.Value {
	out := make([]value.Value, len(ss))
	for i, s := range ss {
		out[i] = value.MakeString(s)
	}
	return value.MakeVector(out)
}

// method makes a member function of re::Regexp that checks its arguments.
func method(name string, min, max int, fn func(a native.Args, env value.Env, r *regexp.Regexp) (value.Value, error)) value.NativeMethod {
	full := "re::Regexp::" + name
	return func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
		a, err := native.Check(full, args, min, max)
		if err != nil {
			return value.NilValue, err
		}
		return fn(a, env, this.State.(*regexp.Regexp))
	}
}
