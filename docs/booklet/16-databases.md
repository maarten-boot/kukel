# 16. Databases

[The booklet](README.md) · previous: [15. Time and identifiers](15-time.md) · next: [17. Events and workers](17-events.md)

The module `db` stores data in SQL databases: SQLite, a whole database in
one file with nothing to install, and the PostgreSQL and MySQL (or
MariaDB) servers. kukel carries all three drivers. A program works the
same with each: the same member functions, `?` for values everywhere, and
the same values back.

This chapter assumes a little SQL: `CREATE TABLE`, `INSERT`, `SELECT`.
Further on, Kukel objects are stored without writing SQL at all.

## Connecting and asking

```
use db;

main() {
    with (local conn = db::open("sqlite::memory:")) {
        conn->exec("CREATE TABLE books (title TEXT, author TEXT, year INTEGER)");

        for (local b in [["Dune", "Herbert", 1965], ["Emma", "Austen", 1815], ["Ubik", "Dick", 1969]]) {
            conn->exec("INSERT INTO books VALUES (?, ?, ?)", b..);
        }

        for (local row in conn->query("SELECT title, year FROM books WHERE year > ? ORDER BY year", 1900)) {
            println(row["title"], row["year"]);
        }

        local oldest = conn->first("SELECT * FROM books ORDER BY year");
        println(oldest);
        println(conn->first("SELECT count(*) AS n FROM books")["n"], "books");
        println(conn->exec("UPDATE books SET year = year + 1 WHERE author = ?", "Dick"), "changed");
    }
}
```

```
$ kukel run books.kkl
Dune 1965
Ubik 1969
{"title": "Emma", "author": "Austen", "year": 1815}
3 books
1 changed
```

- `db::open(url)` connects: `"sqlite:books.db"` is a file (made if it is
  not there), `"sqlite::memory:"` a database that lives only in memory,
  and `"postgres://user:password@host/name"` or
  `"mysql://user:password@host/name"` a server.
- `conn->exec(sql, values...)` runs a statement that gives no rows, and
  says how many rows it changed.
- `conn->query(sql, values...)` gives all rows, as a vector of maps from
  column names to values; `conn->first(...)` gives the first row, or `nil`.
- `with` closes the connection at the end (chapter 6).

## Never glue values into SQL

Every `?` in the SQL takes the next value after it. The values travel
apart from the SQL text, so a value cannot change what the statement
does. Never build SQL by joining strings with values in them:

```
use db;

main() {
    with (local conn = db::open("sqlite::memory:")) {
        conn->exec("CREATE TABLE users (name TEXT)");
        conn->exec("INSERT INTO users VALUES ('ann'), ('bo')");

        local evil = "x' OR '1'='1";
        println(len(conn->query("SELECT * FROM users WHERE name = ?", evil)), "users found");
    }
}
```

```
$ kukel run safe.kkl
0 users found
```

Had the query been `"... WHERE name = '" + evil + "'"`, it would have
found every user: an *SQL injection*, one of the most common security
holes there is.

## Transactions

Several changes that must happen all together, or not at all, go in a
*transaction*:

```
use db;

transfer(conn, from, to, amount) {
    conn->begin();

    try {
        conn->exec("UPDATE accounts SET balance = balance - ? WHERE name = ?", amount, from);

        if (conn->first("SELECT balance FROM accounts WHERE name = ?", from)["balance"] < 0) {
            throw new Error("not enough money in " + from);
        }
        conn->exec("UPDATE accounts SET balance = balance + ? WHERE name = ?", amount, to);
        conn->commit();
    } catch (e) {
        conn->rollback();
        println("cancelled:", e->message);
    }
}

main() {
    with (local conn = db::open("sqlite::memory:")) {
        conn->exec("CREATE TABLE accounts (name TEXT, balance INTEGER)");
        conn->exec("INSERT INTO accounts VALUES ('ann', 100), ('bo', 20)");
        transfer(
            conn,
            "ann",
            "bo",
            30,
        );
        transfer(
            conn,
            "bo",
            "ann",
            500,
        );
        println(conn->query("SELECT * FROM accounts"));
    }
}
```

```
$ kukel run bank.kkl
cancelled: not enough money in bo
[{"name": "ann", "balance": 70}, {"name": "bo", "balance": 50}]
```

`begin()` starts it, `commit()` keeps the changes, `rollback()` undoes
everything since `begin()`. Closing a connection with an open
transaction rolls it back.

## Objects in tables

A connection can also store objects of your classes, one row each,
without SQL: the data members are the columns, `id` is the key, and the
table is the class's name in lower case.

```
use db;

class Book {
    id, title, year, read;
    static columns();
}

Book::columns() {
    return {"title": "TEXT NOT NULL", "year": "INTEGER", "read": "BOOLEAN"};
}

book(title, year) {
    local b = new Book();
    b->title = title;
    b->year = year;
    b->read = false;
    return b;
}

main() {
    with (local conn = db::open("sqlite::memory:")) {
        conn->migrate(Book);                   // makes the table
        local dune = conn->save(book("Dune", 1965));
        conn->save(book("Ubik", 1969));
        conn->save(book("Emma", 1815));
        println(dune->id);

        dune->read = true;
        conn->save(dune);                      // an update: it has an id

        for (local b in conn->find(Book)->where("year > ?", 1900)->orderby("title")->all()) {
            println(
                b->id,
                b->title,
                b->year,
                b->read,
            );
        }
        println(conn->get(Book, 3)->title, conn->find(Book)->count());
        conn->delete(dune);
        println(conn->find(Book)->count());
    }
}
```

```
$ kukel run library.kkl
1
1 Dune 1965 true
2 Ubik 1969 false
Emma 3
2
```

- `conn->migrate(Book)` makes the table if it is not there, and adds
  columns for new data members. `columns()`, a static member function,
  gives the columns their SQL types.
- `conn->save(b)` inserts an object without an id (and sets its `id`), or
  updates the row of one that has an id.
- `conn->get(Book, id)` loads one object; `conn->find(Book)` starts a
  query, refined with `where`, `orderby` and `limit`, and run with
  `all()`, `first()` or `count()`.
- `conn->delete(b)` deletes its row.

Objects can also point to each other: an order to its customer, a
customer to their orders. The [reference](../reference.md#db) shows how
(`relations()`, `load`, `include`).

## Errors

What the database refuses, such as SQL that is not valid or a broken
`UNIQUE` rule, is a `DBError` with the database's own message:

```
use db;

main() {
    with (local conn = db::open("sqlite::memory:")) {
        conn->exec("CREATE TABLE tags (name TEXT UNIQUE)");
        conn->exec("INSERT INTO tags VALUES (?)", "red");

        try {
            conn->exec("INSERT INTO tags VALUES (?)", "red");
        } catch (e) {
            println(e->kind, e->message);
        }
    }
}
```

```
$ kukel run unique.kkl
DBError db::Conn::exec: constraint failed: UNIQUE constraint failed: tags.name (2067)
```

[The booklet](README.md) · previous: [15. Time and identifiers](15-time.md) · next: [17. Events and workers](17-events.md)
