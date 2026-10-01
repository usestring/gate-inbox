package dialog

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// Three Approval questions of 570 to 1036 characters that answer_session
// reported unconfirmed on 2026-10-01 although the child took the answer,
// replayed on live Claude Code 2.1.286 panes: the dialog, and the record the
// child printed after option 1 was picked. At 50 columns the dialog and the
// record break the same long paths at different places, more often than
// the wrap readings can try, and the question read as missing from the
// record.
func TestALongQuestionIsFoundInItsRecordAtEveryWidth(t *testing.T) {
	answers := map[string]string{"mine": "Push and open the PR", "dc2": "Yes, push and open the PR", "dc3": "Yes, open it via the wrapper"}
	for key, answer := range answers {
		for _, width := range []int{50, 137} {
			if key == "mine" && width == 50 {
				continue // its header has scrolled off; see TestARecordWithItsHeaderScrolledOffIsNotRead
			}
			t.Run(fmt.Sprintf("%s/%d", key, width), func(t *testing.T) {
				held, ok := Inspect(ansi.Strip(readScreenFixture(t, fmt.Sprintf("claude-2.1.286-w%d-long-%s-dialog.ansi", width, key))))
				if !ok || held.Options[0] != answer {
					t.Fatalf("dialog read %v %q", ok, held.Options)
				}
				blocks := ParseAnswered(ansi.Strip(readScreenFixture(t, fmt.Sprintf("claude-2.1.286-w%d-long-%s-answered-screen.ansi", width, key))))
				if len(blocks) == 0 || len(blocks[len(blocks)-1]) != 1 {
					t.Fatalf("record read as %+v", blocks)
				}
				entry := blocks[len(blocks)-1][0]
				if !SameQuestion(entry.Question, held.Prompt) {
					t.Errorf("the record's question does not match the dialog's prompt:\n%q\n%q", entry.Question, held.Prompt)
				}
				if !SameText(entry.Answer, answer) {
					t.Errorf("answer %q, want %q", entry.Answer, answer)
				}
				if SameText(entry.Answer, held.Options[1]) {
					t.Errorf("the record's answer matches the other option %q too", held.Options[1])
				}
			})
		}
	}
}

// A record taller than the pane is redrawn without its header, and 2.1.286
// leaves none of it in scrollback, so the pane alone cannot show the answer;
// the transcript has to.
func TestARecordWithItsHeaderScrolledOffIsNotRead(t *testing.T) {
	for _, name := range []string{"answered-screen", "answered-history"} {
		pane := ansi.Strip(readScreenFixture(t, "claude-2.1.286-w50-long-mine-"+name+".ansi"))
		if !strings.Contains(pane, "→ Push and open the PR") {
			t.Fatalf("%s: the fixture lost the record's tail", name)
		}
		if blocks := ParseAnswered(pane); len(blocks) != 0 {
			t.Fatalf("%s: read %d blocks off a pane with no header", name, len(blocks))
		}
	}
}

func TestSameTextStillTellsLabelsApart(t *testing.T) {
	for _, pair := range [][2]string{{"Push only", "Push and open the PR"}, {"No, hold it", "Yes, push and open the PR"}, {"Hold", "Hold for review"}} {
		if SameText(pair[0], pair[1]) {
			t.Errorf("SameText(%q, %q)", pair[0], pair[1])
		}
	}
	if !SameQuestion("May I write /var/sample-​0000/x? (y)", "write /var/sample- 0000/x? (y)") {
		t.Error("a question broken at different places does not match")
	}
	if SameQuestion("May I delete cache-w4?", "delete cache-w40?") {
		t.Error("a different question matches")
	}
}
