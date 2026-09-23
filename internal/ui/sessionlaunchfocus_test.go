package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The operator's ask: "we need to make it so when you open new sesison u are
// focused into that alr". Every test here renders a real frame after a create
// and asks what is on the screen, rather than reading m.mode: the mode is the
// implementation, the frame is the promise.

// focusedFrameMarker is what the footer says only while the keyboard belongs
// to the agent. The list, the form and the quick bar all paint a different
// tier, so its presence is the frame's own answer to "am I inside the pane".
const focusedFrameMarker = "typing"

// frameNow renders the screen the way the runtime does, defeating the reuse
// cache so a test cannot read a frame painted before the create it is about.
func frameNow(t testing.TB, m *Model) string {
	t.Helper()
	m.frameReuse = false
	return ansi.Strip(m.frame())
}

// assertLandedIn is the whole promise in one place: the frame on screen is the
// focused pane view, and the cursor sits on the row that was just made -- so
// leaving focus returns to the new session rather than to wherever the cursor
// happened to be before.
func assertLandedIn(t *testing.T, m *Model, name string) {
	t.Helper()
	frame := frameNow(t, m)
	if !strings.Contains(frame, focusedFrameMarker) {
		t.Errorf("after creating %q the frame is not the focused pane view (err bar: %q); footer was:\n%s",
			name, m.errBar.text, lastLines(frame, 3))
	}
	if m.mode != modeFocus {
		t.Errorf("after creating %q, mode = %v, err = %q", name, m.mode, m.errBar.text)
	}
	if got := focusedName(t, m); got != name {
		t.Errorf("cursor landed on %q, want the session just created (%q)", got, name)
	}
}

func lastLines(frame string, n int) string {
	lines := strings.Split(strings.TrimRight(frame, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// TestTheNewSessionFormLandsInsideTheSessionItMade covers the form path (ctrl+n).
func TestTheNewSessionFormLandsInsideTheSessionItMade(t *testing.T) {
	m := buildModel(t)
	// Driven through submitForm rather than the createSession fixture, which
	// leaves focus on purpose so its own callers get a list.
	m.openForm()
	m.form.name.SetValue("from-the-form")
	m.form.dir.SetValue(t.TempDir())
	m.form.toolIndex = 0
	pickGroup(t, m, "")
	updated, _ := m.submitForm()
	*m = *updated.(*Model)
	if m.errBar.text != "" {
		t.Fatalf("spawn refused: %s", m.errBar.text)
	}
	assertLandedIn(t, m, "from-the-form")
}

// TestTheInstantSpawnLandsInsideTheSessionItMade covers the one-key path (n),
// which already put the cursor on the new row and stopped there.
func TestTheInstantSpawnLandsInsideTheSessionItMade(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	if err := m.store.CreateGroup("work", dir); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())
	m.selectGroupRow(t, "work")

	updated, _ := m.spawnInstant(m.defaultTool())
	*m = *updated.(*Model)
	if m.errBar.text != "" {
		t.Fatalf("instant spawn refused: %s", m.errBar.text)
	}
	assertLandedIn(t, m, filepath.Base(dir))
}

// TestTheQuickBarLandsInsideTheSessionItSpawned covers the quick bar, which
// spawns when the cursor is on a group, and which also has to take itself off
// screen: a bar left armed behind a focused pane would eat the first keystroke
// meant for the agent.
func TestTheQuickBarLandsInsideTheSessionItSpawned(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	if err := m.store.CreateGroup("work", dir); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())
	m.selectGroupRow(t, "work")

	m.openQuickMode()
	if !m.quick.active {
		t.Fatalf("quick bar did not open: %s", m.errBar.text)
	}
	m.quick.input.SetValue("do the thing")
	updated, _ := m.submitQuick()
	*m = *updated.(*Model)
	if m.errBar.text != "" {
		t.Fatalf("quick spawn refused: %s", m.errBar.text)
	}
	if m.quick.active {
		t.Error("the quick bar is still armed behind the focused pane; it would take the agent's first keystroke")
	}

	sess, ok := m.selected()
	if !ok {
		t.Fatal("nothing is selected after a quick spawn")
	}
	assertLandedIn(t, m, sess.Name)
}

// TestAFailedLaunchLeavesTheOperatorInTheList is the case focusing must not
// swallow. A launch that cannot be built writes no pane, so focusing anyway
// would strand the operator staring at a box with nothing behind it and no
// word about why -- the error has to reach the screen instead.
func TestAFailedLaunchLeavesTheOperatorInTheList(t *testing.T) {
	m := buildModel(t)
	// A hooks directory it cannot write is what makes buildLaunch fail, which
	// is the last thing to run before the pane would have been created.
	hooksDir := m.hooks.Dir()
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(hooksDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(hooksDir, 0o755) })

	m.openForm()
	m.form.name.SetValue("doomed")
	m.form.dir.SetValue(t.TempDir())
	m.form.toolIndex = 0
	pickGroup(t, m, "")
	updated, _ := m.submitForm()
	*m = *updated.(*Model)

	if m.mode == modeFocus {
		t.Fatal("a failed launch focused a pane that was never created")
	}
	if m.errBar.text == "" && m.mode != modeLaunchHint {
		t.Fatal("a failed launch reported nothing: the operator is told neither that it failed nor why")
	}
	frame := frameNow(t, m)
	if strings.Contains(frame, focusedFrameMarker) {
		t.Errorf("a failed launch painted the focused pane view:\n%s", lastLines(frame, 3))
	}
	sessions, err := m.store.ListSessions(true)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("a failed launch left %d rows behind", len(sessions))
	}
}

