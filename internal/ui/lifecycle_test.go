// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/uuid"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

func TestCreateArchiveRestoreDelete(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()

	createSession(t, m, "alpha", dir, "")
	if len(m.sessionRows()) != 1 {
		t.Fatalf("after create, sessions = %d want 1 (err=%q)", len(m.sessionRows()), m.errBar.text)
	}
	sess := m.sessionRows()[0]
	if !m.tmux.Exists(sess.ID) {
		t.Fatal("tmux session should exist after create")
	}
	if sess.Name != "alpha" || sess.Tool != "claude" || sess.Group != "" {
		t.Fatalf("session fields wrong: %+v", sess)
	}

	m.selectSessionRow(t, "alpha")
	m.archiveSelected()
	_, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)
	if len(m.sessionRows()) != 0 {
		t.Fatalf("after archive, active sessions = %d want 0", len(m.sessionRows()))
	}
	if m.tmux.Exists(sess.ID) {
		t.Fatal("archive should kill the tmux session")
	}

	m.showArchived = true
	m.applyCmd(t, m.refreshCmd())
	if len(m.sessionRows()) != 1 || !m.sessionRows()[0].Archived {
		t.Fatalf("archived session should show in archived view")
	}

	m.selectSessionRow(t, "alpha")
	m.restoreSelected()
	_, cmd = m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	got, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != status.Starting {
		t.Fatalf("after restore, status = %q want %q", got.Status, status.Starting)
	}
	if got.LaunchTime().Equal(got.CreatedAt) {
		t.Fatal("restore should stamp a new launch time")
	}
	m.applyCmd(t, cmd)
	m.showArchived = false
	m.applyCmd(t, m.refreshCmd())
	if len(m.sessionRows()) != 1 {
		t.Fatalf("after restore, active sessions = %d want 1", len(m.sessionRows()))
	}
	if !m.tmux.Exists(sess.ID) {
		t.Fatal("restore should revive the tmux session")
	}

	m.selectSessionRow(t, "alpha")
	m.archiveSelected()
	if m.mode != modeConfirmDelete {
		t.Fatal("archiveSelected should enter confirm mode")
	}
	_, cmd = m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if m.tmux.Exists(sess.ID) {
		t.Fatal("tmux session should be killed after archiving")
	}
	m.applyCmd(t, cmd)
	if len(m.sessionRows()) != 0 {
		t.Fatalf("after archiving, sessions = %d want 0", len(m.sessionRows()))
	}
	m.applyCmd(t, expireArchives(m))
	if _, err := m.store.Get(sess.ID); err == nil {
		t.Fatal("the row should be gone once its window ran out")
	}
}

// Archiving a group takes its subtree with it, and the retention sweep then
// clears the rows and the group shells they leave behind.
func TestArchivedGroupSubtreeExpires(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()

	if err := m.store.CreateGroup("zone/inner", ""); err != nil {
		t.Fatalf("create group: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())
	createSession(t, m, "in-zone", dir, "zone")
	createSession(t, m, "in-inner", dir, "zone/inner")
	createSession(t, m, "outside", dir, "")

	archivedID := m.sessionRows()[0].ID
	for _, s := range m.sessionRows() {
		if s.Name == "in-inner" {
			archivedID = s.ID
		}
	}
	if err := m.store.SetArchived(archivedID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())

	for i, r := range m.rows {
		if r.isGroup && r.group == "zone" {
			m.cursor = i
		}
	}
	m.archiveSelected()
	if !m.confirm.isGroup || len(m.confirm.sessions) != 1 {
		t.Fatalf("confirm should target the group's live session, got %+v", m.confirm)
	}
	tmuxIDs := []string{archivedID}
	for _, s := range m.confirm.sessions {
		tmuxIDs = append(tmuxIDs, s.ID)
	}
	_, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)
	m.applyCmd(t, expireArchives(m))

	for _, id := range tmuxIDs {
		if m.tmux.Exists(id) {
			t.Fatalf("tmux session %s should be killed", id)
		}
	}
	m.applyCmd(t, m.refreshCmd())
	sessions := m.sessionRows()
	if len(sessions) != 1 || sessions[0].Name != "outside" {
		t.Fatalf("only outside should remain, got %v", sessions)
	}
	all, _ := m.store.ListSessions(true)
	if len(all) != 1 {
		t.Fatalf("archived subtree session should be gone from db, got %d rows", len(all))
	}
	groups, _ := m.store.Groups()
	for _, g := range groups {
		if g.Name == "zone" || g.Name == "zone/inner" {
			t.Fatalf("group %s should be deleted", g.Name)
		}
	}
}

// The sweep takes only what is archived and past its window; a live session
// in the same group, and the group itself, stay.
func TestArchiveSweepSparesLiveSessions(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()

	if err := m.store.CreateGroup("bugs", ""); err != nil {
		t.Fatalf("create group: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())
	createSession(t, m, "old", dir, "bugs")
	createSession(t, m, "live", dir, "bugs")

	m.selectSessionRow(t, "old")
	m.archiveSelected()
	_, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)

	m.showArchived = true
	m.applyCmd(t, m.refreshCmd())
	archived := m.sessionRows()
	if len(archived) != 1 || archived[0].Name != "old" {
		t.Fatalf("the archived view should hold only old, got %+v", archived)
	}
	archivedID := archived[0].ID
	m.applyCmd(t, expireArchives(m))

	m.showArchived = false
	m.applyCmd(t, m.refreshCmd())
	if names := sessionNames(m); len(names) != 1 || names[0] != "live" {
		t.Fatalf("active view sessions = %v want [live]", names)
	}
	if paths := m.groupRowPaths(); len(paths) != 1 || paths[0] != "bugs" {
		t.Fatalf("group holding a live session should survive, got %v", paths)
	}
	for _, sess := range m.sessionRows() {
		if !m.tmux.Exists(sess.ID) {
			t.Fatalf("live session %s lost its tmux window", sess.Name)
		}
	}
	if m.tmux.Exists(archivedID) {
		t.Fatalf("archived session %s should be killed", archivedID)
	}
}

// An archived group with nothing under it has nothing to wait for, so the
// first sweep clears it.
func TestArchiveSweepRemovesAnEmptyArchivedGroup(t *testing.T) {
	m := buildModel(t)
	if err := m.store.CreateGroup("empty", ""); err != nil {
		t.Fatalf("create group: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())
	m.selectGroupRow(t, "empty")
	m.archiveSelected()
	_, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)

	m.showArchived = true
	m.applyCmd(t, m.refreshCmd())
	m.applyCmd(t, expireArchives(m))
	m.applyCmd(t, m.refreshCmd())

	if paths := m.groupRowPaths(); len(paths) != 0 {
		t.Fatalf("archived empty group should be gone, got %v", paths)
	}
}

func TestIgnoreDeletedSessionDropsOnlyTheDeleteRace(t *testing.T) {
	if err := ignoreDeletedSession(fmt.Errorf("abc: %w", store.ErrSessionGone)); err != nil {
		t.Fatalf("a session deleted mid-poll should not fail the pass: %v", err)
	}
	if err := ignoreDeletedSession(errors.New("database is locked")); err == nil {
		t.Fatal("a real store failure must still surface")
	}
}

// A tmux server that outlived the update can still carry the old C-r
// binding, so its marker has to be consumed and ignored rather than
// reopening a screen that no longer exists.
func TestAttachDoneDropsAStaleReviewMarker(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "staleflag", t.TempDir(), "")
	m.selectSessionRow(t, "staleflag")
	sess := m.sessionRows()[0]
	clearRequestOnCleanup(t, m)

	if _, err := tmuxCmd("set-option", "-g", "@gi_request", "review").CombinedOutput(); err != nil {
		t.Fatalf("set marker: %v", err)
	}
	updated, _ := m.Update(attachDoneMsg{sessID: sess.ID})
	*m = *updated.(*Model)
	if m.mode != modeList {
		t.Fatalf("a stale review marker should leave the list alone, mode = %v, err = %q", m.mode, m.errBar.text)
	}

	request, err := m.tmux.PendingRequest()
	if err != nil {
		t.Fatalf("PendingRequest: %v", err)
	}
	if request != "" {
		t.Fatalf("the stale marker should have been consumed, got %q", request)
	}
}

func TestAttachDoneStaysInListWithoutMarker(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "plainexit", t.TempDir(), "")
	m.selectSessionRow(t, "plainexit")
	if err := m.tmux.ClearRequest(); err != nil {
		t.Fatalf("clear marker: %v", err)
	}

	updated, _ := m.Update(attachDoneMsg{})
	*m = *updated.(*Model)
	if m.mode != modeList {
		t.Fatalf("no marker should stay in list, mode = %v", m.mode)
	}
}

func TestAttachAcknowledgesFinished(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "alert-me", t.TempDir(), "")

	sess := m.sessionRows()[0]
	if err := m.store.UpdateStatus(sess.ID, status.Finished); err != nil {
		t.Fatalf("set finished: %v", err)
	}
	m.sessions[0].Status = status.Finished
	m.rebuildRows()
	m.selectSessionRow(t, "alert-me")

	if _, cmd := m.attachSelected(); cmd == nil {
		t.Fatalf("attach did not start, err = %q", m.errBar.text)
	}
	got, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != status.Idle {
		t.Fatalf("after attach, status = %q want %q", got.Status, status.Idle)
	}
	if !got.Acked {
		t.Fatal("attach should mark the session acked")
	}
}

