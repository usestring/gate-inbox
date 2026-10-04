package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// endRow is a dead managed row whose agent launched at launchedAt.
func endRow(id string) store.Session {
	return store.Session{ID: id, Name: id, Tool: "claude", Cwd: "/repo", Status: status.Dead,
		AgentLaunchedAt: launchedAt, LastStatusAt: launchedAt.Add(time.Hour)}
}

// exitRecord is a launch script's record, written after the launch.
func exitRecord(codes map[string]int) func(string) (int, time.Time, bool) {
	return func(id string) (int, time.Time, bool) {
		code, ok := codes[id]
		return code, launchedAt.Add(time.Minute), ok
	}
}

// sameServer is a tmux server that was already up when the row launched and
// still is.
func sameServer() endEvidence {
	return endEvidence{serverKnown: true, serverUp: true, serverStarted: launchedAt.Add(-time.Hour)}
}

func TestEveryDeliberateEndIsNeverOffered(t *testing.T) {
	ev := sameServer()
	// A reboot since, so only the recorded intent can keep these out.
	ev.serverStarted = launchedAt.Add(24 * time.Hour)
	ev.ends = map[string]store.SessionEnd{
		"board-kill": {Reason: store.EndKilled, Launched: launchedAt},
		"cli-kill":   {Reason: store.EndKilled, Launched: launchedAt},
		"mcp-kill":   {Reason: store.EndKilled, Launched: launchedAt},
		"archived":   {Reason: store.EndArchived, Launched: launchedAt},
		"parked":     {Reason: store.EndParked, Launched: launchedAt},
	}
	for id, end := range ev.ends {
		got := classifyEnd(endRow(id), ev)
		if got.verdict != endByOperator {
			t.Errorf("%s (%s): verdict %v (%s), want ended by operator", id, end.Reason, got.verdict, got.why)
		}
	}
}

func TestAnEndMarkForAnEarlierLaunchDoesNotCoverTheNextLoss(t *testing.T) {
	// Killed, revived by hand, then lost in a reboot: the kill was about
	// the first agent.
	ev := endEvidence{serverKnown: true, serverUp: true, serverStarted: launchedAt.Add(48 * time.Hour),
		ends: map[string]store.SessionEnd{"a": {Reason: store.EndKilled, Launched: launchedAt.Add(-time.Hour)}}}
	if got := classifyEnd(endRow("a"), ev); got.verdict != endDied {
		t.Fatalf("verdict %v (%s), want died", got.verdict, got.why)
	}
}

func TestAParkedRowIsNotOfferedEvenWithoutAnEndMark(t *testing.T) {
	ev := sameServer()
	ev.parked = map[string]bool{"a": true}
	if got := classifyEnd(endRow("a"), ev); got.verdict != endByOperator || !strings.Contains(got.why, "unpark") {
		t.Fatalf("got %v (%s)", got.verdict, got.why)
	}
}

func TestTheAgentsOwnExitStatusSeparatesQuitFromCrash(t *testing.T) {
	cases := []struct {
		code int
		want endVerdict
		why  string
	}{
		{0, endByOperator, "exited normally"},
		{130, endByOperator, "ctrl+c"},
		{1, endDied, "crashed (exit status 1)"},
		{137, endDied, "signal 9"},
		{148, endUnknown, "stopped"},
	}
	for _, tc := range cases {
		ev := sameServer()
		ev.exit = exitRecord(map[string]int{"a": tc.code})
		got := classifyEnd(endRow("a"), ev)
		if got.verdict != tc.want || !strings.Contains(got.why, tc.why) {
			t.Errorf("exit %d: got %v (%s), want %v mentioning %q", tc.code, got.verdict, got.why, tc.want, tc.why)
		}
	}
}

func TestAnExitRecordFromThePreviousAgentIsIgnored(t *testing.T) {
	ev := sameServer()
	ev.exit = func(string) (int, time.Time, bool) { return 0, launchedAt.Add(-time.Hour), true }
	ev.serverStarted = launchedAt.Add(time.Hour)
	if got := classifyEnd(endRow("a"), ev); got.verdict != endDied {
		t.Fatalf("a stale clean exit must not hide a reboot: %v (%s)", got.verdict, got.why)
	}
}

func TestAPaneClosedInTmuxWhileTheServerStayedUpIsTheOperators(t *testing.T) {
	if got := classifyEnd(endRow("a"), sameServer()); got.verdict != endByOperator || !strings.Contains(got.why, "closed in tmux") {
		t.Fatalf("kill-pane: got %v (%s)", got.verdict, got.why)
	}
}

func TestARestartedServerMeansTheSessionDied(t *testing.T) {
	ev := sameServer()
	ev.serverStarted = launchedAt.Add(3 * time.Hour)
	if got := classifyEnd(endRow("a"), ev); got.verdict != endDied || !strings.Contains(got.why, "rebooted") {
		t.Fatalf("server restart: got %v (%s)", got.verdict, got.why)
	}
}

func TestNoServerAtAllMeansTheSessionDied(t *testing.T) {
	// A reboot with the board opened before anything else: no tmux server
	// yet, and rows still reading as they did before it.
	ev := endEvidence{serverKnown: true}
	if got := classifyEnd(endRow("a"), ev); got.verdict != endDied {
		t.Fatalf("no server: got %v (%s)", got.verdict, got.why)
	}
}

func TestALaunchThatStartedTheServerIsOnThatServer(t *testing.T) {
	ev := sameServer()
	// The server's whole-second stamp lands just after the launch that
	// started it.
	ev.serverStarted = launchedAt.Add(time.Second).Truncate(time.Second)
	if got := classifyEnd(endRow("a"), ev); got.verdict != endByOperator {
		t.Fatalf("got %v (%s), want the pane read as closed on its own server", got.verdict, got.why)
	}
}

func TestNoEvidenceAtAllIsUnknownAndStillMarked(t *testing.T) {
	m := restoreModel(endRow("a"))
	candidates, ends := m.classifyDeadRows(endEvidence{})
	if len(candidates) != 1 || ends["a"].verdict != endUnknown {
		t.Fatalf("candidates=%v ends=%v", candidates, ends)
	}
}

func TestClassifyingKeepsTheDiedAndUnclearRowsAndLeavesOutTheKilled(t *testing.T) {
	m := restoreModel(endRow("died"), endRow("unclear"), endRow("killed"))
	candidates, ends := m.classifyDeadRows(endEvidence{
		serverKnown: true, serverUp: true, serverStarted: launchedAt.Add(-time.Hour),
		exit: exitRecord(map[string]int{"died": 2, "unclear": 149}),
		ends: map[string]store.SessionEnd{"killed": {Reason: store.EndKilled, Launched: launchedAt}},
	})
	if len(candidates) != 2 || candidates[0].ID != "died" || candidates[1].ID != "unclear" {
		t.Fatalf("kept %v, want the died and unclear rows", candidates)
	}
	if !strings.Contains(ends["died"].why, "crashed (exit status 2)") || ends["killed"].verdict != endByOperator {
		t.Fatalf("ends = %v", ends)
	}
}
