package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/classifier"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

func TestJevFinishVerdictDecides(t *testing.T) {
	cases := []struct {
		name string
		v    jevFinishVerdict
		want jevFinishAction
	}{
		{"missing chrome, not done", jevFinishVerdict{SameGoal: 0.95, Finished: 0.05, MissingChrome: 0.95}, jevFinishRelaunchChrome},
		{"band edges are inclusive", jevFinishVerdict{SameGoal: 0.95, Finished: 0.20, MissingChrome: 0.80}, jevFinishRelaunchChrome},
		{"missing chrome but done", jevFinishVerdict{SameGoal: 0.95, Finished: 0.90, MissingChrome: 0.90}, jevFinishNone},
		{"missing chrome unsure on done", jevFinishVerdict{SameGoal: 0.95, Finished: 0.50, MissingChrome: 0.95}, jevFinishNone},
		{"unrelated task complete", jevFinishVerdict{SameGoal: 0.1, Finished: 0.99, MissingChrome: 0.01}, jevFinishNone},
		{"aligned but unfinished", jevFinishVerdict{SameGoal: 0.98, Finished: 0.05, MissingChrome: 0.01}, jevFinishIncomplete},
		{"done", jevFinishVerdict{SameGoal: 0.95, Finished: 0.96, MissingChrome: 0.02}, jevFinishDone},
		{"unsure", jevFinishVerdict{SameGoal: 0.95, Finished: 0.60, MissingChrome: 0.40}, jevFinishNone},
		{"probably done", jevFinishVerdict{SameGoal: 0.95, Finished: 0.60, MissingChrome: 0.05}, jevFinishNone},
		{"uncertain", jevFinishVerdict{SameGoal: 0.95, Finished: 0.50, MissingChrome: 0.20}, jevFinishNone},
		{"below the probe", jevFinishVerdict{SameGoal: 0.95, Finished: 0.45, MissingChrome: 0.05}, jevFinishNone},
	}
	for _, tc := range cases {
		if got := tc.v.decide(nil, jevFinishBandHigh); got != tc.want {
			t.Errorf("%s: decide() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestAskJevFinishSendsOpeningAndEndStateInOneCall(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer key" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		var body struct {
			Model     string                    `json:"model"`
			State     map[string]any            `json:"state"`
			Questions map[string]map[string]any `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model != jevModel || body.State["initial_request"] != "Verify the fix in Chrome" || body.Questions["same_goal"]["type"] != "noul" || body.Questions["finished"]["type"] != "noul" || body.Questions["missing_chrome"]["type"] != "noul" {
			t.Errorf("request = %+v", body)
		}
		_, _ = w.Write([]byte(`{"model":"` + jevModel + `","answers":{"same_goal":{"type":"noul","noul":0.95},"finished":{"type":"noul","noul":0.1},"missing_chrome":{"type":"noul","noul":0.9}}}`))
	}))
	defer server.Close()

	v, ok := askJevFinish(context.Background(), classifier.JEV, server.Client(), server.URL, "key", jevFinishInput{InitialRequest: "Verify the fix in Chrome", Messages: []suggestMessage{{Role: "assistant", Text: "Chrome tools are not available"}}})
	if !ok || calls != 1 || v.Finished != 0.1 || v.MissingChrome != 0.9 {
		t.Fatalf("verdict = %+v ok = %v calls = %d", v, ok, calls)
	}
}

func TestAskJevFinishRejectsAPartialAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"model":"` + jevModel + `","answers":{"finished":{"type":"noul","noul":0.99},"missing_chrome":{"type":"noul","noul":0.01}}}`))
	}))
	defer server.Close()
	if _, ok := askJevFinish(context.Background(), classifier.JEV, server.Client(), server.URL, "key", jevFinishInput{InitialRequest: "Verify the fix in Chrome"}); ok {
		t.Fatal("an answer missing same_goal must not be read as a verdict")
	}
}

