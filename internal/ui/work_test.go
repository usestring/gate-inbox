package ui

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/forge"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
	"github.com/usestring/gate-inbox/internal/workspec"
	"github.com/usestring/gate-inbox/internal/worktracker"
)

type fakeGit struct{ branch, remote string }

func (f fakeGit) Branch(string) (string, bool)   { return f.branch, f.branch != "" }
func (f fakeGit) Remote(string) (string, bool)   { return f.remote, f.remote != "" }
func (f fakeGit) HasSubmodules(string) bool      { return false }
func (f fakeGit) Names(string) map[string]string { return nil }

type fakePRs struct {
	prs    map[string]forge.PR
	health forge.Health
}

func (f fakePRs) PRs([]workspec.Ref) (map[string]forge.PR, forge.Health) { return f.prs, f.health }

type fakeTickets struct {
	tickets map[string]forge.Ticket
	health  forge.Health
}

func (f fakeTickets) Tickets([]workspec.Ref) (map[string]forge.Ticket, forge.Health) {
	return f.tickets, f.health
}

// trackedModel wires a model to a tracker whose sources are fakes, then runs
// the same discover-and-resolve the ticker runs.
func trackedModel(t *testing.T, prs fakePRs, tickets fakeTickets) *Model {
	t.Helper()
	m := &Model{
		width:  100,
		height: 30,
		work:   worktracker.New(fakeGit{branch: "abc-135518-fork", remote: "git@github.com:example-org/sample-repo.git"}, prs, tickets),
		sessions: []store.Session{{
			ID: "s1", Name: "fork-work", Tool: "claude", Status: status.Waiting,
			Cwd: "/repo", LaunchPrompt: "see PR #838 for the fork",
			CreatedAt: time.Now().Add(-12 * time.Minute),
		}},
	}
	m.runWork(t)
	return m
}

// runWork does one refresh the way the ticker does: refreshWork reads the model
// on the event loop and hands back the command that does the I/O, so a test
// that only calls refreshWork discovers nothing at all.
func (m *Model) runWork(t *testing.T) {
	t.Helper()
	cmd := m.refreshWork()
	if cmd == nil {
		t.Fatal("refreshWork returned no command")
	}
	cmd()
}

// workLabels is what the tree says a session is on, which is the one place
// that answer is now given.
func (m *Model) workLabels(sessID string) []string {
	var labels []string
	for _, row := range m.workRowsFor(sessID) {
		labels = append(labels, row.label+" ("+row.detail+")")
	}
	return labels
}

// The badge counts every artifact rather than naming two of them, and it
// leads with a mark when any of them wants a person: a folded row whose pull
// request has a red check must not read as a healthy one.
func TestWorkBadgeCountsEveryArtifact(t *testing.T) {
	m := trackedModel(t,
		fakePRs{prs: map[string]forge.PR{
			"pr:example-org/sample-repo#838": {
				Repo: "example-org/sample-repo", Number: 838, State: forge.PROpen,
				Checks: forge.ChecksFailing, Review: forge.ReviewChangesRequested,
			},
		}, health: forge.Health{OK: true}},
		fakeTickets{tickets: map[string]forge.Ticket{
			"ticket:ABC-135518": {Identifier: "ABC-135518", State: "In Review", StateType: "started"},
		}, health: forge.Health{OK: true}},
	)

	badge := ansi.Strip(m.workBadge("s1", 40))
	want := badgeGap + statusGlyph(status.Waiting) + " 1 pr · 1 issue"
	if badge != want {
		t.Errorf("badge = %q, want %q", badge, want)
	}
	// The names live in the tree instead, and both of them are there.
	labels := m.workLabels("s1")
	if len(labels) != 2 {
		t.Fatalf("tree rows = %v, want the pull request and the ticket", labels)
	}
	for _, want := range []string{"example-org/sample-repo#838", "ABC-135518"} {
		if !strings.Contains(strings.Join(labels, " "), want) {
			t.Errorf("tree rows %v are missing %q", labels, want)
		}
	}
}

