// Command kukel compiles and runs Kukel programs.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"kukel/internal/ast"
	"kukel/internal/builtins"
	"kukel/internal/bytecode"
	"kukel/internal/compiler"
	"kukel/internal/lexer"
	"kukel/internal/parser"
	"kukel/internal/token"
	"kukel/internal/vm"
)

const usage = `usage: kukel <command> [arguments]

commands:
  run a.kkl b.kkl [-- args]       compile in memory and run
  build -o prog.kkc a.kkl b.kkl   compile to a bytecode file
  exec prog.kkc [args]            run a bytecode file
  disasm a.kkl ... | prog.kkc     show the bytecode in readable form
  tokens a.kkl ...                show the tokens of source files
  ast [-pos] a.kkl ...            show the syntax tree of source files
  help                            show this text
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
	case "tokens":
		return cmdTokens(args, stdout, stderr)
	case "ast":
		return cmdAST(args, stdout, stderr)
	case "run":
		return cmdRun(args, stdin, stdout, stderr)
	case "disasm":
		return cmdDisasm(args, stdout, stderr)
	case "build", "exec":
		return notYet(cmd, 10, stderr)
	}
	fmt.Fprintf(stderr, "kukel: unknown command %q\n\n%s", cmd, usage)
	return 2
}

func notYet(cmd string, milestone int, stderr io.Writer) int {
	fmt.Fprintf(stderr, "kukel: %s is not implemented yet (milestone %d)\n", cmd, milestone)
	return 2
}

// compileFiles parses and compiles source files into one program.
func compileFiles(files []string) (*bytecode.Program, error) {
	var parsed []*ast.File
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		f, err := parser.ParseFile(file, src)
		if err != nil {
			return nil, err
		}
		parsed = append(parsed, f)
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
	out := bufio.NewWriter(stdout)
	defer out.Flush()
	machine, err := vm.New(prog, builtins.Lookup, out, stdin)
	if err != nil {
		fmt.Fprintln(stderr, "kukel:", err)
		return 1
	}
	code, err := machine.Run(progArgs)
	if err != nil {
		out.Flush()
		fmt.Fprintln(stderr, err)
		return 1
	}
	return code
}

// cmdDisasm prints the bytecode of a program compiled from source files.
// (Reading .kkc files comes with milestone 10.)
func cmdDisasm(files []string, stdout, stderr io.Writer) int {
	if len(files) == 0 {
		fmt.Fprintln(stderr, "usage: kukel disasm a.kkl ...")
		return 2
	}
	for _, f := range files {
		if strings.HasSuffix(f, ".kkc") {
			return notYet("disasm of a .kkc file", 10, stderr)
		}
	}
	prog, err := compileFiles(files)
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
