package main

import (
	"bufio"
	"bytes"
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
// terminal is in raw mode only while a line is read. A program's
// readline() reads through the editor too (input), so that lines typed
// ahead reach it, not the prompt; they do not go into the history.

// errInterrupted is what readLine gives for Ctrl-C: the piece being typed
// is dropped.
var errInterrupted = errors.New("interrupted")

// historyMax is how many lines the history (and its file) keeps.
const historyMax = 1000

type lineEditor struct {
	fd      int
	t       *term.Terminal
	keys    *keyReader
	file    string // the history file, or ""
	history *history
}

// history is the lines up and down go through: the last historyMax lines
// that are not blank, without what a program reads (term.Terminal's own
// keeps 100, and blank lines too).
type history struct {
	lines []string // the oldest first
	off   bool     // a program reads: lines are not added
}

func (h *history) Add(line string) {
	if h.off || strings.TrimSpace(line) == "" {
		return
	}
	h.lines = append(h.lines, line)
	if len(h.lines) > historyMax {
		h.lines = h.lines[len(h.lines)-historyMax:]
	}
}

func (h *history) Len() int { return len(h.lines) }

// At gives entry i, 0 being the newest.
func (h *history) At(i int) string { return h.lines[len(h.lines)-1-i] }

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
	e.history = &history{}
	e.t.History = e.history
	e.file = historyFile()
	if e.file != "" {
		if f, err := os.Open(e.file); err == nil {
			s := bufio.NewScanner(f)
			for s.Scan() {
				e.history.Add(s.Text())
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
// errInterrupted. Lines that are not blank go into the history (unless a
// program reads them).
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
	if strings.TrimSpace(line) != "" && !e.history.off {
		e.save(line)
	}
	return line + "\n", nil
}

// save adds a line, which the history has just taken, to the history file,
// which keeps the last historyMax lines too.
func (e *lineEditor) save(line string) {
	if e.file == "" {
		return
	}
	if len(e.history.lines) == historyMax { // the oldest may have gone: write it all
		os.WriteFile(e.file, []byte(strings.Join(e.history.lines, "\n")+"\n"), 0o600)
		return
	}
	if f, err := os.OpenFile(e.file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		f.WriteString(line + "\n")
		f.Close()
	}
}

// input is the standard input of the programs the REPL runs: lines read
// through the editor, without a prompt and not into the history. Ctrl-C
// or Ctrl-D ends what the program is reading (as the end of the input).
// The REPL reads its pieces through it too (replInput): a line it looked
// at but did not take (peek) is what the program reads first.
func (e *lineEditor) input() *editorInput { return &editorInput{e: e} }

type editorInput struct {
	e    *lineEditor
	rest []byte // of the last line read
}

func (in *editorInput) line(prompt string) (string, error) {
	line, err := in.peek(prompt)
	if err == nil {
		in.take()
	}
	return line, err
}

func (in *editorInput) peek(prompt string) (string, error) {
	if len(in.rest) == 0 {
		line, err := in.e.readLine(prompt)
		if err != nil {
			return "", err
		}
		in.rest = []byte(line)
	}
	if i := bytes.IndexByte(in.rest, '\n'); i >= 0 {
		return string(in.rest[:i+1]), nil
	}
	return string(in.rest), nil
}

func (in *editorInput) take() {
	if i := bytes.IndexByte(in.rest, '\n'); i >= 0 {
		in.rest = in.rest[i+1:]
	} else {
		in.rest = nil
	}
}

func (in *editorInput) Read(p []byte) (int, error) {
	if len(in.rest) == 0 {
		in.e.history.off = true
		line, err := in.e.readLine("")
		in.e.history.off = false
		if err != nil {
			return 0, io.EOF
		}
		in.rest = []byte(line)
	}
	n := copy(p, in.rest)
	in.rest = in.rest[n:]
	return n, nil
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
