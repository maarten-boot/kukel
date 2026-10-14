package dbmod_test

import "testing"

// migrate adds the columns a table lacks; a row stored before reads with
// nil there. Mistakes in the mapping and in queries are errors.
func TestMapping(t *testing.T) {
	got := run(t, `use db;
class Note { id, text, seen; static columns(); }
Note::columns() { return {"seen": "BOOLEAN"}; }
class NoId { name; }
class Bad { id, x; static columns(); }
Bad::columns() { return {"y": "TEXT"}; }
class Odd { id; static table(); }
Odd::table() { return "drop table x"; }
class Shared { id; table(); }
Shared::table() { return "x"; }
`+mainOfMapping)
	want := `[{"name": "id", "type": "INTEGER"}, {"name": "text", "type": "TEXT"}, {"name": "seen", "type": "BOOLEAN"}]
Note{id: 1, text: "old", seen: nil}
true
ValueError: db::Conn::migrate: class NoId has no data member id (the primary key)
ValueError: db::Conn::migrate: Bad::columns() names y, which is not a data member of Bad
ValueError: db::Conn::migrate: Odd::table() must give a table name (letters, digits and _), not "drop table x"
TypeError: db::Conn::migrate: Shared::table must be a static member function, defined
TypeError: db::Conn::save: argument 1 must be an object, not int
TypeError: db::Conn::save: Note->text cannot be stored in a database: vector (nil, booleans, numbers, strings and time::Time can)
TypeError: db::Conn::find: argument 1 must be a class, not string
TypeError: db::Conn::migrate: db::Conn is a class written in Go: only Kukel classes can be stored
ValueError: db::Query::limit: a limit and an offset cannot be negative
ValueError: db::Query::delete: delete() takes the rows where() selects: orderby() and limit() do not apply
DBError: db::Query::all: SQL logic error: no such column: nosuch (1)
ValueError: db::Query::all: a cursor is open on this connection: read it to the end or close it first
ValueError: db::Query::all: the connection is closed
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

const mainOfMapping = `main() {
    local conn = db::open("sqlite::memory:");
    conn->exec("CREATE TABLE note (id INTEGER PRIMARY KEY, text TEXT)");
    conn->exec("INSERT INTO note (text) VALUES ('old')");
    conn->migrate(Note);
    println(conn->query("SELECT name, type FROM pragma_table_info('note')"));
    local n = conn->get(Note, 1);
    println(n);
    n->seen = true;
    conn->save(n);
    println(conn->find(Note)->first()->seen);
    for (local f in [fn() { conn->migrate(NoId); }, fn() { conn->migrate(Bad); }, fn() { conn->migrate(Odd); },
                     fn() { conn->migrate(Shared); },
                     fn() { conn->save(42); }, fn() { local x = new Note(); x->text = [1]; conn->save(x); },
                     fn() { conn->find("Note"); }, fn() { conn->migrate(db::Conn); }, fn() { conn->find(Note)->limit(-1); },
                     fn() { conn->find(Note)->limit(1)->delete(); }, fn() { conn->find(Note)->where("nosuch = 1")->all(); }])
        try { f(); } catch (e) { println(e->kind + ":", e->message); }
    local q = conn->find(Note);
    local c = conn->cursor("SELECT 1");
    try { q->all(); } catch (e) { println(e->kind + ":", e->message); }
    c->close();
    conn->close();
    try { q->all(); } catch (e) { println(e->kind + ":", e->message); }
}`
