// Command kukel compiles and runs Kukel programs.
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/builtins"
	"github.com/maarten-boot/kukel/internal/bytecode"
	"github.com/maarten-boot/kukel/internal/compiler"
	"github.com/maarten-boot/kukel/internal/diag"
	"github.com/maarten-boot/kukel/internal/format"
	"github.com/maarten-boot/kukel/internal/lexer"
	"github.com/maarten-boot/kukel/internal/loader"
	_ "github.com/maarten-boot/kukel/internal/modules/dbmod"
	_ "github.com/maarten-boot/kukel/internal/modules/eventsmod"
	_ "github.com/maarten-boot/kukel/internal/modules/fsmod"
	_ "github.com/maarten-boot/kukel/internal/modules/jsonmod"
	_ "github.com/maarten-boot/kukel/internal/modules/p9mod"
	_ "github.com/maarten-boot/kukel/internal/modules/remod"
	_ "github.com/maarten-boot/kukel/internal/modules/stringsmod"
	_ "github.com/maarten-boot/kukel/internal/modules/timemod"
	_ "github.com/maarten-boot/kukel/internal/modules/tkmod"
	_ "github.com/maarten-boot/kukel/internal/modules/uuidmod"
	_ "github.com/maarten-boot/kukel/internal/modules/workermod"
	_ "github.com/maarten-boot/kukel/internal/modules/zmqmod"
	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/parser"
	"github.com/maarten-boot/kukel/internal/token"
	"github.com/maarten-boot/kukel/internal/value"
	"github.com/maarten-boot/kukel/internal/vm"
)

