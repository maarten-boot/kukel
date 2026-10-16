//go:build no9p

package p9mod

import "github.com/maarten-boot/kukel/internal/native"

func init() { native.Omit("p9", "it was built with the build tag no9p") }
