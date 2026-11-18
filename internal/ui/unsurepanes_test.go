package ui

import (
	"strings"
	"testing"
	"time"
)

func TestNoteUnsureFlagsEachPaneOnce(t *testing.T) {
	m := &Model{}
	now := time.Now()
	a := unsurePane{key: "default|%1", session: "cldl", pane: "%1", tool: "codex"}
	b := unsurePane{key: "default|%2", session: "cldl", pane: "%2", tool: "codex"}

	m.noteUnsure([]unsurePane{a}, now)
	if !strings.Contains(m.errBar.text, "cldl %1 (codex)") {
		t.Fatalf("first sighting not flagged: %q", m.errBar.text)
	}

	m.errBar.text = ""
	m.noteUnsure([]unsurePane{a}, now)
	if m.errBar.text != "" {
		t.Fatalf("same pane flagged twice: %q", m.errBar.text)
	}

	m.noteUnsure(nil, now)
	if !m.unsureFlagged[a.key] {
		t.Fatal("a partial scan forgot the flagged panes")
	}

	m.noteUnsure([]unsurePane{a, b}, now)
	if !strings.HasPrefix(m.errBar.text, "2 panes look like an agent but were not adopted") {
		t.Fatalf("new pane not flagged with the full set: %q", m.errBar.text)
	}

	m.errBar.text = ""
	m.noteUnsure([]unsurePane{}, now)
	m.noteUnsure([]unsurePane{a}, now)
	if m.errBar.text == "" {
		t.Fatal("a pane that left and came back was not flagged again")
	}
}

func TestUnsureLineCountsBeyondTheListed(t *testing.T) {
	var panes []unsurePane
	for _, id := range []string{"%1", "%2", "%3", "%4", "%5"} {
		panes = append(panes, unsurePane{key: id, session: "s", pane: id, tool: "codex"})
	}
	got := unsureLine(panes)
	if !strings.HasSuffix(got, "s %3 (codex) and 2 more") {
		t.Fatalf("unsureLine = %q", got)
	}
}

func TestAtShellSkipsIdleTerminals(t *testing.T) {
	for _, c := range []string{"zsh", "-zsh", "bash", "fish"} {
		if !atShell(c) {
			t.Errorf("atShell(%q) = false", c)
		}
	}
	for _, c := range []string{"codex", "node", "claude", ""} {
		if atShell(c) {
			t.Errorf("atShell(%q) = true", c)
		}
	}
}

func TestNoteUnsureRemindsWhilePanesRemain(t *testing.T) {
	m := &Model{}
	now := time.Now()
	a := unsurePane{key: "default|%1", session: "cldl", pane: "%1", tool: "codex"}

	m.noteUnsure([]unsurePane{a}, now)
	m.errBar.text = ""
	m.noteUnsure([]unsurePane{a}, now.Add(unsureRemind-time.Second))
	if m.errBar.text != "" {
		t.Fatalf("reminded early: %q", m.errBar.text)
	}
	m.noteUnsure([]unsurePane{a}, now.Add(unsureRemind))
	if m.errBar.text == "" {
		t.Fatal("no reminder while the pane stayed unadopted")
	}
	m.errBar.text = ""
	m.noteUnsure([]unsurePane{}, now.Add(3*unsureRemind))
	if m.errBar.text != "" {
		t.Fatalf("reminded with nothing unadopted: %q", m.errBar.text)
	}
}

func TestUnsureLineOutlivesTheStatusBarsPolls(t *testing.T) {
	m := &Model{}
	m.noteUnsure([]unsurePane{{key: "default|%1", session: "cldl", pane: "%1", tool: "codex"}}, time.Now())
	line := m.errBar.text
	for range 5 {
		m.ageError()
	}
	if m.errBar.text != line {
		t.Fatalf("line cleared after a few polls: %q", m.errBar.text)
	}
	m.unsureShownAt = time.Now().Add(-unsureShowFor)
	m.ageError()
	if m.errBar.text != "" {
		t.Fatalf("line outlived its hold: %q", m.errBar.text)
	}
}

func TestNoteUnsureWaitsForTheAdoptToast(t *testing.T) {
	now := time.Now()
	m := &Model{offer: adoptOffer{ids: []string{"x"}, text: "adopted x", until: now.Add(time.Minute)}}
	m.errBar.text = "adopted x"
	a := unsurePane{key: "default|%1", session: "cldl", pane: "%1", tool: "codex"}

	m.noteUnsure([]unsurePane{a}, now)
	if m.errBar.text != "adopted x" {
		t.Fatalf("replaced the adopt toast: %q", m.errBar.text)
	}
	m.offer = adoptOffer{}
	m.noteUnsure([]unsurePane{a}, now)
	if !strings.Contains(m.errBar.text, "cldl %1 (codex)") {
		t.Fatalf("pane deferred by the toast was never flagged: %q", m.errBar.text)
	}
}
