//go:build !nomysql

package dbmod

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
)

func TestSourceOf(t *testing.T) {
	tests := []struct{ url, driver, dsn, shown string }{
		{"sqlite::memory:", "sqlite", ":memory:?_pragma=foreign_keys(1)", "sqlite::memory:"},
		{"postgres://u:pw@db.example:5433/shop?sslmode=disable", "pgx", "postgres://u:pw@db.example:5433/shop?sslmode=disable", "postgres://u:***@db.example:5433/shop?sslmode=disable"},
		{"postgresql://db.example/shop", "pgx", "postgresql://db.example/shop", "postgresql://db.example/shop"},
		{"mysql://u:pw@db.example/shop", "mysql", "u:pw@tcp(db.example:3306)/shop?clientFoundRows=true&parseTime=true", "mysql://u:***@db.example/shop"},
		{"mysql://u:pw@db.example:3307/shop?tls=true", "mysql", "u:pw@tcp(db.example:3307)/shop?clientFoundRows=true&parseTime=true&tls=true", "mysql://u:***@db.example:3307/shop?tls=true"},
	}
	for _, tc := range tests {
		s, err := sourceOf(tc.url)
		if err != nil || s.driver != tc.driver || s.dsn != tc.dsn || s.shown != tc.shown {
			t.Errorf("%s: got %+v, %v", tc.url, s, err)
		}
	}
	for _, bad := range []string{"shop.db", "sqlite:", "oracle://u:pw@h/db", "postgres:///db", "mysql://u:pw@/db"} {
		if _, err := sourceOf(bad); err == nil {
			t.Errorf("%s: no error", bad)
		} else if s := err.Error(); strings.Contains(s, "pw@") {
			t.Errorf("%s: the message shows the password: %s", bad, s)
		}
	}
}

// What is wrong with a URL is a ValueError before connecting: options the
// driver does not take, a @ in a password (cross-check 10). The messages
// leave out the password.
func TestSourceChecks(t *testing.T) {
	tests := []struct{ url, want string }{
		{"postgres://u:p@ss@host/db", `"postgres://u:***@host/db": write a @ in the user name or password as %40`},
		{"mysql://u:pw@host/db?tls=bogus", `"mysql://u:***@host/db?tls=bogus": invalid value / unknown config name: bogus`},
	}
	if checkPostgres != nil { // not with the tag nopostgres
		tests = append(tests, struct{ url, want string }{"postgres://u:pw@host/db?sslmode=bogus",
			`"postgres://u:***@host/db?sslmode=bogus": failed to configure TLS (sslmode is invalid)`})
	}
	for _, tc := range tests {
		if _, err := sourceOf(tc.url); err == nil || err.Error() != tc.want {
			t.Errorf("%s: got %v, want %s", tc.url, err, tc.want)
		}
	}
}

// Opening fails with the reason in a line, without Go's words around it.
func TestOpenMessage(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	_, err = net.Dial("tcp", addr)
	if got := openMessage(fmt.Errorf("failed to connect: %w", err)); got != "connection refused" {
		t.Errorf("got %q", got)
	}
	if got := openMessage(errors.New("failed to connect to `user=u`:\n\thost: server error: FATAL: no\n\thost: server error: FATAL: no")); got != "host: server error: FATAL: no" {
		t.Errorf("got %q", got)
	}
}

// A server URL without a host says so.
func TestSourceNoHost(t *testing.T) {
	_, err := sourceOf("postgres://u:pw@/shop")
	if want := `"postgres://u:***@/shop" has no host: a postgres:// URL needs one (postgres://user:password@host/database)`; err == nil || err.Error() != want {
		t.Errorf("got %v, want %s", err, want)
	}
}

// For PostgreSQL, ? parameters become $1, $2, ... except inside strings,
// quoted names, comments and dollar-quoted text.
func TestSQLForPostgres(t *testing.T) {
	c := &conn{dialect: postgres}
	tests := []struct{ in, want string }{
		{"SELECT ? + ?", "SELECT $1 + $2"},
		{"SELECT '?', 'it''s ?', \"a?\" FROM t WHERE x = ?", "SELECT '?', 'it''s ?', \"a?\" FROM t WHERE x = $1"},
		{"SELECT ? -- what?\n, ? /* why? */ , ?", "SELECT $1 -- what?\n, $2 /* why? */ , $3"},
		{"SELECT $$a?b$$, $tag$?$tag$, ?::text", "SELECT $$a?b$$, $tag$?$tag$, $1::text"},
		{"SELECT $1", "SELECT $1"},
		{"SELECT ?, $2, ? + $1", "SELECT $3, $2, $4 + $1"}, // after the highest $n
		{"SELECT a$1, '$9', ?", "SELECT a$1, '$9', $1"},    // not parameters

		{`SELECT E'it\'s ?', e'\\', ?, name'?'`, `SELECT E'it\'s ?', e'\\', $1, name'?'`},
		{"SELECT 'unterminated ?", "SELECT 'unterminated ?"},
	}
	for _, tc := range tests {
		if got := c.sqlFor(tc.in); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := (&conn{dialect: mysqlDB}).sqlFor("SELECT ?"); got != "SELECT ?" {
		t.Errorf("MySQL: %q", got)
	}
}
