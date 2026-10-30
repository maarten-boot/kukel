// Command kukel compiles and runs Kukel programs.
package main

import (
	"bufio"
	"bytes"
	"errors"
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
	_ "github.com/maarten-boot/kukel/internal/modules/termmod"
	_ "github.com/maarten-boot/kukel/internal/modules/timemod"
	_ "github.com/maarten-boot/kukel/internal/modules/tkmod"
	_ "github.com/maarten-boot/kukel/internal/modules/utf8mod"
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
  dap                             the debugger for editors (the Debug Adapter
                                  Protocol on standard input and output)
  profile [-o file] [-pprof file] [-cpu] a.kkl ... [-- args]
                                  run, then report which functions and lines
                                  took the time (to standard error, or -o;
                                  -pprof: also for go tool pprof); Ctrl-C
                                  stops the program and gives the report
  disasm a.kkl ... | prog.kkc     show the bytecode in readable form
  tokens a.kkl ...                show the tokens of source files
  ast [-pos] a.kkl ...            show the syntax tree of source files
  fmt [-w | -l] [-strict] a.kkl ...
                                  format source files (indentation, spacing,
                                  blank lines): print them, -w rewrite
                                  them, -l list the files whose
                                  formatting differs; fmt - formats
                                  standard input; -strict also puts
                                  braces around every statement of if,
                                  else, for, while, do and with, and every
                                  function on several lines
  version                         show the version, bytecode format and Go modules
  help                            show this text

run, build and disasm take -nofuse as their first option: compile without
the instructions that do the work of several (to compare; docs/vm.md).

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
	// run, build and disasm -nofuse: compile without the instructions
	// that do the work of several, to compare (docs/vm.md).
	if (cmd == "run" || cmd == "build" || cmd == "disasm") && len(args) > 0 && args[0] == "-nofuse" {
		compiler.Fuse = false
		defer func() { compiler.Fuse = true }()
		args = args[1:]
	}
	switch cmd {
	case "help", "-h", "-help", "--help":
		if len(args) > 0 {
			fmt.Fprintf(stderr, "kukel help: takes no arguments\nusage: kukel help\n")
			return 2
		}
		_, err := io.WriteString(stdout, usage)
		return writeResult(err, stderr)
	case "version", "-version", "--version":
		if len(args) > 0 {
			fmt.Fprintf(stderr, "kukel version: takes no arguments\nusage: kukel version\n")
			return 2
		}
		return writeResult(cmdVersion(stdout), stderr)
	case "tokens":
		return cmdTokens(args, stdout, stderr)
	case "ast":
		return cmdAST(args, stdout, stderr)
	case "fmt":
		return cmdFmt(args, stdin, stdout, stderr)
	case "run":
		return cmdRun(args, stdin, stdout, stderr)
	case "repl":
		return cmdRepl(args, stdin, stdout, stderr)
	case "debug":
		return cmdDebug(args, stdin, stdout, stderr)
	case "dap":
		return cmdDap(args, stdin, stdout, stderr)
	case "profile":
		return cmdProfile(args, stdin, stdout, stderr)
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
	for _, f := range files {
		if strings.HasSuffix(f, ".kkc") {
			return nil, fmt.Errorf("kukel: %s is a compiled program, not a source file (.kkl)", f)
		}
	}
	parsed, err := loader.LoadWith(files, cliConfig())
	if err != nil {
		return nil, toolError(err)
	}
	prog, err := compiler.Compile(parsed, builtins.Names())
	return prog, toolError(err)
}

// toolError gives err with the prefix "kukel: ", unless it is a compile
// error (which starts with its position) or has the prefix.
func toolError(err error) error {
	if err == nil || diag.Errors(err) != nil || strings.HasPrefix(err.Error(), "kukel: ") {
		return err
	}
	return errors.New("kukel: " + native.IOMessage(err)) // without Go's name of the operation, as the modules say it
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
	if len(files) == 0 || badFiles("run", files, "kukel run a.kkl ... [-- args]", stderr) {
		if len(files) == 0 {
			fmt.Fprintln(stderr, "usage: kukel run a.kkl ... [-- args]")
		}
		return 2
	}
	prog, err := compileFiles(files)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return runProgram(prog, files[0], progArgs, stdin, stdout, stderr)
}

// badFiles reports a usage error for an argument taken as a file name that
// starts with "-": an option the command does not have. A file whose name
// starts with "-" is given as ./-name. Only kukel fmt reads standard input
// ("-"). A compiled program (.kkc) is not a source file: only exec and
// disasm (on its own) take one.
func badFiles(cmd string, files []string, usage string, stderr io.Writer) bool {
	for _, f := range files {
		if strings.HasSuffix(f, ".kkc") && cmd != "exec" && !(cmd == "disasm" && len(files) == 1) {
			switch cmd {
			case "run", "debug", "profile":
				fmt.Fprintf(stderr, "kukel %s: %s is a compiled program: run it with kukel exec %s (or kukel %s)\n", cmd, f, f, f)
			case "disasm":
				fmt.Fprintf(stderr, "kukel disasm: %s is a compiled program: give it alone (kukel disasm %s)\n", f, f)
			default:
				fmt.Fprintf(stderr, "kukel %s: %s is a compiled program, not a source file (.kkl)\n", cmd, f)
			}
			return true
		}
		if f == "-" {
			fmt.Fprintf(stderr, "kukel %s: - (standard input) is only for kukel fmt (a file named so is given as ./-)\nusage: %s\n", cmd, usage)
			return true
		}
		if strings.HasPrefix(f, "-") {
			fmt.Fprintf(stderr, "kukel %s: unknown option %s (a file named so is given as ./%s)\nusage: %s\n", cmd, f, f, usage)
			return true
		}
	}
	return false
}

// runProgram runs a compiled program, whose file is name (for program()),
// and returns its exit code.
// Output that cannot be written to the end (a full disk) is reported, with
// exit code 1.
func runProgram(prog *bytecode.Program, name string, progArgs []string, stdin io.Reader, stdout, stderr io.Writer) int {
	out := bufio.NewWriter(stdout)
	machine, err := vm.NewWith(prog, builtins.Lookup, cliConfig(), out, stdin)
	if err != nil {
		fmt.Fprintln(stderr, "kukel:", err)
		return 1
	}
	machine.SetStderr(stderr)
	native.SetProgram(machine, name)
	if in, ok := stdin.(*os.File); ok {
		if outFile, ok := stdout.(*os.File); ok {
			native.SetTerminal(machine, in, outFile) // module term: raw mode and the size, if they are a terminal
		}
	}
	code, err := machine.Run(progArgs)
	ferr := out.Flush()
	if err != nil {
		fmt.Fprintln(stderr, err)
		code = 1
	}
	code = endProgram(machine, code, stderr)
	if ferr != nil && err == nil {
		return writeResult(ferr, stderr)
	}
	return code
}

// endProgram closes what the program left open (VM.End: its files). A
// failure (a file that cannot be written out) is reported, and makes an
// exit code of 0 a 1.
func endProgram(machine *vm.VM, code int, stderr io.Writer) int {
	if err := machine.End(); err != nil {
		fmt.Fprintln(stderr, "kukel:", err)
		if code == 0 {
			code = 1
		}
	}
	return code
}

// writeResult reports an error writing the output, as "kukel: write ...",
// and gives the exit code: 1 after an error, else 0.
func writeResult(err error, stderr io.Writer) int {
	if err != nil {
		fmt.Fprintln(stderr, "kukel:", err)
		return 1
	}
	return 0
}

// cmdCheck compiles the source files as one program and reports the first
// error; it prints nothing when the program is fine.
func cmdCheck(files []string, stderr io.Writer) int {
	if len(files) == 0 {
		fmt.Fprintln(stderr, "usage: kukel check a.kkl ...")
		return 2
	}
	if badFiles("check", files, "kukel check a.kkl ...", stderr) {
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
				fmt.Fprintln(stderr, "kukel build: -o needs a file name\nusage: kukel build [-module] [-o prog.kkc] a.kkl ...")
				return 2
			}
			out = args[i+1]
			i++
			continue
		}
		files = append(files, args[i])
	}
	if len(files) == 0 {
		fmt.Fprintln(stderr, "usage: kukel build [-module] [-o prog.kkc] a.kkl ...")
		return 2
	}
	if badFiles("build", files, "kukel build [-module] [-o prog.kkc] a.kkl ...", stderr) {
		return 2
	}
	if out == "" {
		out = strings.TrimSuffix(files[0], ".kkl") + ".kkc"
	} else if overwritesSource(out, files) {
		fmt.Fprintf(stderr, "kukel build: -o %s names a source file (.kkl): name the bytecode file like prog.kkc\n", out)
		return 2
	}
	var data []byte
	if module {
		if len(files) != 1 {
			fmt.Fprintln(stderr, "usage: kukel build -module [-o m.kkc] m.kkl")
			return 2
		}
		mod, err := compileModule(files[0])
		if err != nil {
			fmt.Fprintln(stderr, toolError(err))
			return 1
		}
		data, err = bytecode.EncodeModule(mod)
		if err == nil {
			err = os.WriteFile(out, data, 0o644)
		}
		if err != nil {
			fmt.Fprintln(stderr, toolError(err))
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
		fmt.Fprintln(stderr, toolError(err))
		return 1
	}
	return 0
}