func TestJevFinishTurnsKeepsTheLatestTurns(t *testing.T) {
	var messages []search.Message
	for i := range 10 {
		messages = append(messages, search.Message{Role: "user", Text: strings.Repeat("u", i+1)}, search.Message{Role: "tool", Text: "noise"})
	}
	turns := jevFinishTurns(messages, jevFinishMessages, jevFinishMessageChars)
	if len(turns) != jevFinishMessages || turns[len(turns)-1].Text != strings.Repeat("u", 10) || turns[0].Text != strings.Repeat("u", 11-jevFinishMessages) {
		t.Fatalf("turns = %+v", turns)
	}
}

func TestWithChromeFlagAddsItOnce(t *testing.T) {
	if got := withChromeFlag("claude --resume 'abc'"); got != "claude --resume 'abc' --chrome" {
		t.Fatalf("got %q", got)
	}
	if got := withChromeFlag("claude --chrome --resume 'abc'"); got != "claude --chrome --resume 'abc'" {
		t.Fatalf("got %q", got)
	}
}

func TestJevMissingChromeRelaunchesWithChromeAndContinue(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "browser", t.TempDir(), "")
	sess := m.sessionRows()[0]

	argsFile := t.TempDir() + "/launch-args"
	tool := m.cfg.Tools[sess.Tool]
	tool.ResumeByIDCommand = argCaptureCommand(argsFile) + " --resume {id}"
	tool.PromptMode = ""
	tool.PromptFlag = ""
	tool.TypedPromptPrefixes = nil
	tool.ModelFlag = ""
	m.cfg.Tools[sess.Tool] = tool

	if err := m.store.SetAgentSessionID(sess.ID, "held-conversation"); err != nil {
		t.Fatal(err)
	}
	finishedAt := time.Now()
	m.sessions[0].AgentSessionID = "held-conversation"
	m.sessions[0].Status = status.Finished
	m.sessions[0].LastStatusAt = finishedAt
	m.jevFinishCheck = true

	msg := jevFinishResultMsg{id: sess.ID, identity: jevFinishIdentity(m.sessions[0]), ok: true,
		verdict: jevFinishVerdict{SameGoal: 0.95, Finished: 0.05, MissingChrome: 0.97}}
	m.applyJevFinish(msg)
	if !m.errBar.worked() || !strings.Contains(m.errBar.text, "--chrome") {
		t.Fatalf("status bar = %q", m.errBar.text)
	}
	if !m.tmux.Exists(sess.ID) {
		t.Fatal("the session should be running again")
	}
	args := readWhenWritten(t, argsFile)
	// The board's own MCP and system-prompt flags follow; the resume, the
	// Chrome flag and the prompt must lead in that order.
	if !strings.HasPrefix(args, "--resume\nheld-conversation\n--chrome\ncontinue\n") {
		t.Fatalf("launch arguments = %q", args)
	}

	// The same verdict on a later finish must not relaunch a second time.
	for i := range m.sessions {
		if m.sessions[i].ID == sess.ID {
			m.sessions[i].Status = status.Finished
			m.sessions[i].LastStatusAt = finishedAt.Add(time.Minute)
			msg.identity = jevFinishIdentity(m.sessions[i])
		}
	}
	m.applyJevFinish(msg)
	if m.errBar.worked() || !strings.Contains(m.errBar.text, "still lacks Chrome") {
		t.Fatalf("second verdict: status bar = %q", m.errBar.text)
	}
}

func TestJevFinishIgnoresAStaleVerdict(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "moved-on", t.TempDir(), "")
	m.jevFinishCheck = true
	m.sessions[0].Status = status.Working
	m.applyJevFinish(jevFinishResultMsg{id: m.sessions[0].ID, identity: "old", ok: true,
		verdict: jevFinishVerdict{SameGoal: 0.95, Finished: 0.05, MissingChrome: 0.97}})
	if m.errBar.text != "" {
		t.Fatalf("a verdict about an earlier finish acted: %q", m.errBar.text)
	}
}

