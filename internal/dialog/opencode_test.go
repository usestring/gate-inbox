package dialog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/convo"
)

func opencodeCapture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "opencode", "opencode-2.0.3-"+name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

var bannerAsked = []convo.AskQuestion{
	{Header: "Color", Question: "Which color should the banner use?",
		Options: []convo.AskOption{{Label: "Red"}, {Label: "Green"}, {Label: "Blue"}}},
	{Header: "Features", Question: "Which features should ship?", MultiSelect: true,
		Options: []convo.AskOption{{Label: "Search"}, {Label: "Export"}, {Label: "Sharing"}}},
	{Header: "Name", Question: "What should the banner say?",
		Options: []convo.AskOption{{Label: "Welcome"}, {Label: "Hello"}}},
}

func TestOpencodeQuestionDialogsReadAtEveryWidth(t *testing.T) {
	for _, tc := range []struct {
		capture string
		prompt  string
		labels  string
		multi   bool
		checked string
		field   int
		tabs    string
	}{
		{"q3-t1-w40.txt", "Which color should the banner use?", "Red|Green|Blue|Type your own answer", false, "", 1, ""},
		{"q3-t1-w50.txt", "Which color should the banner use?", "Red|Green|Blue|Type your own answer", false, "", 0, "Color|Features|Name"},
		{"q3-t1-w60.txt", "Which color should the banner use?", "Red|Green|Blue|Type your own answer", false, "", 0, "Color|Features|Name"},
		{"q3-t1-w100.txt", "Which color should the banner use?", "Red|Green|Blue|Type your own answer", false, "", 0, "Color|Features|Name"},
		{"q3-t2-custom-typed-w100.txt", "Which features should ship?", "Search|Export|Sharing|Dark mode", true, "2,3,4", 0, "Color|Features|Name"},
		{"q1-w40.txt", "Which environment should the next deploy target?", "Staging|Production|Canary|Type your own answer", false, "", 0, ""},
		{"q1-w50.txt", "Which environment should the next deploy target?", "Staging|Production|Canary|Type your own answer", false, "", 0, ""},
		{"q1-w60.txt", "Which environment should the next deploy target?", "Staging|Production|Canary|Type your own answer", false, "", 0, ""},
		{"multi1-ticked-w40.txt", "Which checks should CI run?", "Lint|Unit tests|E2E tests|Type your own answer", true, "1,3", 0, "Checks"},
		{"multi1-w50.txt", "Which checks should CI run?", "Lint|Unit tests|E2E tests|Type your own answer", true, "", 0, "Checks"},
		{"multi1-w60.txt", "Which checks should CI run?", "Lint|Unit tests|E2E tests|Type your own answer", true, "", 0, "Checks"},
	} {
		t.Run(tc.capture, func(t *testing.T) {
			ask, ok := ParseOpencodeAsk(opencodeCapture(t, tc.capture))
			if !ok {
				t.Fatal("not read as a question dialog")
			}
			var labels, checked []string
			for _, option := range ask.Options {
				labels = append(labels, option.Label)
				if option.Checked {
					checked = append(checked, strings.TrimSpace(string(rune('0'+option.Number))))
				}
			}
			if ask.Prompt != tc.prompt || strings.Join(labels, "|") != tc.labels || ask.Multi != tc.multi ||
				strings.Join(checked, ",") != tc.checked || ask.Field != tc.field || strings.Join(ask.Tabs, "|") != tc.tabs ||
				ask.OnSubmit {
				t.Fatalf("read %+v", ask)
			}
		})
	}
}

func TestOpencodeReviewPagesReadEachAnswer(t *testing.T) {
	for _, capture := range []string{"q3-review-w40.txt", "q3-review-w50.txt", "q3-review-w60.txt", "q3-review-w100.txt"} {
		ask, ok := ParseOpencodeAsk(opencodeCapture(t, capture))
		if !ok || !ask.OnSubmit || len(ask.Review) != 3 || ask.Review[1].Answer != "Export, Sharing, Dark mode" ||
			ask.Review[2].Answer != "Howdy, team" {
			t.Errorf("%s read %+v", capture, ask.Review)
		}
	}
	ask, ok := ParseOpencodeAsk(opencodeCapture(t, "q2-review-none-w50.txt"))
	if !ok || len(ask.Review) != 2 || ask.Review[0].Answer != OpencodeNotAnswered {
		t.Fatalf("unanswered review = %+v", ask.Review)
	}
}

func TestOpencodeQuestionsMergeTheStoreWithTheScreen(t *testing.T) {
	reading, ok := ReadQuestions("opencode", opencodeCapture(t, "q3-t2-custom-typed-w100.ansi"), bannerAsked)
	if !ok || len(reading.Questions) != 3 || !reading.Questions[1].OnScreen || !reading.Questions[1].MultiSelect {
		t.Fatalf("reading = %+v", reading.Questions)
	}
	checked := 0
	for _, option := range reading.Questions[1].Options {
		if option.Checked {
			checked++
		}
	}
	if checked != 2 {
		t.Errorf("ticked boxes carried = %d, want Export and Sharing", checked)
	}
	narrow, ok := ReadQuestions("opencode", opencodeCapture(t, "q3-t1-w40.txt"), bannerAsked)
	if !ok || !narrow.Questions[0].OnScreen {
		t.Fatalf("the narrow heading's question was not placed: %+v", narrow.Questions)
	}
	review, ok := ReadQuestions("opencode", opencodeCapture(t, "q3-review-w60.txt"), bannerAsked)
	if !ok || !review.OnSubmit || !review.Questions[2].Answered || review.Questions[2].Answer != "Howdy, team" {
		t.Fatalf("review reading = %+v", review)
	}
	if _, ok := ReadQuestions("opencode", opencodeCapture(t, "perm-bash-w60.txt"), nil); ok {
		t.Fatal("a permission ask was read as a question")
	}
}

func TestOpencodePermissionAsksAreReadWhole(t *testing.T) {
	for _, tc := range []struct {
		capture, detail string
	}{
		{"perm-bash-w40", "$ echo hello-from-probe"},
		{"perm-bash-w100", "$ echo hello-from-probe"},
		{"perm-edit-w50", "Edit notes.txt"},
		{"perm-webfetch-w60", "URL: https://example.com"},
		{"perm-extdir-w40", "Access external directory"},
	} {
		for _, ext := range []string{".txt", ".ansi"} {
			screen, ok := ReadScreenFor("opencode", opencodeCapture(t, tc.capture+ext))
			if !ok || screen.Kind != ScreenPermission || len(screen.Choices) != 3 || screen.Choices[2].Label != "Reject" ||
				!strings.Contains(screen.Text(), tc.detail) || !strings.Contains(screen.Legend, "select") {
				t.Errorf("%s%s read %+v", tc.capture, ext, screen)
				continue
			}
			if ext == ".ansi" && screen.Cursor() != 0 {
				t.Errorf("%s%s cursor on %d, want Allow once", tc.capture, ext, screen.Cursor())
			}
		}
	}
	screen, _ := ReadScreenFor("opencode", opencodeCapture(t, "perm-bash-reject-sel-w100.ansi"))
	if screen.Cursor() != 2 {
		t.Fatalf("selection on %d, want Reject", screen.Cursor())
	}
}