func TestDotAcknowledgesOnlyCurrentFinishedStatus(t *testing.T) {
	cases := []struct {
		name       string
		listed     string
		stored     string
		wantStatus string
		wantAcked  bool
		wantOffer  bool
	}{
		{name: "finished", listed: status.Finished, stored: status.Finished, wantStatus: status.Idle, wantAcked: true, wantOffer: true},
		{name: "stale snapshot", listed: status.Finished, stored: status.Working, wantStatus: status.Working, wantOffer: true},
		{name: "working", listed: status.Working, stored: status.Working, wantStatus: status.Working},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := buildModel(t)
			createSession(t, m, "alert-me", t.TempDir(), "")
			sess := m.sessionRows()[0]
			if err := m.store.UpdateStatus(sess.ID, tc.stored); err != nil {
				t.Fatalf("set stored status: %v", err)
			}
			m.sessions[0].Status = tc.listed
			m.rebuildRows()
			m.selectSessionRow(t, "alert-me")
			if offered := strings.Contains(m.viewFooter(), "mark idle"); offered != tc.wantOffer {
				t.Fatalf("dot footer offered = %v, want %v", offered, tc.wantOffer)
			}

			m.handleKey(tea.KeyPressMsg{Code: '.', Text: "."})
			got, err := m.store.Get(sess.ID)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if got.Status != tc.wantStatus || got.Acked != tc.wantAcked {
				t.Fatalf("after dot, status = %q acked = %v", got.Status, got.Acked)
			}
			if m.mode != modeList {
				t.Fatalf("dot left list mode: %v", m.mode)
			}
		})
	}
}

func TestDotKeepsArchivedFinishedStatus(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "kept", t.TempDir(), "")
	sess := m.sessionRows()[0]
	if err := m.store.UpdateStatus(sess.ID, status.Finished); err != nil {
		t.Fatalf("set finished: %v", err)
	}
	if err := m.store.SetArchived(sess.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	m.sessions[0].Status = status.Finished
	m.sessions[0].Archived = true
	m.showArchived = true
	m.rebuildRows()
	m.selectSessionRow(t, "kept")
	if strings.Contains(m.viewFooter(), "mark idle") {
		t.Fatal("dot offered on an archived session")
	}

	m.handleKey(tea.KeyPressMsg{Code: '.', Text: "."})
	got, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != status.Finished || got.Acked {
		t.Fatalf("archived session changed: status = %q acked = %v", got.Status, got.Acked)
	}
}

func TestAttachKeepsWorking(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "busy-one", t.TempDir(), "")

	sess := m.sessionRows()[0]
	if err := m.store.UpdateStatus(sess.ID, status.Working); err != nil {
		t.Fatalf("set working: %v", err)
	}
	m.sessions[0].Status = status.Working
	m.rebuildRows()
	m.selectSessionRow(t, "busy-one")

	if _, cmd := m.attachSelected(); cmd == nil {
		t.Fatalf("attach did not start, err = %q", m.errBar.text)
	}
	got, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != status.Working {
		t.Fatalf("after attach, status = %q want %q", got.Status, status.Working)
	}
}

// PrepareAttach flips window-size to auto, which reflows the pane the same
// way the detach-side resize does; without clearing the cached hash first,
// the next poll compares the reflowed pane against a pre-attach hash and
// reads it as working (TestRebaselineKeepsFinishedWithoutFlashingWorking
// proves that precondition). Attach must clear it the same way detach does.
func TestAttachClearsStaleHashBeforeReflow(t *testing.T) {
	m := buildModel(t)
	m.openForm()
	m.form.name.SetValue("attach-reflow")
	m.form.dir.SetValue(t.TempDir())
	m.form.toolIndex = 1 // claude-hooked: configured with an activity region to hash
	pickGroup(t, m, "")
	_, cmd := m.submitForm()
	// A create lands inside the session it made; this test is about the board,
	// so it steps back out.
	if m.mode != modeFocus {
		t.Fatalf("after submit, mode = %v, err = %q", m.mode, m.errBar.text)
	}
	m.applyCmd(t, cmd)
	m.leaveFocusForFixture(t)

	sess := m.sessionRows()[0]
	if sess.Tool != "claude-hooked" {
		t.Fatalf("session tool = %q, want claude-hooked", sess.Tool)
	}
	if err := m.store.UpdateStatus(sess.ID, status.Finished); err != nil {
		t.Fatalf("set finished: %v", err)
	}
	sess.Status = status.Finished
	m.sessions[0].Status = status.Finished
	m.rebuildRows()
	m.selectSessionRow(t, "attach-reflow")

	before := "final answer line that wraps differently after attach\n❯ \n"
	after := "final answer line that wraps\ndifferently after attach\n❯ \n"
	seedRegionHash(t, m, sess, before)
	// Without clearing, the widened pane looks like streaming work.
	if got := deriveStatus(t, m, sess, after, true); got != status.Working {
		t.Fatalf("reflow with a prior hash should look like working (precondition), got %q", got)
	}

	if _, cmd := m.attachSelected(); cmd == nil {
		t.Fatalf("attach did not start, err = %q", m.errBar.text)
	}

	entered, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got := deriveStatus(t, m, entered, after, true); got != status.Idle {
		t.Fatalf("attach must rebaseline the pane hash instead of flashing working, got %q", got)
	}
}

func TestReviveRecreatesDeadSession(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "phoenix", t.TempDir(), "")

	sess := m.sessionRows()[0]
	if err := m.store.SetAgentSessionID(sess.ID, "kept-conversation"); err != nil {
		t.Fatal(err)
	}
	m.applyCmd(t, m.refreshCmd())
	m.selectSessionRow(t, "phoenix")
	sess = m.sessionRows()[0]
	if sess.AgentSessionID != "kept-conversation" {
		t.Fatalf("loaded session id = %q, want kept-conversation", sess.AgentSessionID)
	}
	previousLaunchTime := sess.LaunchTime()

	argsFile := filepath.Join(t.TempDir(), "launch-args")
	tool := m.cfg.Tools[sess.Tool]
	tool.ResumeByIDCommand = argCaptureCommand(argsFile) + " --resume {id}"
	m.cfg.Tools[sess.Tool] = tool

	if err := m.tmux.Kill(sess.ID); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if m.tmux.Exists(sess.ID) {
		t.Fatal("session should be dead before revive")
	}
	m.selectSessionRow(t, "phoenix")

	if err := m.store.SetAcked(sess.ID, true); err != nil {
		t.Fatalf("set acked: %v", err)
	}
	m.preview = "old pane from last life\n"

	if _, _ = m.reviveSelected(); m.errBar.text != "" {
		t.Fatalf("revive: %q", m.errBar.text)
	}
	if !m.tmux.Exists(sess.ID) {
		t.Fatal("revive should recreate the tmux session")
	}
	got, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != status.Starting {
		t.Fatalf("after revive, status = %q want %q", got.Status, status.Starting)
	}
	if got.Acked {
		t.Fatal("revive should clear a leftover ack")
	}
	if got.AgentSessionID != "kept-conversation" || got.RetiredAgentSessionID != "" {
		t.Fatalf("conversation = %q retired = %q, want kept-conversation", got.AgentSessionID, got.RetiredAgentSessionID)
	}
	if got.AgentLaunchedAt.IsZero() || !got.LaunchTime().Equal(got.AgentLaunchedAt) {
		t.Fatalf("launch time = %v, created = %v", got.LaunchTime(), got.CreatedAt)
	}
	if !got.AgentLaunchedAt.After(previousLaunchTime) {
		t.Fatalf("launch time = %v, want after %v", got.AgentLaunchedAt, previousLaunchTime)
	}
	row := m.sessionRows()[0]
	if row.Status != status.Starting {
		t.Fatalf("row status = %q want %q", row.Status, status.Starting)
	}
	gotPreview := previewText(m)
	if !strings.Contains(gotPreview, "No user-facing messages yet") {
		t.Fatalf("revived conversation should wait for its transcript, got %q", gotPreview)
	}
	if strings.Contains(gotPreview, "old pane from last life") {
		t.Fatalf("stale pane should not survive revive, got %q", gotPreview)
	}
	args := readWhenWritten(t, argsFile)
	if !strings.Contains(args, "--resume") || !strings.Contains(args, "kept-conversation") {
		t.Fatalf("revive launch arguments = %q, want --resume kept-conversation", args)
	}
}

func TestReviveKillsNewPaneWhenLaunchTimeCannotPersist(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "phoenix", t.TempDir(), "")

	sess := m.sessionRows()[0]
	if err := m.tmux.Kill(sess.ID); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if err := m.store.Delete(sess.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	err := m.reviveSession(sess)
	if !errors.Is(err, store.ErrSessionGone) {
		t.Fatalf("revive error = %v, want ErrSessionGone", err)
	}
	if m.tmux.Exists(sess.ID) {
		t.Fatal("failed revive must kill the newly created tmux session")
	}
}

