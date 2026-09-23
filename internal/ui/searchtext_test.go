package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// sessionNamed answers with the row the test is about to drive, since spawn
// hands back only the list.
func sessionNamed(t *testing.T, m *Model, name string) store.Session {
	t.Helper()
	for _, sess := range m.sessionRows() {
		if sess.Name == name {
			return sess
		}
	}
	t.Fatalf("no session named %q among %v", name, sessionNames(m))
	return store.Session{}
}

// waitForPaneText blocks until the pane has really painted what was typed
// into it, so a search test is never racing the screen it is searching.
func waitForPaneText(t *testing.T, m *Model, sessID, want string) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		pane, err := m.tmux.CapturePane(sessID)
		if err != nil {
			t.Fatalf("capture %s: %v", sessID, err)
		}
		// Whitespace out of both sides: a pane wraps at its own width, mid-
		// word, so a needle long enough to identify what it is looking for
		// is long enough to straddle a line break.
		if strings.Contains(squashSpace(ansi.Strip(pane)), squashSpace(want)) {
			return pane
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane never showed %q:\n%s", want, ansi.Strip(pane))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func squashSpace(s string) string { return strings.Join(strings.Fields(s), "") }

func filterFor(m *Model, query string) []string {
	m.searching, m.search = true, query
	m.rebuildRows()
	return sessionNames(m)
}

// The point of the whole feature: the session that mentioned a hostname is
// findable by that hostname, not only by whatever it happened to be named.
func TestSearchFindsASessionByWhatItsPaneIsShowing(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	createSession(t, m, "worker", dir, "")
	createSession(t, m, "bystander", dir, "")
	target := sessionNamed(t, m, "worker")

	const needle = "db-primary-07"
	if matchesMetadata(target, needle) {
		t.Fatalf("fixture no longer reproduces the gap: %q is already in the row's metadata", needle)
	}
	if err := m.tmux.SendText(target.ID, "fatal: could not resolve "+needle); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	waitForPaneText(t, m, target.ID, needle)
	m.applyCmd(t, m.refreshCmd())

	if got := filterFor(m, needle); len(got) != 1 || got[0] != "worker" {
		t.Fatalf("filtering on pane text gave %v want [worker]", got)
	}
	rows := railText(t, m)
	if row := rows[lineWith(t, rows, "worker")]; !strings.Contains(row, "≡pane") {
		t.Fatalf("a row the query is nowhere on should say where the hit was: %q", row)
	}
}

// A colour change lands mid-word often enough that matching the raw capture
// would miss most of what a running agent prints.
func TestSearchMatchesTextAColourChangeSplits(t *testing.T) {
	m := buildModel(t)
	// A shell, because the escapes have to be written by the pane rather
	// than typed at it: a line editor would eat them on the way in.
	target := spawnedSession(t, m, shellToolName)

	const needle = "db-primary-07"
	if err := m.tmux.SendText(target.ID, `printf 'fatal: db-\033[31mprimary\033[0m-07 unreachable\n'`); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	pane := waitForPaneText(t, m, target.ID, needle)
	if strings.Contains(pane, needle) {
		t.Fatalf("fixture no longer reproduces the hazard: the escapes must split %q:\n%s", needle, pane)
	}
	m.applyCmd(t, m.refreshCmd())

	if got := filterFor(m, needle); len(got) != 1 || got[0] != target.Name {
		t.Fatalf("filtering across a colour change gave %v want [%s]", got, target.Name)
	}
}

// Adding the pane to the filter must not change what the metadata does, and
// the badge has to tell the two apart: put on every kept row it would say
// nothing, and left off the pane-only row that row reads as a stray match.
func TestPaneMatchesLeaveMetadataMatchesAloneAndUnbadged(t *testing.T) {
	m := &Model{width: 120, height: 30, collapsed: map[string]bool{}}
	m.sessions = []store.Session{
		{ID: "1", Name: "api-server", Tool: "claude", Status: status.Idle},
		{ID: "2", Name: "web-ui", Tool: "claude", Status: status.Idle},
		{ID: "3", Name: "docs", Tool: "claude", Status: status.Idle},
	}
	m.searchText = map[string]string{
		"2": "GET /api/v1/orders 500",
		"3": "nothing to see here",
	}

	if got := filterFor(m, "api"); len(got) != 2 || got[0] != "api-server" || got[1] != "web-ui" {
		t.Fatalf("filter = %v want [api-server web-ui]", got)
	}
	rows := railTextAt(m, 60)
	if row := rows[lineWith(t, rows, "api-server")]; strings.Contains(row, "≡pane") {
		t.Fatalf("a row whose own name carries the query needs no badge: %q", row)
	}
	if row := rows[lineWith(t, rows, "web-ui")]; !strings.Contains(row, "≡pane") {
		t.Fatalf("the pane-only match should say so: %q", row)
	}

	// The tool and status columns are metadata too, and a session with no
	// capture at all still matches on them exactly as it always did.
	if got := filterFor(m, "claude"); len(got) != 3 {
		t.Fatalf("tool filter = %v want all three", got)
	}
	delete(m.searchText, "2")
	if got := filterFor(m, "web"); len(got) != 1 || got[0] != "web-ui" {
		t.Fatalf("a session with no capture stopped matching its name: %v", got)
	}
}

// One screen per session and nothing carried between passes: the map is
// replaced whole, so a line that has scrolled out of view stops being
// findable rather than piling up as searchable history.
func TestPaneTextIsReplacedEachPassAndNeverAccumulates(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	createSession(t, m, "worker", dir, "")
	createSession(t, m, "bystander", dir, "")
	target := sessionNamed(t, m, "worker")

	const needle = "db-primary-07"
	if err := m.tmux.SendText(target.ID, "fatal: could not resolve "+needle); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	waitForPaneText(t, m, target.ID, needle)

	// A leftover from an earlier pass, which a merge would keep forever.
	m.searchText = map[string]string{"ghost": needle}
	m.applyCmd(t, m.refreshCmd())
	if _, stale := m.searchText["ghost"]; stale {
		t.Fatalf("a pass merged instead of replacing: %v", keysOf(m.searchText))
	}
	if len(m.searchText) != 2 {
		t.Fatalf("pane text = %v want one entry per live session", keysOf(m.searchText))
	}
	if got := filterFor(m, needle); len(got) != 1 {
		t.Fatalf("filter = %v want the one session showing it", got)
	}

	// Scrolling the line out of view is the boundary of the feature: the
	// capture behind it carries the visible screen and no history.
	if err := m.tmux.SendText(target.ID, strings.Repeat("filler\n", 200)); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		m.applyCmd(t, m.refreshCmd())
		if !strings.Contains(m.searchText[target.ID], needle) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the line never left the visible screen:\n%s", m.searchText[target.ID])
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(m.searchText) != 2 {
		t.Fatalf("pane text = %v want one entry per live session", keysOf(m.searchText))
	}
	if got := filterFor(m, needle); len(got) != 0 {
		t.Fatalf("a scrolled-away line is still matching %v", got)
	}
}

func keysOf(texts map[string]string) []string {
	ids := make([]string, 0, len(texts))
	for id := range texts {
		ids = append(ids, id)
	}
	return ids
}
