// Package timemod is the native module "time": the class time::Time (an
// instant with a time zone), and sleep.
//
//	use time;
//	local t = new time::Time(2026, 10, 14, 9, 30, 0);       // UTC
//	println(t->format("%d.%m.%Y %H:%M"), t->weekday());     // 14.10.2026 09:30 3
//	local u = time::Time::parse("2026-12-25", "%Y-%m-%d");
//	println(u->sub(t) / 86400);                             // days until then
package timemod

import (
	"fmt"
	"math"
	stdtime "time"
	_ "time/tzdata" // time zones by name on every system

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

// Time is the class time::Time; its objects hold a time.Time.
var Time = &native.Class{Name: "Time"}

func init() {
	Time.New = newTime
	Time.Methods = map[string]value.NativeMethod{
		"year":       field("year", func(t stdtime.Time) int64 { return int64(t.Year()) }),
		"month":      field("month", func(t stdtime.Time) int64 { return int64(t.Month()) }),
		"day":        field("day", func(t stdtime.Time) int64 { return int64(t.Day()) }),
		"hour":       field("hour", func(t stdtime.Time) int64 { return int64(t.Hour()) }),
		"minute":     field("minute", func(t stdtime.Time) int64 { return int64(t.Minute()) }),
		"second":     field("second", func(t stdtime.Time) int64 { return int64(t.Second()) }),
		"nanosecond": field("nanosecond", func(t stdtime.Time) int64 { return int64(t.Nanosecond()) }),
		"weekday":    field("weekday", func(t stdtime.Time) int64 { return int64(t.Weekday()) }),
		"yearday":    field("yearday", func(t stdtime.Time) int64 { return int64(t.YearDay()) }),
		"offset":     field("offset", func(t stdtime.Time) int64 { _, off := t.Zone(); return int64(off) }),
		"unix":       method("unix", 0, 0, func(a native.Args, t stdtime.Time) (value.Value, error) { return value.MakeFloat(unixSeconds(t)), nil }),
		"zone":       method("zone", 0, 0, func(a native.Args, t stdtime.Time) (value.Value, error) { return value.MakeString(zoneName(t)), nil }),
		"format":     method("format", 0, 1, formatMethod),
		"add":        method("add", 1, 1, add),
		"sub":        method("sub", 1, 1, sub),
		"before":     compare("before", func(c int) bool { return c < 0 }),
		"after":      compare("after", func(c int) bool { return c > 0 }),
		"equal":      compare("equal", func(c int) bool { return c == 0 }),
		"compare": method("compare", 1, 1, func(a native.Args, t stdtime.Time) (value.Value, error) {
			u, err := other(a)
			return value.MakeInt(int64(t.Compare(u))), err
		}),
		"utc":     method("utc", 0, 0, func(a native.Args, t stdtime.Time) (value.Value, error) { return Time.Object(t.UTC()), nil }),
		"inlocal": method("inlocal", 0, 0, func(a native.Args, t stdtime.Time) (value.Value, error) { return Time.Object(t.Local()), nil }),
		"inzone":  method("inzone", 1, 1, inZone),
	}
	Time.Statics = map[string]native.Func{
		"parse":    parse,
		"fromunix": fromUnix,
	}
	Time.Show = func(o *value.ObjectObj) string {
		return "time::Time(" + o.State.(stdtime.Time).Format(stdtime.RFC3339Nano) + ")"
	}
	native.Register(&native.Module{
		Name: "time",
		Doc:  "time::Time (dates and times with time zones), sleep",
		Funcs: map[string]native.Func{
			"now":   now,
			"sleep": sleep,
		},
		Classes: []*native.Class{Time},
	})
}

// method makes a member function that checks its arguments and gets the
// object's time.
func method(name string, min, max int, fn func(a native.Args, t stdtime.Time) (value.Value, error)) value.NativeMethod {
	full := "time::Time::" + name
	return func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
		a, err := native.Check(full, args, min, max)
		if err != nil {
			return value.NilValue, err
		}
		return fn(a, this.State.(stdtime.Time))
	}
}

func field(name string, get func(stdtime.Time) int64) value.NativeMethod {
	return method(name, 0, 0, func(a native.Args, t stdtime.Time) (value.Value, error) { return value.MakeInt(get(t)), nil })
}

