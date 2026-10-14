package dbmod

import (
	"fmt"
	"strings"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

// The object mapping (TODO.md Q52): objects of Kukel classes stored as rows,
// by convention. A class's data members are the columns of its table, `id`
// is the primary key, and the table is the class's name in lower case. A
// static member function table() names the table otherwise, and columns()
// gives SQL types and constraints ({"price": "REAL NOT NULL"}); id is an
// INTEGER PRIMARY KEY, which the database fills in, unless columns() says
// otherwise (then the program sets id before the first save). The
// connection does the work (a data mapper): conn->migrate(C), conn->save(p),
// conn->delete(p), conn->get(C, id) and conn->find(C), a db::Query.

// Query is the class db::Query: conn->find(C) and its refinements.
var Query = &native.Class{Name: "Query"}

// ormInit adds the member functions of the mapping (before the module is
// registered).
func ormInit() {
	Conn.Methods["migrate"] = connMethod("migrate", 1, 1, migrate)
	Conn.Methods["save"] = connMethod("save", 1, 1, save)
	Conn.Methods["delete"] = connMethod("delete", 1, 1, deleteObject)
	Conn.Methods["get"] = connMethod("get", 2, 2, get)
	Conn.Methods["find"] = connMethod("find", 1, 1, find)
	Conn.Methods["load"] = connMethod("load", 2, -1, load)
	Query.Methods = map[string]value.NativeMethod{
		"where": queryMethod("where", 1, -1, func(a native.Args, env value.Env, q *finder) (value.Value, error) {
			text, err := a.String(0)
			if err != nil {
				return value.NilValue, err
			}
			r := q.copy()
			r.wheres = append(r.wheres, text)
			r.args = append(r.args, a.V[1:]...)
			return Query.Object(r), nil
		}),
		"orderby": queryMethod("orderby", 1, 1, func(a native.Args, env value.Env, q *finder) (value.Value, error) {
			text, err := a.String(0)
			if err != nil {
				return value.NilValue, err
			}
			r := q.copy()
			r.order = text
			return Query.Object(r), nil
		}),
		"limit": queryMethod("limit", 1, 2, func(a native.Args, env value.Env, q *finder) (value.Value, error) {
			n, err := a.Int(0)
			if err != nil {
				return value.NilValue, err
			}
			off := int64(0)
			if a.Has(1) {
				if off, err = a.Int(1); err != nil {
					return value.NilValue, err
				}
			}
			if n < 0 || off < 0 {
				return value.NilValue, a.Errorf(value.ValueError, "a limit and an offset cannot be negative")
			}
			r := q.copy()
			r.limit, r.offset = n, off
			return Query.Object(r), nil
		}),
		"include": queryMethod("include", 1, -1, func(a native.Args, env value.Env, q *finder) (value.Value, error) {
			r := q.copy()
			for i := range a.V {
				name, err := a.String(i)
				if err != nil {
					return value.NilValue, err
				}
				if q.model.rels[name] == nil {
					return value.NilValue, a.Errorf(value.ValueError, "class %s has no relation %s (relations() names them)", q.model.class.Name, name)
				}
				r.include = append(r.include, name)
			}
			return Query.Object(r), nil
		}),
		"all": queryMethod("all", 0, 0, func(a native.Args, env value.Env, q *finder) (value.Value, error) {
			objs, err := q.load(a, env)
			if err != nil {
				return value.NilValue, err
			}
			return value.MakeVector(objs), nil
		}),
		"first": queryMethod("first", 0, 0, func(a native.Args, env value.Env, q *finder) (value.Value, error) {
			r := q.copy()
			r.limit = 1
			objs, err := r.load(a, env)
			if err != nil || len(objs) == 0 {
				return value.NilValue, err
			}
			return objs[0], nil
		}),
		"count": queryMethod("count", 0, 0, func(a native.Args, env value.Env, q *finder) (value.Value, error) {
			text := "SELECT COUNT(*) FROM (" + q.selectSQL() + ") AS q"
			row, err := first(sqlArgs(a, text, q.args), env, q.conn)
			if err != nil {
				return value.NilValue, err
			}
			return firstValue(row), nil
		}),
		"delete": queryMethod("delete", 0, 0, func(a native.Args, env value.Env, q *finder) (value.Value, error) {
			if q.limit >= 0 || q.order != "" {
				return value.NilValue, a.Errorf(value.ValueError, "delete() takes the rows where() selects: orderby() and limit() do not apply")
			}
			return exec(sqlArgs(a, "DELETE FROM "+q.model.quote(q.model.table)+q.whereSQL(), q.args), env, q.conn)
		}),
	}
	Query.Show = func(o *value.ObjectObj) string {
		q := o.State.(*finder)
		return "db::Query(" + strings.TrimPrefix(q.selectSQL(), "SELECT * FROM ") + ")"
	}
}

// ----------------------------------------------------------------------
// Models

// model is how a class maps to a table.
type model struct {
	conn    *conn
	class   *value.ClassObj
	table   string
	types   map[string]string    // the SQL type of each column ("" for none)
	autoKey bool                 // the database fills in id (an INTEGER PRIMARY KEY, AUTO_INCREMENT, IDENTITY, ...)
	rels    map[string]*relation // relation members (relations()): not columns
}

// columns are the data members stored in the table: all but the relation
// members, in order.
func (m *model) columns() []string {
	var cols []string
	for _, f := range m.class.FieldNames {
		if m.rels[f] == nil {
			cols = append(cols, f)
		}
	}
	return cols
}

// modelOf finds the mapping of class k, asking its table() and columns().
func modelOf(a native.Args, env value.Env, c *conn, k *value.ClassObj) (*model, error) {
	if k.Native {
		return nil, a.Errorf(value.TypeError, "%s is a class written in Go: only Kukel classes can be stored", k.Name)
	}
	if _, ok := k.FieldIndex["id"]; !ok {
		return nil, a.Errorf(value.ValueError, "class %s has no data member id (the primary key)", k.Name)
	}
	name := k.Name[strings.LastIndex(k.Name, ":")+1:]
	m := &model{conn: c, class: k, table: strings.ToLower(name), types: map[string]string{"id": c.keyType()}}
	if v, ok, err := static(a, env, k, "table"); err != nil {
		return nil, err
	} else if ok {
		if v.Kind() != value.String || !isName(v.AsString()) {
			return nil, a.Errorf(value.ValueError, "%s::table() must give a table name (letters, digits and _), not %s", k.Name, v.Repr())
		}
		m.table = v.AsString()
	}
	if v, ok, err := static(a, env, k, "columns"); err != nil {
		return nil, err
	} else if ok {
		if v.Kind() != value.Map {
			return nil, a.Errorf(value.TypeError, "%s::columns() must give a map from data members to SQL types, not %s", k.Name, kindOf(v))
		}
		var bad error
		v.AsMap().Each(func(col, typ value.Value) {
			switch {
			case bad != nil:
			case col.Kind() != value.String || typ.Kind() != value.String:
				bad = a.Errorf(value.TypeError, "%s::columns() must map names to strings (SQL types): %s: %s", k.Name, col.Repr(), typ.Repr())
			default:
				if _, ok := k.FieldIndex[col.AsString()]; !ok {
					bad = a.Errorf(value.ValueError, "%s::columns() names %s, which is not a data member of %s", k.Name, col.AsString(), k.Name)
				}
				m.types[col.AsString()] = typ.AsString()
			}
		})
		if bad != nil {
			return nil, bad
		}
	}
	if err := m.readRelations(a, env); err != nil {
		return nil, err
	}
	key := strings.ToUpper(strings.Join(strings.Fields(m.types["id"]), " "))
	m.autoKey = key == strings.ToUpper(c.keyType()) || strings.Contains(key, "AUTO_INCREMENT") ||
		strings.Contains(key, "IDENTITY") || strings.Contains(key, "SERIAL") ||
		c.dialect == sqlite && strings.HasPrefix(key, "INTEGER PRIMARY KEY")
	return m, nil
}

// static calls the static member function name of k, if it has one.
func static(a native.Args, env value.Env, k *value.ClassObj, name string) (value.Value, bool, error) {
	m, ok := k.Methods[name]
	if !ok {
		return value.NilValue, false, nil
	}
	if !m.Static || m.Fn == nil {
		return value.NilValue, false, a.Errorf(value.TypeError, "%s must be a static member function, defined", m.FullName())
	}
	v, err := env.Call(value.MakeFunction(m.Fn), nil)
	return v, true, err
}

func isName(s string) bool {
	for i, c := range s {
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return s != ""
}

// classArg gives argument i, a class (also the class of an object when
// object is set).
func classArg(a native.Args, i int, object bool) (*value.ClassObj, error) {
	switch v := a.V[i]; {
	case v.Kind() == value.Class:
		return v.AsClass(), nil
	case object && v.Kind() == value.Object:
		return v.AsObject().Class, nil
	}
	if object {
		return nil, a.TypeError(i, "an object")
	}
	return nil, a.TypeError(i, "a class")
}

// sqlArgs makes the arguments of exec, query and first: the SQL text, then
// the values for its ?.
func sqlArgs(a native.Args, text string, args []value.Value) native.Args {
	return native.Args{Name: a.Name, V: append([]value.Value{value.MakeString(text)}, args...)}
}

// columnValues gives the data members of obj (without id) and the values
// for the database, checking that each can be stored.
func (m *model) columnValues(a native.Args, obj *value.ObjectObj) ([]string, []value.Value, error) {
	var cols []string
	var vals []value.Value
	for _, f := range m.columns() {
		i := m.class.FieldIndex[f]
		if f == "id" {
			continue
		}
		if _, ok := dbValue(obj.Fields[i]); !ok {
			return nil, nil, a.Errorf(value.TypeError, "%s->%s cannot be stored in a database: %s (nil, booleans, numbers, strings and time::Time can)", m.class.Name, f, kindOf(obj.Fields[i]))
		}
		cols = append(cols, f)
		vals = append(vals, obj.Fields[i])
	}
	return cols, vals, nil
}

// ----------------------------------------------------------------------
// The member functions of db::Conn

// conn->migrate(C): creates C's table if there is none, else adds the
// columns for data members it does not have yet. It never drops or changes
// a column.
func migrate(a native.Args, env value.Env, c *conn) (value.Value, error) {
	k, err := classArg(a, 0, false)
	if err != nil {
		return value.NilValue, err
	}
	m, err := modelOf(a, env, c, k)
	if err != nil {
		return value.NilValue, err
	}
	rows, err := query(sqlArgs(a, c.columnsSQL(), []value.Value{value.MakeString(m.table)}), env, c)
	if err != nil {
		return value.NilValue, err
	}
	have := map[string]bool{}
	for _, r := range rows.AsVector().Elems {
		have[firstValue(r).AsString()] = true
	}
	column := func(f string) string { return strings.TrimSpace(m.quote(f) + " " + m.types[f]) }
	if c.dialect != sqlite { // SQLite takes columns without a type, the servers do not
		for _, f := range m.columns() {
			if m.types[f] == "" && !have[f] {
				return value.NilValue, a.Errorf(value.ValueError, "%s->%s needs an SQL type in %s::columns() (%s columns have one)", k.Name, f, k.Name, dialectNames[c.dialect])
			}
		}
	}
	if len(have) == 0 {
		defs := []string{column("id")}
		for _, f := range m.columns() {
			if f != "id" {
				defs = append(defs, column(f))
			}
		}
		_, err := exec(sqlArgs(a, "CREATE TABLE "+m.quote(m.table)+" ("+strings.Join(defs, ", ")+")", nil), env, c)
		return value.NilValue, err
	}
	for _, f := range m.columns() {
		if !have[f] {
			if _, err := exec(sqlArgs(a, "ALTER TABLE "+m.quote(m.table)+" ADD COLUMN "+column(f), nil), env, c); err != nil {
				return value.NilValue, err
			}
		}
	}
	return value.NilValue, nil
}

// conn->save(p): stores object p. Without an id (and an INTEGER PRIMARY KEY)
// it inserts a row and sets p->id; with one it inserts the row or updates
// the row with that id. Gives p.
func save(a native.Args, env value.Env, c *conn) (value.Value, error) {
	if a.V[0].Kind() != value.Object {
		return value.NilValue, a.TypeError(0, "an object")
	}
	obj := a.V[0].AsObject()
	m, err := modelOf(a, env, c, obj.Class)
	if err != nil {
		return value.NilValue, err
	}
	cols, vals, err := m.columnValues(a, obj)
	if err != nil {
		return value.NilValue, err
	}
	idSlot := m.class.FieldIndex["id"]
	id := obj.Fields[idSlot]
	if id.IsNil() {
		if !m.autoKey {
			return value.NilValue, a.Errorf(value.ValueError, "%s->id is nil: set it before saving (the key is %s, which the database does not fill in)", m.class.Name, m.types["id"])
		}
		text := "INSERT INTO " + m.quote(m.table) + " (" + m.names(cols) + ") VALUES (" + marks(len(cols)) + ")"
		if len(cols) == 0 {
			text = "INSERT INTO " + m.quote(m.table) + " DEFAULT VALUES"
			if c.dialect == mysqlDB {
				text = "INSERT INTO " + m.quote(m.table) + " () VALUES ()"
			}
		}
		if c.dialect == postgres { // no last insert id: the row says it
			row, err := first(sqlArgs(a, text+" RETURNING "+m.quote("id"), vals), env, c)
			if err != nil {
				return value.NilValue, err
			}
			c.lastID = firstValue(row)
		} else if _, err := exec(sqlArgs(a, text, vals), env, c); err != nil {
			return value.NilValue, err
		}
		obj.Fields[idSlot] = c.lastID
		return a.V[0], nil
	}
	if _, ok := dbValue(id); !ok {
		return value.NilValue, a.Errorf(value.TypeError, "%s->id cannot be stored in a database: %s", m.class.Name, kindOf(id))
	}
	text := "INSERT INTO " + m.quote(m.table) + " (" + m.names(append([]string{"id"}, cols...)) + ") VALUES (" + marks(len(cols)+1) + ")"
	var sets []string
	for _, col := range cols {
		if c.dialect == mysqlDB {
			sets = append(sets, m.quote(col)+" = VALUES("+m.quote(col)+")")
		} else {
			sets = append(sets, m.quote(col)+" = excluded."+m.quote(col))
		}
	}
	switch {
	case c.dialect == mysqlDB && len(cols) == 0:
		text += " ON DUPLICATE KEY UPDATE " + m.quote("id") + " = " + m.quote("id")
	case c.dialect == mysqlDB:
		text += " ON DUPLICATE KEY UPDATE " + strings.Join(sets, ", ")
	case len(cols) == 0:
		text += " ON CONFLICT (" + m.quote("id") + ") DO NOTHING"
	default:
		text += " ON CONFLICT (" + m.quote("id") + ") DO UPDATE SET " + strings.Join(sets, ", ")
	}
	if _, err := exec(sqlArgs(a, text, append([]value.Value{id}, vals...)), env, c); err != nil {
		return value.NilValue, err
	}
	return a.V[0], nil
}

// conn->delete(p): deletes the row of object p; gives whether there was
// one.
func deleteObject(a native.Args, env value.Env, c *conn) (value.Value, error) {
	if a.V[0].Kind() != value.Object {
		return value.NilValue, a.TypeError(0, "an object")
	}
	obj := a.V[0].AsObject()
	m, err := modelOf(a, env, c, obj.Class)
	if err != nil {
		return value.NilValue, err
	}
	id := obj.Fields[m.class.FieldIndex["id"]]
	if id.IsNil() {
		return value.False, nil
	}
	n, err := exec(sqlArgs(a, "DELETE FROM "+m.quote(m.table)+" WHERE "+m.quote("id")+" = ?", []value.Value{id}), env, c)
	if err != nil {
		return value.NilValue, err
	}
	return value.MakeBool(n.AsInt() > 0), nil
}

// conn->get(C, id): the object of class C stored with that id, or nil.
func get(a native.Args, env value.Env, c *conn) (value.Value, error) {
	k, err := classArg(a, 0, false)
	if err != nil {
		return value.NilValue, err
	}
	m, err := modelOf(a, env, c, k)
	if err != nil {
		return value.NilValue, err
	}
	q := &finder{conn: c, model: m, limit: 1, wheres: []string{m.quote("id") + " = ?"}, args: []value.Value{a.V[1]}}
	objs, err := q.objects(a, env, q.selectSQL())
	if err != nil || len(objs) == 0 {
		return value.NilValue, err
	}
	return objs[0], nil
}

// conn->find(C): a db::Query for all objects of class C, refined with
// where(), orderby() and limit().
func find(a native.Args, env value.Env, c *conn) (value.Value, error) {
	k, err := classArg(a, 0, false)
	if err != nil {
		return value.NilValue, err
	}
	m, err := modelOf(a, env, c, k)
	if err != nil {
		return value.NilValue, err
	}
	return Query.Object(&finder{conn: c, model: m, limit: -1}), nil
}

func (m *model) names(cols []string) string {
	q := make([]string, len(cols))
	for i, c := range cols {
		q[i] = m.quote(c)
	}
	return strings.Join(q, ", ")
}

func (m *model) quote(name string) string { return m.conn.quote(name) }

func marks(n int) string { return strings.TrimSuffix(strings.Repeat("?, ", n), ", ") }

// ----------------------------------------------------------------------
// Queries

// finder is the state of a db::Query. Its member functions give new
// queries; a query never changes.
type finder struct {
	conn          *conn
	model         *model
	wheres        []string      // SQL conditions, joined with AND
	args          []value.Value // the values for their ?
	order         string        // SQL after ORDER BY, or ""
	limit, offset int64         // limit -1: none
	include       []string      // relation members to fill (include())
}

// load runs the query and fills the relation members include() names.
func (q *finder) load(a native.Args, env value.Env) ([]value.Value, error) {
	objs, err := q.objects(a, env, q.selectSQL())
	if err != nil {
		return nil, err
	}
	for _, name := range q.include {
		if err := q.model.fill(a, env, objs, name); err != nil {
			return nil, err
		}
	}
	return objs, nil
}

func (q *finder) copy() *finder {
	r := *q
	r.wheres = append([]string(nil), q.wheres...)
	r.args = append([]value.Value(nil), q.args...)
	r.include = append([]string(nil), q.include...)
	return &r
}

func (q *finder) whereSQL() string {
	if len(q.wheres) == 0 {
		return ""
	}
	return " WHERE (" + strings.Join(q.wheres, ") AND (") + ")"
}

func (q *finder) selectSQL() string {
	text := "SELECT * FROM " + q.model.quote(q.model.table) + q.whereSQL()
	if q.order != "" {
		text += " ORDER BY " + q.order
	}
	if q.limit >= 0 {
		text += fmt.Sprintf(" LIMIT %d", q.limit)
		if q.offset > 0 {
			text += fmt.Sprintf(" OFFSET %d", q.offset)
		}
	}
	return text
}

// objects runs a SELECT and makes an object of the model's class of each
// row: its data members get the columns of the same name (the constructor
// does not run; columns without a data member are left out).
func (q *finder) objects(a native.Args, env value.Env, text string) ([]value.Value, error) {
	cu, err := q.conn.rowsOf(sqlArgs(a, text, q.args), env)
	if err != nil {
		return nil, err
	}
	defer cu.finish()
	vals := make([]any, len(cu.cols))
	ptrs := make([]any, len(cu.cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	objs := []value.Value{}
	for cu.rows.Next() {
		if env.Err() != nil {
			return nil, env.Err()
		}
		if err := cu.rows.Scan(ptrs...); err != nil {
			return nil, a.Errorf(DBError, "%v", err)
		}
		obj := value.NewObject(q.model.class)
		o := obj.AsObject()
		for i, col := range cu.cols {
			if slot, ok := q.model.class.FieldIndex[col]; ok {
				v := cu.value(i, vals[i])
				// A column declared BOOLEAN in columns() gives booleans also
				// where the database stores 1 and 0 (MySQL's BOOLEAN is a
				// TINYINT).
				if t := strings.ToUpper(q.model.types[col]); v.Kind() == value.Int && (strings.HasPrefix(t, "BOOL")) {
					v = value.MakeBool(v.AsInt() != 0)
				}
				o.Fields[slot] = v
			}
		}
		objs = append(objs, obj)
	}
	if err := cu.rows.Err(); err != nil {
		return nil, dbError(a, env, err)
	}
	return objs, nil
}

// queryMethod makes a member function of db::Query; its connection must be
// usable, as for the member functions of db::Conn.
func queryMethod(name string, min, max int, fn func(a native.Args, env value.Env, q *finder) (value.Value, error)) value.NativeMethod {
	full := "db::Query::" + name
	return func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
		a, err := native.Check(full, args, min, max)
		if err != nil {
			return value.NilValue, err
		}
		q := this.State.(*finder)
		switch {
		case q.conn.closed:
			return value.NilValue, a.Errorf(value.ValueError, "the connection is closed")
		case q.conn.cursor != nil:
			return value.NilValue, a.Errorf(value.ValueError, "a cursor is open on this connection: read it to the end or close it first")
		}
		return fn(a, env, q)
	}
}

// firstValue gives the value of the first column of a row.
func firstValue(row value.Value) value.Value {
	var first value.Value
	done := false
	row.AsMap().Each(func(k, v value.Value) {
		if !done {
			first, done = v, true
		}
	})
	return first
}

// ----------------------------------------------------------------------
// Relations

// relation is a relation member of a class: many objects of another class
// whose data member key holds this object's id (db::many), or the one
// object of another class whose id this object's data member key holds
// (db::one).
type relation struct {
	many  bool
	class *value.ClassObj
	key   string
}

// Relation is the class of what db::many and db::one give.
var Relation = &native.Class{Name: "Relation", Show: func(o *value.ObjectObj) string {
	r := o.State.(*relation)
	kind := "one"
	if r.many {
		kind = "many"
	}
	return "db::" + kind + "(" + r.class.Name + ", \"" + r.key + "\")"
}}

// db::many(C, key) and db::one(C, key): relations, for relations().
func relationFunc(many bool) native.Func {
	name := "db::one"
	if many {
		name = "db::many"
	}
	return func(env value.Env, args []value.Value) (value.Value, error) {
		a, err := native.Check(name, args, 2, 2)
		if err != nil {
			return value.NilValue, err
		}
		k, err := classArg(a, 0, false)
		if err != nil {
			return value.NilValue, err
		}
		key, err := a.String(1)
		if err != nil {
			return value.NilValue, err
		}
		return Relation.Object(&relation{many: many, class: k, key: key}), nil
	}
}

// readRelations reads the relations() of the model's class: a map from data
// members to db::many(...) and db::one(...), checked against the classes.
func (m *model) readRelations(a native.Args, env value.Env) error {
	k := m.class
	m.rels = map[string]*relation{}
	v, ok, err := static(a, env, k, "relations")
	if err != nil || !ok {
		return err
	}
	if v.Kind() != value.Map {
		return a.Errorf(value.TypeError, "%s::relations() must give a map from data members to db::many(...) and db::one(...), not %s", k.Name, kindOf(v))
	}
	var bad error
	v.AsMap().Each(func(name, rv value.Value) {
		if bad != nil {
			return
		}
		if name.Kind() != value.String || !Relation.Is(rv) {
			bad = a.Errorf(value.TypeError, "%s::relations() must map names to db::many(...) or db::one(...): %s: %s", k.Name, name.Repr(), rv.Repr())
			return
		}
		r, member := rv.AsObject().State.(*relation), name.AsString()
		owner, side := k, "this"
		if r.many {
			owner, side = r.class, "the other"
		}
		_, isMember := k.FieldIndex[member]
		_, hasKey := owner.FieldIndex[r.key]
		_, typed := m.types[member]
		switch {
		case !isMember:
			bad = a.Errorf(value.ValueError, "%s::relations() names %s, which is not a data member of %s", k.Name, member, k.Name)
		case member == "id" || typed:
			bad = a.Errorf(value.ValueError, "%s::relations(): %s is a column (the key, or named in columns()), not a relation", k.Name, member)
		case !hasKey || r.key == "id":
			bad = a.Errorf(value.ValueError, "%s::relations(): %s: %s has no data member %s for the key (on %s side)", k.Name, member, owner.Name, r.key, side)
		}
		m.rels[member] = r
	})
	return bad
}

// fill sets relation member name of each of objs (objects of the model's
// class): one query for all of them (WHERE ... IN (...), in parts).
func (m *model) fill(a native.Args, env value.Env, objs []value.Value, name string) error {
	r := m.rels[name]
	if r == nil {
		return a.Errorf(value.ValueError, "class %s has no relation %s (relations() names them)", m.class.Name, name)
	}
	other, err := modelOf(a, env, m.conn, r.class)
	if err != nil {
		return err
	}
	slot := m.class.FieldIndex[name]
	// The values to look for: the objects' ids (many), or their keys (one).
	from := "id"
	if !r.many {
		from = r.key
	}
	var keys []value.Value
	seen := map[string]bool{}
	for _, o := range objs {
		k := o.AsObject().Fields[m.class.FieldIndex[from]]
		if !k.IsNil() && !seen[k.Repr()] {
			seen[k.Repr()] = true
			keys = append(keys, k)
		}
	}
	// The other objects, by the value that links them.
	by := "id"
	if r.many {
		by = r.key
	}
	found := map[string][]value.Value{}
	const part = 500 // values per query (the databases limit the number of parameters)
	for i := 0; i < len(keys); i += part {
		chunk := keys[i:min(i+part, len(keys))]
		q := &finder{conn: m.conn, model: other, limit: -1, args: chunk,
			wheres: []string{other.quote(by) + " IN (" + marks(len(chunk)) + ")"}}
		if r.many {
			q.order = other.quote("id")
		}
		got, err := q.objects(a, env, q.selectSQL())
		if err != nil {
			return err
		}
		for _, g := range got {
			k := g.AsObject().Fields[other.class.FieldIndex[by]].Repr()
			found[k] = append(found[k], g)
		}
	}
	for _, o := range objs {
		obj := o.AsObject()
		k := obj.Fields[m.class.FieldIndex[from]]
		switch {
		case r.many:
			obj.Fields[slot] = value.MakeVector(append([]value.Value{}, found[k.Repr()]...))
		case !k.IsNil() && len(found[k.Repr()]) > 0:
			obj.Fields[slot] = found[k.Repr()][0]
		default:
			obj.Fields[slot] = value.NilValue
		}
	}
	return nil
}

// conn->load(p, name, ...): fills the relation members of object p.
func load(a native.Args, env value.Env, c *conn) (value.Value, error) {
	if a.V[0].Kind() != value.Object {
		return value.NilValue, a.TypeError(0, "an object")
	}
	m, err := modelOf(a, env, c, a.V[0].AsObject().Class)
	if err != nil {
		return value.NilValue, err
	}
	for i := 1; i < len(a.V); i++ {
		name, err := a.String(i)
		if err != nil {
			return value.NilValue, err
		}
		if err := m.fill(a, env, []value.Value{a.V[0]}, name); err != nil {
			return value.NilValue, err
		}
	}
	return a.V[0], nil
}
