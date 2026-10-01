package dialog

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/convo"
)

// The questions of a pending dialog, every one of them, for a parent that has
// to decide which it can settle itself and which to put to its own user.
//
// The pane shows one question of a several-question dialog at a time. What
// was asked comes from the transcript when there is one, which spells out
// every header, question and option; the pane says which are answered and
// which is on the screen. Without a transcript only the question on the
// screen can be read in full, and the rest are known by their tab labels.

// Option is one choice a question offers.
type Option struct {
	Label       string `json:"label" jsonschema:"the choice's text; pass it as the answer to pick it"`
	Description string `json:"description,omitempty" jsonschema:"what the child said the choice means"`
	Checked     bool   `json:"checked,omitempty" jsonschema:"on a multi-select question on the screen, whether its box is ticked"`
}

// Question is one question of the dialog a session is holding.
type Question struct {
	Index       int      `json:"index" jsonschema:"1-based position in the dialog; answer_session's answers accept it as question"`
	Header      string   `json:"header,omitempty" jsonschema:"the question's short header, drawn as its tab label; answer_session's answers accept it as question"`
	Question    string   `json:"question" jsonschema:"the full question; empty only for a question not on the screen when no transcript could be read"`
	Options     []Option `json:"options,omitempty" jsonschema:"the child's own choices in order, without the dialog's Type something and Chat about this rows"`
	MultiSelect bool     `json:"multi_select,omitempty" jsonschema:"true for a question answered by ticking several boxes; answer it with ticks, the labels to leave ticked"`
	Answered    bool     `json:"answered" jsonschema:"whether this question already has an answer in the dialog"`
	Answer      string   `json:"answer,omitempty" jsonschema:"the answer it has, when the pane shows it"`
	OnScreen    bool     `json:"on_screen,omitempty" jsonschema:"the question the dialog is showing now"`
}

// Questions reads every question of the dialog pane is holding. pane may be
// raw or stripped; raw is better, because only its colours say which tab is
// on the screen. asked is the pending call out of the transcript, or nil.
//
// Nil for a pane holding no question dialog.
func Questions(pane string, asked []convo.AskQuestion) []Question {
	plain := ansi.Strip(pane)
	held, isDialog := Inspect(plain)
	review, isReview := ParseReview(plain)
	if !isReview && (!isDialog || held.Guarded()) {
		return nil
	}
	if isDialog && held.Kind != KindAsk {
		question := screenQuestion(1, "", held)
		markTicked(&question, held)
		return []Question{question}
	}
	stepper, tabbed := ParseStepper(pane)
	count := 1
	if tabbed {
		count = len(stepper.Steps)
	}
	var out []Question
	if len(asked) == count {
		for i, q := range asked {
			question := Question{Index: i + 1, Header: q.Header, Question: q.Question, MultiSelect: q.MultiSelect}
			for _, option := range q.Options {
				question.Options = append(question.Options, Option{Label: option.Label, Description: option.Description})
			}
			out = append(out, question)
		}
	} else {
		for i := range count {
			question := Question{Index: i + 1}
			if tabbed {
				question.Header = stepper.Steps[i].Label
			}
			out = append(out, question)
		}
	}
	for i := range out {
		if tabbed {
			out[i].Answered = stepper.Steps[i].Answered
		}
		for _, answer := range review.Answers {
			if out[i].Question != "" && SameText(answer.Question, out[i].Question) {
				out[i].Answer, out[i].Answered = Readable(answer.Answer), true
			}
		}
	}
	if !isDialog {
		return out
	}
	// Which question the options on the screen belong to: the highlighted tab
	// on a raw capture, else the one whose text is the prompt, else the only
	// question there is.
	on := -1
	switch {
	case !tabbed:
		on = 0
	case stepper.Active >= 0 && stepper.Active < len(out):
		on = stepper.Active
	default:
		for i := range out {
			if out[i].Question != "" && samePrompt(held.Prompt, out[i].Question) {
				on = i
			}
		}
	}
	if on < 0 {
		return out
	}
	shown := screenQuestion(on+1, out[on].Header, held)
	out[on].OnScreen = true
	if out[on].Question == "" {
		out[on].Question, out[on].Options, out[on].MultiSelect = shown.Question, shown.Options, shown.MultiSelect
	}
	if held.Picked > 0 && held.Picked <= len(held.Options) {
		out[on].Answer, out[on].Answered = held.Options[held.Picked-1], true
	}
	markTicked(&out[on], held)
	return out
}

