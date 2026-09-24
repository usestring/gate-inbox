package extension_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/extension"
)

func TestValidSessionIDAcceptsTheShapesStoresUse(t *testing.T) {
	for _, id := range []string{
		"019a4b0e-39e1-7261-b801-e64f2d0e97bd",
		"4b53d997-3d0b-4d89-a5d0-39573e4588f1",
		"ses_8QbCd3Ef",
		"ours-uuid",
		"v1.2_x",
	} {
		if !extension.ValidSessionID(id) {
			t.Errorf("a genuine id was refused: %q", id)
		}
	}
	long := make([]byte, 129)
	for i := range long {
		long[i] = 'a'
	}
	for _, id := range []string{
		"",
		"abc; touch pwned",
		`abc'; touch pwned; echo '`,
		"abc$(touch pwned)",
		"abc\ntouch pwned",
		"../../etc/passwd",
		"-rf",
		".hidden",
		string(long),
	} {
		if extension.ValidSessionID(id) {
			t.Errorf("an id that is not a plain token was accepted: %q", id)
		}
	}
}

// A session launched through a symlinked directory still matches a store
// entry that records the resolved path, from either side.
func TestSamePathResolvesASymlinkedDirectory(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	other := filepath.Join(root, "other")
	for _, dir := range []string{target, other} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if !extension.SamePath(link, target) || !extension.SamePath(target, link) {
		t.Errorf("a symlink and its target compared different")
	}
	if !extension.SamePath(link, link) {
		t.Errorf("a path compared different from itself")
	}
	if extension.SamePath(link, other) {
		t.Errorf("a symlink matched a directory it does not point at")
	}
}

// A path that does not exist cannot be resolved, so it compares as
// written rather than matching everything else that fails to resolve.
func TestSamePathComparesAnUnresolvablePathAsWritten(t *testing.T) {
	root := t.TempDir()
	gone := filepath.Join(root, "gone")
	if !extension.SamePath(gone, gone) {
		t.Errorf("an unresolvable path compared different from itself")
	}
	if extension.SamePath(gone, filepath.Join(root, "missing")) {
		t.Errorf("two different unresolvable paths compared equal")
	}
	if extension.SamePath(gone, root) {
		t.Errorf("an unresolvable path matched an existing directory")
	}
}

func TestEarliestSession(t *testing.T) {
	launch := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	for name, tc := range map[string]struct {
		cands []extension.SessionCandidate
		want  string
	}{
		"none":     {nil, ""},
		"empty":    {[]extension.SessionCandidate{}, ""},
		"only one": {[]extension.SessionCandidate{{ID: "a", Created: launch}}, "a"},
		"earliest wins wherever it is listed": {[]extension.SessionCandidate{
			{ID: "late", Created: launch.Add(2 * time.Second)},
			{ID: "early", Created: launch},
			{ID: "middle", Created: launch.Add(time.Second)},
		}, "early"},
		"a tie goes to the first listed": {[]extension.SessionCandidate{
			{ID: "later", Created: launch.Add(time.Second)},
			{ID: "first", Created: launch},
			{ID: "second", Created: launch},
		}, "first"},
		"an empty id is skipped even when earliest": {[]extension.SessionCandidate{
			{ID: "", Created: launch},
			{ID: "kept", Created: launch.Add(time.Second)},
		}, "kept"},
		"only empty ids": {[]extension.SessionCandidate{{ID: ""}, {ID: ""}}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			if got := extension.EarliestSession(tc.cands); got != tc.want {
				t.Errorf("EarliestSession = %q, want %q", got, tc.want)
			}
		})
	}
}
