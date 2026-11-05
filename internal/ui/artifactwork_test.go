package ui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/forge"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/worktracker"
)

const publishedPage = "https://claude.ai/artifact/H9XE1gDSESPDiKoD6MmZGc"

// publishingModel is a session on a pull request and a ticket that has also published a page,
// read the way the board reads it: from the publish call and its result in the transcript. The
// page another session published, which this one only linked, is in the same transcript.
func publishingModel(t *testing.T) *Model {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s1.jsonl")
	writeJSONLines(t, path,
		`{"type":"user","message":{"role":"user","content":"compare with https://claude.ai/artifact/SomebodyElses1"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"p1","name":"Artifact","input":{"file_path":"/tmp/s/fleet.html","title":"Fleet audit","icon":"chart"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"p1","content":"Published /tmp/s/fleet.html at `+publishedPage+` (Version 1)"}]}}`,
	)
	history := search.New(search.Options{})
	t.Cleanup(func() { history.Close() })
	for i := 0; i < 10; i++ {
		p, err := history.Refresh([]search.Target{{Key: "s1", Tool: search.ToolClaude, Path: path}}, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		if p.Done {
			break
		}
	}
	m := &Model{
		width: 100, height: 30, history: history, collapsed: map[string]bool{},
		work: worktracker.New(fakeGit{branch: "abc-100001-fork", remote: "https://github.com/example-org/sample-repo.git"},
			fakePRs{prs: map[string]forge.PR{
				"pr:example-org/sample-repo#838": {Repo: "example-org/sample-repo", Number: 838, State: forge.PROpen, Checks: forge.ChecksPassing},
			}, health: forge.Health{OK: true}},
			fakeTickets{tickets: map[string]forge.Ticket{
				"ticket:ABC-100001": {Identifier: "ABC-100001", State: "In Progress", StateType: "started"},
			}, health: forge.Health{OK: true}},
		),
		sessions: []store.Session{{
			ID: "s1", Name: "fork-work", Tool: "claude", Status: status.Idle,
			Cwd: "/repo", LaunchPrompt: "see PR #838 for the fork",
			CreatedAt: time.Now().Add(-12 * time.Minute),
		}},
	}
	m.runWork(t)
	return m
}

// A page the session published hangs under it beside its pull request and its ticket, below both,
// named by the title it was published under and opening the page itself. A page it only linked
// is somebody else's and stays off.
func TestAPublishedPageIsARowBesideThePullRequestsAndTickets(t *testing.T) {
	m := publishingModel(t)
	rows := m.workRowsFor("s1")
	var kinds []string
	for _, row := range rows {
		kinds = append(kinds, row.kind)
	}
	if got := strings.Join(kinds, " "); got != "PR TICKET ARTIFACT" {
		t.Fatalf("rows = %v, want the pull request, the ticket, then the one page it published", m.workLabels("s1"))
	}
	page := rows[2]
	if page.label != "Fleet audit" || page.url != publishedPage || page.detail != "claude.ai" {
		t.Errorf("page row = %+v", page)
	}
	if page.glyph != currentGlyphs.artifact {
		t.Errorf("page glyph = %q, want %q", page.glyph, currentGlyphs.artifact)
	}

	if badge, want := ansi.Strip(m.workBadge("s1", 60)), badgeGap+"1 pr · 1 issue · 1 artifact"; badge != want {
		t.Errorf("badge = %q, want %q", badge, want)
	}
}

// On the rail the page is a row like the others: its mark and title are drawn, and the footer
// names it an artifact, with open the action that takes the reader to it.
func TestAPublishedPageRowOnTheRail(t *testing.T) {
	m := publishingModel(t)
	m.mode = modeList
	m.setWorkFolded("s1", false)
	m.rebuildRows()
	var at = -1
	for i, row := range m.rows {
		if row.isArtifact() && row.art.kind == kindArtifact {
			at = i
		}
	}
	if at < 0 {
		t.Fatal("the rail has no row for the published page")
	}
	m.cursor = at
	if legend := m.rowLegend(); legend.title != "Artifact" {
		t.Errorf("legend title = %q, want Artifact", legend.title)
	}
	line := ansi.Strip(m.renderArtifactEntry(m.rows[at], false, 80, "", "", ""))
	for _, want := range []string{currentGlyphs.artifact, "Fleet audit", "claude.ai"} {
		if !strings.Contains(line, want) {
			t.Errorf("rail row %q lacks %q", line, want)
		}
	}
}

// The badge's words give ground the way they do for the other kinds, and a kind the session is not
// on is left out rather than counted as zero.
func TestCountTextNamesPublishedPages(t *testing.T) {
	for _, tc := range []struct {
		counts workCount
		form   countForm
		want   string
	}{
		{workCount{artifacts: 1}, countWords, "1 artifact"},
		{workCount{prs: 2, artifacts: 3}, countWords, "2 prs · 3 artifacts"},
		{workCount{tickets: 1, artifacts: 2}, countShort, "1is · 2ar"},
		{workCount{prs: 1, tickets: 1, artifacts: 1}, countTiny, "1p · 1i · 1a"},
	} {
		if got := countText(tc.counts, " · ", tc.form); got != tc.want {
			t.Errorf("countText(%+v) = %q, want %q", tc.counts, got, tc.want)
		}
	}
}
