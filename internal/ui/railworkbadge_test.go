package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/status"
)

// twinMeta is what both rows in twinSessions say about themselves. The whole
// point of the fixture is that this run reads the same on the row with work
// and the row without, so the tests name it once.
const twinMeta = "working · claude · 4h ago"

// twinSessions are two rows the reader should be able to read straight down:
// same status, same tool, same age, so the only difference between them is
// that one is on work and wears a badge for it.
func twinSessions(t *testing.T) *Model {
	t.Helper()
	m := railModel(t,
		railSession("has-work-here", "", "claude", status.Working,
			"finish PR #838 for ABC-135518", 4*time.Hour),
		railSession("no-work-here", "", "claude", status.Working, "", 4*time.Hour),
	)
	// The row under the cursor opens its work by itself, and an open row
	// wears no badge. The badge is what this file is about, so the cursor
	// parks on the row that has none.
	m.selectSessionRow(t, "no-work-here")
	m.rebuildRows()
	return m
}

// badgeRow is the one rendered rail line naming a session.
// unboxed blanks the selection box's sides, so a boxed row reads like any other.
var unboxed = strings.NewReplacer(selectionBorder.Left, " ", selectionBorder.Right, " ")

func badgeRow(t *testing.T, m *Model, width int, name string) string {
	t.Helper()
	for _, text := range railTextAt(m, width) {
		if strings.Contains(text, name) {
			return strings.TrimRight(unboxed.Replace(text), " ")
		}
	}
	t.Fatalf("no row named %q at width %d", name, width)
	return ""
}

// comfortableRow is a session's two lines at the comfortable density: the one
// carrying its name, and the one under it carrying its meta.
func comfortableRow(t *testing.T, m *Model, width int, name string) (head, meta string) {
	t.Helper()
	lines := railTextAt(m, width)
	for i, text := range lines {
		if strings.Contains(text, name) && i+1 < len(lines) {
			return text, strings.TrimRight(unboxed.Replace(lines[i+1]), " ")
		}
	}
	t.Fatalf("no two-line row named %q at width %d", name, width)
	return "", ""
}

// metaColumn is the cell the meta run starts in. A byte offset will not do:
// the badge and the status marks ahead of it are multi-byte.
func metaColumn(t *testing.T, row string) int {
	t.Helper()
	at := strings.Index(row, twinMeta)
	if at < 0 {
		t.Fatalf("the meta run is not intact:\n%q", row)
	}
	return textfmt.Width(row[:at])
}

// The badge used to be spliced between the state and the age, which moved
// "claude · 4h ago" a different distance on every row that had work. Two rows
// whose state, tool and age read identically must now put that run in
// identical columns whether or not one of them is on something.
func TestBadgeDoesNotShiftTheMetaColumn(t *testing.T) {
	m := twinSessions(t)
	const width = 100
	badged := badgeRow(t, m, width, "has-work-here")
	plain := badgeRow(t, m, width, "no-work-here")

	if !strings.HasSuffix(badged, twinMeta) || !strings.HasSuffix(plain, twinMeta) {
		t.Fatalf("the meta run is not intact at the right edge:\n%q\n%q", badged, plain)
	}
	if got, want := metaColumn(t, badged), metaColumn(t, plain); got != want {
		t.Fatalf("meta starts at column %d on the badged row and %d on the plain one:\n%q\n%q",
			got, want, badged, plain)
	}
	if !strings.Contains(badged, "1 pr · 1 issue") {
		t.Fatalf("the badged row lost its badge:\n%q", badged)
	}
	// Left of the meta, and left of the gap that separates the two columns.
	if strings.Index(badged, "1 pr · 1 issue") > strings.Index(badged, twinMeta) {
		t.Fatalf("the badge is still inside the meta column:\n%q", badged)
	}
}

