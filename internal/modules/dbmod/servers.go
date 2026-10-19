package dbmod

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// The drivers are in files of their own, each left out by a build tag:
// nosqlite, nopostgres, nomysql (driver_*.go). drivers holds the ones this
// kukel has.
var drivers = map[dialect]bool{}

var dialectNames = map[dialect]string{sqlite: "SQLite", postgres: "PostgreSQL", mysqlDB: "MySQL"}

func addDriver(d dialect) bool {
	drivers[d] = true
	return true
}

// Kinds names the databases this kukel can open, in order.
func Kinds() []string {
	var names []string
	for _, d := range []dialect{sqlite, postgres, mysqlDB} {
		if drivers[d] {
			names = append(names, dialectNames[d])
		}
	}
	return names
}

// Has reports whether this kukel can open databases of kind ("SQLite",
// "PostgreSQL" or "MySQL").
func Has(kind string) bool {
	for _, k := range Kinds() {
		if k == kind {
			return true
		}
	}
	return false
}

// mysqlDSN makes the data source name of the MySQL driver from a URL
// (driver_mysql.go; nil without it).
var mysqlDSN func(p *url.URL) string

// The databases db::open reaches (TODO.md Q54): SQLite in a file or in
// memory, and PostgreSQL and MySQL (or MariaDB) servers. Programs write ?
// for parameters everywhere; for PostgreSQL they become $1, $2, ...

// dialect is the kind of database of a connection.
type dialect int

const (
	sqlite dialect = iota
	postgres
	mysqlDB
)

// source is how to open a database: the driver, its data source name, the
// dialect, and the URL as a connection prints it (without a password).
type source struct {
	driver, dsn string
	dialect     dialect
	shown       string
}

// sourceOf reads a database URL: "sqlite:path", "sqlite::memory:",
// "postgres://user:password@host:port/database?options" (also
// "postgresql://"), "mysql://user:password@host:port/database?options".
func sourceOf(u string) (source, error) {
	if path, ok := strings.CutPrefix(u, "sqlite:"); ok && path != "" {
		// Foreign keys are checked, and a database another program is
		// writing is waited for up to 5 seconds instead of failing at
		// once. The path is part of a URI: %, ? and # in it are escaped.
		dsn := "file:" + uriEscape.Replace(path) + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
		if path == ":memory:" {
			dsn = ":memory:?_pragma=foreign_keys(1)"
		}
		return source{"sqlite", dsn, sqlite, u}, nil
	}
	shown := password.ReplaceAllString(u, "${1}***@")
	p, err := url.Parse(u)
	switch {
	case err == nil && p.Host == "" && (p.Scheme == "postgres" || p.Scheme == "postgresql" || p.Scheme == "mysql"):
		return source{}, fmt.Errorf("%q has no host: a %s:// URL needs one (%s://user:password@host/database)", shown, p.Scheme, p.Scheme)
	case err != nil || p.Host == "":
		return source{}, fmt.Errorf("%q is not a database URL: use \"sqlite:file.db\", \"sqlite::memory:\", \"postgres://user:password@host/database\" or \"mysql://user:password@host/database\"", shown)
	}
	switch p.Scheme {
	case "postgres", "postgresql":
		return source{"pgx", u, postgres, shown}, nil
	case "mysql":
		dsn := ""
		if mysqlDSN != nil {
			dsn = mysqlDSN(p)
		}
		return source{"mysql", dsn, mysqlDB, shown}, nil
	}
	return source{}, fmt.Errorf("%q is not a database URL: the kinds are sqlite:, postgres:// and mysql://", shown)
}

// password finds the password of a URL ("//user:password@"), which
// messages and printed connections leave out.
var password = regexp.MustCompile(`(//[^/:@]*:)[^@/]*@`)

// sqlFor gives the SQL text for the database: for PostgreSQL each ? that
// is a parameter (not inside a quoted string or name, or a comment)
// becomes $1, $2, ..., numbered after the highest $n the text has itself.
func (c *conn) sqlFor(text string) string {
	if c.dialect != postgres || !strings.Contains(text, "?") {
		return text
	}
	out, high := numbered(text, 0)
	if high > 0 {
		out, _ = numbered(text, high)
	}
	return out
}

