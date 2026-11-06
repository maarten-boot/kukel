# C. Code that is not trusted

[The booklet](README.md) · previous: [B. Go in Kukel: host modules](23-host-modules.md) · next: [D. Tables](25-tables.md)

A Go program that runs scripts written by its users must keep them in
check: a script should not read the server's files, should not run
forever, and should reach only what the program allows. kukel gives a Go
program four controls:

| Control | Stops |
|---|---|
| `CompileOptions.DenyBuiltins` | built-in functions: `kukel.SystemBuiltins` are those that reach files, the environment, standard input and `exit` |
| `CompileOptions.StandardModules` | standard modules: only those listed may be used (`fs`, `db`, `net`, `zmq`, `p9`, `worker`, ... left out) |
| `CompileOptions.NoFileModules` | program files: `use` finds only standard modules, no `.kkl` files from disk |
| `Options.StepLimit`, `in.CallContext(ctx, ...)` | running too long: after a number of steps, or when a context ends |

The first three act when the script is compiled: a script that uses more
than it may does not compile, so it never runs halfway.

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/maarten-boot/kukel"
)

var limits = kukel.CompileOptions{
	DenyBuiltins:    kukel.SystemBuiltins, // no readfile, writefile, getenv, exit, ...
	StandardModules: []string{"json"},     // json, and nothing else written in Go
	NoFileModules:   true,                 // no modules from disk
}

func try(name, src string) {
	prog, err := kukel.CompileSource(name, src, limits)
	if err != nil {
		fmt.Println(name, "does not compile:", err)
		return
	}
	in, err := kukel.New(prog, kukel.Options{StepLimit: 100_000})
	if err != nil {
		fmt.Println(err)
		return
	}
	defer in.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	v, err := in.CallContext(ctx, "rule", kukel.Int(250))
	var e *kukel.Error
	switch {
	case errors.As(err, &e):
		fmt.Println(name, "stopped:", e.Kind)
	case err != nil:
		fmt.Println(name, "failed:", err)
	default:
		fmt.Println(name, "gives", v)
	}
}

func main() {
	try("fair.kkl", `use json; rule(total) { return json::encode({"discount": total > 100 ? 10 : 0}); } main() {}`)
	try("snoop.kkl", `rule(total) { return readfile("/etc/passwd"); } main() {}`)
	try("disk.kkl", `use fs; rule(total) { return fs::readdir("/"); } main() {}`)
	try("forever.kkl", `rule(total) { for (;;) total++; } main() {}`)
}
```

```
$ go run .
fair.kkl gives {"discount":10}
snoop.kkl does not compile: snoop.kkl:1:22: built-in function readfile is not available in this program
disk.kkl does not compile: disk.kkl:1:5: module fs is not available in this program
forever.kkl stopped: StepLimitError
```

- The step limit counts the steps of every loop, call and `throw`, so no
  loop escapes it, and the same script always stops at the same point.
  Each call from Go gets the full budget again.
- `CallContext` stops a call when its context ends: a timeout, or a
  request that was cancelled. It also wakes a script that waits (in
  `time::sleep`, a network read, a database query).
- After either, the interpreter can still be used: the next call starts
  afresh.

## What is not limited

**Memory** is not limited: Go has one heap for the whole process, and
counting each script's share would slow every program down. A script
that builds a huge string within its steps can use a lot. Where that
matters, run scripts in a separate process, with a limit from the
operating system (`ulimit`, a container, a cgroup).

A single long built-in operation, such as sorting an enormous vector, is
not cut short either; the step limit and the context are noticed when it
returns.

## A checklist

1. Compile with `DenyBuiltins: kukel.SystemBuiltins`, `NoFileModules:
   true`, and `StandardModules` listing only what scripts need (often
   none: `[]string{}`).
2. Give scripts what they may do through a host module of your own
   (appendix B), with functions that check their arguments.
3. Run every call with a `StepLimit` and through `CallContext`, with a
   timeout.
4. Treat what a script returns as input: check it before you use it.
5. Keep memory in mind; for scripts from strangers, a process of their
   own.

[The booklet](README.md) · previous: [B. Go in Kukel: host modules](23-host-modules.md) · next: [D. Tables](25-tables.md)
