package status

import "testing"

func TestCodexCapacityFailureIsCurrentAndOutsideTheComposer(t *testing.T) {
	engine := codexEngine(t)
	failure := "■ The selected model is at capacity. Please try again later."
	for _, tc := range []struct {
		name, pane string
		capacity   bool
	}{
		{"current failure", failure + "\n\n› ", true},
		{"wrapped failure", failure + "\n  Or switch to another model.\n\n› ", true},
		{"closed failed turn", failure + "\n\n  02:41\n\n› ", true},
		{"new turn working", failure + "\n\n› continue\n• Working (2s • esc to interrupt)\n\n› ", false},
		{"new turn completed", failure + "\n\n› continue\n• Done.\n\n  02:42\n\n› ", false},
		{"typed draft", "• Done.\n\n› " + failure, false},
		{"ordinary prose", "• The selected model is at capacity is an error to handle.\n\n› ", false},
		{"unrelated error", "■ Network error: connection reset.\n\n› ", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			banner := engine.CapacityBanner("codex", tc.pane)
			if (banner != "") != tc.capacity {
				t.Fatalf("capacity banner = %q; expected capacity=%v", banner, tc.capacity)
			}
			if tc.capacity {
				if state, matched := engine.Match("codex", tc.pane); state != Errored || !matched {
					t.Fatalf("status = %q, matched=%v; want errored", state, matched)
				}
				if engine.LimitBanner("codex", tc.pane) == "" {
					t.Fatal("capacity failure did not hold queued input")
				}
			}
		})
	}
	if engine.CapacityBanner("claude", failure+"\n❯ ") != "" {
		t.Fatal("scheduled another CLI's error as Codex capacity")
	}
}
