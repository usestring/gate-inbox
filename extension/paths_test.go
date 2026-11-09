package extension

import (
	"strings"
	"testing"
)

// An extension's paths are the board's: the same hash for the same path, so
// an event and a span about one directory join.
func TestExtensionsHashPathsAsTheBoardDoes(t *testing.T) {
	dir := "/" + strings.Join([]string{"home", "someone", "repo"}, "/")
	got := HidePaths("cannot read " + dir + "/x.go: denied")
	if got != "cannot read "+HashPath(dir+"/x.go")+": denied" || !strings.HasPrefix(HashPath(dir+"/x.go"), HashPath(dir)+"/") {
		t.Errorf("HidePaths = %q", got)
	}
	if HidePaths(dir) != HashPath(dir) || HashPath("") != "" {
		t.Error("a path alone should go as its hash, and empty stay empty")
	}
}
