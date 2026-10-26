package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/agentsession"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

const staleShellPane = `● The probe is done.

✻ Baked for 26s · done 15:14 · 1 shell still running
──────────────────────────────────────────────────────────────
❯
──────────────────────────────────────────────────────────────
  ⏵⏵ auto mode on · 1 shell
`

func transcriptAged(t *testing.T, sess store.Session, age time.Duration, now time.Time) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	path, err := agentsession.ClaudeTranscriptPath(sess.Cwd, sess.AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, now.Add(-age), now.Add(-age)); err != nil {
		t.Fatal(err)
	}
}

// Live 2026-10-12: a child whose turn ended six days earlier with a shell
// tail read working the whole time, held off the child sweep, and held its
// parent delegating.
func TestSettleBackgroundRestsAQuietTail(t *testing.T) {
	now := time.Now()
	sess := store.Session{ID: "child001", Tool: "claude", Cwd: "/work/repo", AgentSessionID: "conv-1"}
	p := &poller{engine: liveEngine(t)}

	transcriptAged(t, sess, 129*time.Hour, now)
	if got := p.settleBackground(sess, staleShellPane, status.Working, now); got != status.Finished {
		t.Fatalf("quiet for 129h: got %q, want finished", got)
	}
	sess.Acked = true
	if got := p.settleBackground(sess, staleShellPane, status.Working, now); got != status.Idle {
		t.Fatalf("acknowledged and quiet: got %q, want idle", got)
	}
}

// Live 2026-10-13: a child stopped for a stale sandbox with a local server
// still running in a shell. Claude Code touched the transcript hourly
// with no entry in it, the tail never read as quiet, and the row stayed
// working: no rest relay to the parent and no sandbox restart.
func TestSettleBackgroundIgnoresATouchWithNoEntry(t *testing.T) {
	now := time.Now()
	sess := store.Session{ID: "child001", Tool: "claude", Cwd: "/work/repo", AgentSessionID: "conv-1"}
	p := &poller{engine: liveEngine(t)}

	t.Setenv("HOME", t.TempDir())
	path, err := agentsession.ClaudeTranscriptPath(sess.Cwd, sess.AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	ended := now.Add(-5 * time.Hour).UTC().Format(time.RFC3339Nano)
	transcript := `{"type":"assistant","timestamp":"` + ended + `"}
{"type":"ai-title","aiTitle":"probe","sessionId":"conv-1"}
{"type":"system","subtype":"turn_duration","timestamp":"` + ended + `"}
{"type":"permission-mode","permissionMode":"auto","sessionId":"conv-1"}
`
	if err := os.WriteFile(path, []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}
	touched := now.Add(-time.Minute)
	if err := os.Chtimes(path, touched, touched); err != nil {
		t.Fatal(err)
	}
	if got := p.settleBackground(sess, staleShellPane, status.Working, now); got != status.Finished {
		t.Fatalf("last entry 5h old, file touched 1m ago: got %q, want finished", got)
	}
}

// A turn that ended on a live shell tail is at rest with work still running
// behind it, which is background rather than working.
func TestSettleBackgroundReadsALiveTailAsBackground(t *testing.T) {
	now := time.Now()
	sess := store.Session{ID: "child001", Tool: "claude", Cwd: "/work/repo", AgentSessionID: "conv-1"}
	p := &poller{engine: liveEngine(t)}

	transcriptAged(t, sess, 10*time.Minute, now)
	if got := p.settleBackground(sess, staleShellPane, status.Working, now); got != status.Background {
		t.Fatalf("written 10m ago: got %q, want background", got)
	}
	noTranscript := sess
	noTranscript.AgentSessionID = ""
	if got := p.settleBackground(noTranscript, staleShellPane, status.Working, now); got != status.Background {
		t.Fatalf("no transcript to read: got %q, want background", got)
	}
}
