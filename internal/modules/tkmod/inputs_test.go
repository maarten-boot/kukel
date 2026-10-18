//go:build tk

package tkmod

import "testing"

// The value beside a slider: an int, or as many decimals as the step has.
func TestSliderText(t *testing.T) {
	for _, tt := range []struct {
		s    slider
		x    float64
		want string
	}{
		{slider{ints: true}, 3, "3"},
		{slider{step: 0.25}, 0.5, "0.50"},
		{slider{step: 0.5}, 1, "1.0"},
		{slider{step: 2.5}, -5, "-5.0"},
		{slider{}, 1.0 / 3, "0.33"},
	} {
		if got := tt.s.text(tt.x); got != tt.want {
			t.Errorf("%+v: text(%v) = %q, want %q", tt.s, tt.x, got, tt.want)
		}
	}
}
