package builtins

import (
	"io"
	"math"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

// I/O, conversion, math and system built-ins (milestone 11).

type builtinFn = func(env value.Env, args []value.Value) (value.Value, error)

func init() {
	// ---- I/O ----

	define("eprint", func(env value.Env, args []value.Value) (value.Value, error) {
		flushStdout(env)
		return value.NilValue, write(env.Stderr(), args, "")
	})
	define("eprintln", func(env value.Env, args []value.Value) (value.Value, error) {
		flushStdout(env)
		return value.NilValue, write(env.Stderr(), args, "\n")
	})
	define("readline", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := wantArgs("readline", args, 0); err != nil {
			return value.NilValue, err
		}
		waiting := native.Wait(env) // flushes: a prompt printed before must be visible
		line, err := env.Stdin().ReadString('\n')
		waiting.Done()
		if err != nil && err != io.EOF {
			return value.NilValue, value.Errorf(value.IOError, "readline: %v", err)
		}
		if err == io.EOF && line == "" {
			return value.NilValue, nil
		}
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
		return value.MakeString(line), nil
	})
	define("readfile", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := wantArgs("readfile", args, 1); err != nil {
			return value.NilValue, err
		}
		path, err := argString("readfile", args, 0)
		if err != nil {
			return value.NilValue, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return value.NilValue, ioError("readfile", err)
		}
		return value.MakeString(string(data)), nil
	})
	define("writefile", fileWriter("writefile", os.O_WRONLY|os.O_CREATE|os.O_TRUNC))
	define("appendfile", fileWriter("appendfile", os.O_WRONLY|os.O_CREATE|os.O_APPEND))

	// ---- type and conversion ----

	define("int", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := wantArgs("int", args, 1); err != nil {
			return value.NilValue, err
		}
		switch x := args[0]; x.Kind() {
		case value.Int:
			return x, nil
		case value.Float:
			return floatToInt("int", x.AsFloat(), math.Trunc)
		case value.Decimal:
			return decimalToInt("int", x, value.Down)
		case value.Bool:
			if x.AsBool() {
				return value.MakeInt(1), nil
			}
			return value.MakeInt(0), nil
		case value.String:
			return parseInt(x.AsString())
		default:
			return value.NilValue, value.Errorf(value.TypeError, "int: cannot convert %s", x.Kind())
		}
	})
	define("float", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := wantArgs("float", args, 1); err != nil {
			return value.NilValue, err
		}
		switch x := args[0]; x.Kind() {
		case value.Int:
			return value.MakeFloat(float64(x.AsInt())), nil
		case value.Float:
			return x, nil
		case value.Decimal:
			f := x.DecimalToFloat()
			if math.IsInf(f, 0) {
				return value.NilValue, value.Errorf(value.RangeError, "float: %s is too large", short(x))
			}
			return value.MakeFloat(f), nil
		case value.String:
			return parseFloat(x.AsString())
		default:
			return value.NilValue, value.Errorf(value.TypeError, "float: cannot convert %s", x.Kind())
		}
	})
	define("decimal", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := argRange("decimal", args, 1, 2); err != nil {
			return value.NilValue, err
		}
		var d value.Value
		var err error
		switch x := args[0]; x.Kind() {
		case value.Int:
			d, err = value.DecimalRound(x, 0, value.HalfAway)
		case value.Decimal:
			d = x
		case value.Float:
			if d, err = value.DecimalFromFloat(x.AsFloat()); err != nil {
				return value.NilValue, value.Errorf(value.ValueError, "decimal: %s is not a number a decimal can hold", x)
			}
		case value.String:
			d, err = parseDecimal(x.AsString())
		default:
			return value.NilValue, value.Errorf(value.TypeError, "decimal: cannot convert %s", x.Kind())
		}
		if err != nil {
			return value.NilValue, err
		}
		if len(args) == 2 {
			scale, err := argScale("decimal", args, 1)
			if err != nil {
				return value.NilValue, err
			}
			return value.DecimalRound(d, scale, value.HalfAway)
		}
		return d, nil
	})

	// ---- math ----

	define("abs", func(env value.Env, args []value.Value) (value.Value, error) {
		if len(args) == 1 && args[0].Kind() == value.Decimal {
			if lt, _ := value.Compare(value.Less, args[0], value.MakeInt(0)); lt {
				return value.Neg(args[0])
			}
			return args[0], nil
		}
		x, err := oneNumber("abs", args)
		if err != nil {
			return value.NilValue, err
		}
		if x.Kind() == value.Int {
			if i := x.AsInt(); i < 0 {
				return value.MakeInt(-i), nil // abs(MinInt64) wraps to itself
			}
			return x, nil
		}
		return value.MakeFloat(math.Abs(x.AsFloat())), nil
	})
	define("min", minMax("min", value.Less))
	define("max", minMax("max", value.Greater))
	define("pow", func(env value.Env, args []value.Value) (value.Value, error) {
		if len(args) == 2 && args[0].Kind() == value.Decimal {
			if args[1].Kind() != value.Int {
				return value.NilValue, argTypeError("pow", 1, "an int (the power of a decimal)", args[1])
			}
			return value.DecimalPow(args[0], args[1].AsInt())
		}
		if err := twoNumbers("pow", args); err != nil {
			return value.NilValue, err
		}
		if args[0].Kind() == value.Int && args[1].Kind() == value.Int && args[1].AsInt() >= 0 {
			base, exp, r := args[0].AsInt(), args[1].AsInt(), int64(1)
			for ; exp > 0; exp >>= 1 { // wraps around like *
				if exp&1 == 1 {
					r *= base
				}
				base *= base
			}
			return value.MakeInt(r), nil
		}
		return value.MakeFloat(math.Pow(toFloat(args[0]), toFloat(args[1]))), nil
	})
	define("floor", rounder("floor", math.Floor, value.Floor))
	define("ceil", rounder("ceil", math.Ceil, value.Ceil))
	define("round", func(env value.Env, args []value.Value) (value.Value, error) {
		if len(args) != 2 {
			return rounder("round", math.Round, value.HalfAway)(env, args)
		}
		n, err := argScale("round", args, 1)
		if err != nil {
			return value.NilValue, err
		}
		switch x := args[0]; x.Kind() {
		case value.Int, value.Decimal:
			return value.DecimalRound(x, n, value.HalfAway)
		case value.Float:
			return value.MakeFloat(value.RoundFloat(x.AsFloat(), n)), nil
		default:
			return value.NilValue, argTypeError("round", 0, "a number", x)
		}
	})
	for name, f := range map[string]func(float64) float64{
		"sqrt": math.Sqrt, "sin": math.Sin, "cos": math.Cos, "tan": math.Tan,
		"log": math.Log, "exp": math.Exp,
	} {
		define(name, floatFunc(name, f))
	}
	define("atan2", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := twoNumbers("atan2", args); err != nil {
			return value.NilValue, err
		}
		return value.MakeFloat(math.Atan2(toFloat(args[0]), toFloat(args[1]))), nil
	})
	define("random", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := wantArgs("random", args, 0); err != nil {
			return value.NilValue, err
		}
		return value.MakeFloat(env.Rand().Float64()), nil
	})
	define("randint", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := wantArgs("randint", args, 2); err != nil {
			return value.NilValue, err
		}
		lo, err := argInt("randint", args, 0)
		if err != nil {
			return value.NilValue, err
		}
		hi, err := argInt("randint", args, 1)
		if err != nil {
			return value.NilValue, err
		}
		if lo > hi {
			return value.NilValue, value.Errorf(value.RangeError, "randint: %d is greater than %d", lo, hi)
		}
		span := uint64(hi) - uint64(lo) // the width fits in a uint64
		if span == math.MaxUint64 {
			return value.MakeInt(int64(env.Rand().Uint64())), nil
		}
		return value.MakeInt(lo + int64(env.Rand().Uint64N(span+1))), nil
	})
	define("seed", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := wantArgs("seed", args, 1); err != nil {
			return value.NilValue, err
		}
		n, err := argInt("seed", args, 0)
		if err != nil {
			return value.NilValue, err
		}
		*env.Rand() = *NewRand(uint64(n))
		return value.NilValue, nil
	})

	// ---- system ----

	define("time", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := wantArgs("time", args, 0); err != nil {
			return value.NilValue, err
		}
		return value.MakeFloat(float64(time.Now().UnixNano()) / 1e9), nil
	})
	define("clock", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := wantArgs("clock", args, 0); err != nil {
			return value.NilValue, err
		}
		return value.MakeFloat(time.Since(env.Started()).Seconds()), nil
	})
	define("exit", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := argRange("exit", args, 0, 1); err != nil {
			return value.NilValue, err
		}
		code := int64(0)
		if len(args) == 1 {
			var err error
			if code, err = argInt("exit", args, 0); err != nil {
				return value.NilValue, err
			}
		}
		return value.NilValue, &value.ExitError{Code: int(code)}
	})
	define("program", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := wantArgs("program", args, 0); err != nil {
			return value.NilValue, err
		}
		return value.MakeString(native.Program(env)), nil
	})
	define("getenv", func(env value.Env, args []value.Value) (value.Value, error) {
		if err := wantArgs("getenv", args, 1); err != nil {
			return value.NilValue, err
		}
		name, err := argString("getenv", args, 0)
		if err != nil {
			return value.NilValue, err
		}
		if v, ok := os.LookupEnv(name); ok {
			return value.MakeString(v), nil
		}
		return value.NilValue, nil
	})
}

