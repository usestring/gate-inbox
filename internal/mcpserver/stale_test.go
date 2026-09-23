package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/managerbuild"
	"github.com/usestring/gate-inbox/internal/sessioncmd"
)

func boardBuild(t *testing.T, dir, digest string, since time.Time) {
	t.Helper()
	line := digest + " " + since.UTC().Format(time.RFC3339) + "\n"
	if err := os.WriteFile(filepath.Join(dir, managerbuild.FileName), []byte(line), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestStaleNoticeNamesTheGapAndTheFix(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	boardBuild(t, dir, strings.Repeat("a", 64), now.Add(-6*24*time.Hour))

	notice := staleNotice(dir, now)

	if notice == "" {
		t.Fatal("staleNotice was empty against a different board build")
	}
	if !strings.Contains(notice, "6 days") {
		t.Fatalf("notice does not say how far behind: %q", notice)
	}
	if !strings.Contains(notice, "/mcp") {
		t.Fatalf("notice does not name the fix the person has to make: %q", notice)
	}
	// It is prepended to the session list, so it has to end clear of it.
	if !strings.HasSuffix(notice, "\n\n") {
		t.Fatalf("notice does not separate itself from the list: %q", notice)
	}
}

// The common case by far, and it must add nothing at all: this text is
// prepended to every list_sessions result.
func TestStaleNoticeIsEmptyOnTheBoardsOwnBuild(t *testing.T) {
	dir := t.TempDir()
	boardBuild(t, dir, managerbuild.Fingerprint(), time.Now().Add(-72*time.Hour))

	if notice := staleNotice(dir, time.Now()); notice != "" {
		t.Fatalf("staleNotice = %q, want nothing for the current build", notice)
	}
}

func TestStaleNoticeIsEmptyWithoutABoardRecord(t *testing.T) {
	if notice := staleNotice(t.TempDir(), time.Now()); notice != "" {
		t.Fatalf("staleNotice = %q, want nothing when the board has not recorded a build", notice)
	}
}

func TestHumanDurationRoundsToSomethingActionable(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want string
	}{
		{30 * time.Second, "under a minute"},
		{time.Minute, "1 minute"},
		{25 * time.Minute, "25 minutes"},
		{time.Hour, "1 hour"},
		{5 * time.Hour, "5 hours"},
		{24 * time.Hour, "1 day"},
		{6*24*time.Hour + 3*time.Hour, "6 days"},
	} {
		if got := humanDuration(tc.in); got != tc.want {
			t.Errorf("humanDuration(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The wiring, through the real server: a notice that is computed but never
// reaches the tool result is the same as no notice at all.
func TestListSessionsCarriesTheStaleNotice(t *testing.T) {
	dir := t.TempDir()
	boardBuild(t, dir, strings.Repeat("e", 64), time.Now().Add(-6*24*time.Hour))
	fake := &fakeSessionCommands{listed: []sessioncmd.Session{{ID: "abc123", Name: "a-session"}}}
	session := connectServer(t, newServer(dir, "abc123", "test", &fakeTerminalCommands{}, fake))

	text, _ := callText(t, session, "list_sessions", map[string]any{})
	if !strings.HasPrefix(text, "[gate-inbox]") {
		t.Fatalf("list_sessions text does not lead with the notice: %q", text)
	}
	if !strings.Contains(text, "a-session") {
		t.Fatalf("list_sessions text lost the session list: %q", text)
	}
}

// And it stays out of the way on the build the board is actually running,
// which is every session most of the time.
func TestListSessionsIsUnchangedOnTheCurrentBuild(t *testing.T) {
	dir := t.TempDir()
	boardBuild(t, dir, managerbuild.Fingerprint(), time.Now().Add(-6*24*time.Hour))
	fake := &fakeSessionCommands{listed: []sessioncmd.Session{{ID: "abc123", Name: "a-session"}}}
	session := connectServer(t, newServer(dir, "abc123", "test", &fakeTerminalCommands{}, fake))

	if text, _ := callText(t, session, "list_sessions", map[string]any{}); strings.Contains(text, "[gate-inbox]") {
		t.Fatalf("list_sessions carried a notice on the current build: %q", text)
	}
}