func TestRevivedLaunchTimeSitsInsideStartingGrace(t *testing.T) {
	created := time.Now().Add(-5 * 24 * time.Hour)
	revived := store.Session{CreatedAt: created, AgentLaunchedAt: time.Now()}
	if time.Since(revived.LaunchTime()) >= startingGrace {
		t.Fatal("a revive that stamps launch time must still be inside the grace")
	}
	stale := store.Session{CreatedAt: created}
	if time.Since(stale.LaunchTime()) < startingGrace {
		t.Fatal("a 5-day-old row with no relaunch is outside the grace")
	}
}

// argCaptureCommand builds a launch command that records the arguments the
// manager appended to it and then holds the pane open, so a test can prove
// which flags a launch carried.
func argCaptureCommand(argsFile string) string {
	script := `printf '%s\n' "$@" > ` + tmux.ShellQuote(argsFile) + `; cat`
	return "sh -c " + tmux.ShellQuote(script) + " sh"
}

// readWhenWritten waits for content, not merely for the file: the launching
// shell truncates it before printf runs, so a read that lands between the two
// comes back empty and would fail the assertion it was fetched for.
func readWhenWritten(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(path); err == nil && len(raw) > 0 {
			return string(raw)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no launch arguments written to %s", path)
	return ""
}

// Restart is revive's opposite number: same row, same directory, same tool,
// but a conversation the agent has never seen. The old one is retired rather
// than resumed, so the resume flags revive would have used stay unused.
func TestRestartLaunchesAFreshConversation(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "phoenix", t.TempDir(), "")
	sess := m.sessionRows()[0]

	argsFile := filepath.Join(t.TempDir(), "launch-args")
	tool := m.cfg.Tools[sess.Tool]
	tool.Command = argCaptureCommand(argsFile)
	tool.SessionIDFlag = "--session-id"
	tool.ResumeByIDCommand = "false --resume {id}"
	tool.ReviveCommand = "false --continue"
	m.cfg.Tools[sess.Tool] = tool

	if err := m.store.SetAgentSessionID(sess.ID, "old-conversation"); err != nil {
		t.Fatal(err)
	}
	m.sessions[0].AgentSessionID = "old-conversation"
	if err := m.tmux.Kill(sess.ID); err != nil {
		t.Fatalf("kill: %v", err)
	}
	m.selectSessionRow(t, "phoenix")

	if _, _ = m.restartSelected(); m.mode != modeConfirmDelete {
		t.Fatalf("restart should ask first, mode = %v err = %q", m.mode, m.errBar.text)
	}
	_, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)
	if m.errBar.text != "" {
		t.Fatalf("restart: %q", m.errBar.text)
	}
	if !m.tmux.Exists(sess.ID) {
		t.Fatal("restart should leave the session running")
	}

	got, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentSessionID == "" || got.AgentSessionID == "old-conversation" {
		t.Fatalf("restarted conversation id = %q, want a fresh one", got.AgentSessionID)
	}
	if got.RetiredAgentSessionID != "old-conversation" {
		t.Fatalf("retired conversation = %q, want old-conversation", got.RetiredAgentSessionID)
	}
	if got.AgentLaunchedAt.Before(got.CreatedAt) || got.AgentLaunchedAt.IsZero() {
		t.Fatalf("launch time = %v, created = %v", got.AgentLaunchedAt, got.CreatedAt)
	}
	if got.Status != status.Starting {
		t.Fatalf("after restart, status = %q want %q", got.Status, status.Starting)
	}

	// The fresh conversation id rides the launch; the resume flags revive
	// would have used never appear.
	args := readWhenWritten(t, argsFile)
	if !strings.Contains(args, "--session-id\n"+got.AgentSessionID) {
		t.Fatalf("launch arguments = %q, want the fresh session id", args)
	}
	if strings.Contains(args, "--resume") || strings.Contains(args, "--continue") {
		t.Fatalf("restart must not resume, launch arguments = %q", args)
	}
}

// A tool that mints its own conversation id has nothing to
// hand the launch, so restart clears the binding and leaves the id for the
// poller to capture once the new conversation lands.
func TestRestartClearsCapturedConversationID(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "codexish", t.TempDir(), "")
	sess := m.sessionRows()[0]
	// The precondition under test, spelled out rather than inherited from
	// whatever flags the fake tools happen to carry.
	tool := m.cfg.Tools[sess.Tool]
	tool.SessionIDFlag = ""
	tool.SessionStore = "codex"
	m.cfg.Tools[sess.Tool] = tool

	if err := m.store.SetAgentSessionID(sess.ID, "captured-conversation"); err != nil {
		t.Fatal(err)
	}
	m.sessions[0].AgentSessionID = "captured-conversation"
	if err := m.tmux.Kill(sess.ID); err != nil {
		t.Fatalf("kill: %v", err)
	}
	m.selectSessionRow(t, "codexish")

	m.restartSelected()
	_, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)
	if m.errBar.text != "" {
		t.Fatalf("restart: %q", m.errBar.text)
	}

	got, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentSessionID != "" {
		t.Fatalf("conversation id = %q, want it cleared for capture", got.AgentSessionID)
	}
	if got.RetiredAgentSessionID != "captured-conversation" {
		t.Fatalf("retired conversation = %q", got.RetiredAgentSessionID)
	}
}

// Restart also serves the session that is still running: it ends the agent
// holding the context and brings the row back empty-handed.
func TestRestartEndsALiveAgentFirst(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "busy", t.TempDir(), "")
	sess := m.sessionRows()[0]
	tool := m.cfg.Tools[sess.Tool]
	tool.Command = argCaptureCommand(filepath.Join(t.TempDir(), "launch-args"))
	m.cfg.Tools[sess.Tool] = tool
	m.selectSessionRow(t, "busy")

	if _, _ = m.restartSelected(); m.mode != modeConfirmDelete {
		t.Fatalf("restart should ask first, mode = %v err = %q", m.mode, m.errBar.text)
	}
	if !strings.Contains(m.confirm.label, "ends the running agent") {
		t.Fatalf("confirm label = %q", m.confirm.label)
	}
	_, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)
	if m.errBar.text != "" {
		t.Fatalf("restart: %q", m.errBar.text)
	}
	if !m.tmux.Exists(sess.ID) {
		t.Fatal("restart should leave the session running")
	}
	got, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != status.Starting {
		t.Fatalf("after restart, status = %q want %q", got.Status, status.Starting)
	}
}

func TestRestartRefusesGroupRow(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	if err := m.store.CreateGroup("work", dir); err != nil {
		t.Fatal(err)
	}
	m.applyCmd(t, m.refreshCmd())
	createSession(t, m, "member", dir, "work")
	m.selectGroupRow(t, "work")

	if _, _ = m.restartSelected(); m.mode != modeList || m.errBar.text == "" {
		t.Fatalf("group restart should refuse, mode = %v err = %q", m.mode, m.errBar.text)
	}
}

func TestNewSessionShowsStartingImmediately(t *testing.T) {
	m := buildModel(t)
	m.openForm()
	m.form.name.SetValue("boot")
	m.form.dir.SetValue(t.TempDir())
	m.form.toolIndex = 0
	pickGroup(t, m, "")
	// submitForm without the follow-up refresh: the row must already show the
	// launch state from the optimistic insert alone.
	if _, _ = m.submitForm(); m.errBar.text != "" {
		t.Fatalf("submit: %q", m.errBar.text)
	}
	rows := m.sessionRows()
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	if rows[0].Status != status.Starting {
		t.Fatalf("new row status = %q, want %q", rows[0].Status, status.Starting)
	}
	t.Cleanup(func() { m.tmux.Kill(rows[0].ID) })
}

func TestReviveAllRecreatesEveryDeadSession(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	createSession(t, m, "alpha", dir, "")
	createSession(t, m, "beta", dir, "")

	for _, sess := range m.visibleSessions() {
		if err := m.tmux.Kill(sess.ID); err != nil {
			t.Fatalf("kill %s: %v", sess.Name, err)
		}
	}
	// A refresh marks the pane-less sessions dead so revive-all picks them up.
	m.applyCmd(t, m.refreshCmd())

	if _, _ = m.reviveAllDead(); m.errBar.text != "" {
		t.Fatalf("revive all: %q", m.errBar.text)
	}
	for _, sess := range m.visibleSessions() {
		if !m.tmux.Exists(sess.ID) {
			t.Fatalf("revive all should recreate %s", sess.Name)
		}
	}
}

// A live session is the case an operator hits when the CLI has been up
// since before the skills, settings or MCP servers under it changed: the
// agent cannot reload any of that from inside, and restarting on an empty
// context throws away the conversation to get it. v ends the agent and
// brings it back on the same conversation instead.
func TestReviveRestartsALiveSessionOnItsOwnConversation(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "alive", t.TempDir(), "")
	sess := m.sessionRows()[0]
	held := sess.AgentSessionID
	m.selectSessionRow(t, "alive")

	if _, _ = m.reviveSelected(); m.mode != modeConfirmDelete {
		t.Fatalf("v on a live session should ask first, mode = %v err = %q", m.mode, m.errBar.text)
	}
	if !strings.Contains(m.confirm.label, "ends the running agent") {
		t.Fatalf("confirm label = %q, want it to say the agent is ended", m.confirm.label)
	}
	_, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)
	if m.errBar.text != "" {
		t.Fatalf("restart on the held conversation: %q", m.errBar.text)
	}
	if !m.tmux.Exists(sess.ID) {
		t.Fatal("the session should be running again")
	}
	got, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The whole point: restart keeps the row, this keeps the conversation
	// too, so a later v resumes the same one rather than a fresh id.
	if got.AgentSessionID != held {
		t.Fatalf("conversation id = %q, want the held %q", got.AgentSessionID, held)
	}
}

