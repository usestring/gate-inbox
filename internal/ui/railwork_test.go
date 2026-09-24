package ui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/forge"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
	"github.com/usestring/gate-inbox/internal/worktracker"
)

// railWorkModel is a rail with one session on a pull request and a ticket and
// two sessions on nothing, which is the proportion this machine actually has.
func railWorkModel(t *testing.T) *Model {
	t.Helper()
	return railModel(t,
		railSession("db-migrations", "", "opencode", status.Waiting, "", 3*time.Minute),
		railSession("add-rate-limiting", "backend", "claude", status.Working,
			"finish PR #838 for ABC-135518", 41*time.Second),
		railSession("flaky-e2e", "backend", "claude", status.Errored, "", 22*time.Minute),
	)
}

func railSession(name, group, tool, st, prompt string, age time.Duration) store.Session {
	now := time.Now()
	return store.Session{
		ID: name, Name: name, Group: group, Tool: tool, Status: st,
		Cwd: "/Users/someone/dev/api", LaunchPrompt: prompt,
		CreatedAt: now.Add(-24 * time.Hour), LastStatusAt: now.Add(-age),
	}
}

func railModel(t *testing.T, sessions ...store.Session) *Model {
	t.Helper()
	st, err := store.Open(filepath.Join(tmuxtest.ScratchDir(t), "state.db"))
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	m := &Model{
		width: 120, height: 34, mode: modeList, store: st,
		// → steps into a session unconditionally, so the rail model needs a
		// driver even for the fold assertions that never reach a pane.
		tmux:     newTestDriver(t, testSocket),
		sessions: sessions, collapsed: map[string]bool{}, groups: []string{"backend"},
		split: splitState{ratio: defaultSplitRatio},
		work: worktracker.New(
			fakeGit{remote: "git@github.com:example-org/sample-repo.git"},
			fakePRs{prs: map[string]forge.PR{
				"pr:example-org/sample-repo#838": {
					Repo: "example-org/sample-repo", Number: 838, State: forge.PROpen,
					Checks: forge.ChecksPassing, URL: "https://github.com/example-org/sample-repo/pull/838",
				},
			}, health: forge.Health{OK: true}},
			fakeTickets{tickets: map[string]forge.Ticket{
				"ticket:ABC-135518": {
					Identifier: "ABC-135518", State: "In Review", StateType: "started",
					URL: "https://linear.app/example/issue/ABC-135518",
				},
			}, health: forge.Health{OK: true}},
		),
	}
	m.runWork(t)
	m.rebuildRows()
	return m
}

func (m *Model) artifactRowIndexes() []int {
	var out []int
	for i, row := range m.rows {
		if row.isArtifact() {
			out = append(out, i)
		}
	}
	return out
}

// Eighty-odd sessions and seventy-odd references live on this machine, so a
// rail that opened every session's work would put a hundred rows on the
// primary screen. A session arrives folded, wearing the badge it always wore.
func TestSessionWorkArrivesFolded(t *testing.T) {
	m := railWorkModel(t)
	if got := m.artifactRowIndexes(); len(got) != 0 {
		t.Fatalf("rows opened by themselves: %v", got)
	}
	rail := ansi.Strip(railLinesText(m.railLines(80, m.listBodyHeight())))
	// The badge is counts, not names: one of each, and the mark ahead of them
	// is there because an unmergeable pull request is waiting on a person.
	if want := statusGlyph(status.Waiting) + " 1 pr · 1 issue"; !strings.Contains(rail, want) {
		t.Fatalf("folded row lost its badge (want %q):\n%s", want, rail)
	}
	if strings.Contains(rail, "#838") {
		t.Fatalf("a folded row names an artifact the tree should name:\n%s", rail)
	}
	if !strings.Contains(rail, "▸ ") {
		t.Fatalf("nothing says the row opens:\n%s", rail)
	}
}

// A rail too narrow for one mark per artifact must still say how many there
// are. Under-reporting silently is the fault this whole shape exists to stop.
func TestANarrowFoldedRowStillCountsTheWork(t *testing.T) {
	m := railWorkModel(t)
	rail := ansi.Strip(railLinesText(m.railLines(59, m.listBodyHeight())))
	if !strings.Contains(rail, "+2") {
		t.Fatalf("a rail with no room for marks says nothing about the work:\n%s", rail)
	}
}

