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
	"fmt"
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

// Screen is what is known about a child's screen dialog beyond what it
// draws: who is asking, and the description its pending call gives itself.
type Screen struct {
	Child       string
	Description string
}

// ForScreen is the Input for a permission, trust or other choices dialog,
// credentials scrubbed as the relay shows them, which is what the screen
// relay check reads. A permission prompt about a command is put as a
// decision (permission below); any other dialog has its text as the
// question. Its choices are the options; a dialog with more choices than the
// tool takes offers the first four, which that check accepts. Nil for a
// question dialog, which For reads, and for a screen with fewer than two
// choices.
func ForScreen(screen dialog.Screen, about Screen) *Input {
	labels := screen.Labels()
	if screen.Kind == dialog.ScreenQuestion || len(labels) < 2 {
		return nil
	}
	if in := permission(screen, about, labels); in != nil {
		return in
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

// Permission prompts, as a decision.
//
// The question used to be the prompt's whole text, command and all, and a
// 600-character shell loop inside a question is not something anyone reads
// before answering. Now the question is who wants to run what, in the call's
// own one-line description, and what the command visibly does (Effects).
// The command goes in every option's preview, drawn whole beside the option
// the cursor is on. The question cites the prompt's PromptToken, so the
// relay check can still tell it asks about that prompt and no other, and
// the check reads the command back out of the previews: the person picked
// an option with the exact command in front of them.

func permission(screen dialog.Screen, about Screen, labels []string) *Input {
	command := screen.Command()
	if screen.Kind != dialog.ScreenPermission || command == "" {
		return nil
	}
	description := strings.TrimRight(strings.TrimSpace(about.Description), ".")
	if description == "" {
		description = strings.TrimRight(screen.Description(), ".")
	}
	who := strings.TrimSpace(about.Child)
	if who == "" {
		who = "The child"
	}
	what := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(screen.Title()), "command"))
	if what == "" {
		what = "tool"
	}
	var question strings.Builder
	if description != "" {
		fmt.Fprintf(&question, "%s wants to run a %s command: %q. ", who, what, logging.ScrubWrapped(description))
	} else {
		fmt.Fprintf(&question, "%s wants to run a %s command. ", who, what)
	}
	if strings.EqualFold(what, "bash") {
		question.WriteString(Read(command).Summary() + " ")
	}
	fmt.Fprintf(&question, "The full command is in each option's preview %s. Allow it?", screen.PromptToken())
	preview := "```sh\n" + logging.ScrubWrapped(command) + "\n```"
	q := convo.AskQuestion{Header: dialog.ApprovalHeader, Question: question.String()}
	for i, label := range labels {
		if i == OptionLimit {
			break
		}
		q.Options = append(q.Options, convo.AskOption{Label: logging.ScrubWrapped(label),
			Description: choiceDescription(label), Preview: preview})
	}
	return &Input{Questions: []convo.AskQuestion{q}}
}

// choiceDescription says what picking a permission prompt's choice does.
func choiceDescription(label string) string {
	lower := strings.ToLower(strings.TrimSpace(label))
	switch {
	case lower == "yes":
		return "Run this command once, exactly as in the preview"
	case strings.HasPrefix(lower, "no"):
		return "Refuse it; the child is told it was refused"
	}
	return "Pick this on the child's permission prompt"
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