func compare(name string, test func(int) bool) value.NativeMethod {
	return method(name, 1, 1, func(a native.Args, t stdtime.Time) (value.Value, error) {
		u, err := other(a)
		return value.MakeBool(test(t.Compare(u))), err
	})
}

// other is the Time argument of a comparison or sub.
func other(a native.Args) (stdtime.Time, error) {
	s, err := a.Object(0, Time)
	if err != nil {
		return stdtime.Time{}, err
	}
	return s.(stdtime.Time), nil
}

// new time::Time(year, month, day [, hour [, minute [, second [, zone]]]]):
// second may have a fraction; zone is a name such as "Europe/Amsterdam",
// "Local" or "UTC" (the default). Parts out of range carry over; a time
// beyond about 2.9e11 years from 1970 is a ValueError.
func newTime(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
	a, err := native.Check("time::Time::Time", args, 3, 7)
	if err != nil {
		return value.NilValue, err
	}
	var parts [5]int64 // year, month, day, hour, minute
	for i := range parts {
		if a.Has(i) {
			if parts[i], err = a.Int(i); err != nil {
				return value.NilValue, err
			}
		}
	}
	var sec, nsec int64
	if a.Has(5) {
		if sec, nsec, err = seconds(a, 5); err != nil {
			return value.NilValue, err
		}
	}
	loc := stdtime.UTC
	if a.Has(6) {
		if loc, err = location(a, 6); err != nil {
			return value.NilValue, err
		}
	}
	// Roughly how far from 1970 the parts reach, as floats (which do not
	// wrap around): Go's time.Date would wrap silently beyond int64.
	approx := (float64(parts[0])-1970)*3.16e7 + float64(parts[1])*2.7e6 + float64(parts[2])*86400 +
		float64(parts[3])*3600 + float64(parts[4])*60 + float64(sec)
	if approx >= maxUnix || approx <= -maxUnix {
		return value.NilValue, a.Errorf(value.ValueError, "the time is out of range (about 2.9e11 years from 1970)")
	}
	this.State = stdtime.Date(int(parts[0]), stdtime.Month(parts[1]), int(parts[2]), int(parts[3]), int(parts[4]),
		int(sec), int(nsec), loc)
	return value.NilValue, nil
}

// seconds reads argument i, a number of seconds, as whole seconds and
// nanoseconds (0 to 999999999). nan, inf and numbers beyond about 2.9e11
// years are a ValueError.
func seconds(a native.Args, i int) (whole, nsec int64, err error) {
	s, err := a.Number(i)
	if err != nil {
		return 0, 0, err
	}
	if math.IsNaN(s) || math.IsInf(s, 0) || math.Abs(s) >= 9e18 {
		return 0, 0, a.Errorf(value.ValueError, "%s seconds is out of range", value.FormatFloat(s))
	}
	w := math.Floor(s)
	ns := math.Round((s - w) * 1e9)
	if ns >= 1e9 {
		w, ns = w+1, ns-1e9
	}
	return int64(w), int64(ns), nil
}

func location(a native.Args, i int) (*stdtime.Location, error) {
	name, err := a.String(i)
	if err != nil {
		return nil, err
	}
	loc, err := stdtime.LoadLocation(name)
	if err != nil {
		return nil, a.Errorf(value.ValueError, "unknown time zone %q", name)
	}
	return loc, nil
}

// zoneName is the zone's name, as inzone("...") takes it.
func zoneName(t stdtime.Time) string {
	if name := t.Location().String(); name != "" {
		return name
	}
	if name, _ := t.Zone(); name != "" {
		return name
	}
	return t.Format("-07:00") // a fixed offset from parsing
}

func unixSeconds(t stdtime.Time) float64 {
	return float64(t.Unix()) + float64(t.Nanosecond())/1e9
}

// time::now(): the current time, in the local time zone.
func now(env value.Env, args []value.Value) (value.Value, error) {
	if _, err := native.Check("time::now", args, 0, 0); err != nil {
		return value.NilValue, err
	}
	return Time.Object(stdtime.Now()), nil
}

