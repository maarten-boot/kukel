package builtins

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/maarten-boot/kukel/internal/token"
)

// TestGeanyWords keeps the word lists of the Geany filetype
// (editors/geany) the language's keywords and built-in functions.
func TestGeanyWords(t *testing.T) {
	conf, err := os.ReadFile("../../editors/geany/filetypes.Kukel.conf")
	if err != nil {
		t.Fatal(err)
	}
	words := func(key string) []string {
		for _, line := range strings.Split(string(conf), "\n") {
			if rest, ok := strings.CutPrefix(line, key+"="); ok {
				w := strings.Fields(rest)
				slices.Sort(w)
				return w
			}
		}
		t.Fatalf("no %s= in filetypes.Kukel.conf", key)
		return nil
	}
	var keywords []string
	for k := token.Kind(0); k < 1000; k++ {
		if k.IsKeyword() {
			keywords = append(keywords, k.String())
		}
	}
	slices.Sort(keywords)
	if got := words("primary"); !slices.Equal(got, keywords) {
		t.Errorf("primary=%v\nwant the keywords %v", got, keywords)
	}
	if got := words("secondary"); !slices.Equal(got, Names()) {
		t.Errorf("secondary=%v\nwant the built-in functions %v", got, Names())
	}
}
