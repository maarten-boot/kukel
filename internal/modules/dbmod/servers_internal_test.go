//go:build !nomysql

package dbmod

import (
	"strings"
	"testing"
)

func TestSourceOf(t *testing.T) {
	tests := []struct{ url, driver, dsn, shown string }{
		{"sqlite::memory:", "sqlite", ":memory:?_pragma=foreign_keys(1)", "sqlite::memory:"},
		{"postgres://u:pw@db.example:5433/shop?sslmode=disable", "pgx", "postgres://u:pw@db.example:5433/shop?sslmode=disable", "postgres://u:***@db.example:5433/shop?sslmode=disable"},
		{"postgresql://db.example/shop", "pgx", "postgresql://db.example/shop", "postgresql://db.example/shop"},
		{"mysql://u:pw@db.example/shop", "mysql", "u:pw@tcp(db.example:3306)/shop?parseTime=true", "mysql://u:***@db.example/shop"},
		{"mysql://u:pw@db.example:3307/shop?tls=true", "mysql", "u:pw@tcp(db.example:3307)/shop?parseTime=true&tls=true", "mysql://u:***@db.example:3307/shop?tls=true"},
	}
	for _, tc := range tests {
		s, err := sourceOf(tc.url)
		if err != nil || s.driver != tc.driver || s.dsn != tc.dsn || s.shown != tc.shown {
			t.Errorf("%s: got %+v, %v", tc.url, s, err)
		}
	}
	for _, bad := range []string{"shop.db", "sqlite:", "oracle://u:pw@h/db", "postgres:///db"} {
		if _, err := sourceOf(bad); err == nil {
			t.Errorf("%s: no error", bad)
		} else if s := err.Error(); strings.Contains(s, "pw@") {
			t.Errorf("%s: the message shows the password: %s", bad, s)
		}
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
