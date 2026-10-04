package ui

import (
	"os"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/adopt"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

func TestManagedPaneRecoversOntoItsOwnRowID(t *testing.T) {
	dir := t.TempDir()
	socket := windowFixture(t, "foreign", 1, dir)
	const spawned, orphan = "spawnedrow", "orphanrow"
	addFixtureSession(t, socket, tmux.SessionName(spawned), dir)
	addFixtureSession(t, socket, tmux.SessionName(orphan), dir)
	waitForPrompts(t, socket, 3)

	st := newFixtureStore(t)
	run := newFixtureRun(t, st, socket)

	if err := st.CreateGroup("sample-repo", "sample-repo"); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	parent := store.Session{
		ID: "parentrow", Name: "portal-pull-request", Tool: "claude", Cwd: dir,
		Group: "sample-repo", CreatedAt: time.Now(), LastStatusAt: time.Now(),
	}
	if err := st.CreateSession(parent); err != nil {
		t.Fatalf("CreateSession parent: %v", err)
	}
	child := store.Session{
		ID: spawned, Name: "portal-forensics", Tool: "claude", Cwd: dir,
		Group: "sample-repo", ParentID: parent.ID,
		CreatedAt: time.Now(), LastStatusAt: time.Now(),
	}
	if err := st.CreateSession(child); err != nil {
		t.Fatalf("CreateSession child: %v", err)
	}

	if _, err := run.take(adopt.Panes(socket), adopt.NewProcTable()); err != nil {
		t.Fatalf("take: %v", err)
	}

	rows, err := st.ListSessions(true)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	byID := map[string]store.Session{}
	panes := map[string]int{}
	for _, sess := range rows {
		byID[sess.ID] = sess
		if sess.TmuxPaneID != "" {
			panes[sess.TmuxPaneID]++
		}
	}

	for pane, count := range panes {
		if count > 1 {
			t.Errorf("pane %s has %d rows; adoption minted a duplicate for a session it already owns", pane, count)
		}
	}
	kept, ok := byID[spawned]
	if !ok {
		t.Fatalf("the spawned session's row is gone")
	}
	if kept.ParentID != parent.ID || kept.Group != "sample-repo" {
		t.Errorf("the spawned row lost its place: parent %q group %q, want %q and %q",
			kept.ParentID, kept.Group, parent.ID, "sample-repo")
	}
	for _, candidate := range adopt.Panes(socket) {
		if candidate.Session != tmux.SessionName(spawned) {
			continue
		}
		for _, row := range rows {
			if row.TmuxPaneID == candidate.PaneID {
				t.Errorf("spawned pane acquired an adopted row: %+v", row)
			}
		}
	}

	recovered, ok := byID[orphan]
	if !ok {
		t.Fatalf("no row under %s; the orphan was either left behind or recovered onto a stranger id", orphan)
	}
	if recovered.TmuxPaneID == "" {
		t.Errorf("the recovered row holds no pane id, so nothing can reach its agent")
	}
	if len(rows) != 4 {
		t.Errorf("board holds %d rows, want 4", len(rows))
	}
}

func TestManagedPaneRefusedWhenItsRowPostdatesTheScansRows(t *testing.T) {
	dir := t.TempDir()
	socket := windowFixture(t, "foreign", 1, dir)
	const spawned = "latespawnrow"
	addFixtureSession(t, socket, tmux.SessionName(spawned), dir)
	waitForPrompts(t, socket, 2)

	st := newFixtureStore(t)
	run := newFixtureRun(t, st, socket)
	if err := st.CreateSession(store.Session{
		ID: spawned, Name: "late-spawn", Tool: "claude", Cwd: dir,
		CreatedAt: time.Now(), LastStatusAt: time.Now(),
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	taken, err := run.take(adopt.Panes(socket), adopt.NewProcTable())
	if err != nil {
		t.Fatalf("take aborted the scan over a row it should simply have passed over: %v", err)
	}
	if taken != 1 {
		t.Errorf("took %d panes, want only the foreign one; rejections were %s", taken, rejectionSummary(run.rejected))
	}

	rows, err := st.ListSessions(true)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	panes := map[string]int{}
	for _, sess := range rows {
		if sess.TmuxPaneID != "" {
			panes[sess.TmuxPaneID]++
		}
	}
	for pane, count := range panes {
		if count > 1 {
			t.Errorf("pane %s has %d rows", pane, count)
		}
	}
	if len(rows) != 2 {
		t.Errorf("board holds %d rows, want 2 (the spawn and the foreign pane)", len(rows))
	}
}

func TestKnownPaneTracksItsForegroundHarness(t *testing.T) {
	for _, tc := range []struct {
		name, command, want string
		fresh               bool
	}{
		{"changed harness", "codex", "codex", false},
		{"same harness", "opencode", "opencode", false},
		{"shell with stale agent output", "bash", "opencode", false},
		{"exit after the scan", "codex", "codex", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newFixtureStore(t)
			sess := store.Session{ID: "swap", Name: "swap", Tool: "opencode", AgentSessionID: "old-conversation", Status: status.Errored}
			if err := st.CreateSession(sess); err != nil {
				t.Fatal(err)
			}
			socket := windowFixture(t, tmux.SessionName(sess.ID), 2, t.TempDir())
			driver := newTestDriver(t, socket)
			manager := hooks.NewManager(t.TempDir())
			if err := os.MkdirAll(manager.Dir(), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(manager.ExitFile(sess.ID), []byte("0"), 0o600); err != nil {
				t.Fatal(err)
			}
			run := &adoptRun{stor: st, hooks: manager, driver: driver, agentTools: map[string]bool{"opencode": true, "codex": true}, tools: []adopt.Tool{{Name: "opencode", Command: "opencode"}, {Name: "codex", Command: "codex"}}}
			candidates := adopt.Panes(socket)
			if len(candidates) != 2 {
				t.Fatalf("panes = %d, want 2", len(candidates))
			}
			candidates[0].Command = tc.command
			candidates[1].Command = "opencode"
			observedAfter := time.Now()
			if tc.fresh {
				observedAfter = observedAfter.Add(-time.Hour)
			}
			if err := run.reconcileTools([]store.Session{sess}, candidates, observedAfter); err != nil {
				t.Fatal(err)
			}
			if _, _, exists := manager.ReadExit(sess.ID); exists != (tc.command == "bash" || tc.fresh) {
				t.Fatal("historical exit did not follow foreground liveness")
			}
			got, err := st.Get(sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Tool != tc.want {
				t.Fatalf("tool = %q, want %q", got.Tool, tc.want)
			}
			if tc.want == "codex" {
				if got.AgentSessionID != "" {
					t.Fatal("old harness conversation survived")
				}
				cfg, err := config.Default()
				if err != nil {
					t.Fatal(err)
				}
				engine, err := status.NewEngine(cfg)
				if err != nil {
					t.Fatal(err)
				}
				pane := "Error: previous tool output\n• Working (12s • esc to interrupt)\n\n› Ask Codex to do anything"
				if state, _ := engine.Match(got.Tool, pane); state != status.Working {
					t.Fatalf("state = %q", state)
				}
			} else if got.AgentSessionID != sess.AgentSessionID {
				t.Fatal("unchanged harness lost its conversation")
			}
		})
	}
}
