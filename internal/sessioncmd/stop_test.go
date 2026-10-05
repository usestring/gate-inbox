package sessioncmd

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/store"
)

func TestStopDryRunRequiresItsOwnLivePane(t *testing.T) {
	h := newSessionHarness(t)
	if _, err := h.sessions.Stop(h.caller.ID, true); err == nil {
		t.Fatal("accepted stop outside its own pane")
	}
	cmd := exec.Command("tmux", "-L", h.driver.SocketName(), "display-message", "-p", "-t", "gi_"+h.caller.ID, "#{socket_path},#{pid},#{session_id} #{pane_id}")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(out))
	t.Setenv("TMUX", fields[0])
	t.Setenv("TMUX_PANE", fields[1])
	result, err := h.sessions.Stop(h.caller.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.DryRun || !result.Target.Self || !h.driver.Exists(h.caller.ID) {
		t.Fatalf("dry run: %+v", result)
	}
	ends, err := h.store.SessionEnds()
	if err != nil || len(ends) != 0 {
		t.Fatalf("dry run wrote ends: %v, %v", ends, err)
	}
	t.Setenv("TMUX_PANE", "%9999999")
	if _, err := h.sessions.Stop(h.caller.ID, true); err == nil {
		t.Fatal("accepted stale pane")
	}
}

func TestFinishStopPreservesHistoryAndPinsTheLaunch(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	row, err := h.store.Get(h.caller.ID)
	if err != nil {
		t.Fatal(err)
	}
	pane, err := h.driver.PaneID(row.ID)
	if err != nil {
		t.Fatal(err)
	}
	launch := row.LaunchTime().Format(time.RFC3339Nano)
	for _, bad := range [][2]string{{"old-launch", pane}, {launch, "%9999999"}, {launch, ""}} {
		if _, err := h.sessions.FinishStop(row.ID, bad[0], bad[1]); err == nil {
			t.Fatalf("accepted stale request: %v", bad)
		}
		if !h.driver.Exists(row.ID) {
			t.Fatal("stale request stopped session")
		}
	}
	stopped, err := h.sessions.FinishStop(row.ID, launch, pane)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Running || h.driver.Exists(row.ID) {
		t.Fatal("session is still running")
	}
	kept, err := h.store.Get(row.ID)
	if err != nil || kept.Archived {
		t.Fatalf("history row: %+v, %v", kept, err)
	}
	ends, err := h.store.SessionEnds()
	if err != nil {
		t.Fatal(err)
	}
	if end := ends[row.ID]; end.Reason != store.EndKilled || !end.Matches(kept) {
		t.Fatalf("intentional end: %+v", end)
	}
}
