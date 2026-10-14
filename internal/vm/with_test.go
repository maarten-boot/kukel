package vm_test

import "testing"

// with closes its values however the body is left, the last one first;
// the body's exception wins over a failing close.
func TestWith(t *testing.T) {
	src := `
class R { n; bad; R(n, bad); close(); }
R::R(n, bad = false) { this->n = n; this->bad = bad; }
R::close() { print("close " + n + "; "); if (bad) throw new Error("close " + n, "CloseError"); }
ret() { with (local a = new R("r")) return "ret"; }
main() {
    with (local a = new R("a"), b = new R("b")) print("body; ");
    println();
    println(ret());
    for (local i = 0; i < 3; i++) with (local x = new R(str(i))) { if (i == 0) continue; if (i == 1) break; }
    println();
    outer: { with (local y = new R("y")) break outer; }
    println();
    try { with (local a = new R("a", true)) throw new Error("body", "BodyError"); } catch (e) { println(e->kind); }
    try { with (local a = new R("a", true)) print("ok; "); } catch (e) { println(e->kind); }
    try { with (local a = new R("a"), b = fail()) print("never"); } catch (e) { println(e->message); }
    with (local n = nil, m = new R("m")) print("nil skipped; ");
    println();
    local f;
    with (local c = new R("c")) f = fn() { return c->n; };   // captured: still the value after with
    println(f());
}
fail() { throw new Error("no b"); }`
	want := "body; close b; close a; \nclose r; ret\nclose 0; close 1; \nclose y; \nclose a; BodyError\nok; close a; CloseError\nclose a; no b\nnil skipped; close m; \nclose c; c\n"
	if out, _, err := runSource(t, src); err != nil || out != want {
		t.Errorf("got %q, %v\nwant %q", out, err, want)
	}
}
