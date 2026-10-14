// Package dbmod is the native module "db": SQL databases through Go's
// database/sql: SQLite (modernc.org/sqlite), PostgreSQL (jackc/pgx) and
// MySQL or MariaDB (go-sql-driver/mysql), all pure Go, so no C library is
// needed.
//
//	use db;
//	local conn = db::open("sqlite:shop.db");
//	conn->exec("CREATE TABLE IF NOT EXISTS items (name TEXT, price REAL)");
//	conn->exec("INSERT INTO items VALUES (?, ?)", "pen", 1.5);
//	for (local row in conn->query("SELECT * FROM items WHERE price < ?", 10))
//	    println(row["name"], row["price"]);
//	conn->close();
//
// Values always travel as parameters (?), never glued into the SQL text.
package dbmod

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	stdtime "time"

	"github.com/maarten-boot/kukel/internal/modules/timemod"
	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

// DBError is the kind of the exceptions the database reports: an SQL
// error, a broken constraint, a database file that cannot be opened.
const DBError = "DBError"

// conn is the state of a db::Conn object. One connection (database/sql
// with at most one open connection), so that a transaction and an
// in-memory database behave as one session.
type conn struct {
	url     string // as it prints (without a password)
	dialect dialect
	db      *sql.DB
	tx      *sql.Tx // the open transaction, or nil
	cursor  *cursor // the open cursor, or nil: it holds the connection
	lastID  value.Value
	closed  bool
}

// cursor is the state of a db::Cursor object.
type cursor struct {
	conn  *conn
	rows  *sql.Rows
	cols  []string
	types []string // the columns' declared types, in upper case
	done  bool
	stop  func() // ends the query's context (the rows close when it ends)
}

// Conn and Cursor are the classes db::Conn and db::Cursor.
var (
	Conn   = &native.Class{Name: "Conn"}
	Cursor = &native.Class{Name: "Cursor"}
)

func init() {
	Conn.Methods = map[string]value.NativeMethod{
		"exec":   connMethod("exec", 1, -1, exec),
		"lastid": connMethod("lastid", 0, 0, func(a native.Args, env value.Env, c *conn) (value.Value, error) { return c.lastID, nil }),
		"query":  connMethod("query", 1, -1, query),
		"first":  connMethod("first", 1, -1, first),
		"cursor": connMethod("cursor", 1, -1, openCursor),
		"begin":  connMethod("begin", 0, 0, begin),
		"commit": connMethod("commit", 0, 0, func(a native.Args, env value.Env, c *conn) (value.Value, error) {
			return value.NilValue, c.finish(a, true)
		}),
		"rollback": connMethod("rollback", 0, 0, func(a native.Args, env value.Env, c *conn) (value.Value, error) {
			return value.NilValue, c.finish(a, false)
		}),
		"intransaction": connMethod("intransaction", 0, 0, func(a native.Args, env value.Env, c *conn) (value.Value, error) {
			return value.MakeBool(c.tx != nil), nil
		}),
		"close": connMethod("close", 0, 0, closeConn),
	}
	Conn.Show = func(o *value.ObjectObj) string {
		c := o.State.(*conn)
		if c.closed {
			return "db::Conn(" + c.url + ", closed)"
		}
		return "db::Conn(" + c.url + ")"
	}
	Cursor.Methods = map[string]value.NativeMethod{
		"next": cursorMethod("next", next),
		"close": cursorMethod("close", func(a native.Args, env value.Env, cu *cursor) (value.Value, error) {
			cu.finish()
			return value.NilValue, nil
		}),
	}
	Cursor.Show = func(o *value.ObjectObj) string {
		if o.State.(*cursor).done {
			return "db::Cursor(done)"
		}
		return "db::Cursor(open)"
	}
	ormInit()
	if len(drivers) == 0 {
		return // built without any database (tags nosqlite, nopostgres and nomysql): no module db
	}
	native.Register(&native.Module{
		Name:    "db",
		Doc:     "SQL databases (" + strings.Join(Kinds(), ", ") + "), and objects in tables",
		Funcs:   map[string]native.Func{"open": open, "many": relationFunc(true), "one": relationFunc(false)},
		Classes: []*native.Class{Conn, Cursor, Query, Relation},
	})
}

