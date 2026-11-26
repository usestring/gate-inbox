package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/sessioncmd"
)

func TestRemoteSessionSelectionStaysOnItsHost(t *testing.T) {
	m := &Model{cfg: config.Config{Hosts: map[string]config.Host{
		"east": {SSH: "east"},
		"west": {SSH: "west"},
	}}}
	m.openRemoteSessions()
	snapshot := sessioncmd.HostSnapshot{
		Sessions: []sessioncmd.HostSession{{ID: "same-id", Name: "build", Status: "waiting", Running: true}},
		Matched:  1,
	}
	now := time.Now()
	m.applyRemoteSnapshot(remotePollResult{host: "west", snapshot: snapshot, at: now})
	m.applyRemoteSnapshot(remotePollResult{host: "east", snapshot: snapshot, at: now})
	if got := m.remote.selected; got != (remoteSessionKey{host: "west", id: "same-id"}) {
		t.Fatalf("selection moved to %+v when another host reported the same ID", got)
	}
	m.applyRemoteSnapshot(remotePollResult{host: "west", err: errors.New("connection refused")})
	if cmd := m.attachRemoteSession(); cmd != nil || !strings.Contains(m.errBar.text, "west") {
		t.Fatalf("selecting an offline host was accepted: %q", m.errBar.text)
	}
	if rows := m.remoteRows(); len(rows) != 4 || !strings.HasPrefix(rows[3].label, "cached · ") {
		t.Fatalf("offline host lost its cached row: %+v", rows)
	}
	m.handleRemoteSessionsKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if got := m.remote.selected.host; got != "east" {
		t.Fatalf("up selected %q, want east", got)
	}
	if cmd := m.attachRemoteSession(); cmd == nil {
		t.Fatal("live session on the reachable host could not be attached")
	}
	m.applyRemoteSnapshot(remotePollResult{host: "east", snapshot: sessioncmd.HostSnapshot{}, at: now})
	if cmd := m.attachRemoteSession(); cmd != nil || !strings.Contains(m.errBar.text, "no longer listed") {
		t.Fatalf("a removed row could be attached: %q", m.errBar.text)
	}
}

func TestRemoteBrowserOpensFromAnEmptyLocalInbox(t *testing.T) {
	m := &Model{
		mode: modeCleared,
		cfg:  config.Config{Hosts: map[string]config.Host{"worker": {SSH: "worker"}}},
	}

	m.handleKey(key("ctrl+k"))
	if m.mode != modeQuickActions {
		t.Fatalf("quick actions left the board in %v", m.mode)
	}
	m.actions.input.SetValue("remote sessions")
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.mode != modeRemoteSessions || !m.remote.hosts["worker"].pending {
		t.Fatalf("selecting remote sessions left the board in %v", m.mode)
	}
}

func TestRemoteBrowserMarksStoppedWorkerStatusesStaleAndKeepsAttach(t *testing.T) {
	m := &Model{cfg: config.Config{Hosts: map[string]config.Host{"worker": {SSH: "worker"}}}}
	m.openRemoteSessions()
	now := time.Now()
	snapshot := sessioncmd.HostSnapshot{
		Sessions:      []sessioncmd.HostSession{{ID: "build", Name: "build", Status: "waiting", Running: true}},
		Matched:       1,
		StatusesStale: new(true),
	}
	m.applyRemoteSnapshot(remotePollResult{host: "worker", snapshot: snapshot, at: now})
	rows := m.remoteRows()
	if len(rows) != 2 || rows[0].label != "statuses stale · worker · connected" ||
		rows[1].label != "stale status · worker · waiting · build" {
		t.Fatalf("stopped worker rows = %+v", rows)
	}
	if cmd := m.attachRemoteSession(); cmd == nil {
		t.Fatalf("a live pane should remain attachable: %q", m.errBar.text)
	}

	snapshot.StatusesStale = new(false)
	snapshot.Sessions[0].Status = "finished"
	m.applyRemoteSnapshot(remotePollResult{host: "worker", snapshot: snapshot, at: now})
	rows = m.remoteRows()
	if len(rows) != 2 || rows[0].label != "worker · connected" || rows[1].label != "worker · finished · build" {
		t.Fatalf("restarted worker rows = %+v", rows)
	}
}

func TestRemoteBrowserShowsStaleStatusInNarrowTerminal(t *testing.T) {
	const worker = "ubuntu-development-arm64"
	m := &Model{
		cfg:    config.Config{Hosts: map[string]config.Host{worker: {SSH: worker}}},
		width:  40,
		height: 16,
	}
	m.openRemoteSessions()
	m.applyRemoteSnapshot(remotePollResult{
		host: worker,
		snapshot: sessioncmd.HostSnapshot{
			Sessions:      []sessioncmd.HostSession{{ID: "build", Status: "waiting", Running: true}},
			Matched:       1,
			StatusesStale: new(true),
		},
		at: time.Now(),
	})
	if view := m.viewRemoteSessions(); !strings.Contains(view, "statuses stale") || !strings.Contains(view, "stale status") {
		t.Fatalf("narrow remote browser hid the warnings:\n%s", view)
	}
}

func TestRemoteBrowserCanAttachWhenWorkerDoesNotReportStatusAge(t *testing.T) {
	m := &Model{cfg: config.Config{Hosts: map[string]config.Host{"worker": {SSH: "worker"}}}}
	m.openRemoteSessions()
	m.applyRemoteSnapshot(remotePollResult{
		host: "worker",
		snapshot: sessioncmd.HostSnapshot{
			Sessions: []sessioncmd.HostSession{{ID: "build", Name: "build", Status: "waiting", Running: true}},
			Matched:  1,
		},
		at: time.Now(),
	})
	rows := m.remoteRows()
	if len(rows) != 2 || rows[0].label != "status age unknown · worker · connected" ||
		rows[1].label != "unverified · worker · waiting · build" {
		t.Fatalf("older worker rows = %+v", rows)
	}
	if cmd := m.attachRemoteSession(); cmd == nil {
		t.Fatalf("a live pane should remain attachable: %q", m.errBar.text)
	}
}
