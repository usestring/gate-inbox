package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/asks"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/store"
)

const fakeTool = "fake-cli"

var fakeQuestions = []convo.AskQuestion{
	{ID: "color", Header: "Color", Question: "Which color should the banner use?",
		Options: []convo.AskOption{{Label: "Red", Description: "A bold banner"}, {Label: "Green"}}},
	{ID: "size", Header: "Size", Question: "How big should it be?", MultiSelect: true,
		Options: []convo.AskOption{{Label: "Small"}, {Label: "Large"}}},
}

type fakeAsks struct {
	call asks.Call
	lost *asks.Result
}

func (f fakeAsks) Traits() asks.Traits {
	return asks.Traits{Name: "Fake CLI", MultiSelectAnswerable: true, FreeText: "type as its own answer instead",
		Expires: 2 * time.Minute}
}
func (fakeAsks) Located(asks.Target) bool                       { return true }
func (f fakeAsks) Pending(asks.Target) (asks.Call, bool)        { return f.call, f.call.ID != "" }
func (fakeAsks) Result(asks.Target, string) (asks.Result, bool) { return asks.Result{}, false }
func (fakeAsks) Answered(asks.Target, time.Time) ([]convo.AnsweredAsk, error) {
	return nil, nil
}
func (f fakeAsks) Unanswered(asks.Target) (asks.Result, bool) {
	if f.lost == nil {
		return asks.Result{}, false
	}
	return *f.lost, true
}

func fakeReader(pane string, asked []convo.AskQuestion) (dialog.Reading, bool) {
	if !strings.Contains(pane, "FAKE-DIALOG") {
		return dialog.Reading{}, false
	}
	var out []dialog.Question
	for i, q := range asked {
		question := dialog.Question{Index: i + 1, ID: q.ID, Header: q.Header, Question: q.Question,
			MultiSelect: q.MultiSelect, OnScreen: i == 0}
		for _, option := range q.Options {
			question.Options = append(question.Options, dialog.Option{Label: option.Label, Description: option.Description})
		}
		out = append(out, question)
	}
	if len(out) == 0 {
		out = []dialog.Question{{Index: 1, Question: "Which color should the banner use?", OnScreen: true}}
	}
	return dialog.Reading{Questions: out}, true
}

func registerFakeTool(t *testing.T, src fakeAsks) {
	t.Helper()
	asks.Register(fakeTool, src)
	dialog.RegisterTool(fakeTool, dialog.ToolReader{Questions: fakeReader})
	t.Cleanup(func() {
		asks.Register(fakeTool, fakeAsks{})
		dialog.RegisterTool(fakeTool, dialog.ToolReader{})
	})
}

func TestAnotherCLIsQuestionIsRelayedFromItsOwnRecord(t *testing.T) {
	asked := time.Date(2026, 9, 30, 20, 10, 39, 0, time.UTC)
	registerFakeTool(t, fakeAsks{call: asks.Call{Tool: fakeTool, ID: "call-1", AskedAt: asked, Questions: fakeQuestions}})
	sess := store.Session{ID: "child001", Name: "banner-child", Tool: fakeTool, AgentSessionID: "conv"}

	body, key, urgent := childDialogBody(sess, "FAKE-DIALOG")
	for _, want := range []string{
		"a dialog asking 2 questions",
		"Which color should the banner use?", "Red -- A bold banner", "How big should it be?",
		"Fake CLI resolves this question by itself", "at about 20:12:39 UTC",
		"type as its own answer instead", "give its answers entry ticks",
		"It submits the dialog once every question has an answer",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("relay does not say %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "answer_session cannot tick") {
		t.Errorf("a multi-select this CLI can tick was handed to a person:\n%s", body)
	}
	if !strings.HasPrefix(key, "q:") || !urgent {
		t.Errorf("key %q urgent %v; want a question key sent at once, since the question expires", key, urgent)
	}
}

func TestAQuestionTheCLIDroppedIsRelayedAsLost(t *testing.T) {
	at := time.Date(2026, 9, 30, 20, 14, 11, 0, time.UTC)
	lost := asks.Result{Call: asks.Call{ID: "call-9", Questions: fakeQuestions[:1]}, Outcome: asks.Expired, At: at}
	registerFakeTool(t, fakeAsks{lost: &lost})
	sess := store.Session{ID: "child001", Name: "banner-child", Tool: fakeTool, AgentSessionID: "conv"}

	body, key, urgent := childDialogBody(sess, "resting at its input line")
	for _, want := range []string{"expired with no answer", "at 20:14:11 UTC", "Which color should the banner use?",
		"answer_session can no longer reach it", "send_session"} {
		if !strings.Contains(body, want) {
			t.Errorf("lost-question relay does not say %q:\n%s", want, body)
		}
	}
	if key != "x:call-9" || !urgent {
		t.Errorf("key %q urgent %v; want the call's own key, sent at once", key, urgent)
	}
}

func TestTheClaudeRelayIsUnchangedByTheSeam(t *testing.T) {
	questions := dialog.Questions(askPane, nil)
	sess := store.Session{ID: "abc12345", Name: "census", Tool: "claude"}
	got := childQuestionsMessage(sess, questions)
	if !strings.Contains(got, "or your own words to type instead. It presses Submit once every question has an answer.") {
		t.Fatalf("the Claude Code relay changed wording:\n%s", got)
	}
	if strings.Contains(got, "resolves this question by itself") {
		t.Fatalf("a Claude Code question was said to expire:\n%s", got)
	}
}

func TestThePreviewCardShowsAnotherCLIsSingleQuestion(t *testing.T) {
	registerFakeTool(t, fakeAsks{})
	m := drainFleet(t)
	m.rows[m.cursor].sess.Tool = fakeTool
	sess, _ := m.selected()
	m.preview = "FAKE-DIALOG"
	m.askQuestions = map[string][]convo.AskQuestion{sess.ID: fakeQuestions[:1]}
	card := strippedRows(m.previewQuestions(50, 40))
	for _, want := range []string{"1 question · 0 answered", "Color", "Which color should the banner use?", "1. Red", "2. Green"} {
		if !strings.Contains(card, want) {
			t.Errorf("card does not show %q:\n%s", want, card)
		}
	}
}

func TestThePreviewCardQuotesAnotherCLIsPermissionPrompt(t *testing.T) {
	registerFakeTool(t, fakeAsks{})
	m := drainFleet(t)
	m.rows[m.cursor].sess.Tool = fakeTool
	m.preview = "Would you like to run the following command?\n\n$ rm -rf build\n\n❯ 1. Yes, proceed\n  2. No\n\nEnter to confirm · Esc to cancel\n"
	m.askQuestions = map[string][]convo.AskQuestion{}
	card := strippedRows(m.previewQuestions(60, 40))
	for _, want := range []string{"permission prompt · a person's to answer", "rm -rf build", "▸ 1. Yes, proceed", "2. No"} {
		if !strings.Contains(card, want) {
			t.Errorf("card does not show %q:\n%s", want, card)
		}
	}
	m.rows[m.cursor].sess.Tool = "claude"
	m.screenCard = screenCard{}
	if card := m.previewQuestions(60, 40); card != nil {
		t.Errorf("a Claude Code session's preview gained a card:\n%s", strippedRows(card))
	}
}
