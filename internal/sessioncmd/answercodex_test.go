package sessioncmd

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/asks"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/dialog"
)

type codexModel struct {
	questions []convo.AskQuestion
	index     int
	cursor    int
	notes     bool
	note      string
	confirm   bool
	confirmAt int
	answered  map[int]asks.Registered
	done      bool
	keys      []string
	lie       func(map[int]asks.Registered)
}

func newCodexModel(questions ...convo.AskQuestion) *codexModel {
	return &codexModel{questions: questions, cursor: 1, answered: map[int]asks.Registered{}}
}

func (m *codexModel) options() []string {
	var out []string
	for _, option := range m.questions[m.index].Options {
		out = append(out, option.Label)
	}
	return append(out, dialog.CodexNoneOfTheAbove)
}

func (m *codexModel) Capture() (string, error) {
	var b strings.Builder
	b.WriteString("› Ask me the release questions.\n\n• I'll ask before starting.\n\n")
	switch {
	case m.done:
		b.WriteString("• Questions answered\n\n› Ask Codex to do anything\n")
	case m.confirm:
		unanswered := len(m.questions) - len(m.answered)
		fmt.Fprintf(&b, "  Submit with unanswered questions?\n  %d unanswered question\n\n", unanswered)
		for i, row := range []string{"Proceed  Submit with unanswered questions", "Go back  Return to the first unanswered question"} {
			marker := "  "
			if m.confirmAt == i+1 {
				marker = "› "
			}
			fmt.Fprintf(&b, "  %s%d. %s\n", marker, i+1, row)
		}
		b.WriteString("\n  Press enter to confirm or esc to go back\n")
	default:
		unanswered := len(m.questions) - len(m.answered)
		fmt.Fprintf(&b, "  Question %d/%d", m.index+1, len(m.questions))
		if unanswered > 0 {
			fmt.Fprintf(&b, " (%d unanswered)", unanswered)
		}
		fmt.Fprintf(&b, "\n  %s\n\n", m.questions[m.index].Question)
		for i, label := range m.options() {
			marker := "  "
			if m.cursor == i+1 {
				marker = "› "
			}
			fmt.Fprintf(&b, "  %s%d. %-18s %s\n", marker, i+1, label, "Pick "+label+".")
		}
		b.WriteString("\n")
		last := m.index == len(m.questions)-1
		switch {
		case m.notes:
			fmt.Fprintf(&b, "  › %s\n\n  tab or esc to clear notes | enter to submit ", m.note)
		default:
			b.WriteString("  tab to add notes | enter to submit ")
		}
		if last {
			b.WriteString("all")
		} else {
			b.WriteString("answer")
		}
		b.WriteString(" | ←/→ to navigate questions | esc to interrupt\n")
	}
	return b.String(), nil
}

func (m *codexModel) Keys(keys ...string) error {
	for _, key := range keys {
		m.keys = append(m.keys, key)
		if m.confirm {
			switch {
			case key == "1" || key == "Enter" && m.confirmAt == 1:
				m.confirm, m.done = false, true
			case key == "2" || key == "Enter" && m.confirmAt == 2:
				m.confirm = false
				for i := range m.questions {
					if _, ok := m.answered[i]; !ok {
						m.index, m.cursor = i, 1
						break
					}
				}
			case key == "Down":
				m.confirmAt = min(m.confirmAt+1, 2)
			case key == "Up":
				m.confirmAt = max(m.confirmAt-1, 1)
			}
			continue
		}
		switch key {
		case "Down":
			m.cursor = min(m.cursor+1, len(m.options()))
		case "Up":
			m.cursor = max(m.cursor-1, 1)
		case "Right":
			if !m.notes {
				m.index, m.cursor = min(m.index+1, len(m.questions)-1), 1
			}
		case "Left":
			if !m.notes {
				m.index, m.cursor = max(m.index-1, 0), 1
			}
		case "Tab":
			m.notes = true
		case "Enter":
			m.take(m.cursor)
		default:
			if n, err := strconv.Atoi(key); err == nil && n >= 1 && n <= len(m.options()) {
				m.take(n)
			}
		}
	}
	return nil
}

func (m *codexModel) Type(text string) error {
	m.keys = append(m.keys, "type:"+text)
	if m.notes {
		m.note = text
	}
	m.take(m.cursor)
	return nil
}

