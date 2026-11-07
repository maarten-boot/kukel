# 18. Networking

[The booklet](README.md) · previous: [17. Events and workers](17-events.md) · next: [19. The terminal](19-terminal.md)

Three modules connect a program to other programs, on the same computer
or across the world:

| Module | For |
|---|---|
| `net` | TCP connections and UDP packets: to speak any protocol (HTTP, SMTP, a game's, a device's) with any program |
| `zmq` | ZeroMQ messages: whole messages instead of a stream of bytes, with patterns such as request-reply and publish-subscribe |
| `p9` | the 9P file protocol: serve a tree of files, or read and write another program's |

Each listens or connects on an *address*: `"tcp://host:port"`, and for
some `"unix:///path"` (a socket file, between programs on one computer),
`"udp://host:port"` or `"ipc://..."`. Port 0 means "any free port", which
the examples here use, so that they never clash with another program.

## TCP: `net`

A TCP connection carries bytes both ways, in order, until one side closes
it. A server *listens* on an address and *accepts* connections; a client
*connects*:

```
use net;
use worker;

// Answers each line with its length, until the client is done.
server() {
    with (local listener = net::listen("tcp://127.0.0.1:0")) {
        worker::send(listener->endpoint());   // tell main where to connect

        with (local conn = listener->accept()) {
            for (local line in conn) {
                conn->println(
                    len(line),
                    line,
                );
            }
        }
    }
}

main() {
    local w = worker::start(server);

    with (local c = net::connect(
        w->receive(),
        5,
    )) {
        c->timeout(5);

        for (local word in ["hello", "networked", "world"]) {
            c->println(word);
            println(c->readline());
        }
    }
    w->wait();
}
```

```
$ kukel run lengths.kkl
5 hello
9 networked
5 world
```

- `net::listen(address)` gives a `net::Listener`; `l->accept()` waits for
  the next client and gives a `net::Conn`. `l->endpoint()` is the address
  it listens on, with the port that was picked.
- `net::connect(address, timeout)` connects, waiting at most `timeout`
  seconds.
- A connection reads and writes like a file (chapter 13): `readline()`,
  `for (local line in conn)`, `read(n)`, `write(s)`, `println(...)`.
- `c->timeout(seconds)` makes every later read or write that waits
  longer a `NetError`, so a silent server cannot hang the program.
- Here the server runs in a worker (chapter 17), so that one program can
  show both sides; usually they are separate programs.

A server that talks to many clients at once uses the event loop:
`l->onconnect(f)` calls `f(conn)` for each new connection, and
`conn->ondata(g)` calls `g(data)` as data arrives, and `g(nil)` at the
end:

```
use net;
use events;

main() {
    local server = net::listen("tcp://127.0.0.1:0"), served = 0;
    server->onconnect(fn(conn) {
        conn->ondata(fn(data) {
            if (data != nil) {
                conn->write(upper(data));
                return;
            }
            conn->close();

            if (++served == 2) { server->close(); }
        });
    });

    local a = net::connect(server->endpoint()), b = net::connect(server->endpoint());
    a->write("first client");
    b->write("second client");
    a->closewrite();                         // "I am done sending"
    b->closewrite();
    events::run();                           // until the server is closed
    println(
        a->readall(),
        "/",
        b->readall(),
    );
}
```

```
$ kukel run echo.kkl
FIRST CLIENT / SECOND CLIENT
```

## UDP: `net` too

UDP sends single *packets*, without a connection. A packet may get lost
or come twice, and they may come in another order: the program must
allow that. In return UDP is quick and simple, and good for things like
time, names (DNS) and games.

```
use net;

main() {
    with (local server = net::udp("udp://127.0.0.1:0"), client = net::udp("udp://127.0.0.1:0")) {
        client->sendto(
            "what time is it?",
            server->endpoint(),
        );
        local p = server->receive(5);         // [data, from], or nil after 5 seconds
        println(
            "server got:",
            p[0],
        );
        server->sendto(
            "tea time",
            p[1],
        );     // answer where it came from
        println(
            "client got:",
            client->receive(5)[0],
        );
    }
}
```

```
$ kukel run udp.kkl
server got: what time is it?
client got: tea time
```

`net::lookup(host)` gives the addresses of a host name. There is no
encryption (TLS) yet: use `net` on a network you trust.

## Messages: `zmq`

ZeroMQ moves whole *messages*, and comes with patterns for how programs
talk. Request-reply: a `REQ` socket asks, a `REP` socket answers, in
turn:

```
use zmq;
use worker;

calculator() {
    with (local rep = new zmq::Socket("REP")) {
        rep->bind("tcp://127.0.0.1:0");
        worker::send(rep->endpoint());

        for (local q = rep->receive(); q != "stop"; q = rep->receive()) {
            local parts = split(
                q,
                " ",
            );
            rep->send(str(int(parts[0]) + int(parts[1])));
        }
        rep->send("stopped");
    }
}

main() {
    local w = worker::start(calculator);

    with (local req = new zmq::Socket("REQ")) {
        req->connect(w->receive());

        for (local q in ["1 2", "40 2", "stop"]) {
            req->send(q);
            println(
                q,
                "->",
                req->receive(5),
            );
        }
    }
    w->wait();
}
```

```
$ kukel run calculator.kkl
1 2 -> 3
40 2 -> 42
stop -> stopped
```

The other patterns: `PUB` and `SUB` (one sends, every subscriber gets a
copy), `PUSH` and `PULL` (work handed out to whoever is free), `DEALER`
and `ROUTER` (many clients without the strict turns of `REQ`), and `PAIR`.
ZeroMQ libraries exist for nearly every language, so a Kukel program can
talk to a Python or C program this way; [examples/chat.kkl](../../examples/chat.kkl)
is a small chat.

## Files over the network: `p9`

9P is the file protocol of Plan 9, which Linux can mount (`mount -t 9p`).
A Kukel program can serve a tree of files that it makes up, whose content
it computes when someone reads them:

```
use p9;

main() {
    local t = new p9::Tree(), reads = 0, notes = [];
    t->file(
        "/motd",
        "hello\n",
    );
    t->file(
        "/stats/reads",
        fn() {
            reads++;
            return str(reads);
        },
    );
    t->file(
        "/notes",
        "",
        fn(data) { push(
            notes,
            data,
        ); },
    );
    local s = p9::serve(
        t,
        "tcp://127.0.0.1:0",
    );

    with (local c = p9::dial(s->endpoint())) {   // could be another program, or Linux itself
        print(c->read("/motd"));
        println(
            c->read("/stats/reads"),
            c->read("/stats/reads"),
        );
        c->write(
            "/notes",
            "remember the milk",
        );

        for (local e in c->readdir("/")) {
            println(
                e["name"],
                e["dir"] ? "(directory)" : e["size"],
            );
        }
    }
    println(notes);
    s->close();
}
```

```
$ kukel run files.kkl
hello
1 2
motd 6
notes 0
stats (directory)
["remember the milk"]
```

`p9::disk(dir)` serves a directory of the disk instead, read-only unless
asked otherwise. The [reference](../reference.md#p9) has the rest.

## When the network fails

A server that is not there, a connection that breaks, an address in use:
these are a `NetError`.

```
use net;

main() {
    try {
        net::connect(
            "tcp://127.0.0.1:1",
            2,
        );
    } catch (e) {
        println(
            e->kind,
            e->message,
        );
    }
}
```

```
$ kukel run refused.kkl
NetError net::connect: tcp://127.0.0.1:1: connection refused
```

[The booklet](README.md) · previous: [17. Events and workers](17-events.md) · next: [19. The terminal](19-terminal.md)
