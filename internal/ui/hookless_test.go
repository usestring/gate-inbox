package ui

import (
	"testing"

	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/sysstat"
)

// A pane whose turn has visibly ended, and a status file frozen at idle by
// the last hook that ever ran in it. The pair is what a hookless session looks
// like: applyHookStatus lets an idle hook stand against a finished pane on
// purpose -- a mid-turn idle is a compact, and re-deriving finished there
// would re-raise a turn end the operator already dealt with -- so while the
// file is trusted, the row cannot come off idle however plainly the pane says
// the turn is over.
const (
	hooklessPane = "here is the result\n\n✻ Baked for 5s\n\n❯ \n"
	hooklessID   = "hookless"
)

// The control, and the regression this change most has to avoid: a session
// whose agent still carries the flag is unaffected, and its file still
// outranks its pane.
func TestAWiredSessionStillReadsItsHookFile(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: hooklessID, Tool: "claude-hooked"}
	writeHookStatus(t, m, sess.ID, status.Idle)

	if got := deriveStatus(t, m, sess, hooklessPane, true); got != status.Idle {
		t.Fatalf("status = %q, want %q: a wired session's hook file is still tier 1", got, status.Idle)
	}
}

// And the change: once the pass has found that nothing in the pane is wired to
// the hooks, the file stops being evidence and the pane decides.
func TestAHooklessSessionFallsThroughToItsPane(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: hooklessID, Tool: "claude-hooked"}
	writeHookStatus(t, m, sess.ID, status.Idle)
	m.poller.hookless[sess.ID] = true

	if got := deriveStatus(t, m, sess, hooklessPane, true); got != status.Finished {
		t.Errorf("status = %q, want %q: a hookless row is still being read off a file nobody writes", got, status.Finished)
	}
}

// The file is left where it is. Another session may be reading it, the
// operator may have written it by hand to unblock delivery, and a relaunch
// that restores the flag picks it straight back up -- so falling through to
// the pane must not also be a delete.
func TestFallingThroughDoesNotDeleteTheFile(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: hooklessID, Tool: "claude-hooked"}
	writeHookStatus(t, m, sess.ID, status.Idle)
	m.poller.hookless[sess.ID] = true

	deriveStatus(t, m, sess, hooklessPane, true)
	if _, ok := m.hooks.Read(sess.ID); !ok {
		t.Errorf("the status file is gone; a hookless session's file is not the board's to remove")
	}
}

// The mark lapses on its own. The poller rebuilds the set from the live
// process tree every pass, so a session that gets its wiring back is read off
// its file again on the next one without anything sweeping the mark away.
func TestTheMarkLapsesWhenTheWiringComesBack(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: hooklessID, Tool: "claude-hooked"}
	writeHookStatus(t, m, sess.ID, status.Idle)
	m.poller.hookless[sess.ID] = true
	if got := deriveStatus(t, m, sess, hooklessPane, true); got != status.Finished {
		t.Fatalf("status = %q, want the pane's %q while hookless", got, status.Finished)
	}

	delete(m.poller.hookless, sess.ID)
	if got := deriveStatus(t, m, sess, hooklessPane, true); got != status.Idle {
		t.Errorf("status = %q, want %q: the row did not go back to its file", got, status.Idle)
	}
}

