package ui

import (
	"os"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/status"
)

// piComposerModel is a focused model on a pi pane, read with the rules pi 1.5
// ships with: the editor framed by two rules and no prompt marker, so its
// composer is named by input_between_rules.
func piComposerModel(t *testing.T, flag bool, cursor paneCursor, rows []string) *Model {
	t.Helper()
	engine, err := status.NewEngine(config.Config{Tools: map[string]config.Tool{
		"pi": {
			ActivityCutoff:    `(?ms)\A.*^─{8,}[ \t]*$`,
			ChromeLine:        `^[ \t]*─{8,}[ \t]*$`,
			InputBetweenRules: flag,
		},
	}})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	m := &Model{engine: engine, mode: modeFocus}
	m.preview = strings.Join(rows, "\n") + "\n"
	m.pane.forID = "s1"
	m.pane.cursor = cursor
	return m
}

// piFinishedRows is a pi 1.5.1 pane at rest after a turn, as captured: the
// reply, the editor between its two rules with the caret in column 0 of the
// bare row, then the cwd and usage footer.
func piFinishedRows(t *testing.T) ([]string, int) {
	t.Helper()
	raw, err := os.ReadFile("testdata/pi-finished.txt")
	if err != nil {
		t.Fatal(err)
	}
	rows := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	for y := len(rows) - 1; y > 0; y-- {
		if strings.HasPrefix(rows[y], "────────") && strings.HasPrefix(rows[y-2], "────────") {
			return rows, y - 1
		}
	}
	t.Fatal("fixture has no editor between two rules")
	return nil, 0
}

func TestPiLeftLeavesARestingComposer(t *testing.T) {
	rows, composer := piFinishedRows(t)
	m := piComposerModel(t, true, paneCursor{x: 0, y: composer, ok: true}, rows)
	if !m.leftLeavesFocus("s1", "pi") {
		t.Error("Left at the head of pi's empty editor stayed with the pane")
	}
	unset := piComposerModel(t, false, paneCursor{x: 0, y: composer, ok: true}, rows)
	if unset.leftLeavesFocus("s1", "pi") {
		t.Error("without input_between_rules a bare row was read as a composer")
	}
}

func TestPiLeftStaysInsideWrittenText(t *testing.T) {
	rows, composer := piFinishedRows(t)
	typed := append([]string(nil), rows...)
	typed[composer] = "fix the build"
	if m := piComposerModel(t, true, paneCursor{x: 13, y: composer, ok: true}, typed); m.leftLeavesFocus("s1", "pi") {
		t.Error("Left after typed text left the pane")
	}
	if m := piComposerModel(t, true, paneCursor{x: 0, y: composer, ok: true}, typed); !m.leftLeavesFocus("s1", "pi") {
		t.Error("Left before the first typed character, a no-op for pi, stayed with the pane")
	}

	// A second line just opened with Enter: the caret is at its head, but the
	// message is above it.
	second := append([]string(nil), rows[:composer]...)
	second = append(second, "fix the build", "")
	second = append(second, rows[composer+1:]...)
	if m := piComposerModel(t, true, paneCursor{x: 0, y: composer + 1, ok: true}, second); m.leftLeavesFocus("s1", "pi") {
		t.Error("Left at the head of a message's second line left the pane")
	}
}

// Only the last pair of rules frames the editor: a blank row between two
// rules higher up is transcript.
func TestPiComposerIsTheLastPairOfRules(t *testing.T) {
	rows, _ := piFinishedRows(t)
	quoted := append([]string{"────────", "", "────────"}, rows...)
	if m := piComposerModel(t, true, paneCursor{x: 0, y: 1, ok: true}, quoted); m.leftLeavesFocus("s1", "pi") {
		t.Error("a blank row between rules in the transcript was read as the editor")
	}
}
