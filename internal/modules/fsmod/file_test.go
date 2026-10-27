package fsmod_test

import (
	"bufio"
	"bytes"
	"errors"
	"io/fs"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/builtins"
	"github.com/maarten-boot/kukel/internal/compiler"
	"github.com/maarten-boot/kukel/internal/modules/fsmod"
	_ "github.com/maarten-boot/kukel/internal/modules/workermod"
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
	if runtime.GOOS == "windows" {
		t.Skip("the messages of Windows are its own (TestFilesWindows)")
	}
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

// A last line that ends in a lone \r loses it, as with the built-in
// readline.
func TestFileLineEnds(t *testing.T) {
	t.Chdir(t.TempDir())
	os.WriteFile("cr.txt", []byte("a\r\nb\r"), 0o644)
	got, err := runIO(t, `use fs;

main() {
    local f = fs::open("cr.txt");
    println(f->readline(), len(f->readline()), f->readline(), len(readline()));
}
`, "x\r")
	if err != nil || got != "a 1 nil 1\n" {
		t.Errorf("End: %v; got %q", err, got)
	}
}

// for-each walks a file's lines (Q86); leaving the loop early leaves the
// file where reading stopped; a file closed in the loop ends it with an
// error; standard input is a file too.
func TestFileIter(t *testing.T) {
	t.Chdir(t.TempDir())
	os.WriteFile("l.txt", []byte("a\nb\r\nc"), 0o644)
	got, err := runIO(t, `use fs;

main() {
    with (local f = fs::open("l.txt")) {
        for (local i, line in f) print(i, line, "");
        println(f, f->iter());
    }
    with (local f = fs::open("l.txt")) {
        for (local line in f) break;
        println(f->readline());
        try { for (local line in f) f->close(); } catch (e) { println(e->kind, e->message); }
    }
    for (local line in fs::stdin()) print(line, "");
    println();
}
`, "x\ny\n")
	want := "0 a 1 b 2 c fs::File(\"l.txt\", \"r\") Generator(fs::File::iter)\nb\nValueError fs::File::iter: l.txt is closed\nx y \n"
	if err != nil || got != want {
		t.Errorf("got %q, %v\nwant %q", got, err, want)
	}
}

type failWriter struct{}

func (failWriter) Write(p []byte) (int, error) {
	return 0, &os.PathError{Op: "write", Path: "/dev/stdout", Err: errors.New("no space left on device")}
}

// flush and close of standard output report what cannot be written, as
// they do for a file; the built-in print words it without Go's "write".
func TestFileStandardFlushError(t *testing.T) {
	f, err := parser.ParseFile("t.kkl", []byte(`use fs;

main() {
    local out = fs::stdout();
    for (local call in [fn() { out->write("x")->flush(); }, fn() { out->close(); }, fn() { print("x"); }]) {
        try {
            call();
        } catch (e) {
            eprintln(e->kind + ": " + e->message);
        }
    }
}
`))
	if err != nil {
		t.Fatal(err)
	}
	prog, err := compiler.Compile([]*ast.File{f}, builtins.Names())
	if err != nil {
		t.Fatal(err)
	}
	var errOut bytes.Buffer
	m, err := vm.New(prog, builtins.Lookup, bufio.NewWriter(failWriter{}), strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	m.SetStderr(&errOut)
	m.Run(nil)
	want := "IOError: fs::File::flush: /dev/stdout: no space left on device\n" +
		"IOError: fs::File::close: /dev/stdout: no space left on device\n" +
		"IOError: /dev/stdout: no space left on device\n"
	if errOut.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", errOut.String(), want)
	}
}

// Temporary files and directories, chmod and truncate.
func TestTempChmodTruncate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the modes and messages of Windows are their own (TestFilesWindows)")
	}
	t.Chdir(t.TempDir())
	t.Setenv("TMPDIR", t.TempDir())
	got, err := runIO(t, `use fs;

main() {
    local f = fs::temp(), g = fs::temp("a-*.txt", ".");
    println(fs::dir(f->name()) == getenv("TMPDIR"), find(fs::base(f->name()), "kukel-") == 0, f->mode(), fs::stat(f->name())["mode"]);
    println(fs::dir(g->name()), find(g->name(), "a-") >= 0, fs::ext(g->name()), g->name() != fs::temp("a-*.txt", ".")->name());
    g->write("temporary")->close();
    println(readfile(g->name()), fs::exists(g->name()));
    local d = fs::tempdir("work-*", ".");
    println(fs::stat(d)["dir"], fs::stat(d)["mode"], find(d, "work-") >= 0);

    for (local m in ["rw-r--r--", "-rwx------", "rwsr-Sr-T", "r--r--r--"]) {
        fs::chmod(g->name(), m);
        print(fs::stat(g->name())["mode"], "");
    }
    println();
    fs::chmod(g->name(), "rw-------");

    with (local h = fs::open("t.txt", "w+")) {
        h->write("hello world");
        h->seek(5);
        h->truncate();
        println(h->tell(), readfile("t.txt"));
        h->write("!")->truncate(3);
        println(h->tell(), readfile("t.txt"));
        h->truncate(5);
        println(len(readfile("t.txt")), h->seek(0), len(h->read()));
    }
    for (local call in [fn() { fs::temp("a/b*"); }, fn() { fs::tempdir("x*", "nope/dir"); }, fn() { fs::chmod("t.txt", "rwxrwxrw"); },
            fn() { fs::chmod("t.txt", "rwxrwxrwz"); }, fn() { fs::chmod("nope", "rw-r--r--"); }, fn() { fs::open("t.txt")->truncate(); },
            fn() { fs::open("t.txt", "a")->truncate(-1); }, fn() { fs::stdout()->truncate(); }]) {
        try {
            call();
        } catch (e) {
            println(e->kind + ": " + e->message);
        }
    }
}
`, "")
	want := `true true w+ -rw-------
. true .txt true
temporary true
true drwx------ true
-rw-r--r-- -rwx------ -rwsr-Sr-T -r--r--r-- 
5 hello
6 hel
5 0 5
ValueError: fs::temp: the pattern "a/b*" has a path separator (the directory is the second argument)
`
	if err != nil || !strings.HasPrefix(got, want) {
		t.Errorf("End: %v; got:\n%s\nwant (first):\n%s", err, got, want)
	}
	for _, line := range []string{
		"IOError: fs::tempdir: nope/dir: no such file or directory",
		`ValueError: fs::chmod: the mode "rwxrwxrw" is not written as ls shows it ("rw-r--r--")`,
		`ValueError: fs::chmod: the mode "rwxrwxrwz" is not written as ls shows it ("rw-r--r--")`,
		"IOError: fs::chmod: nope: no such file or directory",
		"ValueError: fs::File::truncate: t.txt is not open for writing",
		"ValueError: fs::File::truncate: a negative size (-1)",
		"IOError: fs::File::truncate: <stdout>: a standard stream cannot be truncated",
	} {
		if !strings.Contains(got, line) {
			t.Errorf("no %q in:\n%s", line, got)
		}
	}
}

