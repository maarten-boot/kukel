//go:build !tk

package tkmod

import "github.com/maarten-boot/kukel/internal/native"

func init() { native.Omit("tk", "it was built without the build tag tk (go build -tags tk)") }
