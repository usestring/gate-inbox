package sessioncmd

import (
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

func cleanupReasons(cleaned ChildCleanup) map[string]string {
	reasons := map[string]string{}
	for _, child := range cleaned.Children {
		reasons[child.SessionID] = child.Skipped
	}
	return reasons
}

func requireFiled(t *testing.T, h *sessionHarness, id string, filed bool) {
	t.Helper()
	row, err := h.store.Get(id)
	if err != nil {
		t.Fatalf("Get %s: %v", id, err)
	}
	if row.Archived != filed {
		t.Errorf("%s archived = %v, want %v", id, row.Archived, filed)
	}
	if filed && h.driver.Exists(id) {
		t.Errorf("%s was archived with its pane still up", id)
	}
	if !filed && row.Status != status.Dead && !h.driver.Exists(id) {
		t.Errorf("%s was left on the list with its pane gone", id)
	}
}

func TestCleanupChildrenFilesTheDoneChildrenAndSaysWhyTheRestStayed(t *testing.T) {
	h := newSessionHarness(t)
	kid := func(id, state string, pane bool) {
		childRow(t, h, store.Session{ID: id, Name: id, ParentID: h.caller.ID, SpawnedBy: h.caller.ID, Status: state}, pane)
	}
	kid("done-aa1", status.Finished, true)
	kid("idle-bb2", status.Idle, true)
	kid("dead-cc3", status.Dead, false)
	kid("busy-dd4", status.Working, true)
	kid("asks-ee5", status.Waiting, true)
	kid("errs-ff6", status.Errored, true)
	kid("kept-gg7", status.Finished, true)
	if err := h.store.SetKeepChild("kept-gg7", true); err != nil {
		t.Fatalf("SetKeepChild: %v", err)
	}
	// A finished child whose own spawn is still at work stays, and one whose
	// spawn is done takes it along. Grandchildren are filed under the root
	// and owned through spawned_by.
	kid("mids-hh8", status.Finished, true)
	childRow(t, h, store.Session{ID: "gkid-ii9", Name: "gkid-ii9", ParentID: h.caller.ID, SpawnedBy: "mids-hh8", Status: status.Working}, true)
	kid("tops-jj0", status.Finished, true)
	childRow(t, h, store.Session{ID: "gkid-kk1", Name: "gkid-kk1", ParentID: h.caller.ID, SpawnedBy: "tops-jj0", Status: status.Finished}, true)
	// Not this caller's.
	stranger := childRow(t, h, store.Session{ID: "other001", Name: "another-agent"}, false)
	childRow(t, h, store.Session{ID: "theirs01", Name: "their-child", ParentID: stranger.ID, SpawnedBy: stranger.ID, Status: status.Finished}, true)

	cleaned, err := h.sessions.CleanupChildren(h.caller.ID, CleanupOptions{})
	if err != nil {
		t.Fatalf("CleanupChildren: %v", err)
	}
	reasons := cleanupReasons(cleaned)
	for _, id := range []string{"done-aa1", "idle-bb2", "dead-cc3", "tops-jj0"} {
		if reasons[id] != "" {
			t.Errorf("%s skipped: %s", id, reasons[id])
		}
		requireFiled(t, h, id, true)
	}
	requireFiled(t, h, "gkid-kk1", true)
	for id, want := range map[string]string{
		"busy-dd4": "working",
		"asks-ee5": "waiting",
		"errs-ff6": "errored",
		"kept-gg7": "keep",
		"mids-hh8": "gkid-ii9 (working)",
	} {
		if !strings.Contains(reasons[id], want) {
			t.Errorf("%s reason = %q, want it to mention %q", id, reasons[id], want)
		}
		requireFiled(t, h, id, false)
	}
	requireFiled(t, h, "gkid-ii9", false)
	requireFiled(t, h, "theirs01", false)
	if _, listed := reasons["theirs01"]; listed {
		t.Error("another session's child was part of this caller's cleanup")
	}
	if _, listed := reasons["gkid-kk1"]; listed {
		t.Error("a grandchild was reported as the caller's own child")
	}
	if cleaned.Archived != 4 || cleaned.Skipped != 5 {
		t.Fatalf("archived=%d skipped=%d, want 4 and 5", cleaned.Archived, cleaned.Skipped)
	}
	text := FormatChildCleanup(cleaned)
	if !strings.HasPrefix(text, "archived 4 of 9 children") || !strings.Contains(text, "tops-jj0 (tops-jj0) with 1 under it") {
		t.Fatalf("formatted = %q", text)
	}
}

func TestCleanupChildrenAllTakesEveryChild(t *testing.T) {
	h := newSessionHarness(t)
	for id, state := range map[string]string{"busy-aa1": status.Working, "kept-bb2": status.Finished} {
		childRow(t, h, store.Session{ID: id, Name: id, ParentID: h.caller.ID, SpawnedBy: h.caller.ID, Status: state}, true)
	}
	if err := h.store.SetKeepChild("kept-bb2", true); err != nil {
		t.Fatalf("SetKeepChild: %v", err)
	}
	cleaned, err := h.sessions.CleanupChildren(h.caller.ID, CleanupOptions{All: true})
	if err != nil {
		t.Fatalf("CleanupChildren: %v", err)
	}
	if cleaned.Archived != 2 {
		t.Fatalf("all archived %d, want 2: %+v", cleaned.Archived, cleaned)
	}
	requireFiled(t, h, "busy-aa1", true)
	requireFiled(t, h, "kept-bb2", true)
}

func TestCleanupChildrenDryRunTouchesNothing(t *testing.T) {
	h := newSessionHarness(t)
	childRow(t, h, store.Session{ID: "done-aa1", Name: "done-aa1", ParentID: h.caller.ID, SpawnedBy: h.caller.ID, Status: status.Finished}, true)
	cleaned, err := h.sessions.CleanupChildren(h.caller.ID, CleanupOptions{DryRun: true})
	if err != nil {
		t.Fatalf("CleanupChildren: %v", err)
	}
	if !cleaned.DryRun || cleaned.Archived != 1 || !strings.HasPrefix(FormatChildCleanup(cleaned), "would archive 1 of 1") {
		t.Fatalf("dry run = %+v", cleaned)
	}
	requireFiled(t, h, "done-aa1", false)
}

func TestCleanupChildrenRefusesAnUnknownStateAndAnEmptyFanOut(t *testing.T) {
	h := newSessionHarness(t)
	if _, err := h.sessions.CleanupChildren(h.caller.ID, CleanupOptions{}); err == nil || !strings.Contains(err.Error(), "no children") {
		t.Fatalf("empty fan-out err = %v", err)
	}
	childRow(t, h, store.Session{ID: "done-aa1", Name: "done-aa1", ParentID: h.caller.ID, SpawnedBy: h.caller.ID, Status: status.Finished}, true)
	if _, err := h.sessions.CleanupChildren(h.caller.ID, CleanupOptions{Statuses: []string{"done"}}); err == nil || !strings.Contains(err.Error(), "not a session state") {
		t.Fatalf("unknown state err = %v", err)
	}
	requireFiled(t, h, "done-aa1", false)
}

// Archiving a parent takes the fan-out under it, at any depth, so nothing is
// left running under a row nobody is watching.
func TestArchiveTakesTheTargetsDescendants(t *testing.T) {
	h := newSessionHarness(t)
	childRow(t, h, store.Session{ID: "mids-aa1", Name: "mids-aa1", ParentID: h.caller.ID, SpawnedBy: h.caller.ID, Status: status.Finished}, true)
	childRow(t, h, store.Session{ID: "gkid-bb2", Name: "gkid-bb2", ParentID: h.caller.ID, SpawnedBy: "mids-aa1", Status: status.Working}, true)
	childRow(t, h, store.Session{ID: "sibl-cc3", Name: "sibl-cc3", ParentID: h.caller.ID, SpawnedBy: h.caller.ID, Status: status.Working}, true)
	if _, err := h.sessions.Archive(h.caller.ID, "mids-aa1", true); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	requireFiled(t, h, "mids-aa1", true)
	requireFiled(t, h, "gkid-bb2", true)
	requireFiled(t, h, "sibl-cc3", false)
}

// A spawner reading its finished child is the finish taken in; a stranger
// reading it is not, and neither is a read of a child still at work.
func TestReadBySpawnerMarksAFinishedChildAbsorbed(t *testing.T) {
	h := newSessionHarness(t)
	childRow(t, h, store.Session{ID: "done-aa1", Name: "done-aa1", ParentID: h.caller.ID, SpawnedBy: h.caller.ID, Status: status.Finished}, true)
	childRow(t, h, store.Session{ID: "busy-bb2", Name: "busy-bb2", ParentID: h.caller.ID, SpawnedBy: h.caller.ID, Status: status.Working}, true)
	stranger := childRow(t, h, store.Session{ID: "other001", Name: "another-agent", Status: status.Idle}, true)
	childRow(t, h, store.Session{ID: "their-cc", Name: "their-cc", ParentID: stranger.ID, SpawnedBy: stranger.ID, Status: status.Finished}, true)

	for _, id := range []string{"done-aa1", "busy-bb2", "their-cc"} {
		if _, err := h.sessions.Read(h.caller.ID, id, ""); err != nil {
			t.Fatalf("Read %s: %v", id, err)
		}
	}
	children, err := h.store.RetirableChildren(time.Now().Add(time.Second))
	if err != nil {
		t.Fatalf("RetirableChildren: %v", err)
	}
	if len(children) != 1 || children[0].ID != "done-aa1" || children[0].Via != store.AbsorbedByRead {
		t.Fatalf("retirable = %+v, want done-aa1 absorbed by the read", children)
	}
}

func TestWaitBySpawnerMarksAFinishedChildAbsorbed(t *testing.T) {
	h := newSessionHarness(t)
	childRow(t, h, store.Session{ID: "done-aa1", Name: "done-aa1", ParentID: h.caller.ID, SpawnedBy: h.caller.ID, Status: status.Finished}, true)
	if _, err := h.sessions.Wait(t.Context(), h.caller.ID, WaitOptions{Children: true, Timeout: time.Second}); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	children, err := h.store.RetirableChildren(time.Now().Add(time.Second))
	if err != nil {
		t.Fatalf("RetirableChildren: %v", err)
	}
	if len(children) != 1 || children[0].ID != "done-aa1" {
		t.Fatalf("retirable = %+v, want done-aa1", children)
	}
}

func TestCreateWithKeepMarksTheChild(t *testing.T) {
	h := newSessionHarness(t)
	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "keeper", Keep: true})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if keep, err := h.store.KeepChild(created.ID); err != nil || !keep {
		t.Fatalf("KeepChild = %v, %v; want the spawn kept", keep, err)
	}
	plain, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "plain"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if keep, err := h.store.KeepChild(plain.ID); err != nil || keep {
		t.Fatalf("KeepChild = %v, %v; want an ordinary spawn unmarked", keep, err)
	}
}

// A child archiving its own parent is not asking to end itself or its work.
func TestArchiveSparesACallerUnderTheTarget(t *testing.T) {
	h := newSessionHarness(t)
	root := childRow(t, h, store.Session{ID: "root-aa1", Name: "root-aa1", Status: status.Finished}, true)
	h.caller.ParentID, h.caller.SpawnedBy = root.ID, root.ID
	if err := h.store.PlaceSession(h.caller.ID, h.caller.Group, root.ID); err != nil {
		t.Fatalf("PlaceSession: %v", err)
	}
	childRow(t, h, store.Session{ID: "mine-bb2", Name: "mine-bb2", ParentID: root.ID, SpawnedBy: h.caller.ID, Status: status.Working}, true)
	childRow(t, h, store.Session{ID: "sibl-cc3", Name: "sibl-cc3", ParentID: root.ID, SpawnedBy: root.ID, Status: status.Finished}, true)
	if _, err := h.sessions.Archive(h.caller.ID, root.ID, true); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	requireFiled(t, h, root.ID, true)
	requireFiled(t, h, "sibl-cc3", true)
	requireFiled(t, h, "mine-bb2", false)
	requireFiled(t, h, h.caller.ID, false)
}
