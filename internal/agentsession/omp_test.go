package agentsession

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/convo"
)

// writeOmpSession files a minimal omp conversation for cwd in root: the
// title line, then the header, as omp 19 writes them.
func writeOmpSession(t *testing.T, root, cwd, id string, started time.Time) string {
	t.Helper()
	dir := filepath.Join(root, convo.OmpProjectDirs(cwd)[0])
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, started.UTC().Format("2006-01-02T15-04-05-000Z")+"_"+id+".jsonl")
	body := `{"type":"title","v":1,"title":"","updatedAt":"x","pad":""}` + "\n" +
		`{"type":"session","version":3,"id":"` + id + `","timestamp":"` + started.UTC().Format(time.RFC3339Nano) + `","cwd":"` + cwd + `"}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCaptureOmpTakesTheConversationStartedSinceLaunch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root, cwd := t.TempDir(), t.TempDir()
	store := convo.PiStore{Tool: convo.OmpTool, Root: root}
	launched := time.Now()

	// An old conversation resumed elsewhere is written to after the launch
	// but started long before it.
	old := writeOmpSession(t, root, cwd, "old-0000", launched.Add(-time.Hour))
	if err := os.Chtimes(old, launched, launched); err != nil {
		t.Fatal(err)
	}
	if id, ok := captureOmp(store, cwd, launched, nil); ok {
		t.Fatalf("captured %q before omp wrote the launch's file", id)
	}

	writeOmpSession(t, root, cwd, "new-1111", launched.Add(time.Second))
	if id, ok := captureOmp(store, cwd, launched, nil); !ok || id != "new-1111" {
		t.Fatalf("capture = %q %v, want new-1111", id, ok)
	}

	// A second launch into the directory: two new conversations, one
	// already held by the first session.
	writeOmpSession(t, root, cwd, "new-2222", launched.Add(2*time.Second))
	if id, ok := captureOmp(store, cwd, launched, nil); ok {
		t.Fatalf("captured %q with two candidates and neither claimed", id)
	}
	if id, ok := captureOmp(store, cwd, launched, map[string]bool{"new-1111": true}); !ok || id != "new-2222" {
		t.Fatalf("capture = %q %v, want the unclaimed new-2222", id, ok)
	}
}

func TestRecaptureOmpTakesTheConversationThePickerResumed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root, cwd := t.TempDir(), t.TempDir()
	store := convo.PiStore{Tool: convo.OmpTool, Root: root}
	before := time.Now().Add(-time.Hour)
	a := writeOmpSession(t, root, cwd, "aaaa", before)
	b := writeOmpSession(t, root, cwd, "bbbb", before)
	for _, path := range []string{a, b} {
		if err := os.Chtimes(path, before, before); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, ok := snapshotOmp(store, cwd)
	if !ok || len(snapshot) != 2 {
		t.Fatalf("snapshot = %v %v", snapshot, ok)
	}
	if err := os.Chtimes(b, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := recaptureOmp(store, cwd, snapshot, nil); len(got) != 1 || got[0] != "bbbb" {
		t.Errorf("recapture = %q, want the one written since", got)
	}
}