// Cancelling leaves the agent alone. A confirm that killed the pane before
// the answer would make n more expensive than never pressing v.
func TestReviveOnALiveSessionCanBeCancelled(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "alive", t.TempDir(), "")
	sess := m.sessionRows()[0]
	m.selectSessionRow(t, "alive")

	if _, _ = m.reviveSelected(); m.mode != modeConfirmDelete {
		t.Fatalf("v on a live session should ask first, mode = %v", m.mode)
	}
	_, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'n', Text: "n"})
	m.applyCmd(t, cmd)
	if !m.tmux.Exists(sess.ID) {
		t.Fatal("a cancelled restart must leave the agent running")
	}
}

// The direct call keeps its guard: the group and view-wide paths run over
// sessions the operator never singled out, and restarting a live agent
// there is not what V was pressed for.
func TestReviveSessionStillRefusesALiveOne(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "alive", t.TempDir(), "")
	sess := m.sessionRows()[0]

	if err := m.reviveSession(sess); err == nil {
		t.Fatal("reviveSession on a live session should error")
	}
	if !m.tmux.Exists(sess.ID) {
		t.Fatal("live session must keep running")
	}
}

func TestReviveRefusesMissingDir(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	createSession(t, m, "homeless", dir, "")

	sess := m.sessionRows()[0]
	if err := m.tmux.Kill(sess.ID); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove dir: %v", err)
	}
	m.selectSessionRow(t, "homeless")

	if _, _ = m.reviveSelected(); m.errBar.text == "" {
		t.Fatal("revive without a working directory should error")
	}
}

func TestArchiveRestoreClearStaleError(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	createSession(t, m, "alpha", dir, "")

	m.selectSessionRow(t, "alpha")
	m.errBar.text = "stale failure from an earlier action"
	m.archiveSelected()
	_, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)
	if m.errBar.text != "" {
		t.Fatalf("archive should clear the stale error, err = %q", m.errBar.text)
	}

	m.showArchived = true
	m.applyCmd(t, m.refreshCmd())
	m.selectSessionRow(t, "alpha")
	m.errBar.text = "stale failure from an earlier action"
	m.restoreSelected()
	_, cmd = m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)
	if m.errBar.text != "" {
		t.Fatalf("restore should clear the stale error, err = %q", m.errBar.text)
	}
}

func TestRestoreKeepsArchiveWhenReviveFails(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	createSession(t, m, "homeless", dir, "")
	sess := m.sessionRows()[0]

	m.selectSessionRow(t, "homeless")
	m.archiveSelected()
	_, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove dir: %v", err)
	}

	m.showArchived = true
	m.applyCmd(t, m.refreshCmd())
	m.selectSessionRow(t, "homeless")
	m.restoreSelected()
	_, cmd = m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)
	if m.errBar.text == "" {
		t.Fatal("restore without a working directory should error")
	}
	if m.tmux.Exists(sess.ID) {
		t.Fatal("failed restore must not leave a tmux session")
	}
	got, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.Archived {
		t.Fatal("failed restore must leave the session archived")
	}
}

func TestArchiveAbortsWhenSnapshotFails(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	createSession(t, m, "alpha", dir, "")
	m.setSnapshots = func(snapshots map[string]string) error {
		return errors.New("disk full")
	}

	m.selectSessionRow(t, "alpha")
	m.archiveSelected()
	_, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)

	if m.errBar.text != "disk full" {
		t.Fatalf("snapshot failure should surface, err = %q", m.errBar.text)
	}
	if len(m.sessionRows()) != 1 {
		t.Fatalf("failed snapshot must not archive, active sessions = %d want 1", len(m.sessionRows()))
	}
	if !m.tmux.Exists(m.sessionRows()[0].ID) {
		t.Fatal("failed snapshot must not kill the tmux session")
	}
	active, err := m.store.ListSessions(false)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(active) != 1 || active[0].Archived {
		t.Fatalf("session should stay unarchived in the store, got %+v", active)
	}
}

func TestArchiveGroupMovesWholeSubtree(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	if err := m.store.CreateGroup("proj", ""); err != nil {
		t.Fatalf("create group: %v", err)
	}
	if err := m.store.CreateGroup("proj/sub", ""); err != nil {
		t.Fatalf("create subgroup: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())
	createSession(t, m, "top", dir, "proj")
	createSession(t, m, "deep", dir, "proj/sub")

	m.selectGroupRow(t, "proj")
	m.archiveSelected()
	_, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)

	if paths := m.groupRowPaths(); len(paths) != 0 {
		t.Fatalf("active view still shows group rows %v", paths)
	}
	if names := sessionNames(m); len(names) != 0 {
		t.Fatalf("active view still shows sessions %v", names)
	}

	m.showArchived = true
	m.applyCmd(t, m.refreshCmd())
	gotGroups := m.groupRowPaths()
	if len(gotGroups) != 2 || gotGroups[0] != "proj" || gotGroups[1] != "proj/sub" {
		t.Fatalf("archived view groups = %v want [proj proj/sub]", gotGroups)
	}
	if names := sessionNames(m); len(names) != 2 {
		t.Fatalf("archived view sessions = %v want 2", names)
	}
	for _, sess := range m.sessionRows() {
		if m.tmux.Exists(sess.ID) {
			t.Fatalf("archived session %s should be killed", sess.Name)
		}
	}

	m.selectGroupRow(t, "proj")
	archived := m.sessionRows()
	m.restoreSelected()
	_, cmd = m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	for _, sess := range archived {
		stored, err := m.store.Get(sess.ID)
		if err != nil {
			t.Fatalf("get %s: %v", sess.Name, err)
		}
		if stored.Status != status.Starting {
			t.Fatalf("after restore, %s status = %q want %q", sess.Name, stored.Status, status.Starting)
		}
		if stored.LaunchTime().Equal(stored.CreatedAt) {
			t.Fatalf("after restore, %s launch time was not refreshed", sess.Name)
		}
	}
	m.applyCmd(t, cmd)
	m.showArchived = false
	m.applyCmd(t, m.refreshCmd())
	if paths := m.groupRowPaths(); len(paths) != 2 {
		t.Fatalf("after restore, active groups = %v want 2", paths)
	}
	if names := sessionNames(m); len(names) != 2 {
		t.Fatalf("after restore, active sessions = %v want 2", names)
	}
	for _, sess := range m.sessionRows() {
		if !m.tmux.Exists(sess.ID) {
			t.Fatalf("restore should revive %s", sess.Name)
		}
	}
}

func TestArchiveGroupKeepsEmptyGroupInArchivedView(t *testing.T) {
	m := buildModel(t)
	if err := m.store.CreateGroup("empty", ""); err != nil {
		t.Fatalf("create group: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())

	m.selectGroupRow(t, "empty")
	m.archiveSelected()
	_, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)

	if paths := m.groupRowPaths(); len(paths) != 0 {
		t.Fatalf("archived empty group still in active view: %v", paths)
	}

	m.showArchived = true
	m.applyCmd(t, m.refreshCmd())
	if paths := m.groupRowPaths(); len(paths) != 1 || paths[0] != "empty" {
		t.Fatalf("archived view groups = %v want [empty]", paths)
	}
}

// Archiving must freeze the pane as a stored snapshot, and the poller must
// keep serving it instead of wiping the preview on the next tick.
func TestArchivedSessionKeepsPaneSnapshot(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "frozen", t.TempDir(), "")
	m.selectSessionRow(t, "frozen")
	sess := m.sessionRows()[0]

	if err := m.tmux.SendText(sess.ID, "snapshot-marker"); err != nil {
		t.Fatalf("send text: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		pane, err := m.tmux.CapturePane(sess.ID)
		if err == nil && strings.Contains(pane, "snapshot-marker") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane never showed the marker, last capture: %q", pane)
		}
		time.Sleep(100 * time.Millisecond)
	}

	m.archiveSelected()
	_, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)

	snapshot, err := m.store.Snapshot(sess.ID)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if !strings.Contains(snapshot, "snapshot-marker") {
		t.Fatalf("archive should persist the pane, snapshot = %q", snapshot)
	}
	if m.tmux.Exists(sess.ID) {
		t.Fatal("archive should kill the tmux session")
	}

	m.showArchived = true
	m.applyCmd(t, nil)
	m.selectSessionRow(t, "frozen")
	m.applyCmd(t, nil)
	if !strings.Contains(m.preview, "snapshot-marker") {
		t.Fatalf("archived preview should survive the poll tick, preview = %q", m.preview)
	}

	if err := m.tmux.Kill(sess.ID); err != nil {
		t.Fatalf("kill: %v", err)
	}
	m.preview = ""
	m.applyCmd(t, nil)
	if !strings.Contains(m.preview, "snapshot-marker") {
		t.Fatalf("archived preview should show the snapshot after tmux is gone, preview = %q", m.preview)
	}

	m.preview = ""
	m.previewGen++
	m.applyCmd(t, m.previewCmd(m.rows[m.cursor].sess, m.previewGen, true))
	if !strings.Contains(m.preview, "snapshot-marker") {
		t.Fatalf("previewCmd should serve the snapshot for an archived session, preview = %q", m.preview)
	}
}

