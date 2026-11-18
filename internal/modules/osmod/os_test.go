package osmod

import (
	"os"
	"testing"

	"github.com/maarten-boot/kukel/internal/value"
)

func TestPid(t *testing.T) {
	v, err := pid(nil, nil)
	if err != nil || v.Kind() != value.Int || v.AsInt() != int64(os.Getpid()) {
		t.Errorf("os::pid() = %v, %v; want %d", v, err, os.Getpid())
	}
	if _, err := pid(nil, []value.Value{value.MakeInt(1)}); err == nil {
		t.Errorf("os::pid(1): no error")
	}
}
