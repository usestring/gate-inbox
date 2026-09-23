package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

func layoutModel(width, height int) *Model {
	now := time.Now()
	m := &Model{
		width: width, height: height,
		split:  splitState{ratio: defaultSplitRatio},
		groups: []string{"sample-repo"},
		sessions: []store.Session{
			{ID: "s1", Name: "gate-inbox-fork", Tool: "claude", Status: status.Waiting, Cwd: "/repo", Group: "sample-repo", CreatedAt: now.Add(-12 * time.Minute)},
			{ID: "s2", Name: "http-gateway-retailer-solver", Tool: "codex", Status: status.Working, Cwd: "/repo", CreatedAt: now.Add(-3 * time.Hour)},
		},
	}
	m.rebuildRows()
	return m
}

// Two panels on a narrow terminal are two slivers: the rail cannot hold a
// session name and the preview cannot hold a line of pane output.
func TestNarrowTerminalDrawsOnePanel(t *testing.T) {
	narrow := layoutModel(minSplitWidth-1, 26)
	if _, right := narrow.splitWidths(); right != 0 {
		t.Fatalf("narrow terminal still splits: right = %d", right)
	}
	frame := ansi.Strip(narrow.frame())
	if !strings.Contains(frame, "gate-inbox-fork") {
		t.Errorf("the full session name does not fit the one-panel list:\n%s", frame)
	}

	wide := layoutModel(minSplitWidth, 26)
	if _, right := wide.splitWidths(); right == 0 {
		t.Fatal("a terminal wide enough for two panels collapsed to one")
	}
}

// Focus mode is driving a live agent and the pane it mirrors is the point, so
// on a narrow terminal the pane takes the screen rather than the list.
func TestFocusTakesTheOnePanelOnANarrowTerminal(t *testing.T) {
	m := layoutModel(minSplitWidth-1, 26)
	list := ansi.Strip(m.frame())
	m.mode = modeFocus
	focused := ansi.Strip(m.frame())

	if !strings.Contains(list, "gate-inbox-fork") || !strings.Contains(list, "http-gateway-retailer-solver") {
		t.Fatalf("the list panel is not showing every session:\n%s", list)
	}
	// The focused pane carries its own session in its header, so the tell is
	// that the sessions it is not focused on are gone.
	if strings.Contains(focused, "gate-inbox-fork") {
		t.Errorf("focus kept drawing the list instead of the pane:\n%s", focused)
	}
}

// previewPaneWidth sizes the real tmux pane, not just the drawing of it, so a
// collapsed preview must still report a usable terminal. Reporting the
// leftover zero started every agent in a one-column pane.
func TestACollapsedPreviewStillSizesAPane(t *testing.T) {
	m := layoutModel(minSplitWidth-1, 26)
	if got := m.previewPaneWidth(); got < minSplitWidth/2 {
		t.Errorf("collapsed preview sizes panes at %d columns", got)
	}
}

// The half of a help row that truncation eats is the half that says what the
// key does.
func TestHelpWrapsRatherThanTruncating(t *testing.T) {
	m := layoutModel(40, 40)
	m.mode = modeHelp
	frame := ansi.Strip(m.frame())
	for _, word := range []string{"step in:", "work,", "group"} {
		if !strings.Contains(frame, word) {
			t.Errorf("help lost %q to truncation:\n%s", word, frame)
		}
	}
}

// A settings row whose value is cut says nothing: the value is what the
// setting currently is.
func TestSettingsValueSurvivesANarrowCard(t *testing.T) {
	m := layoutModel(40, 40)
	m.mode = modeSettings
	frame := ansi.Strip(m.frame())
	for _, value := range []string{"classic", "compact", "stay open"} {
		if !strings.Contains(frame, value) {
			t.Errorf("settings lost the value %q:\n%s", value, frame)
		}
	}
}

func TestNoFrameOverflowsItsTerminal(t *testing.T) {
	for _, width := range []int{40, 56, 60, 100, 200} {
		for _, mode := range []mode{modeList, modeHelp, modeSettings} {
			m := layoutModel(width, 30)
			m.mode = mode
			for i, line := range strings.Split(m.frame(), "\n") {
				if got := ansi.StringWidth(line); got > width {
					t.Errorf("width %d mode %d line %d overflows to %d: %q",
						width, mode, i, got, ansi.Strip(line))
				}
			}
		}
	}
}
