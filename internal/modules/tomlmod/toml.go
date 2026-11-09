// Package tomlmod is the native module "toml": decode TOML text into Kukel
// values and encode Kukel maps as TOML text.
//
//	use toml;
//	local cfg = toml::decode("[server]\nport = 8080\n");
//	println(cfg["server"]["port"]);                  // 8080
//	print(toml::encode({"name": "Kukel", "server": {"port": 8080}}));
//
// Decoding is github.com/BurntSushi/toml (TOML 1.1, which reads TOML 1.0 as
// well); the order of the keys, which that library does not keep in its
// maps, comes from its metadata. Encoding is our own, so that maps keep
// their order.
package tomlmod

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	stdtime "time"
	"unicode/utf8"

	"github.com/BurntSushi/toml"

	"github.com/maarten-boot/kukel/internal/modules/timemod"
	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

func init() {
	native.Register(&native.Module{
		Name: "toml",
		Doc:  "decode and encode TOML",
		Funcs: map[string]native.Func{
			"decode": decode,
			"encode": encode,
		},
	})
}

// maxDepth is how deeply arrays and tables (vectors and maps) may nest, for
// decode and encode alike. It is lower than json's, as TOML is for
// configuration, which nests a few levels at most: the library's time for
// a dotted key grows with the square of its depth, so a high limit would
// let a small text take long to read.
const maxDepth = 32

// decode(s): the map TOML text s stands for. Tables become maps, with their
// keys in the order of the text; arrays become vectors. An offset datetime
// becomes a time::Time; a local datetime, local date or local time (without
// an offset) a string in its TOML form.
func decode(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("toml::decode", args, 1, 1)
	if err != nil {
		return value.NilValue, err
	}
	s, err := a.String(0)
	if err != nil {
		return value.NilValue, err
	}
	if d := nesting(s); d > maxDepth {
		return value.NilValue, a.Errorf(value.ValueError, "arrays and tables nested more than %d deep", maxDepth)
	}
	var data map[string]any
	md, err := toml.Decode(s, &data)
	if err != nil {
		var pe toml.ParseError
		if errors.As(err, &pe) {
			line, col := where(s, pe.Position.Start)
			return value.NilValue, a.Errorf(value.ValueError, "invalid TOML at line %d, column %d: %s", line, col, strings.TrimSuffix(pe.Message, "."))
		}
		return value.NilValue, a.Errorf(value.ValueError, "invalid TOML: %v", err)
	}
	root := &order{}
	for _, k := range md.Keys() {
		root.add(k, md.Type(k...), data)
	}
	v, ok := convert(data, root, 0)
	if !ok {
		return value.NilValue, a.Errorf(value.ValueError, "arrays and tables nested more than %d deep", maxDepth)
	}
	return v, nil
}

// where gives the line and column (counted from 1, the column in
// characters) of byte offset i of s. The library's own Line and Col are
// sometimes off, its byte offset is not.
func where(s string, i int) (line, col int) {
	if i > len(s) {
		i = len(s)
	}
	if i < 0 {
		i = 0
	}
	before := s[:i]
	line = strings.Count(before, "\n") + 1
	col = utf8.RuneCountInString(before[strings.LastIndexByte(before, '\n')+1:]) + 1
	return line, col
}

// nesting is the deepest nesting in s, roughly: brackets and braces
// outside strings and comments, plus the dots on the line so far (a dotted
// key a.b.c is a table in a table). It errs on the safe side (a header
// counts its brackets, a float its dot), and lets decode refuse text that
// nests deeper than maxDepth before the library reads it.
func nesting(s string) int {
	depth, dots, max := 0, 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\n':
			dots = 0
		case '.':
			dots++
			if depth+dots > max {
				max = depth + dots
			}
		case '[', '{':
			depth++
			if depth+dots > max {
				max = depth + dots
			}
		case ']', '}':
			if depth > 0 {
				depth--
			}
		case '#':
			for i+1 < len(s) && s[i+1] != '\n' {
				i++
			}
		case '"', '\'':
			q := s[i]
			if strings.HasPrefix(s[i:], strings.Repeat(string(q), 3)) {
				end := strings.Index(s[i+3:], strings.Repeat(string(q), 3))
				if end < 0 {
					return max
				}
				i += 3 + end + 2
				continue
			}
			for i++; i < len(s) && s[i] != q && s[i] != '\n'; i++ {
				if q == '"' && s[i] == '\\' {
					i++
				}
			}
		}
	}
	return max
}