func TestJevFinishCandidateRecognizesClaudeWrappers(t *testing.T) {
	for _, tc := range []struct {
		name string
		tool config.Tool
		want bool
	}{
		{"claude", config.Tool{Command: "wrapper"}, true},
		{"cc", config.Tool{Command: "/usr/local/bin/claude --verbose"}, true},
		{"custom", config.Tool{Command: "wrapper", SessionStore: search.ToolClaude}, true},
		{"codex", config.Tool{Command: "codex"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &Model{cfg: config.Config{Tools: map[string]config.Tool{tc.name: tc.tool}},
				sessions: []store.Session{{ID: "finished", Tool: tc.name, Status: status.Finished}}}
			_, _, got := m.jevFinishCandidate()
			if got != tc.want {
				t.Fatalf("candidate = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestJevFinishLeavesOperatorInputAlone(t *testing.T) {
	for _, leave := range []bool{false, true} {
		t.Run(fmt.Sprintf("leave=%v", leave), func(t *testing.T) {
			m := buildModel(t)
			createSession(t, m, "draft", t.TempDir(), "")
			sess := m.sessions[0]
			m.sessions[0].Status = status.Finished
			m.sessions[0].LastStatusAt = time.Now()
			m.jevFinishCheck = true
			m.jevFinish.busy = true
			msg := jevFinishResultMsg{id: sess.ID, identity: jevFinishIdentity(m.sessions[0]), ok: true,
				verdict: jevFinishVerdict{SameGoal: 0.95, Finished: 0.05, MissingChrome: 0.97}}
			m.triage = true
			m.focusSession(sess.ID)
			m.focusSelected()
			m.typeInto(t, "keep this draft")
			waitForPaneText(t, m, sess.ID, "keep this draft")
			if leave {
				m.typeInto(t, "\n")
				m.mode = modeList
			}
			if m.sessions[0].Status != status.Finished || jevFinishIdentity(m.sessions[0]) != msg.identity {
				t.Fatal("fixture must retain the pre-poll finished status")
			}
			m.applyJevFinish(msg)
			if m.jevFinish.busy || m.errBar.text != "" || m.jevFinish.chromeRelaunched[sess.ID] {
				t.Fatalf("verdict acted on operator input: %q", m.errBar.text)
			}
			waitForPaneText(t, m, sess.ID, "keep this draft")
			if _, _, ok := m.jevFinishCandidate(); ok {
				t.Fatal("an attended finish must not be checked again after leaving focus")
			}
			m.mode = modeList
			m.sessions[0].LastStatusAt = m.sessions[0].LastStatusAt.Add(time.Minute)
			if _, _, ok := m.jevFinishCandidate(); !ok {
				t.Fatal("a later finish should still be eligible")
			}
		})
	}
}

type jevFinishTransport func(*http.Request) (*http.Response, error)

func (f jevFinishTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestJevFinishUsesAnIndependentLocator(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	home := t.TempDir()
	locator := search.NewLocator(home, "")
	sess := store.Session{ID: "finished", Tool: "claude", Status: status.Finished, AgentSessionID: "conversation"}
	if _, ok := locator.Target(sess.ID, search.ToolClaude, "", sess.AgentSessionID); ok {
		t.Fatal("the preview locator should cache a missing transcript")
	}
	path := filepath.Join(home, "projects", "project", sess.AgentSessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"type":"user","message":{"role":"user","content":"Verify the fix in Chrome"}}
{"type":"assistant","message":{"role":"assistant","content":"Chrome is unavailable"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	previous := jevHTTPClient
	t.Cleanup(func() { jevHTTPClient = previous })
	jevHTTPClient = &http.Client{Transport: jevFinishTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(
			`{"model":"` + jevModel + `","answers":{"same_goal":{"type":"noul","noul":0.95},"finished":{"type":"noul","noul":0.1},"missing_chrome":{"type":"noul","noul":0.9}}}`))}, nil
	})}
	m := &Model{cfg: config.Config{Tools: map[string]config.Tool{"claude": {Command: "claude"}}},
		sessions: []store.Session{sess}, jevFinishCheck: true, conversation: &conversationView{locator: locator},
		rows: []treeRow{{sess: sess}}}
	preview := m.readConversation()
	finish := m.checkFinishedWithJev()
	if preview == nil || finish == nil {
		t.Fatal("both transcript commands should be runnable")
	}
	previewDone := make(chan struct{})
	go func() {
		preview()
		close(previewDone)
	}()
	msg := finish().(jevFinishResultMsg)
	<-previewDone
	if !msg.ok {
		t.Fatal("the finish check reused the preview locator's miss cache")
	}
	if _, ok := locator.Target(sess.ID, search.ToolClaude, "", sess.AgentSessionID); ok {
		t.Fatal("the finish check changed the preview locator's cache")
	}
}

func TestJevFinishAsksOnceForTheSameTurns(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	home := t.TempDir()
	sess := store.Session{ID: "finished", Tool: "claude", Status: status.Finished, AgentSessionID: "conversation",
		LastStatusAt: time.Now()}
	path := filepath.Join(home, "projects", "project", sess.AgentSessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"type":"user","message":{"role":"user","content":"Publish the release"}}
{"type":"assistant","message":{"role":"assistant","content":"Shipped."}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"mcp__gate-inbox__report_status","input":{"state":"done","note":"Shipped."}}]}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := 0
	previous := jevHTTPClient
	t.Cleanup(func() { jevHTTPClient = previous })
	jevHTTPClient = &http.Client{Transport: jevFinishTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(
			`{"model":"` + jevModel + `","answers":{"same_goal":{"type":"noul","noul":0.95},"finished":{"type":"noul","noul":0.4},"missing_chrome":{"type":"noul","noul":0.1}}}`))}, nil
	})}
	m := &Model{cfg: config.Config{Tools: map[string]config.Tool{"claude": {Command: "claude"}}},
		sessions: []store.Session{sess}, jevFinishCheck: true,
		conversation: &conversationView{locator: search.NewLocator(home, "")}}

	m.applyJevFinish(m.checkFinishedWithJev()().(jevFinishResultMsg))
	// A finished, idle, finished flicker restamps the finish with no new turn.
	m.sessions[0].LastStatusAt = m.sessions[0].LastStatusAt.Add(2 * time.Second)
	finish := m.checkFinishedWithJev()
	if finish == nil {
		t.Fatal("a restamped finish is still a candidate")
	}
	m.applyJevFinish(finish().(jevFinishResultMsg))
	if calls != 1 {
		t.Fatalf("JEV calls = %d, want 1 for unchanged turns", calls)
	}
	if m.jevFinish.busy {
		t.Fatal("a skipped call must free the check")
	}
}

