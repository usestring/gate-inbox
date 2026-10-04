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

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

func TestJevFinishVerdictDecides(t *testing.T) {
	cases := []struct {
		name string
		v    jevFinishVerdict
		want jevFinishAction
	}{
		{"missing chrome, not done", jevFinishVerdict{finished: 0.05, missingChrome: 0.95}, jevFinishRelaunchChrome},
		{"band edges are inclusive", jevFinishVerdict{finished: 0.20, missingChrome: 0.80}, jevFinishRelaunchChrome},
		{"missing chrome but done", jevFinishVerdict{finished: 0.90, missingChrome: 0.90}, jevFinishNone},
		{"missing chrome unsure on done", jevFinishVerdict{finished: 0.50, missingChrome: 0.95}, jevFinishNone},
		{"done", jevFinishVerdict{finished: 0.96, missingChrome: 0.02}, jevFinishDone},
		{"unsure", jevFinishVerdict{finished: 0.60, missingChrome: 0.40}, jevFinishNone},
	}
	for _, tc := range cases {
		if got := tc.v.decide(); got != tc.want {
			t.Errorf("%s: decide() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestAskJevFinishSendsBothQuestionsInOneCall(t *testing.T) {
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
		if body.Model != jevModel || body.Questions["finished"]["type"] != "noul" || body.Questions["missing_chrome"]["type"] != "noul" {
			t.Errorf("request = %+v", body)
		}
		_, _ = w.Write([]byte(`{"model":"` + jevModel + `","answers":{"finished":{"type":"noul","noul":0.1},"missing_chrome":{"type":"noul","noul":0.9}}}`))
	}))
	defer server.Close()

	v, ok := askJevFinish(context.Background(), server.Client(), server.URL, "key", []suggestMessage{{Role: "assistant", Text: "Chrome tools are not available"}})
	if !ok || calls != 1 || v.finished != 0.1 || v.missingChrome != 0.9 {
		t.Fatalf("verdict = %+v ok = %v calls = %d", v, ok, calls)
	}
}

func TestAskJevFinishRejectsAPartialAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"model":"` + jevModel + `","answers":{"finished":{"type":"noul","noul":0.1}}}`))
	}))
	defer server.Close()
	if _, ok := askJevFinish(context.Background(), server.Client(), server.URL, "key", []suggestMessage{{Role: "user", Text: "x"}}); ok {
		t.Fatal("an answer missing missing_chrome must not be read as a verdict")
	}
}

func TestJevFinishTurnsKeepsTheLatestTurns(t *testing.T) {
	var messages []search.Message
	for i := range 10 {
		messages = append(messages, search.Message{Role: "user", Text: strings.Repeat("u", i+1)}, search.Message{Role: "tool", Text: "noise"})
	}
	turns := jevFinishTurns(messages)
	if len(turns) != jevFinishMessages || turns[len(turns)-1].Text != strings.Repeat("u", 10) || turns[0].Text != strings.Repeat("u", 5) {
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
		verdict: jevFinishVerdict{finished: 0.05, missingChrome: 0.97}}
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
		verdict: jevFinishVerdict{finished: 0.05, missingChrome: 0.97}})
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
				verdict: jevFinishVerdict{finished: 0.05, missingChrome: 0.97}}
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
	if err := os.WriteFile(path, []byte(`{"type":"assistant","message":{"role":"assistant","content":"Chrome is unavailable"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	previous := jevHTTPClient
	t.Cleanup(func() { jevHTTPClient = previous })
	jevHTTPClient = &http.Client{Transport: jevFinishTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(
			`{"model":"` + jevModel + `","answers":{"finished":{"type":"noul","noul":0.1},"missing_chrome":{"type":"noul","noul":0.9}}}`))}, nil
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
