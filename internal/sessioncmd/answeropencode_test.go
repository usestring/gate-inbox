package sessioncmd

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/asks"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/dialog"
)

// opencodeModel is OpenCode 2's question dialog driven by keys, drawn inside
// the composer bar the way internal/dialog/testdata/opencode shows it, and
// storing its answers the way the session store does.
type opencodeModel struct {
	questions []convo.AskQuestion
	tab       int
	picked    map[int][]string
	checked   map[int]map[int]bool
	custom    map[int]string
	typing    bool
	done      bool
	keys      []string
	lie       func(map[int][]string)
}

func newOpencodeModel(questions ...convo.AskQuestion) *opencodeModel {
	return &opencodeModel{questions: questions, picked: map[int][]string{}, checked: map[int]map[int]bool{}, custom: map[int]string{}}
}

func (m *opencodeModel) single() bool { return len(m.questions) == 1 && !m.questions[0].MultiSelect }

func (m *opencodeModel) answer(i int) []string {
	q := m.questions[i]
	if !q.MultiSelect {
		return m.picked[i]
	}
	var out []string
	for n, option := range q.Options {
		if m.checked[i][n+1] {
			out = append(out, option.Label)
		}
	}
	if text := m.custom[i]; text != "" && m.checked[i][len(q.Options)+1] {
		out = append(out, text)
	}
	return out
}

func (m *opencodeModel) Capture() (string, error) {
	var b strings.Builder
	b.WriteString("     → Asked questions\n  ┃\n")
	if m.done {
		b.WriteString("  ┃  Build · Model · 2.1s\n  ╹▀▀▀▀\n")
		return b.String(), nil
	}
	b.WriteString("  ┃  Questions\n  ┃\n")
	if !m.single() {
		var tabs []string
		for _, q := range m.questions {
			tabs = append(tabs, q.Header)
		}
		fmt.Fprintf(&b, "  ┃  %s  Submit\n  ┃\n", strings.Join(tabs, "  "))
	}
	if m.tab == len(m.questions) {
		for i, q := range m.questions {
			answer := strings.Join(m.answer(i), ", ")
			if answer == "" {
				answer = dialog.OpencodeNotAnswered
			}
			fmt.Fprintf(&b, "  ┃  %s: %s\n", q.Header, answer)
		}
		b.WriteString("  ┃\n  ┃  ⇆ tab  enter submit  esc dismiss\n  ┃\n")
		return b.String(), nil
	}
	q := m.questions[m.tab]
	fmt.Fprintf(&b, "  ┃  %s\n  ┃\n", q.Question)
	rows := append([]convo.AskOption{}, q.Options...)
	customLabel := dialog.OpencodeCustom
	if text := m.custom[m.tab]; text != "" {
		customLabel = text
	}
	rows = append(rows, convo.AskOption{Label: customLabel})
	for n, option := range rows {
		switch {
		case q.MultiSelect:
			box := " "
			if m.checked[m.tab][n+1] {
				box = "✓"
			}
			fmt.Fprintf(&b, "  ┃  %d. [%s] %s\n  ┃         Pick it.\n", n+1, box, option.Label)
		default:
			tick := ""
			if len(m.picked[m.tab]) == 1 && m.picked[m.tab][0] == option.Label {
				tick = " ✓"
			}
			fmt.Fprintf(&b, "  ┃  %d. %s%s\n  ┃     Pick it.\n", n+1, option.Label, tick)
		}
	}
	switch {
	case m.typing:
		b.WriteString("  ┃\n  ┃  ⇆ tab  enter confirm  esc close\n  ┃\n")
	case q.MultiSelect:
		b.WriteString("  ┃\n  ┃  ⇆ tab  ↑↓ select  enter toggle  esc dismiss\n  ┃\n")
	default:
		b.WriteString("  ┃\n  ┃  ⇆ tab  ↑↓ select  enter confirm  esc dismiss\n  ┃\n")
	}
	return b.String(), nil
}

func (m *opencodeModel) advance() {
	if m.single() {
		m.done = true
		return
	}
	m.tab++
}

func (m *opencodeModel) Keys(keys ...string) error {
	for _, key := range keys {
		m.keys = append(m.keys, key)
		switch key {
		case "Tab":
			m.tab = min(m.tab+1, len(m.questions))
		case "BTab":
			m.tab = max(m.tab-1, 0)
		case "Enter":
			if m.tab == len(m.questions) {
				m.done = true
			}
		default:
			n, err := strconv.Atoi(key)
			if err != nil {
				continue
			}
			q := m.questions[m.tab]
			if n == len(q.Options)+1 {
				m.typing = true
				if q.MultiSelect {
					if m.checked[m.tab] == nil {
						m.checked[m.tab] = map[int]bool{}
					}
					m.checked[m.tab][n] = true
				}
				continue
			}
			if q.MultiSelect {
				if m.checked[m.tab] == nil {
					m.checked[m.tab] = map[int]bool{}
				}
				m.checked[m.tab][n] = !m.checked[m.tab][n]
				continue
			}
			m.picked[m.tab] = []string{q.Options[n-1].Label}
			m.advance()
		}
	}
	return nil
}

func (m *opencodeModel) Type(text string) error {
	m.keys = append(m.keys, "type:"+text)
	if !m.typing {
		return nil
	}
	m.typing = false
	m.custom[m.tab] = text
	if !m.questions[m.tab].MultiSelect {
		m.picked[m.tab] = []string{text}
		m.advance()
	}
	return nil
}

