package native

import (
	"errors"
	"io/fs"
	"net"
	"os"
	"runtime"

	"github.com/maarten-boot/kukel/internal/value"
)

// IOMessage is what went wrong with a file, without Go's name of the
// operation: "nope.kkl: no such file or directory" for "open nope.kkl: no
// such file or directory", and "from -> to: ..." for a rename. Module fs
// words its IOErrors so; other modules that read files follow it.
func IOMessage(err error) string {
	var pe *fs.PathError
	var le *os.LinkError
	switch {
	case errors.As(err, &pe):
		return pe.Path + ": " + pe.Err.Error()
	case errors.As(err, &le):
		return le.Old + " -> " + le.New + ": " + le.Err.Error()
	}
	return err.Error()
}

// IOError is the IOError for err, worded by IOMessage.
func IOError(a Args, err error) error {
	return a.Errorf(value.IOError, "%s", IOMessage(err))
}

// NetMessage is what went wrong in reaching or listening on a network
// address, without Go's words around it: "connection refused" for "dial
// tcp 127.0.0.1:1: connect: connection refused", "host x not found" for a
// name that cannot be looked up. The caller names the address.
func NetMessage(err error) string {
	var dns *net.DNSError
	var sys *os.SyscallError
	var op *net.OpError
	switch {
	case errors.As(err, &dns):
		return "host " + dns.Name + " not found"
	case errors.As(err, &sys):
		return sys.Err.Error()
	case errors.As(err, &op):
		return op.Err.Error()
	}
	return err.Error()
}

// MaxSocketPath is the longest path of a Unix socket the system takes, in
// bytes: a longer one fails with just "invalid argument".
var MaxSocketPath = func() int {
	if runtime.GOOS == "linux" || runtime.GOOS == "android" {
		return 107 // sun_path holds 108 bytes, with the closing 0
	}
	return 103 // 104 on macOS and the BSDs
}()
