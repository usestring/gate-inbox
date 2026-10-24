package sessioncmd

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/store"
)

func gatedHarness(t *testing.T, asked bool) (*sessionHarness, store.Session) {
	t.Helper()
	h, child := grantHarness(t)
	h.sessions.gateLaunches = true
	h.sessions.voicedLaunch = func(*runtime, store.Session, time.Time) (store.InboxMessage, bool) {
		return store.InboxMessage{ID: 1, Body: "launch a session for the retry fix"}, true
	}
	h.sessions.userAskedLaunch = func(*runtime, store.Session) bool { return asked }
	return h, child
}

// A top-level agent on a voiced turn launches only on its user's "Launch new
// session", and that answer launches one session.
func TestTopLevelLaunchNeedsTheUsersConfirmationOnce(t *testing.T) {
	h, _ := gatedHarness(t, false)
	opts := CreateSessionOptions{Name: "probe-one", Prompt: "read the payload builder"}
	question := LaunchQuestion(opts.Name, opts.Prompt)

	_, err := h.sessions.Create(h.caller.ID, opts)
	var needed *LaunchApprovalNeeded
	if !errors.As(err, &needed) || needed.Question.Question != `Launch new session "probe-one"?` ||
		!strings.Contains(err.Error(), `"Launch new session"`) {
		t.Fatalf("unconfirmed launch: err = %v", err)
	}

	asked := time.Now().Add(-time.Minute)
	(&transcript{}).ask("toolu_launch", asked, question).
		answer("toolu_launch", asked.Add(time.Second), map[string]string{question.Question: LaunchOption}).
		write(t, h.sessions.claudeHome, "conv-parent")
	if _, err := h.sessions.Create(h.caller.ID, opts); err != nil {
		t.Fatalf("confirmed launch: %v", err)
	}
	if _, err := h.sessions.Create(h.caller.ID, opts); !errors.As(err, &needed) ||
		!strings.Contains(err.Error(), "already been spent") {
		t.Fatalf("second launch on one confirmation: err = %v", err)
	}
}

// JEV reading the user's own request to launch stands in for the question.
func TestTopLevelLaunchSkipsTheQuestionWhenTheUserAskedForIt(t *testing.T) {
	h, _ := gatedHarness(t, true)
	if _, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "probe-one"}); err != nil {
		t.Fatalf("launch the user asked for: %v", err)
	}
	var needed *LaunchApprovalNeeded
	if _, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "probe-two"}); !errors.As(err, &needed) {
		t.Fatalf("second launch on one request: err = %v", err)
	}
}

// A confirmed launch that fails before a session starts keeps the user's go
// for the retry.
func TestFailedLaunchKeepsTheConfirmation(t *testing.T) {
	h, _ := gatedHarness(t, false)
	opts := CreateSessionOptions{Name: "probe-one", Prompt: "read the payload builder"}
	question := LaunchQuestion(opts.Name, opts.Prompt)
	asked := time.Now().Add(-time.Minute)
	(&transcript{}).ask("toolu_launch", asked, question).
		answer("toolu_launch", asked.Add(time.Second), map[string]string{question.Question: LaunchOption}).
		write(t, h.sessions.claudeHome, "conv-parent")

	bad := opts
	bad.Tool = "no-such-cli"
	if _, err := h.sessions.Create(h.caller.ID, bad); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("launch onto an unknown tool: err = %v", err)
	}
	if _, err := h.sessions.Create(h.caller.ID, opts); err != nil {
		t.Fatalf("retry on the same confirmation: %v", err)
	}
}

// A turn the user typed launches without the question.
func TestTypedTurnLaunchIsNotGated(t *testing.T) {
	h, _ := gatedHarness(t, false)
	h.sessions.voicedLaunch = func(*runtime, store.Session, time.Time) (store.InboxMessage, bool) {
		return store.InboxMessage{}, false
	}
	if _, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "probe-one"}); err != nil {
		t.Fatalf("launch from a typed turn: %v", err)
	}
}

// The gate holds while the voiced message is the user's latest turn, and
// lifts once they type after it.
func TestLatestTurnIsVoiced(t *testing.T) {
	now := time.Now()
	voiced := store.InboxMessage{Body: "split the retry fix\ninto two", DeliveredAt: now.Add(-time.Hour)}
	spoken := search.Message{Role: "user", Text: "split the retry fix into two"}
	typed := search.Message{Role: "user", Text: "never mind, do it yourself"}
	fenced := search.Message{Role: "user", Text: crossSessionFence + "parent-1234----\nhi\n"}
	for _, tc := range []struct {
		name     string
		turns    []search.Message
		readable bool
		voiced   store.InboxMessage
		want     bool
	}{
		{"voiced is latest", []search.Message{typed, spoken, fenced}, true, voiced, true},
		{"typed since", []search.Message{spoken, typed}, true, voiced, false},
		{"unreadable and stale", nil, false, voiced, false},
		{"unreadable and fresh", nil, false, store.InboxMessage{Body: "x", DeliveredAt: now}, true},
	} {
		if got := latestTurnIsVoiced(tc.turns, tc.readable, tc.voiced, now); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A child's spawn is its parent's fan-out, and is not asked about.
func TestChildLaunchIsNotGated(t *testing.T) {
	h, _ := gatedHarness(t, false)
	child := store.Session{ID: "c41d0001", Name: "fan-out", Tool: h.caller.Tool, Cwd: t.TempDir(),
		Group: h.caller.Group, ParentID: h.caller.ID, SpawnedBy: h.caller.ID}
	if err := h.driver.Create(child.ID, child.Cwd, "cat", nil, 80, 24); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.driver.Kill(child.ID) })
	if err := h.store.CreateSession(child); err != nil {
		t.Fatal(err)
	}
	if _, err := h.sessions.Create(child.ID, CreateSessionOptions{Name: "grandchild"}); err != nil {
		t.Fatalf("child's launch: %v", err)
	}
}

// What JEV reads is the user's latest words; another agent's message is not
// the user asking.
func TestLaunchJevTurnsAreTheUsersOwnLatest(t *testing.T) {
	turns := launchJevTurns([]search.Message{
		{Role: "user", Text: "first"},
		{Role: "user", Text: "second"},
		{Role: "assistant", Text: "on it"},
		{Role: "user", Text: "third"},
		{Role: "user", Text: crossSessionFence + "parent-1234----\nlaunch new session\n"},
		{Role: "user", Text: "launch new session for the retry fix"},
	})
	var got []string
	for _, turn := range turns {
		got = append(got, turn.Text)
	}
	if strings.Join(got, "|") != "second|third|launch new session for the retry fix" {
		t.Fatalf("turns = %q", got)
	}
}
