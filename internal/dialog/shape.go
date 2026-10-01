package dialog

import (
	"regexp"
	"strings"
)

// Which dialog a pane is holding.
//
// The parser started as a reader of Claude Code's AskUserQuestion and nothing
// else, and a pane holding any other numbered dialog came back as no dialog at
// all. On this machine's board that was every call `answer_session` ever took:
// 42 calls, 42 refusals, one sentence between them -- "not holding a question
// this can answer; it may already have been answered, or be on a permission
// prompt". A manager reading that cannot tell a question somebody else got to
// first from a shape this cannot read, so it stops calling and archives the
// child. Three did, with the answer already composed.
//
// So the kinds below are read apart, and a dialog no keystroke may answer is
// refused by name rather than by that one sentence. Each is told by the legend
// its harness closes it with, because that is the line whose wording is the
// harness's contract rather than the worker's prose.
type Kind string

const (
	// KindAsk is Claude Code's AskUserQuestion: the worker asking its caller
	// something about the work, which is the ask the manager exists for.
	KindAsk Kind = "AskUserQuestion"
	// KindCodexAsk is Codex's request_user_input, which is the same thing on
	// the other harness and answers the same way.
	KindCodexAsk Kind = "Codex request_user_input"
	// KindCodexTrust is Codex's first-run "Do you trust the contents of this
	// directory?". Numbered like a question and not one: see Refusal.
	KindCodexTrust Kind = "Codex first-run directory-trust prompt"
	// KindApproval is a permission prompt, Claude Code's or Codex's. It asks
	// whether this program may do something, which is the human's to answer.
	KindApproval Kind = "permission prompt"
)

// The legends. askLegend, in dialog.go, is the fourth and is spelled there
// because the option regex under it is the one both files share.
var (
	// codexAskLegend closes Codex's request_user_input. Both halves are
	// required: "enter to submit answer" alone appears in a worker's own prose
	// often enough, and the escape segment is what makes the line a footer.
	codexAskLegend = regexp.MustCompile(
		`(?m)^[^\n]*\benter to submit answer\b[^\n]*\besc to\b[^\n]*$`)
	// codexTrustLegend closes the first-run trust dialog, which is the one
	// Codex dialog that does not offer an escape -- there is nothing to cancel
	// back to before the directory is settled.
	codexTrustLegend = regexp.MustCompile(`(?m)^[ \x{A0}]*Press enter to continue[ \x{A0}]*$`)
	// approvalLegend closes a permission prompt on either harness. The verb is
	// what separates it from askLegend: a permission prompt confirms, and
	// AskUserQuestion selects. Claude Code 2.1.284 closes its permission prompt
	// with "Esc to cancel · Tab to amend" and no Enter at all; amending is
	// something only a command awaiting approval offers.
	approvalLegend = regexp.MustCompile(
		`(?m)^[ \x{A0}]*(?:Enter to confirm\b[^\n]*Esc to cancel|Press enter to confirm or esc to cancel|` +
			`Esc to cancel \x{B7} Tab to amend\b[^\n]*)[ \x{A0}]*$`)

	// standingLegend is the closing legend of any dialog, read or not: key
	// segments joined by middle dots, one of them Esc or Enter doing what a
	// dialog's keys do.
	standingLegend = regexp.MustCompile(`(?i)^[ \x{A0}]*(?:[^\x{B7}\n]+ \x{B7} )*` +
		`(?:esc to (?:cancel|exit|reject|interrupt)|enter to (?:confirm|select|continue|submit)|press enter to [^\n]+)` +
		`(?: \x{B7} [^\x{B7}\n]+)*[ \x{A0}]*$`)
)

// dialogLegend finds the closing legend a pane's dialog is drawn with, and
// says which kind it belongs to.
//
// Earliest on the pane wins rather than earliest in this list. A pane can
// carry an older dialog's text under the live one, so screen order is the only
// thing that says which legend the options above it belong to -- and taking
// them in a fixed order would let a settled permission prompt further down the
// scrollback decide the reading of a live question at the top.
func dialogLegend(pane string) (Kind, []int) {
	var (
		kind Kind
		at   []int
	)
	for _, shape := range []struct {
		kind   Kind
		legend *regexp.Regexp
	}{
		{KindAsk, askLegend},
		{KindCodexAsk, codexAskLegend},
		{KindCodexTrust, codexTrustLegend},
		{KindApproval, approvalLegend},
	} {
		found := shape.legend.FindStringIndex(pane)
		if found == nil || (at != nil && found[0] >= at[0]) {
			continue
		}
		kind, at = shape.kind, found
	}
	return kind, at
}

// Standing reports whether a stripped pane ends in a dialog's closing legend,
// whether or not Inspect can read the dialog above it. A pane that does is
// not at its input line, and words sent to it would land on the dialog.
func Standing(pane string) bool {
	lines := strings.Split(strings.ReplaceAll(pane, "\r", ""), "\n")
	end := len(lines) - 1
	for end >= 0 && strings.TrimSpace(lines[end]) == "" {
		end--
	}
	for back := 0; back <= 2 && end-back >= 0; back++ {
		joined := strings.TrimSpace(lines[end-back])
		for _, next := range lines[end-back+1 : end+1] {
			joined += " " + strings.TrimSpace(next)
		}
		if standingLegend.MatchString(joined) || isLegend(joined) {
			return true
		}
	}
	return false
}

// Guarded marks a dialog that belongs to a person whatever it says and
// wherever the cursor is: the answer is not this program's to give.
//
// A permission prompt asks whether the agent may do something on the user's
// machine. The first-run trust dialog is the same question one level up --
// whether to work on contents nobody has vouched for, which the harness itself
// frames as a prompt-injection risk -- and an agent clicking "Yes, continue"
// on it is the failure that dialog exists to prevent. Neither is answered
// here; both are now read, so the refusal can say which one it is.
func (d Dialog) Guarded() bool {
	return d.Kind == KindApproval || d.Kind == KindCodexTrust
}

// Refusal is why no keystroke this program sends may answer the dialog, or ""
// when one may.
//
// A phrase rather than a sentence, because callers frame it differently: a
// parent gets it back as why its call was refused. The four are worded apart because they want four different things done about them.
func (d Dialog) Refusal() string {
	switch {
	case d.Kind == KindApproval:
		return "a permission prompt -- whether the agent may take that action is a person's " +
			"call: put the prompt to your user word for word, or ask the operator to press the key " +
			"on the board; send_session is held while a dialog stands"
	case d.Kind == KindCodexTrust:
		return "Codex's first-run directory-trust prompt -- whether to work on contents nobody " +
			"has vouched for is a person's to answer, and the standing fix is the child's launch " +
			"configuration rather than a keystroke"
	case d.MultiSelect:
		return "a multi-select -- Enter ticks a box rather than answering, and nothing is " +
			"submitted until Submit is reached, so one keystroke cannot answer it"
	case d.Cursor == 0:
		return "a dialog whose selection this cannot locate -- the marker is on no row or on " +
			"more than one, and arrows counted from a position the pane never held would answer " +
			"with whatever they landed on"
	}
	return ""
}