// A session on nothing must stay exactly the row it was: no mark, no children,
// and no key that behaves differently on it.
func TestSessionWithNoWorkIsNotExpandable(t *testing.T) {
	m := railWorkModel(t)
	m.selectSessionRow(t, "flaky-e2e")
	before := len(m.rows)
	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyRight}, {Code: tea.KeyLeft}} {
		updated, _ := m.handleKey(key)
		*m = *updated.(*Model)
	}
	if len(m.rows) != before {
		t.Fatalf("rows = %d, want %d", len(m.rows), before)
	}
	// Another session in this rail has work, so the column is reserved on
	// every row; what a session on nothing must not have is a mark in it.
	fold := ansi.Strip(m.railWorkFold(m.sessions[2]))
	if strings.TrimSpace(fold) != "" {
		t.Fatalf("a session on nothing wears %q", fold)
	}
	if got := ansi.StringWidth(fold); got != railFoldWidth {
		t.Fatalf("a session on nothing holds %d cells of the fold column, want %d", got, railFoldWidth)
	}
}

// Unfolding lists the same artifacts the work view lists, and drops the badge:
// once the rows are on screen the badge is the same thing said again, worse.
func TestUnfoldingASessionListsItsWork(t *testing.T) {
	m := railWorkModel(t)
	m.selectSessionRow(t, "add-rate-limiting")
	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	*m = *updated.(*Model)

	indexes := m.artifactRowIndexes()
	if len(indexes) != 2 {
		t.Fatalf("artifact rows = %d, want 2", len(indexes))
	}
	for _, i := range indexes {
		if m.rows[i].sess.ID != "add-rate-limiting" {
			t.Fatalf("row %d hangs off %q", i, m.rows[i].sess.ID)
		}
		if m.rows[i].depth != m.rows[indexes[0]-1].depth+1 {
			t.Fatalf("row %d is not a child of its session", i)
		}
	}
	rail := ansi.Strip(railLinesText(m.railLines(59, m.listBodyHeight())))
	if !strings.Contains(rail, "▾ ") {
		t.Fatalf("the open row still says it is shut:\n%s", rail)
	}
	if strings.Contains(rail, "claude · "+statusGlyph(status.Waiting)) {
		t.Fatalf("the badge is still on the meta line:\n%s", rail)
	}
	for _, want := range []string{"#838", "ABC-135518", "In Review"} {
		if !strings.Contains(rail, want) {
			t.Fatalf("rail is missing %q:\n%s", want, rail)
		}
	}

	updated, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	*m = *updated.(*Model)
	if got := m.artifactRowIndexes(); len(got) != 0 {
		t.Fatalf("left did not fold the work: %v", got)
	}
}

// The poll rebuilds the tree every couple of seconds. A cursor parked on a
// pull request has to come back to that pull request.
func TestRebuildKeepsTheCursorOnAnArtifact(t *testing.T) {
	m := railWorkModel(t)
	m.selectSessionRow(t, "add-rate-limiting")
	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	*m = *updated.(*Model)
	m.cursor = m.artifactRowIndexes()[1]
	key := rowKey(m.rows[m.cursor])

	m.rebuildRows()
	if got := rowKey(m.rows[m.cursor]); got != key {
		t.Fatalf("cursor moved to %q, want %q", got, key)
	}
}

// Folding from a child would otherwise leave the cursor on a row that is no
// longer there, which is a selection pointing at whatever slid into its index.
func TestFoldingFromAnArtifactLandsOnItsSession(t *testing.T) {
	m := railWorkModel(t)
	m.selectSessionRow(t, "add-rate-limiting")
	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	*m = *updated.(*Model)
	m.cursor = m.artifactRowIndexes()[1]

	updated, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	*m = *updated.(*Model)
	row, ok := m.cursorRow()
	if !ok || !row.isSession() || row.sess.ID != "add-rate-limiting" {
		t.Fatalf("cursor landed on %+v", row)
	}
}

