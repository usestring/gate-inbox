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

func TestCopyQueueDrainsOldestFirstAndRequeuesAFailedWrite(t *testing.T) {
	m := &Model{jevCopyQueue: true}
	m.applyJevCopy(jevCopyResultMsg{from: "deploy", texts: []string{"first", "second"}})
	m.applyJevCopy(jevCopyResultMsg{from: "deploy", texts: []string{"second"}})
	if len(m.jevCopy.queue) != 2 {
		t.Fatalf("queue = %+v, want the repeat dropped", m.jevCopy.queue)
	}
	if !strings.Contains(m.errBar.text, "JEV queued 2 from deploy") {
		t.Fatalf("status = %q", m.errBar.text)
	}

	previous := writeClipboard
	t.Cleanup(func() { writeClipboard = previous })
	var wrote []string
	writeClipboard = func(text string) error {
		wrote = append(wrote, text)
		return nil
	}
	_, cmd := m.copyNextQueued()
	m.applyJevCopied(cmd().(jevCopiedMsg))
	if !slices.Equal(wrote, []string{"first"}) || len(m.jevCopy.queue) != 1 {
		t.Fatalf("wrote %q, queue %+v", wrote, m.jevCopy.queue)
	}
	if !strings.Contains(m.errBar.text, "1 left in the copy queue") {
		t.Fatalf("status = %q", m.errBar.text)
	}

	writeClipboard = func(string) error { return errors.New("no clipboard") }
	_, cmd = m.copyNextQueued()
	m.applyJevCopied(cmd().(jevCopiedMsg))
	if len(m.jevCopy.queue) != 1 || m.jevCopy.queue[0].text != "second" {
		t.Fatalf("a failed write lost its text: %+v", m.jevCopy.queue)
	}
}

func TestCopyQueueWaitsForClipboardResult(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("fail=%v", fail), func(t *testing.T) {
			m := &Model{jevCopyQueue: true, jevCopy: jevCopyState{queue: []copyItem{{text: "first"}, {text: "second"}}}}
			previous := writeClipboard
			t.Cleanup(func() { writeClipboard = previous })
			var wrote []string
			writeClipboard = func(text string) error {
				wrote = append(wrote, text)
				if fail && len(wrote) == 1 {
					return errors.New("clipboard unavailable")
				}
				return nil
			}
			_, first := m.copyNextQueued()
			if first == nil {
				t.Fatal("first item did not start")
			}
			result := first().(jevCopiedMsg)
			_, second := m.copyNextQueued()
			if second != nil || len(m.jevCopy.queue) != 1 || m.jevCopy.queue[0].text != "second" {
				t.Fatalf("copy started before result was applied: queue %+v", m.jevCopy.queue)
			}
			m.applyJevCopied(result)
			_, next := m.copyNextQueued()
			if next == nil {
				t.Fatal("copy stayed blocked after its result")
			}
			m.applyJevCopied(next().(jevCopiedMsg))
			want := []string{"first", "second"}
			if fail {
				want = []string{"first", "first"}
			}
			if !slices.Equal(wrote, want) {
				t.Fatalf("wrote %q, want %q", wrote, want)
			}
		})
	}
}

func TestCopyQueueOffIgnoresALateClipboardResult(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("fail=%v", fail), func(t *testing.T) {
			m := &Model{jevCopyQueue: true, jevCopy: jevCopyState{queue: []copyItem{{text: "first"}, {text: "second"}}}}
			previous := writeClipboard
			t.Cleanup(func() { writeClipboard = previous })
			writeClipboard = func(string) error {
				if fail {
					return errors.New("clipboard unavailable")
				}
				return nil
			}
			_, cmd := m.copyNextQueued()
			m.jevCopyQueue = false
			m.jevCopy = jevCopyState{copying: m.jevCopy.copying}
			m.errBar.text = "queue disabled"
			m.applyJevCopied(cmd().(jevCopiedMsg))
			if m.jevCopy.copying || len(m.jevCopy.queue) != 0 {
				t.Fatalf("disabled queue retained clipboard state: %+v", m.jevCopy)
			}
			if m.errBar.text != "queue disabled" {
				t.Fatalf("late result changed status: %q", m.errBar.text)
			}
		})
	}
}

