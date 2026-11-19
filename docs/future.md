# Kukel: ideas for the future

Ideas written down from v0.1.0 on; most are built now (kukel v1.5).
Parts marked *Done* are built in the current version (see the language reference and docs/embedding.md); for the rest,
the Kukel code below shows how things *could* look and is not meant to run.
Each idea lists open questions (**F-n**) to settle before it is built, in
the same way the Q-list in [TODO.md](../TODO.md) settled the first version.

Contents:

1. [Foundations several ideas need](#1-foundations-several-ideas-need)
2. [Multiple interpreters](#2-multiple-interpreters)
3. [Events and a Tk GUI](#3-events-and-a-tk-gui)
4. [ZeroMQ messaging](#4-zeromq-messaging)
5. [SQL and an ORM](#5-sql-and-an-orm)
6. [Files: a module fs and 9P](#6-files-a-module-fs-and-9p)
7. [Smaller items](#7-smaller-items)
8. [Suggested order](#8-suggested-order)
9. [Named arguments](#9-named-arguments) (the first step done, 2026-11-07)
10. [Kukel-MVC: applications from the designer's schemas](#10-kukel-mvc-applications-from-the-designers-schemas) (a proposal, 2026-11-09)
11. [Email: TLS in `net`, and a module `mail` written in Kukel](#11-email-tls-in-net-and-a-module-mail-written-in-kukel) (a proposal, 2026-11-16)
12. [Structured logging and syslog: a module `logging` written in Kukel](#12-structured-logging-and-syslog-a-module-logging-written-in-kukel) (a proposal, 2026-11-16)
13. [A GUI for LMW-Synth over 9P](#13-a-gui-for-lmw-synth-over-9p) (a proposal, 2026-11-16)
14. [Decimals: a type `decimal` in the language](#14-decimals-a-type-decimal-in-the-language) (a proposal, 2026-11-19)

---

## 1. Foundations several ideas need

The four ideas share needs that Kukel does not have today. Building these
first keeps each idea small.

### 1.1 Modules (name spaces)

Today all built-ins share one flat name space with the program (56
functions plus the classes `Error` and `Generator`, reserved at the top
level; locals and members may reuse them). GUI, messaging and database libraries would add hundreds.
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
`math::sqrt`), or stay global as a small core? *Within v1 they stay: the
built-ins keep their names (docs/compatibility.md); new functions go into
modules.* *Answered (2026-11-07) for v2: yes, and v2 should consider
more rules for name spaces (which names are global, how modules are
named and nested).*

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
*Answered for now: callbacks only. Generators (TODO.md Q86) now save and
resume frames (vm.md §4.14), and take values back with `send` (Q96), which
a coroutine would build on.*

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
`db`, `uuid`, `re`, `fs`, `p9`, `utf8`, `term` (Q99) and `net` (Q103), and
`kukel version` lists them. Build tags are done for the large ones (Q55,
Q62): `nopostgres`, `nomysql`, `nosqlite`, `nozmq` and `no9p`. The GUI
module `tk` is the other way round: only in a kukel built with the tag
`tk` (Q67).*

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
*Answered (2026-11-07): not now; postponed until a library needs it.*

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

        if (msg == nil) {
            break;                // the parent stopped us
        }
        local total = 0;

        for (local x in msg["data"]) {
            total += x;
        }
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

*Done (TODO.md Q67): the module `tk`, with the names of the reference
rather than of the sketch above (`onclick`, `settext`, and the loop of the
module `events`, `events::run()`).*

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

    for (local row in conn->query("SELECT * FROM items WHERE price < ?", 10)) {
        println(row["id"], row["name"], row["price"]);   // a row is a map
    }

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
    pen->name = "pen";
    pen->price = 1.5;
    conn->save(pen);                                      // INSERT; sets pen->id
    pen->price = 1.75;
    conn->save(pen);                                      // UPDATE

    for (local item in conn->find(Item)->where("price < ?", 10)->orderBy("name")->all()) {
        println(item->id, item->name, item->price);       // objects of class Item
    }
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
    for (local e in fs::readdir(args[0])) {            // the entries, sorted by name
        println(e["name"], e["dir"] ? "/" : e["size"]);
    }
    fs::walk(args[0], fn(path, info) {                // every file below, depth first
        if (!info["dir"] && find(path, ".kkl") > 0) { println(path); }
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

    for (local e in c->readdir("/logs")) {
        println(e["name"]);
    }
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
  TODO.md Q60; the Debug Adapter Protocol for editors followed on the
  same hook: *done*, `kukel dap`, TODO.md Q77);
- a profiler: which functions and lines take the time, with call counts
  (*done*: `kukel profile`, also in Go's pprof format with `-pprof`, and
  from the Go API, TODO.md Q78);
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
  `switch`, `try` and `with` (Q53); `-strict` adds braces and puts
  functions on several lines (Q95), and long argument lists one per line;
  `-vertical` every list of two or more (`-linelength`, Q106);
- TCP and UDP for any protocol: *done*, the module `net` (TODO.md Q103);
  TLS is not there yet, for `net` nor for `p9`;
- an HTML front end next to Tk, for example through
  [Wails](https://wails.io) (a Go framework whose windows are the
  system's web view: HTML, CSS and JS in front, Go functions bound for JS
  to call, events both ways). *Not decided; a possible future item.* What
  stands in the way: Wails needs cgo on Linux and macOS (WebKitGTK and its
  development libraries, WKWebView), while the releases are built with
  `CGO_ENABLED=0` for all platforms from one machine; it wants the main
  thread and its own event loop, as Tk does (one app per process), so
  calls from JS must be handed to the interpreter's event loop; and it is
  made for its own build tool, which generates the JS bindings and embeds
  the front end when compiling, while a Kukel script would bring its HTML
  when it runs. The ways in, from least to most work:
  - **A.** Kukel inside a Wails app: a Go program built with Wails runs
    Kukel scripts through the embedding API (section 2.1). Nothing in
    Kukel changes; an example under `examples/` with its own `go.mod`, so
    that the main module does not depend on Wails;
  - **B.** a module (say `ui`) for Kukel scripts: the script gives a
    directory of HTML and binds its functions for JS to call
    (`ui::bind("save", save)`, `ui::emit(...)`, run by `events::run()`),
    behind a build tag in a separate cgo binary (`kukel-gui`) built on each
    platform's own CI runner;
  - **C.** the same model without Wails, in pure Go: the module serves the
    script's HTML on localhost with a WebSocket for calls and events, and
    opens the browser. No cgo, every release platform, the one event loop;
    but a browser tab instead of a native window.

  The suggestion was A as an example first, then C if scripts want an HTML
  front end, and B only when a native window really matters (**F-27**).
  *Answered (2026-11-07): A, for now; B and C would add much more.*

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
| 10 | TLS in `net`: `tls://` and `starttls` (11.2) | — | done (Q113): client, `starttls`, and `tls://` listeners |
| 11 | `encoding` (base64) and `net::hostname()` (11.3) | — | done (Q113): base64 and hexadecimal |
| 12 | Email: the module `mail`, written in Kukel (11.4, 11.5) | 10, 11 | done (Q113) |
| 13 | `unixgram://` in `net` (12.4) | — | done (Q114) |
| 14 | Logging: the module `logging`, written in Kukel, with syslog (12) | 11, 13; 10 for syslog over TLS | done (Q114), with the module `os` |
| 15 | `tk::Notebook` (13.3) | 7 | done (Q115) |
| 16 | LMW-Synth GUI: the module `lmw` and a fake synth (13.4) | 9 | done (Q115) |
| 17 | LMW-Synth GUI: Synth and Effects tabs, keeping in step, colours (13.5 to 13.7) | 15, 16; `/info` in the synth | done, with `tk::setcolors` (Q115) |
| 18 | LMW-Synth GUI: the Matrix tab (13.5) | 17 | done, with `c->focus()` of a canvas (Q115) |
| 19 | LMW-Synth GUI: Load, Save, Notes, choosing a synth (13.5) | 17 | done (Q115) |
| 20 | LMW-Synth GUI: the Schematic tab (13.5) | 18 | done, with `dash` for canvas lines and menus in `tk::setcolors` (Q115) |
| 21 | Decimals: the value and its arithmetic (14.8, step 1) | — | proposed (Q117) |
| 22 | Decimals: the literal `12.50d`, the VM, the built-ins (14.8, step 2) | 21 | proposed (Q117) |
| 23 | Decimals in `json`, `db` and the Go package (14.8, step 3) | 22 | proposed (Q117) |
| 24 | Decimals: documents, editors, an invoice example (14.8, step 4) | 23 | proposed (Q117) |

Steps 4, 5, 7 and 8 are independent of each other once 1 and 3 exist. A
`ffi` module (1.6) can come at any point after step 1.

The open questions **F-1** to **F-70** are best answered just before the
step that needs them; F-28 to F-31 are those of the named arguments
(section 9), F-32 to F-38 those of Kukel-MVC (section 10), F-39 to F-46
those of email (section 11), F-47 to F-54 those of logging (section 12),
F-55 to F-63 those of the LMW-Synth GUI (section 13), F-64 to F-70
those of decimals (section 14).

---

## 9. Named arguments

*The first step is done (2026-11-07, TODO.md Q107; the reference, §7,
describes it): named arguments in the calls whose function the compiler
knows, as answered (9.4). Member function calls (9.3) and Go functions
with parameter names (F-30) are still to come.* An argument may be given by the name of its parameter instead of
by its place:

```
area(w, h = w) { return w * h; }
box(width, height, depth = 1, label = "box") { ... }

main() {
    println(area(h: 4, w: 3));                 // 12
    box(10, 20, label: "crate");               // depth keeps its default
    local p = new Point(y: 2, x: 1);
}
```

**Why, and why not anonymous objects.** A map already passes options by
name (`{"width": 10}`, as `tk` does), and an anonymous object would add
little to it. What maps cannot give is checking: a misspelt key is ignored
in silence, and every function repeats its defaults by hand. Named
arguments are checked when compiling, use the defaults the function
already has, and need nothing on the side of the function. Maps stay the
way for open-ended options (a set of options that grows, or is passed on).

### 9.1 The rules

- **Form**: an argument `name: expression`, where `name` is an
  identifier. In an argument list, positional arguments come first, then
  named ones: `f(1, 2, z: 3)`; a positional argument after a named one is
  a compile error ("a positional argument after a named one"). `name =
  expression` is not possible: it is an assignment, an expression of its
  own.
- **Matching**: positional arguments fill the parameters from the first
  on, as now; each named argument fills the parameter of that name. It is
  a compile error to name a parameter that does not exist ("area has no
  parameter hh; its parameters are w and h"), to name one twice, or to
  name one that a positional argument filled already ("w is given twice:
  by place and by name").
- **Required parameters** must all be filled, by place or by name;
  "area: argument h is missing" otherwise.
- **Optional parameters** may be left out anywhere, also between ones
  that are given (`box(10, 20, label: "crate")` skips `depth`): their
  defaults run as now, in the function, in the order of the parameters,
  and see the parameters before them, given or computed.
- **A rest parameter** cannot be named, and gets nothing in a call with
  named arguments: positional arguments may not reach it then (a compile
  error that says so). `v..` (spread) cannot be combined with named
  arguments, since the compiler would not know which parameters it fills.
- **Order of evaluation**: the arguments are evaluated as they are
  written, left to right, whatever the order of the parameters (side
  effects keep their order); then they are put in place.
- **Which calls**, in the first step: the calls whose function the
  compiler knows: functions of the program and of modules written in Kukel
  (`f(...)`, `m::f(...)`), static member functions (`C::f(...)`),
  `Base::m(...)`, and constructors (`new C(...)`, `new m::C(...)`, also
  the base class's constructor that a class without its own uses). Other
  calls with named arguments are a compile error that says why: member
  function calls (`p->m(...)`, and `m(...)` meaning `this->m(...)`, which
  are virtual: an override may have other parameters), calls of function
  values and closures, built-in functions and functions of modules
  written in Go (which have no parameter names in Kukel).

### 9.2 How it would work

- The parser accepts `name: expression` in argument lists (the "?:" and
  labels do not clash: a label stands only at the start of a statement,
  and ":" of ?: follows a "?"). Syntax version 11; syntax.ebnf and
  syntax.lark get the form.
- The compiler, which knows the function's parameters for these calls
  (it checks their number today), turns the named arguments into
  positional ones. Arguments written in another order are evaluated into
  hidden slots first, then pushed in the order of the parameters.
- Skipping an optional parameter needs a way to say "missing" for a place
  before the last one given: today a default runs when fewer arguments
  came (`JUMP_IF_ARG n`: n < argc). A new instruction pushes an internal
  *absent* marker, and `JUMP_IF_ARG` also runs the default when the slot
  holds it. The marker never reaches Kukel code: the compiler only passes
  it for parameters that have a default, and the default replaces it.
  That is a new instruction: bytecode format 12 (`.kkc` files are built
  again). `JUMP_IF_ARG` gets one comparison more; its cost is to be
  measured (GOMAXPROCS=1, best of 9 alternating runs) before the change
  is kept.

  *As built:* measured, that one comparison made unrelated programs 30 to
  40% slower (the Go compiler then kept many more of the dispatch loop's
  values on the stack), whatever its form. So `JUMP_IF_ARG` and the loop
  stay as they were: a function with optional parameters has a second
  copy of its prologue after its body (`NamedEntry`), with a new
  `JUMP_IF_GIVEN` that counts the marker as missing, and a call that
  passes the marker is preceded by `NAMED_CALL`, which starts the frame
  there. `ABSENT`, `NAMED_CALL` and `JUMP_IF_GIVEN` run outside the loop,
  as the generators' instructions do (docs/vm.md, section 4.3). Other
  calls cost the same as before; a call that skips a parameter about
  120 ns more.
- `kukel fmt` writes `name: value` (no space before the ":"); the
  debugger, the profiler and the `.kkc` format need nothing else.

### 9.3 Later: member function calls

`p->m(name: x)` needs the names when the call runs, since the class of
`p` is known only then. The functions in a `.kkc` file keep their slot
names (`SlotNames`, the parameters first), so a call instruction with a
table of names (`CALL_METHOD_NAMED`) can match them at run time, with an
`ArgumentError` for a name the member function does not have. The
methods of classes written in Go have no parameter names and keep
refusing named arguments. This is a second step, with its own design.

### 9.4 Open questions

- **F-28** Is the first step (calls the compiler knows, constructors
  included) useful enough on its own, or should member function calls
  come at once? *Answered (2026-11-07): the first step on its own;
  member function calls later.*
- **F-29** Should a call be allowed to name a required parameter that
  comes before a positional one it skips (`f(b: 2, 1)`)? This proposal
  says no: positional first, as in Python. *Answered: no, as in Python.*
- **F-30** Should Go functions of host modules (`kukel.NewModule(...).Func`)
  be able to declare parameter names, so that `app::notify(level:
  "warn")` works? It would need an addition to the Go API. *Answered:
  yes, later, as an addition to the Go API; until then Go functions
  refuse named arguments with a message that says so.*
- **F-31** `kukel fmt -vertical` puts named arguments one per line like
  the others; should a call with only named arguments always be split,
  whatever their number? *Answered: no, the rule of the other arguments.
  And long vector and map literals should be split the same way (one
  element per line, a "," after the last): see 9.5.*

### 9.5 Long vectors and maps in kukel fmt

*Done (2026-11-07, TODO.md Q106), as answered: `-strict` splits a literal
only when its line is longer than `-linelength` (the outermost first),
`-vertical` every literal of 2 or more elements.*

Raised with F-31: `kukel fmt -strict` (and `-vertical`) split argument
lists that are long or have many items, but not vector and map literals,
which can be just as long. `kukel fmt -vertical` now turns

```
local config = {"host": "localhost", "port": 8080, "timeout": 30, "retries": 3, "log": "info"};
```

into

```
local config = {
    "host": "localhost",
    "port": 8080,
    "timeout": 30,
    "retries": 3,
    "log": "info",
};
```

`kukel fmt -strict` does so only when the line is longer than
`-linelength`: this one, of 99 characters, stays as it is with the
default of 120. Nested literals (a vector of maps) are split from the
outermost on, as calls are, and a literal that holds a split list is
split too.


---

## 10. Kukel-MVC: applications from the designer's schemas

*A proposal (2026-11-09, TODO.md Q111); not built. The open questions are
10.9; F-32 and F-34 are answered. The project is
[kukel-mvc](https://github.com/maarten-boot/kukel-mvc), with its
specification, plan and tests in docs/planning.* The user's data dictionary tool, the
[designer](https://github.com/maarten-boot/designer), describes a
database: its types, fields, tables and schemas, the rules a value must
keep, and how a value is shown to a person and read back. Kukel-MVC would
build applications on such a description: the database as the *model*, a
window (`tk`) or a terminal (`term`) as the *view*, and a *controller*
between them. Neither side would know the other, nor the concrete
database or toolkit: the model would see a store, the view a description
of a screen, so that another database or another toolkit means one new
part, not a new application.

It would be a project of its own, `kukel-mvc`, written in Kukel, not part
of kukel: a framework changes a lot while it is young, and kukel promises
to stay the same (docs/compatibility.md). Kukel itself would need one
thing it lacks, decimals (10.7). Strictly, a view that knows nothing of
the model is *MVP* (model, view, presenter: a "passive view"); the name
keeps the better-known MVC.

### 10.1 What the designer gives

The designer exports a schema for generators
(`designer-schema-export`, `format_version` 1; its
`examples/sales_schema.export.json`). Kukel-MVC would read only that, never
the designer's own model file: the export is resolved (inherited
definitions written out, names instead of references) and versioned, so
it is the contract between the two. A table has its columns, primary key,
foreign keys, indexes and default order; a column has its base type
(`string`, `integer`, `decimal` with precision and scale, `real`, `date`,
`time`, `datetime`, `boolean`), whether it may be empty, its default, and
three kinds of rule, which are the three layers:

| In the export | What it is | Kept by |
|---|---|---|
| `check` | rules the database can enforce itself (`value >= 0`) | the database; the model checks them first, for a message a person can read |
| `application` | rules the database cannot keep: they span tables (`order.ship_to_country == order.customer.country_code`) or are not deterministic (`in_past`) | the model, before a save |
| `interface` | how a value is shown and read back: a picture (`#,##0.00`, `dd-MM-yyyy`), the separators, leniency | the view's edge: parse on the way in, present on the way out |

So what "the database may already do" is marked rule by rule, by the
designer, and not guessed by the framework. The rules are expressions in
the designer's language (`designer-1`: `regex_full_match(value, ...)`,
paths such as `order.customer.country_code`).

### 10.2 The parts

```text
   view (tk, term, ...)          controller                  model
  ┌──────────────────┐  events  ┌──────────────┐  store   ┌─────────────────┐
  │ draws a screen   │ ───────▶ │ parse        │ ───────▶ │ store interface │
  │ description;     │          │ validate     │          │  ├─ db (SQL)    │
  │ sends "changed", │ ◀─────── │ ask, save    │ ◀─────── │  └─ memory      │
  │ "chosen"         │  screen  │ report       │  records │ rules: check,   │
  └──────────────────┘  state   └──────────────┘          │ application     │
                                                          └─────────────────┘
```

- The **model** knows the tables and their rules, keeps the data in a
  store, and refuses what breaks a rule, with the designer's message.
- The **view** draws a screen from a description and reports what the
  person does. It holds no data of its own beyond what it shows.
- The **controller** turns what the person does into requests to the
  model, and what the model answers into what the view shows.

All three run in Kukel's event loop (`events`), which already drives `tk`
and `term`.

### 10.3 The model: a store, not a database

The controller would ask a *store*, never SQL:

```
// the store's member functions, as they could look
get(table, key)                 // one record, or nil
find(table, criteria, order)    // records, a page at a time
save(record)                    // insert or update; the rules first
remove(record)
transaction(f)                  // f() all or nothing
```

Two stores at first: one over the module `db` (SQLite, PostgreSQL,
MySQL; its ORM already makes tables, saves, finds and follows relations,
and has `begin`, `commit` and `rollback`), and one in memory, for tests
and for screens that need no database. A store would say what it can do
itself (*capabilities*: "keeps check rules", "makes keys"), and the model
would do the rest. That is the one place where the database shows
through, on purpose and by name, instead of everywhere.

Before a save the model would run the `check` rules (the database keeps
them too, but its errors are not meant for people) and the `application`
rules, which may follow paths to other tables through the store
(`order.customer.country_code`).

### 10.4 The view: screens, not widgets

A layer that maps every widget of every toolkit onto one set ends with
what all of them have in common, which is little. The view would instead
get a *screen description*: fields (label, picture, required, read-only,
the current value and error), lists of records, groups, and actions. A
*renderer* draws it in its own way: `tk` with entry fields and a table,
`term` with a form drawn in ANSI codes. A new toolkit is a new renderer.

```
// a screen description, as it could look
{
    "title": "Customer",
    "table": "Customer",
    "fields": [
        {"column": "id", "label": "Id", "readonly": true},
        {"column": "name", "label": "Name", "required": true},
        {"column": "country_code", "label": "Country", "required": true}
    ],
    "actions": ["save", "cancel", "delete"]
}
```

The view sends only events: "field `name` changed to this text", "action
`save` chosen". It never parses or checks a value; it shows the error the
controller gives it.

### 10.5 The controller

When a field changes, the controller parses the text with the column's
interface (the picture and separators): text that cannot be read is an
error at once ("not a number"). The value it reads is a *candidate*,
which the column's rules then check. On `save` it asks the model, which
runs the rules across fields and tables and the store. Every error goes
back to the view with the field it belongs to. Values on their way out
are shown with the same interface. The interface never refuses a value
for its size or range: that is a rule's work (the designer's
docs/designer-interfaces.md, §1).

### 10.6 The rules: generated, not interpreted

The `designer-1` expressions could be read while the application runs
(an interpreter written in Kukel) or turned into Kukel code once, from the
export, by a generator: a function per rule, compiled with the
application (`kukel build`). Generating is the proposal: the Kukel
compiler then checks the rules, they run at Kukel's speed, and the
generated code can be read and debugged with Kukel's tools. Its cost is a
build step after each change of the schema (F-33).

### 10.7 What Kukel needs: decimals

Prices and amounts are `decimal` in the designer (precision 12, scale 2),
and Kukel has only `int` and `float`; money in floats is wrong
(`0.1 + 0.2`). The ways open:

- a type `decimal` in Kukel: literals (`12.50d`?), arithmetic with the
  operators, exact; a syntax version and a bytecode format, and the most
  work;
- a standard module `decimal` with a class, written in Go: `new
  decimal::Decimal("12.50")`, `a->add(b)`: no change to the language,
  clumsier to write;
- a convention: amounts as ints of the smallest unit (cents), the scale
  from the export: no change to Kukel, but every calculation has to keep
  it.

`db` would read and write such values as the database's `NUMERIC` /
`DECIMAL` columns, and `json` as numbers or strings (F-32).

### 10.8 The first step

One thin slice, from beginning to end, before anything general: the table
`Customer` of `sales_schema.export.json`, as a list and an edit form, in
`term` and in `tk` from one screen description, with parsing, the rules,
saving to SQLite, and the errors shown at their fields. If both renderers
work from one description, the split holds; if not, it shows early, while
it is cheap to change.

### 10.9 Open questions

- **F-32** Decimals (10.7): a type in the language, a standard module, or
  ints of the smallest unit? *Answered (2026-11-09): a decimal type in
  the language, added to kukel; its own design and questions come first:
  section 14.*
- **F-33** The rules (10.6): generated into Kukel code from the export
  (proposed), or interpreted while the application runs?
- **F-34** Screens (10.4): where do screen descriptions come from? A new
  kind of item in the designer (with the fields, their order and groups,
  and the tables they show); files of the application; or screens made
  from the tables by default (every column, in order) that an
  application changes where it needs to? *Answered (2026-11-09): default
  screens from the tables first; ways to change them later.*
- **F-35** Two people changing one record: does a save check that the
  record did not change since it was read (a version column, *optimistic
  locking*), and is each action of the person one transaction?
- **F-36** The view's contract: plain calls between controller and view,
  or messages only (as events), which would also let a view run in
  another process or on another machine later (a browser, over `zmq`)?
- **F-37** The project: a repository of its own (`kukel-mvc`, modules
  such as `mvc`, `store` and `forms`), and from which kukel version on?
- **F-38** The export's version: Kukel-MVC reads `format_version` 1; does
  a newer export refuse to load, or load what it knows?

## 11. Email: TLS in `net`, and a module `mail` written in Kukel

*A proposal (2026-11-16, TODO.md Q113); TLS in `net` (11.2),
`encoding` and `net::hostname()` (11.3) and `mail` (11.4, 11.5; built
2026-11-17, reference.md §12) are built. The open questions of 11.7 are
all answered.* Programs that send mail: a report at night, a password reset, a
notice from a Kukel-MVC application. The model is
[Symfony Mailer](https://github.com/symfony/mailer): a message, an
envelope and a transport kept apart, and transports named by a DSN
string. The bulk of it, the messages, MIME and the SMTP conversation,
would be a standard module written in Kukel, as `iter` is; Go would give
only what Kukel should not do itself: encryption, and work on large
amounts of bytes.

### 11.1 The split between Go and Kukel

| Part | Where | Why |
|---|---|---|
| TLS: encrypted connections, and STARTTLS on an open one | Go, in `net` | cryptography is never written in Kukel; IMAP, POP3, LDAP and a later HTTP client need it too |
| base64 | Go, a small module `encoding` | attachments of megabytes, a byte at a time in the VM, would be slow |
| the host's name | Go, `net::hostname()` | SMTP's `EHLO` names the sending host |
| messages, MIME, header encoding, quoted-printable | Kukel, `mail` | well specified text work; readable for those who use it |
| the SMTP conversation, transports | Kukel, `mail` | a protocol of lines over a `net::Conn` |

`mail` would show whether Kukel carries a real library: it is the
largest standard module written in Kukel so far. If a part turns out too
slow in Kukel (quoted-printable of a long body, say), that part moves to
Go, not the module.

### 11.2 TLS in `net`

```
use net;

main() {
    local c = net::connect("tls://smtp.example.com:465", 10);   // TLS from the start (SMTPS)
    println(c->readline());

    local d = net::connect("tcp://smtp.example.com:587", 10);   // plain, then STARTTLS
    // ... EHLO, STARTTLS, the server's 220 ...
    d->starttls("smtp.example.com");                            // from here on encrypted
}
```

- `net::connect("tls://host:port" [, timeout [, options]])`: TLS from the
  first byte (*implicit TLS*; SMTPS on 465, IMAPS on 993).
  [RFC 8314](https://www.rfc-editor.org/rfc/rfc8314) prefers it to
  STARTTLS for mail.
- `c->starttls(servername [, options])`: the handshake on an open plain
  connection; reads and writes after it are encrypted. Two refusals keep
  it safe:
  - data that has come but has not been read when `starttls` is called is
    a `NetError`, not something to keep. It could only have been sent
    before the encryption, by someone in between, to be read as if it
    came after (the STARTTLS command injection of 2011, CVE-2011-0411 and
    its kind);
  - a connection handed to the event loop (`ondata`) cannot start TLS.
- The server's certificate is checked against the system's roots and the
  name (`servername`, or the host of the address). `options` (a map) can
  name `"ca"` (a file of root certificates, for an own CA or a test
  server), `"cert"` and `"key"` (a client certificate), and
  `"insecure": true`, which skips the check and says so in its name. TLS
  1.2 is the oldest version accepted.
- `c->tls()`: `nil` for a plain connection, else a map: `version`,
  `cipher`, `servername`, the certificate's `subject`, `issuer` and
  `expires` (a `time::Time`).
- A failed handshake or check is a `NetError` that says what failed
  (`certificate signed by unknown authority`, `certificate is for
  mail.example.org, not smtp.example.com`).
- `net::listen("tls://...", options)` with `"cert"` and `"key"`: a server
  side, for tests now and servers later. (F-43)

*Built (2026-11-16): as above, with F-43 answered: `tls://` listeners
in the first step, STARTTLS on the listening side later. A listener does
each handshake as the connection comes, so that a program can connect to
its own listener; a refused STARTTLS (data before the handshake) closes
the connection too. See the reference, `net`.*

### 11.3 `encoding`

`encoding::base64(s [, width])` and `encoding::unbase64(s)`, with `width`
breaking the text into lines (76 for MIME); later perhaps `hex` and
`unhex`. A tiny module of its own rather than more functions in
`strings`, so that a Go program that embeds Kukel can allow it apart
(F-42).

### 11.4 Messages

```
use mail;

main() {
    local m = new mail::Message();
    m->from("Shop <shop@example.com>")
     ->to("ann@example.org", "Bob <bob@example.org>")
     ->bcc("archive@example.com")
     ->subject("Your order 1042")
     ->text("Thank you for your order.\n")
     ->html("<p>Thank you for your <b>order</b>.</p>")
     ->attach("invoice-1042.pdf", readfile("invoice-1042.pdf"), "application/pdf");

    local t = mail::transport("smtp://shop:secret@smtp.example.com:587");
    t->send(m);
}
```

- A `mail::Message` holds the addresses (from, to, cc, bcc, reply-to,
  sender), the subject, other headers, a text body, an HTML body,
  attachments, and inline parts that the HTML shows (`cid:`). Each
  setter gives the message, so that calls chain.
- `m->text()` without arguments gives the text; `m->bytes()` the whole
  message as it is sent (RFC 5322 and MIME), for a file, a test, or a
  transport of one's own.
- The structure follows from what the message holds: `text/plain` alone;
  `multipart/alternative` for text and HTML; `multipart/related` around
  HTML with inline parts; `multipart/mixed` around all that with
  attachments. Boundaries come from `uuid`, `Date` from `time`,
  `Message-ID` from `uuid` and the sender's domain.
- Headers that are not ASCII are encoded words (RFC 2047, `=?UTF-8?B?...?=`),
  bodies quoted-printable (text) or base64 (other), lines at most 78
  characters where the rules allow.
- **Header injection is refused.** A CR or LF in a subject, an address, a
  name or any header value is a `ValueError` when it is set, not later:
  otherwise one field of a web form adds headers or recipients to the
  mail. This is the oldest bug of mail libraries.
- **Line endings are normalized.** Every line ending of a body, `\n`,
  `\r\n` or a bare `\r`, is sent as CRLF, and a line that starts with `.`
  gets another (dot-stuffing): a bare `\n.\r\n` or `\r.\r` in a body
  could otherwise end the data early on some servers (SMTP smuggling,
  2023).
- `bcc` is never a header: it is part of the envelope only.

### 11.5 Envelopes and transports

- The *envelope* is what the transport tells the server: one sender
  (`MAIL FROM`) and the recipients (`RCPT TO`). By default it comes from
  the message (sender or from; to, cc and bcc); `t->send(m, envelope)`
  can give another, for bounces to another address, say.
- `mail::transport(dsn)` makes a transport from a DSN, as Symfony's:

| DSN | Transport |
|---|---|
| `smtp://user:pass@host:587` | SMTP; STARTTLS when the server offers it (F-40) |
| `smtps://user:pass@host:465` | SMTP over TLS from the start |
| `smtp://localhost:25?verify=0` | a local relay; options after `?` |
| `memory://` | keeps the messages, `t->sent()` gives them: for tests |
| `null://` | sends nothing |

  Later perhaps `failover(a b)` and `roundrobin(a b)` of other DSNs, as
  Symfony has. A transport of one's own is a class with `send(m,
  envelope)`.
- The SMTP conversation: the greeting, `EHLO` (with `net::hostname()`),
  `STARTTLS` and `EHLO` again, `AUTH PLAIN` or `AUTH LOGIN` (only over
  TLS, unless the DSN says otherwise), `MAIL FROM`, one `RCPT TO` per
  recipient, `DATA`, `QUIT`; replies over several lines (`250-...`), and
  `SMTPUTF8` and `8BITMIME` when offered. A refusal is a `mail::MailError`
  with the server's code and text (`550 5.1.1 <x@example.org>: no such
  user`), and for `RCPT TO` which recipients were refused; a connection
  that fails is the `NetError` of `net`.
- A transport can send several messages over one connection (`RSET`
  between them) and closes it with `t->close()` or `with`.

### 11.6 What it leaves out

- The HTTP APIs of mail services (SendGrid, Mailgun, ...): they need an
  HTTP client, which kukel does not have. SMTP reaches all of them.
- A queue for sending later, events around a send, templates: an
  application builds them on `send` if it needs them.
- DKIM and S/MIME signatures: cryptography, and so Go, if ever (F-44).
- A `sendmail` transport: Kukel cannot start another program.
- Receiving mail (IMAP, POP3): a later module, on the same TLS.

How it would be tested: the MIME that `mail` writes is read back by Go's
own `net/mail` and `mime/multipart` in the tests, an independent check;
the SMTP conversation runs against a small SMTP server in the Go tests
(on `localhost`, with STARTTLS and a test certificate), which also sends
the replies that must fail; the cases of 11.4 (CR and LF in headers,
bare line endings, lines that start with a dot) each get a test.

Steps: 1. TLS in `net` (useful on its own); 2. `encoding` and
`net::hostname()`; 3. `mail`: messages and MIME, then SMTP.

### 11.7 Open questions

- **F-39** Names: `mail` with `mail::Message`, `mail::transport`,
  `mail::MailError` (proposed), or `mailer` as Symfony's?
  *Answered (2026-11-17): `mail`, as proposed; built.*
- **F-40** `smtp://` without TLS: when the server does not offer
  STARTTLS, does sending stop (safe; `?tls=0` to allow plain), or go on
  in plain text (as Symfony does by default)? Proposed: stop, unless the
  host is `localhost`. *Answered (2026-11-17): stop, except on
  `localhost`, `127.0.0.1` and `::1`; `?tls=0` sends in plain text.*
- **F-41** A message with bcc recipients: sent once with all of them in
  the envelope (proposed; the server does not show them), or once per
  bcc recipient? *Answered (2026-11-17): once, as proposed; a refused
  recipient stops the message for all.*
- **F-42** `encoding` as a module of its own (proposed), or base64 in
  `strings`? *Answered (2026-11-16): a module of its own; built, with
  `hex` and `unhex`.*
- **F-43** A TLS server side (`net::listen("tls://...")`) in the first
  step, or only the client, with the test server written in Go?
  *Answered (2026-11-16): `tls://` listeners in the first step;
  STARTTLS on the listening side later.*
- **F-44** DKIM signing: left out (proposed), or planned in Go as
  `mail`'s one native helper? *Answered (2026-11-17): left out.*
- **F-45** Addresses: checked only for what would break the message
  (CR, LF, no `@`, a malformed `Name <addr>`; proposed), or by the full
  grammar of RFC 5322? *Answered (2026-11-17): what would break the
  message, as proposed.*
- **F-46** The DSN carries the password (`smtp://user:pass@...`): also a
  map form (`{"host": ..., "password": ...}`) so that it can come from a
  file or the environment without building a string, and never shown
  when a transport prints? *Answered (2026-11-17): yes, both; the
  password is kept in a closure, so even printing the object shows
  `***`.*

## 12. Structured logging and syslog: a module `logging` written in Kukel

*A proposal (2026-11-16, TODO.md Q114); built (2026-11-18, step 14,
reference.md §12), with the changes noted in 12.4 and 12.7. The open
questions of 12.7 are all answered.* Programs that run unattended (a server, a nightly job, a
Kukel-MVC application) need to say what they did, in a form both people
and log systems can read: a level, a message, and the facts around it as
named values, sent to standard error, a file, or the system's log. The
model is PHP's [Monolog](https://github.com/Seldaek/monolog) rather than
Python's `logging`; as with email (section 11), the module is written in
Kukel and Go gives only the transport underneath. The name is `logging`,
since `log(x)` is the built-in logarithm.

### 12.1 Monolog, not Python's logging

- **Monolog's model is small**: a logger per *channel* (`"shop"`,
  `"db"`), a stack of *handlers* (where a record goes), *processors* (add
  facts to every record: the host, a request id), *formatters* (a line,
  JSON, syslog). Each call is `log(level, message, context)`, with the
  context a map: the structure is there from the start, not added later.
- **Its levels are syslog's**: the eight severities of RFC 5424, debug to
  emergency. Python has five levels of its own, mapped to syslog in its
  handler.
- **Python's tree of named loggers** (`a.b.c` passing records up to
  `a.b` and `a`, each with handlers and levels) is powerful but hard to
  predict: a record shows twice, or not at all, because of a logger
  further up. Here a channel is only a name; one configuration says
  which handlers a channel uses (12.5). What is taken from Python: a
  logger for a channel is made once and found again by name
  (`logging::get("shop")`).

### 12.2 Records, levels, messages

```
use logging;

main() {
    local log = logging::get("shop");
    log->info("order {id} paid by {user}", {"id": 1042, "user": "ann", "total": 99.5});
    log->warning("stock low", {"item": "pen", "left": 3});
    try {
        pay(1043);
    } catch (e) {
        log->error("payment failed", {"order": 1043, "error": e});
    }
}
```

- The levels, with syslog's numbers: `emergency` (0), `alert` (1),
  `critical` (2), `error` (3), `warning` (4), `notice` (5), `info` (6),
  `debug` (7); a member function for each, and `log->log(level, message,
  context)`.
- A *record* is a map: `time` (a `time::Time`), `channel`, `level`,
  `message`, `context`, and `extra` (what processors add). Handlers and
  formatters see records, so a handler of one's own is a small class.
- `{name}` in the message is filled from the context when the record is
  formatted (as PSR-3, PHP's logging interface); the context keeps the
  values themselves, for formats that keep structure. An `Error` in the
  context shows its kind, message and place.
- **A record below a logger's level costs almost nothing**: the level
  check comes first, before a record is made or anything formatted. A
  `debug` call in a loop is then a comparison when debugging is off.

### 12.3 Handlers, processors, formatters

| Handler | Where records go |
|---|---|
| `logging::Stderr` | standard error, a line each (the default) |
| `logging::File(path)` | a file, appended to (`fs`); reopened when it is moved away (for logrotate) |
| `logging::Syslog(address)` | a syslog server or the local syslog (12.4) |
| `logging::Memory()` | keeps the records: `h->records()`, for tests |
| `logging::Null()` | drops them |

- Each handler has its own lowest level, so that `error` and worse can go
  to syslog while everything goes to a file. A handler can stop a record
  from going to the handlers after it (Monolog's *bubble*).
- Processors are functions `fn(record)` that add to `extra`:
  `logging::host` (`net::hostname()`), `logging::pid` (needs the process
  id from Go), and one's own, such as a request id.
- Formatters: `logging::Line` (`2026-11-16T10:00:00.123Z shop.INFO: order
  1042 paid by ann {"total":99.5}`, with a format of one's own),
  `logging::JSON` (a JSON object a line, for log systems; via `json`),
  `logging::RFC5424` and `logging::RFC3164` (12.4).
- **A message cannot forge a record.** A newline or other control
  character in a message or a context value is escaped in the line and
  syslog formats (`\n`), so that text from outside never shows as a log
  line of its own.

### 12.4 Syslog

- **The format**: RFC 5424 to a server (*built: the local syslog gets
  RFC 3164 instead, below*):
  `<14>1 2026-11-16T10:00:00.123Z host shop 4711 - [ctx@32473 id="1042" user="ann"] order 1042 paid by ann`.
  The priority is facility × 8 + severity (here `user`, 1, and `info`, 6); the facility is `user` unless
  set (`local0` ... `local7`, `daemon`, ...). The context becomes
  STRUCTURED-DATA, with `"`, `\` and `]` escaped in values as the RFC
  says, and names cut to its 32 characters. (32473 is the example
  enterprise number of the RFC; F-52.) RFC 3164, the older format
  without structure, for receivers that need it (F-49).
  *Built with a change (2026-11-18): journald does not read RFC 5424 on
  `/dev/log` reliably (it parses the BSD form that the C library's
  `syslog()` sends, and loses the identifier of other text), so the local
  syslog (`unixgram://`) gets RFC 3164 without the host name, as the C
  library writes it, and servers (`udp://`, `tcp://`, `tls://`) RFC 5424;
  the option `format` chooses either.*
- **The transports**:
  - the local syslog: `unixgram:///dev/log` (journald's socket on most
    Linux systems; rsyslog and syslog-ng read it too). It is a Unix
    *datagram* socket, which `net` could not reach: `net::udp` takes
    `unixgram://` addresses as well (Go, the one new piece of `net`
    here; *built 2026-11-17, step 13: `net::udp("unixgram://")` for a
    socket that only sends*);
  - UDP (`udp://host:514`): one record a packet, lost without notice
    when the network drops it;
  - TCP (`tcp://host:514`) with the octet counting of RFC 6587 (each
    record preceded by its length), so that a newline in a record cannot
    split it;
  - TLS (`tls://host:6514`, RFC 5425), with section 11's TLS.
- A syslog that cannot be reached does not stop the program: the record
  goes to standard error with a note, once a while, not once per record
  (F-51).
- Windows has no syslog: there `Stderr` and `File`; the Windows event log
  is left out.

### 12.5 Configuration

```
use logging;
use toml;

main() {
    logging::configure(toml::decode(readfile("logging.toml")));
    logging::get("shop")->info("started");
}
```

```toml
level = "info"                 # every channel, unless it says otherwise
handlers = ["stderr"]

[handler.stderr]
type = "stderr"

[handler.syslog]
type = "syslog"
address = "unixgram:///dev/log"
facility = "local0"
level = "warning"

[channel.shop]
level = "debug"
handlers = ["stderr", "syslog"]
```

`logging::configure(map)` takes a map, so the configuration can come
from TOML, JSON or code alike; without it every channel writes `info`
and worse to standard error. A configuration that names an unknown
handler or level is a `ValueError` naming the key, when it is read.

### 12.6 What it leaves out, and how it would be tested

- Out: sending records in the background (the event loop or a worker)
  until it is shown to be needed (F-50); handlers for log services over
  HTTP (no HTTP client); the Windows event log; reading logs.
- Tests: the `Memory` handler for the logic; the RFC 5424 and RFC 3164
  output checked against the RFCs' own examples and read back by a Go
  parser of the format in the tests; the transports against a Unix
  datagram socket, a UDP socket and a TCP server of the test; the
  escaping cases of 12.3 and 12.4 (newlines, `]`, `"` and `\` in values)
  one test each; the cost of a call below the level measured
  (GOMAXPROCS=1, best of 9).

Steps: 1. `unixgram://` in `net` and `net::hostname()` (the latter shared
with section 11); 2. `logging` with `Stderr`, `File`, `Memory` and the
`Line` and `JSON` formatters; 3. syslog: the formats and the transports
(TLS after section 11's step 1).

### 12.7 Open questions

- **F-47** The model: Monolog's channels with one configuration
  (proposed), or Python's tree of named loggers?
  *Answered (2026-11-18): Monolog's, as proposed; built.*
- **F-48** The name: `logging` (proposed; `log` is the logarithm), or
  `logger`, or `syslog` for the whole?
  *Answered (2026-11-18): `logging`, as proposed.*
- **F-49** RFC 3164 at all, or only RFC 5424 (which current rsyslog,
  syslog-ng and journald read)?
  *Answered (2026-11-18): both, as proposed. Journald turned out not to
  read RFC 5424 on `/dev/log`, so RFC 3164 is the local syslog's default
  (12.4).*
- **F-50** Records sent in the background, by the event loop or a
  worker, so that a slow syslog does not slow the program; or always at
  once (proposed for the first step)?
  *Answered (2026-11-18): at once, as proposed; the background stays
  out until it is shown to be needed.*
- **F-51** A handler that fails: to standard error now and then
  (proposed), an exception in the program, or silence?
  *Answered (2026-11-18): as proposed: its records go to standard error,
  with a note at most once a minute; a server that cannot be reached is
  tried again after 30 seconds.*
- **F-52** The STRUCTURED-DATA id: `ctx@32473` (the RFC's example
  number, proposed until there is reason for another), a private
  enterprise number registered for kukel, or set by the configuration?
  *Answered (2026-11-18): `ctx@32473`, as proposed; `extra` goes in
  `extra@32473`.*
- **F-53** The process id for `logging::pid` and the `PROCID` of RFC
  5424: a built-in `pid()`, or `os::pid()` in a module for the process
  (which might later hold the environment and exit codes)?
  *Answered (2026-11-18): `os::pid()`, in a new module `os` written in Go.
  The environment stays with the built-in `getenv`, which `DenyBuiltins`
  already controls.*
- **F-54** `{name}` placeholders filled from the context (PSR-3,
  proposed), or Kukel's own string formatting?
  *Answered (2026-11-18): PSR-3, as proposed (Kukel has no string
  formatting of its own).*

---

## 13. A GUI for LMW-Synth over 9P

*A proposal (2026-11-16, TODO.md Q115); steps 15 (`tk::Notebook`), 16
(the module `lmw`, a fake synth), 17 (the Synth and Effects tabs,
keeping in step, colours), 18 (the Matrix tab) and 19 (Load, Save, Notes,
choosing a synth) built: the first version is complete. Since then (all
2026-11-18): an on-screen keyboard, and step 20, the Schematic tab; left
are meters and moving the schematic's blocks, which wait for the synth
(13.2).
The open questions are 13.9; all are answered (2026-11-17) as
proposed.* [LMW-Synth](https://github.com/maarten-boot/software-synth-LMW) is
a semi-modular polyphonic synthesizer (C++20, JUCE; standalone and VST3)
whose engine is a 9P2000 server and the single source of truth for its
state (its Spec S10). Its own JUCE GUI stays; a second GUI, written in
Kukel with `tk` and `p9`, is planned there as Phase 7 and is to live in
the Kukel project (its Spec S10.8). The synth is AGPLv3; the Kukel GUI is
a separate program that only talks 9P to it, so it can be MIT like the
rest of Kukel.

### 13.1 What the synth serves

| File | | What it is | Use in the GUI |
|---|---|---|---|
| `/params/<module>/<name>` | rw | each of the 51 parameters, the dotted id as a path (`vcf1.cutoff` is `params/vcf1/cutoff`); the value as text: floats as the shortest text that reads back as the value stored (since 2026-11-18; 7 significant digits before), choices by name, integers. Writing takes the same, a choice also by a prefix (`band`) or an index; a number out of range is clipped; each write applies at once | faders and choice boxes |
| `/cables` | rw | one line per patched input, `input=out[@level]+out...` (`vcf1.cutoff=env2.out@0.5+lfo1.out`, at most 4 outputs); writing a line sets that input's cables, `input=` removes them, `clear` all | the matrix |
| `/ports` | r | the 22 inputs with their normalled source (`input  lfo1.out -> vco1.fm`, `none` for `vcf1.res`), then the 16 outputs | rows and columns of the matrix, sockets: nothing hard-coded |
| `/patch` | rw | the whole state as an `.lmwpatch` text (parameters, cables, `[schematic]`, `[notes]`, the name); writing loads a patch when the file is closed (an error comes back on the close) | loading and saving files; keeping the GUI in step |
| `/notes` | rw | the Notes text, replaced on close | the Notes tab |
| `/source` | r | the repository URL (AGPLv3 §13) | an About box (later: not in the first version) |

Errors are 9P error strings (`bad value`, `unknown input: nope`, a patch
file error, `busy` when the synth's message thread did not answer within
5 s). The synth listens on a Unix socket
`$XDG_RUNTIME_DIR/lmw-synth/<pid>-<n>.sock` (owner only; macOS
`$TMPDIR/lmw-synth`, Windows `%TEMP%\lmw-synth`), and on TCP only when its
config file asks for it (`tcp!host!port`). There is no authentication.

**Tried on 2026-11-16** against the synth itself (the standalone under
Xvfb) with the `p9` client of kukel 1.4:
`p9::dial("unix:///run/user/1000/lmw-synth/<pid>-1.sock")` connects (the
synth answers 9P2000; the client falls back to it by itself); parameters
read and write, choices by prefix, cables with levels and `clear`; the
synth's errors arrive as an `IOError` with its message
(`p9::Client::write: /cables: unknown input: nope`), a bad patch on the
close of `/patch`. A parameter write or read takes about 0.1 ms, a read
of the whole `/patch` (1.3 KB) 0.15 ms: polling is cheap. Found: a synth
stopped with SIGTERM leaves its socket file (the next instance removes
it), so a GUI must pass over sockets it cannot dial. *(Since
2026-11-18 the synth removes its socket on SIGTERM, SIGINT and SIGHUP;
13.8.)*

### 13.2 What is missing, in the synth

1. **No description of the parameters.** Ranges, defaults, units, choice
   lists and a log scale (the cutoff) are not served. Copying them from
   the synth's Spec S5a.1 into Kukel would drift from the synth.
   Proposed: a read-only `/info` in the synth (F-56).
   *The format (2026-11-17), as `examples/lmw/fakesynth.kkl` serves it
   for the synth to copy: one JSON array, one object per parameter in the
   synth's order, with `id`, `name` (the synth's own, "VCF 1 Cutoff"),
   `kind` (`"float"`, `"int"` or `"choice"`) and `default`; a float and an
   int also `min` and `max`; a float `unit` (how the value is shown: `"%"`
   is the value times 100, `"dB"` 20·log10 of it, `"s"`, `"Hz"`, `"oct"`
   and `"st"` the value itself), and when the synth has them `centre` (the
   value in the middle of the fader: JUCE's `setSkewForCentre`, a log
   scale) and `step` (the smallest change, 0.01 for the tune); a choice
   `choices` (the names) and its `default` by name. For example
   `{"id": "vcf1.cutoff", "name": "VCF 1 Cutoff", "kind": "float", "min":
   20.0, "max": 18000.0, "default": 18000.0, "unit": "Hz", "centre":
   600.0}`.*
2. **No notice of changes.** Changes from the synth's own GUI, a DAW or
   another client are seen only by reading again. Polling `/patch` works
   now; a `/serial` file (a number that goes up with each change) would
   make most polls a few bytes (F-57).
   *Served by the synth (2026-11-18): `/serial`, a decimal number and a
   newline, up with every change of parameters (also DAW automation),
   cables, name, notes, schematic or restored state, not with notes
   played; one change may raise it by more than 1. `lmw`'s `changes()`
   asks it first, and the fake serves it by default.*
3. **No way to play notes.** An on-screen keyboard needs a `/keys` file
   (`on 60 100`, `off 60`); until then notes come from MIDI (F-59).
   *Served by the synth (2026-11-18): `/keys`, lines `on <note>
   [velocity]` and `off <note>` (note and velocity at most 3 digits, 0 to
   127; velocity 100 without one, 0 is off; words apart by spaces or tabs;
   empty lines skipped); every line is checked before any is played, a
   bad one is `bad key line: <line>`; reading gives nothing. In `lmw`:
   `s->play(text)`, `s->noteon(note [, velocity])`, `s->noteoff(note)`;
   the fake checks the lines as the synth does and keeps them in
   `played`.*
4. **No meters or scope.** The synth's Spec S10.6 plans telemetry files;
   there are none yet.

These go to the synth's own project (`../software-synth-LMW`), in a
session there. *As built (2026-11-18): the GUI needs `/info` (without it
it says a newer synth is needed and stops); `changes()` of `lmw` reads
`/serial` when there is one and `/patch` otherwise; `/keys` is played by
the on-screen keyboard (13.5); telemetry is not served, so there are no
meters. Moving the schematic's blocks needs one more: a `/schematic` file
(rw, the layout alone, as the synth's `schematicLayout` text), since
writing `/patch` would rewrite the parameters, cables and notes too.*

### 13.3 What is missing, in Kukel

5. **Tabs.** `tk` has no notebook and no way to hide a widget. Proposed:
   `tk::Notebook`, Tk's `ttk::notebook` (F-60):
   `new tk::Notebook(parent)`, `n->add(frame, title)`,
   `n->select(i)`, `n->selected()`, `n->onchange(f)`.
   *Built (2026-11-17): as proposed; `select` also takes the page's
   frame, a notebook holds only frames made in it, and `onchange` is
   called for the user's choice only (not for `add` or `select`). See the
   reference, `tk`.*
6. **Calls that block.** A `p9` call blocks the event loop until the
   answer comes, with no timeout after `dial`. Normally 0.1 ms; when the
   synth's message thread hangs, up to 5 s before `busy`. Proposed: plain
   calls first, a worker only if that proves a problem (F-61).
7. **Linear sliders.** `tk::Scale` is linear: a log parameter (the
   cutoff, 20 Hz to 18 kHz) is a slider from 0 to 1 that the GUI maps,
   with the value in a label beside it. *Built (2026-11-18) with 1000
   steps, and the value in an entry under the fader, where it can also be
   typed.*
8. **No configuration directory.** The config file's place comes from
   `getenv` (`XDG_CONFIG_HOME`, `HOME`, `APPDATA`); no change to Kukel.
9. **No colours.** Found at step 17: `tk`'s themed widgets took no
   colours (only a canvas a `background`), so a white background, black
   text and the units' colours could not be set. *Built (2026-11-18), as
   the user chose: `tk::setcolors(palette)` for all widgets at once (parts
   `background`, `foreground`, `field`, `select`, `selectforeground`,
   `trough`, `border`, `active`, `disabled`; through the ttk styles and
   the state maps of the theme's classes, and the classic texts and list
   boxes one by one), `tk::colors()`, and `background` for a frame and
   `background` and `foreground` for a label. See the reference, `tk`.*
   *Found at step 20: menus kept Tk's grey; `tk::setcolors` colours them
   too since then.*
10. **No keyboard focus for a canvas.** Found at step 18: only an entry
   had `focus()`, so the matrix's cell cursor could not have the keys.
   *Built (2026-11-18): `c->focus()` of a `tk::Canvas`; the matrix takes
   the focus when clicked and when its tab is chosen, and binds its keys
   on the canvas.*
11. **No dashed lines.** Found at step 20: the schematic draws normalled
   connections dashed and internal links dotted. *Built (2026-11-18):
   `dash` for canvas lines and the outlines of shapes (a vector of
   lengths, `[6, 4]`; `[]` is solid).*

### 13.4 The parts

```
examples/lmw/
    lmw.kkl          the module lmw: no GUI; finding, connecting, the model
    lmwgui.kkl       the program: windows, tabs, panels
    lmwtheme.kkl     the GUI's colours and contrast rules (no tk)
    fakesynth.kkl    a 9P server in Kukel that behaves as the synth, for tests
    lmwctl.kkl       a command-line client, for the tests and by hand
    lmwctl.tests     its tests, against fakesynth.kkl
    contrast.kkl     the colour pairs of a contrast preset, checked
    contrast.tests   its tests, for every preset
    lmwschematic.kkl the Schematic tab's geometry, as the synth's (no tk)
    schematic.kkl    prints it: ports, layouts, hover texts, the cursor
    schematic.tests  its tests
```

The module `lmw` (no `tk`, so it runs and is tested anywhere):

- `lmw::sockets()`: the synth sockets, newest first;
  `lmw::connect([address])`: the newest one that answers, or the address
  given (`unix:///...`, `tcp://host:port`, or the synth's own form
  `tcp!host!port`, which is translated).
- `lmw::Synth`: `params()` (from `/info`), `get(id)`, `set(id, value)`,
  `cables()` and `setcables(input, pins)` (pins as a vector of
  `[output, level]`), `ports()`, `patch()` and `load(text)`, `notes()` and
  `setnotes(text)`, `changes()` (what differs since the last call: read
  `/patch`, or `/serial` first when there is one).
- The text forms of the synth (pins `out@0.5+out`, the patch file) are
  read and written here, once.

The program `lmwgui` takes `-a address`; without it, the newest synth.
With several synths running, a menu lists them to switch.

*Built (2026-11-17), step 16, as above with these changes: the tests run
a command-line client, `lmwctl.kkl` (commands `get`, `set`, `info`,
`params`, `cables`, `cable`, `clear`, `ports`, `patch`, `load`, `save`,
`notes`, `setnotes`, `changes`, `sockets`, `source`, several in one run
separated by `,`; `-fake` runs the fake synth in the program, `-fakedir`
on a Unix socket in a new directory, `-serial` and `-noinfo` change what
it serves), in `lmwctl.tests`, since a `.tests` file runs a program.
(Later, 2026-11-18: `-noserial` in place of `-serial`, since the fake
serves `/serial` by default; the commands `serial`, `on`, `off`,
`played`, and `serve seconds` with `-fake` or `-fakeat address`, a fake
at a given address, for the GUI's tests.)
`s->info()` and `s->param(id)` give `/info`; without `/info`, `params()`
lists the files in `/params` and values stay the synth's text (`+1` is an
octave, not a number). `lmw::connect` asks for 9P2000: the synth speaks
only that, and a Kukel server (the fake) would otherwise answer in
9P2000.L, whose errors are numbers. Errors are an `IOError` with the
synth's own message after what was asked (`vcf1.cutoff: bad value`,
`nope: unknown input: nope`). `lmw::parsepatch` and `lmw::writepatch`
read and write patch files as the synth's PatchFile.h does (comments,
unknown sections, a name with `#`, `[notes]` kept as it is).*

### 13.5 The windows

One window with a header (the patch name, Load, Save, Contrast, the synth
it is connected to) and tabs, as the synth's own GUI has (its Spec S6):

- **Synth**: the module panels in the synth's three rows (VCO 1-3, NOISE,
  LFO 1; MIXER, VCF 1, VCF 2; KEYS, ENV 1, ENV 2, LFO 2, VCA, OUT), each a
  frame with a title in the unit's colour (the synth's S6.9), vertical
  faders and choice boxes, a value label with its unit under each; a value
  can be typed (Enter), a double-click resets to the default. Not movable
  in the first version, and no cables drawn here: they are edited in the
  matrix and (since step 20) the schematic.
- **Effects**: the reverb's four faders.
- **Matrix**: a canvas with a row per input and a column per output (from
  `/ports`), as the synth's S6.8: a cell with a cable shows a bar filled
  to its level and the level in %; the normalled source of an input
  without cables is marked with a ring. Click toggles 0 and 100 %, dragging
  up or down sets the level, a double-click asks for it; the keyboard moves
  a cell cursor (Space toggles, + and - change by 10 %, Delete removes).
  Gate inputs are on or off. A fifth cable on an input is refused with the
  synth's message.
- **Notes**: a `tk::Text`, written to `/notes` when it loses focus or
  after a pause in typing.
- **Schematic** (a later step, F-58): the synth's S6.7 drawing on a
  canvas, with the positions from `[schematic]` in `/patch`.

*The Schematic tab (2026-11-18, as the user chose: drawn and cables
edited; the blocks where the synth has them; dashes in `tk`; hover texts
in the status line). The tabs are now the synth's: Synth, Effects,
Matrix, Schematic, Notes. `examples/lmw/lmwschematic.kkl` (no `tk`, tested
by `schematic.tests`) holds the synth's ui/Schematic.h: its 14 blocks on
a canvas of 136 by 72 units, the ports on their edges, `[schematic]` read
as the synth reads it (an unknown block, a bad position or a block that
does not fit gives the default drawing), the connections (a cable of an
input that has some, else its normalled source), the Bezier curves, the
internal links (key tracking while a VCF's Key is above 0, velocity to
the VCA always), the hover texts and the order of the port cursor.
`lmwgui` draws it at 6.4 pixels a unit (878 by 469): the symbols in their
unit's colour (triangles; ADSR, noise and keyboard pictures; OUT a circle
with an arrow) with their names and port names, normalled connections
dashed behind them, internal links dotted with an arrow, cables black
with a white edge on top with their level below 100 %, a legend. Editing
as the synth's (its S6.6 rules): a drag from a port to a port of the
other kind puts a cable in; a drag from a plugged input takes its newest
cable, to another input (keeping its level) or out; Escape cancels;
Left and Right move a port cursor, Down and Up go to the next or previous
block; Enter, F10 or a right click opens the port's menu (Connect from or
to, ticked where there is a cable; taking cables out; only what can be
done); Delete or BackSpace takes an input's cables out. Writes go as the
matrix's (an input's whole pins, at most every 20 ms); a fifth cable is
the synth's to refuse. Moving blocks waits for a `/schematic` file in the
synth: writing `/patch` would rewrite everything else too. Found on the
way: menus had Tk's grey (#d9d9d9) whatever the palette; `tk::setcolors`
now colours them too (background, text, the entry under the mouse, a
tick), so at Max a menu is black on white. Not drawn: the mouse pointer
over a block or port (the synth's hand and crosshair), and the tooltip
window (the text is in the status line).*

Load and Save use the file dialogs: Load writes the file to `/patch`
(the synth checks it and says what is wrong), Save writes what `/patch`
gives. The status line shows the synth's errors and `busy`.

*Built (2026-11-18), step 17: `examples/lmw/lmwgui.kkl` (`-a address`,
`-fake`, `-config file`, `-v`) with the Synth and Effects tabs. The header
has the patch name, the contrast (a list of the presets and an entry for a
custom ratio), the synth's address and "Connect again"; Load and Save come
with step 19. The panels and their short labels are the synth's
(PluginEditor.cpp); a fader is a `tk::Scale` of 1000 steps that the GUI
maps to the value, with JUCE's skew for a parameter with a `centre`, the
value put on its `step` and an int rounded; the value under it is an entry
in the synth's own text (ParamText.h: "800 Hz", "18.0 kHz", "-1.9 dB",
"200 ms", "70 %", "+1.0 oct") and takes typed values as the synth reads
them ("2k", "250 ms", "-6 dB"). A choice is a drop-down list.*

*Built (2026-11-18), step 18: the Matrix tab, as the synth's
MatrixView.cpp: a canvas with the inputs and outputs of `/ports`, their
labels on the units' colours, thicker lines between modules, a cable as
its level in % over a bar filled to it, the normalled source of an input
without cables as a ring, and gates marked "(on/off)". Click, drag (1 %
per 1.5 pixels, up is more), double click (a small window asks for the
level in %), and the keys: arrows, Space or Enter, + or = and - or _ (10
%), Page Up and Page Down (1 %), Home (100 %), Delete, Backspace or End.
The status line says what the cell under the mouse is and what can be
done with it. A cable change is written as the input's whole pins
(`setcables`), at most every 20 ms like a fader, and kept in step by the
same poll; what the synth refuses (a fifth cable) goes to the status line,
and the input's cells show what the synth has again. `-tab name` starts on
a tab. Without a window manager (Xvfb) the level window gets no focus by
itself; on a desktop it does.*

*Built (2026-11-18), step 19: Load and Save are buttons in the header
and Ctrl+O and Ctrl+S (Ctrl+Q quits), through `tk::openfile` and
`tk::savefile` ("Load a patch", "Save the patch"; `.lmwpatch` files, the
patch's name as the file's); pending fader and cable writes and the notes
go out first. A patch the synth refuses gives its message in the status
line. The Notes tab is a `tk::Text`, written a second after typing stops
and when it loses the keyboard, only when it differs from the synth's;
the poll shows notes written elsewhere unless the user has changes not
yet written (those win). The list in the header ("Synth:") has the
running synths, from `lmw::sockets()` every 2 seconds, a Unix socket
shown by its name (`4711-1`); choosing one connects to it (the GUI stays
with the one it has when the other does not answer) and shows what it
has. "Connect again" does the same with the synth that was lost. The header
starts with the synth (its list and "Connect again"), then the patch
(name, Load, Save), then the contrast: the window was about 1400 pixels
wide, and CI's 1024 pixel screen (Xvfb) cut off what stood at the right,
where the list was at first. The GUI tests now fail with that message
when a point they click is off the screen.*

*Made to fit a 1024 by 768 screen (2026-11-18): the controls' labels,
values and lists in an 8 point font, value boxes of 7 characters, faders
of 96 pixels, less padding, matrix rows of 22 pixels. The window is
940x674 with Noto Sans and 989x659 with DejaVu Sans (wider; GitHub's
CI), so a title bar and a desktop's panel fit too; `TestLmwgui` fails
when it is more than 1024x768.*

*The on-screen keyboard (2026-11-18, as the user chose): a strip of 88
keys (A0 to C8, as the synth's) under the tabs, on every tab, played
through `/keys`. A click plays a note, soft near the top of a key and
loud near the bottom (velocity 1 to 127, as JUCE's keyboard); dragging
plays the keys passed over. While the strip has the keyboard (a click
gives it; a ring shows it) the computer keys a w s e d f t g y h u j k o
l p ; play from C4 up at velocity 100, and z and x shift them an octave
(C1 to C6). X repeats a held key as a release and a press: a release
waits 40 ms, and a press of the same key cancels it. Losing the
keyboard, quitting or going to another synth lets go of every note the
GUI plays. A pressed key has the synth's pressed colours (its Theme.h:
`#767676` raised to the ratio against white or black keys, the C's name
on it in black or white), three more pairs in `lmwtheme`'s table. Notes
played elsewhere (MIDI, the synth's own keyboard) are not shown: the
synth does not serve them. The window is 940x726 with Noto Sans and
989x711 with DejaVu Sans (measured again at the crosscheck after step 20:
the Schematic tab makes it no larger).*

### 13.6 Keeping in step

- A fader that moves writes its parameter at most once every 20 ms, the
  last value winning (F-62): a drag gives tens of writes a second, not
  hundreds.
- Every 200 ms the GUI reads `/patch` (F-57) and updates what differs;
  a fader the user holds is left alone until shortly after it is let go,
  so the poll does not pull it back. What the synth reads back is what
  the GUI shows: a clipped value jumps to its clipped place.
- Both GUIs may be open at once; the last write wins, and each sees the
  other's within a poll.
- A lost connection (the synth closed) greys the window and offers to
  connect again; nothing is lost on the synth's side.

*Built (2026-11-18) as above. The writes of all faders go out together:
the first at once, then at most every 20 ms, each fader's last value. A
fader is left alone by the poll while the mouse button is down on it,
within half a second of the user's last change, and while its write is
still to go; a change the poll skipped is shown once the fader is free. The
poll asks `changes()` of `lmw` (`/serial` first). Greyed means every
control disabled, and the status line says why.*
*After the crosscheck (2026-11-18): greyed is also the notes, Load and
Save, and the matrix (drawn in the colour of disabled text); the list of
synths stays usable, as a way to another synth. Notes typed but not yet
written when the connection went are kept, and written when "Connect
again" has the same synth back (before, they were dropped).*

### 13.7 Colours

As in the synth's own GUI (its S6.2 and S6.3): a white
background with black text, every pair at least 4.5:1; presets AA
(4.5:1), AAA (7:1), Max (21:1) and a custom ratio (4.5 to 21), chosen in
the header and kept in a TOML file (`lmw-gui.toml` in the user's
configuration directory, F-63). The unit colours are light tints raised
to the chosen ratio against the black text, white at Max; the pairs are
listed in one table that a test checks for every preset (as the synth's
Theme.h).

*Built (2026-11-18): the module `examples/lmw/lmwtheme.kkl` (no `tk`) has
the synth's unit colours and contrast rules (Contrast.cpp:
`enforce` mixes a colour towards black or white until it has the ratio)
and gives, for a ratio, the palette for `tk::setcolors`, the unit
colours, and every pair the GUI draws: text, selected text, disabled text,
outlines, the faders' groove, a button under the mouse, and each unit's
title. `contrast.kkl` prints them for a preset and fails when a pair is
below it; `contrast.tests` runs AA, AAA, Max and custom ratios. The file is
`lmw-gui.toml` in `$XDG_CONFIG_HOME`, else `%APPDATA%`, else
`~/.config`: `[contrast]` with `preset` and `ratio`; it is written when
the user changes the contrast.*

### 13.8 How it would be tested

- `lmw.kkl` against `fakesynth.kkl`: a `p9::Tree` with the synth's files,
  values, error messages and the text forms, served on a Unix socket in
  the test (a Kukel tree applies a write on close, the synth at once; for
  the tests that is the same).
- The fake against the real one: the same tests against a synth under
  `xvfb-run`, by hand (not in CI, since kukel's CI does not build the
  synth); there is no script for it.
  *Done by hand (2026-11-17) with `lmwctl -a` against a synth under
  `xvfb-run`: values, choices by prefix and index, clipping, step, the
  error messages, cables, ports, `changes()` and the patch-file errors
  agree. Found: a synth without `/info` gives `+1` for an octave, which
  `lmw` first read as the number 1 (now it keeps the text); 800 written
  reads back as 800.0001 (the synth's float round trip: for the synth);
  the synth binary at hand was older than its patch-file code (it refused
  a `[schematic]` section), so the fake follows the source.*
  *Again (2026-11-18), with `/info`, `/serial`, `/keys` and the exact
  float read-back in the synth: the cases of `lmwctl.tests` run with `-a`
  against the synth give what they give against the fake, `/info`
  included. The one difference left is by design: the synth stores a
  32-bit float of a fader's position, so 1234.5678 reads back as
  1234.5673 and 20.000001 as 20.000002 there (800, 0.25 and 99.5 as
  written), where the fake keeps the value as given and writes the
  shortest text for it.*
- The GUI as `notes.kkl` is tested now: under Xvfb with xdotool in
  `make test-tk`, against the fake synth.
  *Done (2026-11-18): `internal/modules/tkmod/lmwgui_test.go` runs
  `lmwctl -fake serve 30` (a fake synth in a process of its own, a
  command added for this) and `lmwgui -a ... -v` against it. With `-v` the
  GUI prints what it shows and writes, and where each fader is in the
  window (from `<Configure>` of its frames), so the test drags the cutoff
  with xdotool wherever the fonts put it: at least one write and no more
  than the moves, the synth has the last; another client's changes show; the
  contrast comes from the settings file; killing the fake greys the
  window. The tk test harness now loads the modules next to a program.*
  *And the Matrix tab (2026-11-18), `TestLmwguiMatrix`: with `-v` the GUI
  prints the matrix's place and cell sizes; a click puts a cable in, a
  drag sets it to 80 %, Right and Space put in a second, minus lowers it
  to 90 %, a fifth cable on an input is refused with the synth's message,
  another client's cable shows, and the synth has the cables the GUI
  wrote.*
  *And step 19, `TestLmwguiFiles`: two fakes on Unix sockets in
  `$XDG_RUNTIME_DIR/lmw-synth` (`lmwctl -fakeat address`, added for
  this): the list has both, newest first; typed notes are written, notes
  written elsewhere show; Ctrl+S saves the synth's patch, Ctrl+O loads a
  patch file (its cutoff shows) and a bad one is refused; choosing the
  other synth in the list connects to it.*
  *And the keyboard, `TestLmwguiKeyboard`: a click low on C4 plays it
  loud, dragging up to D4 plays that softer, a and e held play C4 and
  D#4 once each, x and a play C5.*
  *And the schematic, `TestLmwguiSchematic` (with `-v` the GUI prints
  where it draws each port): dragging lfo1.out to vcf1.cutoff writes the
  cable, dragging it on to vcf2.cutoff moves it, a fifth cable on vcf2.in
  is refused, dragging a cable away takes it out; Enter on vca.mod, Down,
  Right and Enter tick its first source, Delete takes it out; a cable put
  in by another client shows; a patch that moves VCF 1 to the top moves
  its ports, and a drag to them writes the cable. `schematic.tests`: the
  ports at the default drawing, `[schematic]` read as the synth reads it,
  the hover texts, connections and links, the port cursor.*
- The contrast table for every preset. *Done (2026-11-18):
  `contrast.tests`.*
- *`lmwgui` against the real synth (2026-11-18, by hand, the synth
  built that day, under Xvfb with its own home and socket directory):
  the panels and the matrix come from its `/info` and `/ports` as from
  the fake; a dragged cutoff wrote 10 values and the synth reads back the
  last exactly (216.8103); "-6 dB" typed under the volume gave 0.5011872;
  changes made with `lmwctl` show (a value, a choice, a cable, the
  notes); a matrix click puts a cable in; typed notes reach the synth; a
  saved patch is byte for byte what `/patch` gives. Stopped with
  SIGTERM, the synth removes its socket and the GUI greys; a new synth
  appears first in the list, and choosing it connects and shows all 51
  parameters (the dead one leaves the list). Nothing differed from the
  fake.*
- *The on-screen keyboard against the real synth (2026-11-18, by hand,
  the same way; the synth's own keyboard shows the notes it is given):
  a click on C4 lights the synth's C4 until the button is let go; a
  and e held for 1.5 s light C4 and D#4 and send each note once (X's key
  repeat filtered); x then a lights C5; a drag from C4 to C5 lights each
  key in turn; a held key goes dark at once when the focus moves to a
  value box, and a held click when Ctrl+Q quits. Nothing differed from
  the fake. Without a window manager the X focus stays with the window
  it was last given to: the typed keys went to the synth's window (which
  plays them itself) until the GUI was given the focus.*
- *The Schematic tab against the real synth (2026-11-18, by hand, the
  same way, the synth's own Schematic tab open beside it): both draw the
  same blocks, connections, links and legend (the synth draws an output
  as a plain black dot, and words its legend more briefly). Cables
  dragged in `lmwgui` (in, a second on the same input, moved to another
  input, feedback from VCF 2 into its own cutoff, out) show the same
  in the synth's tab, and its `/cables` agrees. A block moved and a cable
  put in on the synth's tab show in `lmwgui` within a poll: the synth
  writes `[schematic]` with every block once one has moved (`lfo2 = 74,
  46`), as `lmwschematic` reads it. Key tracking set to 30 % shows its
  link. A socket left behind in this trial and the keyboard's came from
  the trial script, not the synth: `cd dir && nohup synth &` puts the list
  in a subshell, so the SIGTERM went to that shell and the synth ended
  only with its display; signalled itself, it removes its socket within
  20 ms, also after its window was used.*

Steps (in §8): 15. `tk::Notebook` (13.3); 16. the module `lmw` and the
fake synth (13.4); 17. the Synth and Effects tabs, keeping in step,
colours (13.5 to 13.7); 18. the Matrix tab; 19. Load, Save, Notes,
choosing a synth; 20. the Schematic tab. In the synth, before step 17:
`/info`; later `/serial`, `/keys`, telemetry (`/info`, `/serial` and
`/keys` are served since 2026-11-18; telemetry is not yet). After them:
an on-screen keyboard (built 2026-11-18), meters, and moving the
schematic's blocks once the synth serves `/schematic` (the layout alone).

### 13.9 Open questions

- **F-55** Where it lives: `examples/lmw/` in this repository (proposed),
  or a repository of its own?
  *Answered (2026-11-17): `examples/lmw/`.*
- **F-56** The parameter description: `/info` in the synth before the
  GUI's panels are built (proposed; the synth stays the single source of
  truth), or a table in Kukel for now? Its form: JSON, one object per
  parameter in the synth's order: `{"id": "vcf1.cutoff", "kind": "float",
  "min": 20, "max": 18000, "default": 18000, "unit": "Hz", "scale":
  "log"}`, a choice with `"choices": ["Lowpass", ...]` (proposed; `json`
  reads it), or lines of text?
  *Answered (2026-11-17): `/info` in the synth, as JSON, one object per parameter.*
- **F-57** Keeping in step: poll `/patch` every 200 ms (proposed), with
  `/serial` in the synth later; or a file whose read waits for the next
  change (one request open per client, harder in the synth)?
  *Answered (2026-11-17): poll `/patch` every 200 ms; `/serial` later.*
- **F-58** The first version: steps 15 to 19 without the schematic
  (proposed), or with it?
  *Answered (2026-11-17): steps 15 to 19 without the schematic.*
- **F-59** Notes: a `/keys` file in the synth and an on-screen keyboard
  (proposed for later), or MIDI only?
  *Answered (2026-11-17): `/keys` and an on-screen keyboard later.*
- **F-60** `tk::Notebook` as in 13.3 (proposed), or tabs made of buttons
  and frames in the program (which needs a way to hide a frame)?
  *Answered (2026-11-17): `tk::Notebook`; built.*
- **F-61** 9P calls on the event loop (proposed for the first version),
  or in a worker so that a `busy` synth does not freeze the window?
  *Answered (2026-11-17): plain calls on the event loop first.*
- **F-62** At most one write per parameter every 20 ms while a fader
  moves, the last value winning (proposed)?
  *Answered (2026-11-17): at most one write every 20 ms, the last value winning.*
- **F-63** The GUI's own configuration file, TOML (proposed), or the
  contrast settings shared with the synth's `config.ini` (one setting for
  both GUIs, but a file another program writes)?
  *Answered (2026-11-17): the GUI's own TOML file.*

---

## 14. Decimals: a type `decimal` in the language

*A proposal (2026-11-19, TODO.md Q117); not built. F-32 (section 10.9)
chose a decimal type in the language; the literal (`12.50d`), keeping the
scale, refusing floats in arithmetic, and the number of digits were
chosen the same day (14.2, 14.3). The open questions are in 14.10.*

Prices and amounts are `decimal` in the designer (precision 12, scale 2:
section 10.7), in SQL (`NUMERIC`, `DECIMAL`) and in most business data.
Kukel has `int` and `float`, and a float cannot hold 0.10 exactly:
`0.1 + 0.2` is `0.30000000000000004`, and a sum of many prices drifts. A
decimal is a number in base 10, exact for `+`, `-` and `*`, that keeps
the number of digits after its point:

```
local price = 12.50d;
local total = price * 3;         // 37.50
local vat = round(total * 0.21d, 2); // 7.88 (7.8750 rounded)
println(total + vat);            // 45.38
```

The model is SQL's `NUMERIC` and Python's `decimal` module, without
Python's contexts: one rule for every program, so that a function behaves
the same wherever it is called.

### 14.1 What it is not

- Not a float in base 10 (IEEE decimal128): `+`, `-` and `*` never round.
  Only `/` and the functions that say so round.
- Not money: no currency, no formatting with thousands separators or a
  locale. Those belong in a module (or Kukel-MVC's view) on top.
- Not a change to `int` or `float`: `1.5` stays a float, `7 / 2` stays 3.

### 14.2 The value

- **Literals**: a decimal-form number with the suffix `d`: `12.50d`, `3d`,
  `0.5d`, `.5d`, `1_000.00d`, `1.5e3d` (1500), `2.5e-3d` (0.0025). Only
  base 10: `0x12d` stays a hex int. The scale of a literal is the number
  of digits after its point, less the exponent (`1.5e3d` has scale 0,
  `1.50d` scale 2); a negative scale becomes 0 (`1e3d` is `1000`).
  `-12.50d` is the unary minus on `12.50d`, as for ints.
- **`typeof`** gives `"decimal"`.
- **The scale is kept**: `12.50d` prints as `12.50`, and the scale of a
  result follows SQL: `a + b`, `a - b` and `a % b` have the larger scale,
  `a * b` the sum of the scales, an int counts as scale 0.
  `2.50d * 1.10d` is `2.7500`; `round` (14.4) brings it back.
- **Equality ignores the scale**: `12.50d == 12.5d` is true, and they are
  the same map key; `2d == 2` is true and `2d` and `2` are the same key,
  as `1` and `1.0` are today. (With floats: F-65.)
- **Truth**: a zero of any scale (`0d`, `0.00d`) is false, like `0` and
  `0.0`.
- **Size**: at most 1000 significant digits and a scale of at most 1000
  (F-69). A result beyond is a `RangeError`, so that `x * x` in a loop
  cannot fill memory; repeated `*` without `round` grows the scale until
  it is reached.

### 14.3 Arithmetic

| | Result |
|---|---|
| `d + d`, `d - d`, `d * d`, `d % d` | exact decimal (scale as in 14.2); `%` has the sign of the left side, as for ints |
| `d / d` | decimal: the exact quotient when it has at most 34 significant digits, else rounded to 34, halves to even; its scale the scale of `a` less that of `b` when that is enough (`7.50d / 2` is `3.75`, `10.00d / 4` is `2.50`), else as many as the quotient needs (`1d / 3d` is `0.3333333333333333333333333333333333`) |
| a decimal and an int, either side | the int is a decimal of scale 0 first: exact |
| a decimal and a float in `+ - * / %` | `TypeError` ("decimal * float: write the float as a decimal (1.1d) or convert it with decimal(x)") |
| `d / 0`, `d % 0` (any zero) | `ZeroDivisionError`, as for ints |
| `-d`, `d++`, `d--`, `+=` and the others | as for ints; `d++` adds 1 and keeps the scale |
| `<`, `<=`, `>`, `>=` | with decimals and ints: exact. With floats: F-65 |
| bitwise operators, `<<`, `>>`, `~` | `TypeError`, as for floats |

`int / int` is unchanged (it truncates). A constant may be a decimal
(`const VAT = 0.21d;`); the compiler folds decimal constants with the same
code the VM uses, as it does for ints and floats.

### 14.4 Built-in functions

| Function | With decimals |
|---|---|
| `decimal(x [, scale])` | New (F-70). A decimal from an int (scale 0), a decimal, a string (`"12.50"`, `" -1_000.5 "`, `"1.5e3"`; the rules of `float`'s strings, without `inf`, `nan` and hex) or a float, which becomes the decimal of its shortest text (`decimal(0.1)` is `0.1`, not `0.1000000000000000055511151231257827`). With `scale`, rounded to it as `round` does. A string that is not a number is a `ValueError` |
| `int(d)` | truncated toward zero, as for floats; `RangeError` if it does not fit |
| `float(d)` | the nearest float |
| `str(d)` | `"12.50"`: digits, never an exponent |
| `round(d)`, `floor(d)`, `ceil(d)` | an int, as for floats (`round`: halves away from zero) |
| `round(x, n)` | New optional argument: `x` rounded to `n` digits after the point (0..1000), halves away from zero (F-64); a decimal for a decimal or int, a float for a float (`round(2.675, 2)`, with the float's own inexactness) |
| `abs(d)`, `min`, `max` | as for other numbers; `min` and `max` mix decimals and ints |
| `pow(d, n)` | exact for an int `n >= 0` (scale times `n`); another `n` is a `TypeError` |
| `sqrt`, `exp`, `log` | `TypeError`: they give floats; convert with `float(d)` first |
| `typeof(d)` | `"decimal"` |

### 14.5 Printing and reading back

- `print` and `str`: `12.50`.
- Inside vectors, maps and objects, where strings are quoted so that the
  text reads back: `[12.50d, 1.5]` (F-66), so that `12.50d` and the float
  `12.5` look different.
- `kukel fmt` keeps a literal as written; the REPL and the debuggers show
  values as inside a vector (`12.50d`).

### 14.6 Modules

- **`json`**: `json::encode` writes a decimal as a JSON number with its
  digits (`12.50`); JSON has no decimal type, so it reads back as a float
  unless asked: `json::decode(s, {"decimals": true})` makes every number
  with a fraction or exponent a decimal, exact (F-68).
- **`db`**: a decimal is passed to the database as its text, which
  `NUMERIC` and `DECIMAL` columns take exactly (SQLite stores up to 15
  digits as a REAL or INTEGER and longer text as TEXT, without loss). Read
  back, a column declared `NUMERIC(p, s)` or `DECIMAL(p, s)` gives a
  decimal of scale `s`, once a connection asks for it:
  `db::open(url, {"decimals": true})` (F-68). Today such columns give ints
  or floats, and v1 keeps that. The ORM's models follow their
  connection.
- **`toml`**: TOML has no decimals; `toml::encode` writes one as a float's
  text, and `decode` is unchanged.
- **The Go package**: `kukel.Decimal(text)` makes a decimal (an error
  for text that is not a number), `v.AsDecimal()` gives its text with an
  `ok`, `v.Kind()` is `"decimal"`; `FromGo` is unchanged and `ToGo` gives
  the text, as a `string`.

### 14.7 How it would be built

- **The value**: a new `value.Kind`, `Decimal`. A decimal whose
  coefficient fits in 63 bits (every price, any `DECIMAL(18, s)`) keeps it
  in `num` and its scale as a pointer into a fixed table of scales in
  `obj`, so it does not allocate, as an int does not; a larger one keeps a
  `*big.Int` and its scale in `obj`. The arithmetic is written on
  `math/big` in `internal/value` (about 600 lines with tests): Go has no
  decimal type, and `shopspring/decimal` and `cockroachdb/apd` would each
  bring rules (contexts, a fixed division precision) to adapt rather than
  use.
- **The lexer and parser**: the suffix `d` on a decimal-form number; a
  new token kind `DECIMAL`. **Syntax version 12**: programs of version 11
  are unchanged, since `12.50d` was an error before.
- **The bytecode**: decimal constants in the constant pool, so **bytecode
  format 13**; `.kkc` files are built again. The instructions stay: the
  arithmetic instructions dispatch on kinds already, and the fused int
  forms (ARITH_LK, CMP_JUMP_LK, ...) fall back to the general path for
  any other kind, so ints and floats run as fast as before (measured).
- **The rest**: `typeof`, equality and map hashing (a decimal hashes as
  the int or float it equals, if one does), printing, `kukel fmt`, the
  debuggers' display, syntax.ebnf, syntax.lark, the VS Code grammar and
  Geany's notes, the reference (§2, §3, §6, the built-ins, `json`, `db`),
  the booklet (chapter 2 and a page on money), docs/compatibility.md and
  vm.md §7.

### 14.8 Steps

1. The value, its arithmetic and printing in `internal/value`, with the
   conversions; unit tests and a fuzz test against `math/big.Rat`.
2. The literal (syntax version 12), constants, the VM and bytecode format
   13; `typeof`, `decimal()`, `round(x, n)` and the other built-ins;
   tests/run programs.
3. `json` and `db` (the options of F-68), the Go package.
4. The documents, the booklet, the editors' grammars; an example
   (`examples/invoice.kkl`: lines, VAT, totals, written to SQLite and
   JSON).

Each step is a branch of its own, merged when its tests pass.

### 14.9 How it would be tested

- **Arithmetic**: tables of cases for every operator and scale rule
  (SQL's and Python's documented examples among them), the division rule
  (exact, rounded, the preferred scale), halves in `round` and `/`, the
  1000-digit limit; a fuzz test that compares with `math/big.Rat` for
  `+ - * %` and checks that `/` is within half a unit of its last digit.
- **Equality and keys**: `12.50d == 12.5d`, `2d == 2`, one map key for
  each; hashing agrees with equality (the existing map fuzz test gains
  decimals).
- **The literal**: lexer tables (suffixes, `_`, exponents, `0x12d`), the
  parser's errors, `kukel fmt` round trips, `FuzzLexer` seeds.
- **Speed**: the existing benchmarks of ints and floats before and after,
  and a benchmark of a sum of 10⁶ prices (small form: no allocation).
- **Modules**: `json` and `db` round trips (SQLite in the tests;
  PostgreSQL and MySQL where CI has them), with and without the options.
- **Examples**: the invoice example's `.tests`.

### 14.10 Open questions

- **F-64** `round(x, n)` halves away from zero, as `round(x)` does and
  invoices usually do (proposed), or to even (banker's rounding, as `/`
  does)? A third argument (`round(x, 2, "even")`) could give the other.
- **F-65** A decimal and a float in `==` and `<`: compared exactly
  (proposed: comparing loses nothing, `price > 0.0` works, and `2d == 2.0`
  is true as `2 == 2.0` is), or a `TypeError` as in arithmetic?
- **F-66** A decimal inside a vector or map prints as `12.50d` (proposed,
  so that it reads back and differs from a float), or as `12.50`?
- **F-67** `/` rounds to 34 significant digits (proposed, as decimal128),
  or to 28 (Python's default), or to a fixed scale (SQL Server's 6)?
- **F-68** `json` and `db` give decimals only when asked, per call or
  connection (proposed, so that v1 programs read what they read today),
  or always for `NUMERIC` and `DECIMAL` columns (a v2 change)?
- **F-69** The limit: 1000 significant digits and scale 1000 (proposed),
  or SQL's 38 digits, which every database column fits?
- **F-70** Conversion by a new built-in `decimal(x)` (proposed; a
  program with a top-level name `decimal` must rename it, as with any new
  built-in, docs/compatibility.md), or by a module (`use decimal;
  decimal::of(x)`), which no program's names can clash with?
