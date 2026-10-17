package main

import (
	"fmt"
	"testing"
)

// The history keeps the last historyMax lines that are not blank, and none
// while a program reads.
func TestHistory(t *testing.T) {
	h := &history{}
	for i := 0; i < historyMax+500; i++ {
		h.Add(fmt.Sprint("line ", i))
		h.Add("  ")
	}
	h.off = true
	h.Add("read by a program")
	h.off = false
	if h.Len() != historyMax || h.At(0) != fmt.Sprint("line ", historyMax+499) || h.At(historyMax-1) != "line 500" {
		t.Errorf("Len %d, newest %q, oldest %q", h.Len(), h.At(0), h.At(h.Len()-1))
	}
}
