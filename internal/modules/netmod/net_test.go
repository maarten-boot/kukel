package netmod_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/maarten-boot/kukel"
)

// run runs main of src with args and gives what it printed; an error
// fails the test.
func run(t *testing.T, src string, args ...string) string {
	t.Helper()
	p, err := kukel.CompileSource("t.kkl", src, kukel.CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	in, err := kukel.New(p, kukel.Options{Stdout: &out})
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	if _, err := in.Run(args...); err != nil {
		t.Fatalf("%v\noutput: %s", err, out.String())
	}
	return out.String()
}

func TestTCP(t *testing.T) {
	got := run(t, `use net;
main() {
    local l = net::listen("tcp://127.0.0.1:0");
    local c = net::connect(l->endpoint(), 5);
    local s = l->accept(5);
    println(c->peer() == l->endpoint(), s->peer() == c->endpoint(), str(l) == "net::Listener(" + l->endpoint() + ")");
    println(c->write("one\r\ntwo\n", "", "three") == c);
    c->closewrite();
    println(s->readline(), s->readline(), s->read(2), s->read(), s->read(), s->readline());
    s->print("a", 1); s->println(nil, [2]);
    s->close();
    println(c->read(0), c->readall(), c->read(), c->readline(), c->readall() == "");
    c->close(); c->close(); l->close();
    println(c->closed(), l->closed(), str(c) == "net::Conn(" + l->endpoint() + ", closed)");
    try { c->read(); } catch (e) { println(e->kind, e->message); }
    try { l->accept(); } catch (e) { println(e->kind, e->message); }
}`)
	want := "true true true\n" +
		"true\n" +
		"one two th ree nil nil\n" +
		" a 1nil [2]\n nil nil true\n" +
		"true true true\n" +
		"ValueError net::Conn::read: the connection is closed\n" +
		"ValueError net::Listener::accept: the listener is closed\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// Lines and iteration: a line split over reads, a last line without \n,
// a line longer than the limit (which read can still take).
func TestLines(t *testing.T) {
	got := run(t, `use net;
main() {
    local l = net::listen("tcp://127.0.0.1:0"), c = net::connect(l->endpoint()), s = l->accept();
    c->write("a\nb");
    for (local i = 0; i < 3; i++) c->write("c");
    c->write("\nlast");
    c->closewrite();
    for (local i, line in s) println(i, line);
    s->close(); c->close();

    c = net::connect(l->endpoint()); s = l->accept();
    local big = "x";
    for (local i = 0; i < 20; i++) big = big + big;
    c->write(big + "x");             // 1 MiB and a byte
    try { s->readline(); } catch (e) { println(e->kind, e->message); }
    println(len(s->read(10)));
    c->write("\n");
    println(len(s->readline()));
    c->close(); s->close(); l->close();
}`)
	want := "0 a\n1 bccc\n2 last\n" +
		"NetError net::Conn::readline: a line longer than 1048576 bytes\n" +
		"10\n1048567\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// A call that waits too long throws, and what came meanwhile stays; accept
// gives nil after its timeout; timeout(nil) lifts the limit.
func TestTimeouts(t *testing.T) {
	got := run(t, `use net;
main() {
    local l = net::listen("tcp://127.0.0.1:0");
    println(l->accept(0.05));
    local c = net::connect(l->endpoint())->timeout(0.05), s = l->accept(1);
    try { c->read(); } catch (e) { println(e->kind, e->message); }
    s->write("pa");
    try { c->readline(); } catch (e) { println(e->message); }
    try { c->readall(); } catch (e) { println(e->message); }
    s->write("rt\n");
    println(c->readline());
    c->timeout(nil);
    s->write("more");
    s->close();
    println(c->readall());
    try { c->timeout(-1); } catch (e) { println(e->kind, e->message); }
    c->close(); l->close();
}`)
	want := "nil\n" +
		"NetError net::Conn::read: no data within 0.05 seconds\n" +
		"net::Conn::readline: no data within 0.05 seconds\n" +
		"net::Conn::readall: no data within 0.05 seconds\n" +
		"part\n" +
		"more\n" +
		"ValueError net::Conn::timeout: -1 seconds is not a usable timeout\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// An echo server on the event loop: onconnect and ondata, f(nil) at the
// end, run() ending when everything is closed.
func TestEvents(t *testing.T) {
	got := run(t, `use net; use events;
main() {
    local srv = net::listen("tcp://127.0.0.1:0"), served = 0;
    srv->onconnect(fn(conn) {
        local got = "";
        conn->ondata(fn(d) {
            if (d != nil) { got = got + d; return; }
            conn->write(upper(got));
            conn->close();
            if (++served == 2) srv->close();
        });
    });
    local a = net::connect(srv->endpoint()), b = net::connect(srv->endpoint());
    a->write("one"); a->closewrite();
    b->write("two"); b->closewrite();
    local answers = [];
    a->ondata(fn(d) { if (d != nil) push(answers, d); });
    events::run();
    println(answers, b->readall(), srv->closed());
    try { a->read(); } catch (e) { println(e->kind, e->message); }
    try { srv->onconnect(println); } catch (e) { println(e->kind, e->message); }
    try { b->ondata(1); } catch (e) { println(e->kind, e->message); }
    a->close(); b->close();
}`)
	want := `["ONE"] TWO true
ValueError net::Conn::read: the data goes to the ondata handler
ValueError net::Listener::onconnect: the listener is closed
TypeError net::Conn::ondata: argument 1 must be a function, not int
`
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestUDP(t *testing.T) {
	got := run(t, `use net; use events;
main() {
    local u = net::udp("udp://127.0.0.1:0"), v = net::udp("udp://127.0.0.1:0");
    u->sendto("ping", v->endpoint());
    u->sendto("", v->endpoint());
    local p = v->receive(5);
    println(p[0], p[1] == u->endpoint(), v->receive(5)[0] == "", v->receive(0.05));
    v->onmessage(fn(data, from) {
        println("got", data, from == u->endpoint());
        v->close();
    });
    u->sendto("pong", v->endpoint());
    events::run();
    println(v, str(u) == "net::UDP(" + u->endpoint() + ")");
    try { v->receive(); } catch (e) { println(e->kind, e->message); }
    try { u->sendto("x", "tcp://127.0.0.1:1"); } catch (e) { println(e->kind, e->message); }
    try { u->sendto(1, v->endpoint()); } catch (e) { println(e->kind, e->message); }
    u->close();
}`)
	want := "ping true true nil\n" +
		"got pong true\n" +
		"net::UDP(udp://127.0.0.1:" // the port varies
	if !strings.HasPrefix(got, want) || !strings.HasSuffix(got, ", closed) true\n"+
		"ValueError net::UDP::receive: the socket is closed\n"+
		"ValueError net::UDP::sendto: \"tcp://127.0.0.1:1\" is not an address such as \"udp://host:port\"\n"+
		"TypeError net::UDP::sendto: argument 1 must be a string, not int\n") {
		t.Errorf("got\n%s", got)
	}
}

func TestUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix sockets")
	}
	dir, err := os.MkdirTemp("", "kukel-net") // short: socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	got := run(t, `use net;
main(args) {
    local l = net::listen("unix://" + args[0]);
    local c = net::connect(l->endpoint()), s = l->accept();
    c->println("over a socket file");
    println(s->readline(), l->endpoint() == "unix://" + args[0], s->endpoint() == l->endpoint());
    c->close(); s->close(); l->close();
}`, filepath.Join(dir, "s"))
	if got != "over a socket file true true\n" {
		t.Errorf("got %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "s")); !os.IsNotExist(err) {
		t.Errorf("the socket file stays after close: %v", err)
	}
}

func TestErrors(t *testing.T) {
	got := run(t, `use net;
try1(f) { try { f(); } catch (e) { println(e->kind, e->message); } }
main() {
    try1(fn() { net::connect("tcp://127.0.0.1:1"); });
    try1(fn() { net::connect("localhost:80"); });
    try1(fn() { net::connect("tcp://localhost"); });
    try1(fn() { net::connect("tcp://localhost:70000"); });
    try1(fn() { net::connect("unix://"); });
    try1(fn() { net::connect("udp://localhost:1"); });
    try1(fn() { net::connect("tcp://127.0.0.1:1", -1); });
    try1(fn() { net::listen("tcp://*:x"); });
    try1(fn() { net::lookup(""); });
    try1(fn() { net::lookup("no-such-host.invalid"); });
    local l = net::listen("tcp://127.0.0.1:0");
    try1(fn() { net::listen(l->endpoint()); });
    local c = net::connect(l->endpoint());
    try1(fn() { c->read(-1); });
    try1(fn() { c->write(1); });
    l->close(); c->close();
}`)
	want := `NetError net::connect: tcp://127.0.0.1:1: connection refused
ValueError net::connect: "localhost:80" is not an address such as "tcp://host:port" or "unix:///path"
ValueError net::connect: "tcp://localhost" is not an address such as "tcp://host:port" (a host and a port)
ValueError net::connect: "tcp://localhost:70000": the port must be a number from 0 to 65535
ValueError net::connect: "unix://" has no socket path
ValueError net::connect: "udp://localhost:1" is not an address such as "tcp://host:port" or "unix:///path"
ValueError net::connect: -1 seconds is not a usable timeout
ValueError net::listen: "tcp://*:x": the port must be a number from 0 to 65535
ValueError net::lookup: no host name
NetError net::lookup: host no-such-host.invalid not found
NetError net::listen: tcp://127.0.0.1:PORT: address already in use
ValueError net::Conn::read: a negative number of bytes (-1)
TypeError net::Conn::write: argument 1 must be a string, not int
`
	got = portRE(got)
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// portRE replaces the port of "tcp://127.0.0.1:<port>: address already in
// use" with PORT.
func portRE(s string) string {
	const pre, post = "tcp://127.0.0.1:", ": address already in use"
	i := strings.Index(s, post)
	if i < 0 {
		return s
	}
	j := strings.LastIndex(s[:i], pre)
	if j < 0 {
		return s
	}
	return s[:j+len(pre)] + "PORT" + s[i:]
}

// What the program leaves open is closed when the interpreter is closed.
func TestEndCloses(t *testing.T) {
	p, err := kukel.CompileSource("t.kkl", `use net;
global l;
main() { l = net::listen("tcp://127.0.0.1:0"); println(l->endpoint()); }`, kukel.CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	in, err := kukel.New(p, kukel.Options{Stdout: &out})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := in.Run(); err != nil {
		t.Fatal(err)
	}
	l, err := in.Global("l")
	if err != nil {
		t.Fatal(err)
	}
	in.Close()
	if !strings.HasSuffix(l.String(), ", closed)") {
		t.Errorf("after Close: %v", l)
	}
}

// Calls that wait are cancelled as other waits are.
func TestCancelled(t *testing.T) {
	p, err := kukel.CompileSource("t.kkl", `use net;
global l, c, s, u;
setup() { l = net::listen("tcp://127.0.0.1:0"); c = net::connect(l->endpoint()); s = l->accept(); u = net::udp("udp://127.0.0.1:0"); }
waitline() { return c->readline(); }
waitall() { return c->readall(); }
waitconn() { return l->accept(); }
waitpacket() { return u->receive(); }
main() {}`, kukel.CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	in, err := kukel.New(p, kukel.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	if _, err := in.Call("setup"); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"waitline", "waitall", "waitconn", "waitpacket", "waitline"} {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		start := time.Now()
		_, err := in.CallContext(ctx, f)
		cancel()
		if e, ok := err.(*kukel.Error); !ok || e.Kind != "CancelledError" || time.Since(start) > 5*time.Second {
			t.Errorf("%s: %v after %v", f, err, time.Since(start))
		}
	}
}