// NewRand returns the random source seed(n) sets up.
func NewRand(n uint64) *rand.Rand {
	return rand.New(rand.NewPCG(n, n^0x9e3779b97f4a7c15))
}

// flushStdout writes out buffered standard output (before writing to
// standard error, or reading input after a prompt).
func flushStdout(env value.Env) {
	if f, ok := env.Stdout().(interface{ Flush() error }); ok {
		f.Flush()
	}
}

func ioError(name string, err error) error {
	if pe, ok := err.(*os.PathError); ok {
		return value.Errorf(value.IOError, "%s: %s: %v", name, pe.Path, pe.Err)
	}
	return value.Errorf(value.IOError, "%s: %v", name, err)
}

func fileWriter(name string, flags int) builtinFn {
	return func(env value.Env, args []value.Value) (value.Value, error) {
		if err := wantArgs(name, args, 2); err != nil {
			return value.NilValue, err
		}
		path, err := argString(name, args, 0)
		if err != nil {
			return value.NilValue, err
		}
		text, err := argString(name, args, 1)
		if err != nil {
			return value.NilValue, err
		}
		f, err := os.OpenFile(path, flags, 0o644)
		if err != nil {
			return value.NilValue, ioError(name, err)
		}
		_, err = f.WriteString(text)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return value.NilValue, ioError(name, err)
		}
		return value.NilValue, nil
	}
}

