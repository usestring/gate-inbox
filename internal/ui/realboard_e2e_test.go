package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/forge"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/worktracker"
)

// TestRealBoardWorkE2E runs the production work path over a real board: the
// state database named by E2E_STATE_DB, the transcripts its sessions point at
// through the same locator the poller uses, and live GitHub and Linear. It
// reads a copy of the database and skips unless the variable is set, so it is
// an operator check rather than CI: the log is the evidence, per session.
func TestRealBoardWorkE2E(t *testing.T) {
	src := os.Getenv("E2E_STATE_DB")
	if src == "" {
		t.Skip("E2E_STATE_DB unset")
	}
	dir := t.TempDir()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		data, err := os.ReadFile(src + suffix)
		if err != nil {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, "state.db"+suffix), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sessions, err := st.ListSessions(false)
	if err != nil {
		t.Fatal(err)
	}

	locator := newHistoryLocator()
	index := openHistoryIndex()
	var targets []search.Target
	for _, sess := range sessions {
		tool := sess.Tool
		if tool == "cc" {
			tool = search.ToolClaude
		}
		if target, ok := locator.Target(sess.ID, tool, sess.Cwd, sess.AgentSessionID); ok {
			targets = append(targets, target)
		}
	}
	t.Logf("sessions=%d targets=%d", len(sessions), len(targets))
	for i := 0; i < 500; i++ {
		p, err := index.Refresh(targets, historyRefreshBudget)
		if err != nil {
			t.Logf("refresh: %v", err)
		}
		if p.Done {
			break
		}
	}
	stats := index.Stats()
	t.Logf("index: %+v", stats)

	tracker := worktracker.New(worktracker.NewGit(), forge.NewGitHub(), forge.NewLinear())
	var tracked []worktracker.Session
	before, after := 0, 0
	for _, sess := range sessions {
		live := sess.Status != "dead"
		plain := workText(nil, st, nil, sess.ID, sess.LaunchPrompt, live)
		before += len(tracker.Discover(worktracker.Session{ID: sess.ID, Dir: sess.Cwd, Text: plain, Live: live}))
		ts := worktracker.Session{ID: sess.ID, Dir: sess.Cwd, Text: workText(nil, st, index, sess.ID, sess.LaunchPrompt, live), Live: live}
		after += len(tracker.Discover(ts))
		tracked = append(tracked, ts)
	}
	t.Logf("refs without transcript=%d with transcript=%d", before, after)
	persisted := 0
	for _, sess := range sessions {
		urls, err := st.OpenedPRs(sess.ID)
		if err != nil {
			t.Fatal(err)
		}
		persisted += len(urls)
	}
	t.Logf("opened pull requests persisted to the store copy: %d", persisted)
	tracker.Refresh(tracked) // PRs
	tracker.Refresh(tracked) // tickets adopted from PR branches
	gh, ln := tracker.Health()
	t.Logf("github ok=%v %s | linear ok=%v %s", gh.OK, gh.Reason, ln.OK, ln.Reason)

	sort.Slice(sessions, func(i, j int) bool { return sessions[i].Name < sessions[j].Name })
	for _, sess := range sessions {
		work := tracker.For(sess.ID)
		created := index.Created(sess.ID)
		if created == "" && len(work.Refs) == 0 {
			continue
		}
		var lines []string
		for _, ref := range work.Refs {
			key := ref.Key()
			state := "unlooked"
			if pr, ok := work.PRs[key]; ok {
				state = fmt.Sprintf("%s branch=%s", pr.State, pr.HeadRef)
			} else if tk, ok := work.Tickets[key]; ok {
				state = tk.State
			} else if work.Looked[key] {
				state = "none"
			}
			lines = append(lines, fmt.Sprintf("    %-6s %-24s #%-5d %-9s %-9s %s", ref.Kind, ref.Repo, ref.Number, ref.Identifier, ref.Provenance, state))
		}
		t.Logf("%s (%s) created=%d\n%s", sess.Name, sess.ID, strings.Count(created, "\n"), strings.Join(lines, "\n"))
	}
}
