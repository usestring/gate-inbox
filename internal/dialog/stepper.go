package dialog

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Reading the tabs of a several-question AskUserQuestion.
//
// Claude Code draws a call asking several questions as one dialog with a row
// of tabs above the question on the screen, captured from 2.1.283:
//
//	←  ☒ Tooling  ☐ Compliance  ☐ npm publish  ☐ Run tests  ✔ Submit  →
//
// One entry per question, ☐ until it is answered and ☒ after, then the Submit
// tab, whose page reviews the answers and sends them. Right and Left (or Tab)
// move between tabs without touching an answer; picking a choice answers the
// question and moves on to the next tab by itself. The labels are the
// questions' headers cut to fit: at 50 columns the row above reads
// "☐ Tooling  ☐ Com…  ☐ npm…  ☐ Run…", the tab being shown is drawn with its
// label as whole as the width allows and the others shrink around it, and at
// 44 columns the row itself can wrap onto a second line. So the labels are a
// hint and nothing is keyed off them; the glyphs are what is counted.
//
// Which tab is being shown is marked only by the background colour it is
// drawn on, so that needs the raw capture. A stripped one still says how many
// questions there are and which are answered.

// Step is one question's tab.
type Step struct {
	// Label is the tab's text as drawn, which may be cut short with "…" or
	// be empty on a pane narrow enough to wrap the row.
	Label string
	// Answered is a tab drawn ☒.
	Answered bool
}

// Stepper is the row of tabs a several-question dialog draws.
type Stepper struct {
	// Steps are the question tabs in order, Submit not included.
	Steps []Step
	// Active is the 0-based tab being shown: len(Steps) is the Submit tab,
	// and -1 is a capture that does not say (a stripped one).
	Active int
}

// OnSubmit reports whether the Submit tab is the one being shown.
func (s Stepper) OnSubmit() bool { return s.Active == len(s.Steps) }

// AllAnswered reports whether every question tab is ticked.
func (s Stepper) AllAnswered() bool {
	for _, step := range s.Steps {
		if !step.Answered {
			return false
		}
	}
	return len(s.Steps) > 0
}

// Unanswered is how many question tabs are not ticked.
func (s Stepper) Unanswered() int {
	n := 0
	for _, step := range s.Steps {
		if !step.Answered {
			n++
		}
	}
	return n
}

const (
	glyphPending  = '☐' // ☐
	glyphChecked  = '☑' // ☑, drawn by older releases for an answered tab
	glyphAnswered = '☒' // ☒
	glyphSubmit   = '✔' // ✔
)

// stepperStart is the stripped row's opening: the left arrow, then an entry.
var stepperStart = regexp.MustCompile(`^[ \x{A0}]*\x{2190}[ \x{A0}]+[\x{2610}-\x{2612}\x{2714}]`)

// ParseStepper reads the tab row out of a pane, raw or stripped. ok is false
// for a pane with no such row, which is every single-question dialog.
func ParseStepper(pane string) (Stepper, bool) {
	for _, raw := range strings.Split(pane, "\n") {
		plain := ansi.Strip(raw)
		if !stepperStart.MatchString(plain) || !strings.ContainsRune(plain, glyphSubmit) {
			continue
		}
		return readStepperRow(raw)
	}
	return Stepper{}, false
}

var sgrRun = regexp.MustCompile(`\x1b\[([0-9;:]*)m`)

