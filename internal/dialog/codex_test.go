package dialog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/convo"
)

func codexCapture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "codex", "codex-0.157.0-"+name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

var colorAsked = []convo.AskQuestion{
	{ID: "color", Header: "Color", Question: "Which color should the banner use?",
		Options: []convo.AskOption{{Label: "Red"}, {Label: "Green"}, {Label: "Blue"}}},
	{ID: "size", Header: "Size", Question: "How big should the banner be?",
		Options: []convo.AskOption{{Label: "Small"}, {Label: "Large"}}},
}

func TestCodexQuestionDialogsReadAtEveryWidth(t *testing.T) {
	for _, tc := range []struct {
		capture                  string
		index, total, unanswered int
		cursor                   int
		prompt                   string
		labels                   []string
		notes                    bool
		last                     bool
	}{
		{"ask2-q1-w40.txt", 1, 2, 2, 1, "Which color should the banner use?", []string{"Red", "Green", "Blue", CodexNoneOfTheAbove}, false, false},
		{"ask2-q1-w50.txt", 1, 2, 2, 1, "Which color should the banner use?", []string{"Red", "Green", "Blue", CodexNoneOfTheAbove}, false, false},
		{"ask2-q1-w60.txt", 1, 2, 2, 1, "Which color should the banner use?", []string{"Red", "Green", "Blue", CodexNoneOfTheAbove}, false, false},
		{"ask2-q1-w100.txt", 1, 2, 2, 1, "Which color should the banner use?", []string{"Red", "Green", "Blue", CodexNoneOfTheAbove}, false, false},
		{"ask2-q2-w100.txt", 2, 2, 1, 1, "How big should the banner be?", []string{"Small", "Large", CodexNoneOfTheAbove}, false, true},
		{"ask2-q2-notes-w100.txt", 2, 2, 1, 1, "How big should the banner be?", []string{"Small", "Large", CodexNoneOfTheAbove}, true, true},
		{"ask2-other-notes-w100.txt", 1, 2, 1, 3, "Which database should the service use?", []string{"Postgres", "MySQL", CodexNoneOfTheAbove}, true, false},
		{"ask2-all-answered-w50.txt", 2, 2, 0, 2, "Which cache should it use?", []string{"Redis", "Memcached", CodexNoneOfTheAbove}, false, true},
		{"ask1-w40.txt", 1, 1, 1, 1, "Which environment should the next deploy target?", []string{"Staging", "Production", "Canary", CodexNoneOfTheAbove}, false, false},
		{"ask1-w50.txt", 1, 1, 1, 1, "Which environment should the next deploy target?", []string{"Staging", "Production", "Canary", CodexNoneOfTheAbove}, false, false},
		{"ask1-w60.txt", 1, 1, 1, 1, "Which environment should the next deploy target?", []string{"Staging", "Production", "Canary", CodexNoneOfTheAbove}, false, false},
		{"ask1-countdown-w40.txt", 1, 1, 1, 1, "Which environment should the next deploy target?", []string{"Staging", "Production", "Canary", CodexNoneOfTheAbove}, false, false},
		{"ask1-countdown-w100.txt", 1, 1, 1, 1, "Which environment should the next deploy target?", []string{"Staging", "Production", "Canary", CodexNoneOfTheAbove}, false, false},
	} {
		t.Run(tc.capture, func(t *testing.T) {
			ask, ok := ParseCodexAsk(codexCapture(t, tc.capture))
			if !ok {
				t.Fatal("not read as a request_user_input dialog")
			}
			var labels []string
			for _, option := range ask.Options {
				labels = append(labels, option.Label)
			}
			if ask.Index != tc.index || ask.Total != tc.total || ask.Unanswered != tc.unanswered || ask.Cursor != tc.cursor ||
				ask.Prompt != tc.prompt || strings.Join(labels, "|") != strings.Join(tc.labels, "|") ||
				ask.NotesOpen != tc.notes || ask.Last != tc.last {
				t.Fatalf("read %+v", ask)
			}
			if ask.Other() != len(tc.labels) {
				t.Errorf("None of the above read at row %d, want %d", ask.Other(), len(tc.labels))
			}
			if !strings.HasPrefix(ask.Options[0].Description, "Use ") && !strings.HasPrefix(ask.Options[0].Description, "Target ") {
				t.Errorf("first option's description read as %q", ask.Options[0].Description)
			}
		})
	}
}

func TestCodexCountdownIsRead(t *testing.T) {
	ask, _ := ParseCodexAsk(codexCapture(t, "ask1-countdown-w100.txt"))
	if ask.Countdown != "30s" {
		t.Fatalf("countdown = %q, want 30s", ask.Countdown)
	}
}

