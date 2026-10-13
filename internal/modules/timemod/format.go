package timemod

import (
	"errors"
	"fmt"
	"strings"
	stdtime "time"
)

// Formats use strftime-style directives; any other character stands for
// itself:
//
//	%Y year (2026)          %m month (01-12)        %d day (01-31)
//	%e day, space-padded    %H hour (00-23)         %I hour (01-12)
//	%M minute (00-59)       %S second (00-60)       %f microseconds (000000-999999)
//	%p AM or PM             %y year (00-99)         %j day of the year (001-366)
//	%b month (Jan)          %B month (January)      %a weekday (Mon)
//	%A weekday (Monday)     %z offset (+0200)       %Z zone abbreviation (CEST)
//	%% a "%"
//
// Parsing takes the same directives except %Z (zone abbreviations are
// ambiguous; use %z). Numbers may have fewer digits than shown; %f reads
// 1 to 9 digits as a fraction of a second; %z also reads "Z", "+02" and
// "+02:00"; %y reads 69-99 as 1969-1999 and 00-68 as 2000-2068.

func format(t stdtime.Time, f string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(f); i++ {
		if f[i] != '%' {
			b.WriteByte(f[i])
			continue
		}
		if i++; i == len(f) {
			return "", errors.New(`format ends with a lone "%"`)
		}
		switch f[i] {
		case 'Y':
			fmt.Fprintf(&b, "%04d", t.Year())
		case 'm':
			fmt.Fprintf(&b, "%02d", int(t.Month()))
		case 'd':
			fmt.Fprintf(&b, "%02d", t.Day())
		case 'e':
			fmt.Fprintf(&b, "%2d", t.Day())
		case 'H':
			fmt.Fprintf(&b, "%02d", t.Hour())
		case 'I':
			fmt.Fprintf(&b, "%02d", (t.Hour()+11)%12+1)
		case 'M':
			fmt.Fprintf(&b, "%02d", t.Minute())
		case 'S':
			fmt.Fprintf(&b, "%02d", t.Second())
		case 'f':
			fmt.Fprintf(&b, "%06d", t.Nanosecond()/1000)
		case 'p':
			if t.Hour() < 12 {
				b.WriteString("AM")
			} else {
				b.WriteString("PM")
			}
		case 'y':
			fmt.Fprintf(&b, "%02d", t.Year()%100)
		case 'j':
			fmt.Fprintf(&b, "%03d", t.YearDay())
		case 'b':
			b.WriteString(t.Month().String()[:3])
		case 'B':
			b.WriteString(t.Month().String())
		case 'a':
			b.WriteString(t.Weekday().String()[:3])
		case 'A':
			b.WriteString(t.Weekday().String())
		case 'z':
			b.WriteString(t.Format("-0700"))
		case 'Z':
			name, _ := t.Zone()
			b.WriteString(name)
		case '%':
			b.WriteByte('%')
		default:
			return "", fmt.Errorf("unknown directive %%%c in format", f[i])
		}
	}
	return b.String(), nil
}

// parser reads a text according to a format.
type parser struct {
	text string
	pos  int
}

func (p *parser) fail(what string) error {
	if p.pos >= len(p.text) {
		return fmt.Errorf("%q: expected %s at the end", p.text, what)
	}
	return fmt.Errorf("%q: expected %s at %q", p.text, what, p.text[p.pos:])
}

// number reads 1 to max digits (and a sign if signed).
func (p *parser) number(max int, signed bool, what string) (int, error) {
	neg := false
	if signed && p.pos < len(p.text) && (p.text[p.pos] == '-' || p.text[p.pos] == '+') {
		neg = p.text[p.pos] == '-'
		p.pos++
	}
	start, n := p.pos, 0
	for p.pos < len(p.text) && p.pos-start < max && p.text[p.pos] >= '0' && p.text[p.pos] <= '9' {
		n = n*10 + int(p.text[p.pos]-'0')
		p.pos++
	}
	if p.pos == start {
		return 0, p.fail(what)
	}
	if neg {
		n = -n
	}
	return n, nil
}

// name reads one of names (case-insensitively) and gives its index.
func (p *parser) name(names []string, what string) (int, error) {
	for i, n := range names {
		if end := p.pos + len(n); end <= len(p.text) && strings.EqualFold(p.text[p.pos:end], n) {
			p.pos = end
			return i, nil
		}
	}
	return 0, p.fail(what)
}

var monthNames, weekdayNames []string // full names first, so that "March" is not read as "Mar"

func init() {
	for m := stdtime.January; m <= stdtime.December; m++ {
		monthNames = append(monthNames, m.String())
	}
	for m := stdtime.January; m <= stdtime.December; m++ {
		monthNames = append(monthNames, m.String()[:3])
	}
	for d := stdtime.Sunday; d <= stdtime.Saturday; d++ {
		weekdayNames = append(weekdayNames, d.String())
	}
	for d := stdtime.Sunday; d <= stdtime.Saturday; d++ {
		weekdayNames = append(weekdayNames, d.String()[:3])
	}
}

