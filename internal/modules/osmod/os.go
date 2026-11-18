// Package osmod is the native module "os": facts about the running
// process (TODO.md Q114, docs/future.md §12, F-53).
//
//	use os;
//	println(os::pid());      // 4711
//
// It is a module of its own, so that a Go program that embeds Kukel can
// allow it apart. It holds the process id now; the process's other facts
// may follow. The environment stays with the built-in getenv.
package osmod

import (
	"os"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

func init() {
	native.Register(&native.Module{
		Name: "os",
		Doc:  "the process id",
		Funcs: map[string]native.Func{
			"pid": pid,
		},
	})
}

// os::pid(): the id of the process that runs the program (of kukel, or of
// the Go program that embeds it; workers share it).
func pid(env value.Env, args []value.Value) (value.Value, error) {
	if _, err := native.Check("os::pid", args, 0, 0); err != nil {
		return value.NilValue, err
	}
	return value.MakeInt(int64(os.Getpid())), nil
}
