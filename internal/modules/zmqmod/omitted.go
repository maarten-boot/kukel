//go:build nozmq

package zmqmod

import "github.com/maarten-boot/kukel/internal/native"

func init() { native.Omit("zmq", "the build tag nozmq") }