// hooklessTree is the guard, and these are the ways it must refuse to call a
// session hookless. The asymmetry is the point: a false positive marks a row
// that is working perfectly, on every pass, and teaches the operator to ignore
// the mark; a false negative is the status quo.
func TestHooklessTreeRefusesToGuess(t *testing.T) {
	// The shape the check is looking for: a tree read successfully, argv
	// inspected, and the flag not found anywhere live in it.
	unwired := sysstat.ProcStat{OK: true, Procs: 2, ArgvMarkOK: true, ArgvMark: false}

	if !hooklessTree(store.Session{}, hooks.StatusSourceClaude, true, unwired) {
		t.Fatalf("the one case the check exists for did not fire: %+v", unwired)
	}

	cases := []struct {
		name         string
		sess         store.Session
		statusSource string
		agentAlive   bool
		stat         sysstat.ProcStat
		why          string
	}{
		{
			name: "a tool that does not use hooks", statusSource: "pane", agentAlive: true, stat: unwired,
			why: "there is no hook wiring to lose, so there is nothing to report",
		},
		{
			name: "a pane with no agent in it yet", statusSource: hooks.StatusSourceClaude,
			agentAlive: false, stat: sysstat.ProcStat{OK: true, Procs: 1, ArgvMarkOK: true},
			why: "every session between launch and the agent's first exec is here, and a flag cannot be on a command line that does not exist",
		},
		{
			name: "a tree the sampler could not read", statusSource: hooks.StatusSourceClaude,
			agentAlive: true, stat: sysstat.ProcStat{OK: false, ArgvMarkOK: true},
			why: "a failed sample is not evidence about argv",
		},
		{
			name: "the seeding pass", statusSource: hooks.StatusSourceClaude, agentAlive: true,
			stat: sysstat.ProcStat{OK: true, Procs: 2, ArgvMarkOK: false},
			why:  "the first pass is the ps scan and reads no argv; so is every pass on a kernel without /proc child lists",
		},
		{
			name: "a wired session", statusSource: hooks.StatusSourceClaude, agentAlive: true,
			stat: sysstat.ProcStat{OK: true, Procs: 2, ArgvMarkOK: true, ArgvMark: true},
			why:  "the flag is there",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if hooklessTree(c.sess, c.statusSource, c.agentAlive, c.stat) {
				t.Errorf("called hookless, want left alone: %s", c.why)
			}
		})
	}
}

// The map handed to the UI is a copy. The poller rewrites its own every pass
// while the model reads it from another goroutine to paint.
func TestHooklessRowsAreCopiedForTheUI(t *testing.T) {
	m := buildModel(t)
	m.poller.hookless["a"] = true
	rows := m.poller.hooklessRows()
	if !rows["a"] {
		t.Fatalf("the copy did not carry the finding")
	}
	delete(m.poller.hookless, "a")
	if !rows["a"] {
		t.Errorf("the UI's map changed when the poller's did; it is the same map")
	}
}

// The first pass, pinned at the scale it would have failed on.
//
// The board's first sample is the ps-based full scan, which reads no argv at
// all, and every pass is that scan on a kernel without /proc child lists.
// Reading ArgvMark without ArgvMarkOK there does not mark one row: it marks
// the whole board at once, on the pass an operator is most likely to be
// watching, and every row would be wrong. So this walks the shapes a real
// board is made of rather than asserting the single struct case.
func TestTheFirstPassMarksNothingOnAWholeBoard(t *testing.T) {
	// What a seeding pass hands back: the tree resolved, argv never looked at.
	seeding := sysstat.ProcStat{OK: true, Procs: 2, ArgvMarkOK: false, ArgvMark: false}

	board := []store.Session{
		{ID: "managed-claude", Tool: "claude"},
		{ID: "another-claude", Tool: "claude"},
		{ID: "an-adopted-pane", Tool: "claude", TmuxSocket: "default", TmuxPaneID: "%7"},
		{ID: "a-codex", Tool: "codex"},
	}
	sources := map[string]string{"claude": hooks.StatusSourceClaude, "codex": "pane"}

	var marked []string
	for _, sess := range board {
		if hooklessTree(sess, sources[sess.Tool], true, seeding) {
			marked = append(marked, sess.ID)
		}
	}
	if len(marked) != 0 {
		t.Errorf("the seeding pass marked %v; it reads no argv, so it has nothing to report about any of them", marked)
	}

	// And the pass after it does the work, so the guard is not simply off:
	// the two managed claude rows are the ones a real finding reaches.
	steady := sysstat.ProcStat{OK: true, Procs: 2, ArgvMarkOK: true, ArgvMark: false}
	marked = nil
	for _, sess := range board {
		if hooklessTree(sess, sources[sess.Tool], true, steady) {
			marked = append(marked, sess.ID)
		}
	}
	if len(marked) != 2 || marked[0] != "managed-claude" || marked[1] != "another-claude" {
		t.Errorf("steady-state pass marked %v, want the two managed claude rows only", marked)
	}
}
