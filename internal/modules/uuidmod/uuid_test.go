package uuidmod

import (
	"testing"
	"time"
)

func TestUUIDs(t *testing.T) {
	a := V7()
	time.Sleep(2 * time.Millisecond)
	b := V7()
	if !Valid(a) || !Valid(b) || !(a < b) || a[14] != '7' {
		t.Errorf("version 7: %s, %s", a, b)
	}
	for _, s := range []string{"", "x", "0192f4a0-7c1d-7e2f-8a3b-4c5d6e7f8a9", "0192f4a0x7c1d-7e2f-8a3b-4c5d6e7f8a9b", "0192f4a0-7c1d-7e2f-8a3b-4c5d6e7f8a9g", "{0192f4a0-7c1d-7e2f-8a3b-4c5d6e7f8a9b}"} {
		if Valid(s) {
			t.Errorf("%q is valid", s)
		}
	}
	if !Valid("0192F4A0-7C1D-7E2F-8A3B-4C5D6E7F8A9B") {
		t.Error("upper case")
	}
}
