package dbmod_test

import (
	"os"
	"testing"
)

// TestServers runs testdata/servers.kkl on SQLite and on the database
// servers that KUKEL_TEST_POSTGRES and KUKEL_TEST_MYSQL name (a URL such as
// "postgres://postgres:secret@127.0.0.1:5432/test?sslmode=disable" or
// "mysql://root:secret@127.0.0.1:3306/test"; its tables item, tag and plain
// are dropped): each must print what SQLite prints.
func TestServers(t *testing.T) {
	src, err := os.ReadFile("testdata/servers.kkl")
	if err != nil {
		t.Fatal(err)
	}
	want := run(t, string(src), "sqlite::memory:")
	if want == "" {
		t.Fatal("no output on SQLite")
	}
	for _, env := range []string{"KUKEL_TEST_POSTGRES", "KUKEL_TEST_MYSQL"} {
		t.Run(env, func(t *testing.T) {
			u := os.Getenv(env)
			if u == "" {
				t.Skip(env + " names no database")
			}
			if got := run(t, string(src), u); got != want {
				t.Errorf("got:\n%s\nwant (as on SQLite):\n%s", got, want)
			}
		})
	}
}
