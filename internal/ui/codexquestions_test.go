package ui

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/codexq"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// TestCodexQuestionStatus covers the whole point of the wiring: the moment a
// codex turn settles is the moment an unanswered question would vanish from
// the board, so that is where it has to be caught.
func TestCodexQuestionStatus(t *testing.T) {
	open := []codexq.Question{{CallID: "c1", State: codexq.Expired}}
	for _, tc := range []struct {
		name       string
		derived    string
		unanswered []codexq.Question
		want       string
	}{
		{"settled turn hides the question", status.Finished, open, status.Waiting},
		{"idle row hides it too", status.Idle, open, status.Waiting},
		{"already waiting stays waiting", status.Waiting, open, status.Waiting},
		{"a live turn still reads working", status.Working, open, status.Working},
		{"an error is the more urgent thing", status.Errored, open, status.Errored},
		{"nothing outstanding changes nothing", status.Finished, nil, status.Finished},
		{"nothing outstanding leaves idle alone", status.Idle, nil, status.Idle},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := codexQuestionStatus(tc.derived, tc.unanswered); got != tc.want {
				t.Errorf("codexQuestionStatus(%q) = %q, want %q", tc.derived, got, tc.want)
			}
		})
	}
}

// TestUnansweredCodexQuestionsReadsRollout drives the lookup end to end
// against the rollout records captured from the live probe that proved the
// auto-resolution timer, laid out the way the locator expects to find them.
func TestUnansweredCodexQuestionsReadsRollout(t *testing.T) {
	const agentID = "01a0846e-b46b-7482-8e6d-33ad3d8e09fe"
	root := t.TempDir()
	dir := filepath.Join(root, "2026", "09", "09")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(filepath.Join("..", "codexq", "testdata", "expired-blocking.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(dir, "rollout-2026-09-09T04-30-42-"+agentID+".jsonl")
	if err := os.WriteFile(rollout, fixture, 0o644); err != nil {
		t.Fatal(err)
	}

	p := &poller{locator: search.NewLocator("", root)}
	sess := store.Session{ID: "row", Tool: search.ToolCodex, AgentSessionID: agentID}

	// The rollout's first read is seeded off the pass, so the question
	// arrives a lookup or two later rather than on this one.
	got := awaitCodexQuestions(t, p, sess, 1)
	if got[0].State != codexq.Expired {
		t.Errorf("State = %v, want expired", got[0].State)
	}
	// The pane has gone quiet and the dialog is gone; without the rollout the
	// row would settle and the question would be lost.
	if s := codexQuestionStatus(status.Finished, got); s != status.Waiting {
		t.Errorf("settled row = %q, want waiting", s)
	}

	// The session keeps its tracker, so the next pass reads only what was
	// appended rather than the whole conversation again.
	if _, kept := p.codexQuestions[sess.ID]; !kept {
		t.Fatal("no tracker kept for the session")
	}
	if again := p.unansweredCodexQuestions(sess); len(again) != 1 {
		t.Errorf("second pass returned %d questions, want 1", len(again))
	}
}

// TestUnansweredCodexQuestionsIgnoresOtherTools keeps the rollout read off
// every non-codex row: those have no rollout and must keep deriving from the
// pane exactly as before.
func TestUnansweredCodexQuestionsIgnoresOtherTools(t *testing.T) {
	p := &poller{locator: search.NewLocator("", t.TempDir())}
	for _, sess := range []store.Session{
		{ID: "a", Tool: search.ToolClaude, AgentSessionID: "x"},
		{ID: "b", Tool: search.ToolCodex},
		{ID: "c", Tool: search.ToolCodex, AgentSessionID: "no-such-rollout"},
	} {
		if got := p.unansweredCodexQuestions(sess); got != nil {
			t.Errorf("session %s: got %+v, want none", sess.ID, got)
		}
	}
}

// TestUnansweredCodexQuestionsWithoutLocator: history search off must not
// take the board's status derivation down with it.
func TestUnansweredCodexQuestionsWithoutLocator(t *testing.T) {
	p := &poller{}
	sess := store.Session{ID: "row", Tool: search.ToolCodex, AgentSessionID: "id"}
	if got := p.unansweredCodexQuestions(sess); got != nil {
		t.Errorf("got %+v, want none", got)
	}
}

// TestOperatorReturnClearsTheRow is the other half of the fix. An expired
// question can never be answered -- its dialog is gone -- so a row that only
// ever cleared on an answer would read waiting for the rest of the session's
// life. The operator's next message in the session retires it.
func TestOperatorReturnClearsTheRow(t *testing.T) {
	const agentID = "01a08470-0000-7000-8000-00000000beef"
	root := t.TempDir()
	dir := filepath.Join(root, "2026", "09", "09")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(filepath.Join("..", "codexq", "testdata", "expired-blocking.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(dir, "rollout-2026-09-09T04-30-42-"+agentID+".jsonl")
	if err := os.WriteFile(rollout, fixture, 0o644); err != nil {
		t.Fatal(err)
	}

	p := &poller{locator: search.NewLocator("", root)}
	sess := store.Session{ID: "row", Tool: search.ToolCodex, AgentSessionID: agentID}

	awaitCodexQuestions(t, p, sess, 1)

	// The operator types something in the session.
	f, err := os.OpenFile(rollout, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"payload":{"type":"message","role":"user",` +
		`"content":[{"type":"text","text":"never mind"}]}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	got := p.unansweredCodexQuestions(sess)
	if len(got) != 0 {
		t.Fatalf("after the operator returns: %+v, want none", got)
	}
	if s := codexQuestionStatus(status.Finished, got); s != status.Finished {
		t.Errorf("settled row = %q, want finished: the question is retired", s)
	}
}

// A tracker holds every question its rollout has shown, so one left behind by
// an ended session grows with the conversation.
func TestVanishedSessionDropsItsTracker(t *testing.T) {
	p := &poller{
		codexQuestions:  map[string]*codexq.Tracker{"gone": {}, "live": {}},
		quietSince:      map[string]time.Time{},
		operatorInputAt: map[string]time.Time{},
		forkDialogs:     map[string]forkDialog{},
	}
	p.forgetVanished([]store.Session{{ID: "live"}})
	if _, still := p.codexQuestions["gone"]; still {
		t.Error("tracker for a vanished session was kept")
	}
	if _, kept := p.codexQuestions["live"]; !kept {
		t.Error("tracker for a listed session was dropped")
	}
}

// buildBulkRollout writes a rollout of about sizeMB whose last record is a
// still-outstanding question, and returns its path. The filler carries
// "role":"user" because that is what makes a record expensive: codexq skips a
// line it cannot match with a byte scan, so filler without a mark on it would
// measure the scan rather than the parse a real conversation pays for.
func buildBulkRollout(t *testing.T, root, agentID string, sizeMB int) string {
	t.Helper()
	dir := filepath.Join(root, "2026", "09", "16")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-2026-09-16T04-30-42-"+agentID+".jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriterSize(f, 1<<20)
	filler := `{"payload":{"type":"message","role":"user","content":[{"type":"text","text":"` +
		strings.Repeat("padding ", 100) + `"}]}}` + "\n"
	for written := 0; written < sizeMB<<20; written += len(filler) {
		if _, err := w.WriteString(filler); err != nil {
			t.Fatal(err)
		}
	}
	// Asked after every filler message, so nothing in the file supersedes it.
	if _, err := w.WriteString(`{"payload":{"type":"function_call",` +
		`"name":"request_user_input","call_id":"call_bulk",` +
		`"arguments":"{\"questions\":[{\"header\":\"h\",\"question\":\"q\"}]}"}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestCodexSeedStaysOffThePollPass is the reason codexSeeder exists. A
// tracker's first Update parses the whole rollout, and on the real board those
// files reach hundreds of megabytes; paid where this is called it lands inside
// the status phase of a poll pass and stalls every row on the board, not just
// the codex ones. The first lookup must cost nothing like the read it stands
// in for, and the questions must arrive on a later pass.
func TestCodexSeedStaysOffThePollPass(t *testing.T) {
	const agentID = "01a08470-0000-7000-8000-00000000f00d"
	root := t.TempDir()
	path := buildBulkRollout(t, root, agentID, 8)

	// What the pass used to pay, measured on this machine rather than
	// asserted as a constant, so the comparison holds on a slow box too.
	var direct codexq.Tracker
	start := time.Now()
	if _, err := direct.Update(path); err != nil {
		t.Fatal(err)
	}
	inline := time.Since(start)
	if inline < 25*time.Millisecond {
		t.Fatalf("a full read of the rollout took only %v; the fixture is too "+
			"small for this comparison to mean anything", inline)
	}

	p := &poller{locator: search.NewLocator("", root)}
	sess := store.Session{ID: "row", Tool: search.ToolCodex, AgentSessionID: agentID}

	start = time.Now()
	first := p.unansweredCodexQuestions(sess)
	elapsed := time.Since(start)
	if elapsed > inline/10 {
		t.Errorf("the first lookup took %v against a %v full read: the seed is "+
			"still being read on the pass", elapsed, inline)
	}
	if len(first) != 0 {
		t.Errorf("the first lookup reported %d questions; until the rollout has "+
			"been read the session has nothing to report", len(first))
	}

	got := awaitCodexQuestions(t, p, sess, 1)
	if got[0].CallID != "call_bulk" {
		t.Errorf("CallID = %q, want call_bulk", got[0].CallID)
	}
	// And the row corrects itself: a settled pane reads waiting once the seed
	// lands, which is the whole behaviour the delay must not cost.
	if s := codexQuestionStatus(status.Finished, got); s != status.Waiting {
		t.Errorf("settled row = %q, want waiting", s)
	}
}

// TestVanishedSessionCancelsItsSeed: a seed publishes a tracker for the pass
// to adopt, and a session that ends before it lands leaves one nothing will
// ever collect.
func TestVanishedSessionCancelsItsSeed(t *testing.T) {
	p := &poller{
		codexQuestions:  map[string]*codexq.Tracker{},
		quietSince:      map[string]time.Time{},
		operatorInputAt: map[string]time.Time{},
		forkDialogs:     map[string]forkDialog{},
	}
	p.codexSeeds.ready = map[string]codexSeed{
		"gone": {path: "/gone.jsonl", tracker: &codexq.Tracker{}},
		"live": {path: "/live.jsonl", tracker: &codexq.Tracker{}},
	}
	p.codexSeeds.inflight = map[string]string{"gone": "/gone.jsonl", "live": "/live.jsonl"}

	p.forgetVanished([]store.Session{{ID: "live"}})

	p.codexSeeds.mu.Lock()
	defer p.codexSeeds.mu.Unlock()
	if _, still := p.codexSeeds.ready["gone"]; still {
		t.Error("a seeded tracker for a vanished session was kept")
	}
	if _, still := p.codexSeeds.inflight["gone"]; still {
		t.Error("a running seed for a vanished session was not cancelled")
	}
	if _, kept := p.codexSeeds.ready["live"]; !kept {
		t.Error("a seeded tracker for a listed session was dropped")
	}
	if _, kept := p.codexSeeds.inflight["live"]; !kept {
		t.Error("a running seed for a listed session was cancelled")
	}
}

// awaitCodexQuestions calls the lookup the way the board does -- once a pass,
// only faster -- until the seed has landed.
func awaitCodexQuestions(t *testing.T, p *poller, sess store.Session, want int) []codexq.Question {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		got := p.unansweredCodexQuestions(sess)
		if len(got) == want {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("after 30s the lookup reports %d questions, want %d", len(got), want)
		}
		time.Sleep(time.Millisecond)
	}
}
