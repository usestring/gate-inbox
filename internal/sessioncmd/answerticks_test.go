package sessioncmd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/dialog"
)

var toppings = convo.AskQuestion{Header: "Toppings", Question: "Which toppings do you want?", MultiSelect: true,
	Options: []convo.AskOption{
		{Label: "Cheese", Description: "Melted mozzarella"}, {Label: "Olives", Description: "Black olives"},
		{Label: "Peppers", Description: "Green peppers"}, {Label: "Onions", Description: "Red onions"},
	}}

// toppingsPlan is a numbered list the child wrote above its dialog, worded
// like the dialog's own options.
var toppingsPlan = []string{"1. Cheese on everything", "2. Olives only on half", "3. Peppers, Onions"}

func newMultiDialog(width int, questions ...convo.AskQuestion) *tabbedDialog {
	d := newTabbedDialog(questions)
	d.width, d.prose = width, toppingsPlan
	return d
}

func TestTicksLeaveExactlyThoseBoxesTickedAndSubmit(t *testing.T) {
	fastSettle(t)
	for _, width := range widths {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			sim := newMultiDialog(width, toppings)
			answered, err := fillDialog(sim, questionsOf(t, sim, []convo.AskQuestion{toppings}),
				[]QuestionAnswer{{Question: "1", Ticks: []string{"Peppers", "cheese"}}}, true)
			if err != nil {
				t.Fatalf("fill: %v (keys %v)", err, sim.keys)
			}
			if !sim.closed || sim.answers[0] != "Cheese, Peppers" {
				t.Fatalf("closed %v, registered %q (keys %v)", sim.closed, sim.answers[0], sim.keys)
			}
			if !answered.Submitted || !answered.Verified || answered.Answers[0].Selected != "Cheese, Peppers" {
				t.Fatalf("answered %+v", answered)
			}
		})
	}
}

func TestTicksUntickWhatIsNotWanted(t *testing.T) {
	fastSettle(t)
	sim := newMultiDialog(50, toppings)
	sim.ticked = map[int]map[int]bool{0: {1: true, 2: true}}
	_, err := fillDialog(sim, questionsOf(t, sim, []convo.AskQuestion{toppings}),
		[]QuestionAnswer{{Question: "Toppings", Ticks: []string{"Olives", "Onions"}}}, true)
	if err != nil || sim.answers[0] != "Olives, Onions" {
		t.Fatalf("err %v, registered %q (keys %v)", err, sim.answers[0], sim.keys)
	}
	// Untick Cheese where the cursor starts, leave Olives, tick Onions, then
	// the free-text row, Submit, and Submit answers on the review page.
	if got := fmt.Sprint(sim.keys); got != "[Enter Down Down Down Enter Down Down Enter Enter]" {
		t.Errorf("keyed %v", sim.keys)
	}
}

func TestAMultiSelectAmongOtherQuestionsIsAnsweredInTheSameCall(t *testing.T) {
	fastSettle(t)
	size := convo.AskQuestion{Header: "Size", Question: "Which size?",
		Options: []convo.AskOption{{Label: "Small"}, {Label: "Large"}}}
	crust := convo.AskQuestion{Header: "Crust", Question: "Which crust?",
		Options: []convo.AskOption{{Label: "Thin"}, {Label: "Thick"}}}
	asked := []convo.AskQuestion{size, toppings, crust}
	for _, width := range widths {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			sim := newMultiDialog(width, asked...)
			answered, err := fillDialog(sim, questionsOf(t, sim, asked), []QuestionAnswer{
				{Question: "Size", Answer: "Large"},
				{Question: "Toppings", Ticks: []string{"Onions"}},
				{Question: "Crust", Answer: "Thin"},
			}, true)
			if err != nil || !answered.Submitted {
				t.Fatalf("err %v, %+v (keys %v)", err, answered, sim.keys)
			}
			if strings.Join(sim.answers, "|") != "Large|Onions|Thin" {
				t.Fatalf("registered %q", sim.answers)
			}
		})
	}
}

// A box that does not take its Enter is an error before Submit, never a
// dialog submitted with the wrong boxes.
func TestABoxThatDoesNotToggleStopsBeforeSubmit(t *testing.T) {
	fastSettle(t)
	sim := newMultiDialog(40, toppings)
	sim.stuck = 3
	_, err := fillDialog(sim, questionsOf(t, sim, []convo.AskQuestion{toppings}),
		[]QuestionAnswer{{Question: "1", Ticks: []string{"Cheese", "Peppers"}}}, true)
	if err == nil || !strings.Contains(err.Error(), `"Peppers" did not toggle`) {
		t.Fatalf("err = %v", err)
	}
	if sim.closed || sim.answers[0] != "" {
		t.Fatalf("submitted %q anyway", sim.answers[0])
	}
}

// The boxes read back as a whole: one left ticked that nobody asked for is a
// mismatch, named, before Submit.
func TestBoxesThatReadBackWrongAreAnError(t *testing.T) {
	fastSettle(t)
	sim := newMultiDialog(60, toppings)
	sim.ticked = map[int]map[int]bool{0: {4: true}}
	sim.stuck = 4
	_, err := fillDialog(sim, questionsOf(t, sim, []convo.AskQuestion{toppings}),
		[]QuestionAnswer{{Question: "1", Ticks: []string{"Cheese"}}}, true)
	if err == nil || sim.closed {
		t.Fatalf("err = %v, closed %v", err, sim.closed)
	}
}

func TestTicksAreRefusedBeforeAnyKeyWhenTheyDoNotFit(t *testing.T) {
	fastSettle(t)
	size := convo.AskQuestion{Header: "Size", Question: "Which size?", Options: []convo.AskOption{{Label: "Small"}, {Label: "Large"}}}
	for name, tc := range map[string]struct {
		asked   []convo.AskQuestion
		answers []QuestionAnswer
		says    string
	}{
		"an unknown option":         {[]convo.AskQuestion{toppings}, []QuestionAnswer{{Question: "1", Ticks: []string{"Anchovies"}}}, "names none of its options"},
		"an answer on a multi":      {[]convo.AskQuestion{toppings}, []QuestionAnswer{{Question: "1", Answer: "Cheese"}}, "give it ticks"},
		"ticks on a single choice":  {[]convo.AskQuestion{size, toppings}, []QuestionAnswer{{Question: "Size", Ticks: []string{"Small"}}}, "not a multi-select"},
		"the free-text row as tick": {[]convo.AskQuestion{toppings}, []QuestionAnswer{{Question: "1", Ticks: []string{"Type something"}}}, "names none of its options"},
	} {
		t.Run(name, func(t *testing.T) {
			sim := newMultiDialog(50, tc.asked...)
			_, err := fillDialog(sim, questionsOf(t, sim, tc.asked), tc.answers, true)
			if err == nil || !strings.Contains(err.Error(), tc.says) {
				t.Fatalf("err = %v, want it to say %q", err, tc.says)
			}
			for _, key := range sim.keys {
				if key == "Enter" {
					t.Fatalf("keyed %v", sim.keys)
				}
			}
		})
	}
}

func TestInOptionOrder(t *testing.T) {
	question := dialog.Question{Options: []dialog.Option{{Label: "Cheese"}, {Label: "Olives"}, {Label: "Peppers"}, {Label: "Onions"}}}
	got := inOptionOrder(question, []string{"onions", "Cheese", "Anchovies"})
	if strings.Join(got, "|") != "Cheese|Onions|Anchovies" {
		t.Fatalf("got %q", got)
	}
}