// F is the whole tree's key, so it reaches a session's work as well as a
// group's sessions -- and never leaves the cursor on a row it just hid.
func TestFoldAllReachesSessionWork(t *testing.T) {
	m := railWorkModel(t)
	m.selectSessionRow(t, "add-rate-limiting")
	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	*m = *updated.(*Model)
	m.cursor = m.artifactRowIndexes()[0]

	updated, _ = m.handleKey(tea.KeyPressMsg{Code: 'F', Text: "F"})
	*m = *updated.(*Model)
	if got := m.artifactRowIndexes(); len(got) != 0 {
		t.Fatalf("fold all left work open: %v", got)
	}
	if m.cursor >= len(m.rows) {
		t.Fatalf("cursor %d is past the %d rows left", m.cursor, len(m.rows))
	}
	if row, _ := m.cursorRow(); row.isArtifact() {
		t.Fatal("cursor is still on a row the fold hid")
	}
}

// ↵ and o open the artifact itself; nothing else on the row does.
func TestArtifactRowOpensItsLink(t *testing.T) {
	m := railWorkModel(t)
	m.selectSessionRow(t, "add-rate-limiting")
	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	*m = *updated.(*Model)

	opened := make(chan string, 4)
	restore := openBrowser
	openBrowser = func(target string) error { opened <- target; return nil }
	t.Cleanup(func() { openBrowser = restore })

	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyEnter}, {Code: 'o', Text: "o"}} {
		m.cursor = m.artifactRowIndexes()[0]
		_, cmd := m.handleKey(key)
		if cmd == nil {
			t.Fatalf("%v returned no command", key)
		}
		cmd()
		select {
		case got := <-opened:
			if got != "https://github.com/example-org/sample-repo/pull/838" {
				t.Fatalf("opened %q", got)
			}
		default:
			t.Fatalf("%v opened nothing", key)
		}
	}
}

// The whole reason this row model is careful: a treeRow used to mean a group
// or a session, and every handler in the package reads one. Aimed at a pull
// request, the keys that kill, archive, delete, restart, rename, move, fork,
// reorder or prompt a session must reach no session at all -- not the wrong
// one, and not a plausible-looking empty one.
func TestArtifactRowRefusesTheSessionKeys(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "alpha", t.TempDir(), "")
	live := m.sessionRows()
	if len(live) != 1 {
		t.Fatalf("sessions = %d, want 1 (err=%q)", len(live), m.errBar.text)
	}
	sess := live[0]
	for i := range m.sessions {
		m.sessions[i].LaunchPrompt = "finish PR #838 for ABC-135518"
	}
	m.work = worktracker.New(
		fakeGit{remote: "git@github.com:example-org/sample-repo.git"},
		fakePRs{prs: map[string]forge.PR{
			"pr:example-org/sample-repo#838": {Repo: "example-org/sample-repo", Number: 838, State: forge.PROpen},
		}, health: forge.Health{OK: true}},
		fakeTickets{tickets: map[string]forge.Ticket{
			"ticket:ABC-135518": {Identifier: "ABC-135518", State: "In Review", StateType: "started"},
		}, health: forge.Health{OK: true}},
	)
	m.runWork(t)
	m.setWorkFolded(sess.ID, false)
	m.rebuildRows()

	artifacts := m.artifactRowIndexes()
	if len(artifacts) != 2 {
		t.Fatalf("artifact rows = %d, want 2", len(artifacts))
	}
	before, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	rowsBefore := len(m.rows)

	destroying := map[string]tea.KeyPressMsg{
		"x":        {Code: 'x', Text: "x"},
		"X":        {Code: 'X', Text: "X"},
		"d":        {Code: 'd', Text: "d"},
		"a":        {Code: 'a', Text: "a"},
		"u":        {Code: 'u', Text: "u"},
		"R":        {Code: 'R', Text: "R"},
		"v":        {Code: 'v', Text: "v"},
		"V":        {Code: 'V', Text: "V"},
		"A":        {Code: 'A', Text: "A"},
		"T":        {Code: 'T', Text: "T"},
		"f":        {Code: 'f', Text: "f"},
		"r":        {Code: 'r', Text: "r"},
		"alt+r":    {Code: 'r', Mod: tea.ModAlt},
		"m":        {Code: 'm', Text: "m"},
		".":        {Code: '.', Text: "."},
		"space":    {Code: tea.KeySpace, Text: " "},
		"shift+up": {Code: tea.KeyUp, Mod: tea.ModShift},
		"shift+dn": {Code: tea.KeyDown, Mod: tea.ModShift},
	}
	for name, key := range destroying {
		m.cursor = m.artifactRowIndexes()[0]
		m.errBar.text = ""
		updated, _ := m.handleKey(key)
		*m = *updated.(*Model)
		if m.mode != modeList {
			t.Fatalf("%s opened mode %v on an artifact row", name, m.mode)
		}
		if m.quick.active {
			t.Fatalf("%s opened the prompt on an artifact row", name)
		}
		if m.errBar.text == "" {
			t.Fatalf("%s was swallowed without saying so", name)
		}
		if !m.tmux.Exists(sess.ID) {
			t.Fatalf("%s killed the session behind the artifact", name)
		}
	}

	after, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if after.Archived != before.Archived || after.Name != before.Name ||
		after.Group != before.Group || after.Acked != before.Acked {
		t.Fatalf("the session changed under the artifact rows:\nbefore %+v\nafter  %+v", before, after)
	}
	if len(m.rows) != rowsBefore {
		t.Fatalf("rows = %d, want %d", len(m.rows), rowsBefore)
	}
	if !m.tmux.Exists(sess.ID) {
		t.Fatal("the session did not survive")
	}
}

