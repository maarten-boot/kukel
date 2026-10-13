package loader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tree writes files (path relative to a new temporary directory: source)
// and returns the directory.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// order lists the loaded files as "module:file" (file relative to dir).
func order(t *testing.T, dir string, files []string, path ...string) string {
	t.Helper()
	for i := range files {
		files[i] = filepath.Join(dir, files[i])
	}
	for i := range path {
		path[i] = filepath.Join(dir, path[i])
	}
	got, err := Load(files, path)
	if err != nil {
		t.Fatal(err)
	}
	var s []string
	for _, f := range got {
		rel, _ := filepath.Rel(dir, f.Name)
		s = append(s, f.Module+":"+filepath.ToSlash(rel))
	}
	return strings.Join(s, " ")
}

func TestLoadOrder(t *testing.T) {
	dir := tree(t, map[string]string{
		"main.kkl":     "use a; use b; main() {}",
		"more.kkl":     "use b; f() {}",
		"a.kkl":        "use c; fa() {}",
		"b.kkl":        "use c; fb() {}",
		"c.kkl":        "fc() {}",
		"lib/d.kkl":    "use e; fd() {}", // e is found next to d
		"lib/e.kkl":    "fe() {}",
		"other/e.kkl":  "fe() {}",
		"withlib.kkl":  "use d; main() {}",
		"shadow/a.kkl": "fa() {}",
	})
	// Dependencies first, each module once, then the program's files.
	if got, want := order(t, dir, []string{"main.kkl", "more.kkl"}), "c:c.kkl a:a.kkl b:b.kkl :main.kkl :more.kkl"; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	// The search path; a module's own directory comes before it.
	if got, want := order(t, dir, []string{"withlib.kkl"}, "other", "lib"), "e:lib/e.kkl d:lib/d.kkl :withlib.kkl"; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	// The directory of the using file comes before the search path.
	if got, want := order(t, dir, []string{"main.kkl"}, "shadow"), "c:c.kkl a:a.kkl b:b.kkl :main.kkl"; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestLoadErrors(t *testing.T) {
	dir := tree(t, map[string]string{
		"missing.kkl": "use nosuch; main() {}",
		"cycle.kkl":   "use x; main() {}",
		"x.kkl":       "use y; f() {}",
		"y.kkl":       "use x; g() {}",
		"self.kkl":    "use self; main() {}",
		"twice.kkl":   "use dup; use sub; main() {}",
		"dup.kkl":     "f() {}",
		"sub/sub.kkl": "use dup; g() {}",
		"sub/dup.kkl": "h() {}",
		"both.kkl":    "use dup; main() {}",
		"bad.kkl":     "use broken; main() {}",
		"broken.kkl":  "f( {}",
		"selfuse.kkl": "use selfuse; main() {}",
		"errmod.kkl":  "use Error; main() {}",
		"sub/me.kkl":  "use me; f() {}",
		"useme.kkl":   "use me; main() {}",
	})
	tests := []struct {
		files []string
		msg   string
	}{
		{[]string{"missing.kkl"}, "missing.kkl:1:5: module nosuch not found: no nosuch.kkl in " + dir},
		{[]string{"cycle.kkl"}, "y.kkl:1:5: modules use each other in a cycle: x -> y -> x"},
		{[]string{"self.kkl"}, "self.kkl:1:5: " + filepath.Join(dir, "self.kkl") + " is both a file of the program and module self"},
		{[]string{"twice.kkl"}, "sub.kkl:1:5: module dup is " + filepath.Join(dir, "sub", "dup.kkl") + " here, but " + filepath.Join(dir, "dup.kkl") + " elsewhere in the program"},
		{[]string{"both.kkl", "dup.kkl"}, "both.kkl:1:5: " + filepath.Join(dir, "dup.kkl") + " is both a file of the program and module dup"},
		{[]string{"bad.kkl"}, "broken.kkl:1:4: "},
		{[]string{"nofile.kkl"}, "nofile.kkl"},
		{[]string{"selfuse.kkl"}, "selfuse.kkl is both a file of the program and module selfuse"}, // a program file that uses itself
		{[]string{"errmod.kkl"}, "errmod.kkl:1:5: a module cannot be called Error"},
		{[]string{"useme.kkl"}, "me.kkl:1:5: module me cannot use itself"},
	}
	for _, tc := range tests {
		var files []string
		for _, f := range tc.files {
			files = append(files, filepath.Join(dir, f))
		}
		_, err := Load(files, []string{filepath.Join(dir, "sub")})
		if err == nil || !strings.Contains(err.Error(), tc.msg) {
			t.Errorf("%v: got %v, want an error containing %q", tc.files, err, tc.msg)
		}
	}
}

// An empty entry in the search path is skipped, not the current directory.
func TestEmptyPathEntry(t *testing.T) {
	dir := tree(t, map[string]string{"prog/main.kkl": "use cwdmod; main() {}", "cwdmod.kkl": "f() {}"})
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil { // cwdmod.kkl is in the current directory
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	_, err = Load([]string{filepath.Join(dir, "prog", "main.kkl")}, []string{"", filepath.Join(dir, "none")})
	if err == nil || !strings.Contains(err.Error(), "module cwdmod not found") {
		t.Errorf("got %v", err)
	}
}
