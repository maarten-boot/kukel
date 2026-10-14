//go:build !nozmq

package zmqmod_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/builtins"
	"github.com/maarten-boot/kukel/internal/compiler"
	_ "github.com/maarten-boot/kukel/internal/modules/timemod"
	_ "github.com/maarten-boot/kukel/internal/modules/workermod"
	_ "github.com/maarten-boot/kukel/internal/modules/zmqmod"
	"github.com/maarten-boot/kukel/internal/parser"
	"github.com/maarten-boot/kukel/internal/vm"
)

func run(t *testing.T, src string) string {
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
	if _, err := m.Run(nil); err != nil {
		t.Fatalf("%v\noutput: %s", err, out.String())
	}
	return out.String()
}

// Request and reply, with a worker as the server, over TCP (port 0: a
// free port) and with messages of several parts.
func TestReqRep(t *testing.T) {
	got := run(t, `use zmq; use worker;
server() {
    local rep = new zmq::Socket("REP");
    rep->bind("tcp://127.0.0.1:0");
    worker::send(rep->endpoint());
    for (local m = rep->receive(); m != "quit"; m = rep->receive())
        rep->send(typeof(m) == "vector" ? [str(len(m)), m[0]] : upper(m));
    rep->send("bye");
    rep->close();                          // the reply still leaves (linger)
}
main() {
    local w = worker::start(server), req = new zmq::Socket("REQ");
    req->connect(w->receive(5));
    req->send("hi");      println(req->receive(5));
    req->send(["a", "b"]); println(req->receive(5));
    req->send("quit");    println(req->receive(5));
    req->close();
    w->wait();
}`)
	if got != "HI\n[\"2\", \"a\"]\nbye\n" {
		t.Errorf("got %q", got)
	}
}

// Publish and subscribe: only the subscribed prefixes arrive.
func TestPubSub(t *testing.T) {
	got := run(t, `use zmq; use time;
main() {
    local pub = new zmq::Socket("PUB"), sub = new zmq::Socket("SUB");
    pub->bind("inproc://news");
    sub->connect("inproc://news");
    sub->subscribe("sport.");
    sub->subscribe("weather.");
    time::sleep(0.1);                      // let the subscriptions arrive
    for (local m in ["sport.1", "politics.1", "weather.1", "sport.2"]) pub->send(m);
    for (local i = 0; i < 3; i++) println(sub->receive(5));
    println(sub->receive(0.1));            // politics.1 never comes
    sub->unsubscribe("sport.");
    pub->close(); sub->close();
}`)
	if got != "sport.1\nweather.1\nsport.2\nnil\n" {
		t.Errorf("got %q", got)
	}
}

// Push and pull through the event loop; closing the socket in the handler
// ends events::run().
func TestPushPullEvents(t *testing.T) {
	got := run(t, `use zmq; use events;
main() {
    local pull = new zmq::Socket("PULL"), push = new zmq::Socket("PUSH"), n = 0;
    pull->bind("inproc://jobs");
    push->connect("inproc://jobs");
    pull->onmessage(fn(m) { println("job", m); if (++n == 3) pull->close(); });
    for (local i = 1; i <= 3; i++) push->send(str(i));
    events::run();
    push->close();
    println(pull);
}`)
	if got != "job 1\njob 2\njob 3\nzmq::Socket(PULL, closed)\n" {
		t.Errorf("got %q", got)
	}
}

// A receive that timed out leaves its read running: the next receive gets
// that message, none is lost.
func TestTimeoutKeepsMessage(t *testing.T) {
	got := run(t, `use zmq;
main() {
    local pull = new zmq::Socket("PULL"), push = new zmq::Socket("PUSH");
    pull->bind("inproc://t");
    push->connect("inproc://t");
    println(pull->receive(0.05));
    push->send("late");
    println(pull->receive(5));
    push->close(); pull->close();
}`)
	if got != "nil\nlate\n" {
		t.Errorf("got %q", got)
	}
}

