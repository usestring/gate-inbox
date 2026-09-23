package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/store"
)

// groupAt registers a group with a default path and puts the cursor on its
// row, which is what decides where an instant spawn lands.
func groupAt(t *testing.T, m *Model, name, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := m.store.AddGroup(name, path); err != nil {
		t.Fatal(err)
	}
	loadStoredRows(t, m)
	m.selectGroupRow(t, name)
}

func pressKey(t *testing.T, m *Model, msg tea.KeyPressMsg) {
	t.Helper()
	updated, cmd := m.handleKey(msg)
	*m = *updated.(*Model)
	if cmd != nil {
		m.applyCmd(t, cmd)
	}
}

func instantKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'n', Text: "n"} }
func enterKey() tea.KeyPressMsg   { return tea.KeyPressMsg{Code: tea.KeyEnter} }

// instantSpawn is the whole new-session gesture: n, which asks which agent,
// and enter, which accepts the CLI the box opened on. Every test that used to
// press n alone means this.
func instantSpawn(t *testing.T, m *Model) {
	t.Helper()
	pressKey(t, m, instantKey())
	if m.mode != modeAgentPick {
		t.Fatalf("n opened %v, want the agent box (err %q)", m.mode, m.errBar.text)
	}
	pressKey(t, m, enterKey())
}

func advancedKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl} }

func TestNSpawnsASessionAskingOnlyWhichAgent(t *testing.T) {
	m := buildModel(t)
	dir := filepath.Join(t.TempDir(), "sample-repo")
	groupAt(t, m, "proj", dir)

	instantSpawn(t, m)

	if m.mode != modeFocus {
		t.Fatalf("n then enter opened %v instead of spawning, err = %q", m.mode, m.errBar.text)
	}
	m.leaveFocusForFixture(t)
	rows := m.sessionRows()
	if len(rows) != 1 {
		t.Fatalf("sessions = %d, want the one the keypress made (err %q)", len(rows), m.errBar.text)
	}
	sess := rows[0]
	if sess.Name != "sample-repo" {
		t.Errorf("name = %q, want the working directory's own name", sess.Name)
	}
	if sess.Name == "my-session" {
		t.Error("the spawn wore the form's placeholder name")
	}
	if sess.Group != "proj" || sess.Cwd != dir {
		t.Errorf("group = %q cwd = %q, want the group under the cursor and its directory", sess.Group, sess.Cwd)
	}
	stored, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Only a derived name is one the title pass is allowed to replace, so this
	// is the whole difference between a row that names itself and one stuck
	// wearing its directory forever.
	if stored.NameSource != store.SourceDerived {
		t.Errorf("name_source = %q, want %q", stored.NameSource, store.SourceDerived)
	}
}

// The cursor lands on the new row AND the keyboard goes with it.
//
// This inverts the rule it replaces. That one kept the keyboard on the board
// on two grounds: the new pane is still booting, and a second press of the
// same key would mean something else. The operator asked for the opposite --
// "when you open new sesison u are focused into that alr" -- and the first
// ground does not survive contact: the list owns the keyboard for exactly as
// long as focus is withheld, and the list's keys are single letters that
// archive, delete and quit, so the boot window is more dangerous on the board
// than in the pane.
//
// The second ground is real and is the price: mashing n no longer spawns a
// burst, because presses after the first go to the agent. Leaving focus is one
// key and lands back here, on the row just made.
func TestInstantSpawnTakesTheKeyboardIntoTheNewSession(t *testing.T) {
	m := buildModel(t)
	groupAt(t, m, "proj", filepath.Join(t.TempDir(), "sample-repo"))

	instantSpawn(t, m)

	if m.mode != modeFocus {
		t.Fatalf("mode = %v, want the new session's pane", m.mode)
	}
	entry, ok := m.selectedRow()
	if !ok || !entry.isSession() {
		t.Fatalf("the cursor is not on a session row: %+v", entry)
	}
	if entry.sess.Name != "sample-repo" {
		t.Errorf("the cursor sits on %q, not on the row the keypress made", entry.sess.Name)
	}
	// Leaving lands on the new row rather than wherever the cursor was, which
	// is the other half of the ask.
	m.leaveFocusForFixture(t)
	entry, ok = m.selectedRow()
	if !ok || !entry.isSession() || entry.sess.Name != "sample-repo" {
		t.Errorf("leaving focus landed on %+v, want the row the keypress made", entry)
	}
}

