// Package loader reads the source files of a program together with the
// modules they use ("use name;"), directly or through other modules.
package loader

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/diag"
	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/parser"
)

// Ext is the extension of Kukel source files.
const Ext = ".kkl"

// Load parses the program's files and every module they use, and returns
// them in compile order: each module before the files that use it, then
// the program's files in the order given.
//
// Module name is a module written in Go (package native) if one is
// registered under that name; else the file name.kkl, looked up first in
// the directory of the file that uses it, then in the directories of path
// (KUKEL_PATH). A name
// stands for one file in the whole program; modules may not use each other
// in a cycle.
func Load(files []string, path []string) ([]*ast.File, error) {
	return LoadWith(files, &native.Config{Path: path})
}

// LoadWith is Load for a program limited by config: the modules written in
// Go it may use, and where (and whether) it finds modules written in Kukel.
func LoadWith(files []string, config *native.Config) ([]*ast.File, error) {
	var prog []*ast.File
	for _, file := range files {
		f, err := parse(file, "")
		if err != nil {
			return nil, err
		}
		prog = append(prog, f)
	}
	return resolve(prog, config, true)
}

// Resolve loads the modules that already parsed program files use. With
// nextToFiles false, a module is looked for only in config.Path, not next
// to the file that uses it (for source that is not from a file).
func Resolve(prog []*ast.File, config *native.Config, nextToFiles bool) ([]*ast.File, error) {
	return resolve(prog, config, nextToFiles)
}

func resolve(prog []*ast.File, config *native.Config, nextToFiles bool) ([]*ast.File, error) {
	l := &loader{config: config, nextToFiles: nextToFiles, modules: map[string]*module{}, program: map[string]string{}}
	if config != nil {
		l.path = config.Path
	}
	for _, f := range prog {
		l.program[abs(f.Name)] = f.Name
	}
	for _, f := range prog {
		if err := l.uses(f, nil); err != nil {
			return nil, err
		}
	}
	return append(l.out, prog...), nil
}

type loader struct {
	config      *native.Config
	nextToFiles bool // look for modules next to the file that uses them
	path        []string
	modules     map[string]*module
	program     map[string]string // the program's own files, by absolute path
	out         []*ast.File       // the modules, in compile order
}

type module struct {
	file    string // as found
	abs     string
	loading bool // its uses are being loaded: seeing it again is a cycle
}

// uses loads the modules file f uses. chain holds the modules being loaded
// that lead to f, for the message about a cycle.
func (l *loader) uses(f *ast.File, chain []string) error {
	for _, d := range f.Decls {
		u, ok := d.(*ast.UseDecl)
		if !ok {
			continue
		}
		name := u.Name.Name
		switch {
		case name == f.Module:
			return diag.Errorf(u.Name.NamePos, "module %s cannot use itself", name)
		case name == "Error":
			return diag.Errorf(u.Name.NamePos, "a module cannot be called Error (the name of the class of runtime errors)")
		}
		if _, ok := l.config.Lookup(name); ok {
			continue // written in Go: nothing to load
		}
		if l.config.Disallowed(name) {
			continue // the compiler reports it
		}
		file, err := l.find(name, filepath.Dir(f.Name))
		if err != nil {
			return diag.Errorf(u.Name.NamePos, "%v", err)
		}
		if m, ok := l.modules[name]; ok {
			switch {
			case m.abs != abs(file):
				return diag.Errorf(u.Name.NamePos, "module %s is %s here, but %s elsewhere in the program", name, file, m.file)
			case m.loading:
				return diag.Errorf(u.Name.NamePos, "modules use each other in a cycle: %s", strings.Join(append(chain, name), " -> "))
			}
			continue
		}
		if p, ok := l.program[abs(file)]; ok {
			return diag.Errorf(u.Name.NamePos, "%s is both a file of the program and module %s (leave it off the command line)", p, name)
		}
		m := &module{file: file, abs: abs(file), loading: true}
		l.modules[name] = m
		mf, err := parse(file, name)
		if err != nil {
			return err
		}
		if err := l.uses(mf, append(chain[:len(chain):len(chain)], name)); err != nil {
			return err
		}
		m.loading = false
		l.out = append(l.out, mf)
	}
	return nil
}

// find returns the file of module name: name.kkl in dir, else in the
// directories of the search path.
func (l *loader) find(name, dir string) (string, error) {
	if l.config != nil && l.config.NoFileModules {
		return "", errors.New("module " + name + " not found (this program can only use modules written in Go)")
	}
	var dirs []string
	if l.nextToFiles {
		dirs = append(dirs, dir)
	}
	for _, d := range l.path {
		if d != "" { // an empty entry (KUKEL_PATH="a::b") is not the current directory
			dirs = append(dirs, d)
		}
	}
	for _, d := range dirs {
		file := filepath.Join(d, name+Ext)
		info, err := os.Stat(file)
		if err == nil && info.Mode().IsRegular() {
			return file, nil
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
	}
	if len(dirs) == 0 {
		return "", errors.New("module " + name + " not found: there is no directory to look for " + name + Ext + " in (no search path)")
	}
	return "", errors.New("module " + name + " not found: no " + name + Ext + " in " + strings.Join(dirs, ", "))
}

func parse(file, module string) (*ast.File, error) {
	src, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	f, err := parser.ParseFile(file, src)
	if err != nil {
		return nil, err
	}
	f.Module = module
	return f, nil
}

func abs(file string) string {
	if a, err := filepath.Abs(file); err == nil {
		return a
	}
	return filepath.Clean(file)
}
