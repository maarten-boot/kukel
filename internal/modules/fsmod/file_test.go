package fsmod_test

import (
	"bufio"
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/builtins"
	"github.com/maarten-boot/kukel/internal/compiler"
	"github.com/maarten-boot/kukel/internal/parser"
	"github.com/maarten-boot/kukel/internal/vm"
)

// runIO runs src with stdin as its standard input, standard output
// buffered as kukel buffers it, and standard error into the same buffer;
// then ends the program as kukel does (VM.End) and gives the output and
// End's error.
func runIO(t *testing.T, src, stdin string) (string, error) {
	t.Helper()
	f, err := parser.ParseFile("t.kkl", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	prog, err := compiler.Compile([]*ast.File{f}, builtins.Names())
	if err != nil {
		t.Fatal(err)
	}
	var all bytes.Buffer
	out := bufio.NewWriter(&all)
	m, err := vm.New(prog, builtins.Lookup, out, strings.NewReader(stdin))
	if err != nil {
		t.Fatal(err)
	}
	m.SetStderr(&all)
	if _, err := m.Run(nil); err != nil {
		t.Fatalf("%v\noutput: %s", err, all.String())
	}
	err = m.End()
	out.Flush()
	return all.String(), err
}

// Writing, reading, the position, mixing reads and writes on a "+" file,
// and the errors of the member functions.
func TestFile(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runIO(t, `use fs;

main() {
    with (local f = fs::open("a.txt", "w")) {
        println(f->write("one\n", "two\r\n")->tell());
        f->println("three", 3, [4]);
        f->print("x", nil);
        println(f, f->name(), f->mode(), f->closed());
    }
    local f = fs::open("a.txt", "r+");
    println(f->readline(), f->readline(), f->tell());
    f->write("TH");
    println(f->tell(), f->seek(0), f->read(3), f->read(0), f->seek(1, "current"));
    println(f->readline(), f->seek(-5, "end"), f->read(100), f->read(), f->read(1), f->readline());
    f->close();
    f->close();
    println(f, f->closed(), readfile("a.txt") == "one\ntwo\r\nTHree 3 [4]\nx nil");
    for (local call in [fn() { f->read(); }, fn() { f->tell(); },
            fn() { fs::open("a.txt")->write("x"); }, fn() { fs::open("a.txt", "a")->readline(); },
            fn() { fs::open("a.txt")->read(-1); }, fn() { fs::open("a.txt")->seek(0, "middle"); },
            fn() { fs::open("a.txt")->seek(-1); }, fn() { fs::open("a.txt", "w")->write(1); },
            fn() { fs::open("a.txt", "rw"); }, fn() { fs::open("a.txt", "x"); }, fn() { fs::open("."); },
            fn() { fs::open("nope/a.txt"); }, fn() { fs::open("nope/a.txt", "w"); }, fn() { fs::open(1); }]) {
        try {
            call();
        } catch (e) {
            println(e->kind + ": " + e->message);
        }
    }
    with (local n = fs::open("new.txt", "x+")) println(n->write("abc")->seek(0), n->read());
    with (local a = fs::open("new.txt", "a+")) println(a->write("d")->tell(), a->seek(0), a->read(), a->write("e")->tell());
    println(readfile("new.txt"));
}
`, "")
	want := `9
fs::File("a.txt", "w") a.txt w false
one two 9
11 0 one  4
two 21 x nil nil nil nil
fs::File("a.txt", closed) true true
ValueError: fs::File::read: a.txt is closed
ValueError: fs::File::tell: a.txt is closed
ValueError: fs::File::write: a.txt is not open for writing
ValueError: fs::File::readline: a.txt is not open for reading
ValueError: fs::File::read: a negative number of bytes (-1)
ValueError: fs::File::seek: whence must be "start", "current" or "end", not "middle"
IOError: fs::File::seek: a.txt: invalid argument
TypeError: fs::File::write: argument 1 must be a string, not int
ValueError: fs::open: the mode "rw" is not one of "r", "w", "a" and "x", with or without "+"
IOError: fs::open: a.txt: file exists
IOError: fs::open: .: is a directory
IOError: fs::open: nope/a.txt: no such file or directory
IOError: fs::open: nope/a.txt: no such file or directory
TypeError: fs::open: argument 1 must be a string, not int
0 abc
4 0 abcd 5
abcde
`
	if err != nil || got != want {
		t.Errorf("End: %v; got:\n%s\nwant:\n%s", err, got, want)
	}
}

// A large file, line by line and in pieces: the buffer's edges, and the
// position after each line.
func TestFileLarge(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runIO(t, `use fs;

main() {
    with (local f = fs::open("big.txt", "w")) {
        for (local i = 0; i < 100000; i++) f->println("line", i);
    }
    local size = len(readfile("big.txt")), n = 0, sum = 0, ok = true, line;
    with (local f = fs::open("big.txt")) {
        while ((line = f->readline()) != nil) {
            n++;
            sum += int(split(line, " ")[1]);
            if (n % 9973 == 0) ok = ok && f->tell() == len(join(slice(split(readfile("big.txt"), "\n"), 0, n), "\n")) + 1;
        }
        println(n, sum, ok, f->tell() == size);
        f->seek(0);
        local total = 0, piece;
        while ((piece = f->read(4096)) != nil) total += len(piece);
        println(total == size);
    }
}
`, "")
	if err != nil || got != "100000 4999950000 true true\ntrue\n" {
		t.Errorf("End: %v; got %q", err, got)
	}
}

// The standard streams: standard input shared with readline, standard
// output with println, standard error after what was printed before it;
// close only flushes, and they have no position.
func TestFileStandard(t *testing.T) {
	got, err := runIO(t, `use fs;

main() {
    local inp = fs::stdin(), out = fs::stdout();
    println(inp, out, fs::stderr(), inp == fs::stdin(), inp->name(), inp->mode(), out->mode());
    println(inp->readline(), readline(), inp->read(3), inp->readline(), readline(), inp->read(), inp->readline());
    print("a ");
    fs::stderr()->println("b");
    out->write("c ")->print("d", 1);
    println();
    out->close();
    println(out->closed());
    for (local call in [fn() { inp->tell(); }, fn() { out->seek(0); }, fn() { inp->write("x"); }, fn() { out->read(); }]) {
        try {
            call();
        } catch (e) {
            println(e->kind + ": " + e->message);
        }
    }
}
`, "one\r\ntwo\nthree\nfour\nfive")
	want := `fs::File(<stdin>) fs::File(<stdout>) fs::File(<stderr>) true <stdin> r w
one two thr ee four five nil
a b
c d 1
false
IOError: fs::File::tell: <stdin>: a standard stream has no position
IOError: fs::File::seek: <stdout>: a standard stream has no position
ValueError: fs::File::write: <stdin> is not open for writing
ValueError: fs::File::read: <stdout> is not open for reading
`
	if err != nil || got != want {
		t.Errorf("End: %v; got:\n%s\nwant:\n%s", err, got, want)
	}
}

// Files the program did not close are closed when it ends, written out;
// a file that cannot be written out is End's error.
func TestFileLeftOpen(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := runIO(t, `use fs;

global keep;

main() {
    keep = fs::open("b.txt", "w");
    keep->write("kept");
    fs::open("a.txt", "a")->write("dropped");
}
`, "")
	if a, _ := os.ReadFile("a.txt"); err != nil || string(a) != "dropped" {
		t.Errorf("End: %v, a.txt: %q", err, a)
	}
	if b, _ := os.ReadFile("b.txt"); string(b) != "kept" {
		t.Errorf("b.txt: %q", b)
	}
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("no /dev/full")
	}
	_, err = runIO(t, `use fs;

main() {
    fs::open("/dev/full", "w")->write("x");
    try {
        fs::open("/dev/full", "w")->write("x")->flush();
    } catch (e) {
        println(e->message);
    }
}
`, "")
	if err == nil || err.Error() != "/dev/full: no space left on device" {
		t.Errorf("End: %v", err)
	}
}
