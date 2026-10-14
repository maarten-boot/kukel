package timemod

import (
	"strings"
	"testing"
	stdtime "time"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

func args(vs ...value.Value) native.Args { return native.Args{Name: "t", V: vs} }

// Far-away times: no int64 wrap-around anywhere, but a ValueError.
func TestRanges(t *testing.T) {
	far := func(s int64) stdtime.Time { return stdtime.Unix(s, 0).UTC() }
	d, err := sub(args(Time.Object(far(-5e18))), far(5e18))
	if err != nil || d.AsFloat() != 1e19 {
		t.Errorf("sub: %v, %v", d, err)
	}
	if _, err := add(args(value.MakeFloat(8.99e18)), far(8.99e18)); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Errorf("add beyond the range: %v", err)
	}
	if r, err := add(args(value.MakeFloat(-1e18)), far(1e18)); err != nil || r.AsObject().State.(stdtime.Time).Unix() != 0 {
		t.Errorf("add within the range: %v, %v", r, err)
	}
	o := &value.ObjectObj{}
	for _, year := range []int64{9223372036854775807, 300000000000, -300000000000} {
		_, err := newTime(nil, o, []value.Value{value.MakeInt(year), value.MakeInt(1), value.MakeInt(1)})
		if err == nil || !strings.Contains(err.Error(), "out of range") {
			t.Errorf("year %d: %v", year, err)
		}
	}
	if _, err := newTime(nil, o, []value.Value{value.MakeInt(2026), value.MakeInt(13), value.MakeInt(1)}); err != nil {
		t.Errorf("month 13 carries over: %v", err)
	}
}
