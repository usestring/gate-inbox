package handover

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// omp writes pi's tree behind a title line of its own, which has no id and
// so passes through first, where omp reads it back. The conversation's path
// is kept whole, side calls and renames included.
func TestPiFiltersAnOmpSession(t *testing.T) {
	src := filepath.Join("..", "convo", "testdata", "omp-19.0.1", "named.jsonl")
	if !Reads("omp") {
		t.Fatal("the filter does not read omp's layout")
	}
	dst := filepath.Join(t.TempDir(), "copy.jsonl")
	stats, err := Pi(src, dst, Options{})
	if err != nil {
		t.Fatal(err)
	}
	in, out := typesOf(t, src), typesOf(t, dst)
	if len(out) == 0 || out[0] != "title" || out[1] != "session" {
		t.Fatalf("copy opens %v, want the title line then the header", out)
	}
	for _, want := range []string{"title_change", "message"} {
		if count(out, want) != count(in, want) {
			t.Errorf("%s entries: %d of %d kept", want, count(out, want), count(in, want))
		}
	}
	// One side call (65f73b19) hangs off the user's message rather than the
	// path's leaf; it is the only entry off that path.
	if stats.Kept != len(in)-1 {
		t.Errorf("kept %d of %d lines", stats.Kept, len(in))
	}
}

func typesOf(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var types []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(nil, 1<<20)
	for scanner.Scan() {
		var line struct{ Type string }
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		types = append(types, line.Type)
	}
	return types
}

func count(types []string, want string) int {
	n := 0
	for _, got := range types {
		if got == want {
			n++
		}
	}
	return n
}
