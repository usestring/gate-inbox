package dialog

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/convo"
)

// An AskUserQuestion whose first option carries a preview, captured live on
// Claude Code 2.1.286: options on the left, the preview boxed on the right,
// and at 40 and 50 columns the first option's number drawn on its second row.
func TestAPreviewDialogReadsAsItsOptions(t *testing.T) {
	for _, width := range []int{40, 50, 60} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			pane := ansi.Strip(readScreenFixture(t, fmt.Sprintf("claude-2.1.286-w%d-preview.ansi", width)))
			held, ok := Inspect(pane)
			if !ok {
				t.Fatal("no dialog read")
			}
			if held.Kind != KindAsk || held.Guarded() || held.Refusal() != "" {
				t.Fatalf("kind %q refusal %q", held.Kind, held.Refusal())
			}
			if strings.Join(held.Options, "|") != "Yes, push and open the PR|No, hold" {
				t.Fatalf("options = %q", held.Options)
			}
			if held.Cursor != 1 || held.Prompt != "May I push the branch and open the PR?" {
				t.Fatalf("cursor %d prompt %q", held.Cursor, held.Prompt)
			}
			if keys, err := AnswerKeys(held, "No, hold"); err != nil || fmt.Sprint(keys) != "[Down Enter]" {
				t.Fatalf("keys %v err %v", keys, err)
			}
		})
	}
}

// The relay carries every option's preview whole, from the call, where the
// pane shows a few clipped rows of it.
func TestQuestionsCarryThePreviewWhole(t *testing.T) {
	preview := "$ git push -u origin s-1\n$ gh pr create --title \"Ship it\" --body-file body.md\n\n## Summary\nEvery line kept."
	asked := []convo.AskQuestion{{Header: "Approval", Question: "May I push the branch and open the PR?",
		Options: []convo.AskOption{{Label: "Yes, push and open the PR", Preview: preview}, {Label: "No, hold"}}}}
	questions := Questions(readScreenFixture(t, "claude-2.1.286-w40-preview.ansi"), asked)
	if len(questions) != 1 || questions[0].Options[0].Preview != preview {
		t.Fatalf("questions = %+v", questions)
	}
	rendered := RenderQuestions(questions)
	for _, line := range strings.Split(preview, "\n") {
		if !strings.Contains(rendered, "| "+line) {
			t.Errorf("render lacks %q:\n%s", line, rendered)
		}
	}
}
