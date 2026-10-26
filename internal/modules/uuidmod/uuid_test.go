package uuidmod

import (
	"strings"
	"testing"
	"time"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

func TestUUIDs(t *testing.T) {
	a := V7()
	time.Sleep(2 * time.Millisecond)
	b := V7()
	if !Valid(a) || !Valid(b) || !(a < b) || a[14] != '7' {
		t.Errorf("version 7: %s, %s", a, b)
	}
	for _, s := range []string{"", "x", "0192f4a0-7c1d-7e2f-8a3b-4c5d6e7f8a9", "0192f4a0x7c1d-7e2f-8a3b-4c5d6e7f8a9b", "0192f4a0-7c1d-7e2f-8a3b-4c5d6e7f8a9g", "{0192f4a0-7c1d-7e2f-8a3b-4c5d6e7f8a9b}"} {
		if Valid(s) {
			t.Errorf("%q is valid", s)
		}
	}
	if !Valid("0192F4A0-7C1D-7E2F-8A3B-4C5D6E7F8A9B") {
		t.Error("upper case")
	}
}

// Versions 5 and 3: the examples of RFC 9562 (appendix A.4 and A.2), and
// the namespace in either case.
func TestNamed(t *testing.T) {
	m, _ := native.Lookup("uuid")
	call := func(f string, args ...string) (string, error) {
		var vs []value.Value
		for _, a := range args {
			vs = append(vs, value.MakeString(a))
		}
		v, err := m.Funcs[f](nil, vs)
		if err != nil {
			return "", err
		}
		return v.AsString(), nil
	}
	dns := m.Consts["DNS"].AsString()
	for _, c := range []struct{ f, ns, want string }{
		{"v5", dns, "2ed6657d-e927-568b-95e1-2665a8aea6a2"},
		{"v3", dns, "5df41881-3aed-3515-88a7-2f4a814cf09e"},
		{"v5", strings.ToUpper(dns), "2ed6657d-e927-568b-95e1-2665a8aea6a2"},
	} {
		if got, err := call(c.f, c.ns, "www.example.com"); err != nil || got != c.want {
			t.Errorf("%s(%s): %s, %v", c.f, c.ns, got, err)
		}
	}
	for name, want := range map[string]string{"DNS": "6ba7b810-9dad-11d1-80b4-00c04fd430c8", "URL": "6ba7b811-9dad-11d1-80b4-00c04fd430c8",
		"OID": "6ba7b812-9dad-11d1-80b4-00c04fd430c8", "X500": "6ba7b814-9dad-11d1-80b4-00c04fd430c8"} {
		if got := m.Consts[name].AsString(); got != want {
			t.Errorf("uuid::%s is %s", name, got)
		}
	}
	if _, err := call("v5", "dns", "x"); err == nil || !strings.Contains(err.Error(), `uuid::v5: the namespace "dns" is not a UUID`) {
		t.Errorf("a bad namespace: %v", err)
	}
}