// The rail is narrower than the work card, so a row that cannot hold the
// repository drops it. Truncating instead would leave "example-org/sample-repo…",
// which has lost the number that is the whole identity of the thing.
func TestArtifactRowDropsTheRepositoryBeforeTheNumber(t *testing.T) {
	m := railWorkModel(t)
	m.setWorkFolded("add-rate-limiting", false)
	m.rebuildRows()

	wide := ansi.Strip(railLinesText(m.railLines(59, m.listBodyHeight())))
	if !strings.Contains(wide, "example-org/sample-repo#838") {
		t.Fatalf("a rail with room for the repository dropped it:\n%s", wide)
	}
	narrow := ansi.Strip(railLinesText(m.railLines(29, m.listBodyHeight())))
	if strings.Contains(narrow, "example-org") {
		t.Fatalf("the repository crowded a narrow rail:\n%s", narrow)
	}
	for _, want := range []string{"#838", "open"} {
		if !strings.Contains(narrow, want) {
			t.Fatalf("a narrow rail lost %q:\n%s", want, narrow)
		}
	}
}

// railFoldModel is three sessions at one depth, so a column read off one row
// is comparable with the next. onWork decides whether any of them is on a
// pull request, which is the one thing the reserved fold column turns on.
func railFoldModel(t *testing.T, onWork bool) *Model {
	t.Helper()
	prompt := ""
	if onWork {
		prompt = "finish PR #838 for ABC-135518"
	}
	return railModel(t,
		railSession("db-migrations", "backend", "opencode", status.Waiting, "", 3*time.Minute),
		railSession("add-rate-limiting", "backend", "claude", status.Working, prompt, 41*time.Second),
		railSession("flaky-e2e", "backend", "claude", status.Errored, "", 22*time.Minute),
	)
}

// railRowLine is the rendered line a session's name is on.
func railRowLine(t *testing.T, rail, name string) string {
	t.Helper()
	for _, line := range strings.Split(rail, "\n") {
		if strings.Contains(line, name) {
			return line
		}
	}
	t.Fatalf("no row for %q:\n%s", name, rail)
	return ""
}

// glyphColumn is the cell a session row starts its status glyph in, read off
// the name it sits two cells ahead of.
func glyphColumn(t *testing.T, rail, name string) int {
	t.Helper()
	line := railRowLine(t, rail, name)
	return ansi.StringWidth(line[:strings.Index(line, name)]) - 2
}

