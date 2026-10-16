//go:build nozmq

package zmqmod

import "github.com/maarten-boot/kukel/internal/native"

func init() { native.Omit("zmq", "it was built with the build tag nozmq") }