// TestASlowStartingAgentIsStillFocusedAtOnce is the timing question the
// operator's ask leaves open: a pane whose agent has not drawn a byte yet.
//
// slow-take-tool holds its prompt back for half a second, so this create
// finishes while the pane is still blank. Focusing is deliberately not
// deferred until that first paint: until then the list would still own the
// keyboard, and its keys are single letters that archive, delete and quit, so
// a keystroke aimed at the new agent would act on the board instead. The
// assertion is that the blank pane is focused anyway and the frame is whole --
// a live pane still filling, never a bail back to the list.
func TestASlowStartingAgentIsStillFocusedAtOnce(t *testing.T) {
	m := buildModel(t)
	m.openForm()
	m.form.name.SetValue("slow-boot")
	m.form.dir.SetValue(t.TempDir())
	m.form.toolIndex = toolIndexOf(t, m, "slow-take-tool")
	pickGroup(t, m, "")
	updated, cmd := m.submitForm()
	*m = *updated.(*Model)
	if m.errBar.text != "" {
		t.Fatalf("spawn refused: %s", m.errBar.text)
	}

	// Asserted before anything is allowed to poll or capture, which is the
	// only window in which the pane is provably still blank.
	assertLandedIn(t, m, "slow-boot")

	// And it survives the pane actually arriving: the capture that lands a
	// moment later must not knock the operator back out to the list.
	m.applyCmd(t, cmd)
	assertLandedIn(t, m, "slow-boot")
}

func toolIndexOf(t *testing.T, m *Model, name string) int {
	t.Helper()
	for i, tool := range m.form.toolNames {
		if tool == name {
			return i
		}
	}
	t.Fatalf("tool %q not in the form's list %v", name, m.form.toolNames)
	return 0
}

// TestCreatingFromTheAttentionFilterStillLandsInside is the case the rebuild in
// landInNewSession exists for.
//
// A new session is "starting", which the attention filter excludes -- so the
// tree built during the launch does not carry the new row, and every create
// path widens the filter afterwards to put it back. The cursor can only find
// the row on a tree rebuilt against the widened filter, so a create made while
// the board was filtered would otherwise focus whatever the cursor was already
// on, or nothing at all.
func TestCreatingFromTheAttentionFilterStillLandsInside(t *testing.T) {
	m := buildModel(t)
	m.statusFilter = statusFilterAttention
	m.rebuildRows()

	m.openForm()
	m.form.name.SetValue("born-filtered")
	m.form.dir.SetValue(t.TempDir())
	m.form.toolIndex = 0
	pickGroup(t, m, "")
	updated, _ := m.submitForm()
	*m = *updated.(*Model)
	if m.errBar.text != "" {
		t.Fatalf("spawn refused: %s", m.errBar.text)
	}
	assertLandedIn(t, m, "born-filtered")
}