// time::sleep(seconds): waits that long (seconds may have a fraction).
func sleep(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("time::sleep", args, 1, 1)
	if err != nil {
		return value.NilValue, err
	}
	d, err := a.Seconds(0, "a usable duration")
	if err != nil {
		return value.NilValue, err
	}
	native.Flush(env)
	t := stdtime.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return value.NilValue, nil
	case <-env.Done(): // the program was cancelled
		return value.NilValue, env.Err()
	}
}

// time::Time::fromunix(seconds): the time that many seconds after
// 1970-01-01T00:00:00Z, in UTC.
func fromUnix(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("time::Time::fromunix", args, 1, 1)
	if err != nil {
		return value.NilValue, err
	}
	sec, nsec, err := seconds(a, 0)
	if err != nil {
		return value.NilValue, err
	}
	return Time.Object(stdtime.Unix(sec, nsec).UTC()), nil
}

// t->add(seconds): the time that many seconds later (earlier if negative).
func add(a native.Args, t stdtime.Time) (value.Value, error) {
	sec, nsec, err := seconds(a, 0)
	if err != nil {
		return value.NilValue, err
	}
	// Through Unix seconds, not a Duration: those end at about 292 years.
	if float64(t.Unix())+float64(sec) >= maxUnix || float64(t.Unix())+float64(sec) <= -maxUnix {
		return value.NilValue, a.Errorf(value.ValueError, "the result is out of range (about 2.9e11 years from 1970)")
	}
	return Time.Object(stdtime.Unix(t.Unix()+sec, int64(t.Nanosecond())+nsec).In(t.Location())), nil
}

// maxUnix bounds the Unix seconds of a time (about 2.9e11 years): beyond
// it the int64 arithmetic of Go's time package wraps around.
const maxUnix = 9e18

// t->sub(u): the seconds from u to t, as a float.
func sub(a native.Args, t stdtime.Time) (value.Value, error) {
	u, err := other(a)
	if err != nil {
		return value.NilValue, err
	}
	// Not t.Sub(u): a Duration ends at about 292 years; and the seconds are
	// subtracted as floats, which cannot wrap around.
	return value.MakeFloat(float64(t.Unix()) - float64(u.Unix()) + float64(t.Nanosecond()-u.Nanosecond())/1e9), nil
}

// t->inzone(zone): the same instant in another time zone.
func inZone(a native.Args, t stdtime.Time) (value.Value, error) {
	loc, err := location(a, 0)
	if err != nil {
		return value.NilValue, err
	}
	return Time.Object(t.In(loc)), nil
}

// t->format([format]): t as text; without a format in RFC 3339
// ("2026-10-14T09:30:00Z", with a fraction of a second if there is one).
func formatMethod(a native.Args, t stdtime.Time) (value.Value, error) {
	if !a.Has(0) {
		return value.MakeString(t.Format(stdtime.RFC3339Nano)), nil
	}
	f, err := a.String(0)
	if err != nil {
		return value.NilValue, err
	}
	s, err := format(t, f)
	if err != nil {
		return value.NilValue, a.Errorf(value.ValueError, "%v", err)
	}
	return value.MakeString(s), nil
}

// time::Time::parse(text [, format [, zone]]): the time text stands for.
// Without a format, text is RFC 3339. A text without a zone offset is in
// zone (default "UTC").
func parse(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("time::Time::parse", args, 1, 3)
	if err != nil {
		return value.NilValue, err
	}
	text, err := a.String(0)
	if err != nil {
		return value.NilValue, err
	}
	loc := stdtime.UTC
	if a.Has(2) {
		if loc, err = location(a, 2); err != nil {
			return value.NilValue, err
		}
	}
	var t stdtime.Time
	if a.Has(1) {
		var f string
		if f, err = a.String(1); err != nil {
			return value.NilValue, err
		}
		t, err = parseFormat(text, f, loc)
	} else {
		t, err = stdtime.ParseInLocation(stdtime.RFC3339Nano, text, loc)
		if err != nil {
			err = fmt.Errorf("%q is not an RFC 3339 time", text)
		}
	}
	if err != nil {
		return value.NilValue, a.Errorf(value.ValueError, "%v", err)
	}
	return Time.Object(t), nil
}
