// Package p9mod is the native module "p9": the 9P file protocol of Plan 9
// and Linux (TODO.md Q62), as a client and as a server, in both versions:
// 9P2000 and 9P2000.L.
//
//	use p9;
//	local c = p9::dial("tcp://localhost:5640");
//	for (local e in c->readdir("/")) println(e["name"], e["size"]);
//	println(c->read("/motd"));
//	c->close();
//
// A server serves a tree the program builds; its files hold a string, or
// call a Kukel function when they are read or written:
//
//	local t = new p9::Tree();
//	t->file("/motd", "hello\n");
//	t->file("/time", fn() { return string(new time::Time()) + "\n"; });
//	t->file("/log", "", fn(data) { print(data); });
//	local s = p9::serve(t, "tcp://*:5640");
//	events::run();
//
// 9P2000.L is served and spoken with github.com/hugelgupf/p9, 9P2000 with
// 9fans.net/go/plan9. The build tag no9p leaves the module out (its code is
// in the files with the build constraint !no9p).
package p9mod
