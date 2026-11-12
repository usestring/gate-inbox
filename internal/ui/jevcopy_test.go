package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/autocopy"
	"github.com/usestring/gate-inbox/internal/classifier"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

func TestLatestAssistantTextNeedsTheTurnToEndOnTheAgent(t *testing.T) {
	messages := []search.Message{{Role: "user", Text: "go"}, {Role: "assistant", Text: "run `make`"}}
	if got := latestAssistantText(messages); got != "run `make`" {
		t.Fatalf("got %q", got)
	}
	if got := latestAssistantText(append(messages, search.Message{Role: "user", Text: "next"})); got != "" {
		t.Fatalf("a turn ending on the user gave %q", got)
	}
}

func TestAskJevCopyKeepsOnlyConfidentPicks(t *testing.T) {
	previous := jevHTTPClient
	t.Cleanup(func() { jevHTTPClient = previous })
	var body struct {
		State     struct{ Message string }
		Questions map[string]struct{ Instructions string }
	}
	jevHTTPClient = &http.Client{Transport: jevFinishTransport(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return copyAnswers(t, body.Questions, func(name string) float64 {
			return map[string]float64{"c1": 0.95, "c2": 0.5}[name]
		}), nil
	})}
	message := "Run `gcloud auth login`, then read `README.md`."
	plan := autocopy.Prepare(message)
	picked, commands, ok := askJevCopy(t.Context(), classifier.JEV, "key", plan)
	if !ok || !slices.Equal(picked, []string{"gcloud auth login"}) || !slices.Equal(commands, []string{"gcloud auth login"}) {
		t.Fatalf("picked %q commands %q ok=%v", picked, commands, ok)
	}
	if body.State.Message != message || len(body.Questions) != len(plan.Input().Questions) {
		t.Fatalf("JEV was not sent the plan: message %q, %d questions", body.State.Message, len(body.Questions))
	}
	if !strings.HasSuffix(body.Questions["c2"].Instructions, "README.md") {
		t.Fatalf("question did not quote its candidate: %q", body.Questions["c2"].Instructions)
	}
}

// copyAnswers is a JEV reply answering every question asked with score(name).
func copyAnswers[T any](t *testing.T, questions map[string]T, score func(string) float64) *http.Response {
	t.Helper()
	answers := make(map[string]any, len(questions))
	for name := range questions {
		answers[name] = map[string]any{"type": "noul", "noul": score(name)}
	}
	response, err := json.Marshal(map[string]any{"model": jevModel, "answers": answers})
	if err != nil {
		t.Fatal(err)
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(response)))}
}

