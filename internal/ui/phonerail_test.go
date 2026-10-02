package ui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/store"
)

// phoneCols and phoneRows are a phone held upright with its keyboard up, the
// terminal the compact rail is laid out for.
const phoneCols, phoneRows = 48, 25

// phoneRowCount is how many session rows a frame shows, read off the glyph
// column every session row has and nothing else does.
func phoneRowCount(frame string) int {
	count := 0
	for _, line := range strings.Split(frame, "\n") {
		if sessionRowPattern.MatchString(line) {
			count++
		}
	}
	return count
}

var sessionRowPattern = regexp.MustCompile(`[▸▾ ] [◆◐●○✕❯⠋◌] \S`)

// On a phone the frame is the list: one line of machine readings, a one-row
// footer, the cursor on a band rather than in a box, and every other row a
// session. The fleet fixture fit seven at this size when the dock, the
// detail block, the box and the footer's second row all stood.
func TestAPhoneFrameIsMostlySessions(t *testing.T) {
	m := undecidedFleet(t, 40, phoneCols, phoneRows)
	m.placeCursor(len(m.rows) / 2)
	m.rebuildRows()
	frame := ansi.Strip(m.frame())
	t.Logf("%dx%d:\n%s", phoneCols, phoneRows, frame)
	if lines := strings.Split(frame, "\n"); len(lines) != phoneRows {
		t.Fatalf("frame is %d rows, want %d", len(lines), phoneRows)
	}
	if strings.Contains(frame, "computer") || strings.Contains(frame, "swap") {
		t.Fatal("the full machine dock is still on a short terminal")
	}
	if !strings.Contains(frame, "cpu 88% · mem 75%") {
		t.Fatal("the one-line machine reading is missing")
	}
	if rows := m.footerRows(); rows != 1 {
		t.Fatalf("footer takes %d rows, want 1", rows)
	}
	if strings.Contains(frame, "▁▁▁") {
		t.Fatal("the cursor is boxed on a short terminal")
	}
	if got := phoneRowCount(frame); got < 14 {
		t.Fatalf("%d session rows on a phone, want at least 14", got)
	}
}

// A compact row never cuts its meta: the age is whole and unsuffixed, and
// the CLI the board is mostly running goes unnamed.
func TestACompactRowKeepsItsAgeWhole(t *testing.T) {
	m := undecidedFleet(t, 40, phoneCols, phoneRows)
	frame := ansi.Strip(m.frame())
	if strings.Contains(frame, " ago") {
		t.Fatalf("a compact row still says ago:\n%s", frame)
	}
	if cut := regexp.MustCompile(`· \S*…`).FindString(frame); cut != "" {
		t.Fatalf("a meta column was cut %q:\n%s", cut, frame)
	}
	if m.commonTool == "" || strings.Contains(frame, "· "+m.commonTool) {
		t.Fatalf("rows name the common CLI %q:\n%s", m.commonTool, frame)
	}

	wide := undecidedFleet(t, 40, 200, 50)
	if !strings.Contains(ansi.Strip(wide.frame()), "· "+wide.commonTool+" ·") {
		t.Fatal("a wide rail lost the CLI column")
	}
}

// The CLI is named on a compact row when it is not the one most sessions run.
func TestACompactRowNamesAnUnusualCLI(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = phoneCols, phoneRows
	m.sessions = []store.Session{
		{ID: "a", Name: "one", Tool: "claude", Status: "idle"},
		{ID: "b", Name: "two", Tool: "claude", Status: "idle"},
		{ID: "c", Name: "three", Tool: "codex", Status: "idle"},
	}
	m.rebuildRows()
	rail := ansi.Strip(railLinesText(m.railLines(m.railWidth(), m.listBodyHeight())))
	if line := railRowLine(t, rail, "three"); !strings.Contains(line, "idle · codex ·") {
		t.Fatalf("the odd CLI out is unnamed: %q", line)
	}
	if line := railRowLine(t, rail, "one"); strings.Contains(line, "claude") {
		t.Fatalf("the common CLI is named: %q", line)
	}
}

// A compact rail gives the row's own level two columns and every ancestor
// one, and its uprights still stand under the corners they hang from.
func TestCompactGuidesStepOneColumnALevel(t *testing.T) {
	m := Model{width: phoneCols, height: phoneRows, rows: []treeRow{
		{isGroup: true, group: "a"},
		{depth: 1, isGroup: true, group: "a/b"},
		{depth: 2, sess: store.Session{ID: "deep"}},
		{depth: 2, sess: store.Session{ID: "deeper"}},
		{depth: 1, sess: store.Session{ID: "last"}},
	}}
	want := []string{"", "├ ", "│├ ", "│╰ ", "╰ "}
	for i := range m.rows {
		if got := ansi.Strip(m.treeGuidesAt(i)); got != want[i] {
			t.Errorf("row %d guide = %q, want %q", i, got, want[i])
		}
	}
	m.width, m.height = 200, 50
	if got := ansi.Strip(m.treeGuidesAt(2)); got != "│  ├─ " {
		t.Errorf("wide guide = %q, want the three-column slots", got)
	}
}

// The cursor crossing a session on a compact rail leaves its work folded
// into the row's badge; an explicit unfold still opens it.
func TestACompactRailKeepsTheCursorsWorkFolded(t *testing.T) {
	m := undecidedFleet(t, 40, phoneCols, phoneRows)
	sess := firstWorkingSession(t, m)
	m.placeCursor(sessionRowIndex(t, m, sess.ID))
	m.rebuildRows()
	if m.railWorkExpanded(sess.ID) {
		t.Fatal("the cursor opened a session's work on a compact rail")
	}
	m.toggleRailWork()
	if !m.railWorkExpanded(sess.ID) {
		t.Fatal("an explicit unfold did not open it")
	}

	wide := undecidedFleet(t, 40, 200, 50)
	wide.placeCursor(sessionRowIndex(t, wide, sess.ID))
	wide.rebuildRows()
	if !wide.railWorkExpanded(sess.ID) {
		t.Fatal("a wide rail stopped opening the cursor's work")
	}
}