// parseInt reads an int written like a literal: decimal, 0x hexadecimal or
// 0b binary, an optional sign, "_" separators, surrounding white space.
func parseInt(s string) (value.Value, error) {
	t := strings.ReplaceAll(strings.TrimSpace(s), "_", "")
	neg := false
	if t != "" && (t[0] == '-' || t[0] == '+') {
		neg, t = t[0] == '-', t[1:]
	}
	base := 10
	switch {
	case len(t) > 2 && (t[:2] == "0x" || t[:2] == "0X"):
		base, t = 16, t[2:]
	case len(t) > 2 && (t[:2] == "0b" || t[:2] == "0B"):
		base, t = 2, t[2:]
	}
	u, err := strconv.ParseUint(t, base, 64)
	if err != nil {
		if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
			return value.NilValue, value.Errorf(value.RangeError, "int: %q is too large", s)
		}
		return value.NilValue, value.Errorf(value.ValueError, "int: %q is not an integer", s)
	}
	if base == 10 && (!neg && u > math.MaxInt64 || neg && u > 1<<63) {
		return value.NilValue, value.Errorf(value.RangeError, "int: %q is too large", s)
	}
	i := int64(u) // hexadecimal and binary: any 64-bit pattern, as in literals
	if neg {
		i = -i
	}
	return value.MakeInt(i), nil
}

// parseFloat reads a float (also an integer, "inf" or "nan"), with "_"
// separators and surrounding white space.
func parseFloat(s string) (value.Value, error) {
	f, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(s), "_", ""), 64)
	if err != nil {
		ne, ok := err.(*strconv.NumError)
		switch {
		case ok && ne.Err == strconv.ErrRange && math.IsInf(f, 0):
			return value.NilValue, value.Errorf(value.RangeError, "float: %q is too large", s)
		case !(ok && ne.Err == strconv.ErrRange): // an underflow just gives 0
			return value.NilValue, value.Errorf(value.ValueError, "float: %q is not a number", s)
		}
	}
	return value.MakeFloat(f), nil
}

// floatToInt rounds f with r and converts it to an int.
func floatToInt(name string, f float64, r func(float64) float64) (value.Value, error) {
	g := r(f)
	if g != g || g < -9223372036854775808.0 || g >= 9223372036854775808.0 {
		return value.NilValue, value.Errorf(value.RangeError, "%s: %s does not fit in an int", name, value.FormatFloat(f))
	}
	return value.MakeInt(int64(g)), nil
}

// decimalRefused is the error of a function that gives a float, given a
// decimal (sqrt, sin, atan2, pow of a non-decimal, ...).
func decimalRefused(name string) error {
	return value.Errorf(value.TypeError, "%s: takes a float or an int, not a decimal (convert it with float(x))", name)
}

