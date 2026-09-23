// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/store"
)

// A destructive answer is asked in a dialog, not on the status line, so a
// busy frame cannot hide the question.
func TestConfirmRendersAsADialog(t *testing.T) {
	m := buildModel(t)
	m.mode = modeConfirmDelete
	m.confirm = confirmTarget{
		action:   actionArchive,
		sessions: []store.Session{{ID: "one", Name: "builder"}},
		label:    "end builder? frees its RAM, t finds it.",
	}

	out := ansi.Strip(m.frame())
	for _, want := range []string{"End session", "end builder?", "frees its RAM", "cancel"} {
		if !strings.Contains(out, want) {
			t.Fatalf("confirm dialog missing %q:\n%s", want, out)
		}
	}
	if status := ansi.Strip(m.statusLine()); strings.Contains(status, "builder") {
		t.Fatalf("the question moved to the dialog, status should not repeat it: %q", status)
	}
}

func TestConfirmTitleNamesTheAct(t *testing.T) {
	m := buildModel(t)
	cases := []struct {
		action  string
		isGroup bool
		want    string
	}{
		{actionArchive, false, "End session"},
		{actionArchive, true, "End group"},
		{actionRestart, false, "Restart session"},
		{actionRestore, false, "Restore session"},
	}
	for _, tc := range cases {
		m.confirm = confirmTarget{action: tc.action, isGroup: tc.isGroup}
		if got := m.confirmTitle(); !strings.Contains(got, tc.want) {
			t.Errorf("action %q group=%v titled %q, want it to name %q", tc.action, tc.isGroup, got, tc.want)
		}
	}
}

func TestSplitConfirmLabel(t *testing.T) {
	question, consequence := splitConfirmLabel("archive web? frees its RAM.")
	if question != "archive web?" || consequence != "frees its RAM." {
		t.Fatalf("split gave %q / %q", question, consequence)
	}
	if question, consequence = splitConfirmLabel("archive everything"); question != "archive everything" || consequence != "" {
		t.Fatalf("a label without a question mark stays whole, got %q / %q", question, consequence)
	}
}

// cardText is the dialog as a reader takes it in: the frame gone and the
// wrapped lines rejoined, so an assertion covers a whole sentence and a word
// broken across two lines would show up as a failure.
func cardText(m *Model) string {
	var parts []string
	for _, line := range strings.Split(ansi.Strip(m.frame()), "\n") {
		if trimmed := strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "│")); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return strings.Join(parts, " ")
}

// The warning before ending somebody else's pane has to survive the narrow
// terminal too: the same facts, wrapped, at 40 columns and at 100.
func TestConfirmWarnsBeforeEndingSomeoneElsesPane(t *testing.T) {
	m := buildModel(t)
	m.mode = modeConfirmDelete
	sess := store.Session{ID: "one", Name: "refactor", TmuxSocket: "default", TmuxPaneID: "%12"}

	for _, tc := range []struct {
		action string
		label  string
		title  string
		want   []string
	}{
		{actionArchive, adoptedArchiveLabel(sess), "End someone else's pane", []string{
			"the manager did not start refactor.",
			"it is pane %12 on tmux server default.",
			"end it?",
			"this kills the pane, not just the row:",
			"the agent running in it dies, and whatever it has not saved dies with it.",
		}},
	} {
		m.confirm = confirmTarget{action: tc.action, sessions: []store.Session{sess}, label: tc.label}
		for _, width := range []int{40, 100} {
			m.width = width
			card := cardText(m)
			t.Logf("%s at %d columns:\n%s", tc.action, width, ansi.Strip(m.frame()))
			if !strings.Contains(card, tc.title) {
				t.Errorf("at %d columns the title should name whose pane it is, got:\n%s", width, card)
			}
			for _, want := range tc.want {
				if !strings.Contains(card, want) {
					t.Errorf("at %d columns the warning is missing %q, got:\n%s", width, want, card)
				}
			}
		}
	}
}

// The consequence is where the warning lives for an adopted pane, so it is
// the one case that must not be painted as the muted aside every other
// dialog puts there: it has to carry the same alarm as the question above it.
func TestAdoptedConsequenceIsPaintedAsAnAlarm(t *testing.T) {
	m := buildModel(t)
	m.mode = modeConfirmDelete
	m.width = 100
	sess := store.Session{ID: "one", Name: "refactor", TmuxSocket: "default", TmuxPaneID: "%12"}
	m.confirm = confirmTarget{action: actionArchive, sessions: []store.Session{sess}, label: adoptedArchiveLabel(sess)}
	if m.confirmAdopted() != 1 {
		t.Fatalf("confirmAdopted = %d, want 1", m.confirmAdopted())
	}
	frame := m.frame()
	// styleOf reads the escape a run opens with, so both anchors have to sit
	// at the start of their wrapped line -- "not just the row" is mid-line.
	question, consequence := styleOf(t, frame, "the manager did not start"), styleOf(t, frame, "this kills the pane")
	if question != consequence {
		t.Fatalf("the warning is styled as an aside: question %q, consequence %q", question, consequence)
	}

	// The same dialog for a session the manager did start keeps the ordinary
	// two weights, which is what makes the alarm above mean something.
	m.confirm = confirmTarget{
		action:   actionArchive,
		sessions: []store.Session{{ID: "two", Name: "refactor"}},
		label:    "end refactor? frees its RAM, t finds it.",
	}
	if m.confirmAdopted() != 0 {
		t.Fatal("a session the manager started is not an adopted pane")
	}
	if got := m.confirmTitle(); !strings.Contains(got, "End session") {
		t.Fatalf("a managed archive keeps its own title, got %q", got)
	}
	frame = m.frame()
	if styleOf(t, frame, "end refactor?") == styleOf(t, frame, "frees its RAM") {
		t.Fatal("an ordinary archive should still set its consequence at the quieter weight")
	}
}

// styleOf is the escape sequence a line opens with, which is how a test tells
// the alarm weight from the muted one without asserting a color.
func styleOf(t *testing.T, frame, text string) string {
	t.Helper()
	for _, line := range strings.Split(frame, "\n") {
		at := strings.Index(line, text)
		if at < 0 {
			continue
		}
		if open := strings.LastIndex(line[:at], "\x1b["); open >= 0 {
			return line[open:at]
		}
	}
	t.Fatalf("no styled line carries %q in:\n%s", text, ansi.Strip(frame))
	return ""
}