// A session on more artifacts than the line can hold still says how many there
// are, so a folded row can never under-report without saying that it has.
func TestABadgeTooNarrowForEveryMarkCountsTheRest(t *testing.T) {
	m := trackedModel(t,
		fakePRs{prs: map[string]forge.PR{
			"pr:example-org/sample-repo#838": {Repo: "example-org/sample-repo", Number: 838, State: forge.PROpen, Mergeable: true},
		}, health: forge.Health{OK: true}},
		fakeTickets{tickets: map[string]forge.Ticket{
			"ticket:ABC-135518": {Identifier: "ABC-135518", State: "In Review", StateType: "started"},
		}, health: forge.Health{OK: true}},
	)

	// Room for the separator and one mark only.
	if got, want := ansi.Strip(m.workBadge("s1", badgeSeparator+1)), badgeGap+"+2"; got != want {
		t.Errorf("badge = %q, want %q", got, want)
	}
	// Narrower than that and the fold arrow beside the name is what says the
	// session has work at all.
	if got := ansi.Strip(m.workBadge("s1", badgeSeparator)); got != "" {
		t.Errorf("badge = %q, want nothing on a line with no room", got)
	}
}

// A reference nothing has answered for is a row saying so. Dropping it made a
// session whose every reference went unanswered read exactly like a session
// working on nothing.
func TestAnUnansweredReferenceStillGetsARow(t *testing.T) {
	m := trackedModel(t,
		fakePRs{health: forge.Health{Reason: "gh: not authenticated"}},
		fakeTickets{health: forge.Health{Reason: "LINEAR_API_KEY is not set"}},
	)

	labels := m.workLabels("s1")
	if len(labels) != 2 {
		t.Fatalf("tree rows = %v, want a row for each unanswered reference", labels)
	}
	for _, label := range labels {
		if !strings.Contains(label, "not looked up") {
			t.Errorf("row %q does not say the reference was never looked up", label)
		}
	}
	if badge := ansi.Strip(m.workBadge("s1", 40)); badge == "" {
		t.Error("a session whose references went unanswered wears no badge")
	}
}

// The other silence is a real answer. GitHub and Linear were asked and had
// nothing, which is what a ticket-shaped mention like "CVE-2024" deserves.
func TestAReferenceTheSourcesDeniedIsDropped(t *testing.T) {
	m := trackedModel(t,
		fakePRs{health: forge.Health{OK: true}},
		fakeTickets{health: forge.Health{OK: true}},
	)
	if labels := m.workLabels("s1"); len(labels) != 0 {
		t.Errorf("tree rows = %v, want none: both sources answered and had nothing", labels)
	}
}

// A row with no work must not grow a separator, or every untracked session
// gains a trailing " · " that says nothing.
func TestWorkBadgeIsEmptyWhenThereIsNoWork(t *testing.T) {
	m := trackedModel(t,
		fakePRs{health: forge.Health{OK: true}},
		fakeTickets{health: forge.Health{OK: true}},
	)
	m.work = worktracker.New(fakeGit{}, fakePRs{health: forge.Health{OK: true}}, fakeTickets{health: forge.Health{OK: true}})
	m.sessions[0].LaunchPrompt = "no references here"
	m.runWork(t)

	if badge := ansi.Strip(m.workBadge("s1", 40)); badge != "" {
		t.Errorf("badge = %q, want empty", badge)
	}
}

func TestSessionRowCarriesTheWorkBadge(t *testing.T) {
	m := trackedModel(t,
		fakePRs{prs: map[string]forge.PR{
			"pr:example-org/sample-repo#838": {
				Repo: "example-org/sample-repo", Number: 838, State: forge.PROpen,
				Checks: forge.ChecksPassing, Review: forge.ReviewPending, Mergeable: true,
			},
		}, health: forge.Health{OK: true}},
		fakeTickets{tickets: map[string]forge.Ticket{
			"ticket:ABC-135518": {Identifier: "ABC-135518", State: "In Review", StateType: "started"},
		}, health: forge.Health{OK: true}},
	)

	for _, width := range []int{40, 100, 200} {
		row := ansi.Strip(m.renderTreeRow(treeRow{sess: m.sessions[0]}, false, width, 0, panelHex()))
		if got := ansi.StringWidth(row); got > width {
			t.Errorf("width %d: row overflows to %d: %q", width, got, row)
		}
		t.Logf("width %3d |%s|", width, row)
	}
}