// waitForPane blocks until a session's pane shows the marker, so a test can
// act on a pane that has actually painted.
func waitForPane(t *testing.T, m *Model, id, marker string) {
	t.Helper()
	if err := m.tmux.SendText(id, marker); err != nil {
		t.Fatalf("send text: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		pane, err := m.tmux.CapturePane(id)
		if err == nil && strings.Contains(pane, marker) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane never showed %q, last capture: %q", marker, pane)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// confirmKill answers the pending confirm modal with yes.
func confirmArchive(t *testing.T, m *Model) {
	t.Helper()
	if m.mode != modeConfirmDelete {
		t.Fatalf("archive should ask before acting, mode = %v, err = %q", m.mode, m.errBar.text)
	}
	if m.confirm.ack != "" {
		m.handleConfirmKey(tea.KeyPressMsg{Code: ' ', Text: " "})
	}
	_, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)
	if m.errBar.text != "" {
		t.Fatalf("archive: %q", m.errBar.text)
	}
}

// expireArchives runs the retention sweep with every archived row past its
// window, which is what a week of wall clock would do.
func expireArchives(m *Model) tea.Cmd {
	return m.sweepArchivesBefore(time.Now().Add(time.Hour))
}

// seedGroups creates group rows so the new-session picker offers them.
func seedGroups(t *testing.T, m *Model, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if err := m.store.CreateGroup(path, ""); err != nil {
			t.Fatalf("create group %s: %v", path, err)
		}
	}
	m.applyCmd(t, m.refreshCmd())
}

// x is the board's one teardown: it ends the pane, files the row under t,
// and leaves everything restore needs. Nothing about the session is lost
// until the retention window runs out.
func TestArchiveEndsTheSessionAndKeepsItRestorable(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "hungry", t.TempDir(), "")
	m.selectSessionRow(t, "hungry")
	sess := m.sessionRows()[0]
	waitForPane(t, m, sess.ID, "kill-marker")

	m.archiveSelected()
	confirmArchive(t, m)

	if m.tmux.Exists(sess.ID) {
		t.Fatal("archive should end the tmux session")
	}
	if len(m.sessionRows()) != 0 {
		t.Fatalf("the active view should be empty, rows = %d", len(m.sessionRows()))
	}
	stored, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !stored.Archived {
		t.Fatal("archive should file the row away")
	}
	if stored.ArchivedAt.IsZero() {
		t.Fatal("archive should start the retention clock")
	}
	if stored.Status != status.Dead {
		t.Fatalf("after archive, status = %q want %q", stored.Status, status.Dead)
	}

	snapshot, err := m.store.Snapshot(sess.ID)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if !strings.Contains(snapshot, "kill-marker") {
		t.Fatalf("archive should freeze the pane, snapshot = %q", snapshot)
	}

	m.showArchived = true
	m.applyCmd(t, m.refreshCmd())
	m.selectSessionRow(t, "hungry")
	if _, _ = m.restoreSelected(); m.mode != modeConfirmDelete {
		t.Fatalf("restore should ask, mode = %v (%q)", m.mode, m.errBar.text)
	}
	_, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)
	if !m.tmux.Exists(sess.ID) {
		t.Fatal("restore should bring an archived session back")
	}
	back, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if back.Archived || !back.ArchivedAt.IsZero() {
		t.Fatalf("restore should clear the archive and its clock, got archived=%v at=%v", back.Archived, back.ArchivedAt)
	}
}

// A row already dead still archives. x means "I am done with this", and
// refusing on a dead session would leave no way to clear it but to wait.
func TestArchiveTakesADeadSessionToo(t *testing.T) {
	m := buildModel(t)
	seedGroups(t, m, "work")
	createSession(t, m, "ghost", t.TempDir(), "work")
	sess := m.sessionRows()[0]
	if err := m.tmux.Kill(sess.ID); err != nil {
		t.Fatalf("kill: %v", err)
	}

	m.selectSessionRow(t, "ghost")
	m.archiveSelected()
	confirmArchive(t, m)
	stored, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !stored.Archived {
		t.Fatal("archiving a dead session should still file it away")
	}
}

func TestArchiveGroupEndsEverySessionInside(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	seedGroups(t, m, "work", "work/api")
	createSession(t, m, "alpha", dir, "work")
	createSession(t, m, "beta", dir, "work/api")
	createSession(t, m, "outside", dir, "")

	m.selectGroupRow(t, "work")
	m.archiveSelected()
	confirmArchive(t, m)

	for _, sess := range m.visibleSessions() {
		alive := m.tmux.Exists(sess.ID)
		if sess.Name == "outside" && !alive {
			t.Fatal("a group archive must leave sessions outside the group running")
		}
		if sess.Name != "outside" && alive {
			t.Fatalf("group archive should have ended %s", sess.Name)
		}
	}
}

func TestReviveGroupBringsBackEverySessionInside(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	seedGroups(t, m, "work", "work/api")
	createSession(t, m, "alpha", dir, "work")
	createSession(t, m, "beta", dir, "work/api")
	createSession(t, m, "outside", dir, "")

	m.selectGroupRow(t, "work")
	m.archiveSelected()
	confirmArchive(t, m)

	// The group left the active view with its sessions, so this is where it
	// is now. v still means revive there; u is the key that also unfiles it.
	m.showArchived = true
	m.applyCmd(t, m.refreshCmd())
	m.selectGroupRow(t, "work")
	if _, _ = m.reviveSelected(); m.errBar.text != "" {
		t.Fatalf("revive group: %q", m.errBar.text)
	}
	for _, sess := range m.visibleSessions() {
		if !m.tmux.Exists(sess.ID) {
			t.Fatalf("revive group should have brought back %s", sess.Name)
		}
	}
}

func TestArchiveAllEndsEverySessionInView(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	seedGroups(t, m, "work")
	createSession(t, m, "alpha", dir, "work")
	createSession(t, m, "outside", dir, "")

	updated, _ := m.handleKey(tea.KeyPressMsg{Code: 'X', Text: "X"})
	m = updated.(*Model)
	confirmArchive(t, m)

	for _, sess := range m.visibleSessions() {
		if m.tmux.Exists(sess.ID) {
			t.Fatalf("archive all should have ended %s", sess.Name)
		}
	}
	if _, _ = m.archiveAllLive(); m.errBar.text == "" {
		t.Fatal("archive all with nothing listed should report it")
	}
}

// X covers every session on screen, which is more than a reader takes in from
// one sentence, so it asks for a tick as well as a y. Until the box is
// ticked, y does nothing but say so.
func TestArchiveAllNeedsTheTickFirst(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	createSession(t, m, "alpha", dir, "")
	createSession(t, m, "beta", dir, "")

	updated, _ := m.handleKey(tea.KeyPressMsg{Code: 'X', Text: "X"})
	m = updated.(*Model)
	if m.confirm.ack == "" {
		t.Fatal("archiving the whole view should ask for a tick")
	}

	m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if m.mode != modeConfirmDelete {
		t.Fatalf("an unticked y should leave the dialog up, mode = %v", m.mode)
	}
	if !m.confirm.nudged {
		t.Fatal("an unticked y should mark the tick as the thing in the way")
	}
	for _, sess := range m.visibleSessions() {
		if !m.tmux.Exists(sess.ID) {
			t.Fatalf("an unticked y ended %s", sess.Name)
		}
	}
	if card := cardText(m); !strings.Contains(card, "tick it with space first") {
		t.Fatalf("the dialog should say what is in the way:\n%s", card)
	}

	m.handleConfirmKey(tea.KeyPressMsg{Code: ' ', Text: " "})
	if !m.confirm.acked || m.confirm.nudged {
		t.Fatalf("space should tick the box, acked=%v nudged=%v", m.confirm.acked, m.confirm.nudged)
	}
	if card := cardText(m); !strings.Contains(card, "[x] yes, end all 2") {
		t.Fatalf("a ticked box should read as ticked:\n%s", card)
	}

	_, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)
	for _, sess := range m.visibleSessions() {
		if m.tmux.Exists(sess.ID) {
			t.Fatalf("a ticked y should have ended %s", sess.Name)
		}
	}
}

// Ticking and then unticking puts the gate back, so a mis-hit space is not a
// tick that stays ticked.
func TestArchiveAllTickToggles(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "alpha", t.TempDir(), "")

	updated, _ := m.handleKey(tea.KeyPressMsg{Code: 'X', Text: "X"})
	m = updated.(*Model)
	m.handleConfirmKey(tea.KeyPressMsg{Code: ' ', Text: " "})
	m.handleConfirmKey(tea.KeyPressMsg{Code: ' ', Text: " "})
	if m.confirm.acked {
		t.Fatal("a second space should untick the box")
	}
	m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if m.mode != modeConfirmDelete {
		t.Fatal("an unticked box should still hold the answer back")
	}
}

