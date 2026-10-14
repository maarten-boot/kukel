package dbmod_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/builtins"
	"github.com/maarten-boot/kukel/internal/compiler"
	_ "github.com/maarten-boot/kukel/internal/modules/dbmod"
	"github.com/maarten-boot/kukel/internal/parser"
	"github.com/maarten-boot/kukel/internal/vm"
)

func run(t *testing.T, src string, args ...string) string {
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
	if _, err := m.Run(args); err != nil {
		t.Fatalf("%v\noutput: %s", err, out.String())
	}
	return out.String()
}

// Every kind of value goes in and comes back; a value that looks like SQL
// stays a value.
func TestValues(t *testing.T) {
	got := run(t, `use db; use time;
main() {
    local c = db::open("sqlite::memory:");
    c->exec("CREATE TABLE t (i INTEGER, r REAL, s TEXT, b BLOB, ok BOOLEAN, at DATETIME)");
    c->exec("INSERT INTO t VALUES (?, ?, ?, ?, ?, ?)", -9223372036854775808, 0.1, "x'); DROP TABLE t; --", "\x00\xff", false,
            new time::Time(2026, 10, 14, 9, 30, 15.5, "Europe/Amsterdam"));
    c->exec("INSERT INTO t VALUES (?, ?, ?, ?, ?, ?)", nil, nil, nil, nil, nil, nil);
    for (local row in c->query("SELECT * FROM t")) println(row);
    println(c->first("SELECT count(*) AS n, typeof(b) AS bt FROM t WHERE i IS NOT NULL"));
    c->close();
}`)
	want := `{"i": -9223372036854775808, "r": 0.1, "s": "x'); DROP TABLE t; --", "b": "\0` + "\xff" + `", "ok": false, "at": time::Time(2026-10-14T09:30:15.5+02:00)}
{"i": nil, "r": nil, "s": nil, "b": nil, "ok": nil, "at": nil}
{"n": 1, "bt": "text"}
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestTransactions(t *testing.T) {
	got := run(t, `use db;
main() {
    local c = db::open("sqlite::memory:");
    c->exec("CREATE TABLE acc (name TEXT PRIMARY KEY, money INTEGER CHECK (money >= 0))");
    c->exec("INSERT INTO acc VALUES ('a', 10), ('b', 0)");
    // a transfer that breaks the CHECK in its second statement: all of it is undone
    try {
        c->begin();
        c->exec("UPDATE acc SET money = money + 50 WHERE name = 'b'");
        c->exec("UPDATE acc SET money = money - 50 WHERE name = 'a'");
        c->commit();
    } catch (e) {
        c->rollback();
        println(e->kind);
    }
    println(c->query("SELECT money FROM acc ORDER BY name"), c->intransaction());
    c->begin();
    c->exec("UPDATE acc SET money = 5 WHERE name = 'b'");
    c->commit();
    println(c->first("SELECT money FROM acc WHERE name = 'b'")["money"]);
    try { c->begin(); c->begin(); } catch (e) { println(e->message); }
    c->close();                            // the open transaction is undone
}`)
	want := "DBError\n[{\"money\": 10}, {\"money\": 0}] false\n5\ndb::Conn::begin: a transaction is already open (transactions do not nest)\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A file database keeps its rows between connections (and programs).
func TestFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "shop.db")
	write := `use db;
main(args) {
    local c = db::open("sqlite:" + args[0]);
    c->exec("CREATE TABLE IF NOT EXISTS items (id INTEGER PRIMARY KEY, name TEXT)");
    println(c->exec("INSERT INTO items (name) VALUES (?), (?)", "pen", "ink"), c->lastid());
    c->close();
}`
	read := `use db;
main(args) {
    local c = db::open("sqlite:" + args[0]);
    for (local r = c->cursor("SELECT * FROM items ORDER BY id"), row = r->next(); row != nil; row = r->next())
        println(row["id"], row["name"]);
    c->close();
}`
	if got := run(t, write, file); got != "2 2\n" {
		t.Errorf("write: %q", got)
	}
	if got := run(t, read, file); got != "1 pen\n2 ink\n" {
		t.Errorf("read: %q", got)
	}
	// %, ? and # are ordinary characters of a file name.
	odd := filepath.Join(t.TempDir(), "a%25b?c#d.db")
	if got := run(t, write, odd); got != "2 2\n" {
		t.Errorf("write to %s: %q", odd, got)
	}
	if _, err := os.Stat(odd); err != nil {
		t.Error(err)
	}
}

func TestErrors(t *testing.T) {
	got := run(t, `use db;
main() {
    local c = db::open("sqlite::memory:");
    c->exec("CREATE TABLE p (id INTEGER PRIMARY KEY)");
    c->exec("CREATE TABLE ch (p INTEGER REFERENCES p(id))");
    local cur = c->cursor("SELECT 1 UNION SELECT 2");
    local bad = [fn() { c->exec("SELECT 1"); },                       // the cursor is open
                 fn() { cur->close(); c->exec("INSERT INTO ch VALUES (7)"); },   // foreign keys are on
                 fn() { c->query("SELECT * FROM nosuch"); },
                 fn() { c->exec("SELECT ?", {"a": 1}); },
                 fn() { c->exec(5); },
                 fn() { c->rollback(); },
                 fn() { db::open("mysql://x"); },
                 fn() { c->close(); c->query("SELECT 1"); }];
    for (local f in bad) try { f(); } catch (e) { println(e->kind + ":", e->message); }
    println(cur, c);
}`)
	want := `ValueError: db::Conn::exec: a cursor is open on this connection: read it to the end or close it first
DBError: db::Conn::exec: constraint failed: FOREIGN KEY constraint failed (787)
DBError: db::Conn::query: SQL logic error: no such table: nosuch (1)
TypeError: db::Conn::exec: argument 2 cannot be stored in a database: map (nil, booleans, numbers, strings and time::Time can)
TypeError: db::Conn::exec: argument 1 must be a string, not int
ValueError: db::Conn::rollback: there is no open transaction (begin() starts one)
ValueError: db::open: "mysql://x" is not a database URL: use "sqlite:file.db" or "sqlite::memory:"
ValueError: db::Conn::query: the connection is closed
db::Cursor(done) db::Conn(sqlite::memory:, closed)
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