func (m *codexModel) take(n int) {
	registered := asks.Registered{Labels: []string{m.options()[n-1]}, Note: m.note}
	m.answered[m.index] = registered
	m.notes, m.note = false, ""
	if len(m.answered) == len(m.questions) && m.index == len(m.questions)-1 {
		m.done = true
		return
	}
	if m.index == len(m.questions)-1 {
		m.confirm, m.confirmAt = true, 1
		return
	}
	m.index, m.cursor = m.index+1, 1
}

func (m *codexModel) record(call asks.Call) func(string) (asks.Result, bool) {
	return func(id string) (asks.Result, bool) {
		if id != call.ID {
			return asks.Result{}, false
		}
		if !m.done {
			return asks.Result{Call: call}, true
		}
		got := map[int]asks.Registered{}
		for k, v := range m.answered {
			got[k] = v
		}
		if m.lie != nil {
			m.lie(got)
		}
		result := asks.Result{Call: call, Outcome: asks.Answered}
		for i := range call.Questions {
			result.Answers = append(result.Answers, got[i])
		}
		return result, true
	}
}

var (
	colorQ = convo.AskQuestion{ID: "color", Header: "Color", Question: "Which color should the banner use?",
		Options: []convo.AskOption{{Label: "Red"}, {Label: "Green"}, {Label: "Blue"}}}
	sizeQ = convo.AskQuestion{ID: "size", Header: "Size", Question: "How big should the banner be?",
		Options: []convo.AskOption{{Label: "Small"}, {Label: "Large"}}}
)

func noLoss() (asks.Result, bool) { return asks.Result{}, false }

func quickSettle(t *testing.T) {
	t.Helper()
	oldSettle, oldPoll, oldReadback := settleTimeout, settlePoll, readbackTimeout
	settleTimeout, settlePoll, readbackTimeout = 200*time.Millisecond, time.Millisecond, 200*time.Millisecond
	t.Cleanup(func() { settleTimeout, settlePoll, readbackTimeout = oldSettle, oldPoll, oldReadback })
}

func runCodex(t *testing.T, m *codexModel, answers []QuestionAnswer) (AnsweredQuestion, error) {
	t.Helper()
	call := asks.Call{Tool: "codex", ID: "call_1", Questions: m.questions}
	raw, _ := m.Capture()
	return answerCodex(m, call, true, m.record(call), noLoss, raw, answers, true, nil)
}

func TestCodexBatchAnswersEveryQuestionAndReadsItBack(t *testing.T) {
	quickSettle(t)
	m := newCodexModel(colorQ, sizeQ)
	got, err := runCodex(t, m, []QuestionAnswer{{Question: "Size", Answer: "keep it subtle, medium"}, {Question: "1", Answer: "Blue"}})
	if err != nil {
		t.Fatalf("answer: %v (keys %q)", err, m.keys)
	}
	if !got.Submitted || !got.Verified || len(got.Answers) != 2 {
		t.Fatalf("result = %+v, want both answers submitted and verified", got)
	}
	if want := (asks.Registered{Labels: []string{"Blue"}}); fmt.Sprint(m.answered[0]) != fmt.Sprint(want) {
		t.Errorf("question 1 registered %+v, want %+v", m.answered[0], want)
	}
	if want := (asks.Registered{Labels: []string{dialog.CodexNoneOfTheAbove}, Note: "keep it subtle, medium"}); fmt.Sprint(m.answered[1]) != fmt.Sprint(want) {
		t.Errorf("question 2 registered %+v, want the words as the note of None of the above", m.answered[1])
	}
	if strings.Join(m.keys, ",") != "3,Down,Down,Tab,type:keep it subtle, medium" {
		t.Errorf("keys = %q", m.keys)
	}
}

func TestCodexSingleAnswerPicksANonDefaultOption(t *testing.T) {
	quickSettle(t)
	m := newCodexModel(colorQ)
	got, err := runCodex(t, m, []QuestionAnswer{{Answer: "green"}})
	if err != nil || !got.Verified || got.Selected != "Green" {
		t.Fatalf("answer = %+v, %v; want Green verified", got, err)
	}
}

func TestCodexMismatchIsAnError(t *testing.T) {
	quickSettle(t)
	m := newCodexModel(colorQ, sizeQ)
	m.lie = func(got map[int]asks.Registered) { got[1] = asks.Registered{Labels: []string{"Small"}} }
	got, err := runCodex(t, m, []QuestionAnswer{{Question: "1", Answer: "Red"}, {Question: "2", Answer: "Large"}})
	if !errors.Is(err, errWrongAnswer) || got.Verified {
		t.Fatalf("err = %v verified=%v; want a mismatch naming what Codex registered", err, got.Verified)
	}
	if !strings.Contains(err.Error(), `"Large"`) || !strings.Contains(err.Error(), `"Small"`) {
		t.Fatalf("mismatch error does not name both answers: %v", err)
	}
}

