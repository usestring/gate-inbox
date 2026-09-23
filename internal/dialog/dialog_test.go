package dialog

import (
	"reflect"
	"strings"
	"testing"
)

// capturedAskPane is an AskUserQuestion as Claude Code drew it, ANSI stripped,
// which is the form the event carries. The legend under the options is what
// tells it from a permission prompt.
const capturedAskPane = `● I have read the 909 payload and both paths look viable.

  Which approach should I try first?

  ❯ 1. Reproduce the crypto natively
    2. Keep the vendor solver for now
    3. Ask the vendor for the algorithm

  Enter to select · ↑/↓ to navigate · Esc to cancel
`

// capturedPermissionPane is the other arrow dialog, and stays the dialog
// guard: whether this program may run a command is the human's to answer.
const capturedPermissionPane = `● Bash(rm -rf build/)

  Do you want to proceed?
  ❯ 1. Yes
    2. No, and tell Claude what to do differently

  Enter to confirm · Esc to cancel
`

func TestParseReadsTheQuestionAndItsOptions(t *testing.T) {
	dialog, ok := Parse(capturedAskPane)
	if !ok {
		t.Fatal("a captured AskUserQuestion pane did not parse")
	}
	want := []string{
		"Reproduce the crypto natively",
		"Keep the vendor solver for now",
		"Ask the vendor for the algorithm",
	}
	if !reflect.DeepEqual(dialog.Options, want) {
		t.Fatalf("options = %q, want %q", dialog.Options, want)
	}
	if dialog.Cursor != 1 {
		t.Fatalf("cursor = %d, want the marked option", dialog.Cursor)
	}
	if !strings.Contains(dialog.Prompt, "Which approach should I try first?") {
		t.Fatalf("prompt = %q", dialog.Prompt)
	}
}

func TestParseRefusesWhatIsNotAnAskUserQuestion(t *testing.T) {
	for name, pane := range map[string]string{
		"a permission prompt": capturedPermissionPane,
		"a settled prompt":    settledPane,
		"an empty pane":       "",
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := Parse(pane); ok {
				t.Fatal("parsed as an AskUserQuestion")
			}
		})
	}
}

// The manager is shown the question and the choices in the words the worker
// put on the screen, and told it may answer with either.
func TestTheQuestionCarriesTheChoices(t *testing.T) {
	dialog, _ := Parse(capturedAskPane)
	question := dialog.Question()
	for _, want := range []string{
		"Which approach should I try first?",
		"1. Reproduce the crypto natively",
		"3. Ask the vendor for the algorithm",
		"or with your own words",
	} {
		if !strings.Contains(question, want) {
			t.Fatalf("question is missing %q:\n%s", want, question)
		}
	}
}

func TestChooseMapsAnAnswerOntoAnOption(t *testing.T) {
	dialog, _ := Parse(capturedAskPane)
	for _, that := range []struct {
		name   string
		answer string
		want   int
	}{
		{"the option verbatim", "Keep the vendor solver for now", 2},
		{"case and spacing do not matter", "  reproduce   the CRYPTO natively ", 1},
		{"a prefix of one option", "Ask the vendor", 3},
		{"the option inside a sentence", "Go with: Keep the vendor solver for now.", 2},
		{"words of its own", "Try the native path but time-box it to an hour", 0},
		{"nothing", "", 0},
	} {
		t.Run(that.name, func(t *testing.T) {
			if got := dialog.Choose(that.answer); got != that.want {
				t.Fatalf("Choose(%q) = %d, want %d", that.answer, got, that.want)
			}
		})
	}
}

// An answer that matches two options is no answer: picking one would choose
// something nobody chose.
func TestChooseRefusesAnAmbiguousAnswer(t *testing.T) {
	dialog := Dialog{Options: []string{"Retry the request", "Retry the request twice"}, Cursor: 1}
	if got := dialog.Choose("Retry the request"); got != 1 {
		t.Fatalf("Choose = %d; an exact match should still win over a prefix", got)
	}
	if got := dialog.Choose("Retry"); got != 0 {
		t.Fatalf("Choose = %d, want the ambiguous prefix to fall back to typing", got)
	}
}

func TestSelectKeysWalksToTheOption(t *testing.T) {
	for _, that := range []struct {
		name     string
		at, want int
		keys     []string
	}{
		{"already there", 2, 2, []string{"Enter"}},
		{"down two", 1, 3, []string{"Down", "Down", "Enter"}},
		{"back up", 3, 1, []string{"Up", "Up", "Enter"}},
		{"nothing to pick", 1, 0, nil},
		{"cursor unknown", 0, 2, nil},
	} {
		t.Run(that.name, func(t *testing.T) {
			if got := selectKeys(that.at, that.want); !reflect.DeepEqual(got, that.keys) {
				t.Fatalf("selectKeys(%d, %d) = %v, want %v", that.at, that.want, got, that.keys)
			}
		})
	}
}

// The harness appends its own rows to every dialog; they are not the worker's
// choices, and the cap gives way on the worker's list rather than overflowing.
func TestChoicesLeaveOutTheHarnesssOwnRows(t *testing.T) {
	dialog := Dialog{Options: []string{"Add a Bash allow-rule", "You run the scripts",
		"Type something.", "Chat about this"}}
	want := []string{"Add a Bash allow-rule", "You run the scripts"}
	if got := dialog.Choices(3); !reflect.DeepEqual(got, want) {
		t.Fatalf("choices = %v, want %v", got, want)
	}
	if got := (Dialog{Options: []string{"Type something", "Chat about this"}}).Choices(3); got != nil {
		t.Fatalf("choices = %v, want none: there is nothing of the worker's to offer", got)
	}
	capped := Dialog{Options: []string{"one", "two", "three", "four", "five"}}
	if got := capped.Choices(3); !reflect.DeepEqual(got, []string{"one", "two", "three"}) {
		t.Fatalf("choices = %v, want the first three", got)
	}
}