// The rail says "child" by indenting, so a session indented past its
// neighbour reads as that neighbour's child. Having work is not being one:
// every session row starts its glyph in the same column whether or not it
// has a fold mark to put ahead of it.
func TestSessionRowsShareTheGlyphColumn(t *testing.T) {
	m := railFoldModel(t, true)
	rail := ansi.Strip(railLinesText(m.railLines(80, m.listBodyHeight())))
	t.Logf("rail with work in it:\n%s", rail)

	want := glyphColumn(t, rail, "add-rate-limiting")
	for _, name := range []string{"db-migrations", "flaky-e2e"} {
		if got := glyphColumn(t, rail, name); got != want {
			t.Fatalf("%s starts its glyph at column %d, add-rate-limiting at %d:\n%s",
				name, got, want, rail)
		}
	}
	if row := railRowLine(t, rail, "add-rate-limiting"); !strings.Contains(row, "▸ "+statusGlyph(status.Working)) {
		t.Fatalf("the fold mark is not in the column ahead of the glyph: %q", row)
	}
	for _, name := range []string{"db-migrations", "flaky-e2e"} {
		if row := railRowLine(t, rail, name); strings.ContainsAny(row, "▸▾") {
			t.Fatalf("%s wears a fold mark with nothing to fold: %q", name, row)
		}
	}
}

// The column costs every name two cells, so it is only reserved while
// something in the rail can fill it.
func TestARailWithNoWorkReservesNoFoldColumn(t *testing.T) {
	idle := railFoldModel(t, false)
	for _, sess := range idle.sessions {
		if fold := idle.railWorkFold(sess); fold != "" {
			t.Fatalf("%s reserved %q with no work anywhere in the rail", sess.Name, fold)
		}
	}
	bare := ansi.Strip(railLinesText(idle.railLines(80, idle.listBodyHeight())))
	t.Logf("rail with no work in it:\n%s", bare)

	working := railFoldModel(t, true)
	busy := ansi.Strip(railLinesText(working.railLines(80, working.listBodyHeight())))
	got, want := glyphColumn(t, bare, "flaky-e2e"), glyphColumn(t, busy, "flaky-e2e")-railFoldWidth
	if got != want {
		t.Fatalf("a work-free rail starts its glyphs at column %d, want %d:\n%s", got, want, bare)
	}
}

// railRawLine is the rail line naming text, escapes left on, which is the
// point when the thing under test is an escape.
func railRawLine(t *testing.T, m *Model, width int, name string) string {
	t.Helper()
	lines := m.railLines(width, m.listBodyHeight())
	for _, line := range lines {
		if strings.Contains(ansi.Strip(line.text), name) {
			return line.text
		}
	}
	t.Fatalf("no rail line names %s:\n%s", name, railLinesText(lines))
	return ""
}

// An unfolded row under a session is there to reach the pull request, and on
// a terminal that understands OSC 8 the identifier itself is the way there,
// as it already is on the work card.
func TestAnUnfoldedRailRowLinksItsPullRequestAndTicket(t *testing.T) {
	m := railWorkModel(t)
	m.setWorkFolded("add-rate-limiting", false)
	m.rebuildRows()

	pr := railRawLine(t, m, 80, "#838")
	if want := "\x1b]8;;https://github.com/example-org/sample-repo/pull/838\x1b\\"; !strings.Contains(pr, want) {
		t.Errorf("a pull request row carries no link to itself: %q", pr)
	}
	if !strings.Contains(pr, "\x1b]8;;\x1b\\") {
		t.Errorf("a linked row never closes its link, so the state joins it: %q", pr)
	}
	ticket := railRawLine(t, m, 80, "ABC-135518")
	if want := "\x1b]8;;https://linear.app/example/issue/ABC-135518\x1b\\"; !strings.Contains(ticket, want) {
		t.Errorf("a ticket row carries no link to itself: %q", ticket)
	}
	if got, want := textfmt.Width(pr), textfmt.Width(ansi.Strip(pr)); got != want {
		t.Errorf("a linked rail row measures %d cells against %d bare: the link is taking width", got, want)
	}
}

// A rail too narrow for the repository still names the number, and the number
// still opens the pull request.
func TestANarrowUnfoldedRailRowStillLinks(t *testing.T) {
	m := railWorkModel(t)
	m.setWorkFolded("add-rate-limiting", false)
	m.rebuildRows()
	line := railRawLine(t, m, 34, "#838")
	if strings.Contains(ansi.Strip(line), "example-org") {
		t.Fatalf("width 34 was meant to drop the repository: %q", ansi.Strip(line))
	}
	if !strings.Contains(line, "\x1b]8;;https://github.com/example-org/sample-repo/pull/838\x1b\\") {
		t.Errorf("the short form lost its link: %q", line)
	}
}
