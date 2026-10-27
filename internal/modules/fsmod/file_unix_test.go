//go:build unix

package fsmod_test

import (
	"syscall"
	"testing"
)

// On a FIFO open both ways a write after a read needs no seek (what was
// read ahead stays to be read).
func TestFilePipes(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := syscall.Mkfifo("fifo", 0o644); err != nil {
		t.Skip("no FIFO:", err)
	}
	got, err := runIO(t, `use fs;

main() {
    local p = fs::open("fifo", "r+");
    p->write("one\n", "two\n")->flush();
    println(p->readline());
    p->write("three\n")->flush();
    println(p->readline(), p->readline());
}
`, "")
	if err != nil || got != "one\ntwo three\n" {
		t.Errorf("End: %v; got %q", err, got)
	}
}
