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

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

func TestCopyCandidatesTakesBlocksSpansAndLinksInOrder(t *testing.T) {
	message := "Run this yourself:\n\n```bash\ngcloud auth login\n```\n\n" +
		"Then type `! bun run deploy` and open <https://console.cloud.google.com/run>, then check `x`.\n" +
		"The link `https://example.com/inside` is quoted once."
	got := copyCandidates(message)
	want := []string{"gcloud auth login", "! bun run deploy", "https://console.cloud.google.com/run", "https://example.com/inside"}
	if !slices.Equal(got, want) {
		t.Fatalf("candidates = %q, want %q", got, want)
	}
}

func TestCopyCandidatesPreservesMixedOrderBeforeLimit(t *testing.T) {
	message := "Open https://example.com/first then run `first-command`.\n"
	want := []string{"https://example.com/first", "first-command"}
	for i := range jevCopyCandidates {
		command := fmt.Sprintf("command-%d", i)
		message += "```sh\n" + command + "\n```\n"
		if len(want) < jevCopyCandidates {
			want = append(want, command)
		}
	}
	if got := copyCandidates(message); !slices.Equal(got, want) {
		t.Fatalf("candidates = %q, want %q", got, want)
	}
}

func TestCopyCandidatesPreservesURLParentheses(t *testing.T) {
	for _, message := range []string{
		"https://en.wikipedia.org/wiki/Foo_(bar)",
		"[link](https://en.wikipedia.org/wiki/Foo_(bar)).",
		"(https://en.wikipedia.org/wiki/Foo_(bar)),",
		"https://en.wikipedia.org/wiki/Foo_(bar)).after",
	} {
		if got := copyCandidates(message); !slices.Equal(got, []string{"https://en.wikipedia.org/wiki/Foo_(bar)"}) {
			t.Errorf("message %q: candidates = %q", message, got)
		}
	}
	message := "[nested](https://example.com/a_(b_(c))) and <https://example.com/plain>."
	want := []string{"https://example.com/a_(b_(c))", "https://example.com/plain"}
	if got := copyCandidates(message); !slices.Equal(got, want) {
		t.Fatalf("candidates = %q, want %q", got, want)
	}
}

func TestCopyCandidatesPreservesURLTrailingPunctuation(t *testing.T) {
	for _, suffix := range []string{"!", "?", ".", ",", ";", ":", "!!", "?q=value!"} {
		link := "https://example.test/run" + suffix
		for _, message := range []string{link, "[run](" + link + ").", "<" + link + ">,", "`" + link + "`"} {
			if got := copyCandidates(message); !slices.Equal(got, []string{link}) {
				t.Errorf("message %q: candidates = %q, want %q", message, got, link)
			}
		}
	}
}

func TestCopyCandidatesStripsPunctuationBeforeContinuingProse(t *testing.T) {
	for _, punctuation := range []string{",", ".", ";", ":", "!", "?"} {
		link := "https://example.test/run"
		for _, message := range []string{"Open " + link + punctuation + " then continue", link + punctuation + "\tNext step"} {
			if got := copyCandidates(message); !slices.Equal(got, []string{link}) {
				t.Errorf("message %q: candidates = %q, want %q", message, got, link)
			}
		}
		for _, message := range []string{"<" + link + punctuation + "> then continue", "[run](" + link + punctuation + ") then continue", "`" + link + punctuation + "` then continue"} {
			if got := copyCandidates(message); !slices.Equal(got, []string{link + punctuation}) {
				t.Errorf("message %q: candidates = %q, want %q", message, got, link+punctuation)
			}
		}
	}
}

func TestCopyCandidatesLeavesOutWhatIsTooLongRatherThanCutting(t *testing.T) {
	long := strings.Repeat("a", jevCopyCandidateMax+1)
	if got := copyCandidates("`" + long + "` and `ok-value`"); !slices.Equal(got, []string{"ok-value"}) {
		t.Fatalf("candidates = %q", got)
	}
}

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
	var questions map[string]struct{ Instructions string }
	jevHTTPClient = &http.Client{Transport: jevFinishTransport(func(r *http.Request) (*http.Response, error) {
		var body struct {
			Questions map[string]struct{ Instructions string }
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		questions = body.Questions
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(
			`{"model":"` + jevModel + `","answers":{"c1":{"type":"noul","noul":0.95},"c2":{"type":"noul","noul":0.5}}}`))}, nil
	})}
	picked, ok := askJevCopy(t.Context(), "key", "message", []string{"gcloud auth login", "README.md"})
	if !ok || !slices.Equal(picked, []string{"gcloud auth login"}) {
		t.Fatalf("picked %q ok=%v", picked, ok)
	}
	if !strings.HasSuffix(questions["c2"].Instructions, "README.md") {
		t.Fatalf("question did not quote its candidate: %q", questions["c2"].Instructions)
	}
}

// copyTurn writes a Claude transcript whose last message is text and returns
// a model with JEV copy on, a session for it, and a count of JEV calls that
// answer each candidate with the matching score.
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
		var answers []string
		for i, score := range scores {
			answers = append(answers, fmt.Sprintf(`"c%d":{"type":"noul","noul":%v}`, i+1, score))
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(
			`{"model":"` + jevModel + `","answers":{` + strings.Join(answers, ",") + `}}`))}, nil
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
