package ui

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// bodyRows is the frame's main body: the rows between the header band and
// the rule that closes it, which is where both panels are painted.
func bodyRows(t *testing.T, m *Model) []string {
	t.Helper()
	lines := strings.Split(m.frame(), "\n")
	top, bottom := m.bodyYRange()
	if bottom > len(lines) {
		t.Fatalf("frame is %d rows, body wants %d", len(lines), bottom)
	}
	rows := make([]string, 0, bottom-top)
	for _, line := range lines[top:bottom] {
		rows = append(rows, ansi.Strip(line))
	}
	return rows
}

// A hundred-row terminal paints a ninety-row preview panel. The pane behind
// it has to be ninety rows too: left at whatever size its own window has --
// which is every adopted pane, and the board is mostly adopted -- the agent's
// output lands in a band at the top and the rest of the panel paints nothing.
func TestATallPreviewPanelFillsWithTheAdoptedPanesOutput(t *testing.T) {
	m := buildModel(t)
	m.conversation = nil
	m.width, m.height = 200, 100
	// A foreign server opens its window at 80x24, which is exactly the case:
	// a pane sized for somewhere else, previewed in a much taller panel.
	socket, pane := uiForeignServer(t, `sh -c 'i=1; while [ $i -le 200 ]; do echo "agent output line $i"; i=$((i+1)); done; cat'`)
	const id = "borrowed"
	if err := m.store.CreateSession(store.Session{
		ID: id, Name: "borrowed", Tool: "claude", Cwd: t.TempDir(),
		Status: status.Idle, CreatedAt: time.Now(), LastStatusAt: time.Now(),
		TmuxSocket: socket, TmuxPaneID: pane,
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := m.tmux.Adopt(id, tmux.Target{Socket: socket, Name: pane}); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	// A refresh is what sizes the fleet, the same pass that runs on the first
	// poll and after every window resize. Twice, with the row picked in
	// between: the panel's height depends on the head above it, so a fleet
	// sized while the cursor was still on root is pinned to a different
	// panel than the one this session is painted into.
	m.applyCmd(t, nil)
	m.selectSessionRow(t, "borrowed")
	m.applyCmd(t, nil)

	sess, ok := m.selected()
	if !ok {
		t.Fatal("no session selected")
	}
	deadline := time.Now().Add(5 * time.Second)
	for lipgloss.Height(m.preview) < m.previewPaneHeight() {
		if time.Now().After(deadline) {
			t.Fatalf("the adopted pane never grew to the preview panel's %d rows, capture is %d",
				m.previewPaneHeight(), lipgloss.Height(m.preview))
		}
		time.Sleep(50 * time.Millisecond)
		m.applyCmd(t, m.previewCmd(sess, m.previewGen, true))
	}

	rows := bodyRows(t, m)
	// The pane's own last line is blank, so the claim is that the output
	// reaches the foot of the panel, not that it paints the very last cell.
	tail := rows[len(rows)-3:]
	if !strings.Contains(strings.Join(tail, "\n"), "output line") {
		t.Fatalf("the preview stops short of the panel's bottom; last rows:\n%s", strings.Join(tail, "\n"))
	}
}
