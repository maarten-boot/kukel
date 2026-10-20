package debugger

import "testing"

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
		{"%0", nil},
	} {
		var got []int
		for n := 1; n <= 6; n++ {
			if hitCount(tt.cond, n) {
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
	for _, bad := range []string{"", "x", ">= x", "3 times"} {
		if CheckHit(bad) == nil {
			t.Errorf("CheckHit(%q) accepted it", bad)
		}
	}
	if err := CheckHit(">= 2"); err != nil {
		t.Error(err)
	}
}
