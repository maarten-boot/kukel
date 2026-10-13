package timemod

import (
	"strings"
	"testing"
	"time"
)

func TestFormat(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatal(err)
	}
	tm := time.Date(2026, 3, 5, 14, 7, 9, 123456789, loc)
	tests := map[string]string{
		"%Y-%m-%d %H:%M:%S.%f": "2026-03-05 14:07:09.123456",
		"%e|%I %p|%y|%j":       " 5|02 PM|26|064",
		"%a %A %b %B":          "Thu Thursday Mar March",
		"%z %Z %%":             "+0100 CET %",
		"no directives":        "no directives",
	}
	for f, want := range tests {
		if got, err := format(tm, f); err != nil || got != want {
			t.Errorf("format(%q) = %q, %v; want %q", f, got, err, want)
		}
	}
	for _, f := range []string{"%", "%Q"} {
		if _, err := format(tm, f); err == nil {
			t.Errorf("format(%q): no error", f)
		}
	}
	if s, _ := format(time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC), "%I %p"); s != "12 AM" {
		t.Errorf("midnight: %s", s)
	}
}

func TestParseFormat(t *testing.T) {
	tests := []struct{ text, format, want string }{
		{"2026-10-14", "%Y-%m-%d", "2026-10-14T00:00:00Z"},
		{"2026-1-5 7:08:09.5", "%Y-%m-%d %H:%M:%S.%f", "2026-01-05T07:08:09.5Z"},
		{"14 october 26, 12:15 am", "%d %B %y, %I:%M %p", "2026-10-14T00:15:00Z"},
		{"Sat 1 Jan 70 12:00 PM", "%a %e %b %y %I:%M %p", "1970-01-01T12:00:00Z"},
		{"2026-10-14T09:30:00+02:00", "%Y-%m-%dT%H:%M:%S%z", "2026-10-14T09:30:00+02:00"},
		{"2026-10-14 09:30 -0530", "%Y-%m-%d %H:%M %z", "2026-10-14T09:30:00-05:30"},
		{"2026-10-14 09:30 Z", "%Y-%m-%d %H:%M %z", "2026-10-14T09:30:00Z"},
		{"2024-366", "%Y-%j", "2024-12-31T00:00:00Z"},
		{"10%", "%d%%", "1970-01-10T00:00:00Z"},
	}
	for _, tc := range tests {
		got, err := parseFormat(tc.text, tc.format, time.UTC)
		if err != nil || got.Format(time.RFC3339Nano) != tc.want {
			t.Errorf("parse(%q, %q) = %v, %v; want %s", tc.text, tc.format, got.Format(time.RFC3339Nano), err, tc.want)
		}
	}
	bad := []struct{ text, format, msg string }{
		{"2026-02-29", "%Y-%m-%d", "day 29 out of range"},
		{"2026-00-01", "%Y-%m-%d", "month 0 out of range"},
		{"2026-01-01 25:00", "%Y-%m-%d %H:%M", "time of day out of range"},
		{"13 PM", "%I %p", "hour 13 out of range 1..12"},
		{"2025-366", "%Y-%j", "day of the year 366 out of range"},
		{"2026/01/01", "%Y-%m-%d", `expected '-' at "/01/01"`},
		{"2026-01", "%Y-%m-%d", "expected '-' at the end"},
		{"2026-01-01x", "%Y-%m-%d", `text left over at "x"`},
		{"Foo", "%b", "expected a month name"},
		{"CET", "%Z", "%Z cannot be parsed"},
		{"x", "%Q", "unknown directive %Q"},
		{"1", "%", `lone "%"`},
		{"+x", "%z", "the hours of a zone offset"},
		{"0100", "%z", "a zone offset (+hhmm or Z)"},
	}
	for _, tc := range bad {
		_, err := parseFormat(tc.text, tc.format, time.UTC)
		if err == nil || !strings.Contains(err.Error(), tc.msg) {
			t.Errorf("parse(%q, %q): got %v, want %q", tc.text, tc.format, err, tc.msg)
		}
	}
}

// FuzzParseFormat: parsing never panics, and a parsed time formats back to
// a text that parses to the same instant.
func FuzzParseFormat(f *testing.F) {
	f.Add("2026-10-14 09:30:00.5 +0200", "%Y-%m-%d %H:%M:%S.%f %z")
	f.Add("14 Oct 26 9 PM", "%d %b %y %I %p")
	f.Add("2024-366", "%Y-%j")
	f.Fuzz(func(t *testing.T, text, layout string) {
		tm, err := parseFormat(text, layout, time.UTC)
		if err != nil {
			return
		}
		const full = "%Y-%m-%dT%H:%M:%S.%f%z"
		s, err := format(tm, full)
		if err != nil {
			t.Fatal(err)
		}
		back, err := parseFormat(s, full, time.UTC)
		if err != nil || !back.Truncate(time.Microsecond).Equal(tm.Truncate(time.Microsecond)) {
			t.Fatalf("%q %q: %v -> %s -> %v (%v)", text, layout, tm, s, back, err)
		}
	})
}
