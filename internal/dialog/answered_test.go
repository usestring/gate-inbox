package dialog

import (
	"fmt"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// A child that lays its choices out as a numbered list before asking puts
// "1. ... 2. ... 3." over the dialog's own rows. Read as options, those
// shifted every index the answer path keyed. Captured from 2.1.284.
func TestInspectReadsOnlyTheDialogsOwnRowsUnderANumberedList(t *testing.T) {
	want := []string{"Hold for staged UI", "Only region #4021", "Do both", "Type something.", "Chat about this"}
	for _, name := range []string{
		"w40-prose-single", "w50-prose-single", "w60-prose-single",
		"w40-prose-tabs-first", "w50-prose-tabs-first", "w60-prose-tabs-first",
	} {
		t.Run(name, func(t *testing.T) {
			held, ok := Inspect(ansi.Strip(fixture(t, "claude-2.1.284-"+name+".ansi")))
			if !ok {
				t.Fatal("no dialog read")
			}
			if fmt.Sprint(held.Options) != fmt.Sprint(want) {
				t.Fatalf("options = %q, want %q", held.Options, want)
			}
			if held.Cursor != 1 || held.FreeText != 4 {
				t.Errorf("cursor = %d free text = %d, want 1 and 4", held.Cursor, held.FreeText)
			}
			if held.Prompt != "Which rollout path should I take?" {
				t.Errorf("prompt = %q", held.Prompt)
			}
			for i, label := range want[:3] {
				if got := held.Choose(label); got != i+1 {
					t.Errorf("Choose(%q) = %d, want %d", label, got, i+1)
				}
			}
			if keys := SelectKeys(held.Cursor, held.Choose("Do both")); fmt.Sprint(keys) != "[Down Down Enter]" {
				t.Errorf("keys for Do both = %v", keys)
			}
		})
	}
}

// A description's own "N." is not a row, and a list that does not start at 1
// never shifts the dialog's.
func TestInspectIgnoresNumbersOutOfSequence(t *testing.T) {
	pane := "Earlier:\n  4. something else\n────────\nPick one\n\n❯ 1. Alpha\n     2026. was a year\n  2. Beta\n  3. Type something.\n\nEnter to select · ↑/↓ to navigate · Esc to cancel\n"
	held, ok := Inspect(pane)
	if !ok || fmt.Sprint(held.Options) != "[Alpha Beta Type something.]" || held.Prompt != "Pick one" {
		t.Fatalf("read %+v (%v)", held, ok)
	}
}

func TestParseAnsweredReadsWhatTheChildTook(t *testing.T) {
	for name, want := range map[string][]ReviewAnswer{
		"w50-single-after": {{"Which rollout path should I take?", "Do both"}},
		"w60-single-after": {{"Which rollout path should I take?", "Do both"}},
		"w40-single-free-after": {{"Which rollout path should I take?",
			"Ship region #4021 first, then staged UI next week once the rework is green"}},
		"w50-tabs-after-submit": {
			{"Which rollout path should I take?", "Do both"},
			{"Should I notify the channel when it lands?", "Only after the deploy is verified in prod, then post"},
		},
		"w40-tabs-after-submit": {
			{"Which rollout path should I take?", "Only region #4021"},
			{"Should I notify the channel when it lands?", "Only after the deploy is verified in prod, then post in the channel"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			blocks := ParseAnswered(ansi.Strip(fixture(t, "claude-2.1.284-"+name+".ansi")))
			if len(blocks) != 1 || fmt.Sprint(blocks[0]) != fmt.Sprint(want) {
				t.Fatalf("read %q, want %q", blocks, want)
			}
		})
	}
	for _, name := range []string{"w50-prose-single", "w40-prose-tabs-first"} {
		if blocks := ParseAnswered(ansi.Strip(fixture(t, "claude-2.1.284-"+name+".ansi"))); len(blocks) != 0 {
			t.Errorf("%s: read %q off a pane still asking", name, blocks)
		}
	}
}

// An answer wider than the pane wraps under its arrow on the Submit page,
// and the readback compares all of it.
func TestParseReviewJoinsAWrappedAnswer(t *testing.T) {
	got, ok := ParseReview(ansi.Strip(fixture(t, "claude-2.1.284-w50-tabs-review-complete.ansi")))
	if !ok || !got.Complete || len(got.Answers) != 2 {
		t.Fatalf("read %+v (%v)", got, ok)
	}
	if got.Answers[1].Answer != "Only after the deploy is verified in prod, then post" {
		t.Errorf("answer = %q", got.Answers[1].Answer)
	}
	partial, ok := ParseReview(ansi.Strip(fixture(t, "claude-2.1.284-w50-tabs-review-partial.ansi")))
	if !ok || partial.Complete || len(partial.Answers) != 1 || partial.Answers[0].Answer != "Do both" {
		t.Errorf("partial review = %+v (%v)", partial, ok)
	}
}

// Typed words replace the free-text row's label, wrapped over as many rows
// as they need; the row is still the dialog's fourth and no option moves.
func TestInspectReadsWordsTypedIntoTheFreeTextRow(t *testing.T) {
	held, ok := Inspect(ansi.Strip(fixture(t, "claude-2.1.284-w40-free-text-typed.ansi")))
	if !ok || held.Cursor != 4 || len(held.Options) != 5 || held.Options[2] != "Do both" {
		t.Fatalf("read %+v (%v)", held, ok)
	}
	on, ok := Inspect(ansi.Strip(fixture(t, "claude-2.1.284-w40-on-free-text.ansi")))
	if !ok || on.Cursor != 4 || on.FreeText != 4 {
		t.Fatalf("on the free-text row read %+v (%v)", on, ok)
	}
}

func TestSameQuestionToleratesAPromptCutShort(t *testing.T) {
	if !SameQuestion("Which rollout path\nshould I take?", "Which rollout path should I take?") ||
		!SameQuestion("should I take?", "Which rollout path should I take?") ||
		SameQuestion("", "Which rollout path should I take?") ||
		SameQuestion("Should I notify the channel?", "Which rollout path should I take?") {
		t.Fatal("SameQuestion")
	}
}

// The list above this dialog repeats its labels exactly, so every label
// matched twice and Choose gave up: "Do both" went in as typed words, which
// the dialog drops, and the Enter after them took the highlighted option.
func TestALabelRepeatedAboveTheDialogStillPicksTheOption(t *testing.T) {
	held, ok := Inspect(ansi.Strip(fixture(t, "claude-2.1.284-w50-prose-duplicates.ansi")))
	if !ok || held.Choose("Do both") != 3 || held.Choose("Only region #4021") != 2 {
		t.Fatalf("read %+v (%v)", held, ok)
	}
	blocks := ParseAnswered(ansi.Strip(fixture(t, "claude-2.1.284-w50-typed-answer-took-highlighted.ansi")))
	if len(blocks) != 1 || blocks[0][0].Answer != "Hold for staged UI" {
		t.Fatalf("record = %q, want the highlighted option the typed answer fell onto", blocks)
	}
}