// The archived view lists rows that are already on the clock, and x/X reach
// every row a view lists. Re-filing one would stamp it afresh, so one sweep
// in that view would buy every session on screen another seven days.
func TestArchivingAgainDoesNotRestartTheClock(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "alpha", t.TempDir(), "")
	m.selectSessionRow(t, "alpha")
	sess := m.sessionRows()[0]
	m.archiveSelected()
	confirmArchive(t, m)

	filed, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	m.showArchived = true
	m.applyCmd(t, m.refreshCmd())
	m.selectSessionRow(t, "alpha")
	if _, _ = m.archiveSelected(); m.mode == modeConfirmDelete {
		t.Fatal("an archived row should not open the archive confirm again")
	}
	if m.errBar.text == "" {
		t.Fatal("archiving an archived row should say it is already filed")
	}

	// And the whole-view sweep finds nothing to do rather than re-filing them.
	m.errBar.text = ""
	if _, _ = m.archiveAllLive(); m.mode == modeConfirmDelete {
		t.Fatal("X over the archived view should find nothing to archive")
	}

	again, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !again.ArchivedAt.Equal(filed.ArchivedAt) {
		t.Fatalf("the clock moved: %v -> %v", filed.ArchivedAt, again.ArchivedAt)
	}
}

// A dialog that names one session is one keystroke, the way it always was.
func TestArchiveOneAsksForNoTick(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "alpha", t.TempDir(), "")
	m.selectSessionRow(t, "alpha")
	m.archiveSelected()
	if m.confirm.ack != "" {
		t.Fatalf("a single-session archive should not ask for a tick, got %q", m.confirm.ack)
	}
}

// deleteSession takes a row off the board for good the way an operator does
// now: archive it, then let its retention window run out.
func deleteSession(t *testing.T, m *Model, name string) {
	t.Helper()
	m.selectSessionRow(t, name)
	m.archiveSelected()
	_, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)
	cmd = expireArchives(m)
	// Worktree cleanup is git over a whole checkout, which the confirm
	// handler hands back rather than paying for on the event loop. The
	// runtime runs that command and feeds its answer back through Update, so
	// a test about what a delete leaves on disk has to do the same.
	if cmd == nil {
		return
	}
	if msg := cmd(); msg != nil {
		updated, _ := m.Update(msg)
		*m = *updated.(*Model)
	}
}

// Restart has to hold for every CLI the manager ships with, not just the one
// the fake tools stand in for: it launches each tool the way a brand new
// session does, and never reaches for a resume, continue or fork command.
func TestRestartLaunchIsAFreshStartForEveryShippedTool(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Tools) < 4 {
		t.Fatalf("built-in tools = %d, expected every shipped CLI", len(cfg.Tools))
	}
	for name, tool := range cfg.Tools {
		// A shell has no conversation for a restart to start fresh.
		if tool.Shell {
			continue
		}
		command, agentSessionID := restartLaunch(tool)
		if !strings.HasPrefix(command, tool.Command) {
			t.Errorf("%s: restart command %q does not start from its launch command %q", name, command, tool.Command)
		}
		rest := strings.TrimPrefix(command, tool.Command)
		for _, resume := range []string{"resume", "continue", "fork", "--session ", "--last"} {
			if strings.Contains(rest, resume) {
				t.Errorf("%s: restart command %q carries %q", name, command, resume)
			}
		}
		if tool.SessionIDFlag == "" {
			// Some tools mint their own id; the poller captures it.
			if agentSessionID != "" || rest != "" {
				t.Errorf("%s: restart handed an id to a tool that mints its own: %q", name, command)
			}
			if tool.SessionStore == "" {
				t.Errorf("%s: no session_id_flag and no session_store leaves restart with no conversation id at all", name)
			}
			continue
		}
		if _, err := uuid.Parse(agentSessionID); err != nil {
			t.Errorf("%s: restart conversation id %q is not a uuid: %v", name, agentSessionID, err)
		}
		if want := " " + tool.SessionIDFlag + " " + agentSessionID; rest != want {
			t.Errorf("%s: restart command tail = %q, want %q", name, rest, want)
		}
		if _, second := restartLaunch(tool); second == agentSessionID {
			t.Errorf("%s: two restarts reused conversation id %q", name, agentSessionID)
		}
	}
}