// ----------------------------------------------------------------------
// Opening and closing

// db::open(url): "sqlite:path" (a file, created if it does not exist) or
// "sqlite::memory:" (a database in memory, gone when it is closed).
var uriEscape = strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23")

func open(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("db::open", args, 1, 1)
	if err != nil {
		return value.NilValue, err
	}
	u, err := a.String(0)
	if err != nil {
		return value.NilValue, err
	}
	src, err := sourceOf(u)
	if err != nil {
		return value.NilValue, a.Errorf(value.ValueError, "%v", err)
	}
	if !drivers[src.dialect] {
		tag := map[dialect]string{sqlite: "nosqlite", postgres: "nopostgres", mysqlDB: "nomysql"}[src.dialect]
		return value.NilValue, a.Errorf(value.ValueError, "this kukel was built without %s (build tag %s)", dialectNames[src.dialect], tag)
	}
	// One connection: a transaction, lastid() and an in-memory database
	// belong to one session.
	d, err := sql.Open(src.driver, src.dsn)
	if err == nil {
		d.SetMaxOpenConns(1)
		d.SetConnMaxIdleTime(0)
		ctx, done := contextOf(env)
		err = d.PingContext(ctx) // open the file or reach the server now, so that a bad URL fails here
		done()
	}
	if err != nil {
		return value.NilValue, a.Errorf(DBError, "%v", err)
	}
	return Conn.Object(&conn{url: src.shown, dialect: src.dialect, db: d, lastID: value.NilValue}), nil
}

func closeConn(a native.Args, env value.Env, c *conn) (value.Value, error) {
	if !c.closed {
		if c.cursor != nil {
			c.cursor.finish()
		}
		if c.tx != nil {
			c.tx.Rollback() // an unfinished transaction is undone
			c.tx = nil
		}
		c.closed = true
		c.db.Close()
	}
	return value.NilValue, nil
}

// connMethod makes a member function of db::Conn that checks its
// arguments and the state of the connection.
func connMethod(name string, min, max int, fn func(a native.Args, env value.Env, c *conn) (value.Value, error)) value.NativeMethod {
	full := "db::Conn::" + name
	return func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
		a, err := native.Check(full, args, min, max)
		if err != nil {
			return value.NilValue, err
		}
		c := this.State.(*conn)
		switch {
		case name == "close":
		case c.closed:
			return value.NilValue, a.Errorf(value.ValueError, "the connection is closed")
		case c.cursor != nil && name != "intransaction" && name != "lastid":
			return value.NilValue, a.Errorf(value.ValueError, "a cursor is open on this connection: read it to the end or close it first")
		}
		return fn(a, env, c)
	}
}

// contextOf gives a context that ends when the program is cancelled (a Go
// program that embeds Kukel stops a call), and a function to call when
// the database call is done.
func contextOf(env value.Env) (context.Context, func()) {
	done := env.Done()
	if done == nil {
		return context.Background(), func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	stop := make(chan struct{})
	go func() {
		select {
		case <-done:
			cancel()
		case <-stop:
		}
	}()
	return ctx, func() { close(stop); cancel() }
}

// dbError turns an error of the database into a Kukel exception, or into
// the cancellation of the program.
func dbError(a native.Args, env value.Env, err error) error {
	if e := env.Err(); e != nil && errors.Is(err, context.Canceled) {
		return e
	}
	return a.Errorf(DBError, "%v", err)
}

// ----------------------------------------------------------------------
// Statements

// querier is what runs SQL: the database, or the open transaction.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func (c *conn) q() querier {
	if c.tx != nil {
		return c.tx
	}
	return c.db
}

// params turns the arguments after the SQL text into values for the
// database: nil (NULL), booleans, ints, floats, strings and time::Time.
func params(a native.Args) (string, []any, error) {
	text, err := a.String(0)
	if err != nil {
		return "", nil, err
	}
	ps := make([]any, len(a.V)-1)
	for i, v := range a.V[1:] {
		p, ok := dbValue(v)
		if !ok {
			return "", nil, a.Errorf(value.TypeError, "argument %d cannot be stored in a database: %s (nil, booleans, numbers, strings and time::Time can)", i+2, kindOf(v))
		}
		ps[i] = p
	}
	return text, ps, nil
}