// order is the order of the keys of one table, and of the tables below it,
// as the text has them. BurntSushi/toml decodes tables into Go maps, which
// have no order, but lists every key path in the order of the text
// (MetaData.Keys); add replays that list into a tree of orders.
type order struct {
	keys   []string
	kids   map[string]*order
	elems  []*order         // the tables of an array of tables
	inline bool             // an array written in the text ([{...}, {...}]), not with [[headers]]
	cur    int              // inline: the element that keys go to now
	tables []map[string]any // inline: the decoded tables, made once
}

func (o *order) kid(name string) *order {
	if o.kids == nil {
		o.kids = map[string]*order{}
	}
	k, ok := o.kids[name]
	if !ok {
		k = &order{}
		o.kids[name] = k
		o.keys = append(o.keys, name)
	}
	return k
}

func (o *order) has(name string) bool {
	_, ok := o.kids[name]
	return ok
}

// add places the key path k, of TOML type ty, in the tree; data is the
// decoded table at the root. A [[header]] (type ArrayHash) starts a new
// element of its array; the keys of an inline array of tables have no
// element in their path, so the element is the first, from the current
// one on, that holds the key and has not had it yet: the keys of one
// inline table come together, and the tables in their order.
func (o *order) add(k toml.Key, ty string, data map[string]any) {
	node, d := o, any(data)
	for i, part := range k {
		dm, ok := d.(map[string]any)
		if !ok {
			return
		}
		child, cd := node.kid(part), dm[part]
		last := i == len(k)-1
		if last {
			switch ty {
			case "ArrayHash":
				child.elems = append(child.elems, &order{})
			case "Array":
				child.inline = true
			}
			return
		}
		if !child.inline {
			tables, _ := cd.([]map[string]any)
			if tables == nil {
				node, d = child, cd
				continue
			}
			if len(child.elems) == 0 {
				return
			}
			n := len(child.elems) - 1
			if n >= len(tables) {
				return
			}
			node, d = child.elems[n], tables[n]
			continue
		}
		if child.tables == nil {
			child.tables = tablesOf(cd)
		}
		tables := child.tables
		if tables == nil {
			node, d = child, cd
			continue
		}
		for len(child.elems) < len(tables) {
			child.elems = append(child.elems, &order{})
		}
		next := k[i+1]
		for child.cur < len(tables) {
			_, in := tables[child.cur][next]
			if in && !child.elems[child.cur].has(next) {
				break
			}
			child.cur++
		}
		if child.cur >= len(tables) {
			return
		}
		node, d = child.elems[child.cur], tables[child.cur]
	}
}

// tablesOf gives the tables of an array of tables, nil for anything else.
func tablesOf(d any) []map[string]any {
	switch d := d.(type) {
	case []map[string]any:
		return d
	case []any:
		tables := make([]map[string]any, len(d))
		for i, e := range d {
			m, ok := e.(map[string]any)
			if !ok {
				return nil
			}
			tables[i] = m
		}
		return tables
	}
	return nil
}

// convert turns a decoded value into a Kukel value; o (which may be nil)
// orders its tables. Keys that o does not know (it should know them all)
// come after the known ones, sorted, so that nothing is lost. It fails (ok
// false) for arrays and tables nested more than maxDepth deep, which
// dotted keys can make without brackets.
func convert(d any, o *order, depth int) (v value.Value, ok bool) {
	if depth > maxDepth {
		return value.NilValue, false
	}
	switch d := d.(type) {
	case map[string]any:
		m := value.NewMap()
		var rest []string
		if o != nil {
			for _, k := range o.keys {
				if x, in := d[k]; in {
					if v, ok = convert(x, o.kids[k], depth+1); !ok {
						return v, false
					}
					m.Set(value.MakeString(k), v)
				}
			}
		}
		for k := range d {
			if o == nil || !o.has(k) {
				rest = append(rest, k)
			}
		}
		sort.Strings(rest)
		for _, k := range rest {
			if v, ok = convert(d[k], nil, depth+1); !ok {
				return v, false
			}
			m.Set(value.MakeString(k), v)
		}
		return value.MakeMap(m), true
	case []map[string]any:
		elems := make([]value.Value, len(d))
		for i, e := range d {
			if elems[i], ok = convert(e, elemOrder(o, i), depth+1); !ok {
				return elems[i], false
			}
		}
		return value.MakeVector(elems), true
	case []any:
		elems := make([]value.Value, len(d))
		for i, e := range d {
			if elems[i], ok = convert(e, elemOrder(o, i), depth+1); !ok {
				return elems[i], false
			}
		}
		return value.MakeVector(elems), true
	case string:
		return value.MakeString(d), true
	case int64:
		return value.MakeInt(d), true
	case float64:
		return value.MakeFloat(d), true
	case bool:
		return value.MakeBool(d), true
	case stdtime.Time:
		switch d.Location().String() {
		case "datetime-local":
			return value.MakeString(d.Format("2006-01-02T15:04:05.999999999")), true
		case "date-local":
			return value.MakeString(d.Format("2006-01-02")), true
		case "time-local":
			return value.MakeString(d.Format("15:04:05.999999999")), true
		}
		return timemod.Time.Object(d), true
	}
	return value.MakeString(fmt.Sprint(d)), true // not reached: the library gives only the types above
}

