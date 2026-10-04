package hooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/singleton"
)

// sessionStart is the registered SessionStart command of b.
func (b adoptedBoard) sessionStart(t *testing.T) string {
	t.Helper()
	groups := b.settings.Hooks[arrivalEvent]
	if len(groups) != 1 || len(groups[0].Hooks) != 1 {
		t.Fatalf("SessionStart carries %d groups, want one global entry", len(groups))
	}
	return groups[0].Hooks[0].Command
}

// A claude starting in a pane the running board has no marker for announces
// that pane, and nothing else: one file, named like a marker, holding what
// the board needs to find the pane and check it is the one that spoke. It is
// written with builtins alone -- the hook runs here with no PATH at all.
func TestSessionStartAnnouncesAnUnmarkedPaneWithBuiltinsAlone(t *testing.T) {
	b := newAdoptedBoard(t)
	cmd := exec.Command("/bin/sh", "-c", b.sessionStart(t))
	cmd.Env = []string{"PATH=/nonexistent", "TMUX=/tmp/tmux-1/work,4242,3", "TMUX_PANE=%8"}
	cmd.Stdin = strings.NewReader(`{"source":"startup"}`)
	out, err := cmd.CombinedOutput()
	if err != nil || len(out) != 0 {
		t.Fatalf("SessionStart exited %v with %q", err, out)
	}
	if _, err := os.Stat(b.calls); err == nil {
		t.Fatalf("an unmarked pane ran the binary: %s", readFile(t, b.calls))
	}
	raw := readFile(t, filepath.Join(NewManager(b.configDir).ArrivalsDir(), "4242%8"))
	arrival, ok := ParseArrival(raw)
	if !ok {
		t.Fatalf("the announcement %q does not parse", raw)
	}
	want := Arrival{Socket: "work", ServerPID: 4242, PaneID: "%8", AgentPID: os.Getpid()}
	if arrival != want {
		t.Fatalf("announced %+v, want %+v", arrival, want)
	}

	// Every other event stays silent in the same pane.
	noise, _ := b.fireAll(t, []string{"PATH=/usr/bin:/bin", "TMUX=/tmp/tmux-1/work,4242,3", "TMUX_PANE=%9"}, direct)
	if noise != "" {
		t.Fatalf("the hooks printed %q", noise)
	}
	entries, err := os.ReadDir(NewManager(b.configDir).ArrivalsDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("%d announcements after one SessionStart per pane, want 2", len(entries))
	}
}

func TestTakeArrivalsReadsRemovesAndWaitsOutAPartialWrite(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.PrepareArrivals(); err != nil {
		t.Fatal(err)
	}
	dir := m.ArrivalsDir()
	writeFile(t, filepath.Join(dir, "4242%1"), "/tmp/tmux-1/default,4242,0\n%1\n77\n")
	writeFile(t, filepath.Join(dir, "4242%2"), "/tmp/tmux-1/default,4242,0\n%2\n")
	writeFile(t, filepath.Join(dir, "4242%3"), "garbage")
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(filepath.Join(dir, "4242%3"), old, old); err != nil {
		t.Fatal(err)
	}

	got := m.TakeArrivals(time.Now())
	want := []Arrival{{Socket: "default", ServerPID: 4242, PaneID: "%1", AgentPID: 77}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("TakeArrivals() = %+v, want %+v", got, want)
	}
	left := dirEntries(t, dir)
	if strings.Join(left, ",") != "/4242%2" {
		t.Fatalf("left %v, want only the write still in progress", left)
	}
	if got := m.TakeArrivals(time.Now().Add(time.Minute)); len(got) != 0 {
		t.Fatalf("a write that never finished was read as %+v", got)
	}
	if left := dirEntries(t, dir); len(left) != 0 {
		t.Fatalf("an abandoned write was kept: %v", left)
	}
	if got := NewManager(t.TempDir()).TakeArrivals(time.Now()); got != nil {
		t.Fatalf("a board with no arrivals directory read %+v", got)
	}
}

func TestParseArrivalRefusesWhatTheHookWouldNotWrite(t *testing.T) {
	for _, raw := range []string{
		"",
		"/tmp/s,4242,0\n%1\n77",
		"/tmp/s,4242,0\n1\n77\n",
		"/tmp/s,x,0\n%1\n77\n",
		"/tmp/s,4242,0\n%1\n0\n",
		",4242,0\n%1\n77\n",
		"/tmp/s,4242,0\n%1\n77\nextra\n",
	} {
		if arrival, ok := ParseArrival(raw); ok {
			t.Errorf("ParseArrival(%q) = %+v, want a refusal", raw, arrival)
		}
	}
}

// The relay announces the way the hook does, for the same panes only.
func TestAnnounceMatchesTheHook(t *testing.T) {
	t.Setenv(EnvStatusFile, "")
	b := newAdoptedBoard(t)
	m := NewManager(b.configDir)
	const tmuxEnv = "/tmp/tmux-1/default,4242,0"
	if m.Announce(tmuxEnv, "%7", os.Getpid()) {
		t.Error("announced a pane that already has a marker")
	}
	if !m.Announce(tmuxEnv, "%8", os.Getpid()) {
		t.Fatal("did not announce an unmarked pane to a running board")
	}
	raw := readFile(t, filepath.Join(m.ArrivalsDir(), "4242%8"))
	if arrival, ok := ParseArrival(raw); !ok || arrival.AgentPID != os.Getpid() || arrival.PaneID != "%8" {
		t.Fatalf("announced %q", raw)
	}
	if m.Announce("", "%9", os.Getpid()) || m.Announce(tmuxEnv, "", os.Getpid()) {
		t.Error("announced from outside tmux")
	}
	t.Setenv(EnvStatusFile, filepath.Join(t.TempDir(), "x.status"))
	if m.Announce(tmuxEnv, "%9", os.Getpid()) {
		t.Error("announced a session the board launched")
	}
	t.Setenv(EnvStatusFile, "")
	writeFile(t, filepath.Join(b.configDir, singleton.FileName), strconv.Itoa(deadPID(t)))
	if m.Announce(tmuxEnv, "%9", os.Getpid()) {
		t.Error("announced to a board that is not running")
	}
}