const usage = `usage: kukel <command> [arguments]

commands:
  run a.kkl b.kkl [-- args]       compile in memory and run
  a.kkl [args] | prog.kkc [args]  the same as run a.kkl -- args (exec for
                                  .kkc): for scripts that start with
                                  #!/usr/bin/env kukel
  check a.kkl ...                 compile only: report the errors, run nothing
  build [-o prog.kkc] a.kkl ...   compile to a bytecode file (default: a.kkc)
  build -module [-o m.kkc] m.kkl  compile module m on its own, for programs
                                  that use it ('use m;' finds m.kkc when
                                  there is no m.kkl)
  exec prog.kkc [--] [args]       run a bytecode file
  repl [a.kkl ...]                read declarations and statements and run
                                  them as they come (after the declarations
                                  of the files, if given)
  debug a.kkl ... [-- args]       run under the debugger: breakpoints,
                                  stepping, values (help inside)
  disasm a.kkl ... | prog.kkc     show the bytecode in readable form
  tokens a.kkl ...                show the tokens of source files
  ast [-pos] a.kkl ...            show the syntax tree of source files
  fmt [-w | -l] a.kkl ...         format source files (indentation, spacing,
                                  blank lines): print them, -w rewrite
                                  them, -l list the files whose
                                  formatting differs
  version                         show the version, bytecode format and Go modules
  help                            show this text

Modules ("use name;") are name.kkl files (or name.kkc, compiled on their
own) in the directory of the file that uses them, or in the directories of
KUKEL_PATH.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run executes the command line args and returns the exit code.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cmd, args := args[0], args[1:]
	switch cmd {
	case "help", "-h", "-help", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case "version", "-version", "--version":
		return cmdVersion(stdout)
	case "tokens":
		return cmdTokens(args, stdout, stderr)
	case "ast":
		return cmdAST(args, stdout, stderr)
	case "fmt":
		return cmdFmt(args, stdout, stderr)
	case "run":
		return cmdRun(args, stdin, stdout, stderr)
	case "repl":
		return cmdRepl(args, stdin, stdout, stderr)
	case "debug":
		return cmdDebug(args, stdin, stdout, stderr)
	case "check":
		return cmdCheck(args, stderr)
	case "disasm":
		return cmdDisasm(args, stdout, stderr)
	case "build":
		return cmdBuild(args, stderr)
	case "exec":
		return cmdExec(args, stdin, stdout, stderr)
	}
	// "kukel prog.kkl args": a file instead of a command is run (a .kkc file
	// executed), all the arguments after it going to main. This is what a
	// script starting with "#!/usr/bin/env kukel" gets.
	// A name ending in .kkl or .kkc is taken as a file also when it is not
	// there, so that the message says so.
	isFile := strings.HasSuffix(cmd, ".kkl") || strings.HasSuffix(cmd, ".kkc")
	if info, err := os.Stat(cmd); isFile || err == nil && info.Mode().IsRegular() {
		if strings.HasSuffix(cmd, ".kkc") {
			return cmdExec(append([]string{cmd, "--"}, args...), stdin, stdout, stderr)
		}
		return cmdRun(append([]string{cmd, "--"}, args...), stdin, stdout, stderr)
	}
	fmt.Fprintf(stderr, "kukel: unknown command %q\n\n%s", cmd, usage)
	return 2
}

// cliConfig is what programs run by kukel may use: everything; modules
// written in Kukel are also searched for in the directories of KUKEL_PATH.
func cliConfig() *native.Config {
	return &native.Config{Path: filepath.SplitList(os.Getenv("KUKEL_PATH"))}
}

// compileFiles parses and compiles source files, with the modules they use,
// into one program.
func compileFiles(files []string) (*bytecode.Program, error) {
	parsed, err := loader.LoadWith(files, cliConfig())
	if err != nil {
		return nil, err
	}
	return compiler.Compile(parsed, builtins.Names())
}

// cmdRun compiles the source files before "--" and runs the program with
// the arguments after it.
func cmdRun(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	files, progArgs := args, []string(nil)
	for i, a := range args {
		if a == "--" {
			files, progArgs = args[:i], args[i+1:]
			break
		}
	}
	if len(files) == 0 {
		fmt.Fprintln(stderr, "usage: kukel run a.kkl ... [-- args]")
		return 2
	}
	prog, err := compileFiles(files)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return runProgram(prog, progArgs, stdin, stdout, stderr)
}

// runProgram runs a compiled program and returns its exit code.
func runProgram(prog *bytecode.Program, progArgs []string, stdin io.Reader, stdout, stderr io.Writer) int {
	out := bufio.NewWriter(stdout)
	defer out.Flush()
	machine, err := vm.NewWith(prog, builtins.Lookup, cliConfig(), out, stdin)
	if err != nil {
		fmt.Fprintln(stderr, "kukel:", err)
		return 1
	}
	machine.SetStderr(stderr)
	code, err := machine.Run(progArgs)
	if err != nil {
		out.Flush()
		fmt.Fprintln(stderr, err)
		return 1
	}
	return code
}

// cmdCheck compiles the source files as one program and reports the first
// error; it prints nothing when the program is fine.
func cmdCheck(files []string, stderr io.Writer) int {
	if len(files) == 0 {
		fmt.Fprintln(stderr, "usage: kukel check a.kkl ...")
		return 2
	}
	if _, err := compileFiles(files); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

// cmdBuild compiles source files into a bytecode file, or a module on
// its own (-module).
func cmdBuild(args []string, stderr io.Writer) int {
	out, module := "", false
	var files []string
	for i := 0; i < len(args); i++ {
		if args[i] == "-module" || args[i] == "--module" {
			module = true
			continue
		}
		if args[i] == "-o" {
			if i+1 == len(args) {
				fmt.Fprintln(stderr, "kukel build: -o needs a file name\nusage: kukel build [-o prog.kkc] a.kkl ...")
				return 2
			}
			out = args[i+1]
			i++
			continue
		}
		files = append(files, args[i])
	}
	if len(files) == 0 {
		fmt.Fprintln(stderr, "usage: kukel build [-o prog.kkc] a.kkl ...")
		return 2
	}
	if out == "" {
		out = strings.TrimSuffix(files[0], ".kkl") + ".kkc"
	}
	var data []byte
	if module {
		if len(files) != 1 {
			fmt.Fprintln(stderr, "usage: kukel build -module [-o m.kkc] m.kkl")
			return 2
		}
		mod, err := compileModule(files[0])
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		data, err = bytecode.EncodeModule(mod)
		if err == nil {
			err = os.WriteFile(out, data, 0o644)
		}
		if err != nil {
			fmt.Fprintln(stderr, "kukel:", err)
			return 1
		}
		return 0
	}
	prog, err := compileFiles(files)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	data, err = bytecode.Encode(prog)
	if err == nil {
		err = os.WriteFile(out, data, 0o644)
	}
	if err != nil {
		fmt.Fprintln(stderr, "kukel:", err)
		return 1
	}
	return 0
}

// compileModule compiles module file on its own: its name is the file's
// name without .kkl.
func compileModule(file string) (*bytecode.Module, error) {
	name := strings.TrimSuffix(filepath.Base(file), ".kkl")
	if toks, err := lexer.Tokenize("", []byte(name)); err != nil || len(toks) != 2 || toks[0].Kind != token.IDENT || toks[0].Text != name {
		return nil, fmt.Errorf("kukel: %s: a module's file name must be its name, a name such as util.kkl", file)
	}
	src, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("kukel: %v", err)
	}
	f, err := parser.ParseFile(file, src)
	if err != nil {
		return nil, err
	}
	f.Module = name
	files, err := loader.Resolve([]*ast.File{f}, cliConfig(), true)
	if err != nil {
		return nil, err
	}
	return compiler.CompileModule(files, src, builtins.Names(), cliConfig())
}

// loadKKC reads and verifies a bytecode file.
func loadKKC(file string) (*bytecode.Program, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	prog, err := bytecode.Decode(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", file, err)
	}
	return prog, nil
}

// cmdExec runs a bytecode file; the arguments after it (an optional "--"
// first) go to main.
func cmdExec(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: kukel exec prog.kkc [--] [args]")
		return 2
	}
	progArgs := args[1:]
	if len(progArgs) > 0 && progArgs[0] == "--" {
		progArgs = progArgs[1:]
	}
	prog, err := loadKKC(args[0])
	if err != nil {
		fmt.Fprintln(stderr, "kukel:", err)
		return 1
	}
	return runProgram(prog, progArgs, stdin, stdout, stderr)
}

// cmdDisasm prints the bytecode of one .kkc file, or of the program
// compiled from source files.
func cmdDisasm(files []string, stdout, stderr io.Writer) int {
	if len(files) == 0 {
		fmt.Fprintln(stderr, "usage: kukel disasm prog.kkc | a.kkl ...")
		return 2
	}
	var prog *bytecode.Program
	var err error
	if strings.HasSuffix(files[0], ".kkc") {
		if len(files) > 1 {
			fmt.Fprintln(stderr, "usage: kukel disasm prog.kkc | a.kkl ...")
			return 2
		}
		prog, err = loadKKC(files[0])
	} else {
		prog, err = compileFiles(files)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := bytecode.Disassemble(stdout, prog); err != nil {
		fmt.Fprintln(stderr, "kukel:", err)
		return 1
	}
	return 0
}

// cmdTokens prints one line per token, in aligned columns: position, kind,
// source text and, for literals, the decoded value.
func cmdTokens(files []string, stdout, stderr io.Writer) int {
	if len(files) == 0 {
		fmt.Fprintln(stderr, "usage: kukel tokens a.kkl ...")
		return 2
	}
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			fmt.Fprintf(stderr, "kukel: %v\n", err)
			return 1
		}
		w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		l := lexer.New(file, src)
		for {
			t, err := l.Next()
			if err != nil {
				w.Flush()
				fmt.Fprintf(stderr, "%v\n", err)
				return 1
			}
			fmt.Fprintln(w, formatToken(t))
			if t.Kind == token.EOF {
				break
			}
		}
		w.Flush()
	}
	return 0
}

// cmdAST prints the syntax tree of each file; -pos adds node positions.
func cmdAST(args []string, stdout, stderr io.Writer) int {
	withPos := false
	if len(args) > 0 && args[0] == "-pos" {
		withPos, args = true, args[1:]
	}
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: kukel ast [-pos] a.kkl ...")
		return 2
	}
	for _, file := range args {
		src, err := os.ReadFile(file)
		if err != nil {
			fmt.Fprintf(stderr, "kukel: %v\n", err)
			return 1
		}
		f, err := parser.ParseFile(file, src)
		if err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return 1
		}
		if err := ast.FprintFile(stdout, f, withPos); err != nil {
			fmt.Fprintf(stderr, "kukel: %v\n", err)
			return 1
		}
	}
	return 0
}

func formatToken(t token.Token) string {
	line := t.Pos.String() + "\t" + t.Kind.String()
	if t.Kind == token.EOF {
		return line
	}
	line += "\t" + showText(t.Text)
	switch t.Kind {
	case token.INT:
		if t.IntMin {
			return line + "  = 2^63 (only valid after unary minus)"
		}
		return line + "  = " + strconv.FormatInt(int64(t.Int), 10)
	case token.CHAR:
		return line + "  = " + strconv.FormatUint(t.Int, 10)
	case token.FLOAT:
		return line + "  = " + strconv.FormatFloat(t.Float, 'g', -1, 64)
	case token.STRING:
		return line + "  = " + strconv.Quote(t.Str)
	}
	return line
}

// showText keeps a token's source text on one line: control characters
// (literal newlines in strings, for example) are shown as \n, \t, \xNN.
func showText(text string) string {
	var b strings.Builder
	for i := 0; i < len(text); i++ {
		switch c := text[i]; {
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\t':
			b.WriteString(`\t`)
		case c == '\r':
			b.WriteString(`\r`)
		case c < 0x20 || c == 0x7f:
			fmt.Fprintf(&b, `\x%02X`, c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// cmdFmt formats source files: printed (default), rewritten (-w), or only
// listed when their formatting differs (-l). A file that does not parse is
// left alone and reported.
func cmdFmt(args []string, stdout, stderr io.Writer) int {
	write, list := false, false
	var files []string
	for _, a := range args {
		switch a {
		case "-w":
			write = true
		case "-l":
			list = true
		default:
			files = append(files, a)
		}
	}
	if len(files) == 0 || write && list {
		fmt.Fprintln(stderr, "usage: kukel fmt [-w | -l] a.kkl ...")
		return 2
	}
	code := 0
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			fmt.Fprintln(stderr, err)
			code = 1
			continue
		}
		out, err := format.Source(file, src)
		if err != nil {
			fmt.Fprintln(stderr, err)
			code = 1
			continue
		}
		switch {
		case list:
			if !bytes.Equal(src, out) {
				fmt.Fprintln(stdout, file)
			}
		case write:
			if !bytes.Equal(src, out) {
				if err := os.WriteFile(file, out, 0o644); err != nil {
					fmt.Fprintln(stderr, err)
					code = 1
				}
			}
		default:
			stdout.Write(out)
		}
	}
	return code
}

// cmdRepl reads declarations and statements, one piece after the other,
// and runs each piece when it is complete: the declarations' global
// initializers, or the statements. A piece shares the program and its
// state with the pieces before it (compiler.Session, vm.Extend).
func cmdRepl(files []string, stdin io.Reader, stdout, stderr io.Writer) int {
	config := cliConfig()
	session, err := compiler.NewSession(builtins.Names(), config)
	if err != nil {
		fmt.Fprintln(stderr, "kukel:", err)
		return 1
	}
	in := bufio.NewReader(stdin)
	out := bufio.NewWriter(stdout)
	defer out.Flush()
	machine, err := vm.NewWith(session.Program(), builtins.Lookup, config, out, in)
	if err == nil {
		err = machine.Init()
	}
	if err != nil {
		fmt.Fprintln(stderr, "kukel:", err)
		return 1
	}
	machine.SetStderr(stderr)
	r := &repl{session: session, machine: machine, out: out, stderr: stderr}
	if len(files) > 0 {
		parsed, err := loader.LoadWith(files, config)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fn, err := session.Declare(parsed, nil)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if code, exit := r.run(fn, false); exit {
			return code
		}
	}

	interactive := false
	if f, ok := stdin.(*os.File); ok {
		if info, err := f.Stat(); err == nil && info.Mode()&os.ModeCharDevice != 0 {
			interactive = true
		}
	}
	var editor *lineEditor // line editing and history, on a terminal
	if f, ok := stdout.(*os.File); ok && interactive {
		editor = newLineEditor(stdin.(*os.File), f)
	}
	if interactive {
		first, _, _ := strings.Cut(versionText(), "\n")
		fmt.Fprintf(out, "%s\ntype declarations or statements; a blank line ends an incomplete piece, end of input (Ctrl-D) quits\n", first)
	}
	for n := 1; ; n++ {
		var src []byte
		var stmts []ast.Stmt
		var decls *ast.File
		name := fmt.Sprintf("<input %d>", n)
		for {
			prompt := "> "
			if len(src) > 0 {
				prompt = "... "
			}
			if interactive && editor == nil {
				fmt.Fprint(out, prompt)
			}
			out.Flush()
			var line string
			var rerr error
			if editor != nil {
				line, rerr = editor.readLine(prompt)
				if rerr == errInterrupted { // Ctrl-C: the piece is dropped
					src = nil
					continue
				}
			} else {
				line, rerr = in.ReadString('\n')
			}
			end := rerr != nil && line == ""
			if end && len(src) == 0 {
				if interactive {
					fmt.Fprintln(out)
				}
				return 0
			}
			blank := strings.TrimSpace(line) == "" // with the end of the input: ends the piece
			if blank && len(src) == 0 {
				continue
			}
			src = append(src, line...)
			var incomplete bool
			decls, stmts, incomplete, err = parseInput(name, src, blank)
			if err == nil || !incomplete || blank {
				break
			}
		}
		if err != nil {
			fmt.Fprintln(r.stderr, err)
			continue
		}
		var fn *bytecode.Function
		show := false
		if decls != nil {
			var files []*ast.File // the modules it uses, from the current directory or KUKEL_PATH
			if files, err = loader.Resolve([]*ast.File{decls}, config, true); err == nil {
				fn, err = session.Declare(files[:len(files)-1], decls)
			}
		} else {
			fn, show, err = session.Statements(name, stmts)
		}
		if err != nil {
			fmt.Fprintln(r.stderr, err)
			continue
		}
		if code, exit := r.run(fn, show); exit {
			return code
		}
	}
}

// parseInput parses a piece read by the REPL: declarations, or else
// statements. Statements without a final ';' get one if they are one line,
// or the last line of the piece (last). incomplete reports an
// error at the end of src: more lines could complete the piece. Of two
// errors the one further on counts.
func parseInput(name string, src []byte, last bool) (decls *ast.File, stmts []ast.Stmt, incomplete bool, err error) {
	f, ferr := parser.ParseFile(name, src)
	if ferr == nil {
		return f, nil, false, nil
	}
	stmts, serr := parser.ParseStmts(name, src)
	if serr == nil {
		return nil, stmts, false, nil
	}
	if line := bytes.TrimSpace(src); atEnd(serr) && (last || !bytes.Contains(line, []byte("\n"))) {
		if stmts, err := parser.ParseStmts(name, append(slices.Clip(line), ';')); err == nil {
			return nil, stmts, false, nil
		}
	}
	err = serr
	fd, ok1 := ferr.(*diag.Error)
	sd, ok2 := serr.(*diag.Error)
	if ok1 && ok2 && fd.Pos.Offset > sd.Pos.Offset {
		err = ferr
	}
	return nil, nil, atEnd(ferr) || atEnd(serr), err
}

// atEnd reports whether a syntax error is at the end of the input.
func atEnd(err error) bool {
	msg := err.Error()
	return strings.HasSuffix(msg, "found end of file") || strings.HasSuffix(msg, "unterminated string") ||
		strings.HasSuffix(msg, "unterminated comment")
}

// repl is the state of kukel repl.
type repl struct {
	session *compiler.Session
	machine *vm.VM
	out     *bufio.Writer
	stderr  io.Writer
}

// run runs the function of a piece; with show, it prints the value the
// function gives, unless nil. exit reports exit(code).
func (r *repl) run(fn *bytecode.Function, show bool) (code int, exit bool) {
	defer r.out.Flush()
	if err := r.machine.Extend(r.session.Program()); err != nil {
		fmt.Fprintln(r.stderr, "kukel:", err)
		return 0, false
	}
	v, err := r.machine.Call(value.MakeFunction(fn), nil)
	r.out.Flush()
	if re, ok := err.(*vm.RuntimeError); ok && re.Exit != nil {
		return *re.Exit, true
	}
	if err != nil {
		fmt.Fprintln(r.stderr, err)
		return 0, false
	}
	if show && !v.IsNil() {
		fmt.Fprintln(r.out, v.Repr())
	}
	return 0, false
}