// numbered turns each ? parameter of text into $n, counting from after;
// it also gives the highest $n written in text.
func numbered(text string, after int) (string, int) {
	var b strings.Builder
	n, high := after, 0
	for i := 0; i < len(text); i++ {
		ch := text[i]
		switch {
		case ch == '\'' || ch == '"': // a string, or a quoted name: up to the closing quote ('' and "" are inside)
			// E'...' is a string with backslash escapes (\' inside).
			escapes := ch == '\'' && i > 0 && (text[i-1] == 'E' || text[i-1] == 'e') && (i == 1 || !isNameByte(text[i-2]))
			j := i + 1
			for j < len(text) {
				if escapes && text[j] == '\\' {
					j += 2
					continue
				}
				if text[j] == ch {
					if j+1 < len(text) && text[j+1] == ch {
						j += 2
						continue
					}
					break
				}
				j++
			}
			b.WriteString(text[i:min(j+1, len(text))])
			i = j
		case ch == '-' && i+1 < len(text) && text[i+1] == '-': // a comment to the end of the line
			j := strings.IndexByte(text[i:], '\n')
			if j < 0 {
				j = len(text) - i - 1
			}
			b.WriteString(text[i : i+j+1])
			i += j
		case ch == '/' && i+1 < len(text) && text[i+1] == '*':
			j := strings.Index(text[i+2:], "*/")
			end := len(text)
			if j >= 0 {
				end = i + 2 + j + 2
			}
			b.WriteString(text[i:end])
			i = end - 1
		case ch == '$' && dollarTag(text[i:]) != "": // $tag$ ... $tag$
			tag := dollarTag(text[i:])
			j := strings.Index(text[i+len(tag):], tag)
			end := len(text)
			if j >= 0 {
				end = i + len(tag) + j + len(tag)
			}
			b.WriteString(text[i:end])
			i = end - 1
		case ch == '$' && i+1 < len(text) && isDigit(text[i+1]) && (i == 0 || !isNameByte(text[i-1])): // $n
			j := i + 1
			for j < len(text) && isDigit(text[j]) {
				j++
			}
			if k, err := strconv.Atoi(text[i+1 : j]); err == nil {
				high = max(high, k)
			}
			b.WriteString(text[i:j])
			i = j - 1
		case ch == '?':
			n++
			b.WriteString("$" + strconv.Itoa(n))
		default:
			b.WriteByte(ch)
		}
	}
	return b.String(), high
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// dollarTag gives the $tag$ (or $$) that starts s, or "".
func dollarTag(s string) string {
	for i := 1; i < len(s); i++ {
		switch c := s[i]; {
		case c == '$':
			return s[:i+1]
		case !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 1 && c >= '0' && c <= '9'):
			return ""
		}
	}
	return ""
}

// quote quotes an SQL name (tables and columns are Kukel names: no quotes
// inside): "name", or `name` for MySQL.
func (c *conn) quote(name string) string {
	if c.dialect == mysqlDB {
		return "`" + name + "`"
	}
	return `"` + name + `"`
}

// keyType is the column type of id that the database fills in.
func (c *conn) keyType() string {
	switch c.dialect {
	case postgres:
		return "BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY"
	case mysqlDB:
		return "BIGINT AUTO_INCREMENT PRIMARY KEY"
	}
	return "INTEGER PRIMARY KEY"
}

// columnsSQL is a query for the names of the columns of a table (the
// parameter), or no rows if there is no such table.
func (c *conn) columnsSQL() string {
	switch c.dialect {
	case postgres:
		return "SELECT column_name FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = ?"
	case mysqlDB:
		return "SELECT column_name FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ?"
	}
	return "SELECT name FROM pragma_table_info(?)"
}

func isNameByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// uuidType is the column type for "UUID" in columns(): PostgreSQL has one,
// the others keep the text form.
func (c *conn) uuidType(key bool) string {
	t := "CHAR(36)"
	if c.dialect == postgres {
		t = "UUID"
	}
	if key {
		t += " PRIMARY KEY"
	}
	return t
}