// copyTurn writes a Claude transcript whose last message is text and returns
// a model with JEV copy on, a session for it, and a count of JEV calls that
// answer each candidate's command question with the matching score and
// every other question with zero.
func copyTurn(t *testing.T, text string, scores ...float64) (*Model, store.Session, *int) {
	t.Helper()
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	home := t.TempDir()
	sess := store.Session{ID: "waiting", Name: "auth", Tool: "claude", Status: status.Waiting,
		AgentSessionID: "conversation", LastStatusAt: time.Unix(100, 0)}
	path := filepath.Join(home, "projects", "project", sess.AgentSessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content, _ := json.Marshal(text)
	line := `{"type":"assistant","message":{"role":"assistant","content":` + string(content) + `}}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	previous := jevHTTPClient
	t.Cleanup(func() { jevHTTPClient = previous })
	calls := 0
	jevHTTPClient = &http.Client{Transport: jevFinishTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		var body struct{ Questions map[string]json.RawMessage }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return copyAnswers(t, body.Questions, func(name string) float64 {
			for i, score := range scores {
				if name == fmt.Sprintf("c%d", i+1) {
					return score
				}
			}
			return 0
		}), nil
	})}
	m := &Model{cfg: config.Config{Tools: map[string]config.Tool{"claude": {Command: "claude"}}},
		sessions: []store.Session{sess}, jevCopyOn: true,
		conversation: &conversationView{locator: search.NewLocator(home, "")}}
	return m, sess, &calls
}

// pressCopy presses the copy key on sess and runs what it starts to the end.
func pressCopy(t *testing.T, m *Model, sess store.Session) {
	t.Helper()
	_, cmd := m.copyWithJev(sess)
	for cmd != nil {
		switch msg := cmd().(type) {
		case jevCopyResultMsg:
			cmd = m.applyJevCopy(msg)
		case jevCopiedMsg:
			m.applyJevCopied(msg)
			cmd = nil
		default:
			t.Fatalf("unexpected message %T", msg)
		}
	}
}

func stubClipboard(t *testing.T, fail func(string) bool) *[]string {
	t.Helper()
	previous := writeClipboard
	t.Cleanup(func() { writeClipboard = previous })
	var wrote []string
	writeClipboard = func(text string) error {
		wrote = append(wrote, text)
		if fail != nil && fail(text) {
			return errors.New("clipboard unavailable")
		}
		return nil
	}
	return &wrote
}

func TestCopyWithJevReadsOnTheKeyThenWalksItsPicks(t *testing.T) {
	m, sess, calls := copyTurn(t, "Run `gcloud auth login`, then open https://console.example.test/run and read `README.md`.", 0.9, 0.95, 0.1)
	wrote := stubClipboard(t, nil)
	if *calls != 0 {
		t.Fatal("JEV was asked before the key")
	}
	pressCopy(t, m, sess)
	if *calls != 1 || !slices.Equal(*wrote, []string{"gcloud auth login"}) {
		t.Fatalf("calls %d, wrote %q", *calls, *wrote)
	}
	if !strings.Contains(m.errBar.text, "copied 17 chars from auth (1 of 2)") {
		t.Fatalf("status = %q", m.errBar.text)
	}
	pressCopy(t, m, sess)
	pressCopy(t, m, sess)
	want := []string{"gcloud auth login", "https://console.example.test/run", "gcloud auth login"}
	if *calls != 1 || !slices.Equal(*wrote, want) {
		t.Fatalf("the same turn was not walked from what JEV picked: calls %d, wrote %q", *calls, *wrote)
	}
	sess.LastStatusAt = sess.LastStatusAt.Add(time.Minute)
	pressCopy(t, m, sess)
	if *calls != 2 {
		t.Fatalf("a later turn was not read again: calls %d", *calls)
	}
}

func TestCopyWithJevSaysWhyNothingWasCopied(t *testing.T) {
	m, sess, calls := copyTurn(t, "Read `README.md` for the details.", 0.2)
	wrote := stubClipboard(t, nil)
	pressCopy(t, m, sess)
	if *calls != 1 || len(*wrote) != 0 || !strings.Contains(m.errBar.text, "JEV found nothing") {
		t.Fatalf("calls %d, wrote %q, status %q", *calls, *wrote, m.errBar.text)
	}

	m, sess, calls = copyTurn(t, "All done, nothing for you to run.")
	pressCopy(t, m, sess)
	if *calls != 0 || !strings.Contains(m.errBar.text, "nothing to copy") {
		t.Fatalf("a message with no candidates made %d calls, status %q", *calls, m.errBar.text)
	}
}

func TestCopyWithJevCachesEmptyVerdictsForTheCurrentTurn(t *testing.T) {
	m, sess, calls := copyTurn(t, "Read `README.md` for the details.", 0.2)
	wrote := stubClipboard(t, nil)
	pressCopy(t, m, sess)
	status := m.errBar.text
	m.errBar.text = "another status"
	pressCopy(t, m, sess)
	if *calls != 1 || len(*wrote) != 0 || m.errBar.text != status {
		t.Fatalf("calls %d, wrote %q, status %q", *calls, *wrote, m.errBar.text)
	}
	sess.LastStatusAt = sess.LastStatusAt.Add(time.Minute)
	pressCopy(t, m, sess)
	if *calls != 2 {
		t.Fatalf("a later turn reused an empty verdict: calls %d", *calls)
	}
}

func TestCopyWithJevRetriesFailedRequests(t *testing.T) {
	m, sess, calls := copyTurn(t, "Run `example command`.", 0.9)
	wrote := stubClipboard(t, nil)
	workingClient := jevHTTPClient
	failedCalls := 0
	jevHTTPClient = &http.Client{Transport: jevFinishTransport(func(*http.Request) (*http.Response, error) {
		failedCalls++
		return nil, errors.New("service unavailable")
	})}
	pressCopy(t, m, sess)
	if failedCalls == 0 || len(*wrote) != 0 || !strings.Contains(m.errBar.text, "JEV did not answer") {
		t.Fatalf("failed calls %d, wrote %q, status %q", failedCalls, *wrote, m.errBar.text)
	}
	jevHTTPClient = workingClient
	pressCopy(t, m, sess)
	if *calls != 1 || !slices.Equal(*wrote, []string{"example command"}) {
		t.Fatalf("failed request was not retried: calls %d, wrote %q", *calls, *wrote)
	}
}

func TestCopyWithJevIsOffUntilEnabled(t *testing.T) {
	m, sess, calls := copyTurn(t, "Run `gcloud auth login`.", 0.9)
	m.jevCopyOn = false
	if _, cmd := m.copyWithJev(sess); cmd != nil || *calls != 0 {
		t.Fatal("the key acted with the experiment off")
	}
}

func TestCopyWithJevRetriesAFailedWriteOnTheSamePick(t *testing.T) {
	m, sess, _ := copyTurn(t, "Run `first-command` then `second-command`.", 0.9, 0.9)
	failed := false
	wrote := stubClipboard(t, func(string) bool {
		if failed {
			return false
		}
		failed = true
		return true
	})
	pressCopy(t, m, sess)
	if !strings.Contains(m.errBar.text, "could not copy") {
		t.Fatalf("status = %q", m.errBar.text)
	}
	pressCopy(t, m, sess)
	if !slices.Equal(*wrote, []string{"first-command", "first-command"}) {
		t.Fatalf("wrote %q", *wrote)
	}
}

func TestCopyWithJevTakesOnePressAtATime(t *testing.T) {
	m, sess, calls := copyTurn(t, "Run `first-command` then `second-command`.", 0.9, 0.9)
	wrote := stubClipboard(t, nil)
	_, read := m.copyWithJev(sess)
	if _, again := m.copyWithJev(sess); again != nil {
		t.Fatal("a second press started while JEV was reading")
	}
	write := m.applyJevCopy(read().(jevCopyResultMsg))
	if _, again := m.copyWithJev(sess); again != nil {
		t.Fatal("a second press started while the clipboard was being written")
	}
	m.applyJevCopied(write().(jevCopiedMsg))
	pressCopy(t, m, sess)
	if *calls != 1 || !slices.Equal(*wrote, []string{"first-command", "second-command"}) {
		t.Fatalf("calls %d, wrote %q", *calls, *wrote)
	}
}

func TestCopyWithJevReenabledIgnoresPreviousActivation(t *testing.T) {
	for _, kind := range []string{"jev", "clipboard-success", "clipboard-failure"} {
		t.Run(kind, func(t *testing.T) {
			m := buildModel(t)
			m.jevCopyOn = true
			m.jevCopy = jevCopyState{turn: "old", from: "s", picks: []string{"old pick", "other pick"}}
			generation := m.jevCopy.generation
			var copied jevCopiedMsg
			if kind == "jev" {
				m.jevCopy.busy = true
			} else {
				stubClipboard(t, func(string) bool { return kind == "clipboard-failure" })
				copied = m.copyNextPick()().(jevCopiedMsg)
			}
			m.openSettings()
			m.settings.jevCopyOn = false
			m.persistSettings()
			m.settings.jevCopyOn = true
			m.persistSettings()
			if m.jevCopy.generation == generation || len(m.jevCopy.picks) != 0 {
				t.Fatalf("toggling kept the previous activation: %+v", m.jevCopy)
			}
			m.errBar.text = "current status"
			if kind == "jev" {
				if cmd := m.applyJevCopy(jevCopyResultMsg{generation: generation, turn: "old", texts: []string{"old result"}}); cmd != nil {
					t.Fatal("a stale read started a copy")
				}
			} else {
				m.applyJevCopied(copied)
			}
			if m.jevCopy.busy || m.jevCopy.copying || len(m.jevCopy.picks) != 0 || m.errBar.text != "current status" {
				t.Fatalf("stale result changed the new activation: state %+v, status %q", m.jevCopy, m.errBar.text)
			}
		})
	}
}

func TestCopyWithJevCopiesDraftWithoutItsCommentary(t *testing.T) {
	for _, link := range []string{"", " Open https://example.test/billing."} {
		t.Run(link, func(t *testing.T) {
			draft := "Hey, sorry you had trouble finding it!" + link + "\n\nYour unused balance carries over."
			message := "Here's the revised reply.\n\n" + draft + "\n\nI checked the page. These changes are only proposals."
			m, sess, calls := copyTurn(t, message)
			wrote := stubClipboard(t, nil)
			jevHTTPClient = &http.Client{Transport: jevFinishTransport(func(r *http.Request) (*http.Response, error) {
				*calls++
				var body struct{ Questions map[string]json.RawMessage }
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				return copyAnswers(t, body.Questions, func(name string) float64 {
					if name == "has_draft" || name == "p2" || name == "p3" || name == "c1" {
						return 0.99
					}
					return 0
				}), nil
			})}
			pressCopy(t, m, sess)
			pressCopy(t, m, sess)
			if *calls != 1 || !slices.Equal(*wrote, []string{draft, draft}) {
				t.Fatalf("calls=%d clipboard=%q", *calls, *wrote)
			}
		})
	}
}

// pressRun presses the run key on sess, runs what it starts to the end, and
// returns the lines it would have typed into the session.
func pressRun(t *testing.T, m *Model, sess store.Session) []string {
	t.Helper()
	previous := sendJevCommand
	t.Cleanup(func() { sendJevCommand = previous })
	var typed []string
	sendJevCommand = func(_ *Model, into store.Session, text, _ string) (tea.Cmd, bool) {
		if into.ID != sess.ID {
			t.Fatalf("typed into %q, want %q", into.ID, sess.ID)
		}
		typed = append(typed, text)
		return nil, true
	}
	_, cmd := m.runWithJev(sess)
	for cmd != nil {
		msg, ok := cmd().(jevCopyResultMsg)
		if !ok {
			t.Fatalf("unexpected message %T", msg)
		}
		cmd = m.applyJevCopy(msg)
	}
	return typed
}

func TestRunWithJevTypesEachCommandOnceThenStops(t *testing.T) {
	m, sess, calls := copyTurn(t, "Run `gcloud auth login`, then `! bun run deploy`, then open https://console.example.test/run.", 0.9, 0.95, 0.1)
	m.sessions = []store.Session{sess}
	stubClipboard(t, nil)
	if got := pressRun(t, m, sess); !slices.Equal(got, []string{"! gcloud auth login"}) {
		t.Fatalf("first press typed %q", got)
	}
	if got := pressRun(t, m, sess); !slices.Equal(got, []string{"! bun run deploy"}) {
		t.Fatalf("second press typed %q", got)
	}
	if got := pressRun(t, m, sess); len(got) != 0 || !strings.Contains(m.errBar.text, "already sent all 2 commands") {
		t.Fatalf("a third press typed %q, status %q", got, m.errBar.text)
	}
	if *calls != 1 {
		t.Fatalf("the same turn was read %d times", *calls)
	}
	// The copy key shares that read and still walks its own picks.
	wrote := stubClipboard(t, nil)
	pressCopy(t, m, sess)
	if *calls != 1 || !slices.Equal(*wrote, []string{"gcloud auth login"}) {
		t.Fatalf("copy after run: calls %d, wrote %q", *calls, *wrote)
	}
}

func TestRunWithJevSkipsWhatItIsNotSureOfAndMultilineBlocks(t *testing.T) {
	m, sess, _ := copyTurn(t, "Maybe `make lint`.\n\n```sh\necho one\necho two\n```", 0.75, 0.99)
	m.sessions = []store.Session{sess}
	if got := pressRun(t, m, sess); len(got) != 0 || !strings.Contains(m.errBar.text, "left for you to run") {
		t.Fatalf("typed %q, status %q", got, m.errBar.text)
	}
}

func TestRunWithJevIsOffUntilEnabled(t *testing.T) {
	m, sess, calls := copyTurn(t, "Run `gcloud auth login`.", 0.9)
	m.jevCopyOn = false
	if _, cmd := m.runWithJev(sess); cmd != nil || *calls != 0 {
		t.Fatal("the run key acted with the experiment off")
	}
}

// The run key reads which candidate a command question quotes from its
// wording, which autocopy does not export; this fails if an upgrade changes it.
func TestJevRunQuestionMatchesAutocopy(t *testing.T) {
	questions := autocopy.Prepare("Run `example command`.").Input().Questions
	if questions["c1"] != jevRunQuestion+"example command" {
		t.Fatalf("autocopy's command question is now %q", questions["c1"])
	}
}

func TestRunWithJevKeepsARefusedCommandForTheNextPress(t *testing.T) {
	m, sess, _ := copyTurn(t, "Run `gcloud auth login`.", 0.9)
	m.sessions = []store.Session{sess}
	previous := sendJevCommand
	t.Cleanup(func() { sendJevCommand = previous })
	sendJevCommand = func(*Model, store.Session, string, string) (tea.Cmd, bool) { return nil, false }
	_, cmd := m.runWithJev(sess)
	m.applyJevCopy(cmd().(jevCopyResultMsg))
	if got := pressRun(t, m, sess); !slices.Equal(got, []string{"! gcloud auth login"}) {
		t.Fatalf("after a refused send the next press typed %q", got)
	}
}