func TestRepeatedInstantSpawnsStayApartOnTheBoard(t *testing.T) {
	m := buildModel(t)
	groupAt(t, m, "proj", filepath.Join(t.TempDir(), "sample-repo"))

	for i := 0; i < 3; i++ {
		// Back to the board between presses: a spawn now takes the keyboard
		// into its own pane, so a second n typed there is the letter n. This
		// test is about the names a burst of spawns wears, not about where
		// the keyboard sits.
		m.leaveFocusForFixture(t)
		instantSpawn(t, m)
	}

	var names []string
	seen := map[string]bool{}
	for _, sess := range m.sessionRows() {
		if seen[sess.Name] {
			t.Errorf("two rows both called %q: a burst of spawns cannot be told apart", sess.Name)
		}
		seen[sess.Name] = true
		names = append(names, sess.Name)
	}
	if len(names) != 3 {
		t.Fatalf("names = %v, want three sessions", names)
	}
	for _, want := range []string{"sample-repo", "sample-repo-2", "sample-repo-3"} {
		if !seen[want] {
			t.Errorf("names = %v, want %q among them", names, want)
		}
	}
}

// A promptless spawn is one nobody has spoken to yet, so it is not asked to
// name itself: the directive is a turn the agent would spend before the user
// had asked it for anything.
func TestInstantSpawnAsksTheAgentForNothing(t *testing.T) {
	m := buildModel(t)
	groupAt(t, m, "proj", filepath.Join(t.TempDir(), "sample-repo"))

	instantSpawn(t, m)

	sess := m.sessionRows()[0]
	if sess.LaunchPrompt != "" {
		t.Errorf("launch prompt = %q, want nothing", sess.LaunchPrompt)
	}
	for _, input := range sess.PendingInputs {
		if strings.Contains(input, launch.DeferredRenameDirective) {
			t.Errorf("the spawn queued a rename directive: %q", input)
		}
	}
	if _, waiting := m.awaitedRenames[sess.ID]; waiting {
		t.Error("the row is waiting for a rename nobody asked for")
	}
}

func TestCtrlNStillOpensTheForm(t *testing.T) {
	m := buildModel(t)
	groupAt(t, m, "proj", filepath.Join(t.TempDir(), "sample-repo"))

	pressKey(t, m, advancedKey())

	if m.mode != modeForm {
		t.Fatalf("mode = %v, want the form (err %q)", m.mode, m.errBar.text)
	}
	if len(m.sessionRows()) != 0 {
		t.Error("the deliberate key spawned something before asking")
	}
}

// A group whose configured path has since been deleted resolves the way the
// form's own directory field resolves it, rather than refusing: the spawn is
// the point, and the row says where it landed.
func TestInstantSpawnFallsBackWhenTheGroupPathIsGone(t *testing.T) {
	m := buildModel(t)
	gone := filepath.Join(t.TempDir(), "gone")
	groupAt(t, m, "proj", gone)
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	instantSpawn(t, m)

	rows := m.sessionRows()
	if len(rows) != 1 {
		t.Fatalf("sessions = %d, want the spawn to land somewhere (err %q)", len(rows), m.errBar.text)
	}
	if rows[0].Cwd == gone || !isDir(rows[0].Cwd) {
		t.Errorf("cwd = %q, want a directory that exists", rows[0].Cwd)
	}
	if rows[0].Cwd != m.groupDefaultDir("proj") {
		t.Errorf("cwd = %q, want the directory the form would have filled in", rows[0].Cwd)
	}
}

func TestDerivedSessionNameFallsBackToTheTool(t *testing.T) {
	m := buildModel(t)
	if got := m.derivedSessionName(string(filepath.Separator), "claude"); got != "claude" {
		t.Errorf("derivedSessionName(root) = %q, want the tool name", got)
	}
}
