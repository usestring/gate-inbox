package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// windowSize is tmux's own account of whether a window is pinned. Read from
// the server rather than from anything the manager remembers: the failure
// this guards against is the manager believing it released a window it did
// not.
func windowSizeOption(t testing.TB, socket, pane string) string {
	t.Helper()
	out, err := tmuxOnSocket(socket, "show-window-options", "-v", "-t", pane, "window-size").CombinedOutput()
	if err != nil {
		t.Fatalf("show-window-options: %v: %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// twoAdoptedRows puts two panes from a foreign server on the board, which is
// what the operator's own board is: windows they use directly in tmux, that
// the manager also previews.
func twoAdoptedRows(t testing.TB) (m *Model, socket string, paneA, paneB string) {
	t.Helper()
	m = buildModel(t)
	socket, paneA = uiForeignServer(t, "sleep 100000")
	out, err := tmuxOnSocket(socket, "new-window", "-t", "user", "-P", "-F", "#{pane_id}", "sleep 100000").CombinedOutput()
	if err != nil {
		t.Fatalf("second foreign window: %v: %s", err, out)
	}
	paneB = strings.TrimSpace(string(out))

	for i, pane := range []string{paneA, paneB} {
		id := []string{"adoptedA", "adoptedB"}[i]
		if err := m.store.CreateSession(store.Session{
			ID: id, Name: id, Tool: "claude", Cwd: t.TempDir(),
			Status: status.Idle, CreatedAt: time.Now(), LastStatusAt: time.Now(),
			TmuxSocket: socket, TmuxPaneID: pane,
		}); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		if err := m.tmux.Adopt(id, tmux.Target{Socket: socket, Name: pane}); err != nil {
			t.Fatalf("Adopt: %v", err)
		}
	}
	m.applyCmd(t, nil)
	return m, socket, paneA, paneB
}

// The pin follows the cursor and nothing else keeps one.
//
// Before this, resizeSessions ran over every non-archived session and never
// released any of them, so a manager that had been up for an hour held all of
// the operator's windows frozen at the width of its preview panel -- windows
// they were also using directly in tmux, where a manual window-size does not
// track their terminal at all.
func TestOnlyThePreviewedWindowIsPinned(t *testing.T) {
	m, socket, paneA, paneB := twoAdoptedRows(t)

	m.selectSessionRow(t, "adoptedA")
	m.resizeNow(t)
	if got := windowSizeOption(t, socket, paneA); got != "manual" {
		t.Fatalf("the previewed window is not pinned: window-size = %q, want manual", got)
	}
	if got := windowSizeOption(t, socket, paneB); got == "manual" {
		t.Fatal("a window the manager is not previewing was pinned")
	}

	// The cursor moves on. The window it left is the operator's again.
	m.selectSessionRow(t, "adoptedB")
	m.resizeNow(t)
	if got := windowSizeOption(t, socket, paneA); got != "" {
		t.Fatalf("after the cursor moved away, window-size = %q, want it inheriting the server's again", got)
	}
	if got := windowSizeOption(t, socket, paneB); got != "manual" {
		t.Fatalf("the newly previewed window is not pinned: window-size = %q, want manual", got)
	}
}

// Leaving focus is not the only way to stop previewing, but it is the one the
// operator named. A modal covering the preview is the same thing.
func TestLeavingThePreviewReleasesTheWindow(t *testing.T) {
	m, socket, paneA, _ := twoAdoptedRows(t)
	m.selectSessionRow(t, "adoptedA")
	m.resizeNow(t)
	if got := windowSizeOption(t, socket, paneA); got != "manual" {
		t.Fatalf("setup did not pin the window: window-size = %q", got)
	}

	// A modal takes the preview off screen, so nothing is reading that pane.
	m.mode = modeConfirmDelete
	m.resizeNow(t)
	if got := windowSizeOption(t, socket, paneA); got != "" {
		t.Fatalf("with no preview on screen, window-size = %q, want released", got)
	}

	// Back to the list, and the pin comes back with the preview.
	m.mode = modeList
	m.resizeNow(t)
	if got := windowSizeOption(t, socket, paneA); got != "manual" {
		t.Fatalf("the preview is back but the window is not pinned: window-size = %q", got)
	}
}

// The operator switching to another tmux window is the case that made this
// urgent: the manager is still running, still has a row selected, and is
// holding one of their windows while they are not looking at the manager at
// all.
func TestBlurReleasesAndFocusRestoresThePin(t *testing.T) {
	m, socket, paneA, _ := twoAdoptedRows(t)
	m.selectSessionRow(t, "adoptedA")
	m.resizeNow(t)
	if got := windowSizeOption(t, socket, paneA); got != "manual" {
		t.Fatalf("setup did not pin the window: window-size = %q", got)
	}

	updated, cmd := m.Update(tea.BlurMsg{})
	*m = *updated.(*Model)
	m.applyResize(t, cmd)
	if got := windowSizeOption(t, socket, paneA); got != "" {
		t.Fatalf("while the operator is looking elsewhere, window-size = %q, want released", got)
	}

	updated, cmd = m.Update(tea.FocusMsg{})
	*m = *updated.(*Model)
	m.applyResize(t, cmd)
	if got := windowSizeOption(t, socket, paneA); got != "manual" {
		t.Fatalf("the operator came back but the window was not re-pinned: window-size = %q", got)
	}
}

// The polled read is what covers the blur inside tmux, where the terminal's
// own focus reporting never arrives: focus-events is off by default and
// turning it on would be a server-global write on a server the manager does
// not own.
func TestThePolledVisibilityReadReleasesThePin(t *testing.T) {
	m, socket, paneA, _ := twoAdoptedRows(t)
	m.selectSessionRow(t, "adoptedA")
	m.resizeNow(t)
	if got := windowSizeOption(t, socket, paneA); got != "manual" {
		t.Fatalf("setup did not pin the window: window-size = %q", got)
	}

	// What tmux answers for the manager's own pane when the operator has
	// switched their client to a different window.
	updated, cmd := m.Update(visibleMsg{state: "1,0,1"})
	*m = *updated.(*Model)
	m.applyResize(t, cmd)
	if got := windowSizeOption(t, socket, paneA); got != "" {
		t.Fatalf("the client is on another window but window-size = %q, want released", got)
	}

	updated, cmd = m.Update(visibleMsg{state: "1,1,1"})
	*m = *updated.(*Model)
	m.applyResize(t, cmd)
	if got := windowSizeOption(t, socket, paneA); got != "manual" {
		t.Fatalf("the client came back but window-size = %q, want manual", got)
	}
}

// The three states the read has to tell apart, and the one it must refuse to
// answer. A malformed reply leaving visibility alone is deliberate: treating
// an unreadable pane as a blur would release the pin the preview on screen is
// relying on.
func TestApplyVisible(t *testing.T) {
	for _, tt := range []struct {
		reply   string
		visible bool
		ok      bool
	}{
		{"1,1,1", true, true},
		{"1,0,1", false, true},
		{"0,1,1", false, true},
		{"1,1,0", false, true},
		{"1,1,2", true, true},
		{"", false, false},
		{"1,1", false, false},
		{"garbage", false, false},
	} {
		visible, ok := applyVisible(tt.reply)
		if visible != tt.visible || ok != tt.ok {
			t.Fatalf("applyVisible(%q) = (%v, %v), want (%v, %v)", tt.reply, visible, ok, tt.visible, tt.ok)
		}
	}
}

// applyResize runs a resize command the way the runtime does. Both the pin
// and the release happen off the event loop, so a test that only inspected
// the model would see neither.
func (m *Model) applyResize(t testing.TB, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	if msg == nil {
		return
	}
	updated, _ := m.Update(msg)
	*m = *updated.(*Model)
	m.settleResize(t)
}
