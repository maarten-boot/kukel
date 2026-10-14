// Command kukel compiles and runs Kukel programs.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/builtins"
	"github.com/maarten-boot/kukel/internal/bytecode"
	"github.com/maarten-boot/kukel/internal/compiler"
	"github.com/maarten-boot/kukel/internal/lexer"
	"github.com/maarten-boot/kukel/internal/loader"
	_ "github.com/maarten-boot/kukel/internal/modules/dbmod"
	_ "github.com/maarten-boot/kukel/internal/modules/eventsmod"
	_ "github.com/maarten-boot/kukel/internal/modules/jsonmod"
	_ "github.com/maarten-boot/kukel/internal/modules/stringsmod"
	_ "github.com/maarten-boot/kukel/internal/modules/timemod"
	_ "github.com/maarten-boot/kukel/internal/modules/workermod"
	_ "github.com/maarten-boot/kukel/internal/modules/zmqmod"
	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/parser"
	"github.com/maarten-boot/kukel/internal/token"
	"github.com/maarten-boot/kukel/internal/vm"
)

const usage = `usage: kukel <command> [arguments]

commands:
  run a.kkl b.kkl [-- args]       compile in memory and run
  check a.kkl ...                 compile only: report the first error, run nothing
  build [-o prog.kkc] a.kkl ...   compile to a bytecode file (default: a.kkc)
  exec prog.kkc [--] [args]       run a bytecode file
  disasm a.kkl ... | prog.kkc     show the bytecode in readable form
  tokens a.kkl ...                show the tokens of source files
  ast [-pos] a.kkl ...            show the syntax tree of source files
  version                         show the version, bytecode format and Go modules
  help                            show this text

Modules ("use name;") are name.kkl files in the directory of the file that
uses them, or in the directories of KUKEL_PATH.
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
	case "run":
		return cmdRun(args, stdin, stdout, stderr)
	case "check":
		return cmdCheck(args, stderr)
	case "disasm":
		return cmdDisasm(args, stdout, stderr)
	case "build":
		return cmdBuild(args, stderr)
	case "exec":
		return cmdExec(args, stdin, stdout, stderr)
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

// cmdBuild compiles source files into a bytecode file.
func cmdBuild(args []string, stderr io.Writer) int {
	out := ""
	var files []string
	for i := 0; i < len(args); i++ {
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
	prog, err := compileFiles(files)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	data, err := bytecode.Encode(prog)
	if err == nil {
		err = os.WriteFile(out, data, 0o644)
	}
	if err != nil {
		fmt.Fprintln(stderr, "kukel:", err)
		return 1
	}
	return 0
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
		fmt.Fprintln(stderr, "usage: kukel exec prog.kkc [args]")
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