// dbValue turns a Kukel value into one for the database: nil (NULL),
// booleans, ints, floats, strings and time::Time; ok is false for others.
func dbValue(v value.Value) (p any, ok bool) {
	switch v.Kind() {
	case value.Nil:
		return nil, true
	case value.Bool:
		return v.AsBool(), true
	case value.Int:
		return v.AsInt(), true
	case value.Float:
		return v.AsFloat(), true
	case value.String:
		return v.AsString(), true
	}
	if timemod.Time.Is(v) {
		return v.AsObject().State.(stdtime.Time), true
	}
	return nil, false
}

// kindOf names the kind of v for messages: its class for an object.
func kindOf(v value.Value) string {
	if v.Kind() == value.Object {
		return v.AsObject().Class.Name
	}
	return v.Kind().String()
}

// conn->exec(sql, args...): runs a statement that gives no rows (INSERT,
// UPDATE, CREATE, ...) and gives the number of rows it changed.
func exec(a native.Args, env value.Env, c *conn) (value.Value, error) {
	text, ps, err := params(a)
	if err != nil {
		return value.NilValue, err
	}
	ctx, done := contextOf(env)
	defer done()
	res, err := c.q().ExecContext(ctx, c.sqlFor(text), ps...)
	if err != nil {
		return value.NilValue, dbError(a, env, err)
	}
	c.lastID = value.NilValue
	if id, err := res.LastInsertId(); err == nil && id != 0 {
		c.lastID = value.MakeInt(id)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return value.NilValue, nil
	}
	return value.MakeInt(n), nil
}

// rowsOf starts a query and reads its columns.
func (c *conn) rowsOf(a native.Args, env value.Env) (*cursor, error) {
	text, ps, err := params(a)
	if err != nil {
		return nil, err
	}
	ctx, stop := contextOf(env)
	rows, err := c.q().QueryContext(ctx, c.sqlFor(text), ps...)
	if err != nil {
		stop()
		return nil, dbError(a, env, err)
	}
	cols, err := rows.ColumnTypes()
	if err != nil {
		rows.Close()
		stop()
		return nil, dbError(a, env, err)
	}
	cu := &cursor{conn: c, rows: rows, stop: stop}
	for _, col := range cols {
		cu.cols = append(cu.cols, col.Name())
		cu.types = append(cu.types, strings.ToUpper(col.DatabaseTypeName()))
	}
	return cu, nil
}

// row reads the current row as a map from column names to values.
func (cu *cursor) row(a native.Args) (value.Value, error) {
	vals := make([]any, len(cu.cols))
	ptrs := make([]any, len(cu.cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := cu.rows.Scan(ptrs...); err != nil {
		return value.NilValue, a.Errorf(DBError, "%v", err)
	}
	m := value.NewMap()
	for i, name := range cu.cols {
		m.Set(value.MakeString(name), cu.value(i, vals[i])) // a string key cannot fail
	}
	return value.MakeMap(m), nil
}

// value turns a value of the database into a Kukel value. Numbers that a
// driver gives as text (MySQL without parameters, PostgreSQL NUMERIC) are
// numbers again.
func (cu *cursor) value(i int, v any) value.Value {
	switch v := v.(type) {
	case nil:
		return value.NilValue
	case int64:
		if t := cu.types[i]; t == "BOOLEAN" || t == "BOOL" {
			return value.MakeBool(v != 0)
		}
		return value.MakeInt(v)
	case []byte:
		return cu.text(i, string(v))
	case string:
		return cu.text(i, v)
	case float64:
		return value.MakeFloat(v)
	case bool:
		return value.MakeBool(v)
	case stdtime.Time:
		return timemod.Time.Object(v)
	}
	return value.MakeString(fmt.Sprint(v))
}

// text turns a value the driver gives as text into a number for a column of
// a number type, else into a string.
func (cu *cursor) text(i int, s string) value.Value {
	switch cu.types[i] {
	case "TINYINT", "SMALLINT", "MEDIUMINT", "INT", "INTEGER", "BIGINT", "YEAR",
		"UNSIGNED TINYINT", "UNSIGNED SMALLINT", "UNSIGNED MEDIUMINT", "UNSIGNED INT", "UNSIGNED BIGINT":
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return value.MakeInt(n)
		}
	case "FLOAT", "DOUBLE", "REAL", "DECIMAL", "NUMERIC", "FLOAT4", "FLOAT8":
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return value.MakeFloat(f)
		}
	}
	return value.MakeString(s)
}

