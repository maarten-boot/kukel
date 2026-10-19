# Kukel: ideas for the future

Ideas for after v0.1.0. Parts marked *Done* are built (as of v0.18, see the
language reference and docs/embedding.md); for the rest, the Kukel code below
shows how things *could* look and is not meant to run. Each idea lists open
questions (**F-n**) to settle before it is built, in the same way the Q-list
in [TODO.md](../TODO.md) settled the first version.

Contents:

1. [Foundations several ideas need](#1-foundations-several-ideas-need)
2. [Multiple interpreters](#2-multiple-interpreters)
3. [Events and a Tk GUI](#3-events-and-a-tk-gui)
4. [ZeroMQ messaging](#4-zeromq-messaging)
5. [SQL and an ORM](#5-sql-and-an-orm)
6. [Files: a module fs and 9P](#6-files-a-module-fs-and-9p)
7. [Smaller items](#7-smaller-items)
8. [Suggested order](#8-suggested-order)

---

## 1. Foundations several ideas need

The four ideas share needs that Kukel does not have today. Building these
first keeps each idea small.

### 1.1 Modules (name spaces)

Today all built-ins share one flat name space with the program (55 names
plus the class `Error`, reserved at the top level; locals and members may
reuse them). GUI, messaging and database libraries would add hundreds.
They need name spaces, loaded only when a program asks for them:

```
use gui;                 // or: import "gui";
use db;

main() {
    local w = new gui::Window("Hello");
    local conn = db::open("sqlite:app.db");
}
```

- `use name;` makes a library's functions and classes available as
  `name::f(...)` and `name::Class`. The `::` syntax already exists (static
  members), so the grammar change is small: `use` declarations, and
  `Identifier :: Identifier` meaning a module member when the left side is
  a module.
- The same mechanism could load other Kukel source files
  (`use "lib/util.kkl";`), replacing "list every file on the command line".
  *(Built as `use util;`, which finds `util.kkl`.)*
- Libraries written in Go register themselves under a name; a program that
  does not `use` them pays nothing.
- Where modules come from (Kukel files, Go, C libraries) is in 1.6.

*Done for Kukel source files (TODO.md Q35): `use name;` loads `name.kkl`
from the using file's directory or `KUKEL_PATH`; see the language
reference. Modules written in Go are done too (1.2, 1.6), and modules
compiled on their own into `.kkc` files (TODO.md Q49).*

**F-1** Syntax: `use gui;` or `import gui;`? Also for `.kkl` files?
*Answered: `use name;`, also for `.kkl` files.*
**F-2** Do the current built-ins move into modules (`str::split`,
`math::sqrt`), or stay global as a small core?

### 1.2 Classes and objects implemented in Go ("native classes")

A window, a socket or a database connection is a Go value with methods.
Kukel needs a way for Go code to define classes whose objects carry Go state:

```
local sock = new zmq::Socket("REQ");    // a native class
sock->connect("tcp://localhost:5555");  // native methods (a REP socket must be bound there)
sock->close();
```

- A native class has Go methods (like built-ins, but with `this`) and an
  opaque Go value inside each object.
- Kukel classes should be able to derive from native ones
  (`class MyWindow : gui::Window { ... }`), so that programs override event
  methods such as `onClose()`.
- Resources need explicit closing (`close()`); a `finally` block or a
  future `with` statement makes that safe. Garbage collection could close
  forgotten ones as a safety net (Go finalizers), but not as the plan.

*Done (TODO.md Q36): `internal/native`; every module written in Go uses
it. Kukel classes can derive from native classes that have a
constructor (TODO.md Q51), and Go programs that embed Kukel can write
native classes in host modules.*

**F-3** Is deriving from native classes needed from the start (GUI style
"override `onClick`"), or are callbacks (`button->onClick(fn() {...})`) enough?
*Answered: not from the start; callbacks first, deriving when the GUI needs it.
Deriving is done (Q51): the derived constructor calls the native one; Go code
calls an overridable step through the object (`this.Call` in the public API).*
**F-4** A `with (local f = open(...)) { ... }` statement that closes
automatically, or keep `try`/`finally`?
*Answered and done (TODO.md Q45): the `with` statement.*

### 1.3 An event loop

GUI events, incoming messages and timers all mean "call this Kukel function
when something happens". The VM runs one Kukel call at a time, so events must
be queued and dispatched from one loop:

```
use events;
use zmq;

main() {
    local sock = new zmq::Socket("PULL");
    sock->bind("tcp://*:5557");
    events::every(1, fn() { println("tick"); });   // seconds
    sock->onmessage(fn(msg) { println("got", msg); });
    events::run();          // dispatches until events::stop()
}
```

- One loop per interpreter; Tk, ZeroMQ and timers feed it from their own
  goroutines; handlers run on the interpreter's goroutine, one at a time
  (no locking inside Kukel programs).
- An exception in a handler: report it and continue, or stop the loop
  (**F-5**).
- Later, if wanted: `await`-style coroutines instead of callbacks. That is a
  much larger change to the VM (saving and resuming frames) and not needed
  for the first versions.

*Done (TODO.md Q37): the native module `events`, with timers, `post`,
`stop`, `onerror`; Go modules feed it with `eventsmod.For(env).Post` and
`Hold`.*

**F-5** What happens to an uncaught exception in an event handler?
*Answered: it leaves `events::run()`, unless `events::onerror(f)` takes it.*
**F-6** Callbacks only, or coroutines (`await`) later?
*Answered for now: callbacks only.*

### 1.4 Values that travel: serialization

Messages between interpreters, ZeroMQ payloads and database rows all need
Kukel values turned into bytes and back:

- `json::encode(v)` / `json::decode(s)` for nil, booleans, numbers, strings,
  vectors and maps (Go's `encoding/json`, much faster than the Kukel parser
  in `tests/run/json.kkl`);
- maybe a compact binary form for messages between interpreters, which can
  also keep ints and floats apart exactly.
- Objects, functions and classes cannot travel; encoding them is an error
  (or objects become maps of their data members, **F-7**).

*`json::encode`/`json::decode` are done (TODO.md Q36); a binary form is not.*

**F-7** Can objects be encoded (as maps of their data members)?
*Answered for now: no, encoding an object is a `TypeError`.*

### 1.5 A date and time type

Databases and GUIs need dates. Today only `time()` (a float) exists. A
`time::Time` native class (parse, format, arithmetic, time zones) would
cover SQL `TIMESTAMP` columns and timers.

*Done (TODO.md Q36): `time::Time`, with strftime-style formats and IANA time
zones.*

### 1.6 Where modules come from

A module behind `use` can come from three places. A program does not see
the difference: each gives names under `name::`.

**Kukel modules (`.kkl` source or compiled `.kkc`).** This is entirely
Kukel's own compiler and VM; Go plays no part.

- `use util;` looks for `util.kkl` or `util.kkc` on a search path (the
  program's directory, then `KUKEL_PATH`), and loads each module once.
  *(Built: looked for next to the file that uses it, then on `KUKEL_PATH`;
  `util.kkc` counts when there is no `util.kkl` (TODO.md Q49).)*
- The compiler gives the module's top-level names the prefix `util::`.
  Names starting with `_` could stay private to the module (**F-19**).
- A `.kkc` module carries a table of the names it exports (functions,
  classes, constants and globals, with parameter counts), so a program can
  be compiled against it without its source. The loader checks the table
  against what the program uses, like the current check of built-ins.
  *(Built as a header: the module's declarations without bodies, which the
  compiler reads like source; the code is linked by name.)*
- Open points: cycles between modules (refuse them at first), and whether
  `kukel build` writes one `.kkc` per module or links them all into one
  file (**F-20**).

**Native modules written in Go, compiled into `kukel`.** The modules the
ideas in this document need (`gui`, `zmq`, `db`, `json`, `time`) are Go
packages that register their functions and native classes (1.2) under a
module name, the way the built-ins do today. Pure-Go libraries exist for
each, so no C compiler is needed:

| Module | Go library |
|---|---|
| `db` | `database/sql` with `modernc.org/sqlite` (no cgo), and the Postgres and MySQL drivers |
| `zmq` | `github.com/go-zeromq/zmq4` |
| `gui` | `modernc.org/tk9.0` |
| `json`, `time` | the standard library |

`kukel` stays one static binary that cross-compiles to Linux, macOS and
Windows. Build tags can leave out the large modules (`go build -tags
nogui`), and `kukel version` lists the modules a binary contains.

*Done for `json`, `strings`, `time` (TODO.md Q36), `events`, `worker`, `zmq`,
`db`, `uuid`, `re`, `fs` and `p9`, and `kukel version` lists them. Build tags
are done for the large ones (Q55, Q62): `nopostgres`, `nomysql`, `nosqlite`,
`nozmq` and `no9p`; a GUI module would get `nogui`.*

**Foreign libraries (`.so` on Linux, `.dll` on Windows, `.dylib` on
macOS).** For a C library that has no Go binding. Go offers four ways:

| Approach | How | Drawbacks |
|---|---|---|
| cgo | C libraries are linked into `kukel` when it is built | needs a C compiler; harder cross-compiling; the set of libraries is fixed per binary |
| Go's `plugin` package | loads a Go-built `.so` at run time | no Windows; the plugin must be built with exactly the same Go version and dependency versions; cannot be unloaded. Too fragile |
| `github.com/ebitengine/purego` | opens any C `.so`/`.dll` at run time and calls its functions, without cgo | Linux, macOS and Windows; structs and callbacks are limited; every signature must be declared |
| a separate process | the library runs in a helper program that talks to Kukel over a pipe or socket (like HashiCorp's go-plugin) | works everywhere and a crash cannot take the VM down, but every call is slow and values are copied |

The best fit is an `ffi` module on top of purego, in which the program
declares each function's signature:

```
use ffi;

main() {
    local libm = ffi::open("libm.so.6");               // or "msvcrt.dll"
    local cos = libm->func("cos", "double", ["double"]);
    println(cos(0.5));
    libm->close();
}
```

- Types: integers of each width, `double`, `float`, C strings (copied in
  and out), and opaque pointers as a native class. Structs and callbacks
  come later, if at all (**F-21**).
- A wrong signature crashes the process instead of throwing; `ffi` is for
  writing modules, not for everyday programs. A `--no-ffi` switch (or a
  build tag) can turn it off for untrusted code, together with the step
  limit.
- A library that is used a lot is better wrapped once as a Kukel module
  over `ffi`, or as a Go native module.

So the order is: Kukel modules and Go native modules first (they cover
every idea here), `ffi` later and only if a library needs it.

**F-19** Private names in modules: by a `_` prefix, an `export` list, or
everything public? *Answered: a `_` prefix.*
**F-20** `kukel build`: one `.kkc` per module, or one linked file?
*Answered: one file holding the program and its modules; a module can
also be compiled on its own (`kukel build -module`) and is then linked
into the program when the program is compiled (TODO.md Q49).*
**F-21** Is `ffi` wanted at all, and with structs and callbacks?

---

## 2. Multiple interpreters

**Goal**: several independent Kukel interpreters at once, in one process:
from Go programs that embed Kukel, and from Kukel itself (workers that run in
parallel and talk through messages).

### 2.1 Embedding Kukel in Go programs

A public Go package (today everything is in `internal/`):

```go
import "github.com/maarten-boot/kukel/kukel"

prog, err := kukel.Compile(files...)          // compile once
interp := kukel.New(prog, kukel.Options{       // as many as needed
    Stdout:    os.Stdout,
    StepLimit: 1_000_000,                      // sandboxing
    Modules:   []string{"json"},               // what the program may use
})
interp.Register("notify", func(args []kukel.Value) (kukel.Value, error) { ... })
result, err := interp.Call("main", kukel.String("hi"))
```

- Much of this is ready: a compiled program can already be shared by
  several VMs (milestone 12 moved the inline caches into the VM), and each
  VM has its own globals, output, input and random numbers.
- Needed: a stable public API (values, errors, calling Kukel functions,
  registering Go functions), and limits for untrusted code: steps (exists
  for tests), memory, and which modules (files, network) are allowed.

*Done (TODO.md Q39), with differences from the sketch above: the package is
the repository root (`import "github.com/maarten-boot/kukel"`), Go functions
come in host modules (`use app; app::notify(x)`), and the limits are a step
budget per call, cancellation by a Go context, and allow-lists of modules
and built-ins. Memory is not limited. Host modules can hold classes written
in Go too (Q51). See docs/embedding.md.*

### 2.2 Interpreters inside Kukel (workers)

```
use worker;

main() {
    local w = worker::startfile("crunch.kkl", "--fast"); // a new interpreter on its own goroutine
    w->send({"job": 1, "data": [1, 2, 3]});              // values are copied (serialized)
    println(w->receive());                               // waits for the answer
    w->stop();
}
```

```
// crunch.kkl
use worker;

main(args) {
    while (true) {
        local msg = worker::receive();        // from the parent

        if (msg == nil) break;                // the parent stopped us
        local total = 0;

        for (local x in msg["data"]) total += x;
        worker::send(total);
    }
}
```

- Interpreters share nothing; messages are copied (section 1.4), so there
  are no data races and no locks in Kukel programs. This is the actor model
  (as in Erlang, or Go's goroutines with channels).
- Real parallelism comes for free: each interpreter is a goroutine.
- With the event loop (section 1.3), `w->onmessage(fn(msg) {...})` instead
  of a blocking `receive()`.
- ZeroMQ's `inproc://` transport could carry these messages, so that a
  worker can later move to another process or machine unchanged.

*Done (TODO.md Q38): the native module `worker`, with `start(f, ...)` for a
function of the program and `startfile(path, ...)` for another program.*

**F-8** Is the main goal embedding in Go, workers inside Kukel, or both?
*Answered: both; workers first, embedding later.*
**F-9** Blocking `receive()`, event callbacks, or both?
*Answered: both, on both sides.*
**F-10** Can a worker start workers? Limits on how many?
*Answered: yes; no fixed limit.*

---

## 3. Events and a Tk GUI

**Goal**: desktop GUI programs written in Kukel, with Tk widgets and Kukel
functions (closures, bound methods) as event handlers.

```
use tk;

class Counter {
    count, label;
    Counter(window);
    clicked();
}

Counter::Counter(window) {
    count = 0;
    label = new tk::Label(window, "clicked 0 times");
    local button = new tk::Button(window, "Click me");
    button->onClick(this->clicked);               // a bound method as handler
    label->pack();
    button->pack();
}

Counter::clicked() {
    count++;
    label->setText("clicked " + str(count) + " times");
}

main() {
    local window = new tk::Window("Kukel");
    new Counter(window);
    tk::run();                                    // the event loop (section 1.3)
}
```

Design notes:

- **Which Tk binding.** Options in Go:
  - `modernc.org/tk9.0`: Tk 9 without cgo. It keeps `go install` working
    with no C libraries, but it is large and young.
  - a cgo binding to the system's Tcl/Tk: smaller, but it needs Tcl/Tk and
    a C compiler wherever kukel is built.
  - talking to a `wish` process over a pipe (sending Tcl commands): the
    simplest and keeps kukel pure Go, but slower and with less control.
- **Threads**: Tk must run on one OS thread (often the main one). The Tk
  loop runs there and posts events into the interpreter's event queue;
  calls from Kukel into Tk are passed to the Tk thread. This is the main
  technical risk, so a small proof of concept should come first.
  *Found in the first version (TODO.md Q67)*: Tk must stay on the
  **main thread**, used by an interpreter running in the main goroutine.
  Locking some other goroutine to its thread is not enough: when that
  goroutine ends while locked, Go destroys the thread under Tcl/Tk and the
  process crashes. So workers cannot use Tk, an embedding Go program runs
  the interpreter in its main goroutine, and Go tests hand Tk work to the
  main goroutine from `TestMain`.
- **Widgets**: start small: window, label, button, entry (text field),
  text, list box, canvas, menus, message boxes; layout with `pack` and
  `grid`. The Tk API maps well onto native classes (section 1.2).
- **Optional build**: GUI support pulls in large dependencies. Build it
  only with a tag (`go build -tags tk`), or as a separate binary
  (`kukel-tk`), so the default `kukel` stays small.

**F-11** Which binding: pure Go (`tk9.0`), cgo, or a `wish` pipe?
*Answered: pure Go, `modernc.org/tk9.0`.*
**F-12** Tk specifically, or open to another toolkit (Fyne, a web UI)?
*Answered: Tk, with themed (ttk) widgets.*
**F-13** Part of `kukel` behind a build tag, or a separate `kukel-tk`?
*Answered: behind the build tag `tk`; releases add `kukel-tk` archives
built with it. The rest of the design (widgets, `configure`, `bind`,
`grid`, no raw Tcl) is in TODO.md Q67.*

---

## 4. ZeroMQ messaging

**Goal**: Kukel programs that talk to other programs (any language) over
ZeroMQ: request/reply, publish/subscribe, pipelines.

```
use zmq;
use json;

main() {
    local server = new zmq::Socket("REP");
    server->bind("tcp://*:5555");

    while (true) {
        local request = json::decode(server->receive());
        server->send(json::encode({"echo": request, "at": time()}));
    }
}
```

```
use zmq;
use events;

main() {
    local sub = new zmq::Socket("SUB");
    sub->connect("tcp://localhost:5556");
    sub->subscribe("prices.");
    sub->onmessage(fn(msg) { println("update:", msg); });   // via the event loop
    events::run();
}
```

Design notes:

- **Library**: `github.com/go-zeromq/zmq4` is pure Go (keeps `go install`
  simple, covers the common socket types); `github.com/pebbe/zmq4` wraps
  the C libzmq (complete and fast, but needs libzmq and cgo).
- **Socket types**: REQ/REP, PUB/SUB, PUSH/PULL, DEALER/ROUTER, PAIR; multi-part
  messages as vectors of strings.
- **Payloads** are bytes (Kukel strings already are); structured data with
  `json::encode`/`decode` or the binary form (section 1.4).
- **Blocking and events**: blocking `receive()` (with a timeout) for simple
  programs; `onmessage` handlers through the event loop; a `zmq::poll` over
  several sockets.
- **Errors**: a `NetError` kind for connection and timeout problems.

*Done (TODO.md Q43): the module `zmq` with REQ/REP, PUB/SUB, PUSH/PULL,
DEALER/ROUTER and PAIR, multi-part messages, `receive([timeout])`,
`onmessage`, `zmq::poll` and `NetError`. Not done: security (PLAIN, CURVE;
the Go library has no CURVE, so it would mean our own cryptography, or a
build on the C libzmq), monitors.*

**F-14** Pure Go (`go-zeromq/zmq4`) or libzmq (`pebbe/zmq4`)?
*Answered: pure Go.*
**F-15** Which socket types and features first (security/CURVE, monitors)?
*Answered: REQ/REP, PUB/SUB, PUSH/PULL first; the rest later.*

---

## 5. SQL and an ORM

**Goal**: Kukel programs that use SQL databases: first plain queries, then
an object-relational mapping that stores Kukel objects in tables.

### 5.1 Plain SQL

```
use db;

main() {
    local conn = db::open("sqlite:shop.db");     // or "postgres://...", "mysql://..."
    conn->exec("CREATE TABLE IF NOT EXISTS items (id INTEGER PRIMARY KEY, name TEXT, price REAL)");
    conn->exec("INSERT INTO items (name, price) VALUES (?, ?)", "pen", 1.5);

    for (local row in conn->query("SELECT * FROM items WHERE price < ?", 10))
        println(row["id"], row["name"], row["price"]);   // a row is a map

    try {
        conn->begin();
        conn->exec("UPDATE items SET price = price * 1.1");
        conn->commit();
    } catch (e) {
        conn->rollback();
        throw e;
    }
}
```

- Built on Go's `database/sql`; drivers: SQLite (`modernc.org/sqlite`, pure
  Go), PostgreSQL (`pgx`), MySQL (`go-sql-driver/mysql`).
- Always with parameters (`?`), never by gluing strings together (SQL
  injection); values map as nil ↔ NULL, int, float, string, bool, and dates
  once there is a time type (section 1.5).
- `query` gives a vector of maps (simple); `cursor` for large results.

### 5.2 The ORM

```
use db;

class Item : db::Model {           // a native base class (section 1.2)
    static table, columns;          // the mapping, as static data members
    id, name, price;
}

global itemsSetup = db::model(Item, "items", {"id": "INTEGER PRIMARY KEY", "name": "TEXT", "price": "REAL"});

main() {
    local conn = db::open("sqlite:shop.db");
    conn->migrate(Item);                                  // CREATE TABLE if needed

    local pen = new Item();
    pen->name = "pen"; pen->price = 1.5;
    conn->save(pen);                                      // INSERT; sets pen->id
    pen->price = 1.75;
    conn->save(pen);                                      // UPDATE

    for (local item in conn->find(Item)->where("price < ?", 10)->orderBy("name")->all())
        println(item->id, item->name, item->price);       // objects of class Item
    conn->delete(pen);
}
```

- The VM already knows a class's data members (no built-in exposes them
  yet; one would be added), so a mapping by convention is possible: class name → table, data members →
  columns, `id` → primary key. Kukel has no annotations, so types and other
  details come from a description in static members or a `db::model(...)`
  call. (**F-17**)
- Query building by method chaining (`where`, `orderBy`, `limit`) produces
  parameterized SQL.
- Relations (`has many`, `belongs to`), lazy loading and migrations of
  existing tables are each a project of their own: later, if needed.
  *(Relations are done, Q56: `db::many` is has-many, `db::one`
  belongs-to; loading is explicit, since a data member cannot load
  lazily.)*

*Plain SQL is done for SQLite (TODO.md Q44): the module `db` with `exec`,
`query`, `first`, cursors and transactions. The ORM is done too (Q52), as a
data mapper rather than the sketch above: any class, no `db::Model` base;
`conn->migrate(C)`, `save`, `get`, `delete`, and `conn->find(C)->where(...)
->orderby(...)->limit(...)->all()`; see the language reference. PostgreSQL and
MySQL/MariaDB are done (Q54), relations (`db::many`, `db::one`,
`conn->load`, `query->include`; Q56) and UUID keys (`"UUID"` in
`columns()`, the module `uuid`; Q58).*

**F-16** Which databases first (SQLite only, to start)?
*Answered: SQLite first; PostgreSQL and MySQL/MariaDB followed (Q54).*
**F-17** How a class describes its table: conventions only, static data
members, a `db::model(...)` call, or a new syntax?
*Answered: conventions (data members are columns, `id` the key, the class
name in lower case the table), with optional static member functions
`columns()` and `table()`.*
**F-18** Plain SQL only at first, with the ORM later on top?
*Answered: yes; plain SQL came first, then the ORM on top (Q52, Q56).*

---

## 6. Files: a module fs and 9P

**Goal**: programs that work with file trees, local or remote: list and
search directories (a `grep -r`), and reach or serve trees over 9P, the
file protocol of Plan 9 (also spoken by QEMU/KVM `virtfs`, WSL2, gVisor,
plan9port and Inferno).

### 6.1 Local files: fs

```
use fs;

main(args) {
    for (local e in fs::readdir(args[0]))            // the entries, sorted by name
        println(e["name"], e["dir"] ? "/" : e["size"]);
    fs::walk(args[0], fn(path, info) {                // every file below, depth first
        if (!info["dir"] && find(path, ".kkl") > 0) println(path);
    });
}
```

- `fs::readdir(path)`, `fs::stat(path)` (name, size, time as
  `time::Time`, whether a directory, mode), `fs::walk(path, f)`; also
  `fs::mkdir`, `fs::remove`, `fs::rename` and `fs::exists`, so that programs
  need not shell out.
- An `IOError` for what fails, as `readfile` has. A Go program that embeds
  Kukel can leave `fs` out (`StandardModules`), as it does `db`.

**F-22** Entries as maps (`e["name"]`) or as objects of a class `fs::Entry`
(`e->name()`)? *Answered: maps.* **F-23** Does `walk` follow symbolic
links? *Answered: no; they are reported.*

*Done (TODO.md Q61), with `fs::removeall` as a separate name, and the path
functions `join`, `base`, `dir` and `ext`; `walk` gives `(path, entry)`.*

### 6.2 9P: client and server

The client reaches a file tree another system serves; the server serves a
tree whose files are backed by Kukel functions.

```
use p9;

main() {
    local c = p9::dial("tcp://fileserver:564", {"user": "ann"});   // attach as user ann

    for (local e in c->readdir("/logs")) println(e["name"]);
    println(c->read("/logs/today.txt"));
    c->write("/tmp/note", "hello");
    c->close();
}
```

```
use p9;
use events;

global hits = 0;

main() {
    local tree = new p9::Tree();
    tree->file("/status", fn() { return "hits " + str(hits) + "\n"; });   // read: a function
    tree->file("/hit", "", fn(data) { hits++; });                        // write: a function
    tree->dir("/config");
    p9::serve(tree, "tcp://*:5640");      // requests arrive through the event loop
    events::run();
}
```

Then on Linux: `mount -t 9p -o trans=tcp,port=5640 host /mnt/app`, and
`cat /mnt/app/status`, `echo x > /mnt/app/hit` talk to the program.

- The client has the same functions as `fs` (`readdir`, `stat`, `read`,
  `write`, `mkdir`, `remove`), so code written for local trees works on
  remote ones.
- The server runs its handlers on the interpreter's goroutine through the
  event loop, one at a time, as `zmq` handlers do; a slow handler holds up
  the others.
- Libraries: pure Go, so `go install` and cross-compiling keep working, and
  a build tag `no9p` leaves the module out: `github.com/hugelgupf/p9`
  (9P2000.L, client and server, from gVisor), `9fans.net/go/plan9/client`
  (9P2000 client), `github.com/knusbaum/go9p` (9P2000 server and client).

**F-24** Which protocol versions: 9P2000 (Plan 9, plan9port, Inferno),
9P2000.L (Linux, QEMU) or both? This picks the library, or two.
*Answered: both.*
**F-25** Authentication: none (trusted networks, like most 9P use today),
or the protocol's `afid` with a password, or TLS around the connection?
*Answered: none at first, for trusted networks; TLS later as an option
(little code with Go's `crypto/tls`, but the usual 9P clients, such as the
Linux kernel, do not speak it).*
**F-26** The server's tree: built in Kukel as above, or also a directory of
the local disk served as it is (a file server written in Kukel)?
*Answered: both; the disk directory read-only unless asked otherwise.*

*Done, step 1 (TODO.md Q62): `p9::dial` (9P2000.L tried first, then
9P2000), `p9::Tree` with `file`, `dir` and `remove`, and `p9::serve`, which
serves both versions on one address (it reads the version from the
client's first message). Files hold a string or a function; `write`
functions get what a client wrote when it closes the file. 9P2000.L uses
`github.com/hugelgupf/p9`; 9P2000 uses `9fans.net/go/plan9` (its client,
and its message codec for a small server of our own; `go9p` logs to
standard error and pulls in more). The functions of a tree run inside
`events::run()`, or while a client of the same program waits, so a program
can read its own server. Not tried yet: a Linux kernel mount (it needs
root).*

*Done, step 2 (TODO.md Q63): `p9::disk(dir [, {"readonly": false}])`, a
directory of the local disk served as it is: read-only unless asked,
confined to `dir` with Go's `os.Root` (no way out through `..` or symbolic
links), files shown as the user running the server; `tree->mount(path,
disk)` puts one into a tree; the client got `rename`. Next: TLS, as an
option of `serve` and `dial`.*

## 7. Smaller items

Carried over from the last item of TODO.md's milestone list, plus things the
ideas above make useful:

- jump tables for `switch` over dense int cases (*done*, TODO.md Q50);
- a peephole pass over the generated code (*done*, Q50);
- a REPL (`kukel repl`): handy for trying the GUI, messaging and database
  libraries interactively (*done*, TODO.md Q48);
- reporting several compile errors at once (*done*, TODO.md Q46);
- strings without an extra allocation per value (a different `Value`
  layout) (*done*, Q50, with in-place appends for `s += x`);
- a debugger hook (breakpoints, stepping) on the VM, which the
  disassembler and line tables already prepare for (*done*: `kukel debug`,
  TODO.md Q60; the Debug Adapter Protocol for editors could follow on the
  same hook);
- reading directories: the module `fs` of section 6.1 (*done*, Q61), and
  `examples/grep.kkl` searches trees (`-r`);
- a faster VM (register-based, or fewer instructions per call): *done* by
  targeted changes (TODO.md Q69: a fast call path, two fused
  instructions; calls 22-40% faster), and more fused instructions chosen by
  counting what programs run (TODO.md Q74; typical programs 5-36% faster);
  a smaller value (16 bytes instead of 32) was tried and not kept (Q75: the
  garbage collector then has a pointer to check for every number in a
  vector);
- a code formatter (`kukel fmt`): *done* for indentation and spacing
  (TODO.md Q47), and a blank line before `if`, `while`, `do`, `for`,
  `switch`, `try` and `with` (Q53).

---

## 8. Suggested order

Each step is usable on its own and prepares the next:

| Step | What | Needs | Status |
|---|---|---|---|
| 1 | Modules (`use`) from Kukel files and Go, native classes, `json` | — | done (Q35, Q36, Q49) |
| 2 | Public embedding API; several interpreters in one process | 1 | done (Q39) |
| 3 | Event loop, timers; workers with messages | 1 | done (Q37, Q38) |
| 4 | ZeroMQ (blocking first, then events) | 1, 3 | done (Q43), without security |
| 5 | SQL: plain queries, transactions (SQLite first) | 1 | done: SQLite (Q44), PostgreSQL and MySQL (Q54) |
| 6 | Time type; then the ORM on top of SQL | 5 | done: time type (Q36), ORM (Q52), relations (Q56) |
| 7 | Tk GUI (after a proof of concept for the threading) | 1, 3 | done (Q67): the module `tk` (build tag `tk`), on the main thread only |
| 8 | Files: the module `fs` (6.1) | 1 | done (Q61) |
| 9 | 9P client and server (6.2) | 3, 8 | done (Q62, Q63): trees built in Kukel and disk directories; TLS later |

Steps 4, 5, 7 and 8 are independent of each other once 1 and 3 exist. A
`ffi` module (1.6) can come at any point after step 1.

The open questions **F-1** to **F-26** are best answered just before the
step that needs them.
