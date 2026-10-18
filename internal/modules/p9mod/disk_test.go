//go:build !no9p

package p9mod_test

import (
	"os"
	"path/filepath"
	"testing"

	"9fans.net/go/plan9"
	plan9client "9fans.net/go/plan9/client"
)

// diskDir makes a directory to serve, next to a file that must stay out
// of reach: top/{a.txt, sub/b.txt, in -> a.txt, out -> ../secret,
// abs -> /etc/passwd}, and secret.
func diskDir(t *testing.T) string {
	base := t.TempDir()
	top := filepath.Join(base, "top")
	must(t, os.MkdirAll(filepath.Join(top, "sub"), 0o755))
	must(t, os.WriteFile(filepath.Join(top, "a.txt"), []byte("alpha\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(top, "sub", "b.txt"), []byte("beta\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(base, "secret"), []byte("secret\n"), 0o644))
	must(t, os.Symlink("a.txt", filepath.Join(top, "in")))
	must(t, os.Symlink("../secret", filepath.Join(top, "out")))
	must(t, os.Symlink("/etc/passwd", filepath.Join(top, "abs")))
	return top
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// A disk served read-only: what is inside can be read, nothing outside,
// and nothing can be changed.
func TestDiskReadOnly(t *testing.T) {
	top := diskDir(t)
	got := run(t, `use p9;
main(args) {
    local d = p9::disk(args[0]);
    local s = p9::serve(d, "tcp://127.0.0.1:0");
    println(str(d) == "p9::Disk(" + args[0] + ", read-only)");
    for (local v in ["9P2000", "9P2000.L"]) {
        local c = p9::dial(s->endpoint(), {"version": v});
        println("--", v);
        for (local e in c->readdir("/")) println(e["name"], e["size"], e["dir"], e["mode"]);
        print(c->read("a.txt"), c->read("/sub/b.txt"), c->read("in"), c->read("/sub/../../../a.txt"));
        for (local f in [fn() { c->read("out"); }, fn() { c->read("abs"); }, fn() { c->read("nope"); },
                         fn() { c->write("a.txt", "x"); }, fn() { c->write("new.txt", "x"); }, fn() { c->mkdir("d"); },
                         fn() { c->remove("a.txt"); }, fn() { c->rename("a.txt", "z.txt"); }])
            try { f(); } catch (e) { println(e->message); }
        c->close();
    }
    s->close();
}`, top)
	want := `true
-- 9P2000
a.txt 6 false -r--r--r--
in 6 false -r--r--r--
sub 0 true dr-xr-xr-x
alpha
 beta
 alpha
 alpha
p9::Client::read: out: permission denied
p9::Client::read: abs: permission denied
p9::Client::read: nope: file does not exist
p9::Client::write: a.txt: read-only file system
p9::Client::write: new.txt: read-only file system
p9::Client::mkdir: d: read-only file system
p9::Client::remove: a.txt: read-only file system
p9::Client::rename: a.txt: read-only file system
-- 9P2000.L
a.txt 6 false -r--r--r--
in 6 false -r--r--r--
sub 0 true dr-xr-xr-x
alpha
 beta
 alpha
 alpha
p9::Client::read: out: permission denied
p9::Client::read: abs: permission denied
p9::Client::read: nope: no such file or directory
p9::Client::write: a.txt: read-only file system
p9::Client::write: new.txt: read-only file system
p9::Client::mkdir: d: read-only file system
p9::Client::remove: a.txt: read-only file system
p9::Client::rename: a.txt: read-only file system
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	// A client that walks ".." itself stays at the top.
	sock := filepath.Join(t.TempDir(), "s")
	done := make(chan string)
	go func() {
		out, err := runErr(t, `use p9; use events;
main(args) {
    local t = new p9::Tree(), s = nil;
    t->mount("/disk", p9::disk(args[0]));
    t->file("/stop", "", fn(data) { s->close(); });
    s = p9::serve(t, "unix://" + args[1]);
    events::run();
    println(s->closed());
}`, top, sock)
		if err != nil {
			out += err.Error()
		}
		done <- out
	}()
	waitFor(t, sock)
	fsys, err := plan9client.Mount("unix", sock)
	must(t, err)
	for _, p := range []string{"disk/../../../secret", "disk/sub/../../secret", "disk/out", "disk/abs"} {
		if f, err := fsys.Open(p, plan9.OREAD); err == nil {
			f.Close()
			t.Errorf("%s could be opened", p)
		}
	}
	f, err := fsys.Open("disk/sub/../../disk/a.txt", plan9.OREAD)
	must(t, err)
	f.Close()
	f, err = fsys.Open("stop", plan9.OWRITE|plan9.OTRUNC)
	must(t, err)
	f.Close()
	if out := <-done; out != "true\n" {
		t.Errorf("server: %s", out)
	}
}

func waitFor(t *testing.T, sock string) {
	for i := 0; i < 500; i++ {
		if _, err := os.Stat(sock); err == nil {
			return
		}
		<-timeAfter10ms()
	}
	t.Fatal("the server did not start")
}

// A writable disk, mounted in a tree, in both versions.
func TestDiskWritable(t *testing.T) {
	top := diskDir(t)
	got := run(t, `use p9;
main(args) {
    local t = new p9::Tree();
    t->file("/README", "files are in /data\n");
    t->mount("/data", p9::disk(args[0], {"readonly": false}));
    local s = p9::serve(t, "tcp://127.0.0.1:0");
    for (local v in ["9P2000", "9P2000.L"]) {
        local c = p9::dial(s->endpoint(), {"version": v});
        println("--", v);
        for (local e in c->readdir("/")) println(e["name"], e["dir"]);
        c->write("/data/new.txt", "made with " + v);
        c->write("/data/a.txt", "changed with " + v);
        c->mkdir("/data/dir");
        c->write("/data/dir/x", "x");
        c->rename("/data/dir/x", "/data/dir/y");
        println(c->read("/data/new.txt"), c->read("/data/a.txt"), c->read("/data/dir/y"), c->stat("/data/dir")["dir"]);
        try {
            c->rename("/data/dir/y", "/data/y");   // 9P2000: only within a directory
            println("moved", c->read("/data/y"));
        } catch (e) {
            println(e->message);
            c->remove("/data/dir/y");
        }
        c->remove("/data/dir");
        for (local f in [fn() { c->remove("/data/sub"); }, fn() { c->remove("/README"); }, fn() { c->write("/x", "x"); },
                         fn() { c->rename("/data", "/data2"); }, fn() { c->mkdir("/data/sub"); }])
            try { f(); } catch (e) { println(e->message); }
        c->remove("/data/new.txt");
        println(c->exists("/data/new.txt"));
        c->close();
    }
    for (local f in [fn() { t->file("/data/x", "x"); }, fn() { t->dir("/data/sub/y"); }])
        try { f(); } catch (e) { println(e->message); }
    s->close();
}`, top)
	want := `-- 9P2000
README false
data true
made with 9P2000 changed with 9P2000 x true
p9::Client::rename: /data/dir/y: 9P2000 renames only within a directory
p9::Client::remove: /data/sub: directory not empty
p9::Client::remove: /README: permission denied: the tree is made by the server program
p9::Client::write: /x: permission denied: the tree is made by the server program
p9::Client::rename: /data: permission denied: the tree is made by the server program
p9::Client::mkdir: /data/sub: file exists
false
-- 9P2000.L
README false
data true
made with 9P2000.L changed with 9P2000.L x true
moved x
p9::Client::remove: /data/sub: directory not empty
p9::Client::remove: /README: permission denied
p9::Client::write: /x: permission denied
p9::Client::rename: /data: permission denied
p9::Client::mkdir: /data/sub: file exists
false
p9::Tree::file: /data is a mounted disk directory: the tree does not reach into it
p9::Tree::dir: /data is a mounted disk directory: the tree does not reach into it
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	b, err := os.ReadFile(filepath.Join(top, "a.txt"))
	if err != nil || string(b) != "changed with 9P2000.L" {
		t.Errorf("a.txt: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(top, "y")); err != nil {
		t.Errorf("y, renamed by 9P2000.L: %v", err)
	}
	for _, gone := range []string{"new.txt", "dir"} {
		if _, err := os.Stat(filepath.Join(top, gone)); err == nil {
			t.Errorf("%s is still there", gone)
		}
	}
}

func TestDiskErrors(t *testing.T) {
	top := diskDir(t)
	got := run(t, `use p9;
main(args) {
    for (local f in [fn() { p9::disk(args[0] + "/nope"); },
                     fn() { p9::disk(args[0] + "/a.txt"); },
                     fn() { p9::disk(args[0], {"readonly": 1}); },
                     fn() { p9::disk(args[0], {"writable": true}); },
                     fn() { p9::disk(args[0], 5); },
                     fn() { local t = new p9::Tree(); t->file("/f", "x"); t->mount("/", p9::disk(args[0])); },
                     fn() { local t = new p9::Tree(); t->mount("/m", 5); }])
        try { f(); } catch (e) { println(e->kind + ":", replace(e->message, args[0], "TOP")); }
    local t = new p9::Tree();
    t->file("/m/old", "x");
    t->mount("/m", p9::disk(args[0], {"readonly": false}));   // replaces /m
    local top = new p9::Tree();
    top->mount("/", p9::disk(args[0]));
    println(t, top, replace(str(p9::disk(args[0], {"readonly": false})), args[0], "TOP"));
}`, top)
	want := `IOError: p9::disk: open TOP/nope: no such file or directory
IOError: p9::disk: open TOP/a.txt: not a directory
TypeError: p9::disk: option readonly must be true or false, not int
ValueError: p9::disk: unknown option "writable" (the option is readonly)
TypeError: p9::disk: argument 2 must be a map of options, not int
ValueError: p9::Tree::mount: the tree is not empty: mount at the top only on an empty tree
TypeError: p9::Tree::mount: argument 2 must be a p9::Disk, not int
p9::Tree(0 files) p9::Tree(0 files) p9::Disk(TOP)
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
