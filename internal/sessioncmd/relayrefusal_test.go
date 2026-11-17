package sessioncmd

import (
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/store"
)

// Taken from parent sessions that asked the user the same child question
// twice. The user ticked options and typed a note. The parent relayed that
// answer word for word, and the child's dialog refused it before any key
// reached it, either as a tick naming no option or as text where only ticks
// fit. The ledger still marked the answer used, so the parent's corrected
// second relay came back "already been relayed once; ask them again".
func TestARelayRefusedBeforeAnyKeyLeavesTheUsersAnswerToPassOn(t *testing.T) {
	fastSettle(t)
	for name, tc := range map[string]struct {
		given  string
		answer QuestionAnswer
		says   string
	}{
		"ticks with the typed note as a tick": {
			given:  "Cheese, Olives, Extra basil please",
			answer: QuestionAnswer{Question: "1", Ticks: []string{"Cheese", "Olives", "Extra basil please"}},
			says:   "names none of its options",
		},
		"the typed answer as text on a multi-select": {
			given:  "Cheese, Olives, I already said what I want",
			answer: QuestionAnswer{Question: "1", Answer: "Cheese, Olives, I already said what I want"},
			says:   "give it ticks",
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newRelayFixture(t, toppings)
			f.parentSays(t, (&transcript{}).
				ask("toolu_parent", relayStart.Add(time.Minute), toppings).
				answer("toolu_parent", relayStart.Add(2*time.Minute), map[string]string{toppings.Question: tc.given}))
			sim := newMultiDialog(60, toppings)

			err := relayInto(t, f, sim, tc.answer)
			if err == nil || !strings.Contains(err.Error(), tc.says) {
				t.Fatalf("err = %v, want it to say %q", err, tc.says)
			}
			if len(sim.keys) > 0 {
				t.Fatalf("keyed %v", sim.keys)
			}
			if rows := f.rows(t); len(rows) != 1 || rows[0].State != store.AnswerReleased || rows[0].EvidenceToolUseID != "" {
				t.Fatalf("ledger = %+v, want the one row released, citing nothing", rows)
			}
			// What the parent does next: the same answer of the user's, passed
			// on again.
			questions := questionsOf(t, sim, []convo.AskQuestion{toppings})
			if err := f.guard(true).admit([]plannedAnswer{{0, plannedReply(questions, tc.answer)}}, questions); err != nil {
				t.Fatalf("the second relay of the same answer was refused: %v", err)
			}
		})
	}
}

// The release is only for an answer none of whose keys reached the child.
// One whose boxes were ticked and then read back wrong may have changed the
// dialog, so it stays spent, and the parent asks again rather than key it twice.
func TestARelayThatSentKeysStaysSpent(t *testing.T) {
	fastSettle(t)
	f := newRelayFixture(t, toppings)
	f.parentSays(t, (&transcript{}).
		ask("toolu_parent", relayStart.Add(time.Minute), toppings).
		answer("toolu_parent", relayStart.Add(2*time.Minute), map[string]string{toppings.Question: "Cheese"}))
	sim := newMultiDialog(60, toppings)
	sim.ticked = map[int]map[int]bool{0: {4: true}}
	sim.stuck = 4

	if err := relayInto(t, f, sim, QuestionAnswer{Question: "1", Ticks: []string{"Cheese"}}); err == nil {
		t.Fatal("boxes that read back wrong were taken as answered")
	}
	if len(sim.keys) == 0 {
		t.Fatal("no key was sent, so this does not test a partly keyed answer")
	}
	if rows := f.rows(t); len(rows) != 1 || rows[0].State != store.AnswerFailed || rows[0].EvidenceToolUseID != "toolu_parent" {
		t.Fatalf("ledger = %+v, want the one row failed, still citing toolu_parent", rows)
	}
}

// relayInto relays answer into the child on pane the way answerAll does:
// ledger rows first, then the guarded keys.
func relayInto(t *testing.T, f *relayFixture, pane dialogPane, answer QuestionAnswer) error {
	t.Helper()
	questions := questionsOf(t, pane, []convo.AskQuestion{toppings})
	g := f.guard(true)
	if err := g.admit([]plannedAnswer{{0, plannedReply(questions, answer)}}, questions); err != nil {
		t.Fatalf("admit: %v", err)
	}
	_, err := fillGuarded(pane, questions, []QuestionAnswer{answer}, true, g)
	return err
}

func plannedReply(questions []dialog.Question, answer QuestionAnswer) string {
	if len(answer.Ticks) > 0 {
		return strings.Join(inOptionOrder(questions[0], answer.Ticks), ", ")
	}
	return answer.reply()
}