func TestJevFinishEvidenceKeepsOpeningOutsideTail(t *testing.T) {
	opening := "Count the records and verify the result"
	messages := []search.Message{
		{Role: "user", Text: "Switch tasks and publish the agenda"},
		{Role: "assistant", Text: "I will publish the agenda"},
		{Role: "assistant", Text: "I prepared the release"},
		{Role: "assistant", Text: "Uploaded the agenda"},
		{Role: "assistant", Text: "Verified the published agenda"},
		{Role: "user", Text: jevFinishProbe},
		{Role: "assistant", Text: "DONE"},
	}
	evidence := jevFinishEvidence(opening, messages, jevFinishMessages, jevFinishMessageChars)
	if evidence.InitialRequest != opening || len(evidence.Messages) != 4 ||
		evidence.Messages[0].Text != "Switch tasks and publish the agenda" || evidence.Messages[3].Text != "Verified the published agenda" {
		t.Fatalf("evidence = %+v", evidence)
	}
	for _, unusable := range []string{"", "continue", "[Image]", strings.Repeat("a", jevFinishOpeningChars+1)} {
		if state := jevFinishEvidence(unusable, messages, jevFinishMessages, jevFinishMessageChars); state.InitialRequest != "" {
			t.Fatal("checked a missing or truncated initial request")
		}
	}
	unanswered := append(messages, search.Message{Role: "user", Text: "Now verify it on a second account"})
	if state := jevFinishEvidence(opening, unanswered, jevFinishMessages, jevFinishMessageChars); len(state.Messages) != 0 {
		t.Fatal("checked an end state with a newer unanswered request")
	}
}

