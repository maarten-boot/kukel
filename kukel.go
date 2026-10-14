// Package kukel embeds the Kukel language in Go programs: compile Kukel
// source once, run it in as many interpreters as needed, call its
// functions, and give it Go functions of your own.
//
//	app := kukel.NewModule("app").
//		Func("greet", func(in *kukel.Interp, args []kukel.Value) (kukel.Value, error) {
//			return kukel.String("hello, " + args[0].String()), nil
//		})
//	prog, err := kukel.CompileSource("rules.kkl", `
//		use app;
//		discount(total) { return total > 100 ? 0.1 : 0; }
//		main() { println(app::greet("world")); }`,
//		kukel.CompileOptions{Modules: []*kukel.Module{app}})
//	in, err := kukel.New(prog, kukel.Options{Stdout: os.Stdout, StepLimit: 1_000_000})
//	d, err := in.Call("discount", kukel.Int(250))   // 0.1
//
// The language is described in docs/reference.md of the repository, the
// virtual machine in docs/vm.md, and this package in docs/embedding.md.
//
// For code that is not trusted, CompileOptions limit what a program may
// use (modules, built-in functions such as readfile), Options.StepLimit
// bounds the work of every call, and CallContext stops a call when its
// context ends. Memory is not limited.
package kukel

import (
	"errors"
	"fmt"
	"os"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/builtins"
	"github.com/maarten-boot/kukel/internal/bytecode"
	"github.com/maarten-boot/kukel/internal/compiler"
	"github.com/maarten-boot/kukel/internal/diag"
	"github.com/maarten-boot/kukel/internal/loader"
	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/parser"

	// The modules written in Go that come with Kukel.
	_ "github.com/maarten-boot/kukel/internal/modules/dbmod"
	_ "github.com/maarten-boot/kukel/internal/modules/eventsmod"
	_ "github.com/maarten-boot/kukel/internal/modules/jsonmod"
	_ "github.com/maarten-boot/kukel/internal/modules/stringsmod"
	_ "github.com/maarten-boot/kukel/internal/modules/timemod"
	_ "github.com/maarten-boot/kukel/internal/modules/workermod"
	_ "github.com/maarten-boot/kukel/internal/modules/zmqmod"
)

// SystemBuiltins are the built-in functions that reach outside the
// program: files (readfile, writefile, appendfile), standard input
// (readline), the environment (getenv) and ending the process (exit).
// CompileOptions{DenyBuiltins: kukel.SystemBuiltins} keeps them away from
// a program.
var SystemBuiltins = []string{"readfile", "writefile", "appendfile", "readline", "getenv", "exit"}

// Builtins lists the names of all built-in functions.
func Builtins() []string { return builtins.Names() }

// StandardModules lists the modules written in Go that come with Kukel:
// db, events, json, strings, time, worker, zmq.
func StandardModules() []string { return native.Names() }

// CompileOptions say what a program may use beyond its own code.
type CompileOptions struct {
	// Modules are the program's own modules, written in Go ("use app;").
	// A module here hides a standard module of the same name.
	Modules []*Module
	// StandardModules lists the standard modules the program may use
	// (nil: all of them; an empty, non-nil slice: none).
	StandardModules []string
	// DenyBuiltins lists built-in functions the program may not use
	// (using one is a compile error); see SystemBuiltins.
	DenyBuiltins []string
	// Path lists directories where "use name;" looks for name.kkl (or
	// name.kkc, a module compiled on its own), after the directory of the
	// file that uses it (CompileFiles only).
	Path []string
	// NoFileModules forbids reading program files: "use" finds only
	// modules written in Go, and worker::startfile is refused.
	NoFileModules bool
}