func TestErrors(t *testing.T) {
	got := run(t, `use zmq;
main() {
    local s = new zmq::Socket("PULL");
    s->close();
    s->close();                            // no effect
    local bad = [fn() { new zmq::Socket("STREAM"); },
                 fn() { s->receive(); },
                 fn() { new zmq::Socket("PUSH")->subscribe(""); },
                 fn() { new zmq::Socket("PUSH")->bind("localhost:5555"); },
                 fn() { new zmq::Socket("PUSH")->send(5); },
                 fn() { new zmq::Socket("PUSH")->send([]); },
                 fn() { new zmq::Socket("PULL")->receive(-1); },
                 fn() { new zmq::Socket("PULL")->onmessage(5); },
                 fn() { new zmq::Socket("PUSH")->receive(); },
                 fn() { new zmq::Socket("PUB")->onmessage(fn(m) {}); },
                 fn() { zmq::poll([new zmq::Socket("PULL"), new zmq::Socket("PUSH")]); }];
    for (local f in bad) try { f(); } catch (e) { println(e->kind + ":", e->message); }
}`)
	want := `ValueError: zmq::Socket::Socket: unknown socket type "STREAM" (one of DEALER, PAIR, PUB, PULL, PUSH, REP, REQ, ROUTER, SUB)
ValueError: zmq::Socket::receive: the socket is closed
ValueError: zmq::Socket::subscribe: only a SUB socket subscribes, not a PUSH socket
ValueError: zmq::Socket::bind: "localhost:5555" is not an endpoint such as "tcp://localhost:5555"
TypeError: zmq::Socket::send: argument 1 must be a string or a vector of strings, not int
ValueError: zmq::Socket::send: a message needs at least one part
ValueError: zmq::Socket::receive: -1.0 seconds is not a usable timeout
TypeError: zmq::Socket::onmessage: argument 1 must be a function, not int
ValueError: zmq::Socket::receive: a PUSH socket cannot receive messages
ValueError: zmq::Socket::onmessage: a PUB socket cannot receive messages
ValueError: zmq::poll: socket 2 is a PUSH socket, which cannot receive messages
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A ROUTER gets the sender's identity as the first part (and the empty
// separator part of a REQ) and replies by putting it first again.
func TestRouterDealer(t *testing.T) {
	got := run(t, `use zmq; use time;
main() {
    local router = new zmq::Socket("ROUTER");
    router->bind("inproc://r");
    local dealer = new zmq::Socket("DEALER", "dealer-1"), req = new zmq::Socket("REQ", "req-1");
    dealer->connect("inproc://r");
    req->connect("inproc://r");
    time::sleep(0.05);
    dealer->send("from dealer");
    local m = router->receive(5);
    println(m);
    router->send([m[0], "reply 1"]);
    println(dealer->receive(5));
    req->send("from req");
    m = router->receive(5);
    println(m);
    router->send([m[0], "", "reply 2"]);
    println(req->receive(5));
    for (local s in [router, dealer, req]) s->close();
}`)
	want := "[\"dealer-1\", \"from dealer\"]\nreply 1\n[\"req-1\", \"\", \"from req\"]\nreply 2\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPair(t *testing.T) {
	got := run(t, `use zmq;
main() {
    local a = new zmq::Socket("PAIR"), b = new zmq::Socket("PAIR");
    a->bind("inproc://pair");
    b->connect("inproc://pair");
    b->send("ping"); println(a->receive(5));
    a->send("pong"); println(b->receive(5));
    a->close(); b->close();
}`)
	if got != "ping\npong\n" {
		t.Errorf("got %q", got)
	}
}

// poll gives the socket that has a message; the first one wins when
// several have one; a timeout gives nil; no message is lost.
func TestPoll(t *testing.T) {
	got := run(t, `use zmq; use events; use time;
main() {
    local a = new zmq::Socket("PULL"), b = new zmq::Socket("PULL");
    local pa = new zmq::Socket("PUSH"), pb = new zmq::Socket("PUSH");
    a->bind("inproc://a"); b->bind("inproc://b");
    pa->connect("inproc://a"); pb->connect("inproc://b");
    println(zmq::poll([a, b], 0.05));                 // nothing yet
    pb->send("for b");
    local s = zmq::poll([a, b], 5);
    println(s == b, s->receive(0));                   // the message is already there
    pa->send("a1"); pb->send("b1");
    time::sleep(0.05);                                // both arrive
    local first = zmq::poll([a, b], 5);
    println(first == a);                              // a comes first in the vector
    println(a->receive(5), b->receive(5));            // nothing lost on either
    pa->send("a2");
    zmq::poll([a], 5);                                // a's message waits in a ...
    a->onmessage(fn(m) { println("handler", m); a->close(); });
    events::run();                                    // ... and onmessage gets it
    local errs = [fn() { zmq::poll([]); }, fn() { zmq::poll([1]); }, fn() { zmq::poll([a]); },
                  fn() { zmq::poll([b], -1); }];
    for (local f in errs) try { f(); } catch (e) { println(e->kind + ":", e->message); }
    for (local x in [b, pa, pb]) x->close();
}`)
	want := `nil
true for b
true
a1 b1
handler a2
ValueError: zmq::poll: no sockets to wait for
TypeError: zmq::poll: element 1 of argument 1 must be a zmq::Socket, not int
ValueError: zmq::poll: socket 1 is closed
ValueError: zmq::poll: -1.0 seconds is not a usable timeout
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