// readStepperRow walks one row of the capture, glyph by glyph, noting where
// the first background colour starts: the entry after it is the active one.
func readStepperRow(raw string) (Stepper, bool) {
	highlight := -1 // byte offset into the stripped row
	var plain strings.Builder
	rest := raw
	for len(rest) > 0 {
		loc := sgrRun.FindStringSubmatchIndex(rest)
		if loc == nil {
			plain.WriteString(ansi.Strip(rest))
			break
		}
		plain.WriteString(ansi.Strip(rest[:loc[0]]))
		if highlight < 0 && setsBackground(rest[loc[2]:loc[3]]) {
			highlight = plain.Len()
		}
		rest = rest[loc[1]:]
	}
	row := plain.String()
	stepper := Stepper{Active: -1}
	type entry struct {
		at    int
		glyph rune
	}
	var entries []entry
	for at, r := range row {
		switch r {
		case glyphPending, glyphChecked, glyphAnswered, glyphSubmit:
			entries = append(entries, entry{at, r})
		}
	}
	if len(entries) < 2 || entries[len(entries)-1].glyph != glyphSubmit {
		return Stepper{}, false
	}
	for i, e := range entries {
		end := len(row)
		if i+1 < len(entries) {
			end = entries[i+1].at
		}
		label := row[e.at+len(string(e.glyph)) : end]
		label = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(label), "→"))
		if highlight >= 0 && stepper.Active < 0 && highlight <= e.at {
			stepper.Active = i
		}
		if e.glyph == glyphSubmit {
			continue
		}
		stepper.Steps = append(stepper.Steps, Step{
			Label:    label,
			Answered: e.glyph != glyphPending,
		})
	}
	return stepper, true
}

// setsBackground reports whether one SGR's parameters set a background
// colour: 40-47, 100-107, or the 48 that introduces an indexed or truecolour
// one. Which colour is not read, only that there is one.
func setsBackground(params string) bool {
	for _, field := range strings.FieldsFunc(params, func(r rune) bool { return r == ';' || r == ':' }) {
		code, err := strconv.Atoi(field)
		if err != nil {
			continue
		}
		if code == 48 || (code >= 40 && code <= 47) || (code >= 100 && code <= 107) {
			return true
		}
	}
	return false
}

// Review is the Submit tab's page: each question with the answer it will
// send, and the two choices under them.
//
//	Review your answers
//	 │ ● Which package manager should the new workspace standardise on for every
//	 │   JavaScript package?
//	   → pnpm
//	 ● Should the release pipeline block on the licence compliance scan?
//	   → Warn for a month, then block
//	Ready to submit your answers?
//	❯ 1. Submit answers
//	  2. Cancel
//
// It draws no legend, so Inspect does not read it as a dialog; this is its
// reader.
type Review struct {
	// Answers are the questions answered so far, in order.
	Answers []ReviewAnswer
	// Complete is false while the page warns that not every question has an
	// answer.
	Complete bool
	// Cursor is the 1-based choice the marker is on: 1 is "Submit answers".
	Cursor int
	// Submit is the 1-based row of "Submit answers", 0 when it is not drawn.
	Submit int
}

// ReviewAnswer is one question on the review page and its answer.
type ReviewAnswer struct {
	Question string
	Answer   string
}

// ParseReview reads the Submit tab's page off a stripped pane.
func ParseReview(pane string) (Review, bool) {
	lines := strings.Split(pane, "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "Review your answers" {
			start = i
		}
	}
	if start < 0 {
		return Review{}, false
	}
	review := Review{Complete: true}
	var current *ReviewAnswer
	inAnswer := false
	for _, line := range lines[start+1:] {
		text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "│"))
		switch {
		case text == "":
			continue
		case strings.HasPrefix(text, "⚠"):
			review.Complete = false
		case strings.HasPrefix(text, "● "):
			review.Answers = append(review.Answers, ReviewAnswer{Question: strings.TrimSpace(text[len("● "):])})
			current, inAnswer = &review.Answers[len(review.Answers)-1], false
		case strings.HasPrefix(text, "→ ") && current != nil && !inAnswer:
			current.Answer = strings.TrimSpace(text[len("→ "):])
			inAnswer = true
		case text == "Ready to submit your answers?":
			current, inAnswer = nil, false
		case inAnswer && current != nil && !askOption.MatchString(line):
			// An answer longer than the pane is wide wraps under its arrow.
			current.Answer += " " + text
		case askOption.MatchString(line):
			match := askOption.FindStringSubmatch(line)
			n, _ := strconv.Atoi(match[2])
			if match[1] != "" {
				review.Cursor = n
			}
			if normalise(match[3]) == "submit answers" {
				review.Submit = n
			}
		case current != nil && current.Answer == "":
			current.Question += " " + text
		}
	}
	if review.Submit == 0 {
		return Review{}, false
	}
	return review, true
}
