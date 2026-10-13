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
		"utc":    method("utc", 0, 0, func(a native.Args, t stdtime.Time) (value.Value, error) { return Time.Object(t.UTC()), nil }),
		"local":  method("local", 0, 0, func(a native.Args, t stdtime.Time) (value.Value, error) { return Time.Object(t.Local()), nil }),
		"inzone": method("inzone", 1, 1, inZone),
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

// new time::Time(year, month, day [, hour, minute, second [, zone]]): second
// may have a fraction; zone is a name such as "Europe/Amsterdam", "Local"
// or "UTC" (the default).
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
	var sec float64
	if a.Has(5) {
		if sec, err = a.Number(5); err != nil {
			return value.NilValue, err
		}
	}
	loc := stdtime.UTC
	if a.Has(6) {
		if loc, err = location(a, 6); err != nil {
			return value.NilValue, err
		}
	}
	whole := int64(sec)
	this.State = stdtime.Date(int(parts[0]), stdtime.Month(parts[1]), int(parts[2]), int(parts[3]), int(parts[4]),
		int(whole), int((sec-float64(whole))*1e9+0.5), loc)
	return value.NilValue, nil
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
	s, err := a.Number(0)
	if err != nil {
		return value.NilValue, err
	}
	if s < 0 {
		return value.NilValue, a.Errorf(value.ValueError, "negative duration %s", value.FormatFloat(s))
	}
	t := stdtime.NewTimer(stdtime.Duration(s * 1e9))
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
	s, err := a.Number(0)
	if err != nil {
		return value.NilValue, err
	}
	whole := int64(s)
	return Time.Object(stdtime.Unix(whole, int64((s-float64(whole))*1e9)).UTC()), nil
}

// t->add(seconds): the time that many seconds later (earlier if negative).
func add(a native.Args, t stdtime.Time) (value.Value, error) {
	s, err := a.Number(0)
	if err != nil {
		return value.NilValue, err
	}
	return Time.Object(t.Add(stdtime.Duration(s * 1e9))), nil
}

// t->sub(u): the seconds from u to t, as a float.
func sub(a native.Args, t stdtime.Time) (value.Value, error) {
	u, err := other(a)
	if err != nil {
		return value.NilValue, err
	}
	return value.MakeFloat(t.Sub(u).Seconds()), nil
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