func TestJevFinishCollectsOnlyPositiveCandidates(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "review", t.TempDir(), "")
	m.jevFinishCheck = true
	m.cfg.FinishCheck.CollectOnly = true
	m.sessions[0].Status = status.Finished
	m.sessions[0].LastStatusAt = time.Now()
	sess := m.sessions[0]
	input := jevFinishInput{InitialRequest: "Review the release",
		Messages: []suggestMessage{{Role: "assistant", Text: "Reviewed. The checks passed."}}}
	msg := jevFinishResultMsg{id: sess.ID, identity: jevFinishIdentity(sess), ok: true,
		digest: jevFinishDigest(input), input: input}
	path := filepath.Join(m.configDir(), jevFinishPositivesFile)
	for _, verdict := range []jevFinishVerdict{
		{0.05, 0.99, 0.01}, {0.98, 0.1, 0.01}, {0.9, 0.65, 0.01}, {0.95, 0.05, 0.95},
	} {
		msg.verdict = verdict
		m.applyJevFinish(msg)
	}
	msg.verdict = jevFinishVerdict{0.94, 0.97, 0.02}
	msg.input.StatusReport = &search.StatusReport{State: "waiting", Note: "Approval is needed"}
	m.applyJevFinish(msg)
	msg.input.StatusReport = nil
	stale := msg
	stale.identity = "old"
	m.applyJevFinish(stale)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a non-positive or stale verdict created a dataset: %v", err)
	}
	m.applyJevFinish(msg)
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var got jevFinishPositive
	dec := json.NewDecoder(f)
	if err := dec.Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.SessionID != sess.ID || got.Input.InitialRequest != input.InitialRequest || got.EvidenceID != msg.digest ||
		got.Scores != msg.verdict || got.Classifier != "JEV" || len(got.Input.Messages) != 1 {
		t.Fatalf("recorded %+v", got)
	}
	if err := dec.Decode(&got); err != io.EOF {
		t.Fatalf("unexpected extra record: %v", err)
	}
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("dataset permissions = %o", info.Mode().Perm())
	}
	if m.errBar.done != "" || m.errBar.text != "" || m.jevFinish.chromeRelaunched[sess.ID] || len(sessionPendingInputs(t, m, sess.ID)) != 0 {
		t.Fatal("collection mode acted on a finish")
	}
}

func TestJevFinishDiscardsOldQueuedProbes(t *testing.T) {
	for _, input := range []string{jevFinishProbePrefix + time.Now().UTC().Format(time.RFC3339Nano), jevFinishProbe} {
		st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		sess := store.Session{ID: "probe", Tool: "claude", Status: status.Finished,
			PendingInputs: []string{input, "the user's next task"}}
		if err := st.CreateSession(sess); err != nil {
			t.Fatal(err)
		}
		p := &poller{store: st}
		if sent, err := p.maybeSendPendingInputWhenReady(sess, tmux.Capture{}, status.Finished, true); err != nil || sent {
			t.Fatalf("obsolete probe sent = %v, %v", sent, err)
		}
		stored, err := st.Get(sess.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(stored.PendingInputs) != 1 || stored.PendingInputs[0] != "the user's next task" || stored.PendingInputClaimed {
			t.Fatalf("queue after discarding probe: %#v, claimed=%v", stored.PendingInputs, stored.PendingInputClaimed)
		}
	}
}