func TestCodexQuestionsMergeTheRolloutWithTheScreen(t *testing.T) {
	reading, ok := ReadQuestions("codex", codexCapture(t, "ask2-q2-w100.txt"), colorAsked)
	if !ok || len(reading.Questions) != 2 {
		t.Fatalf("reading = %+v, %v", reading, ok)
	}
	q1, q2 := reading.Questions[0], reading.Questions[1]
	if q1.OnScreen || !q2.OnScreen || q1.ID != "color" || q2.Header != "Size" || len(q2.Options) != 2 {
		t.Fatalf("questions = %+v", reading.Questions)
	}
	screenOnly, ok := ReadQuestions("codex", codexCapture(t, "ask2-q1-w40.txt"), nil)
	if !ok || len(screenOnly.Questions) != 2 || screenOnly.Questions[0].Question != "Which color should the banner use?" ||
		len(screenOnly.Questions[0].Options) != 3 || screenOnly.Questions[1].Question != "" {
		t.Fatalf("screen-only reading = %+v", screenOnly.Questions)
	}
	confirm, ok := ReadQuestions("codex", codexCapture(t, "ask-unanswered-confirm-w40.txt"), colorAsked)
	if !ok || !confirm.OnSubmit || len(confirm.Questions) != 2 {
		t.Fatalf("the submit confirmation reads %+v, %v; want the call's questions on the submit page", confirm, ok)
	}
	if _, ok := ReadQuestions("codex", codexCapture(t, "exec-approval-w60.txt"), nil); ok {
		t.Fatal("an approval prompt was read as a question")
	}
}

func TestCodexHeldScreensAreReadWhole(t *testing.T) {
	for _, tc := range []struct {
		capture string
		kind    ScreenKind
		title   string
		choices []string
		cursor  int
	}{
		{"exec-approval-w40.txt", ScreenPermission, "Would you like to run the following", []string{"Yes, proceed (y)", "Yes, and don't ask again", "No, and tell Codex what to do differently (esc)"}, 0},
		{"exec-approval-w100.txt", ScreenPermission, "Would you like to run the following command?", []string{"Yes, proceed (y)", "Yes, and don't ask again", "No, and tell Codex"}, 0},
		{"patch-approval-w50.txt", ScreenPermission, "Would you like to make the following edits?", []string{"Yes, proceed (y)", "Yes, and don't ask again for these files (a)", "No, and tell Codex"}, 0},
		{"mcp-tool-approval-w40.txt", ScreenPermission, "Field 1/1", []string{"Allow -- Run the tool and continue", "Allow for this session", "Always allow", "Cancel -- Cancel this tool call"}, 0},
		{"elicit-f1-w60.txt", ScreenElicitation, "Field 1/2 (1 required unanswered)", nil, -1},
		{"elicit-f2-w100.txt", ScreenElicitation, "Field 2/2 (1 required unanswered)", []string{"eu-west", "us-east", "ap-south"}, 0},
		{"trust-w40.txt", ScreenWorkspaceTrust, "Folder access", []string{"Trust and continue", "Back to Agent Command Center"}, 0},
		{"update-prompt-w40.txt", ScreenUpdate, "Update available", []string{"Update now (runs `npm install -g @openai/codex`)", "Skip", "Skip until next version"}, 0},
		{"ask-unanswered-confirm-w60.txt", ScreenQuestion, "Submit with unanswered questions?", []string{"Proceed", "Go back"}, 0},
	} {
		t.Run(tc.capture, func(t *testing.T) {
			screen, ok := ReadScreenFor("codex", codexCapture(t, tc.capture))
			if !ok {
				t.Fatal("not read")
			}
			if screen.Kind != tc.kind || !strings.HasPrefix(strings.TrimSpace(screen.Lines[0]), tc.title) {
				t.Fatalf("read %s starting %q", screen.Kind, screen.Lines[0])
			}
			if len(screen.Choices) != len(tc.choices) {
				t.Fatalf("choices = %+v", screen.Choices)
			}
			for i, want := range tc.choices {
				if !strings.HasPrefix(screen.Choices[i].Label, want) {
					t.Errorf("choice %d = %q, want it to start %q", i+1, screen.Choices[i].Label, want)
				}
			}
			if tc.cursor >= 0 && screen.Cursor() != tc.cursor {
				t.Errorf("cursor on %d, want %d", screen.Cursor(), tc.cursor)
			}
			if !strings.Contains(strings.ToLower(screen.Legend), "enter") {
				t.Errorf("legend = %q", screen.Legend)
			}
		})
	}
}

func TestCodexExecApprovalKeepsTheCommand(t *testing.T) {
	screen, _ := ReadScreenFor("codex", codexCapture(t, "exec-approval-w60.txt"))
	if !strings.Contains(screen.Text(), "curl -sS -o /dev/null") || !strings.Contains(screen.Text(), "Reason: test harness network check") {
		t.Fatalf("screen text lost the command or its reason:\n%s", screen.Text())
	}
	if strings.Contains(screen.Text(), "Run exactly this shell command") {
		t.Fatalf("the screen took in the conversation above the prompt:\n%s", screen.Text())
	}
}