// adoptedWorkID names the one adopted row the work tests plant.
const adoptedWorkID = "adoptedwork"

// adoptedShowing plants a session the manager did not start: a pane on
// somebody else's tmux server, reached through the store row and the driver
// mapping adoption leaves behind.
//
// It carries no launch prompt and its directory is no repository, so what the
// pane is showing is the only thing discovery has to read — the shape of every
// real adopted row.
func adoptedShowing(t *testing.T, command string, prs fakePRs, tickets fakeTickets) (*Model, string, string) {
	t.Helper()
	m := buildModel(t)
	socket, pane := uiForeignServer(t, command)
	if err := m.store.CreateSession(store.Session{
		ID:           adoptedWorkID,
		Name:         "adopted",
		Tool:         "claude",
		Cwd:          t.TempDir(),
		Status:       status.Idle,
		CreatedAt:    time.Now(),
		LastStatusAt: time.Now(),
		TmuxSocket:   socket,
		TmuxPaneID:   pane,
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := m.tmux.Adopt(adoptedWorkID, tmux.Target{Socket: socket, Name: pane}); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	m.applyCmd(t, nil)
	m.work = worktracker.New(fakeGit{}, prs, tickets)

	var adopted store.Session
	for _, sess := range m.sessions {
		if sess.ID == adoptedWorkID {
			adopted = sess
		}
	}
	if adopted.ID == "" {
		t.Fatalf("adopted session never loaded, rows = %v", sessionNames(m))
	}
	if adopted.LaunchPrompt != "" {
		t.Fatalf("an adopted row must have no launch prompt, got %q", adopted.LaunchPrompt)
	}
	return m, socket, pane
}

func workFakes() (fakePRs, fakeTickets) {
	return fakePRs{prs: map[string]forge.PR{
			"pr:example-org/sample-repo#838": {
				Repo: "example-org/sample-repo", Number: 838, State: forge.PROpen,
				Checks: forge.ChecksPassing, Mergeable: true,
			},
		}, health: forge.Health{OK: true}},
		fakeTickets{tickets: map[string]forge.Ticket{
			"ticket:ABC-135518": {Identifier: "ABC-135518", State: "In Review", StateType: "started"},
		}, health: forge.Health{OK: true}}
}

// The gap the pane read closes. An adopted session has no launch prompt and no
// final capture, so before this every adopted row had nothing to scan and so
// showed no pull request and no ticket, however plainly its pane named them.
func TestWorkFindsReferencesOnAnAdoptedPane(t *testing.T) {
	prs, tickets := workFakes()
	m, socket, pane := adoptedShowing(t,
		`printf 'opened https://github.com/example-org/sample-repo/pull/838 for ABC-135518\n'; sleep 300`,
		prs, tickets)
	foreignPaneContains(t, socket, pane, func(out string) bool {
		return strings.Contains(out, "ABC-135518")
	})

	m.runWork(t)

	labels := strings.Join(m.workLabels(adoptedWorkID), " ")
	for _, want := range []string{"#838", "ABC-135518"} {
		if !strings.Contains(labels, want) {
			t.Errorf("tree rows %q are missing %q", labels, want)
		}
	}
}

// A reference an application painted in two colours arrives from tmux with an
// escape sequence sitting inside it, which every pattern in workspec reads as
// a word boundary. Stripping is what keeps a styled line scannable.
func TestWorkStripsAnsiFromTheCapturedPane(t *testing.T) {
	prs, tickets := workFakes()
	m, socket, pane := adoptedShowing(t,
		`printf 'opened https://github.com/example-org/sample-repo/pull/\033[31m838 for ABC-\033[32m135518\033[0m\n'; sleep 300`,
		prs, tickets)
	foreignPaneContains(t, socket, pane, func(out string) bool {
		return strings.Contains(out, "ABC-135518")
	})

	raw, err := m.tmux.CapturePane(adoptedWorkID)
	if err != nil {
		t.Fatalf("CapturePane: %v", err)
	}
	// Without this the test would pass on a capture that never split the
	// references, which proves nothing about stripping.
	for _, ref := range []string{"pull/838", "ABC-135518"} {
		if strings.Contains(raw, ref) {
			t.Fatalf("colour did not split %q, so this test is vacuous: %q", ref, raw)
		}
	}

	m.runWork(t)

	labels := strings.Join(m.workLabels(adoptedWorkID), " ")
	for _, want := range []string{"#838", "ABC-135518"} {
		if !strings.Contains(labels, want) {
			t.Errorf("tree rows %q are missing %q", labels, want)
		}
	}
}

// A dead session is not captured from. Its pane is normally gone, but an
// adopted one outlives the row that pointed at it, and reading that would
// credit a session that ended with whatever somebody else is doing now.
func TestWorkDoesNotCaptureADeadSession(t *testing.T) {
	prs, tickets := workFakes()
	m, socket, pane := adoptedShowing(t,
		`printf 'opened https://github.com/example-org/sample-repo/pull/838 for ABC-135518\n'; sleep 300`,
		prs, tickets)
	foreignPaneContains(t, socket, pane, func(out string) bool {
		return strings.Contains(out, "ABC-135518")
	})

	// The pane really is readable, so an empty badge below is the live gate
	// and not a fixture that never showed anything.
	if text := workText(m.tmux, nil, nil, adoptedWorkID, "", true); !strings.Contains(text, "ABC-135518") {
		t.Fatalf("a live read of the same pane found nothing: %q", text)
	}

	for i := range m.sessions {
		if m.sessions[i].ID == adoptedWorkID {
			m.sessions[i].Status = status.Dead
		}
	}
	m.runWork(t)

	if labels := m.workLabels(adoptedWorkID); len(labels) != 0 {
		t.Errorf("a dead session was credited with %v", labels)
	}
}

// workTreeModel plants three sessions that are on something and one that is
// on nothing, which is the shape the screen has to hold: the grouping, the
// ordering promise, and a session with more than one artifact under it.
func workTreeModel(t *testing.T) *Model {
	t.Helper()
	prs := fakePRs{prs: map[string]forge.PR{
		"pr:example-org/sample-repo#333": {
			Repo: "example-org/sample-repo", Number: 333, State: forge.PRMerged,
			Checks: forge.ChecksPassing, Mergeable: true,
			URL: "https://github.com/example-org/sample-repo/pull/333",
		},
		"pr:example-org/sample-repo#892": {
			Repo: "example-org/sample-repo", Number: 892, State: forge.PRMerged, Mergeable: true,
			URL: "https://github.com/example-org/sample-repo/pull/892",
		},
		"pr:example-org/sample-repo#700": {
			Repo: "example-org/sample-repo", Number: 700, State: forge.PROpen,
			Checks: forge.ChecksFailing, Review: forge.ReviewChangesRequested,
			URL: "https://github.com/example-org/sample-repo/pull/700",
		},
	}, health: forge.Health{OK: true}}
	tickets := fakeTickets{tickets: map[string]forge.Ticket{
		"ticket:ABC-133683": {
			Identifier: "ABC-133683", State: "In Progress", StateType: "started",
			URL: "https://linear.app/example/issue/ABC-133683",
		},
	}, health: forge.Health{OK: true}}

	m := &Model{
		width: 100, height: 30,
		work: worktracker.New(fakeGit{}, prs, tickets),
		sessions: []store.Session{
			{ID: "s1", Name: "sample-repo-5", Tool: "claude", Status: status.Waiting, Cwd: "/repo",
				LaunchPrompt: "opened https://github.com/example-org/sample-repo/pull/333 for ABC-133683"},
			{ID: "s2", Name: "sample-repo-11", Tool: "claude", Status: status.Idle, Cwd: "/repo",
				LaunchPrompt: "https://github.com/example-org/sample-repo/pull/892"},
			{ID: "s3", Name: "sample-repo-21", Tool: "codex", Status: status.Working, Cwd: "/repo",
				LaunchPrompt: "https://github.com/example-org/sample-repo/pull/700"},
			{ID: "s4", Name: "sample-repo-33", Tool: "claude", Status: status.Idle, Cwd: "/repo",
				LaunchPrompt: "no references in this one"},
		},
	}
	m.runWork(t)
	return m
}

// A pull request the session opened stays its work after the transcript that
// proved it is gone: the store remembers what the index has seen, and the next
// index -- a restart onto a new conversation, a transcript the CLI deleted --
// starts empty without the board forgetting.
func TestOpenedPullRequestsOutliveTheIndex(t *testing.T) {
	st, err := store.Open(filepath.Join(tmuxtest.ScratchDir(t), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.CreateSession(store.Session{ID: "s", Name: "s", Tool: "claude", Cwd: "/repo", Status: "idle"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "s.jsonl")
	writeJSONLines(t, path,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"gh-pr-create.sh --title x"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"https://github.com/o/r/pull/5\n"}]}}`,
	)
	first := search.New(search.Options{})
	defer first.Close()
	for i := 0; i < 10; i++ {
		if p, err := first.Refresh([]search.Target{{Key: "s", Tool: search.ToolClaude, Path: path}}, 1<<20); err != nil {
			t.Fatal(err)
		} else if p.Done {
			break
		}
	}
	if got := openedPRs(st, first, "s"); len(got) != 1 || got[0] != "https://github.com/o/r/pull/5" {
		t.Fatalf("with the transcript: %v", got)
	}

	second := search.New(search.Options{})
	defer second.Close()
	if got := openedPRs(st, second, "s"); len(got) != 1 || got[0] != "https://github.com/o/r/pull/5" {
		t.Fatalf("after the index forgot: %v, want the store's copy", got)
	}
	if text := workText(nil, st, second, "s", "", false); !strings.Contains(text, "https://github.com/o/r/pull/5") {
		t.Errorf("workText lacks the remembered pull request:\n%s", text)
	}
	if got := openedPRs(nil, second, "s"); len(got) != 0 {
		t.Errorf("no store and an empty index still answered %v", got)
	}
}

func writeJSONLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A child's work rolls up onto its parent, tagged with the child that produced
// it, so a folded parent still says what came out of its subtree. Every
// session here is on the branch's ABC-135518, which the parent already carries
// as its own and so does not repeat, and the archived child's work stays put
// away while its own live child's does not.
func TestChildWorkRollsUpOntoTheParent(t *testing.T) {
	pr := func(n int) forge.PR {
		return forge.PR{Repo: "example-org/sample-repo", Number: n, State: forge.PROpen, Checks: forge.ChecksPassing, Mergeable: true}
	}
	_, tickets := workFakes()
	prs := fakePRs{prs: map[string]forge.PR{
		"pr:example-org/sample-repo#838": pr(838),
		"pr:example-org/sample-repo#900": pr(900),
		"pr:example-org/sample-repo#901": pr(901),
		"pr:example-org/sample-repo#902": pr(902),
	}, health: forge.Health{OK: true}}
	session := func(id, name, parent, prompt string, archived bool) store.Session {
		return store.Session{
			ID: id, Name: name, ParentID: parent, Tool: "claude", Status: status.Idle,
			Cwd: "/repo", LaunchPrompt: prompt, Archived: archived,
			CreatedAt: time.Now().Add(-12 * time.Minute),
		}
	}
	m := &Model{
		width: 100, height: 30,
		work: worktracker.New(fakeGit{branch: "abc-135518-fork", remote: "git@github.com:example-org/sample-repo.git"}, prs, tickets),
		sessions: []store.Session{
			session("p", "planner", "", "plan the fork", false),
			session("c", "builder", "p", "see PR #838", false),
			session("g", "fixer", "c", "see PR #900", false),
			session("a", "shelved", "p", "see PR #901", true),
			session("r", "rescuer", "a", "see PR #902", false),
		},
	}
	m.runWork(t)

	from := func(sessID string) map[string]string {
		out := map[string]string{}
		for _, row := range m.workRowsFor(sessID) {
			out[row.label] = row.from
		}
		return out
	}
	parent := from("p")
	want := map[string]string{
		"ABC-135518":                  "",
		"example-org/sample-repo#838": "builder",
		"example-org/sample-repo#900": "fixer",
		"example-org/sample-repo#902": "rescuer",
	}
	if !maps.Equal(parent, want) {
		t.Errorf("parent rows (label → child) = %v, want %v", parent, want)
	}
	// The rollup and the pull-requests-first sort compose: a child's pull
	// request outranks the parent's own ticket, which the branch names and so
	// has the strongest evidence of anything here.
	rows := m.workRowsFor("p")
	if last := rows[len(rows)-1]; last.kind != "TICKET" || last.from != "" {
		t.Errorf("parent rows = %v, want the parent's own ticket below every rolled-up pull request", m.workLabels("p"))
	}
	// The child keeps its own rows untagged, and rolls its own child up in turn.
	child := from("c")
	if child["example-org/sample-repo#838"] != "" || child["example-org/sample-repo#900"] != "fixer" {
		t.Errorf("child rows (label → child) = %v", child)
	}
}

// The child's name gives way before the artifact's number does: a narrow rail
// keeps "#900" whole and drops the tag, and a wide one draws both.
func TestRolledUpRowDropsTheChildBeforeTheNumber(t *testing.T) {
	m := &Model{}
	art := workRow{
		kind: "PR", label: "example-org/sample-repo#900", short: "#900",
		detail: "open ✓", glyph: "●", from: "terminal-gate-inbox-3",
	}
	entry := treeRow{art: &art}
	narrow := ansi.Strip(m.renderArtifactEntry(entry, false, 18, "", "", ""))
	if !strings.Contains(narrow, "#900") || strings.Contains(narrow, "↳") {
		t.Errorf("narrow row = %q, want #900 and no child tag", narrow)
	}
	wide := ansi.Strip(m.renderArtifactEntry(entry, false, 90, "", "", ""))
	if !strings.Contains(wide, "example-org/sample-repo#900") || !strings.Contains(wide, "↳ terminal-gate-inbox-3") {
		t.Errorf("wide row = %q, want the full label and the child tag", wide)
	}
}

// A pull request is what the operator acts on and a ticket is the trail, so a
// pull request sorts above a ticket even when the ticket has the stronger
// evidence: this session's branch names ABC-135518, and only its prompt names
// the pull request.
func TestPullRequestsSortAboveTickets(t *testing.T) {
	prs, tickets := workFakes()
	m := trackedModel(t, prs, tickets)

	rows := m.workRowsFor("s1")
	if len(rows) != 2 {
		t.Fatalf("rows = %v, want the pull request and the ticket", m.workLabels("s1"))
	}
	if rows[0].kind != "PR" || rows[1].kind != "TICKET" {
		t.Errorf("rows = %v, want the pull request first", m.workLabels("s1"))
	}
}

func TestSortWorkRowsPutsKindBeforeBlocker(t *testing.T) {
	rows := []workRow{
		{kind: "TICKET", label: "ABC-1"},
		{kind: "PR", label: "quiet"},
		{kind: "TICKET", label: "ABC-2"},
		{kind: "PR", label: "blocked", needsYou: true},
	}
	sortWorkRows(rows)
	var got []string
	for _, row := range rows {
		got = append(got, row.label)
	}
	if want := "blocked quiet ABC-1 ABC-2"; strings.Join(got, " ") != want {
		t.Errorf("order = %v, want %s", got, want)
	}
}