func (m *opencodeModel) record(call asks.Call) func(string) (asks.Result, bool) {
	return func(id string) (asks.Result, bool) {
		if !m.done {
			return asks.Result{Call: call}, true
		}
		got := map[int][]string{}
		for i := range m.questions {
			got[i] = m.answer(i)
		}
		if m.lie != nil {
			m.lie(got)
		}
		result := asks.Result{Call: call, Outcome: asks.Answered}
		for i := range m.questions {
			result.Answers = append(result.Answers, asks.Registered{Labels: got[i]})
		}
		return result, true
	}
}

var (
	ocColor = convo.AskQuestion{Header: "Color", Question: "Which color should the banner use?",
		Options: []convo.AskOption{{Label: "Red"}, {Label: "Green"}, {Label: "Blue"}}}
	ocFeatures = convo.AskQuestion{Header: "Features", Question: "Which features should ship?", MultiSelect: true,
		Options: []convo.AskOption{{Label: "Search"}, {Label: "Export"}, {Label: "Sharing"}}}
	ocName = convo.AskQuestion{Header: "Name", Question: "What should the banner say?",
		Options: []convo.AskOption{{Label: "Welcome"}, {Label: "Hello"}}}
)

func runOpencode(t *testing.T, m *opencodeModel, answers []QuestionAnswer, submit bool) (AnsweredQuestion, error) {
	t.Helper()
	call := asks.Call{Tool: "opencode", ID: "call_q", Questions: m.questions}
	raw, _ := m.Capture()
	return answerOpencode(m, call, true, m.record(call), noLoss, raw, answers, submit, nil)
}

func TestOpencodeBatchAnswersEveryKindAndReadsItBack(t *testing.T) {
	quickSettle(t)
	m := newOpencodeModel(ocColor, ocFeatures, ocName)
	got, err := runOpencode(t, m, []QuestionAnswer{
		{Question: "Name", Answer: "Howdy, team"},
		{Question: "Features", Answer: "Sharing, Export, Dark mode"},
		{Question: "1", Answer: "Blue"},
	}, true)
	if err != nil {
		t.Fatalf("answer: %v (keys %q)", err, m.keys)
	}
	if !got.Submitted || !got.Verified || len(got.Answers) != 3 {
		t.Fatalf("result = %+v; want all three submitted and verified", got)
	}
	if a := m.answer(1); strings.Join(a, "|") != "Export|Sharing|Dark mode" {
		t.Errorf("features registered %q", a)
	}
	if a := m.answer(2); strings.Join(a, "|") != "Howdy, team" {
		t.Errorf("name registered %q, want the typed words", a)
	}
}

func TestOpencodeMismatchIsAnError(t *testing.T) {
	quickSettle(t)
	m := newOpencodeModel(ocColor, ocName)
	m.lie = func(got map[int][]string) { got[0] = []string{"Red"} }
	_, err := runOpencode(t, m, []QuestionAnswer{{Question: "Color", Answer: "Green"}, {Question: "Name", Answer: "Hello"}}, true)
	if !errors.Is(err, errWrongAnswer) || !strings.Contains(err.Error(), `"Red"`) {
		t.Fatalf("err = %v, want a mismatch naming what OpenCode stored", err)
	}
}

func TestOpencodePartialAnswerIsReadBackOffTheReviewPage(t *testing.T) {
	quickSettle(t)
	m := newOpencodeModel(ocColor, ocName)
	got, err := runOpencode(t, m, []QuestionAnswer{{Question: "2", Answer: "Welcome"}}, true)
	if err != nil {
		t.Fatalf("answer: %v (keys %q)", err, m.keys)
	}
	if got.Submitted || !got.Verified || got.Standing != 1 || m.done || m.tab != 0 {
		t.Fatalf("result = %+v done=%v tab=%d; want it read back off the review and left on question 1", got, m.done, m.tab)
	}
}

func TestOpencodeSingleQuestionIsAnsweredByItsNumber(t *testing.T) {
	quickSettle(t)
	m := newOpencodeModel(ocColor)
	got, err := runOpencode(t, m, []QuestionAnswer{{Answer: "green"}}, true)
	if err != nil || !got.Verified || got.Selected != "Green" || strings.Join(m.keys, ",") != "2" {
		t.Fatalf("answer = %+v, %v keys %q", got, err, m.keys)
	}
	m = newOpencodeModel(ocColor)
	got, err = runOpencode(t, m, []QuestionAnswer{{Answer: "Teal, please"}}, true)
	if err != nil || !got.Verified || strings.Join(m.answer(0), "") != "Teal, please" {
		t.Fatalf("typed answer = %+v, %v; registered %q", got, err, m.answer(0))
	}
}

func TestOpencodePermissionAskIsRefused(t *testing.T) {
	pane := "  ┃  △ Permission required\n  ┃    # Shell command\n  ┃\n  ┃  $ rm -rf build\n  ┃\n" +
		"  ┃   Allow once   Always allow   Reject\n  ┃\n  ┃  ctrl+f fullscreen  ⇆ select  enter confirm\n  ┃\n"
	_, err := answerOpencode(newOpencodeModel(ocColor), asks.Call{}, false, nil, noLoss, pane, []QuestionAnswer{{Answer: "Allow once"}}, true, nil)
	if !errors.Is(err, dialog.ErrNotKeyAnswerable) || !strings.Contains(err.Error(), "permission ask") {
		t.Fatalf("err = %v", err)
	}
	dismissed := func() (asks.Result, bool) { return asks.Result{Outcome: asks.Dismissed}, true }
	_, err = answerOpencode(newOpencodeModel(ocColor), asks.Call{}, false, nil, dismissed, "  ┃  Build · Model\n", []QuestionAnswer{{Answer: "Red"}}, true, nil)
	if !errors.Is(err, dialog.ErrNoDialog) || !strings.Contains(err.Error(), "dismissed") {
		t.Fatalf("err = %v, want the dismissed question named", err)
	}
}
