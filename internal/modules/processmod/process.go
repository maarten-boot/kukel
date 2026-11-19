// Package processmod is the native module "process": starting programs,
// feeding them, capturing what they write, connecting them with pipes and
// waiting for them, with time limits and cancellation that leave no
// processes behind (TODO.md Q118, docs/future.md §15). It has no policy of
// its own: the standard module proc, written in Kukel
// (internal/stdlib/proc.kkl), checks the arguments, turns failures into
// proc::ProcError and is what programs use.
//
// A Go program that embeds Kukel gets it only when it names it in
// StandardModules (F-74): it is the largest power a module gives.
package processmod

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

func init() {
	native.Register(&native.Module{
		Name:  "process",
		Doc:   "starting programs and waiting for them (under module proc)",
		OptIn: true,
		Funcs: map[string]native.Func{
			"run":      run,
			"pipe":     pipe,
			"spawn":    spawn,
			"lookpath": lookpath,
			"environ":  environ,
			"signum":   signumFunc,
		},
		Classes: []*native.Class{Child},
	})
}

// process::lookpath(name): the path of the program name stands for (found
// on PATH unless it holds a /), or nil.
func lookpath(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("process::lookpath", args, 1, 1)
	if err != nil {
		return value.NilValue, err
	}
	name, err := a.String(0)
	if err != nil {
		return value.NilValue, err
	}
	p, err := exec.LookPath(name)
	if err != nil || name == "" {
		return value.NilValue, nil
	}
	return value.MakeString(p), nil
}

// process::signum(name): the number of signal name ("TERM"), or nil (also
// on Windows, which has none).
func signumFunc(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("process::signum", args, 1, 1)
	if err != nil {
		return value.NilValue, err
	}
	name, err := a.String(0)
	if err != nil {
		return value.NilValue, err
	}
	if n := signalNumber(name); n > 0 {
		return value.MakeInt(int64(n)), nil
	}
	return value.NilValue, nil
}

// process::environ(): the environment as a map, by name.
func environ(env value.Env, args []value.Value) (value.Value, error) {
	if _, err := native.Check("process::environ", args, 0, 0); err != nil {
		return value.NilValue, err
	}
	vars := os.Environ()
	sort.Strings(vars)
	m := value.NewMap()
	for _, kv := range vars {
		if k, v, ok := strings.Cut(kv, "="); ok && k != "" {
			m.Set(value.MakeString(k), value.MakeString(v))
		}
	}
	return value.MakeMap(m), nil
}

// options are run's, pipe's and spawn's, as proc gives them (checked
// there; checked again here, so that the module cannot be misused alone).
type options struct {
	in     string // "inherit", "null", "data", or "pipe" (spawn)
	data   string // with in "data"
	out    stream
	err    stream
	dir    string
	env    []string // nil: the program's environment
	limit  float64  // seconds, 0: none
	max    int      // bytes captured of each stream
	hasEnv bool
}

// stream is where output goes: "capture", "inherit", "null", "file",
// "append", "pipe" (spawn), or for err also "out".
type stream struct {
	kind, path string
}

// process::run(argv, options): runs a program to its end. It gives a map:
// "started" (false, with "error", when it could not be started), "code"
// (nil after a signal), "signal" and "signum", "out" and "err" (when
// captured), "time" (seconds), "timedout" and "truncated" (true when the
// time limit or the cap on captured output ended it). A failing program
// is not an error here: proc decides.
func run(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("process::run", args, 2, 2)
	if err != nil {
		return value.NilValue, err
	}
	argv, err := argvOf(a, a.V[0], "the command")
	if err != nil {
		return value.NilValue, err
	}
	o, err := optionsOf(a, false)
	if err != nil {
		return value.NilValue, err
	}
	return runJob(env, [][]string{argv}, o)
}

// process::pipe(argvs, options): runs the programs of argvs at the same
// time, the output of each the input of the next, through the operating
// system's pipes; in goes to the first, out comes from the last, err from
// all. It gives run's map for the last program, with "codes", the exit
// codes of all (nil for one a signal ended), and "signals".
func pipe(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("process::pipe", args, 2, 2)
	if err != nil {
		return value.NilValue, err
	}
	v := a.V[0]
	if v.Kind() != value.Vector || len(v.AsVector().Elems) == 0 {
		return value.NilValue, a.TypeError(0, "a vector of commands")
	}
	var argvs [][]string
	for i, c := range v.AsVector().Elems {
		argv, err := argvOf(a, c, fmt.Sprintf("command %d", i+1))
		if err != nil {
			return value.NilValue, err
		}
		argvs = append(argvs, argv)
	}
	o, err := optionsOf(a, false)
	if err != nil {
		return value.NilValue, err
	}
	return runJob(env, argvs, o)
}

// runJob runs a job to its end and gives its result.
func runJob(env value.Env, argvs [][]string, o options) (value.Value, error) {
	j, failed := newJob(env, argvs, o)
	if failed.Kind() != value.Nil {
		return failed, nil
	}
	defer native.Wait(env).Done()
	if failed := j.start(); failed.Kind() != value.Nil {
		return failed, nil
	}
	if _, err := j.wait(env, nil); err != nil {
		return value.NilValue, err
	}
	return j.result(), nil
}

