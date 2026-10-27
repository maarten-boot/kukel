package main

import (
	"fmt"
	"io"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/maarten-boot/kukel/internal/bytecode"
	"github.com/maarten-boot/kukel/internal/modules/dbmod"
	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/stdlib"
)

// version is set by the Makefile (-ldflags "-X main.version=..."); without
// it, the module version of `go install ...@v1.2.3` is used.
var version = ""

// versionText describes this build: version, commit, Go version and the
// bytecode format it reads and writes.
func versionText() string {
	v, commit, dirty := version, "", false
	if info, ok := debug.ReadBuildInfo(); ok {
		if v == "" && info.Main.Version != "" && info.Main.Version != "(devel)" {
			v = info.Main.Version
		}
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				commit = s.Value
			case "vcs.modified":
				dirty = s.Value == "true"
			}
		}
	}
	if v == "" {
		v = "devel"
	}
	text := "kukel " + v
	if commit != "" {
		if len(commit) > 12 {
			commit = commit[:12]
		}
		if dirty {
			commit += "+changes"
		}
		text += " (" + commit + ")"
	}
	return fmt.Sprintf("%s, %s, bytecode format %d", text, runtime.Version(), bytecode.FormatVersion)
}

func cmdVersion(stdout io.Writer) error {
	_, err := fmt.Fprintf(stdout, "%s\nmodules written in Go: %s\nmodules written in Kukel: %s\n", versionText(),
		strings.Join(moduleNames(), ", "), strings.Join(stdlib.Names(), ", "))
	return err
}

// moduleNames lists the modules written in Go that this kukel has; for db,
// the databases it can open (build tags leave some out).
func moduleNames() []string {
	names := native.Names()
	for i, name := range names {
		if name == "db" {
			names[i] = "db (" + strings.Join(dbmod.Kinds(), ", ") + ")"
		}
	}
	return names
}
