package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// pressList sends one list-mode key and swaps in the model it returns, the
// way the runtime does, so a sequence of digits is read as a sequence.
func (m *Model) pressList(t testing.TB, key string) tea.Cmd {
	t.Helper()
	updated, cmd := m.handleKey(tea.KeyPressMsg{Code: rune(key[0]), Text: key})
	*m = *updated.(*Model)
	return cmd
}

// numberedBoard is a two-level tree: backend with an api child and an auth
// grandchild, and frontend beside it.
func numberedBoard(t testing.TB) *Model {
	t.Helper()
	m := buildModel(t)
	for _, path := range []string{"backend", "backend/api", "backend/api/auth", "frontend"} {
		if err := m.store.CreateGroup(path, t.TempDir()); err != nil {
			t.Fatalf("create group %q: %v", path, err)
		}
	}
	m.applyCmd(t, m.refreshCmd())
	return m
}

func (m *Model) groupRowIndex(t testing.TB, path string) int {
	t.Helper()
	for i, row := range m.rows {
		if row.isGroup && row.group == path {
			return i
		}
	}
	t.Fatalf("group %q is not in the rows", path)
	return -1
}

func TestGroupsAndSubgroupsAreNumberedAsAnOutline(t *testing.T) {
	m := numberedBoard(t)
	want := map[string]string{
		rootGroup:          "0",
		"backend":          "1",
		"backend/api":      "1.1",
		"backend/api/auth": "1.1.1",
		"frontend":         "2",
	}
	for path, number := range want {
		if got := m.groupNumber(path); got != number {
			t.Fatalf("group %q numbered %q, want %q", path, got, number)
		}
		if got := m.groupByNumber[number]; got != path {
			t.Fatalf("number %q reads back as %q, want %q", number, got, path)
		}
	}
}

func TestTypingAGroupNumberMovesTheCursorToIt(t *testing.T) {
	m := numberedBoard(t)
	m.cursor = 0
	m.pressList(t, "2")
	if got, want := m.cursor, m.groupRowIndex(t, "frontend"); got != want {
		t.Fatalf("cursor at %d, want frontend at %d", got, want)
	}
	if m.errBar.text != "" {
		t.Fatalf("a jump that landed should say nothing: %q", m.errBar.text)
	}
}

func TestASecondDigitWalksIntoTheGroupJustReached(t *testing.T) {
	m := numberedBoard(t)
	m.cursor = 0
	m.pressList(t, "1")
	m.pressList(t, "1")
	if got, want := m.cursor, m.groupRowIndex(t, "backend/api"); got != want {
		t.Fatalf("cursor at %d, want backend/api at %d", got, want)
	}
	m.pressList(t, "1")
	if got, want := m.cursor, m.groupRowIndex(t, "backend/api/auth"); got != want {
		t.Fatalf("cursor at %d, want backend/api/auth at %d", got, want)
	}
	if m.jump.buffer != "1.1.1" {
		t.Fatalf("pending number is %q, want %q", m.jump.buffer, "1.1.1")
	}
}