func TestCodexAnsweringOnlyTheLastQuestionLeavesTheDialogStanding(t *testing.T) {
	quickSettle(t)
	m := newCodexModel(colorQ, sizeQ)
	got, err := runCodex(t, m, []QuestionAnswer{{Question: "Size", Answer: "Small"}})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if got.Submitted || got.Verified || got.Standing != 1 || m.done {
		t.Fatalf("result = %+v done=%v; want the dialog back on its unanswered question, not submitted", got, m.done)
	}
	if last := m.keys[len(m.keys)-1]; last != "2" {
		t.Fatalf("the submit confirmation was answered %q, want Go back", last)
	}
}

func TestCodexExpiredQuestionIsReportedAsExpired(t *testing.T) {
	lost := func() (asks.Result, bool) {
		return asks.Result{Outcome: asks.Expired, At: time.Date(2026, 9, 30, 20, 14, 11, 0, time.UTC)}, true
	}
	_, err := answerCodex(newCodexModel(colorQ), asks.Call{}, false, nil, lost, "› Ask Codex to do anything\n",
		[]QuestionAnswer{{Answer: "Red"}}, true, nil)
	if !errors.Is(err, dialog.ErrNoDialog) || !strings.Contains(err.Error(), "expired") ||
		!strings.Contains(err.Error(), "20:14:11") {
		t.Fatalf("err = %v, want the question reported as expired", err)
	}
}

func TestCodexApprovalIsRefusedAsAPersonsCall(t *testing.T) {
	pane := "• Running curl https://example.com\n\n  Would you like to run the following command?\n\n" +
		"  $ curl https://example.com\n\n› 1. Yes, proceed (y)\n  2. No, and tell Codex what to do differently (esc)\n\n" +
		"  Press enter to confirm or esc to cancel\n"
	_, err := answerCodex(newCodexModel(colorQ), asks.Call{}, false, nil, noLoss, pane, []QuestionAnswer{{Answer: "Yes"}}, true, nil)
	if !errors.Is(err, dialog.ErrNotKeyAnswerable) || !strings.Contains(err.Error(), "approval prompt") {
		t.Fatalf("err = %v, want the approval refused as a person's", err)
	}
}

type asyncPane struct{ typed []string }

func (p *asyncPane) Capture() (string, error) { return "› Ask Codex to do anything\n", nil }
func (p *asyncPane) Keys(...string) error     { return nil }
func (p *asyncPane) Type(text string) error   { p.typed = append(p.typed, text); return nil }

func TestCodexAsyncQuestionIsAnsweredAsAMessage(t *testing.T) {
	quickSettle(t)
	call := asks.Call{Tool: "codex", ID: "call_async", Async: true, Questions: []convo.AskQuestion{
		{Question: "Which theme should the docs use?", Options: []convo.AskOption{{Label: "Light"}, {Label: "Dark"}}}}}
	pane := &asyncPane{}
	reply := ""
	record := func(string) (asks.Result, bool) {
		if len(pane.typed) == 0 {
			return asks.Result{Call: call}, true
		}
		return asks.Result{Call: call, Outcome: asks.Answered, Reply: reply + pane.typed[0]}, true
	}
	got, err := answerCodex(pane, call, true, record, noLoss, "", []QuestionAnswer{{Answer: "Dark"}}, true, nil)
	if err != nil || !got.Verified || len(pane.typed) != 1 || pane.typed[0] != "Dark" {
		t.Fatalf("answer = %+v, %v typed %q; want Dark sent as a message and read back", got, err, pane.typed)
	}
	pane.typed, reply = nil, "something else: "
	record2 := func(string) (asks.Result, bool) {
		if len(pane.typed) == 0 {
			return asks.Result{Call: call}, true
		}
		return asks.Result{Call: call, Outcome: asks.Answered, Reply: "Light"}, true
	}
	if _, err := answerCodex(pane, call, true, record2, noLoss, "", []QuestionAnswer{{Answer: "Dark"}}, true, nil); !errors.Is(err, errWrongAnswer) {
		t.Fatalf("err = %v, want a mismatch when the recorded message is not the answer", err)
	}
}
