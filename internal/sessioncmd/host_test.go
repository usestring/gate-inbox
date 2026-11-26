package sessioncmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/store"
)

func TestHostSnapshotReportsLiveWorkerSessions(t *testing.T) {
	h := newSessionHarness(t)
	snapshot, err := h.sessions.HostSessions()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Matched != 1 || len(snapshot.Sessions) != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if row := snapshot.Sessions[0]; row.ID != h.caller.ID || row.Name != h.caller.Name || !row.Running {
		t.Fatalf("worker session = %+v, want live %s", row, h.caller.ID)
	}
	if err := h.sessions.AttachHost("unknown"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("attach unknown session = %v", err)
	}
	if err := h.driver.Kill(h.caller.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.sessions.AttachHost(h.caller.ID); err == nil || !strings.Contains(err.Error(), "no live pane") {
		t.Fatalf("attach stopped session = %v", err)
	}
}

func TestHostSnapshotRequiresAnExistingBoard(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewSessions(dir, CLIVocabulary()).HostSessions(); !os.IsNotExist(err) {
		t.Fatalf("snapshot of missing board = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.toml")); !os.IsNotExist(err) {
		t.Fatalf("config must not be created by a snapshot: %v", err)
	}
}

func TestHostSnapshotMarksStatusesStaleWhenTheWorkerStopsPolling(t *testing.T) {
	h := newSessionHarnessWithConfig(t, "poll_interval = \"20s\"\n"+sessionConfig)
	if err := h.store.UpdateStatus(h.caller.ID, "waiting"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, tc := range []struct {
		name      string
		heartbeat string
		stale     bool
	}{
		{"missing heartbeat", "", true},
		{"polling at a custom interval", strconv.FormatInt(now.Add(-40*time.Second).UnixNano(), 10), false},
		{"stopped polling", strconv.FormatInt(now.Add(-time.Hour).UnixNano(), 10), true},
		{"unreadable heartbeat", "bad", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.heartbeat != "" {
				if err := h.store.SetSetting(store.PollerHeartbeatKey, tc.heartbeat); err != nil {
					t.Fatal(err)
				}
			}
			snapshot, err := h.sessions.HostSessions()
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Sessions) != 1 || snapshot.Sessions[0].Status != "waiting" || !snapshot.Sessions[0].Running {
				t.Fatalf("snapshot lost the last status or live pane: %+v", snapshot)
			}
			wire, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			var received struct {
				StatusesStale *bool `json:"statuses_stale"`
			}
			if err := json.Unmarshal(wire, &received); err != nil {
				t.Fatal(err)
			}
			if received.StatusesStale == nil || *received.StatusesStale != tc.stale {
				t.Fatalf("want statuses_stale = %v in %s", tc.stale, wire)
			}
		})
	}
}
