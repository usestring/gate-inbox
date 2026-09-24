// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package agentsession

import (
	"path/filepath"
	"testing"
	"time"
)

// A rollout is a file any process can write, so an id spelling a command
// must be left alone. The conversation we really launched still has to
// bind, or one planted file would end id capture for that directory.
func TestCaptureCodexLeavesAnIDThatIsNotAPlainToken(t *testing.T) {
	root := t.TempDir()
	launch := time.Now()
	writeFile(t, filepath.Join(root, "2026/07/18/rollout-planted.jsonl"),
		codexRollout(`abc; touch pwned`, "/repo"), launch.Add(time.Second))
	writeFile(t, filepath.Join(root, "2026/07/18/rollout-ours.jsonl"),
		codexRollout("ours-uuid", "/repo"), launch.Add(2*time.Second))

	id, ok := captureCodex(root, "/repo", launch, map[string]bool{})
	if !ok || id != "ours-uuid" {
		t.Fatalf("got id=%q ok=%v, want ours-uuid true", id, ok)
	}
}