// overwritesSource reports whether the output file out of kukel build is
// one of the source files or a .kkl file.
func overwritesSource(out string, files []string) bool {
	if strings.HasSuffix(out, ".kkl") {
		return true
	}
	oi, err := os.Stat(out)
	for _, f := range files {
		if filepath.Clean(f) == filepath.Clean(out) {
			return true
		}
		if fi, ferr := os.Stat(f); err == nil && ferr == nil && os.SameFile(oi, fi) {
			return true
		}
	}
	return false
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
		return nil, toolError(err)
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
		return nil, toolError(err)
	}
	prog, err := bytecode.Decode(data)
	if err != nil {
		return nil, fmt.Errorf("kukel: %s: %v", file, err)
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
	if badFiles("exec", args[:1], "kukel exec prog.kkc [--] [args]", stderr) {
		return 2
	}
	progArgs := args[1:]
	if len(progArgs) > 0 && progArgs[0] == "--" {
		progArgs = progArgs[1:]
	}
	prog, err := loadKKC(args[0])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return runProgram(prog, args[0], progArgs, stdin, stdout, stderr)
}

// cmdDisasm prints the bytecode of one .kkc file, or of the program
// compiled from source files.
func cmdDisasm(files []string, stdout, stderr io.Writer) int {
	if len(files) == 0 {
		fmt.Fprintln(stderr, "usage: kukel disasm prog.kkc | a.kkl ...")
		return 2
	}
	if badFiles("disasm", files, "kukel disasm prog.kkc | a.kkl ...", stderr) {
		return 2
	}
	var prog *bytecode.Program
	var err error
	if strings.HasSuffix(files[0], ".kkc") {
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
	if badFiles("tokens", files, "kukel tokens a.kkl ...", stderr) {
		return 2
	}
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			fmt.Fprintln(stderr, toolError(err))
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
		if err := w.Flush(); err != nil {
			return writeResult(err, stderr)
		}
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
	if badFiles("ast", args, "kukel ast [-pos] a.kkl ...", stderr) {
		return 2
	}
	for _, file := range args {
		src, err := os.ReadFile(file)
		if err != nil {
			fmt.Fprintln(stderr, toolError(err))
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
// left alone and reported. "-" formats standard input to standard output.
// -strict adds braces and puts functions on several lines (format.Options).
func cmdFmt(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	write, list := false, false
	var opt format.Options
	var files []string
	for _, a := range args {
		switch a {
		case "-w":
			write = true
		case "-l":
			list = true
		case "-strict":
			opt.Strict = true
		default:
			files = append(files, a)
		}
	}
	const fmtUsage = "kukel fmt [-w | -l] [-strict] a.kkl ... | kukel fmt [-strict] -"
	if len(files) == 0 || write && list {
		fmt.Fprintln(stderr, "usage: "+fmtUsage)
		return 2
	}
	if slices.Contains(files, "-") {
		if len(files) > 1 || write || list {
			fmt.Fprintln(stderr, "kukel fmt: - (standard input) goes alone, without -w or -l\nusage: "+fmtUsage)
			return 2
		}
		src, err := io.ReadAll(stdin)
		if err == nil {
			var out []byte
			if out, err = format.SourceWith("<stdin>", src, opt); err == nil {
				_, err = stdout.Write(out)
				return writeResult(err, stderr)
			}
		}
		fmt.Fprintln(stderr, toolError(err))
		return 1
	}
	if badFiles("fmt", files, fmtUsage, stderr) {
		return 2
	}
	code := 0
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			fmt.Fprintln(stderr, toolError(err))
			code = 1
			continue
		}
		out, err := format.SourceWith(file, src, opt)
		if err != nil {
			fmt.Fprintln(stderr, toolError(err))
			code = 1
			continue
		}
		switch {
		case list:
			if !bytes.Equal(src, out) {
				_, err = fmt.Fprintln(stdout, file)
			}
		case write:
			if !bytes.Equal(src, out) {
				if err := os.WriteFile(file, out, 0o644); err != nil {
					fmt.Fprintln(stderr, toolError(err))
					code = 1
				}
			}
		default:
			_, err = stdout.Write(out)
		}
		if err != nil && !write {
			return writeResult(err, stderr) // the output is gone: the other files would fail too
		}
	}
	return code
}

// cmdRepl reads declarations and statements, one piece after the other,
// and runs each piece when it is complete: the declarations' global
// initializers, or the statements. A piece shares the program and its
// state with the pieces before it (compiler.Session, vm.Extend).
func cmdRepl(files []string, stdin io.Reader, stdout, stderr io.Writer) (code int) {
	if badFiles("repl", files, "kukel repl [a.kkl ...]", stderr) {
		return 2
	}
	config := cliConfig()
	session, err := compiler.NewSession(builtins.Names(), config)
	if err != nil {
		fmt.Fprintln(stderr, "kukel:", err)
		return 1
	}
	in := bufio.NewReader(stdin)
	out := bufio.NewWriter(stdout)
	defer out.Flush()
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
	// The pieces and what the programs read (readline()) come from the
	// same input.
	pi := &pipeInput{in: in, out: out, prompts: interactive}
	var input replInput = pi
	var programIn io.Reader = pi
	if editor != nil {
		ei := editor.input()
		input, programIn = ei, ei
	}
	machine, err := vm.NewWith(session.Program(), builtins.Lookup, config, out, programIn)
	if err == nil {
		err = machine.Init()
	}
	if err != nil {
		fmt.Fprintln(stderr, "kukel:", err)
		return 1
	}
	machine.SetStderr(stderr)
	defer func() { code = endProgram(machine, code, stderr) }() // the files the program left open
	r := &repl{session: session, machine: machine, out: out, stderr: stderr}
	if len(files) > 0 {
		parsed, err := loader.LoadWith(files, config)
		if err != nil {
			fmt.Fprintln(stderr, toolError(err))
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

	if interactive {
		first, _, _ := strings.Cut(versionText(), "\n")
		fmt.Fprintf(out, "%s\ntype declarations or statements; a blank line ends an incomplete piece, end of input (Ctrl-D) quits\n", first)
		out.Flush() // the line editor writes its prompt to the terminal itself
	}
	ended := false // the end of the input came when looking for an else or finally
	for n := 1; ; n++ {
		if err := out.Flush(); err != nil { // output that cannot be written ends the session
			return writeResult(err, stderr)
		}
		if ended {
			if interactive {
				fmt.Fprintln(out)
			}
			return writeResult(out.Flush(), stderr)
		}
		var src []byte
		var stmts []ast.Stmt
		var decls *ast.File
		name := fmt.Sprintf("<input %d>", n)
		var joined []byte // the lines that joined an if through else, or a try through finally
	read:
		for {
			prompt := "> "
			if len(src) > 0 {
				prompt = "... "
			}
			var line string
			var rerr error
			if joined != nil {
				line, joined = string(joined), nil
			} else {
				line, rerr = input.line(prompt)
			}
			if rerr == errInterrupted { // Ctrl-C: the piece is dropped
				src = nil
				continue
			}
			end := rerr != nil && line == ""
			if end && len(src) == 0 {
				if interactive {
					fmt.Fprintln(out)
				}
				return writeResult(out.Flush(), stderr)
			}
			blank := strings.TrimSpace(line) == "" // with the end of the input: ends the piece
			if blank && len(src) == 0 {
				continue
			}
			src = append(src, line...)
			var incomplete bool
			var fixed []byte
			decls, stmts, fixed, incomplete, err = parseInput(name, src, blank)
			if err == nil {
				src = fixed // with the ';' it added
			}
			if els, fin := continuations(stmts); err == nil && !blank && !end && (els || fin) {
				// An else (or a finally) may follow on the next line; comment
				// lines between join the piece. An empty line runs the piece,
				// and so does any other line, which then stays in the input:
				// the next piece, or what the piece reads.
				var comments []byte
				for {
					next, perr := input.peek("... ")
					if perr == errInterrupted {
						src = nil
						continue read
					}
					if perr != nil && next == "" {
						ended = true
						break
					}
					if strings.TrimSpace(next) == "" {
						input.take()
						break
					}
					switch k := startsWith(append(comments, next...)); {
					case k == "comment":
						input.take()
						comments = append(comments, next...)
						continue
					case k == "else" && els, k == "finally" && fin:
						input.take()
						joined = append(comments, next...)
						continue read
					}
					break
				}
				src = append(src, comments...)
				break
			}
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

// replInput is where kukel repl reads its pieces: the same input as the
// programs' readline(), so that a line the REPL looks at but does not take
// goes to the program.
type replInput interface {
	// line reads the next line (with its "\n"), after prompt.
	line(prompt string) (string, error)
	// peek gives the next line (after prompt, if it must be read) and
	// leaves it in the input; take takes it.
	peek(prompt string) (string, error)
	take()
}

// pipeInput reads from a pipe, a file or a terminal without line editing.
// It is also the programs' standard input, which gets a line the REPL
// looked at but did not take (peek) first. It reads a line at a time, so
// that the programs' reader holds no more than the line they read.
type pipeInput struct {
	in      *bufio.Reader
	out     *bufio.Writer
	prompts bool
	rest    []byte // of the last line read
}

func (p *pipeInput) line(prompt string) (string, error) {
	line, err := p.peek(prompt)
	if err == nil {
		p.take()
	}
	return line, err
}

func (p *pipeInput) peek(prompt string) (string, error) {
	if len(p.rest) == 0 {
		if p.prompts {
			fmt.Fprint(p.out, prompt)
		}
		p.out.Flush()
		line, err := p.in.ReadString('\n')
		if line == "" {
			return "", err
		}
		p.rest = []byte(line)
	}
	if i := bytes.IndexByte(p.rest, '\n'); i >= 0 {
		return string(p.rest[:i+1]), nil
	}
	return string(p.rest), nil
}

func (p *pipeInput) take() {
	if i := bytes.IndexByte(p.rest, '\n'); i >= 0 {
		p.rest = p.rest[i+1:]
	} else {
		p.rest = nil
	}
}

func (p *pipeInput) Read(b []byte) (int, error) {
	if len(p.rest) == 0 {
		line, err := p.in.ReadString('\n')
		if line == "" {
			return 0, err
		}
		p.rest = []byte(line)
	}
	n := copy(b, p.rest)
	p.rest = p.rest[n:]
	return n, nil
}

// parseInput parses a piece read by the REPL: declarations, or else
// statements. Statements without a final ';' get one when that completes
// them and, unless the piece is ending (last), the last statement does not
// get the ';' as its empty body (a header such as "if (c)", whose
// statement is on the next line); fixed is src with that ';'.
// incomplete reports an error at the end of src: more lines could
// complete the piece. Of two errors the one further on counts.
func parseInput(name string, src []byte, last bool) (decls *ast.File, stmts []ast.Stmt, fixed []byte, incomplete bool, err error) {
	f, ferr := parser.ParseFile(name, src)
	if ferr == nil {
		return f, nil, src, false, nil
	}
	stmts, serr := parser.ParseStmts(name, src)
	if serr == nil {
		return nil, stmts, src, false, nil
	}
	if atEnd(serr) {
		if toks, err := lexer.Tokenize(name, src); err == nil && len(toks) > 1 {
			t := toks[len(toks)-2] // the last before EOF
			end := t.Pos.Offset + len(t.Text)
			fixed = append(append(slices.Clip(src[:end]), ';'), src[end:]...)
			if stmts, err := parser.ParseStmts(name, fixed); err == nil && (last || !emptyBody(stmts[len(stmts)-1])) {
				return nil, stmts, fixed, false, nil
			}
		}
	}
	err = serr
	fd, ok1 := ferr.(*diag.Error)
	sd, ok2 := serr.(*diag.Error)
	if ok1 && ok2 && fd.Pos.Offset > sd.Pos.Offset {
		err = ferr
	}
	return nil, nil, nil, atEnd(ferr) || atEnd(serr), err
}

// emptyBody reports whether s ends in an empty statement as the body of an
// if, else, while, for or with: the ';' the REPL added, where the user's
// body is still to come.
func emptyBody(s ast.Stmt) bool {
	for {
		var body ast.Stmt
		switch x := s.(type) {
		case *ast.IfStmt:
			body = x.Then
			if x.Else != nil {
				body = x.Else
			}
		case *ast.WhileStmt:
			body = x.Body
		case *ast.ForStmt:
			body = x.Body
		case *ast.ForEachStmt:
			body = x.Body
		case *ast.WithStmt:
			body = x.Body
		case *ast.LabeledStmt:
			body = x.Stmt
		default:
			return false
		}
		if _, empty := body.(*ast.EmptyStmt); empty {
			return true
		}
		s = body
	}
}

// continuations reports what a next line may add to the last of stmts:
// an else, when it ends in an if without one, and a finally, when it ends
// in a try with a catch but no finally (also as the body of a loop, an if,
// a with or after a label).
func continuations(stmts []ast.Stmt) (els, fin bool) {
	if len(stmts) == 0 {
		return false, false
	}
	s := stmts[len(stmts)-1]
	for {
		switch x := s.(type) {
		case *ast.IfStmt:
			if x.Else != nil {
				s = x.Else
				continue
			}
			els, s = true, x.Then // "if (c) try {} catch (e) {}" may get a finally too
		case *ast.TryStmt:
			return els, x.Catch != nil && x.Finally == nil
		case *ast.WhileStmt:
			s = x.Body
		case *ast.ForStmt:
			s = x.Body
		case *ast.ForEachStmt:
			s = x.Body
		case *ast.WithStmt:
			s = x.Body
		case *ast.LabeledStmt:
			s = x.Stmt
		default:
			return els, false
		}
	}
}

// startsWith tells how lines start, by their first token: "else",
// "finally", "comment" (nothing but comments, or a comment that goes on),
// or "".
func startsWith(lines []byte) string {
	t, err := lexer.New("", lines).Next()
	switch {
	case err != nil:
		if strings.HasSuffix(err.Error(), "unterminated comment") {
			return "comment"
		}
	case t.Kind == token.EOF:
		return "comment"
	case t.Kind == token.ELSE:
		return "else"
	case t.Kind == token.FINALLY:
		return "finally"
	}
	return ""
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
