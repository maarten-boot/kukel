// Package stdlib holds the standard modules written in Kukel (Q88), built
// into kukel: "use iter;" finds iter.kkl here, after the modules written
// in Go and before files on disk.
package stdlib

import (
	"embed"
	"sort"
	"strings"
)

//go:embed *.kkl
var files embed.FS

// Dir is the directory the modules' files seem to be in, in positions and
// traces: "<std>/iter.kkl".
const Dir = "<std>"

// File gives the name module name's file has in positions and traces.
func File(name string) string { return Dir + "/" + name + ".kkl" }

// Source gives the source of module name, if there is a standard module
// written in Kukel by that name.
func Source(name string) ([]byte, bool) {
	if strings.ContainsAny(name, "/\\.") {
		return nil, false
	}
	src, err := files.ReadFile(name + ".kkl")
	return src, err == nil
}

// SourceOf gives the source of file, a name File gave.
func SourceOf(file string) ([]byte, bool) {
	name, ok := strings.CutPrefix(file, Dir+"/")
	if !ok {
		return nil, false
	}
	return Source(strings.TrimSuffix(name, ".kkl"))
}

// Names gives the names of the standard modules written in Kukel, sorted.
func Names() []string {
	entries, _ := files.ReadDir(".")
	var names []string
	for _, e := range entries {
		names = append(names, strings.TrimSuffix(e.Name(), ".kkl"))
	}
	sort.Strings(names)
	return names
}