// markTicked copies a multi-select's checked boxes off the screen onto the
// question's options, matched by label.
func markTicked(question *Question, held Dialog) {
	for _, n := range held.Ticked {
		if n < 1 || n > len(held.Options) {
			continue
		}
		for i := range question.Options {
			if normalise(question.Options[i].Label) == normalise(held.Options[n-1]) {
				question.Options[i].Checked = true
			}
		}
	}
}

// screenQuestion is the question on the screen, read off the pane alone.
func screenQuestion(index int, header string, held Dialog) Question {
	question := Question{Index: index, Header: header, Question: joinLines(held.Prompt), MultiSelect: held.MultiSelect, OnScreen: true}
	for _, choice := range held.Choices(len(held.Options)) {
		question.Options = append(question.Options, Option{Label: choice})
	}
	return question
}

// samePrompt reports whether a prompt read off a pane, wrapped to its width,
// is the question text.
func samePrompt(prompt, question string) bool {
	return normalise(joinLines(prompt)) == normalise(question)
}

func joinLines(text string) string { return strings.Join(strings.Fields(text), " ") }

// Resolve finds which question ref names: its 1-based index, its header, or
// its text. A header matches a tab label cut short with "…" by its start, and
// a question's text by being contained in it; each only when exactly one
// question matches, because answering the wrong question is worse than
// asking which one was meant.
func Resolve(questions []Question, ref string) (int, error) {
	ref = strings.TrimSpace(ref)
	if n, err := strconv.Atoi(strings.TrimPrefix(ref, "#")); err == nil {
		if n < 1 || n > len(questions) {
			return 0, fmt.Errorf("question %d does not exist; the dialog asks %d", n, len(questions))
		}
		return n - 1, nil
	}
	want := normalise(ref)
	if want == "" {
		return 0, fmt.Errorf("question is empty; name it by its index (1 to %d), header or text", len(questions))
	}
	for _, match := range []func(Question) bool{
		func(q Question) bool { return normalise(q.Header) == want },
		func(q Question) bool { return normalise(q.Question) == want },
		func(q Question) bool {
			header := normalise(strings.TrimSuffix(q.Header, "…"))
			return header != "" && (strings.HasPrefix(want, header) && strings.HasSuffix(q.Header, "…") ||
				strings.HasPrefix(header, want))
		},
		func(q Question) bool { return q.Question != "" && strings.Contains(normalise(q.Question), want) },
	} {
		found, count := 0, 0
		for i, q := range questions {
			if match(q) {
				found, count = i, count+1
			}
		}
		if count == 1 {
			return found, nil
		}
		if count > 1 {
			return 0, fmt.Errorf("%q matches more than one question; name it by its index", ref)
		}
	}
	return 0, fmt.Errorf("%q names none of the dialog's questions; name one by its index (1 to %d), header or text", ref, len(questions))
}

// Choose finds the option an answer names on a question, 1-based, or 0 for
// words that are none of them. The same rule as Dialog.Choose, over the
// question's own labels.
func (q Question) Choose(answer string) int {
	labels := make([]string, len(q.Options))
	for i, option := range q.Options {
		labels[i] = option.Label
	}
	return Dialog{Options: labels}.Choose(answer)
}

// RenderQuestions is the questions as text, for a message: each with its
// header, whether it is answered, and its choices with what the child said
// they mean, in the child's words.
func RenderQuestions(questions []Question) string {
	var out strings.Builder
	for i, q := range questions {
		if i > 0 {
			out.WriteString("\n")
		}
		fmt.Fprintf(&out, "Question %d of %d", q.Index, len(questions))
		if q.Header != "" {
			fmt.Fprintf(&out, " [%s]", q.Header)
		}
		switch {
		case q.Answered && q.Answer != "":
			fmt.Fprintf(&out, " -- already answered: %s", q.Answer)
		case q.Answered:
			out.WriteString(" -- already answered")
		}
		if q.MultiSelect {
			out.WriteString(" -- multi-select: answer with ticks")
		}
		out.WriteString("\n")
		if q.Question != "" {
			out.WriteString(q.Question + "\n")
		} else {
			out.WriteString("(not on the screen, and no transcript to read it from)\n")
		}
		for n, option := range q.Options {
			box := ""
			if q.MultiSelect && q.OnScreen {
				box = "[ ] "
				if option.Checked {
					box = "[x] "
				}
			}
			fmt.Fprintf(&out, "  %d. %s%s", n+1, box, option.Label)
			if option.Description != "" {
				fmt.Fprintf(&out, " -- %s", option.Description)
			}
			out.WriteString("\n")
		}
	}
	return out.String()
}