// config turns the options into the internal form. An unknown name in
// StandardModules or DenyBuiltins is an error: a misspelt name must not
// leave something allowed by accident.
func (o CompileOptions) config() (*native.Config, error) {
	c := &native.Config{Path: o.Path, NoFileModules: o.NoFileModules}
	for _, m := range o.Modules {
		c.Modules = append(c.Modules, m.prepare())
	}
	if o.StandardModules != nil {
		c.AllowedModules = map[string]bool{}
		for _, name := range o.StandardModules {
			if _, ok := native.Lookup(name); !ok {
				return nil, fmt.Errorf("kukel: StandardModules: there is no standard module %s", name)
			}
			c.AllowedModules[name] = true
		}
	}
	if len(o.DenyBuiltins) > 0 {
		c.DeniedBuiltins = map[string]bool{}
		for _, name := range o.DenyBuiltins {
			if _, ok := builtins.Lookup(name); !ok {
				return nil, fmt.Errorf("kukel: DenyBuiltins: there is no built-in function %s", name)
			}
			c.DeniedBuiltins[name] = true
		}
	}
	return c, nil
}

// Program is a compiled program. It does not change when it runs, so any
// number of interpreters (New) may run it, also at the same time.
type Program struct {
	prog   *bytecode.Program
	config *native.Config
}

// CompileFiles compiles source files as one program, with the modules they
// use. An error in the source is a *CompileError.
func CompileFiles(files []string, opts CompileOptions) (*Program, error) {
	config, err := opts.config()
	if err != nil {
		return nil, err
	}
	parsed, err := loader.LoadWith(files, config)
	if err != nil {
		return nil, compileError(err)
	}
	return compile(parsed, config)
}

// CompileSource compiles a program given as text; name is used in
// messages. Modules written in Kukel are looked for in opts.Path only.
func CompileSource(name, src string, opts CompileOptions) (*Program, error) {
	config, err := opts.config()
	if err != nil {
		return nil, err
	}
	f, err := parser.ParseFile(name, []byte(src))
	if err != nil {
		return nil, compileError(err)
	}
	parsed, err := loader.Resolve([]*ast.File{f}, config, false)
	if err != nil {
		return nil, compileError(err)
	}
	return compile(parsed, config)
}

func compile(files []*ast.File, config *native.Config) (*Program, error) {
	prog, err := compiler.CompileWith(files, builtins.Names(), config)
	if err != nil {
		return nil, compileError(err)
	}
	return &Program{prog: prog, config: config}, nil
}

// LoadProgram reads a program from the bytes of a .kkc file (Encode, or
// kukel build). opts say what it may use; New refuses a program that uses
// more.
func LoadProgram(data []byte, opts CompileOptions) (*Program, error) {
	prog, err := bytecode.Decode(data) // Decode also verifies
	if err != nil {
		return nil, fmt.Errorf("kukel: %w", err)
	}
	config, err := opts.config()
	if err != nil {
		return nil, err
	}
	return &Program{prog: prog, config: config}, nil
}

// Encode gives the program as the bytes of a .kkc file.
func (p *Program) Encode() ([]byte, error) { return bytecode.Encode(p.prog) }

// CompileError is an error in Kukel source. A compile can find several:
// the first is the CompileError itself, the others are in More (in the
// order of the source), and Error() gives all of them, one per line.
type CompileError struct {
	File      string
	Line, Col int
	Message   string
	More      []*CompileError
}

func (e *CompileError) Error() string {
	s := fmt.Sprintf("%s:%d:%d: %s", e.File, e.Line, e.Col, e.Message)
	for _, m := range e.More {
		s += "\n" + m.Error()
	}
	return s
}

func compileError(err error) error {
	if ds := diag.Errors(err); len(ds) > 0 {
		one := func(d *diag.Error) *CompileError {
			return &CompileError{File: d.Pos.File, Line: d.Pos.Line, Col: d.Pos.Col, Message: d.Msg}
		}
		e := one(ds[0])
		for _, d := range ds[1:] {
			e.More = append(e.More, one(d))
		}
		return e
	}
	var pe *os.PathError
	if errors.As(err, &pe) {
		return err
	}
	return fmt.Errorf("kukel: %w", err)
}
