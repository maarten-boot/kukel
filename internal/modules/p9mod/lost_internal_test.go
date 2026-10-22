//go:build !no9p

package p9mod

import (
	"errors"
	"fmt"
	"io"
	"testing"
)

// A lost connection is told without the words of the libraries and of Go
// around what went wrong.
func TestLostMessage(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{errors.New("wait: socket error: read tcp 127.0.0.1:1->127.0.0.1:2: read: connection reset by peer"), "connection reset by peer"},
		{fmt.Errorf("wait: %w", io.EOF), "the server closed it"},
		{errors.New("socket error: EOF"), "the server closed it"},
		{errors.New("connection has been closed"), "the server closed it"},
	} {
		if got := lostMessage(tc.err); got != tc.want {
			t.Errorf("lostMessage(%q) = %q, want %q", tc.err, got, tc.want)
		}
	}
}
