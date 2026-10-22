package debugger

import (
	"strings"
	"testing"

	"github.com/maarten-boot/kukel/internal/value"
)

func TestHitCount(t *testing.T) {
	for _, tt := range []struct {
		cond string
		hits []int // the counts from 1 to 6 that meet it
	}{
		{"3", []int{3}},
		{"== 3", []int{3}},
		{"!= 3", []int{1, 2, 4, 5, 6}},
		{"> 4", []int{5, 6}},
		{">=4", []int{4, 5, 6}},
		{"< 2", []int{1}},
		{"<= 2", []int{1, 2}},
		{"% 2", []int{2, 4, 6}},
	} {
		hc, err := parseHit(tt.cond)
		if err != nil {
			t.Errorf("%q: %v", tt.cond, err)
			continue
		}
		var got []int
		for n := 1; n <= 6; n++ {
			if hc.meets(n) {
				got = append(got, n)
			}
		}
		if len(got) != len(tt.hits) {
			t.Errorf("%q: %v, want %v", tt.cond, got, tt.hits)
			continue
		}
		for i := range got {
			if got[i] != tt.hits[i] {
				t.Errorf("%q: %v, want %v", tt.cond, got, tt.hits)
			}
		}
	}
	for _, bad := range []string{"", "x", ">= x", "3 times", "==!=3", "% 0", "%-2"} {
		if CheckHit(bad) == nil {
			t.Errorf("CheckHit(%q) accepted it", bad)
		}
	}
	if err := CheckHit(">= 2"); err != nil {
		t.Error(err)
	}
}

func TestCheck(t *testing.T) {
	for _, ok := range []string{"i == 3", "-x + v[i] > 2 && !done", "this->n", "C::k", "a ? [1, 2] : {\"k\": 1}"} {
		if err := Check(ok); err != nil {
			t.Errorf("Check(%q): %v", ok, err)
		}
	}
	for src, want := range map[string]string{
		"i ==":         "expected",
		"len(v) > 2":   "calls are not evaluated",
		"x->f() > 2":   "calls are not evaluated",
		"i = 3":        "without side effects",
		"i++":          "without side effects",
		"new C()":      "without side effects",
		"fn() { }":     "without side effects",
		"[v..]":        "with .. is not evaluated",
		"1 + (x = 2)":  "without side effects",
		"{1: f()}":     "calls are not evaluated",
		"c ? 1 : g(2)": "calls are not evaluated",
	} {
		err := Check(src)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Check(%q) = %v, want %q", src, err, want)
		} else if strings.Contains(err.Error(), "1:") {
			t.Errorf("Check(%q) = %v: no position wanted", src, err)
		}
	}
}

func TestClosing(t *testing.T) {
	for s, want := range map[string]int{
		"{x} rest":          2,
		`{"}"} rest`:        4,
		`{'}'}`:             4,
		`{"\"}"}`:           6,
		`{{"k": 1}["k"]}`:   14,
		`{m["{"] + "}"} x}`: 13,
		"{x":                -1,
		`{"}`:               -1,
	} {
		if got := closing(s); got != want {
			t.Errorf("closing(%s) = %d, want %d", s, got, want)
		}
	}
}

func TestShow(t *testing.T) {
	ints := func(n int) value.Value {
		v := make([]value.Value, n)
		for i := range v {
			v[i] = value.MakeInt(int64(i))
		}
		return value.MakeVector(v)
	}
	if got := Show(ints(3)); got != "[0, 1, 2]" {
		t.Errorf("Show of a small vector: %s", got)
	}
	big := Show(ints(1_000_000))
	if !strings.HasSuffix(big, ", …]") || strings.Count(big, ",") != showElems {
		t.Errorf("Show of a large vector: %.200s ... %s", big, big[max(0, len(big)-20):])
	}
	m := value.NewMap()
	for i := range 1000 {
		m.Set(value.MakeInt(int64(i)), value.MakeString(strings.Repeat("x", 50)))
	}
	if got := Show(value.MakeMap(m)); len(got) > showChars+200 || !strings.HasSuffix(got, "…}") {
		t.Errorf("Show of a large map: %d bytes: %s", len(got), got[max(0, len(got)-40):])
	}
	s := Show(value.MakeString(strings.Repeat("é", 2000)))
	if !strings.HasSuffix(s, "…\"") || !strings.HasPrefix(s, "\"éé") || len(s) > showChars+10 || !strings.Contains(s, "é…") {
		t.Errorf("Show of a long string: %s", s[max(0, len(s)-20):])
	}
	cyc := value.MakeVector([]value.Value{value.MakeInt(1)})
	cyc.AsVector().Elems = append(cyc.AsVector().Elems, cyc)
	if got := Show(cyc); got != "[1, [...]]" {
		t.Errorf("Show of a vector in itself: %s", got)
	}
}