// decimalToInt rounds a decimal to an int by mode.
func decimalToInt(name string, x value.Value, mode value.RoundMode) (value.Value, error) {
	n, err := value.DecimalToInt(x, mode)
	if err != nil {
		return value.NilValue, value.Errorf(value.RangeError, "%s: %s does not fit in an int", name, short(x))
	}
	return value.MakeInt(n), nil
}

// short gives a decimal's text for a message, its middle cut when it is
// long (a decimal may have 1000 digits).
func short(x value.Value) string {
	if s := x.String(); len(s) > 40 {
		return s[:20] + "..." + s[len(s)-17:]
	}
	return x.String()
}

// argScale gives argument i as a scale: an int from 0 to 1000.
func argScale(name string, args []value.Value, i int) (int, error) {
	n, err := argInt(name, args, i)
	if err != nil {
		return 0, err
	}
	if n < 0 || n > value.MaxDecimalScale {
		return 0, value.Errorf(value.RangeError, "%s: %d digits after the point; 0 to %d are possible", name, n, value.MaxDecimalScale)
	}
	return int(n), nil
}

// parseDecimal reads a decimal from a string as decimal() does: spaces
// around it allowed, every _ ignored (as for int and float).
func parseDecimal(s string) (value.Value, error) {
	d, err := value.ParseDecimal(strings.ReplaceAll(strings.TrimSpace(s), "_", ""))
	if err != nil {
		if e, ok := err.(*value.Error); ok && e.Kind == value.RangeError {
			return value.NilValue, value.Errorf(value.RangeError, "decimal: %q has more than %d digits or a scale above %d", s, value.MaxDecimalDigits, value.MaxDecimalScale)
		}
		return value.NilValue, value.Errorf(value.ValueError, "decimal: %q is not a number", s)
	}
	return d, nil
}

func toFloat(v value.Value) float64 {
	if v.Kind() == value.Int {
		return float64(v.AsInt())
	}
	return v.AsFloat()
}

func isNumber(v value.Value) bool { return v.Kind() == value.Int || v.Kind() == value.Float }

func oneNumber(name string, args []value.Value) (value.Value, error) {
	if err := wantArgs(name, args, 1); err != nil {
		return value.NilValue, err
	}
	if args[0].Kind() == value.Decimal {
		return value.NilValue, decimalRefused(name)
	}
	if !isNumber(args[0]) {
		return value.NilValue, argTypeError(name, 0, "a number", args[0])
	}
	return args[0], nil
}

func twoNumbers(name string, args []value.Value) error {
	if err := wantArgs(name, args, 2); err != nil {
		return err
	}
	for i := range args {
		if args[i].Kind() == value.Decimal {
			return decimalRefused(name)
		}
		if !isNumber(args[i]) {
			return argTypeError(name, i, "a number", args[i])
		}
	}
	return nil
}

// rounder: floor, ceil and round give ints (an int argument is returned
// as it is); round rounds halves away from zero.
func rounder(name string, r func(float64) float64, mode value.RoundMode) builtinFn {
	return func(env value.Env, args []value.Value) (value.Value, error) {
		if len(args) == 1 && args[0].Kind() == value.Decimal {
			return decimalToInt(name, args[0], mode)
		}
		x, err := oneNumber(name, args)
		if err != nil || x.Kind() == value.Int {
			return x, err
		}
		return floatToInt(name, x.AsFloat(), r)
	}
}

func floatFunc(name string, f func(float64) float64) builtinFn {
	return func(env value.Env, args []value.Value) (value.Value, error) {
		x, err := oneNumber(name, args)
		if err != nil {
			return value.NilValue, err
		}
		return value.MakeFloat(f(toFloat(x))), nil
	}
}

// minMax: min(a, b, ...) or min(vector), of numbers or strings, compared
// like < and >.
func minMax(name string, better value.CmpOp) builtinFn {
	return func(env value.Env, args []value.Value) (value.Value, error) {
		vals := args
		if len(args) == 1 && args[0].Kind() == value.Vector {
			vals = args[0].AsVector().Elems
		}
		if len(vals) == 0 {
			return value.NilValue, value.Errorf(value.ArgumentError, "%s: no values", name)
		}
		best := vals[0]
		if !isNumber(best) && best.Kind() != value.Decimal && best.Kind() != value.String {
			return value.NilValue, value.Errorf(value.TypeError, "%s: cannot compare %s", name, best.Kind())
		}
		for _, v := range vals[1:] {
			b, err := value.Compare(better, v, best)
			if err != nil {
				return value.NilValue, value.Errorf(value.TypeError, "%s: cannot compare %s and %s", name, best.Kind(), v.Kind())
			}
			if b {
				best = v
			}
		}
		return best, nil
	}
}