// argvOf checks a command: a vector of strings, not empty, without NUL.
func argvOf(a native.Args, v value.Value, what string) ([]string, error) {
	if v.Kind() != value.Vector || len(v.AsVector().Elems) == 0 {
		return nil, a.Errorf(value.TypeError, "%s must be a vector of strings: the program and its arguments", what)
	}
	var argv []string
	for i, e := range v.AsVector().Elems {
		if e.Kind() != value.String {
			return nil, a.Errorf(value.TypeError, "argument %d of %s must be a string, not %s", i+1, what, e.Kind())
		}
		if strings.IndexByte(e.AsString(), 0) >= 0 {
			return nil, a.Errorf(value.ValueError, "argument %d of %s holds a NUL byte", i+1, what)
		}
		argv = append(argv, e.AsString())
	}
	if argv[0] == "" {
		return nil, a.Errorf(value.ValueError, "the program's name in %s is empty", what)
	}
	return argv, nil
}

func optionsOf(a native.Args, pipes bool) (options, error) {
	o := options{in: "null", out: stream{kind: "capture"}, err: stream{kind: "inherit"}, max: 64 << 20}
	m := a.V[1]
	if m.Kind() != value.Map {
		return o, a.TypeError(1, "a map of options")
	}
	var bad error
	str := func(k string, v value.Value) (string, bool) {
		if v.Kind() != value.String {
			bad = a.Errorf(value.TypeError, "option %s must be a string, not %s", k, v.Kind())
			return "", false
		}
		return v.AsString(), true
	}
	m.AsMap().Each(func(k, v value.Value) {
		if bad != nil {
			return
		}
		key := k.String()
		switch key {
		case "in":
			if s, ok := str(key, v); ok {
				if s != "inherit" && s != "null" && s != "data" && !(pipes && s == "pipe") {
					bad = a.Errorf(value.ValueError, "option in: %q (inherit, null, data%s)", s, map[bool]string{true: ", pipe", false: ""}[pipes])
				}
				o.in = s
			}
		case "data":
			if s, ok := str(key, v); ok {
				o.data = s
			}
		case "out", "err":
			st, err := streamOf(a, key, v, pipes)
			if err != nil {
				bad = err
			} else if key == "out" {
				o.out = st
			} else {
				o.err = st
			}
		case "dir":
			if s, ok := str(key, v); ok {
				o.dir = s
			}
		case "env":
			if v.Kind() != value.Vector {
				bad = a.Errorf(value.TypeError, "option env must be a vector of \"name=value\" strings, not %s", v.Kind())
				return
			}
			o.hasEnv = true
			o.env = []string{}
			for _, e := range v.AsVector().Elems {
				if s, ok := str(key, e); ok {
					o.env = append(o.env, s)
				}
			}
		case "timeout":
			if v.Kind() != value.Int && v.Kind() != value.Float {
				bad = a.Errorf(value.TypeError, "option timeout must be a number of seconds, not %s", v.Kind())
				return
			}
			f := v.AsFloat()
			if v.Kind() == value.Int {
				f = float64(v.AsInt())
			}
			if !(f > 0) || f > 1e9 {
				bad = a.Errorf(value.ValueError, "option timeout: %s seconds (more than 0)", v)
			}
			o.limit = f
		case "max":
			if v.Kind() != value.Int || v.AsInt() < 0 {
				bad = a.Errorf(value.ValueError, "option max must be an int of 0 or more (bytes), not %s", v.Repr())
				return
			}
			o.max = int(min(v.AsInt(), 1<<40))
		default:
			bad = a.Errorf(value.ValueError, "unknown option %s", k.Repr())
		}
	})
	if bad == nil && o.out.kind == "out" {
		bad = a.Errorf(value.ValueError, "option out: \"out\" is only for err")
	}
	return o, bad
}

func streamOf(a native.Args, key string, v value.Value, pipes bool) (stream, error) {
	kinds := "capture, inherit, null"
	if key == "err" {
		kinds += ", out"
	}
	if pipes {
		kinds += ", pipe"
	}
	if v.Kind() == value.String {
		switch s := v.AsString(); s {
		case "capture", "inherit", "null", "out", "pipe":
			if s != "pipe" || pipes {
				return stream{kind: s}, nil
			}
		}
	}
	if v.Kind() == value.Map && v.AsMap().Len() == 1 {
		k := v.AsMap().Keys()[0]
		p, _, _ := v.AsMap().Get(k)
		if (k.String() == "file" || k.String() == "append") && k.Kind() == value.String && p.Kind() == value.String && p.AsString() != "" {
			return stream{kind: k.String(), path: p.AsString()}, nil
		}
	}
	return stream{}, a.Errorf(value.ValueError, "option %s: %s (%s, or {\"file\": path} or {\"append\": path})", key, v.Repr(), kinds)
}

// notStarted is the result for a program that could not be started.
func notStarted(argv []string, err error) value.Value {
	msg := err.Error()
	var ee *exec.Error
	var pe *os.PathError
	switch {
	case errors.As(err, &ee) && errors.Is(ee.Err, exec.ErrNotFound):
		msg = fmt.Sprintf("program %s not found", argv[0])
	case errors.As(err, &pe):
		msg = fmt.Sprintf("%s: %s", pe.Path, native.IOMessage(pe.Err))
	}
	r := value.NewMap()
	r.Set(value.MakeString("started"), value.False)
	r.Set(value.MakeString("error"), value.MakeString(msg))
	return value.MakeMap(r)
}
