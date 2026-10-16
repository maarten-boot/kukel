package main

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"
)

// The line editing of kukel repl on a terminal, with golang.org/x/term:
// the arrow keys, Home and End, Ctrl-A, -E, -K, -U and -W, and the lines
// typed before (up and down), also those of earlier sessions (the file
// ~/.kukel_history, or $KUKEL_HISTORY; KUKEL_HISTORY= keeps none). The
// terminal is in raw mode only while a line is read, so a program's
// readline() reads as usual.

// errInterrupted is what readLine gives for Ctrl-C: the piece being typed
// is dropped.
var errInterrupted = errors.New("interrupted")

// historyMax is how many lines the history file keeps.
const historyMax = 1000

type lineEditor struct {
	fd      int
	t       *term.Terminal
	keys    *keyReader
	file    string // the history file, or ""
	history []string
}

// newLineEditor gives an editor for the terminal stdin and stdout, or nil
// when they are not both terminals.
func newLineEditor(stdin, stdout *os.File) *lineEditor {
	if !term.IsTerminal(int(stdin.Fd())) || !term.IsTerminal(int(stdout.Fd())) {
		return nil
	}
	keys := &keyReader{r: stdin}
	e := &lineEditor{fd: int(stdin.Fd()), keys: keys}
	e.t = term.NewTerminal(struct {
		io.Reader
		io.Writer
	}{keys, stdout}, "")
	e.file = historyFile()
	if e.file != "" {
		if f, err := os.Open(e.file); err == nil {
			s := bufio.NewScanner(f)
			for s.Scan() {
				if line := s.Text(); line != "" {
					e.history = append(e.history, line)
					e.t.History.Add(line)
				}
			}
			f.Close()
		}
	}
	return e
}

func historyFile() string {
	if f, ok := os.LookupEnv("KUKEL_HISTORY"); ok {
		return f
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".kukel_history")
}

// readLine reads a line (with its "\n") after prompt. At the end of the
// input (Ctrl-D on an empty line) it gives io.EOF; for Ctrl-C,
// errInterrupted.
func (e *lineEditor) readLine(prompt string) (string, error) {
	old, err := term.MakeRaw(e.fd)
	if err != nil {
		return "", err
	}
	defer term.Restore(e.fd, old)
	if w, h, err := term.GetSize(e.fd); err == nil && w > 0 && h > 0 { // a terminal that knows its size
		e.t.SetSize(w, h)
	}
	e.t.SetPrompt(prompt)
	line, err := e.t.ReadLine()
	if e.keys.interrupted {
		e.keys.interrupted = false
		e.t.Write([]byte("^C\n"))
		return "", errInterrupted
	}
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(line) != "" {
		e.remember(line)
	}
	return line + "\n", nil
}

// remember adds a line to the history file, which keeps the last
// historyMax lines.
func (e *lineEditor) remember(line string) {
	if e.file == "" {
		return
	}
	e.history = append(e.history, line)
	if len(e.history) > historyMax {
		e.history = e.history[len(e.history)-historyMax:]
		os.WriteFile(e.file, []byte(strings.Join(e.history, "\n")+"\n"), 0o600)
		return
	}
	if f, err := os.OpenFile(e.file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		f.WriteString(line + "\n")
		f.Close()
	}
}

// keyReader passes on the keys typed, except Ctrl-C: the editor would end
// the input with it. It becomes Ctrl-A, Ctrl-K, Enter (an empty line) and
// is noted, so that readLine can tell.
type keyReader struct {
	r           io.Reader
	interrupted bool
}

func (k *keyReader) Read(p []byte) (int, error) {
	n, err := k.r.Read(p)
	if i := strings.IndexByte(string(p[:n]), 3); i >= 0 {
		k.interrupted = true
		copy(p, []byte{1, 11, 13}) // what came before ^C is dropped too, with the line
		return 3, err
	}
	return n, err
}
