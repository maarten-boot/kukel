//go:build !nosqlite

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
true nil 0
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
    println(conn->find(Note)->first()->seen, conn->find(Note)->limit(0)->first(), len(conn->find(Note)->limit(0)->all()));
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

// Relations: include() asks in parts of 500 values; relations() is checked.
func TestRelations(t *testing.T) {
	got := run(t, `use db;
class Box { id, items; static relations(); }
Box::relations() { return {"items": db::many(Item, "boxid")}; }
class Item { id, boxid, box; static relations(); }
Item::relations() { return {"box": db::one(Box, "boxid")}; }
class NotMember { id; static relations(); }
NotMember::relations() { return {"x": db::many(Item, "boxid")}; }
class NoKey { id, items; static relations(); }
NoKey::relations() { return {"items": db::many(Item, "nokey")}; }
class Typed { id, items; static relations(); static columns(); }
Typed::relations() { return {"items": db::many(Item, "boxid")}; }
Typed::columns() { return {"items": "TEXT"}; }
class NotRel { id, items; static relations(); }
NotRel::relations() { return {"items": [Item, "boxid"]}; }
main() {
    local conn = db::open("sqlite::memory:");
    conn->migrate(Box);
    conn->migrate(Item);
    println(conn->query("SELECT name FROM pragma_table_info('box')"));
    conn->begin();
    for (local i = 0; i < 1200; i++) {
        local b = conn->save(new Box());
        for (local j = 0; j < i % 3; j++) {
            local it = new Item();
            it->boxid = b->id;
            conn->save(it);
        }
    }
    conn->commit();
    local total = 0, boxes = conn->find(Box)->include("items")->all();
    for (local b in boxes) total += len(b->items);
    local items = conn->find(Item)->include("box")->all(), right = 0;
    for (local it in items) if (it->box->id == it->boxid) right++;
    println(len(boxes), total, len(items), right);
    for (local c in [NotMember, NoKey, Typed, NotRel])
        try { conn->migrate(c); } catch (e) { println(e->kind + ":", e->message); }
}`)
	want := `[{"name": "id"}]
1200 1200 1200 1200
ValueError: db::Conn::migrate: NotMember::relations() names x, which is not a data member of NotMember
ValueError: db::Conn::migrate: NoKey::relations(): items: Item has no data member nokey for the key (on the other side)
ValueError: db::Conn::migrate: Typed::relations(): items is a column (the key, or named in columns()), not a relation
TypeError: db::Conn::migrate: NotRel::relations() must map names to db::many(...) or db::one(...): "items": [<class Item>, "boxid"]
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Fixes of cross-check 5: relation keys match by value (a TEXT key column),
// a class derived from a Go class cannot be stored, lastid() is nil after an
// UPDATE.
func TestCrossCheck5(t *testing.T) {
	got := run(t, `use db;
use time;
class Box { id, items; static relations(); }
Box::relations() { return {"items": db::many(Item, "boxid")}; }
class Item { id, boxid, box; static relations(); static columns(); }
Item::relations() { return {"box": db::one(Box, "boxid")}; }
Item::columns() { return {"boxid": "TEXT"}; }
class Note : time::Time { id; }
main() {
    local conn = db::open("sqlite::memory:");
    conn->migrate(Box);
    conn->migrate(Item);
    local b = conn->save(new Box()), it = new Item();
    it->boxid = str(b->id);
    conn->save(it);
    println(len(conn->load(b, "items")->items), conn->load(conn->get(Item, 1), "box")->box->id);
    try { conn->migrate(Note); } catch (e) { println(e->kind + ":", e->message); }
    conn->exec("CREATE TABLE t (a)");
    conn->exec("INSERT INTO t VALUES (1)");
    println(conn->lastid());
    conn->exec("UPDATE t SET a = 2");
    println(conn->lastid());
    conn->exec("/* x */ insert into t values (3)");
    println(conn->lastid());
}`)
	want := `1 1
TypeError: db::Conn::migrate: Note derives from time::Time, a class written in Go: its Go part cannot be stored, so the class cannot be
1
nil
2
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// lastid() is nil after an INSERT that adds no row, after migrate of a
// complete table and after delete(p) of an object without an id; after
// save of a row already there, it is that row's id.
func TestLastIDCrossCheck9(t *testing.T) {
	got := run(t, `use db;
class Pen { id, name; }
main() {
    local conn = db::open("sqlite::memory:");
    conn->exec("CREATE TABLE t (id INTEGER PRIMARY KEY, a UNIQUE)");
    local statements = ["INSERT INTO t (a) VALUES (1)", "INSERT OR IGNORE INTO t (a) VALUES (1)",
                        "INSERT INTO t (a) VALUES (2)", "INSERT INTO t (a) VALUES (2) ON CONFLICT DO NOTHING",
                        "INSERT INTO t (a) VALUES (3)", "INSERT INTO t (a) SELECT a FROM t WHERE a > 9"];
    for (local s in statements) println(conn->exec(s), conn->lastid());
    conn->migrate(Pen);
    local p = conn->save(new Pen());
    println(conn->lastid());
    conn->migrate(Pen);
    println(conn->lastid());
    conn->save(new Pen());
    p->name = "red";
    conn->save(p);
    println(conn->lastid());
    conn->save(new Pen());
    println(conn->delete(new Pen()), conn->lastid());
}`)
	want := `1 1
0 nil
1 2
0 nil
1 3
0 nil
1
nil
1
false nil
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
