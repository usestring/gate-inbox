package store

import (
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
)

func kid(id, parent, state string) Session {
	return Session{ID: id, ParentID: parent, SpawnedBy: parent, Tool: "claude", Status: state}
}

func statusOf(sessions []Session, id string) string {
	for _, sess := range sessions {
		if sess.ID == id {
			return sess.Status
		}
	}
	return ""
}

func TestApplyDelegationEntersWhileAChildWorks(t *testing.T) {
	for _, rest := range []string{status.Finished, status.Idle} {
		rows := ApplyDelegation([]Session{
			{ID: "p", Status: rest},
			kid("c", "p", status.Working),
		}, nil, nil)
		if got := statusOf(rows, "p"); got != status.Delegating {
			t.Fatalf("%s parent with a working child = %q, want delegating", rest, got)
		}
		if got := statusOf(rows, "c"); got != status.Working {
			t.Fatalf("child = %q, want its own status untouched", got)
		}
	}
}

func TestApplyDelegationLeavesOnceTheLastChildFinishes(t *testing.T) {
	rows := ApplyDelegation([]Session{
		{ID: "p", Status: status.Finished},
		kid("a", "p", status.Finished),
		kid("b", "p", status.Dead),
	}, nil, nil)
	if got := statusOf(rows, "p"); got != status.Finished {
		t.Fatalf("parent with every child done = %q, want finished", got)
	}
}

func TestApplyDelegationHoldsUntilTheRestNoticeLands(t *testing.T) {
	rows := ApplyDelegation([]Session{
		{ID: "p", Status: status.Finished},
		kid("a", "p", status.Finished),
	}, map[string]bool{"p": true}, nil)
	if got := statusOf(rows, "p"); got != status.Delegating {
		t.Fatalf("parent with a report still queued = %q, want delegating", got)
	}
}

func TestApplyDelegationLeavesAskingAndErroredParentsAlone(t *testing.T) {
	for _, own := range []string{status.Waiting, status.Errored, status.Working} {
		rows := ApplyDelegation([]Session{
			{ID: "p", Status: own},
			kid("c", "p", status.Working),
		}, nil, nil)
		if got := statusOf(rows, "p"); got != own {
			t.Fatalf("%s parent = %q, want it left as its pane reads", own, got)
		}
	}
}

func TestApplyDelegationSkipsArchivedShellsAndDetached(t *testing.T) {
	archived := kid("arch", "p", status.Working)
	archived.Archived = true
	shell := kid("term", "p", status.Working)
	shell.Tool = "shell"
	detached := Session{ID: "det", SpawnedBy: "p", Status: status.Working}
	rows := ApplyDelegation([]Session{
		{ID: "p", Status: status.Finished}, archived, shell, detached,
	}, nil, func(sess Session) bool { return sess.Tool == "shell" })
	if got := statusOf(rows, "p"); got != status.Finished {
		t.Fatalf("parent held only by archived, shell and detached rows = %q, want finished", got)
	}
}

func TestApplyDelegationCarriesUpTheTree(t *testing.T) {
	// The middle row is finished on its own pane and still owes the root
	// its result, because its own child is working.
	rows := ApplyDelegation([]Session{
		{ID: "root", Status: status.Idle},
		kid("mid", "root", status.Finished),
		{ID: "leaf", ParentID: "root", SpawnedBy: "mid", Tool: "claude", Status: status.Working},
	}, nil, nil)
	if got := statusOf(rows, "mid"); got != status.Delegating {
		t.Fatalf("mid = %q, want delegating", got)
	}
	if got := statusOf(rows, "root"); got != status.Delegating {
		t.Fatalf("root = %q, want delegating through its delegating child", got)
	}
}

func TestPendingChildReportsIsUndeliveredRestNoticesOnly(t *testing.T) {
	st := withParent(t)
	if err := st.CreateSession(sample("p2", "")); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	childOf(t, st, "p1", "queued", "finished", time.Minute)
	restNotice(t, st, "p1", "queued", now, time.Time{})
	childOf(t, st, "p2", "landed", "finished", time.Minute)
	restNotice(t, st, "p2", "landed", now, now)
	pending, err := st.PendingChildReports()
	if err != nil {
		t.Fatal(err)
	}
	if !pending["p1"] || pending["p2"] || len(pending) != 1 {
		t.Fatalf("pending = %v, want only p1", pending)
	}
}