func TestArchiveAgentIncludesChildren(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	if err := m.store.CreateGroup("backend", dir); err != nil {
		t.Fatalf("group: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())
	createSession(t, m, "coder", dir, "backend")
	m.selectSessionRow(t, "coder")
	shell := spawnTerminal(t, m)
	m.selectSessionRow(t, "coder")
	_, cmd := m.archiveSelected()
	m.applyCmd(t, cmd)
	if !strings.Contains(m.confirm.label, "terminal") {
		t.Fatalf("confirm should name terminals: %q", m.confirm.label)
	}
	ids := map[string]bool{}
	for _, sess := range m.confirm.sessions {
		ids[sess.ID] = true
	}
	if !ids[shell.ID] {
		t.Fatal("archive confirm omitted the child")
	}
}

func TestArchiveChildIsSingleSession(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	if err := m.store.CreateGroup("backend", dir); err != nil {
		t.Fatalf("group: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())
	createSession(t, m, "coder", dir, "backend")
	m.selectSessionRow(t, "coder")
	shell := spawnTerminal(t, m)
	m.selectSessionRow(t, shell.Name)
	m.archiveSelected()
	if len(m.confirm.sessions) != 1 || m.confirm.sessions[0].ID != shell.ID {
		t.Fatalf("child archive = %+v", m.confirm.sessions)
	}
}

func TestArchiveAgentPersistsEveryChild(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	if err := m.store.CreateGroup("backend", dir); err != nil {
		t.Fatalf("group: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())
	createSession(t, m, "coder", dir, "backend")
	m.selectSessionRow(t, "coder")
	shell := spawnTerminal(t, m)
	m.selectSessionRow(t, "coder")
	m.archiveSelected()
	_, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)
	child, err := m.store.Get(shell.ID)
	if err != nil || !child.Archived {
		t.Fatalf("child archived=%v err=%v", child.Archived, err)
	}
}

func TestExpiryRemovesChildrenBeforeTheirAgent(t *testing.T) {
	agent := store.Session{ID: "agent"}
	child := store.Session{ID: "sh", ParentID: "agent"}
	ordered := childrenFirst([]store.Session{agent, child})
	if len(ordered) != 2 || ordered[0].ID != child.ID || ordered[1].ID != agent.ID {
		t.Fatalf("order = %+v", ordered)
	}
}

func TestArchiveDeadAgentStillKillsItsLiveChild(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	if err := m.store.CreateGroup("backend", dir); err != nil {
		t.Fatalf("group: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())
	createSession(t, m, "coder", dir, "backend")
	m.selectSessionRow(t, "coder")
	shell := spawnTerminal(t, m)
	agent := m.sessionRows()[0]
	if err := m.tmux.Kill(agent.ID); err != nil {
		t.Fatalf("kill agent: %v", err)
	}
	m.selectSessionRow(t, "coder")
	_, cmd := m.archiveSelected()
	m.applyCmd(t, cmd)
	if m.mode != modeConfirmDelete {
		t.Fatalf("mode = %v, want the archive confirm (errBar %q)", m.mode, m.errBar.text)
	}
	_, cmd = m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)
	if m.tmux.Exists(shell.ID) {
		t.Fatal("live child survived the archive")
	}
}
func TestRestoreAgentUnarchivesEveryChild(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	if err := m.store.CreateGroup("backend", dir); err != nil {
		t.Fatalf("group: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())
	createSession(t, m, "coder", dir, "backend")
	m.selectSessionRow(t, "coder")
	shell := spawnTerminal(t, m)
	agent := m.sessionRows()[0]
	m.selectSessionRow(t, "coder")
	m.archiveSelected()
	_, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)
	m.showArchived = true
	m.applyCmd(t, m.refreshCmd())
	m.selectSessionRow(t, "coder")
	m.restoreSelected()
	_, cmd = m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)
	for _, id := range []string{agent.ID, shell.ID} {
		got, err := m.store.Get(id)
		if err != nil || got.Archived {
			t.Fatalf("%s archived=%v err=%v", id, got.Archived, err)
		}
		if !m.tmux.Exists(id) {
			t.Fatalf("%s not running", id)
		}
	}
}

func TestArchiveAndExpiryIncludeChildren(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	if err := m.store.CreateGroup("backend", dir); err != nil {
		t.Fatalf("group: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())
	createSession(t, m, "coder", dir, "backend")
	m.selectSessionRow(t, "coder")
	shell := spawnTerminal(t, m)
	m.selectSessionRow(t, "coder")
	m.archiveSelected()
	ids := map[string]bool{}
	for _, sess := range m.confirm.sessions {
		ids[sess.ID] = true
	}
	if !ids[shell.ID] {
		t.Fatal("archive confirm omitted the child")
	}
	agent := m.sessionRows()[0]
	_, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)
	m.applyCmd(t, expireArchives(m))
	for _, id := range []string{shell.ID, agent.ID} {
		if _, err := m.store.Get(id); err == nil {
			t.Fatalf("%s row survived its retention window", id)
		}
		if m.tmux.Exists(id) {
			t.Fatalf("%s pane survived its retention window", id)
		}
	}
}

func TestReviveAgentIncludesDeadChildren(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	if err := m.store.CreateGroup("backend", dir); err != nil {
		t.Fatalf("group: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())
	createSession(t, m, "coder", dir, "backend")
	m.selectSessionRow(t, "coder")
	shell := spawnTerminal(t, m)
	m.selectSessionRow(t, "coder")
	coder, ok := m.selected()
	if !ok {
		t.Fatal("coder row missing")
	}
	if err := m.tmux.Kill(shell.ID); err != nil {
		t.Fatalf("kill child: %v", err)
	}
	if err := m.tmux.Kill(coder.ID); err != nil {
		t.Fatalf("kill agent: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())
	m.selectSessionRow(t, "coder")
	m.reviveSelected()
	ids := map[string]bool{}
	for _, sess := range m.confirm.sessions {
		ids[sess.ID] = true
	}
	if !ids[shell.ID] {
		t.Fatal("revive confirm omitted the child")
	}
	_, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)
	if !m.tmux.Exists(coder.ID) || !m.tmux.Exists(shell.ID) {
		t.Fatal("confirm should revive the agent and its dead children")
	}
}

func TestRestartAgentStaysSingleSession(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	if err := m.store.CreateGroup("backend", dir); err != nil {
		t.Fatalf("group: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())
	createSession(t, m, "coder", dir, "backend")
	m.selectSessionRow(t, "coder")
	spawnTerminal(t, m)
	m.selectSessionRow(t, "coder")
	m.restartSelected()
	if len(m.confirm.sessions) != 1 {
		t.Fatalf("restart confirm = %+v", m.confirm.sessions)
	}
}

func TestArchiveAgentConfirmNamesExtraTerminals(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	if err := m.store.CreateGroup("backend", dir); err != nil {
		t.Fatalf("group: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())
	createSession(t, m, "coder", dir, "backend")
	m.selectSessionRow(t, "coder")
	spawnTerminal(t, m)
	m.selectSessionRow(t, "coder")
	spawnTerminal(t, m)
	m.selectSessionRow(t, "coder")
	m.archiveSelected()
	want := "end coder and 2 terminals? frees their RAM, t finds them, deleted for good after 7 days."
	if m.confirm.label != want {
		t.Fatalf("label = %q, want %q", m.confirm.label, want)
	}
}

// adoptedRow plants a session the manager did not start: a pane on somebody
// else's tmux server, reached the way adoption leaves it — a store row
// carrying the pane's location and a driver that resolves the id to it.
func adoptedRow(t *testing.T, m *Model, name, group string) (id, socket, pane string) {
	t.Helper()
	socket, pane = uiForeignServer(t, "cat")
	id = "adopted" + name
	if err := m.store.CreateSession(store.Session{
		ID:           id,
		Name:         name,
		Tool:         "claude",
		Cwd:          t.TempDir(),
		Group:        group,
		Status:       status.Idle,
		CreatedAt:    time.Now(),
		LastStatusAt: time.Now(),
		TmuxSocket:   socket,
		TmuxPaneID:   pane,
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := m.tmux.Adopt(id, tmux.Target{Socket: socket, Name: pane}); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	m.applyCmd(t, nil)
	return id, socket, pane
}

func foreignPaneAlive(t *testing.T, socket, pane string) bool {
	t.Helper()
	out, err := exec.Command("tmux", "-L", socket, "list-panes", "-a", "-F", "#{pane_id}").CombinedOutput()
	if err != nil {
		// A server whose last pane died exits, and listing it fails.
		return false
	}
	for _, id := range strings.Fields(string(out)) {
		if id == pane {
			return true
		}
	}
	return false
}

// The sweep is the dangerous one: one keystroke over a whole view cannot be
// an informed answer about somebody else's pane, so it leaves adopted panes
// alone and says on the dialog that it is doing so.
func TestArchiveAllLeavesAdoptedPanesAlone(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "mine", t.TempDir(), "")
	_, socket, pane := adoptedRow(t, m, "borrowed", "")
	mine := m.sessionRows()[0]
	if mine.Name != "mine" {
		mine = m.sessionRows()[1]
	}

	updated, _ := m.handleKey(tea.KeyPressMsg{Code: 'X', Text: "X"})
	m = updated.(*Model)
	if m.mode != modeConfirmDelete {
		t.Fatalf("archive all should ask first, mode = %v, err = %q", m.mode, m.errBar.text)
	}
	if want := "end every session listed (1)?"; !strings.Contains(m.confirm.label, want) {
		t.Fatalf("the sweep should count only its own sessions, label = %q", m.confirm.label)
	}
	if want := "1 adopted pane stays up"; !strings.Contains(m.confirm.label, want) {
		t.Fatalf("the sweep should say what it is leaving behind, label = %q", m.confirm.label)
	}
	for _, sess := range m.confirm.sessions {
		if sess.TmuxPaneID != "" {
			t.Fatalf("the sweep took an adopted pane with it: %+v", sess)
		}
	}

	confirmArchive(t, m)
	if m.tmux.Exists(mine.ID) {
		t.Fatal("archive all should have ended the manager's own session")
	}
	if !foreignPaneAlive(t, socket, pane) {
		t.Fatal("archive all must not touch a pane the manager did not start")
	}
}

// A group archive cannot leave adopted panes out the way a sweep does —
// their rows go when the group goes — so it says how many of them there are.
func TestArchiveGroupCountsTheAdoptedPanesInIt(t *testing.T) {
	m := buildModel(t)
	seedGroups(t, m, "work")
	createSession(t, m, "mine", t.TempDir(), "work")
	adoptedRow(t, m, "borrowed", "work")

	m.selectGroupRow(t, "work")
	m.archiveSelected()
	if want := "1 of them is a pane the manager did not start"; !strings.Contains(m.confirm.label, want) {
		t.Fatalf("group archive should count the adopted panes, label = %q", m.confirm.label)
	}
}

// Archive used to refuse an adopted pane outright, which made archive
// unusable on a board that is mostly adopted panes: one refusal aborted the
// whole set. It goes through now, and what replaces the refusal is the
// warning -- archive says the pane and its agent die, in the same words kill
// and delete use, before it does anything.
func TestArchiveAdoptedPaneWarnsThenEndsIt(t *testing.T) {
	m := buildModel(t)
	id, socket, pane := adoptedRow(t, m, "borrowed", "")
	m.selectSessionRow(t, "borrowed")

	m.archiveSelected()
	if m.mode != modeConfirmDelete {
		t.Fatalf("archive should ask first, mode = %v, err = %q", m.mode, m.errBar.text)
	}
	for _, want := range []string{
		"the manager did not start borrowed", pane, socket, "end it?",
		"kills the pane, not just the row", "the agent running in it dies",
	} {
		if !strings.Contains(m.confirm.label, want) {
			t.Fatalf("the warning is missing %q:\n%s", want, m.confirm.label)
		}
	}

	_, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)
	if m.errBar.text != "" {
		t.Fatalf("archive: %q", m.errBar.text)
	}
	if foreignPaneAlive(t, socket, pane) {
		t.Fatal("a confirmed archive should end the adopted pane")
	}
	sess, err := m.store.Get(id)
	if err != nil {
		t.Fatalf("the row should survive an archive: %v", err)
	}
	if !sess.Archived {
		t.Fatal("archive killed the pane and left the row unarchived")
	}
	if names := archivedRowNames(t, m); !slices.Contains(names, "borrowed") {
		t.Fatalf("t should find the archived row, archived view has %v", names)
	}
}

// The warning is only worth anything if it reaches the screen: the label
// lives in the model, and this asserts the dialog the operator actually looks
// at carries the words, title included.
func TestArchiveAdoptedDialogRendersTheWarning(t *testing.T) {
	m := buildModel(t)
	_, socket, pane := adoptedRow(t, m, "borrowed", "")
	m.selectSessionRow(t, "borrowed")
	m.width, m.height = 200, 50
	m.archiveSelected()

	frame := flattenFrame(m.frame())
	for _, want := range []string{
		"End someone else's pane",
		"the manager did not start borrowed",
		"pane " + pane + " on tmux server " + socket,
		"kills the pane, not just the row",
		"the agent running in it dies",
	} {
		if !strings.Contains(frame, want) {
			t.Fatalf("the rendered dialog is missing %q:\n%s", want, ansi.Strip(m.frame()))
		}
	}
}

// A group archive takes its adopted panes with it -- it cannot leave them
// behind the way a kill sweep does -- so it says how many, and then ends
// managed and adopted alike.
func TestArchiveGroupWarnsAboutAdoptedPanesAndEndsThem(t *testing.T) {
	m := buildModel(t)
	seedGroups(t, m, "work")
	createSession(t, m, "mine", t.TempDir(), "work")
	id, socket, pane := adoptedRow(t, m, "borrowed", "work")
	mine := namedSessionRow(t, m, "mine")

	m.selectGroupRow(t, "work")
	m.archiveSelected()
	if want := "1 of them is a pane the manager did not start"; !strings.Contains(m.confirm.label, want) {
		t.Fatalf("group archive should count the adopted panes, label = %q", m.confirm.label)
	}

	_, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)
	if m.errBar.text != "" {
		t.Fatalf("group archive: %q", m.errBar.text)
	}
	if foreignPaneAlive(t, socket, pane) {
		t.Fatal("a confirmed group archive should end the adopted pane in it")
	}
	if m.tmux.Exists(mine.ID) {
		t.Fatal("a group archive should end the manager's own session too")
	}
	for _, want := range []string{"mine", "borrowed"} {
		if !slices.Contains(archivedRowNames(t, m), want) {
			t.Fatalf("t should find %q archived, archived view has %v", want, archivedRowNames(t, m))
		}
	}
	if sess, err := m.store.Get(id); err != nil || !sess.Archived {
		t.Fatalf("the adopted row should be archived, got %+v err %v", sess, err)
	}
}

// A session with terminals under it is one set, and the adopted pane in that
// set is what the operator has to be told about before answering for all of
// them.
func TestArchiveSessionWithAdoptedChildWarnsAndEndsBoth(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "parent", t.TempDir(), "")
	parent := namedSessionRow(t, m, "parent")
	socket, pane := uiForeignServer(t, "cat")
	if err := m.store.CreateSession(store.Session{
		ID: "adoptedchild", Name: "child", Tool: "claude", Cwd: t.TempDir(),
		ParentID: parent.ID, Status: status.Idle,
		CreatedAt: time.Now(), LastStatusAt: time.Now(),
		TmuxSocket: socket, TmuxPaneID: pane,
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := m.tmux.Adopt("adoptedchild", tmux.Target{Socket: socket, Name: pane}); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	m.applyCmd(t, nil)

	m.selectSessionRow(t, "parent")
	m.archiveSelected()
	if want := "1 of them is a pane the manager did not start"; !strings.Contains(m.confirm.label, want) {
		t.Fatalf("a follow-set archive should warn about the adopted pane in it, label = %q", m.confirm.label)
	}

	_, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)
	if m.errBar.text != "" {
		t.Fatalf("archive: %q", m.errBar.text)
	}
	if foreignPaneAlive(t, socket, pane) {
		t.Fatal("archiving the parent should have ended the adopted child pane")
	}
	if m.tmux.Exists(parent.ID) {
		t.Fatal("archiving should have ended the parent session")
	}
	for _, want := range []string{"parent", "child"} {
		if !slices.Contains(archivedRowNames(t, m), want) {
			t.Fatalf("t should find %q archived, archived view has %v", want, archivedRowNames(t, m))
		}
	}
}

// An archive that fails partway used to return on the first error, leaving
// the sessions it had already killed showing as live rows: panes dead,
// nothing filed away. Every session is archived as its own kill returns now,
// so a failure costs that session and nothing else.
func TestArchiveKeepsWhatSucceededWhenOneFails(t *testing.T) {
	m := buildModel(t)
	seedGroups(t, m, "work")
	createSession(t, m, "first", t.TempDir(), "work")
	createSession(t, m, "second", t.TempDir(), "work")

	m.selectGroupRow(t, "work")
	m.archiveSelected()
	if len(m.confirm.sessions) != 2 {
		t.Fatalf("expected both sessions in the set, got %+v", m.confirm.sessions)
	}
	// The second row goes out from under the archive between the dialog and
	// the answer, which is what makes its bookkeeping fail while the first
	// one's succeeds. Its window goes first so the pre-kill snapshot pass
	// skips it and the failure lands where this test is aiming: in the loop
	// that has already killed something.
	doomed := m.confirm.sessions[1]
	kept := m.confirm.sessions[0]
	if err := m.tmux.Kill(doomed.ID); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if err := m.store.Delete(doomed.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)
	if !strings.Contains(m.errBar.text, "archived 1 of 2") {
		t.Fatalf("a partial archive should report what it managed, err = %q", m.errBar.text)
	}
	sess, err := m.store.Get(kept.ID)
	if err != nil {
		t.Fatalf("the surviving row should still be there: %v", err)
	}
	if !sess.Archived {
		t.Fatalf("%s was killed and left showing as live: the failure stranded it", kept.Name)
	}
	if m.tmux.Exists(kept.ID) {
		t.Fatalf("%s should have been ended by the archive", kept.Name)
	}
}

// namedSessionRow finds a session row by name so a test can hold on to its id.
func namedSessionRow(t *testing.T, m *Model, name string) store.Session {
	t.Helper()
	for _, sess := range m.sessionRows() {
		if sess.Name == name {
			return sess
		}
	}
	t.Fatalf("no session row named %q", name)
	return store.Session{}
}

// archivedRowNames is the archived view as t reaches it: the operator's own
// keystroke, not a field poke, so the test breaks if t stops finding what
// archive filed away.
func archivedRowNames(t *testing.T, m *Model) []string {
	t.Helper()
	if !m.showArchived {
		updated, cmd := m.handleKey(tea.KeyPressMsg{Code: 't', Text: "t"})
		m = updated.(*Model)
		m.applyCmd(t, cmd)
	}
	var names []string
	for _, row := range m.rows {
		if row.isSession() {
			names = append(names, row.sess.Name)
		}
	}
	return names
}

// flattenFrame reads a rendered frame the way an operator does: the styling
// off and the wrapping undone, so a sentence the dialog broke across lines
// still reads as one sentence.
func flattenFrame(frame string) string {
	plain := strings.Map(func(r rune) rune {
		// The card's own border sits between the two halves of every
		// wrapped sentence, so it goes before the words are rejoined.
		if strings.ContainsRune("│─╭╮╰╯├┤", r) {
			return ' '
		}
		return r
	}, ansi.Strip(frame))
	return strings.Join(strings.Fields(plain), " ")
}

// The restart is only worth having if the new process is wired to the
// current manager: the MCP config, the settings file and $GATE_INBOX_BIN
// are composed at launch, and reusing the ones the session started on would
// bring the agent back onto exactly the stale tools it was restarted to
// escape. Deleting the config and finding it rewritten is what says the
// relaunch went through the full launch path rather than replaying a
// recorded command.
func TestRestartingALiveSessionRewritesItsMCPRegistration(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "alive", t.TempDir(), "")
	mcpConfig := filepath.Join(m.hooks.Dir(), "gate-inbox-mcp-claude.json")
	if _, err := os.Stat(mcpConfig); err != nil {
		t.Fatalf("the spawn should have written %s: %v", mcpConfig, err)
	}
	if err := os.Remove(mcpConfig); err != nil {
		t.Fatal(err)
	}
	m.selectSessionRow(t, "alive")

	if _, _ = m.reviveSelected(); m.mode != modeConfirmDelete {
		t.Fatalf("v on a live session should ask first, mode = %v", m.mode)
	}
	_, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)
	if m.errBar.text != "" {
		t.Fatalf("restart: %q", m.errBar.text)
	}

	written, err := os.ReadFile(mcpConfig)
	if err != nil {
		t.Fatalf("the restart should have re-registered the MCP server: %v", err)
	}
	if !strings.Contains(string(written), launch.Executable()) {
		t.Fatalf("rewritten MCP config does not point at the current manager (%s): %s",
			launch.Executable(), written)
	}
}

// The incident this closes: v on a live parent with 47 dead archived
// children revived all 47 and skipped the parent, which was the only session
// the dialog named. The parent is what v is about; the children are an
// opt-in that starts off.
func TestReviveOnALiveParentRestartsItAndLeavesDeadChildrenAlone(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	createSession(t, m, "parent", dir, "")
	parent := m.sessionRows()[0]
	m.selectSessionRow(t, "parent")
	child := spawnTerminal(t, m)
	if err := m.tmux.Kill(child.ID); err != nil {
		t.Fatalf("kill child: %v", err)
	}
	m.selectSessionRow(t, "parent")

	if _, _ = m.reviveSelected(); m.mode != modeConfirmDelete {
		t.Fatalf("v on a live parent should ask first, mode = %v err = %q", m.mode, m.errBar.text)
	}
	if m.confirm.action != actionResume {
		t.Fatalf("action = %q, want the restart rather than the mass revive", m.confirm.action)
	}
	if !m.confirm.keepChildren || len(m.confirm.keptChildren) != 1 {
		t.Fatalf("the dead child should be offered and left off by default: keep=%v kept=%d",
			m.confirm.keepChildren, len(m.confirm.keptChildren))
	}
	_, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)
	if m.errBar.text != "" {
		t.Fatalf("restart: %q", m.errBar.text)
	}
	if !m.tmux.Exists(parent.ID) {
		t.Fatal("the parent should be running again: it is what v was pressed on")
	}
	if m.tmux.Exists(child.ID) {
		t.Fatal("the dead child came back without being asked for")
	}
}

// Pressing k takes them along, which is the old behaviour still available to
// anyone who wanted it -- now as a choice rather than as the default.
func TestReviveOnALiveParentCanTakeTheDeadChildrenToo(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	createSession(t, m, "parent", dir, "")
	parent := m.sessionRows()[0]
	m.selectSessionRow(t, "parent")
	child := spawnTerminal(t, m)
	if err := m.tmux.Kill(child.ID); err != nil {
		t.Fatalf("kill child: %v", err)
	}
	m.selectSessionRow(t, "parent")

	if _, _ = m.reviveSelected(); m.mode != modeConfirmDelete {
		t.Fatalf("v on a live parent should ask first, mode = %v", m.mode)
	}
	m.handleConfirmKey(tea.KeyPressMsg{Code: 'k', Text: "k"})
	if m.confirm.keepChildren {
		t.Fatal("k should have turned the children on")
	}
	_, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)
	if m.errBar.text != "" {
		t.Fatalf("restart with children: %q", m.errBar.text)
	}
	if !m.tmux.Exists(parent.ID) {
		t.Fatal("the parent should be running again")
	}
	if !m.tmux.Exists(child.ID) {
		t.Fatal("the child was asked for and did not come back")
	}
}