func elemOrder(o *order, i int) *order {
	if o == nil || i >= len(o.elems) {
		return nil
	}
	return o.elems[i]
}

// encode(v): the map v as TOML text. Keys and values come in the map's
// order, except that a table's tables (maps) and arrays of tables (vectors
// of maps) follow its other values, as TOML requires.
func encode(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("toml::encode", args, 1, 1)
	if err != nil {
		return value.NilValue, err
	}
	if args[0].Kind() != value.Map {
		return value.NilValue, a.Errorf(value.TypeError, "a TOML document is a map, not %s", native.TypeName(args[0]))
	}
	e := &encoder{a: a, open: map[any]bool{}}
	if err := e.table(args[0], nil, 0); err != nil {
		return value.NilValue, err
	}
	return value.MakeString(e.b.String()), nil
}

type encoder struct {
	a    native.Args
	b    strings.Builder
	open map[any]bool // the vectors and maps being encoded (a cycle is an error)
}

func (e *encoder) enter(v value.Value, depth int) error {
	if e.open[v.Obj()] {
		return e.a.Errorf(value.ValueError, "the value contains itself")
	}
	if depth > maxDepth {
		return e.a.Errorf(value.ValueError, "vectors and maps nested more than %d deep", maxDepth)
	}
	e.open[v.Obj()] = true
	return nil
}

// isTable reports whether v is written as a [table].
func isTable(v value.Value) bool { return v.Kind() == value.Map }

// isTables reports whether v is written as an [[array of tables]]: a
// vector, not empty, of maps only.
func isTables(v value.Value) bool {
	if v.Kind() != value.Vector || len(v.AsVector().Elems) == 0 {
		return false
	}
	for _, x := range v.AsVector().Elems {
		if x.Kind() != value.Map {
			return false
		}
	}
	return true
}

// entries gives the keys of map v as TOML keys (number keys become their
// text), with their values; two keys with the same text are an error.
func (e *encoder) entries(v value.Value) ([]string, []value.Value, error) {
	m := v.AsMap()
	keys := m.Keys()
	names := make([]string, len(keys))
	vals := make([]value.Value, len(keys))
	seen := map[string]bool{}
	for i, k := range keys {
		names[i] = k.String()
		if k.Kind() == value.String {
			names[i] = k.AsString()
		}
		if seen[names[i]] {
			return nil, nil, e.a.Errorf(value.ValueError, "the map has the key %s twice (as a number and as a string)", strconv.Quote(names[i]))
		}
		seen[names[i]] = true
		vals[i], _, _ = m.Get(k)
	}
	return names, vals, nil
}

// table writes the map v, the table at path (nil: the document), with its
// header if it has one.
func (e *encoder) table(v value.Value, path []string, depth int) error {
	if err := e.enter(v, depth); err != nil {
		return err
	}
	defer delete(e.open, v.Obj())
	names, vals, err := e.entries(v)
	if err != nil {
		return err
	}
	for i, x := range vals {
		if isTable(x) || isTables(x) {
			continue
		}
		if err := e.key(names[i]); err != nil {
			return err
		}
		e.b.WriteString(" = ")
		if err := e.value(x, depth+1); err != nil {
			return err
		}
		e.b.WriteByte('\n')
	}
	for i, x := range vals {
		sub := append(path[:len(path):len(path)], names[i])
		switch {
		case isTable(x):
			if err := e.header("[", sub, "]"); err != nil {
				return err
			}
			if err := e.table(x, sub, depth+1); err != nil {
				return err
			}
		case isTables(x):
			if err := e.enter(x, depth+1); err != nil {
				return err
			}
			for _, t := range x.AsVector().Elems {
				if err := e.header("[[", sub, "]]"); err != nil {
					return err
				}
				if err := e.table(t, sub, depth+2); err != nil {
					return err
				}
			}
			delete(e.open, x.Obj())
		}
	}
	return nil
}