func TestCopyQueueIsCapped(t *testing.T) {
	m := &Model{jevCopyQueue: true}
	for i := range jevCopyQueueCap + 3 {
		m.applyJevCopy(jevCopyResultMsg{from: "s", texts: []string{strings.Repeat("x", i+1)}})
	}
	if len(m.jevCopy.queue) != jevCopyQueueCap || m.jevCopy.queue[0].text != "xxxx" {
		t.Fatalf("queue kept %d, head %q", len(m.jevCopy.queue), m.jevCopy.queue[0].text)
	}
}

func TestCopyQueueOffIgnoresALateResult(t *testing.T) {
	m := &Model{jevCopy: jevCopyState{busy: true}}
	m.applyJevCopy(jevCopyResultMsg{from: "s", texts: []string{"value"}})
	if m.jevCopy.busy || len(m.jevCopy.queue) != 0 {
		t.Fatalf("state = %+v", m.jevCopy)
	}
}

func TestCheckCopyWithJevSeedsThenReadsANewTurnEnd(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	home := t.TempDir()
	sess := store.Session{ID: "waiting", Name: "auth", Tool: "claude", Status: status.Waiting,
		AgentSessionID: "conversation", LastStatusAt: time.Unix(100, 0)}
	path := filepath.Join(home, "projects", "project", sess.AgentSessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"assistant","message":{"role":"assistant","content":"Please run ` + "`gcloud auth login`" + ` yourself."}}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	previous := jevHTTPClient
	t.Cleanup(func() { jevHTTPClient = previous })
	calls := 0
	jevHTTPClient = &http.Client{Transport: jevFinishTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(
			`{"model":"` + jevModel + `","answers":{"c1":{"type":"noul","noul":0.9}}}`))}, nil
	})}
	m := &Model{cfg: config.Config{Tools: map[string]config.Tool{"claude": {Command: "claude"}}},
		sessions: []store.Session{sess}, jevCopyQueue: true,
		conversation: &conversationView{locator: search.NewLocator(home, "")}}
	if cmd := m.checkCopyWithJev(); cmd != nil || calls != 0 {
		t.Fatal("the first pass should only record where rows stand")
	}
	if cmd := m.checkCopyWithJev(); cmd != nil {
		t.Fatal("an unchanged turn was read again")
	}
	m.sessions[0].LastStatusAt = m.sessions[0].LastStatusAt.Add(time.Minute)
	m.sessions[0].AgentSessionID = ""
	cmd := m.checkCopyWithJev()
	if cmd == nil {
		t.Fatal("a new turn end was not read")
	}
	m.applyJevCopy(cmd().(jevCopyResultMsg))
	if calls != 0 || len(m.jevCopy.queue) != 0 {
		t.Fatalf("an uncaptured conversation made %d calls, queue %+v", calls, m.jevCopy.queue)
	}
	if cmd := m.checkCopyWithJev(); cmd != nil {
		t.Fatal("an unchanged missing conversation was read again")
	}
	m.sessions[0].AgentSessionID = sess.AgentSessionID
	cmd = m.checkCopyWithJev()
	if cmd == nil {
		t.Fatal("the newly captured conversation was not retried")
	}
	m.applyJevCopy(cmd().(jevCopyResultMsg))
	if calls != 1 || len(m.jevCopy.queue) != 1 || m.jevCopy.queue[0].text != "gcloud auth login" {
		t.Fatalf("calls %d, queue %+v", calls, m.jevCopy.queue)
	}
	if cmd := m.checkCopyWithJev(); cmd != nil {
		t.Fatal("a checked conversation was read again")
	}
}
