//go:build !no9p

package p9mod_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"9fans.net/go/plan9"
	plan9client "9fans.net/go/plan9/client"
	"github.com/hugelgupf/p9/p9"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/builtins"
	"github.com/maarten-boot/kukel/internal/compiler"
	_ "github.com/maarten-boot/kukel/internal/modules/eventsmod"
	_ "github.com/maarten-boot/kukel/internal/modules/p9mod"
	_ "github.com/maarten-boot/kukel/internal/modules/timemod"
	_ "github.com/maarten-boot/kukel/internal/modules/workermod"
	"github.com/maarten-boot/kukel/internal/parser"
	"github.com/maarten-boot/kukel/internal/vm"
)

func run(t *testing.T, src string, args ...string) string {
	t.Helper()
	out, err := runErr(t, src, args...)
	if err != nil {
		t.Fatalf("%v\noutput: %s", err, out)
	}
	return out
}

func runErr(t *testing.T, src string, args ...string) (string, error) {
	t.Helper()
	f, err := parser.ParseFile("t.kkl", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	prog, err := compiler.Compile([]*ast.File{f}, builtins.Names())
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	m, err := vm.New(prog, builtins.Lookup, &out, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Run(args)
	return out.String(), err
}

// A program serves a tree and reads it with its own client, in both
// versions: the functions of the files run while the client waits.
func TestBothVersions(t *testing.T) {
	got := run(t, `use p9;
main() {
    local log = [], n = 0, t = new p9::Tree();
    t->file("/motd", "hello\n");
    t->file("/sub/count", fn() { n++; return "count " + str(n) + "\n"; });
    t->file("/sub/log", "", fn(data) { push(log, data); });
    t->file("/bad", fn() { throw new Error("no luck"); });
    t->file("/num", fn() { return 42; });
    local s = p9::serve(t, "tcp://127.0.0.1:0");
    println(t);
    for (local v in ["9P2000", "9P2000.L"]) {
        local c = p9::dial(s->endpoint(), {"version": v});
        println(c->version());
        for (local d in ["/", "sub"])
            for (local e in c->readdir(d)) println(" ", e["name"], e["size"], e["dir"], e["link"], e["mode"]);
        print(c->read("/motd"), c->read("/sub/count"), c->read("sub/../sub/count"));
        c->write("/sub/log", "written with " + v);
        c->write("/sub/log", "");
        println(c->stat("/"), c->exists("/nope"), c->exists("/sub"));
        for (local f in [fn() { c->read("/nope"); }, fn() { c->write("/motd", "x"); }, fn() { c->write("/new", "x"); },
                         fn() { c->mkdir("/d"); }, fn() { c->remove("/motd"); }, fn() { c->read("/bad"); },
                         fn() { c->read("/num"); }, fn() { c->readdir("/motd"); }, fn() { c->read("/sub"); }])
            try { f(); } catch (e) { println(" ", e->kind + ":", e->message); }
        c->close();
        println(c->version());
    }
    println(log);
    s->close();
    s->close();
}`)
	got = regexp(got)
	want := `p9::Tree(5 files)
9P2000
  bad 0 false false -r--r--r--
  motd 6 false false -r--r--r--
  num 0 false false -r--r--r--
  sub 0 true false dr-xr-xr-x
  count 0 false false -r--r--r--
  log 0 false false -rw-rw-rw-
hello
 count 1
 count 2
{"name": "/", "size": 0, "dir": true, "link": false, "mode": "dr-xr-xr-x", "time": TIME} false true
  IOError: p9::Client::read: /nope: file does not exist
  IOError: p9::Client::write: /motd: permission denied: the file cannot be written
  IOError: p9::Client::write: /new: permission denied: the tree is made by the server program
  IOError: p9::Client::mkdir: /d: permission denied: the tree is made by the server program
  IOError: p9::Client::remove: /motd: permission denied: the tree is made by the server program
  IOError: p9::Client::read: /bad: no luck
  IOError: p9::Client::read: /num: the content function of /num gave int, not a string
  IOError: p9::Client::readdir: /motd: not a directory
  IOError: p9::Client::read: /sub: is a directory
9P2000
9P2000.L
  bad 0 false false -r--r--r--
  motd 6 false false -r--r--r--
  num 0 false false -r--r--r--
  sub 0 true false dr-xr-xr-x
  count 0 false false -r--r--r--
  log 0 false false -rw-rw-rw-
hello
 count 3
 count 4
{"name": "/", "size": 0, "dir": true, "link": false, "mode": "dr-xr-xr-x", "time": TIME} false true
  IOError: p9::Client::read: /nope: no such file or directory
  IOError: p9::Client::write: /motd: permission denied
  IOError: p9::Client::write: /new: permission denied
  IOError: p9::Client::mkdir: /d: permission denied
  IOError: p9::Client::remove: /motd: permission denied
  IOError: p9::Client::read: /bad: input/output error
  IOError: p9::Client::read: /num: input/output error
  IOError: p9::Client::readdir: /motd: not a directory
  IOError: p9::Client::read: /sub: is a directory
9P2000.L
["written with 9P2000", "", "written with 9P2000.L", ""]
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// regexp hides the times of entries.
func regexp(s string) string {
	for {
		i := strings.Index(s, "time::Time(")
		if i < 0 {
			return s
		}
		j := strings.IndexByte(s[i:], ')')
		s = s[:i] + "TIME" + s[i+j+1:]
	}
}

// The tree changes while it is served; dial finds the version itself;
// aname attaches to a directory; a Unix socket.
func TestTreeChanges(t *testing.T) {
	sock := socketPath(t)
	got := run(t, `use p9;
main(args) {
    local t = new p9::Tree();
    t->dir("/a/b");
    t->file("/a/f", "one");
    local s = p9::serve(t, "unix://" + args[0]);
    local c = p9::dial("unix://" + args[0]);
    println(c->version(), c->read("/a/f"));
    local v1 = c->stat("/a/f")["size"];
    t->file("/a/f", "three");
    println(c->read("/a/f"), v1, c->stat("/a/f")["size"]);
    t->remove("/a/b");
    println(len(c->readdir("/a")), c->exists("/a/b"));
    local sub = p9::dial("unix://" + args[0], {"aname": "a", "version": "9P2000"});
    println(sub->version(), sub->read("f"), sub->read("/f"));
    local subL = p9::dial("unix://" + args[0], {"aname": "/a"});
    println(subL->read("f"), subL->readdir("/")[0]["name"]);
    for (local x in [c, sub, subL]) x->close();
    s->close();
    println(s, t);
}`, sock)
	want := "9P2000.L one\nthree 3 5\n1 false\n9P2000 three three\nthree f\np9::Server(unix://" + sock + ", closed) p9::Tree(1 file)\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Files larger than a message, both ways.
func TestLarge(t *testing.T) {
	got := run(t, `use p9;
main() {
    local big = "", got = nil, t = new p9::Tree();
    for (local i = 0; i < 30000; i++) big += "0123456789";
    t->file("/big", big, fn(data) { got = data; });
    local s = p9::serve(t, "tcp://127.0.0.1:0");
    for (local v in ["9P2000", "9P2000.L"]) {
        local c = p9::dial(s->endpoint(), {"version": v});
        println(len(c->read("/big")) == len(big), c->read("/big") == big);
        c->write("/big", big + big);
        println(got == big + big);
        c->close();
    }
    s->close();
}`)
	if got != "true true\ntrue\ntrue true\ntrue\n" {
		t.Errorf("got %q", got)
	}
}

func TestErrors(t *testing.T) {
	got := run(t, strings.ReplaceAll(`use p9;
class T : p9::Tree { T(); }
T::T() {}
main() {
    local t = new p9::Tree();
    t->file("/f", "x");
    local s = p9::serve(t, "tcp://127.0.0.1:0");
    local c = p9::dial(s->endpoint());
    c->close();
    local bad = [fn() { t->file("/f/g", "x"); },
                 fn() { t->dir("/f"); },
                 fn() { t->file("/", "x"); },
                 fn() { t->file("/d", 5); },
                 fn() { t->file("/d", "x", "y"); },
                 fn() { t->remove("/"); },
                 fn() { t->remove("/nope"); },
                 fn() { p9::serve(t, "http://x"); },
                 fn() { p9::serve(5, "tcp://:0"); },
                 fn() { p9::serve(t, s->endpoint()); },
                 fn() { p9::dial("localhost:564"); },
                 fn() { p9::dial("tcp://"); },
                 fn() { p9::dial(s->endpoint(), {"version": "9P1"}); },
                 fn() { p9::dial(s->endpoint(), {"colour": "red"}); },
                 fn() { p9::dial(s->endpoint(), {"timeout": -1}); },
                 fn() { p9::dial(s->endpoint(), {"user": 5}); },
                 fn() { p9::dial(s->endpoint(), []); },
                 fn() { c->read("/f"); },
                 fn() { p9::dial("tcp://127.0.0.1:99999"); },
                 fn() { p9::serve(t, "unix:///LONG"); },
                 fn() { p9::serve(new T(), "tcp://127.0.0.1:0"); },
                 fn() { p9::dial(s->endpoint(), {"timeout": 1e300})->close(); throw new Error("no limit", "OK"); }];
    for (local f in bad) try { f(); } catch (e) { println(e->kind + ":", replace(e->message, s->endpoint(), "EP")); }
    s->close();
    try { p9::dial(s->endpoint(), {"timeout": 2}); } catch (e) { println(e->kind, replace(e->message, s->endpoint(), "EP")); }
    println(c);
}`, "LONG", strings.Repeat("x", 200)))
	want := `ValueError: p9::Tree::file: /f is a file, not a directory
ValueError: p9::Tree::dir: /f is a file
ValueError: p9::Tree::file: the top of the tree is a directory
TypeError: p9::Tree::file: argument 2 must be a string or a function, not int
TypeError: p9::Tree::file: argument 3 must be a function, not string
ValueError: p9::Tree::remove: the top of the tree cannot be removed
ValueError: p9::Tree::remove: /nope does not exist
ValueError: p9::serve: "http://x" is not a 9P address such as "tcp://*:564" or "unix:///path"
TypeError: p9::serve: argument 1 must be a p9::Tree or a p9::Disk, not int
NetError: p9::serve: EP: address already in use
ValueError: p9::dial: "localhost:564" is not a 9P address such as "tcp://host:564" or "unix:///path"
ValueError: p9::dial: "tcp://" is not a 9P address such as "tcp://host:564" or "unix:///path"
ValueError: p9::dial: version "9P1": the versions are "9P2000.L" and "9P2000"
ValueError: p9::dial: unknown option "colour" (the options are version, user, aname and timeout)
ValueError: p9::dial: -1 seconds is not a usable timeout
TypeError: p9::dial: option user must be a string, not int
TypeError: p9::dial: argument 2 must be a map of options, not vector
ValueError: p9::Client::read: the client is closed
ValueError: p9::dial: "tcp://127.0.0.1:99999" has no port number from 0 to 65535
ValueError: p9::serve: "unix:///LONG" is a socket path longer than 107 bytes
ValueError: p9::serve: argument 1: the p9::Tree part of this T object is not set up: its constructor must call p9::Tree::Tree(...)
OK: no limit
NetError p9::dial: EP: connection refused
p9::Client(ENDPOINT, closed)
`
	got = strings.ReplaceAll(got, strings.Repeat("x", 200), "LONG")
	if runtime.GOOS != "linux" {
		want = strings.Replace(want, "107", "103", 1)
	}
	got = maskAddr(got)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// maskAddr hides the addresses the test cannot know.
func maskAddr(s string) string {
	var b strings.Builder
	for _, line := range strings.SplitAfter(s, "\n") {
		if i := strings.Index(line, "listen tcp "); i >= 0 {
			j := strings.Index(line, ": bind")
			line = line[:i+len("listen tcp ")] + "ADDR" + line[j:]
		}
		if i := strings.Index(line, "tcp://127.0.0.1:"); i >= 0 && strings.HasPrefix(line, "p9::Client(") {
			line = "p9::Client(ENDPOINT, closed)\n"
		}
		b.WriteString(line)
	}
	return b.String()
}

// A server program in events::run(), and clients of other programs (here
// Go, in both versions) that use it at the same time.
func TestOtherClients(t *testing.T) {
	sock := socketPath(t)
	done := make(chan string)
	go func() {
		out, err := runErr(t, `use p9; use events;
main(args) {
    local n = 0, t = new p9::Tree(), s = nil;
    t->file("/count", fn() { n++; return str(n); });
    t->file("/static", "the same");
    t->file("/stop", "", fn(data) { println("stop:", data); s->close(); });
    s = p9::serve(t, "unix://" + args[0]);
    events::run();
    println("served", n);
}`, sock)
		if err != nil {
			out += err.Error()
		}
		done <- out
	}()
	for i := 0; ; i++ {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		if i == 500 {
			t.Fatal("the server did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	const clients, reads = 4, 25
	var wg sync.WaitGroup
	counts := make(chan string, 2*clients*reads)
	errs := make(chan error, 2*clients)
	for i := 0; i < clients; i++ {
		wg.Add(2)
		go func() { // 9P2000
			defer wg.Done()
			fsys, err := plan9client.Mount("unix", sock)
			if err != nil {
				errs <- err
				return
			}
			defer fsys.Close()
			for j := 0; j < reads; j++ {
				for _, name := range []string{"count", "static"} {
					f, err := fsys.Open(name, plan9.OREAD)
					if err != nil {
						errs <- err
						return
					}
					b, err := io.ReadAll(f)
					f.Close()
					if err != nil {
						errs <- err
						return
					}
					if name == "count" {
						counts <- string(b)
					} else if string(b) != "the same" {
						errs <- fmt.Errorf("static: %q", b)
					}
				}
			}
		}()
		go func() { // 9P2000.L
			defer wg.Done()
			conn, err := net.Dial("unix", sock)
			if err != nil {
				errs <- err
				return
			}
			c, err := p9.NewClient(conn)
			if err != nil {
				errs <- err
				return
			}
			defer c.Close()
			root, err := c.Attach("/")
			if err != nil {
				errs <- err
				return
			}
			for j := 0; j < reads; j++ {
				_, f, err := root.Walk([]string{"count"})
				if err != nil {
					errs <- err
					return
				}
				if _, _, err := f.Open(p9.ReadOnly); err != nil {
					errs <- err
					return
				}
				buf := make([]byte, 100)
				n, err := f.ReadAt(buf, 0)
				f.Close()
				if err != nil && !errors.Is(err, io.EOF) {
					errs <- err
					return
				}
				counts <- string(buf[:n])
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	close(counts)
	seen := map[string]bool{}
	for c := range counts {
		if seen[c] {
			t.Errorf("count %s twice", c)
		}
		seen[c] = true
	}
	total := clients*reads + clients*reads
	if len(seen) != total {
		t.Errorf("%d different counts, want %d", len(seen), total)
	}
	// Stop the server by writing to /stop.
	fsys, err := plan9client.Mount("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	f, err := fsys.Open("stop", plan9.OWRITE|plan9.OTRUNC)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("now"))
	f.Close()
	select {
	case out := <-done:
		if want := fmt.Sprintf("stop: now\nserved %d\n", total); out != want {
			t.Errorf("got %q, want %q", out, want)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the server program did not end")
	}
}

// A server in a worker, a client in the main program.
func TestWorker(t *testing.T) {
	got := run(t, `use p9; use events; use worker;
serve() {
    local t = new p9::Tree(), s = nil;
    t->file("/hello", fn() { return "hello from the worker"; });
    t->file("/quit", "", fn(data) { s->close(); });
    s = p9::serve(t, "tcp://127.0.0.1:0");
    worker::send(s->endpoint());
    events::run();
}
main() {
    local w = worker::start(serve);
    local c = p9::dial(w->receive(5));
    println(c->read("/hello"));
    c->write("/quit", "");
    c->close();
    w->wait();
    println("done");
}`)
	if got != "hello from the worker\ndone\n" {
		t.Errorf("got %q", got)
	}
}

func timeAfter10ms() <-chan time.Time { return time.After(10 * time.Millisecond) }

// What fails between client and server: a server that never answers (the
// timeout covers the handshake), an attach the server refuses, a lost
// connection; exists() fails then instead of giving false.
func TestClientFailures(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() { // accepts, and never answers
		var held []net.Conn
		for {
			c, err := ln.Accept()
			if err != nil {
				for _, c := range held {
					c.Close()
				}
				return
			}
			held = append(held, c)
		}
	}()
	got := run(t, `use p9;
main(args) {
    for (local v in [nil, "9P2000", "9P2000.L"]) {
        local o = {"timeout": 0.5}, t0 = clock();
        if (v != nil) o["version"] = v;
        try { p9::dial("tcp://" + args[0], o); } catch (e) { println(e->kind, replace(e->message, args[0], "ADDR"), clock() - t0 < 3); }
    }
    local t = new p9::Tree();
    t->file("/a/b", "x");
    t->mount("/m", p9::disk("."));
    try { t->remove("/m/x"); } catch (e) { println(e->message); }
    local s = p9::serve(t, "tcp://127.0.0.1:0");
    for (local v in ["9P2000", "9P2000.L"]) {
        try { p9::dial(s->endpoint(), {"aname": "/nope", "version": v}); } catch (e) { println(e->kind, find(e->message, "the server refuses the attach to \"/nope\"") > 0); }
        try { p9::dial(s->endpoint(), {"aname": "/a/b", "version": v}); } catch (e) { println(e->kind, slice(e->message, find(e->message, "the server"))); }
        local c = p9::dial(s->endpoint(), {"version": v});
        try { c->rename("/a/b", "/"); } catch (e) { println(e->kind, e->message); }
        println(c->exists("/a/b"), c->exists("/a/nope"), c->exists("/nope/x"), c->exists("/a/b/x"), c->exists("/m/p9.go/x"));
        try { c->read("/m/nope/deeper"); } catch (e) { println(e->kind); }
        try { c->mkdir("/"); } catch (e) { println(e->kind, e->message); }
        local s2 = p9::serve(t, "tcp://127.0.0.1:0");
        local c2 = p9::dial(s2->endpoint(), {"version": v});
        s2->close();
        try { c2->exists("/a/b"); } catch (e) { println(e->kind, find(e->message, "the connection is lost") > 0); }
        println(find(str(c2), "closed") > 0);
        c->close();
    }
    s->close();
}`, ln.Addr().String())
	want := `NetError p9::dial: tcp://ADDR: no 9P answer within 0.5 seconds true
NetError p9::dial: tcp://ADDR: no 9P answer within 0.5 seconds true
NetError p9::dial: tcp://ADDR: no 9P answer within 0.5 seconds true
p9::Tree::remove: /m is a mounted disk directory: the tree does not reach into it
IOError true
IOError the server refuses the attach to "/a/b": not a directory
IOError p9::Client::rename: /: the top directory cannot be written, made, renamed or removed
true false false false false
IOError
IOError p9::Client::mkdir: /: the top directory cannot be written, made, renamed or removed
NetError true
true
IOError true
IOError the server refuses the attach to "/a/b": not a directory
IOError p9::Client::rename: /: the top directory cannot be written, made, renamed or removed
true false false false false
IOError
IOError p9::Client::mkdir: /: the top directory cannot be written, made, renamed or removed
NetError true
true
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// closed() tells whether a client or a server is closed; a client is
// also closed when its server went away.
func TestClosed(t *testing.T) {
	got := run(t, `use p9;
main() {
    local s = p9::serve(new p9::Tree(), "tcp://127.0.0.1:0");
    local c = p9::dial(s->endpoint()), d = p9::dial(s->endpoint());
    println(s->closed(), c->closed());
    c->close();
    println(c->closed());
    s->close();
    println(s->closed());
    try { d->readdir("/"); } catch (e) { println(e->kind); }
    println(d->closed());
}`)
	if want := "false false\ntrue\ntrue\nNetError\ntrue\n"; got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