// finish closes the rows; the connection is free again.
func (cu *cursor) finish() {
	if !cu.done {
		cu.done = true
		cu.rows.Close()
		cu.stop()
		if cu.conn.cursor == cu {
			cu.conn.cursor = nil
		}
	}
}

// conn->query(sql, args...): all rows of a query, as a vector of maps from
// column names to values.
func query(a native.Args, env value.Env, c *conn) (value.Value, error) {
	cu, err := c.rowsOf(a, env)
	if err != nil {
		return value.NilValue, err
	}
	defer cu.finish()
	var rows []value.Value
	for cu.rows.Next() {
		if env.Err() != nil {
			return value.NilValue, env.Err()
		}
		r, err := cu.row(a)
		if err != nil {
			return value.NilValue, err
		}
		rows = append(rows, r)
	}
	if err := cu.rows.Err(); err != nil {
		return value.NilValue, dbError(a, env, err)
	}
	return value.MakeVector(append([]value.Value{}, rows...)), nil
}

// conn->first(sql, args...): the first row of a query, or nil.
func first(a native.Args, env value.Env, c *conn) (value.Value, error) {
	cu, err := c.rowsOf(a, env)
	if err != nil {
		return value.NilValue, err
	}
	defer cu.finish()
	if !cu.rows.Next() {
		if err := cu.rows.Err(); err != nil {
			return value.NilValue, dbError(a, env, err)
		}
		return value.NilValue, nil
	}
	return cu.row(a)
}

// conn->cursor(sql, args...): a db::Cursor that reads the rows one at a
// time (for results too large to hold at once). While it is open, the
// connection does nothing else.
func openCursor(a native.Args, env value.Env, c *conn) (value.Value, error) {
	cu, err := c.rowsOf(a, env)
	if err != nil {
		return value.NilValue, err
	}
	c.cursor = cu
	return Cursor.Object(cu), nil
}

func cursorMethod(name string, fn func(a native.Args, env value.Env, cu *cursor) (value.Value, error)) value.NativeMethod {
	full := "db::Cursor::" + name
	return func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
		a, err := native.Check(full, args, 0, 0)
		if err != nil {
			return value.NilValue, err
		}
		return fn(a, env, this.State.(*cursor))
	}
}

// cursor->next(): the next row (a map), or nil at the end, which also
// closes the cursor.
func next(a native.Args, env value.Env, cu *cursor) (value.Value, error) {
	if cu.done {
		return value.NilValue, nil
	}
	if !cu.rows.Next() {
		err := cu.rows.Err()
		cu.finish()
		if err != nil {
			return value.NilValue, dbError(a, env, err)
		}
		return value.NilValue, nil
	}
	return cu.row(a)
}

// ----------------------------------------------------------------------
// Transactions

// conn->begin(): starts a transaction; the statements until commit() or
// rollback() count together, or not at all.
func begin(a native.Args, env value.Env, c *conn) (value.Value, error) {
	if c.tx != nil {
		return value.NilValue, a.Errorf(value.ValueError, "a transaction is already open (transactions do not nest)")
	}
	ctx, done := contextOf(env)
	defer done()
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return value.NilValue, dbError(a, env, err)
	}
	c.tx = tx
	return value.NilValue, nil
}

// finish commits or rolls back the open transaction.
func (c *conn) finish(a native.Args, commit bool) error {
	if c.tx == nil {
		return a.Errorf(value.ValueError, "there is no open transaction (begin() starts one)")
	}
	tx := c.tx
	c.tx = nil
	var err error
	if commit {
		err = tx.Commit()
	} else {
		err = tx.Rollback()
	}
	if err != nil {
		return a.Errorf(DBError, "%v", err)
	}
	return nil
}
