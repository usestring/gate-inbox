package extension

import (
	"slices"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/dialog"
)

// DialogKind is which harness drew a dialog and which of its dialogs it is,
// told apart by the legend the harness closes it with.
type DialogKind string

const (
	// DialogAsk is Claude Code's AskUserQuestion.
	DialogAsk = DialogKind(dialog.KindAsk)
	// DialogCodexAsk is Codex's request_user_input, which answers the same
	// way.
	DialogCodexAsk = DialogKind(dialog.KindCodexAsk)
	// DialogCodexTrust is Codex's first-run directory-trust prompt. It is
	// guarded.
	DialogCodexTrust = DialogKind(dialog.KindCodexTrust)
	// DialogApproval is a permission prompt on either harness. It is
	// guarded.
	DialogApproval = DialogKind(dialog.KindApproval)
)

// Dialog is a question dialog as it stood on a pane when it was read. It is
// a copy: changing it changes nothing on the pane, and the pane is the only
// place a later state can be read from.
type Dialog struct {
	Kind DialogKind
	// Prompt is what was asked, as the pane shows it.
	Prompt string
	// Options are the numbered choices in screen order.
	Options []string
	// Cursor is the 1-based option the selection is on, or 0 where the pane
	// does not say.
	Cursor int
	// MultiSelect marks a dialog whose choices are checkboxes.
	MultiSelect bool
	// Steps is how many questions one dialog is asking, and Answered how
	// many of them are ticked; both 0 for an ordinary single question.
	Steps    int
	Answered int
}

// ParseDialog reads the dialog a pane is holding that an answer could be
// given to. ok is false for a pane holding none, and for one holding a
// guarded dialog: those are a person's to answer.
//
// pane is a capture of the visible screen; terminal escapes are stripped
// first, so a capture taken with them reads the same as one taken without.
func ParseDialog(pane string) (Dialog, bool) {
	held, ok := dialog.Parse(ansi.Strip(pane))
	return fromDialog(held), ok
}

// InspectDialog reads whatever dialog a pane is holding, guarded ones
// included, for a caller that has to say why it will not answer. ok is
// false only where the pane holds no dialog at all.
func InspectDialog(pane string) (Dialog, bool) {
	held, ok := dialog.Inspect(ansi.Strip(pane))
	return fromDialog(held), ok
}

// Guarded marks a dialog that belongs to a person whatever it says: a
// permission prompt or a first-run trust dialog.
func (d Dialog) Guarded() bool { return d.parsed().Guarded() }

// Refusal is why no answer from the board may be keyed into the dialog, as
// a phrase, or "" when one may.
func (d Dialog) Refusal() string { return d.parsed().Refusal() }

// Standing is how many of the dialog's questions are still unanswered.
func (d Dialog) Standing() int { return d.parsed().Standing() }

// Question is the dialog as prose, fit to put to whoever is to answer it:
// the prompt, the choices, and how many questions are still standing.
func (d Dialog) Question() string { return d.parsed().Question() }

// Choices is the dialog's own options, without the rows the harness appends
// to every dialog, and at most max of them.
func (d Dialog) Choices(max int) []string { return d.parsed().Choices(max) }

// Choose is the 1-based option answer names, or 0 for an answer that names
// none of them and would be typed instead.
func (d Dialog) Choose(answer string) int { return d.parsed().Choose(answer) }

func fromDialog(d dialog.Dialog) Dialog {
	return Dialog{
		Kind:        DialogKind(d.Kind),
		Prompt:      d.Prompt,
		Options:     slices.Clone(d.Options),
		Cursor:      d.Cursor,
		MultiSelect: d.MultiSelect,
		Steps:       d.Steps,
		Answered:    d.Answered,
	}
}

func (d Dialog) parsed() dialog.Dialog {
	return dialog.Dialog{
		Kind:        dialog.Kind(d.Kind),
		Prompt:      d.Prompt,
		Options:     d.Options,
		Cursor:      d.Cursor,
		MultiSelect: d.MultiSelect,
		Steps:       d.Steps,
		Answered:    d.Answered,
	}
}
