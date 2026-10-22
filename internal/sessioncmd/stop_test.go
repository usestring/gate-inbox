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
	if _, err := h.sessions.Stop(h.caller.ID, true, false); err == nil {
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
	if _, err := h.sessions.Stop(h.caller.ID, true, false); err == nil {
		t.Fatal("accepted pane environment without caller process ancestry")
	}
	t.Setenv("TMUX_PANE", "%9999999")
	if _, err := h.sessions.Stop(h.caller.ID, true, false); err == nil {
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
	runtime, err := h.sessions.open()
	if err != nil {
		t.Fatal(err)
	}
	pin, err := runtime.paneProcess(row)
	runtime.store.Close()
	if err != nil {
		t.Fatal(err)
	}
	launch := row.LaunchTime().Format(time.RFC3339Nano)
	for _, bad := range [][2]string{{"old-launch", pane}, {launch, "%9999999"}, {launch, ""}} {
		if _, err := h.sessions.FinishStop(row.ID, bad[0], bad[1], pin, false); err == nil {
			t.Fatalf("accepted stale request: %v", bad)
		}
		if !h.driver.Exists(row.ID) {
			t.Fatal("stale request stopped session")
		}
	}
	if _, err := h.sessions.FinishStop(row.ID, launch, pane, "", false); err == nil {
		t.Fatal("accepted empty pane process pin")
	}
	if _, err := h.sessions.FinishStop(row.ID, launch, pane, "1:old-start", false); err == nil {
		t.Fatal("accepted foreign pane process pin")
	}
	stopped, err := h.sessions.FinishStop(row.ID, launch, pane, pin, false)
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

func TestFinishStopArchiveFilesTheEndedRow(t *testing.T) {
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
	runtime, err := h.sessions.open()
	if err != nil {
		t.Fatal(err)
	}
	pin, err := runtime.paneProcess(row)
	runtime.store.Close()
	if err != nil {
		t.Fatal(err)
	}
	launch := row.LaunchTime().Format(time.RFC3339Nano)
	if _, err := h.sessions.FinishStop(row.ID, launch, pane, "1:old-start", true); err == nil {
		t.Fatal("accepted foreign pane process pin")
	}
	if kept, err := h.store.Get(row.ID); err != nil || kept.Archived {
		t.Fatalf("refused request archived the row: %+v, %v", kept, err)
	}
	stopped, err := h.sessions.FinishStop(row.ID, launch, pane, pin, true)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Running || !stopped.Archived || h.driver.Exists(row.ID) {
		t.Fatalf("stopped: %+v", stopped)
	}
	filed, err := h.store.Get(row.ID)
	if err != nil || !filed.Archived {
		t.Fatalf("archived row: %+v, %v", filed, err)
	}
	ends, err := h.store.SessionEnds()
	if err != nil {
		t.Fatal(err)
	}
	if end := ends[row.ID]; end.Reason != store.EndKilled || !end.Matches(filed) {
		t.Fatalf("intentional end: %+v", end)
	}
}
