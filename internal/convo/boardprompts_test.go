package convo

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/band"
	"github.com/usestring/gate-inbox/internal/sessname"
)

// keepWarm is what Claude Code records as the last prompt after the cache
// warden's keep-warm turn, opening exactly as the board's envelope does.
func keepWarm(sent string) string {
	return band.Tag + ` From the "cache-warden" extension running on this board, not from the user, sent ` + sent +
		". Everything between the ----EXTENSION-MESSAGE-cache-warden-ABCD1234---- lines is its text. " +
		"Automatic prompt-cache keep-alive, not a task. Reply with only the word ok."
}

func lastPrompts(t *testing.T, title string, prompts ...string) []byte {
	t.Helper()
	var lines []string
	add := func(rec map[string]string) {
		line, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(line))
	}
	add(map[string]string{"type": "ai-title", "aiTitle": title, "sessionId": "keep-warm"})
	for _, prompt := range prompts {
		add(map[string]string{"type": "last-prompt", "lastPrompt": prompt})
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

// The board's own messages are not what a session is about. Three keep-warms
// in a row used to read as the session moving on, and the drift rule renamed
// it "cache-warden-extension-running-board".
func TestParseTailLeavesTheBoardsOwnMessagesOutOfThePrompts(t *testing.T) {
	raw := lastPrompts(t, "Disk usage alert",
		"look into the disk usage alert and fix it",
		"admin merge and deploy",
		band.Tag+` From agent "idle-status-check" (session 0a1b2c3d), sent 2026-09-30 20:22.`,
		keepWarm("2026-09-30 21:23"),
		keepWarm("2026-09-30 23:47"),
		keepWarm("2026-10-01 01:25"),
	)
	got := parseTail(raw, false)
	want := []string{"look into the disk usage alert and fix it", "admin merge and deploy"}
	if strings.Join(got.Prompts, "|") != strings.Join(want, "|") {
		t.Fatalf("prompts = %q, want %q", got.Prompts, want)
	}

	drift := sessname.NewDrift()
	for pass := range 3 {
		if title := drift.Title("warm", got.Title, got.Prompts); title != got.Title {
			t.Fatalf("pass %d: title drifted to %q on the board's own messages", pass, title)
		}
	}
}

// A person typing between keep-warms is still one prompt each, and the same
// prompt recorded either side of a keep-warm is still one prompt.
func TestParseTailKeepsTheTypedPromptsAroundKeepWarms(t *testing.T) {
	raw := lastPrompts(t, "Terraform auto runner",
		"lets close these all for now",
		keepWarm("2026-10-02 02:14"),
		"lets close these all for now",
		"what is left",
	)
	got := parseTail(raw, false)
	want := []string{"lets close these all for now", "what is left"}
	if strings.Join(got.Prompts, "|") != strings.Join(want, "|") {
		t.Fatalf("prompts = %q, want %q", got.Prompts, want)
	}
}