func parseFormat(text, f string, loc *stdtime.Location) (stdtime.Time, error) {
	p := &parser{text: text}
	year, month, day, hour, minute, second, nsec, yday := 1970, 1, 1, 0, 0, 0, 0, 0
	pm, hasPM, hasDate := false, false, false
	var err error
	for i := 0; i < len(f) && err == nil; i++ {
		if f[i] != '%' {
			if p.pos >= len(text) || text[p.pos] != f[i] {
				return stdtime.Time{}, p.fail(fmt.Sprintf("%q", f[i]))
			}
			p.pos++
			continue
		}
		if i++; i == len(f) {
			return stdtime.Time{}, errors.New(`format ends with a lone "%"`)
		}
		switch f[i] {
		case 'Y':
			year, err = p.number(4, true, "a year")
		case 'y':
			if year, err = p.number(2, false, "a two-digit year"); year < 69 {
				year += 2000
			} else {
				year += 1900
			}
		case 'm':
			month, err = p.number(2, false, "a month")
			hasDate = true
		case 'd', 'e':
			if f[i] == 'e' && p.pos < len(text) && text[p.pos] == ' ' {
				p.pos++
			}
			day, err = p.number(2, false, "a day")
			hasDate = true
		case 'H', 'I':
			hour, err = p.number(2, false, "an hour")
		case 'M':
			minute, err = p.number(2, false, "a minute")
		case 'S':
			second, err = p.number(2, false, "a second")
		case 'f':
			start := p.pos
			var frac int
			if frac, err = p.number(9, false, "a fraction of a second"); err == nil {
				for d := p.pos - start; d < 9; d++ {
					frac *= 10
				}
				nsec = frac
			}
		case 'p':
			var k int
			k, err = p.name([]string{"AM", "PM"}, "AM or PM")
			pm, hasPM = k == 1, true
		case 'j':
			yday, err = p.number(3, false, "a day of the year")
		case 'b', 'B':
			var k int
			k, err = p.name(monthNames, "a month name")
			month, hasDate = k%12+1, true
		case 'a', 'A':
			_, err = p.name(weekdayNames, "a weekday name") // checked only for its form
		case 'z':
			loc, err = p.offset()
		case 'Z':
			return stdtime.Time{}, errors.New("%Z cannot be parsed (zone abbreviations are ambiguous): use %z")
		case '%':
			if p.pos >= len(text) || text[p.pos] != '%' {
				return stdtime.Time{}, p.fail(`"%"`)
			}
			p.pos++
		default:
			return stdtime.Time{}, fmt.Errorf("unknown directive %%%c in format", f[i])
		}
	}
	if err != nil {
		return stdtime.Time{}, err
	}
	if p.pos < len(text) {
		return stdtime.Time{}, fmt.Errorf("%q: text left over at %q", text, text[p.pos:])
	}
	if hasPM {
		if hour < 1 || hour > 12 {
			return stdtime.Time{}, fmt.Errorf("%q: hour %d out of range 1..12 for %%I", text, hour)
		}
		hour = hour % 12
		if pm {
			hour += 12
		}
	}
	switch {
	case month < 1 || month > 12:
		return stdtime.Time{}, fmt.Errorf("%q: month %d out of range", text, month)
	case hour > 23 || minute > 59 || second > 60:
		return stdtime.Time{}, fmt.Errorf("%q: time of day out of range", text)
	}
	if yday > 0 && !hasDate {
		t := stdtime.Date(year, 1, yday, hour, minute, second, nsec, loc)
		if t.Year() != year {
			return stdtime.Time{}, fmt.Errorf("%q: day of the year %d out of range", text, yday)
		}
		return t, nil
	}
	t := stdtime.Date(year, stdtime.Month(month), day, hour, minute, second, nsec, loc)
	if t.Day() != day {
		return stdtime.Time{}, fmt.Errorf("%q: day %d out of range for that month", text, day)
	}
	return t, nil
}

// offset reads a zone offset: Z, +hh, +hhmm or +hh:mm.
func (p *parser) offset() (*stdtime.Location, error) {
	if p.pos < len(p.text) && (p.text[p.pos] == 'Z' || p.text[p.pos] == 'z') {
		p.pos++
		return stdtime.UTC, nil
	}
	if p.pos >= len(p.text) || (p.text[p.pos] != '+' && p.text[p.pos] != '-') {
		return nil, p.fail("a zone offset (+hhmm or Z)")
	}
	sign := 1
	if p.text[p.pos] == '-' {
		sign = -1
	}
	p.pos++
	h, err := p.number(2, false, "the hours of a zone offset")
	if err != nil {
		return nil, err
	}
	m := 0
	if p.pos < len(p.text) && p.text[p.pos] == ':' {
		p.pos++
	}
	if p.pos < len(p.text) && p.text[p.pos] >= '0' && p.text[p.pos] <= '9' {
		if m, err = p.number(2, false, "the minutes of a zone offset"); err != nil {
			return nil, err
		}
	}
	off := sign * (h*3600 + m*60)
	if off == 0 {
		return stdtime.UTC, nil
	}
	return stdtime.FixedZone("", off), nil
}
