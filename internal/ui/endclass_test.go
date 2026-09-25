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

func TestNoEvidenceAtAllIsUnknownAndStillOffered(t *testing.T) {
	m := restoreModel(endRow("a"))
	candidates := m.restoreCandidates()
	if len(candidates) != 1 || m.restore.ends["a"].verdict != endUnknown {
		t.Fatalf("candidates=%v ends=%v", candidates, m.restore.ends)
	}
}

func TestTheCardSaysWhyAndCountsWhatItLeftOut(t *testing.T) {
	m := restoreModel(endRow("died"), endRow("unclear"), endRow("killed"))
	m.restore.candidates = m.classifyRestore(endEvidence{
		serverKnown: true, serverUp: true, serverStarted: launchedAt.Add(-time.Hour),
		exit: exitRecord(map[string]int{"died": 2, "unclear": 149}),
		ends: map[string]store.SessionEnd{"killed": {Reason: store.EndKilled, Launched: launchedAt}},
	})
	body := strings.Join(m.restoreBody(100), "\n")
	for _, want := range []string{"crashed (exit status 2)", "stopped (status 149)", "last seen", "1 more you ended yourself is not offered"} {
		if !strings.Contains(body, want) {
			t.Errorf("card body lacks %q:\n%s", want, body)
		}
	}
	if len(m.restore.candidates) != 2 {
		t.Fatalf("offered %d, want the died and unclear rows", len(m.restore.candidates))
	}
}
