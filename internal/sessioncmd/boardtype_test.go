package sessioncmd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/dialog"
)

// typeSingle answers the one-question dialog on sim the way BoardType does:
// the reply typed into the free-text row, never read as an option.
func typeSingle(t *testing.T, sim *tabbedDialog, reply string) (AnsweredQuestion, error) {
	t.Helper()
	raw, err := sim.Capture()
	if err != nil {
		t.Fatal(err)
	}
	held, ok := dialog.Inspect(ansi.Strip(raw))
	if !ok || !held.TakesWords() {
		t.Fatalf("no dialog that takes words read off:\n%s", raw)
	}
	return answerHeldAs(sim, raw, held, reply, true)
}

// A snippet is the operator's own words. Typed, they land in the free-text
// row and are read back from there.
func TestTypedWordsLandInTheFreeTextRow(t *testing.T) {
	fastSettle(t)
	for _, width := range widths {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			sim := newSingleDialog(rollout, width, plan...)
			answered, err := typeSingle(t, sim, "What would you suggest?")
			if err != nil {
				t.Fatalf("type: %v (keys %v)", err, sim.keys)
			}
			if sim.answers[0] != "What would you suggest?" || answered.Selected != "" || !answered.Verified {
				t.Fatalf("registered %q, selected %q verified=%v", sim.answers[0], answered.Selected, answered.Verified)
			}
		})
	}
}

// The answer path picks an option whose label the words name; a typed
// snippet must not, or "Do both" sent as a sentence would choose for the
// operator.
func TestTypedWordsNeverPickTheOptionTheyName(t *testing.T) {
	fastSettle(t)
	sim := newSingleDialog(rollout, 60, plan...)
	if _, err := typeSingle(t, sim, "Do both"); err != nil {
		t.Fatalf("type: %v (keys %v)", err, sim.keys)
	}
	if sim.answers[0] != "Do both" {
		t.Fatalf("registered %q", sim.answers[0])
	}
	for _, key := range sim.keys {
		if key == "3" {
			t.Fatalf("keyed the option's number: %v", sim.keys)
		}
	}
}

// A question with no free-text row has nowhere for the words to go, so the
// board types nothing rather than letting them fall on an option.
func TestBoardTypeRefusesAQuestionWithoutAFreeTextRow(t *testing.T) {
	h := newSessionHarness(t)
	child := childShowing(t, h, "", "child105", "orphan", boardAskPane)
	_, err := h.sessions.BoardType(child.ID, "What would you suggest?")
	if err == nil || !strings.Contains(err.Error(), "free-text row") {
		t.Fatalf("BoardType err = %v, want the free-text refusal", err)
	}
}

func TestBoardTypeRefusesAPermissionPrompt(t *testing.T) {
	h := newSessionHarness(t)
	child := childShowing(t, h, "", "child106", "approval", codexApprovalFixture)
	if _, err := h.sessions.BoardType(child.ID, "Yes, proceed"); err == nil {
		t.Fatal("BoardType typed into a permission prompt")
	}
}
