// Package relayask builds the input a parent passes to its own AskUserQuestion
// to relay a child's dialog to its user word for word.
//
// A relayed answer is admitted only when the parent's own dialog asks the
// child's question with its options word for word (sessioncmd's relay.go).
// Parents were left to copy that by hand, and a third of their answer_session
// calls came back refused: a reworded question, a dropped option, a header put
// to the user as its own question. Input is the copy made for them, from the
// same record the check reads, so passing it as is is the one input the check
// accepts.
package relayask

import (
	"encoding/json"
	"strings"

	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/logging"
)

// Input is AskUserQuestion's input: questions in the shape that tool takes,
// each the child's own text.
type Input struct {
	Questions []convo.AskQuestion `json:"questions" jsonschema:"pass this object as is to your own AskUserQuestion; each question is the child's, word for word, with its header, options and multiSelect"`
}

// JSON is the input as one line, ready to pass.
func (in *Input) JSON() string {
	raw, err := json.Marshal(in)
	if err != nil {
		return ""
	}
	return string(raw)
}

// OptionLimit is how many options AskUserQuestion takes.
const OptionLimit = 4

// For is the Input for the questions of a dialog still standing: each from the
// child's own call where it has one, which is what the relay check reads, and
// from the screen where it does not. Nil when no question is left to ask.
func For(asked []convo.AskQuestion, shown []dialog.Question) *Input {
	out := &Input{}
	for i, q := range shown {
		if q.Answered {
			continue
		}
		if i < len(asked) && (q.Question == "" || dialog.SameQuestion(asked[i].Question, q.Question)) {
			out.Questions = append(out.Questions, askInput(asked[i]))
			continue
		}
		if q.Question == "" || len(q.Options) == 0 {
			continue
		}
		built := convo.AskQuestion{Header: q.Header, Question: q.Question, MultiSelect: q.MultiSelect}
		for _, option := range q.Options {
			built.Options = append(built.Options, convo.AskOption{Label: option.Label, Description: option.Description})
		}
		out.Questions = append(out.Questions, askInput(built))
	}
	if len(out.Questions) == 0 {
		return nil
	}
	return out
}

// askInput is q as AskUserQuestion takes it: the id and previews the child's
// call carried are not part of the match and are left out, and an option with
// no description is described by its label, because the tool requires one.
func askInput(q convo.AskQuestion) convo.AskQuestion {
	out := convo.AskQuestion{Header: q.Header, Question: q.Question, MultiSelect: q.MultiSelect}
	if strings.TrimSpace(out.Header) == "" {
		out.Header = "Question"
	}
	for _, option := range q.Options {
		description := option.Description
		if strings.TrimSpace(description) == "" {
			description = option.Label
		}
		out.Options = append(out.Options, convo.AskOption{Label: option.Label, Description: description})
	}
	return out
}

// ForScreen is the Input for a permission, trust or other choices dialog: its
// text as the question and its choices as the options, credentials scrubbed
// as the relay shows them, which is what the screen relay check reads. A
// dialog with more choices than the tool takes offers the first four, which
// that check accepts. Nil for a question dialog, which For reads, and for a
// screen with fewer than two choices.
func ForScreen(screen dialog.Screen) *Input {
	labels := screen.Labels()
	if screen.Kind == dialog.ScreenQuestion || len(labels) < 2 {
		return nil
	}
	q := convo.AskQuestion{Header: dialog.ApprovalHeader, Question: logging.ScrubWrapped(screen.Prompt())}
	for i, label := range labels {
		if i == OptionLimit {
			break
		}
		q.Options = append(q.Options, convo.AskOption{Label: logging.ScrubWrapped(label),
			Description: "Pick this on the child's " + string(screen.Kind)})
	}
	return &Input{Questions: []convo.AskQuestion{q}}
}

// Line is the sentence a relay message ends on: the input to pass, or "" when
// there is none.
func Line(in *Input, sessionID string) string {
	if in == nil {
		return ""
	}
	return "\n\nTo put it to your user, pass this as is to your own AskUserQuestion, then answer_session on session " +
		sessionID + " with relay: true and the option they pick:\n" + in.JSON()
}