// A comfortable row keeps its badge on the meta line, where it has room the
// name line does not, and puts it after the age rather than into the middle
// of the run. Nothing on that line was ever out of column -- it starts at a
// fixed indent -- so what the badge owes it is only to leave the state, the
// tool and the age reading as one thing.
func TestComfortableRowsKeepTheBadgeAfterTheAge(t *testing.T) {
	m := twinSessions(t)
	m.comfortableRows = true
	const width = 100

	head, badged := comfortableRow(t, m, width, "has-work-here")
	_, plain := comfortableRow(t, m, width, "no-work-here")

	if strings.Contains(head, "pr") || strings.Contains(head, "issue") {
		t.Fatalf("the badge climbed onto the name line, where the long names are:\n%q", head)
	}
	if !strings.HasSuffix(plain, twinMeta) {
		t.Fatalf("the row with no work does not end at its age:\n%q", plain)
	}
	if got, want := metaColumn(t, badged), metaColumn(t, plain); got != want {
		t.Fatalf("meta starts at column %d badged and %d plain:\n%q\n%q", got, want, badged, plain)
	}
	if !strings.HasSuffix(badged, twinMeta+badgeGap+"◆ 1 pr · 1 issue") {
		t.Fatalf("the badge does not follow an unbroken meta run:\n%q", badged)
	}
}

// The badge takes what the line has left over and the name keeps the rest: a
// name long enough to fill the row wears no badge rather than being cut back
// to make one fit. The fold arrow is still there saying there is work.
func TestALongNameKeepsItsRowFromTheBadge(t *testing.T) {
	long := "abc-135518-gate-inbox-rail-row-badge-alignment-and-width"
	m := railModel(t,
		railSession(long, "", "claude", status.Working, "finish PR #838 for ABC-135518", 4*time.Hour),
		railSession("elsewhere", "", "claude", status.Working, "", 4*time.Hour),
	)
	m.selectSessionRow(t, "elsewhere")
	m.rebuildRows()
	row := badgeRow(t, m, 90, long)

	if !strings.HasSuffix(row, twinMeta) {
		t.Fatalf("the meta did not survive a long name:\n%q", row)
	}
	// Nothing but the gap between the two columns stands between them: the
	// name is whole and no badge was squeezed in beside it.
	between := row[strings.Index(row, long)+len(long) : strings.Index(row, twinMeta)]
	if strings.TrimSpace(between) != "" {
		t.Fatalf("a badge was drawn where there was no room for one: %q in\n%q", between, row)
	}
	if !strings.Contains(row, "▸ ") {
		t.Fatalf("nothing says the row opens:\n%q", row)
	}
}

// countLadder still runs in the new position: as the rail narrows the badge
// drops its punctuation, then its words, then its mark, then falls back to
// the marks-and-a-count it wore before words, and only then goes. The meta
// run stays whole and against the right edge at every rung, which is the
// whole reason the badge moved.
func TestTheBadgeWalksTheLadderDownAsTheRailNarrows(t *testing.T) {
	for _, tc := range []struct {
		width int
		want  string
	}{
		{100, "◆ 1 pr · 1 issue"},
		{63, "◆ 1 pr 1 issue"},
		{58, "◆ 1pr 1is"},
		{56, "1pr 1is"},
		{54, "1p 1i"},
		{52, "◆ ◐"},
		{50, "+2"},
		{48, ""},
	} {
		m := twinSessions(t)
		row := badgeRow(t, m, tc.width, "has-work-here")
		if !strings.HasSuffix(row, twinMeta) {
			t.Fatalf("width %d: the meta run was broken:\n%q", tc.width, row)
		}
		got := strings.TrimSpace(strings.TrimSuffix(row, twinMeta))
		got = strings.TrimSpace(strings.TrimPrefix(got, "▸ ◐ has-work-here"))
		if got != tc.want {
			t.Fatalf("width %d: want badge %q, got %q in:\n%q", tc.width, tc.want, got, row)
		}
	}
}
