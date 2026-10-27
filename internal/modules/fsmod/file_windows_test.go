//go:build windows

package fsmod_test

import "testing"

// What Windows does its own way: chmod changes only the read-only flag,
// locks are mandatory (a locked file cannot be read through another
// handle), and a file opened "a" can be locked.
func TestFilesWindows(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runIO(t, `use fs;

main() {
    local f = fs::temp("w-*.txt", ".");
    f->write("abc")->close();
    fs::chmod(f->name(), "r--r--r--");
    println(fs::stat(f->name())["mode"]);

    try {
        fs::open(f->name(), "w");
    } catch (e) {
        println(e->kind);
    }
    fs::chmod(f->name(), "rw-------");
    println(fs::stat(f->name())["mode"]);
    local a = fs::open(f->name(), "r+"), b = fs::open(f->name());
    println(a->lock(), b->lock("shared", 0));

    try {
        b->read();
    } catch (e) {
        println(e->kind);
    }
    a->unlock();
    println(b->read());

    with (local out = fs::open("log.txt", "a")) {
        println(out->lock());
        out->println("x");
    }
    println(readfile("log.txt") == "x\n");
}
`, "")
	want := "-r--r--r--\nIOError\n-rw-rw-rw-\ntrue false\nIOError\nabc\ntrue\ntrue\n"
	if err != nil || got != want {
		t.Errorf("End: %v; got:\n%s\nwant:\n%s", err, got, want)
	}
}
