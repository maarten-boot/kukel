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

// A value on a step has the decimals of the step and from, no more.
func TestSliderSnap(t *testing.T) {
	for _, tt := range []struct {
		s       slider
		x, want float64
	}{
		{slider{to: 1, step: 0.1}, 0.3, 0.3},
		{slider{to: 1, step: 0.1}, 0.7, 0.7},
		{slider{to: 1, step: 0.1}, 0.66, 0.7},
		{slider{to: 0.3, step: 0.1}, 5, 0.3},
		{slider{from: 0.05, to: 1, step: 0.1}, 0.3, 0.35},
		{slider{from: 1, to: 0, step: 0.1}, 0.3, 0.3},
		{slider{to: 10, step: 3, ints: true}, 100, 9},
		{slider{to: 1}, 0.123456, 0.123456},
	} {
		if got := tt.s.snap(tt.x); got != tt.want {
			t.Errorf("%+v: snap(%v) = %v, want %v", tt.s, tt.x, got, tt.want)
		}
	}
}
