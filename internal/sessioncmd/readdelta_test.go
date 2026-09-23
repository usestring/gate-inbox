package sessioncmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// A cursor carries the conversation it came from, so one taken before a
// revive cannot be read as a position in the conversation that replaced it.
// The offsets would both parse; only the id says they mean different files.
func TestCursorIsRefusedForAnotherConversation(t *testing.T) {
	cursor := encodeCursor("conv-a", 4096)
	if offset, ok := decodeCursor(cursor, "conv-a"); !ok || offset != 4096 {
		t.Fatalf("decodeCursor(own) = %d, %v", offset, ok)
	}
	if _, ok := decodeCursor(cursor, "conv-b"); ok {
		t.Error("a cursor from another conversation was accepted")
	}
	for _, bad := range []string{"", "4096", "conv-a@", "conv-a@-1", "conv-a@many"} {
		if _, ok := decodeCursor(bad, "conv-a"); ok {
			t.Errorf("decodeCursor(%q) accepted", bad)
		}
	}
	if encodeCursor("", 10) != "" {
		t.Error("a session with no conversation must not be handed a cursor")
	}
}

// seedTranscript gives a row a conversation on disk under the harness's own
// claude home, which is what readDelta resolves a transcript through.
func seedTranscript(t *testing.T, h *sessionHarness, tool, body string) store.Session {
	t.Helper()
	claudeHome := t.TempDir()
	h.sessions.claudeHome = claudeHome
	sess := store.Session{
		ID:             uuid.NewString()[:8],
		Name:           "worker",
		Tool:           tool,
		Cwd:            h.caller.Cwd,
		Group:          h.caller.Group,
		Status:         status.Working,
		AgentSessionID: "conv-delta",
	}
	if err := h.store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetSnapshot(sess.ID, "the whole pane, every line of it"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(claudeHome, "projects", "any-project", sess.AgentSessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return sess
}

func assistantRecord(text string) string {
	return `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"` + text + `"}]}}` + "\n"
}

// The default has to stay exactly what it was, because every caller that
// exists today passes nothing.
func TestReadWithNoCursorStillReturnsTheWholePane(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	sess := seedTranscript(t, h, "claude", assistantRecord("opened the PR"))

	screen, err := h.sessions.Read(h.caller.ID, sess.ID, "")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if screen.Mode != "pane" || screen.Output != "the whole pane, every line of it" {
		t.Fatalf("mode=%q output=%q, want the pane unchanged", screen.Mode, screen.Output)
	}
	// And it comes back with the two things that make the next read cheap.
	if screen.Cursor == "" {
		t.Error("a first read handed back no cursor, so a second read cannot be a delta")
	}
	if screen.Digest.Result != "opened the PR" {
		t.Errorf("digest result = %q, want the session's last words", screen.Digest.Result)
	}
}

// The unit: a repeat read carries the gap, not the screen.
func TestReadWithACursorReturnsOnlyWhatTheSessionAddedSince(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	sess := seedTranscript(t, h, "claude", assistantRecord("starting on the parser"))

	first, err := h.sessions.Read(h.caller.ID, sess.ID, "")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	path := filepath.Join(h.sessions.claudeHome, "projects", "any-project", sess.AgentSessionID+".jsonl")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(assistantRecord("parser done, tests green")); err != nil {
		t.Fatal(err)
	}
	file.Close()

	second, err := h.sessions.Read(h.caller.ID, sess.ID, first.Cursor)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if second.Mode != "delta" {
		t.Fatalf("mode = %q, want delta", second.Mode)
	}
	if !strings.Contains(second.Output, "parser done, tests green") {
		t.Fatalf("output = %q, want the new turn", second.Output)
	}
	if strings.Contains(second.Output, "starting on the parser") {
		t.Error("the delta replayed a turn the caller had already read")
	}
	if strings.Contains(second.Output, "the whole pane") {
		t.Error("the delta carried the pane as well")
	}
	if second.Degraded != "" {
		t.Errorf("a readable transcript reported degraded=%q", second.Degraded)
	}
	// The saving this exists for: the gap is smaller than the screen.
	if len(second.Output) >= len(first.Output) {
		t.Errorf("delta (%d bytes) was not cheaper than the pane (%d bytes)", len(second.Output), len(first.Output))
	}
}

// A child that has said nothing costs a few bytes rather than a whole pane,
// and says so rather than looking like an empty screen.
func TestReadWithACursorIsNearlyFreeWhenNothingHappened(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	sess := seedTranscript(t, h, "claude", assistantRecord("still working"))

	first, err := h.sessions.Read(h.caller.ID, sess.ID, "")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	second, err := h.sessions.Read(h.caller.ID, sess.ID, first.Cursor)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if second.Mode != "delta" || second.Output != "" {
		t.Fatalf("mode=%q output=%q, want an empty delta", second.Mode, second.Output)
	}
	if !strings.Contains(FormatSessionScreen(second), "nothing new since your last read") {
		t.Errorf("an empty delta read as an empty screen: %q", FormatSessionScreen(second))
	}
	if second.Digest.Status == "" {
		t.Error("an empty delta still has to say what the session is doing")
	}
}

// Transcript saving has been off for child sessions in some configurations,
// which is why a missing transcript degrades rather than fails: a caller
// that asked for a delta must not lose the screen it used to get.
func TestReadDegradesToThePaneWhenThereIsNoTranscript(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	sess := seedTranscript(t, h, "claude", assistantRecord("ignored"))
	if err := os.RemoveAll(filepath.Join(h.sessions.claudeHome, "projects")); err != nil {
		t.Fatal(err)
	}

	screen, err := h.sessions.Read(h.caller.ID, sess.ID, "conv-delta@100")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if screen.Mode != "pane" || screen.Output != "the whole pane, every line of it" {
		t.Fatalf("mode=%q output=%q, want the pane back", screen.Mode, screen.Output)
	}
	if !strings.Contains(screen.Degraded, "transcript saving is off") {
		t.Errorf("degraded = %q, want it to name the reason", screen.Degraded)
	}
}

// A tool this cannot read a conversation for keeps the behaviour it had.
func TestReadDegradesToThePaneForAToolWithNoReadableTranscript(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	sess := seedTranscript(t, h, "echoer", assistantRecord("ignored"))

	screen, err := h.sessions.Read(h.caller.ID, sess.ID, "conv-delta@0")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if screen.Mode != "pane" {
		t.Fatalf("mode = %q, want pane", screen.Mode)
	}
	if !strings.Contains(screen.Degraded, "echoer") {
		t.Errorf("degraded = %q, want it to name the tool", screen.Degraded)
	}
	if screen.Cursor != "" {
		t.Errorf("cursor = %q, want none for a session with no readable conversation", screen.Cursor)
	}
}

// A cursor from the conversation a revive left behind is stale, not broken:
// the caller gets a read and a cursor that fits, and is told why.
func TestReadStartsFreshOnACursorFromAnotherConversation(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	sess := seedTranscript(t, h, "claude", assistantRecord("resumed on a new conversation"))

	screen, err := h.sessions.Read(h.caller.ID, sess.ID, encodeCursor("conv-retired", 900))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	// It still answers as a delta -- the caller asked for one and the
	// conversation is readable -- but as a fresh one, and says so.
	if screen.Mode != "delta" {
		t.Fatalf("mode = %q, want a fresh delta", screen.Mode)
	}
	if !strings.Contains(screen.Output, "resumed on a new conversation") {
		t.Errorf("output = %q, want the current conversation read from its end", screen.Output)
	}
	if !strings.Contains(screen.Degraded, "different conversation") {
		t.Errorf("degraded = %q", screen.Degraded)
	}
	if _, ok := decodeCursor(screen.Cursor, sess.AgentSessionID); !ok {
		t.Errorf("cursor = %q, want one the next read can use", screen.Cursor)
	}
}

// A dead session's stored snapshot is the screen it died on. Reading the
// status rules over it would report whatever it was doing at the time --
// "working" for a session that stopped, or a question nobody can answer any
// more -- so the digest takes its status off the row instead.
func TestDigestDoesNotReadStatusOffADeadSessionsSnapshot(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	sess := seedTranscript(t, h, "claude", assistantRecord("finished and stopped"))
	if err := h.store.UpdateStatus(sess.ID, status.Dead); err != nil {
		t.Fatal(err)
	}

	screen, err := h.sessions.Read(h.caller.ID, sess.ID, "")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if screen.Session.Running {
		t.Fatal("the harness left the session running, so this proves nothing")
	}
	if screen.Digest.Status != status.Dead {
		t.Errorf("digest status = %q, want the row's %q rather than a reading of a stale screen", screen.Digest.Status, status.Dead)
	}
	if screen.Digest.Question != "" {
		t.Errorf("digest offered a question on a dead session: %q", screen.Digest.Question)
	}
	// The transcript outlives the pane, so the last thing it said still reads.
	if screen.Digest.Result != "finished and stopped" {
		t.Errorf("digest result = %q, want the last turn from the transcript", screen.Digest.Result)
	}
}