// ParseMode reads every mode ModeString writes, and only those.
func TestParseMode(t *testing.T) {
	for m := fs.FileMode(0); m < 0o1000; m++ {
		for _, special := range []fs.FileMode{0, fs.ModeSetuid, fs.ModeSetgid, fs.ModeSticky, fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky} {
			s := fsmod.ModeString(m | special)
			if got, ok := fsmod.ParseMode(s); !ok || got != m|special {
				t.Fatalf("%s: %v, %v", s, got, ok)
			}
			if got, ok := fsmod.ParseMode(s[1:]); !ok || got != m|special {
				t.Fatalf("%s: %v, %v", s[1:], got, ok)
			}
		}
	}
	for _, s := range []string{"", "rw-r--r-", "rw-r--r--x", "xw-r--r--", "rw-r--r-s", "rwtr--r--", "rw-r--r--\n"} {
		if _, ok := fsmod.ParseMode(s); ok {
			t.Errorf("%q is taken", s)
		}
	}
}

// Locks: between two files of one program and between a program and its
// worker, with and without a timeout, shared and exclusive, given up by
// unlock, close and the end of the program; waiting stops when the program
// is cancelled.
func TestLocks(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runIO(t, `use fs;
use worker;

holder() {
    local f = fs::open("lock.txt", "a");
    f->lock("shared");
    worker::send("shared");
    worker::receive();
    f->lock();
    worker::send("exclusive");
    worker::receive();
    f->unlock();
    f->unlock();
    worker::send("unlocked");
    worker::receive();
    f->lock();
}

main() {
    writefile("lock.txt", "");
    local w = worker::start(holder), mine = fs::open("lock.txt");
    println(w->receive(), mine->lock("shared", 0), mine->lock("exclusive", 0), mine->lock("shared", 0));
    mine->unlock();
    w->send("go");
    println(w->receive(), mine->lock("shared", 0), mine->lock("shared", 0.1));
    local t0 = clock();
    w->send("go");
    println(w->receive(), mine->lock("exclusive", 5), clock() - t0 < 4);
    mine->close();
    w->send("go");
    w->wait();
    local a = fs::open("lock.txt"), b = fs::open("lock.txt");
    println(a->lock(), b->lock("shared", 0));
    a->close();
    println(b->lock("shared", 0), b->lock(), fs::open("lock.txt")->lock("shared", 0.05));
}
`, "")
	want := "shared true false true\nexclusive false false\nunlocked true true\ntrue false\ntrue true false\n"
	if err != nil || got != want {
		t.Errorf("End: %v; got:\n%s\nwant:\n%s", err, got, want)
	}

	// Waiting for a lock stops when the program is cancelled.
	f, err := parser.ParseFile("t.kkl", []byte("use fs;\n\nmain() {\n    local a = fs::open(\"lock.txt\"), b = fs::open(\"lock.txt\");\n    a->lock();\n    b->lock();\n}\n"))
	if err != nil {
		t.Fatal(err)
	}
	prog, err := compiler.Compile([]*ast.File{f}, builtins.Names())
	if err != nil {
		t.Fatal(err)
	}
	m, err := vm.New(prog, builtins.Lookup, &bytes.Buffer{}, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	c := m.Cancellable()
	time.AfterFunc(100*time.Millisecond, func() { c.Cancel(errors.New("stop")) })
	start := time.Now()
	if _, err := m.Run(nil); err == nil || time.Since(start) > 2*time.Second {
		t.Errorf("cancelled after %v: %v", time.Since(start), err)
	}
	m.End()
}
