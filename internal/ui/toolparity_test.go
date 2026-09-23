package ui

import (
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/config"
)

// Scrolling is routed by what tmux says about the pane, never by which CLI is
// in it. The board runs Claude, Codex and OpenCode side by side, and they do
// not agree on how a terminal should be driven: Claude and OpenCode take the
// alternate screen and claim the mouse, so a notch is theirs to act on and
// tmux keeps no history to walk; Codex draws on the normal screen and claims
// nothing, so its history is tmux's and the manager walks it.
//
// Both answers have to keep working for every tool, which is what this pins:
// the same session row, the same notch, the two routes chosen by the pane's
// own facts.
func TestScrollRoutesOnPaneFactsNotOnTheTool(t *testing.T) {
	for _, tool := range []string{"claude", "codex", "opencode"} {
		t.Run(tool+"/owns the mouse", func(t *testing.T) {
			m, _ := listWithHistory(t, "parity-mouse-"+tool)
			m.rows[m.cursor].sess.Tool = tool
			// An agent CLI on the alternate screen: it scrolls itself, and
			// there is no tmux history behind it to walk instead.
			m.pane.mouse, m.pane.sgr, m.pane.history = true, true, 0
			m.focusActiveAt = time.Time{}

			x, y := previewCell(m)
			updated, cmd := m.handleMouse(wheel(true, x, y))
			m = updated.(*Model)
			if cmd == nil {
				t.Fatal("a notch at a mouse-owning pane scheduled no look")
			}
			if m.focusScroll != 0 {
				t.Fatalf("a pane that scrolls itself was also walked back: offset %d", m.focusScroll)
			}
		})

		t.Run(tool+"/plain pane", func(t *testing.T) {
			m, sessID := listWithHistory(t, "parity-plain-"+tool)
			m.rows[m.cursor].sess.Tool = tool
			// Codex's shape, and every tool's shape once its TUI exits: the
			// normal screen, no mouse, real scrollback.
			m.pane.mouse, m.pane.sgr = false, false
			m.pane.history = paneHistorySize(t, m, sessID)

			x, y := previewCell(m)
			updated, cmd := m.handleMouse(wheel(true, x, y))
			m = updated.(*Model)
			if m.focusScroll != focusScrollStep {
				t.Fatalf("a notch moved the view %d lines, want %d", m.focusScroll, focusScrollStep)
			}
			if cmd == nil {
				t.Fatal("the notch scheduled no read")
			}
			updated, _ = m.Update(cmd())
			m = updated.(*Model)
			assertFrameMatchesTmux(t, m, sessID, m.previewPaneHeight())
		})

		t.Run(tool+"/alt arrows", func(t *testing.T) {
			m, sessID := listWithHistory(t, "parity-keys-"+tool)
			m.rows[m.cursor].sess.Tool = tool
			m.pane.history = paneHistorySize(t, m, sessID)
			before := m.cursor

			updated, cmd := m.handleKey(altUp())
			m = updated.(*Model)
			if m.cursor != before {
				t.Fatalf("alt+up moved the list cursor to %d", m.cursor)
			}
			if m.focusScroll == 0 {
				t.Fatal("alt+up did not scroll the preview")
			}
			if cmd == nil {
				t.Fatal("alt+up scheduled no read")
			}
		})
	}
}

// The chase after a forwarded notch runs on the tool's own echo budget, so a
// CLI that repaints slower than Claude is still caught by its own chase
// rather than waiting out a tick. Codex names 90ms for exactly that reason.
func TestForwardedNotchUsesTheToolsEchoBudget(t *testing.T) {
	// The shipped defaults, not a test fixture: the point is that the tool
	// table an operator actually runs carries the slower budget.
	cfg, err := config.Default()
	if err != nil {
		t.Fatalf("default config: %v", err)
	}
	m := &Model{cfg: cfg}
	claude, codex := m.echoBudget("claude"), m.echoBudget("codex")
	if codex <= claude {
		t.Fatalf("codex chases for %v against claude's %v; codex repaints slower, so its chase must not be shorter", codex, claude)
	}
	if unknown := m.echoBudget("a-tool-nobody-configured"); unknown != focusEchoBudgetDefault {
		t.Fatalf("an unconfigured tool got %v, want the default %v", unknown, focusEchoBudgetDefault)
	}
	// opencode takes the alternate screen and claims the mouse exactly as
	// claude does, so its notches are forwarded and chased the same way. It
	// names no budget of its own, which is the default and is fine until
	// somebody measures it repainting slower.
	if oc := m.echoBudget("opencode"); oc != focusEchoBudgetDefault {
		t.Fatalf("opencode budget %v, want the default %v", oc, focusEchoBudgetDefault)
	}
}
