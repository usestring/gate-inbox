package sessioncmd

import (
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// A replacement sits where the old session sat, inherits what was queued
// for it and what it held, and the old one is left dead with its screen.
func TestBoardReplaceTakesTheOldSessionsPlace(t *testing.T) {
	h := newSessionHarness(t)
	watcher := &launchWatcher{}
	useWatcher(t, watcher)
	old := childShowing(t, h, h.caller.ID, "child201", "worker", "lost the thread\n")
	if _, err := h.sessions.BoardSend("ext1", old.ID, "a report for whoever holds the seat", "", false); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := h.store.Reserve([]store.Reservation{{
		ID: "lease201", SessionID: old.ID, Pattern: "internal/*",
		Mode: store.ReservationExclusive, AcquiredAt: now, ExpiresAt: now.Add(time.Hour),
	}}); err != nil {
		t.Fatal(err)
	}

	fresh, err := h.sessions.BoardReplace(old.ID, "ext1/", BoardLaunchOptions{Prompt: "start over"})
	if err != nil {
		t.Fatalf("BoardReplace: %v", err)
	}
	if fresh.ID == old.ID || fresh.Name != "worker" || fresh.Tool != "echoer" || fresh.ParentID != h.caller.ID || fresh.Group != old.Group {
		t.Fatalf("fresh = %+v, want the old seat under a new id", fresh)
	}
	if h.driver.Exists(old.ID) {
		t.Fatal("the old pane outlived its replacement")
	}
	stored, err := h.store.Get(old.ID)
	if err != nil || stored.Status != status.Dead {
		t.Fatalf("old row = %+v, %v; want it kept, dead", stored, err)
	}
	if snap, _ := h.store.Snapshot(old.ID); !strings.Contains(snap, "lost the thread") {
		t.Fatalf("snapshot = %q, want the old pane's last screen", snap)
	}
	if counts, _ := h.store.QueuedCounts(); counts[fresh.ID] != 1 || counts[old.ID] != 0 {
		t.Fatalf("queued = %v, want the report forwarded", counts)
	}
	if leases, _ := h.store.Reservations(time.Now()); len(leases) != 1 || leases[0].SessionID != fresh.ID {
		t.Fatalf("leases = %+v, want the lease moved", leases)
	}
	if len(watcher.asked) != 1 || watcher.asked[0].By != extension.SpawnByExtension {
		t.Fatalf("asked = %+v, want the spawn policy asked once", watcher.asked)
	}
	if launch := watcher.launches[0]; launch.Reason != extension.LaunchReplace || launch.From != old.ID || launch.Session.ID != fresh.ID {
		t.Fatalf("launch = %+v, want a replacement from the old session", launch)
	}
	waitForSessionOutput(t, h.sessions, h.caller.ID, fresh.ID, "start over")
}

// A refused replacement leaves the old session running and nothing filed.
func TestBoardReplaceRefusalsLeaveTheOldSessionAlone(t *testing.T) {
	h := newSessionHarness(t)
	watcher := &launchWatcher{refuse: "over-budget"}
	useWatcher(t, watcher)
	old := childShowing(t, h, "", "child202", "worker", "working\n")
	if err := h.store.CreateSession(store.Session{ID: "theirs01", Name: "theirs", Tool: "echoer", Cwd: old.Cwd, Role: "other/helper"}); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func() error{
		"cannot be filed elsewhere": func() error {
			_, err := h.sessions.BoardReplace(old.ID, "ext1/", BoardLaunchOptions{ParentID: h.caller.ID})
			return err
		},
		"another extension's role": func() error {
			_, err := h.sessions.BoardReplace("theirs01", "ext1/", BoardLaunchOptions{})
			return err
		},
		"over the spawn budget": func() error {
			_, err := h.sessions.BoardReplace(old.ID, "ext1/", BoardLaunchOptions{Name: "over-budget"})
			return err
		},
		"is not configured": func() error {
			_, err := h.sessions.BoardReplace(old.ID, "ext1/", BoardLaunchOptions{Tool: "nonesuch"})
			return err
		},
	}
	for want, replace := range cases {
		if err := replace(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want %q", err, want)
		}
	}
	if !h.driver.Exists(old.ID) {
		t.Fatal("a refused replacement ended the old pane")
	}
	listed, err := h.store.ListSessions(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 3 {
		t.Fatalf("listed %d rows, want only the caller, the old session and the other extension's", len(listed))
	}
}

// A plan of a replacement asks what the replacement would ask and files,
// starts, moves and takes nothing, and refuses what the replacement would.
func TestBoardPlanReplaceChangesNothing(t *testing.T) {
	h := newSessionHarness(t)
	watcher := &launchWatcher{env: map[string]string{"PLANNED": "yes"}}
	useWatcher(t, watcher)
	old := childShowing(t, h, h.caller.ID, "child203", "worker", "working\n")
	if _, err := h.sessions.BoardSend("ext1", old.ID, "still queued", "", false); err != nil {
		t.Fatal(err)
	}
	if err := h.store.CreateSession(store.Session{ID: "theirs02", Name: "theirs", Tool: "echoer", Cwd: old.Cwd, Role: "other/helper"}); err != nil {
		t.Fatal(err)
	}
	before, err := h.store.ListSessions(true)
	if err != nil {
		t.Fatal(err)
	}

	plan, err := h.sessions.BoardPlanReplace(old.ID, "ext1/", BoardLaunchOptions{Prompt: "start over", Args: []string{"--one word"}})
	if err != nil {
		t.Fatalf("BoardPlanReplace: %v", err)
	}
	if plan.SessionID == "" || plan.SessionID == old.ID || plan.Env["GATE_INBOX_SESSION_ID"] != plan.SessionID || plan.Env["PLANNED"] != "yes" {
		t.Fatalf("plan = %+v, want a new id carried in its environment with the contributor's", plan)
	}
	if !strings.Contains(plan.Command, "start over") || !strings.Contains(plan.Command, "'--one word'") {
		t.Fatalf("plan command = %q, want the prompt and the quoted argument", plan.Command)
	}
	if launch := watcher.launches[len(watcher.launches)-1]; launch.Reason != extension.LaunchReplace || launch.From != old.ID {
		t.Fatalf("launch = %+v, want the contributor told of a replacement", launch)
	}
	after, err := h.store.ListSessions(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) || h.driver.Exists(plan.SessionID) || !h.driver.Exists(old.ID) {
		t.Fatalf("rows %d -> %d, planned pane %v, old pane %v: want nothing started or ended",
			len(before), len(after), h.driver.Exists(plan.SessionID), h.driver.Exists(old.ID))
	}
	if counts, _ := h.store.QueuedCounts(); counts[old.ID] != 1 {
		t.Fatalf("queued = %v, want the old session's message left where it was", counts)
	}
	if borrower, _ := h.store.Setting("account_borrower:" + plan.SessionID); borrower != "" {
		t.Fatalf("a plan recorded a borrower %q", borrower)
	}
	if len(watcher.spawned) != 0 {
		t.Fatalf("spawned = %+v, want no spawn reported for a plan", watcher.spawned)
	}
	if _, err := h.sessions.BoardPlanReplace("theirs02", "ext1/", BoardLaunchOptions{}); err == nil || !strings.Contains(err.Error(), "another extension's role") {
		t.Fatalf("plan of another extension's session: %v", err)
	}
}