// A board wide enough to carry a group 11 is the one case where the digits of
// a wider number and a step down read the same. The number a group actually
// carries wins, and the separator is what reaches the child.
func TestANumberAGroupCarriesWinsOverTheStepDown(t *testing.T) {
	m := buildModel(t)
	for i := 1; i <= 11; i++ {
		if err := m.store.CreateGroup(fmt.Sprintf("g%02d", i), t.TempDir()); err != nil {
			t.Fatalf("create group: %v", err)
		}
	}
	if err := m.store.CreateGroup("g01/child", t.TempDir()); err != nil {
		t.Fatalf("create child group: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())

	m.pressList(t, "1")
	m.pressList(t, "1")
	if got, want := m.cursor, m.groupRowIndex(t, "g11"); got != want {
		t.Fatalf("cursor at %d, want g11 at %d", got, want)
	}

	m.clearGroupJump()
	m.pressList(t, "1")
	m.pressList(t, ".")
	m.pressList(t, "1")
	if got, want := m.cursor, m.groupRowIndex(t, "g01/child"); got != want {
		t.Fatalf("cursor at %d, want g01/child at %d", got, want)
	}
}

func TestASubgroupNumberWalksInAndOpensTheFoldsOverIt(t *testing.T) {
	m := numberedBoard(t)
	m.collapsed["backend"] = true
	m.collapsed["backend/api"] = true
	m.collapsed["backend/api/auth"] = true
	m.rebuildRows()
	m.cursor = 0

	m.pressList(t, "1")
	m.pressList(t, ".")
	m.pressList(t, "1")
	m.pressList(t, ".")
	m.pressList(t, "1")

	if m.collapsed["backend"] || m.collapsed["backend/api"] {
		t.Fatal("jumping into the subtree should open every fold over it")
	}
	if !m.collapsed["backend/api/auth"] {
		t.Fatal("the target's own fold is the operator's, and should be left alone")
	}
	if got, want := m.cursor, m.groupRowIndex(t, "backend/api/auth"); got != want {
		t.Fatalf("cursor at %d, want backend/api/auth at %d", got, want)
	}
}

func TestADigitThatCannotExtendTheNumberStartsAFreshOne(t *testing.T) {
	m := numberedBoard(t)
	m.cursor = 0
	m.pressList(t, "1")
	// Neither a group 12 nor a subgroup 1.2 exists, so the 2 is the second
	// group rather than a digit of a number that can never be finished.
	m.pressList(t, "2")
	if got, want := m.cursor, m.groupRowIndex(t, "frontend"); got != want {
		t.Fatalf("cursor at %d, want frontend at %d", got, want)
	}
	if m.jump.buffer != "2" {
		t.Fatalf("pending number is %q, want %q", m.jump.buffer, "2")
	}
}

func TestADigitNamingNoGroupIsRefusedAndDropsThePendingNumber(t *testing.T) {
	m := numberedBoard(t)
	before := m.cursor
	m.pressList(t, "7")
	if m.cursor != before {
		t.Fatal("a number no group carries should not move the cursor")
	}
	if m.jump.buffer != "" {
		t.Fatalf("a refused number should not stand: %q", m.jump.buffer)
	}
	if m.errBar.text == "" {
		t.Fatal("a refused number should say so")
	}
}

func TestAnyOtherKeyEndsThePendingNumber(t *testing.T) {
	m := numberedBoard(t)
	m.pressList(t, "1")
	if m.jump.buffer == "" {
		t.Fatal("a digit should leave a number pending")
	}
	m.pressList(t, "j")
	if m.jump.buffer != "" {
		t.Fatalf("moving the cursor should end the number: %q", m.jump.buffer)
	}
}

func TestAPendingNumberExpiresOnItsOwnSettle(t *testing.T) {
	m := numberedBoard(t)
	m.pressList(t, "1")
	stale := m.jump.gen - 1

	updated, _ := m.Update(groupJumpSettleMsg{gen: stale})
	*m = *updated.(*Model)
	if m.jump.buffer != "1" {
		t.Fatalf("a superseded settle should leave the number alone, got %q", m.jump.buffer)
	}

	updated, _ = m.Update(groupJumpSettleMsg{gen: m.jump.gen})
	*m = *updated.(*Model)
	if m.jump.buffer != "" {
		t.Fatalf("the number's own settle should expire it, got %q", m.jump.buffer)
	}
}

func TestASeparatorWithNoNumberPendingStaysTheDismissKey(t *testing.T) {
	m := numberedBoard(t)
	if m.isGroupJumpKey(".") {
		t.Fatal("a bare . starts no number and must reach dismiss")
	}
	m.pressList(t, "1")
	if !m.isGroupJumpKey(".") {
		t.Fatal("with digits standing, . separates the levels")
	}
}

func TestTriageDropsTheNumbersWithTheGroupRows(t *testing.T) {
	m := numberedBoard(t)
	m.applyCmd(t, m.toggleTriage())
	if len(m.groupByNumber) != 0 {
		t.Fatalf("triage has no group rows to number: %v", m.groupByNumber)
	}
	m.pressList(t, "1")
	if m.errBar.text == "" {
		t.Fatal("a digit in a view with no groups should say so")
	}
}

func TestTheGroupRowPrintsItsNumber(t *testing.T) {
	m := numberedBoard(t)
	row := m.renderTreeRow(m.rows[m.groupRowIndex(t, "backend/api")], false, 60,
		m.groupRowIndex(t, "backend/api"), backdropHex())
	if !strings.Contains(ansi.Strip(row), "1.1") {
		t.Fatalf("the group row should print its number: %q", row)
	}
}

// numberTint reports whether a group's printed number is in the key tint.
func (m *Model) numberTint(t testing.TB, path string) bool {
	t.Helper()
	label := m.groupNumberLabel(path)
	if label == "" {
		t.Fatalf("group %q prints no number", path)
	}
	return label == keyStyle.Render(m.groupNumber(path))+" "
}

func TestANumberThatNamesAGroupTintsThatGroupAlone(t *testing.T) {
	m := numberedBoard(t)
	m.pressList(t, "1")
	if !m.numberTint(t, "backend") {
		t.Fatalf("the group the number named should be tinted")
	}
	for _, path := range []string{"backend/api", "backend/api/auth"} {
		if m.numberTint(t, path) {
			t.Fatalf("child %q reads as selected alongside its parent", path)
		}
	}
}

func TestAnUnfinishedNumberTintsWhatItCouldStillReach(t *testing.T) {
	m := numberedBoard(t)
	m.pressList(t, "1")
	m.pressList(t, ".")
	if !m.numberTint(t, "backend/api") {
		t.Fatalf("a number still being typed should tint what finishing it reaches")
	}
	if m.numberTint(t, "frontend") {
		t.Fatalf("a group the number cannot reach should stay subtle")
	}
}