func (e *encoder) header(open string, path []string, close string) error {
	if e.b.Len() > 0 {
		e.b.WriteByte('\n')
	}
	e.b.WriteString(open)
	for i, p := range path {
		if i > 0 {
			e.b.WriteByte('.')
		}
		if err := e.key(p); err != nil {
			return err
		}
	}
	e.b.WriteString(close)
	e.b.WriteByte('\n')
	return nil
}

var bare = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func (e *encoder) key(k string) error {
	if bare.MatchString(k) {
		e.b.WriteString(k)
		return nil
	}
	return e.str(k)
}

// value writes v inline: a vector as an array, a map as an inline table.
func (e *encoder) value(v value.Value, depth int) error {
	switch v.Kind() {
	case value.Bool:
		e.b.WriteString(strconv.FormatBool(v.AsBool()))
	case value.Int:
		e.b.WriteString(strconv.FormatInt(v.AsInt(), 10))
	case value.Float:
		e.b.WriteString(value.FormatFloat(v.AsFloat())) // inf, -inf and nan are TOML too
	case value.String:
		return e.str(v.AsString())
	case value.Vector:
		if err := e.enter(v, depth); err != nil {
			return err
		}
		defer delete(e.open, v.Obj())
		e.b.WriteByte('[')
		for i, x := range v.AsVector().Elems {
			if i > 0 {
				e.b.WriteString(", ")
			}
			if err := e.value(x, depth+1); err != nil {
				return err
			}
		}
		e.b.WriteByte(']')
	case value.Map:
		if err := e.enter(v, depth); err != nil {
			return err
		}
		defer delete(e.open, v.Obj())
		names, vals, err := e.entries(v)
		if err != nil {
			return err
		}
		e.b.WriteByte('{')
		for i, x := range vals {
			if i > 0 {
				e.b.WriteByte(',')
			}
			e.b.WriteByte(' ')
			if err := e.key(names[i]); err != nil {
				return err
			}
			e.b.WriteString(" = ")
			if err := e.value(x, depth+1); err != nil {
				return err
			}
		}
		if len(vals) > 0 {
			e.b.WriteByte(' ')
		}
		e.b.WriteByte('}')
	default:
		if timemod.Time.Is(v) {
			t := v.AsObject().State.(stdtime.Time)
			if t.Year() < 0 || t.Year() > 9999 {
				return e.a.Errorf(value.ValueError, "the year %d has no TOML form (0 to 9999)", t.Year())
			}
			e.b.WriteString(t.Format("2006-01-02T15:04:05.999999999Z07:00"))
			return nil
		}
		if timemod.Time.Derived(v) {
			return e.a.NotSetUp("the value", timemod.Time, v)
		}
		return e.a.Errorf(value.TypeError, "cannot encode %s as TOML", native.TypeName(v))
	}
	return nil
}

// str writes s as a TOML basic string.
func (e *encoder) str(s string) error {
	if !utf8.ValidString(s) {
		shown := s
		if len(shown) > 40 { // the string may be long: its start
			shown = shown[:40]
			return e.a.Errorf(value.ValueError, "string %s... (%d bytes) is not valid UTF-8", strconv.Quote(shown), len(s))
		}
		return e.a.Errorf(value.ValueError, "string %s is not valid UTF-8", strconv.Quote(shown))
	}
	e.b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"' || c == '\\':
			e.b.WriteByte('\\')
			e.b.WriteByte(c)
		case c == '\n':
			e.b.WriteString(`\n`)
		case c == '\r':
			e.b.WriteString(`\r`)
		case c == '\t':
			e.b.WriteString(`\t`)
		case c < 0x20 || c == 0x7f:
			fmt.Fprintf(&e.b, `\u%04x`, c)
		default:
			e.b.WriteByte(c)
		}
	}
	e.b.WriteByte('"')
	return nil
}
