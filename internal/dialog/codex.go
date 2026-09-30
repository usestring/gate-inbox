package dialog

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/convo"
)

// CodexNoneOfTheAbove is the row Codex 0.157 adds under every
// request_user_input question; its note is where typed words go.
const CodexNoneOfTheAbove = "None of the above"

// CodexAsk is a request_user_input dialog as Codex draws it:
//
//	Question 1/2 (2 unanswered) · auto-resolves in 30s
//	Which color should the banner use?
//	› 1. Red                Use a red banner.
//	  2. None of the above  Optionally, add details in notes (tab)
//	tab to add notes | enter to submit answer | ←/→ to navigate questions | esc to interrupt
type CodexAsk struct {
	Index, Total int
	// Unanswered is -1 when the heading does not say.
	Unanswered int
	Countdown  string
	Prompt     string
	Options    []CodexOption
	// Cursor is the 1-based option the › marker is on, 0 for none.
	Cursor    int
	NotesOpen bool
	Notes     string
	// Last is the legend offering to submit every answer: the question on
	// the screen is the last one.
	Last   bool
	Legend string
}

type CodexOption struct {
	Number      int
	Label       string
	Description string
}

// Other is the 1-based row of None of the above, 0 when it is not drawn.
func (a CodexAsk) Other() int {
	for _, option := range a.Options {
		if option.Label == CodexNoneOfTheAbove {
			return option.Number
		}
	}
	return 0
}

// Choose finds the option answer names, 1-based, or 0 for words that are
// none of them.
func (a CodexAsk) Choose(answer string) int {
	labels := make([]string, len(a.Options))
	for i, option := range a.Options {
		labels[i] = option.Label
	}
	return Dialog{Options: labels}.Choose(answer)
}

var (
	codexHead       = regexp.MustCompile(`^\s*Question (\d+)/(\d+)(?: \((\d+) unanswered\))?(?:\s*\x{B7}\s*(.*?))?\s*$`)
	codexOptionRow  = regexp.MustCompile(`^(\s*)(\x{203A}\s+)?(\d+)\.\s+(.*?)\s*$`)
	codexNotesRow   = regexp.MustCompile(`^\s*\x{203A}\s+(.*?)\s*$`)
	codexLegendHead = regexp.MustCompile(`(?i)^\s*(?:press enter\b|enter\b|esc\b|tab\b|ctrl\+|\x{2190}/\x{2192}|tab or esc\b)`)
	codexSplit      = regexp.MustCompile(`\S(\s{2,})\S`)
)

// ParseCodexAsk reads the request_user_input dialog off a pane, raw or
// stripped.
func ParseCodexAsk(pane string) (CodexAsk, bool) {
	lines := codexLines(pane)
	head := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if codexHead.MatchString(lines[i]) {
			head = i
			break
		}
	}
	if head < 0 {
		return CodexAsk{}, false
	}
	match := codexHead.FindStringSubmatch(lines[head])
	ask := CodexAsk{Unanswered: -1, Countdown: strings.TrimSpace(strings.TrimPrefix(match[4], "auto-resolves in"))}
	ask.Index, _ = strconv.Atoi(match[1])
	ask.Total, _ = strconv.Atoi(match[2])
	if match[3] != "" {
		ask.Unanswered, _ = strconv.Atoi(match[3])
	} else {
		ask.Unanswered = 0
	}
	i := head + 1
	var prompt []string
	for ; i < len(lines) && !codexOptionRow.MatchString(lines[i]); i++ {
		if text := strings.TrimSpace(lines[i]); text != "" {
			prompt = append(prompt, text)
		}
	}
	ask.Prompt = strings.Join(prompt, " ")
	options, next := codexOptions(lines, i)
	ask.Options = options
	for _, line := range lines[i:next] {
		if m := codexOptionRow.FindStringSubmatch(line); m != nil && m[2] != "" {
			ask.Cursor, _ = strconv.Atoi(m[3])
		}
	}
	var legend []string
	for _, line := range lines[next:] {
		text := strings.TrimSpace(line)
		switch {
		case text == "":
			continue
		case len(legend) == 0 && !codexLegendHead.MatchString(text):
			if m := codexNotesRow.FindStringSubmatch(line); m != nil {
				ask.NotesOpen, ask.Notes = true, m[1]
			}
		default:
			legend = append(legend, text)
		}
	}
	ask.Legend = strings.Join(legend, " | ")
	lower := strings.ToLower(ask.Legend)
	if !strings.Contains(lower, "enter to submit") || len(ask.Options) < 2 || ask.Total < 1 {
		return CodexAsk{}, false
	}
	if strings.Contains(lower, "clear notes") {
		ask.NotesOpen = true
	}
	ask.Last = strings.Contains(lower, "submit all")
	return ask, true
}

func codexLines(pane string) []string {
	lines := strings.Split(strings.ReplaceAll(ansi.Strip(pane), "\r", ""), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t ")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// codexOptions reads the numbered rows from lines[from], each with the lines
// its label or description wrapped onto, and returns where they end.
func codexOptions(lines []string, from int) ([]CodexOption, int) {
	var (
		options []CodexOption
		descCol = -1
		labCol  = -1
	)
	i := from
	for ; i < len(lines); i++ {
		line := lines[i]
		if m := codexOptionRow.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[3])
			if n != len(options)+1 {
				break
			}
			text := m[4]
			option := CodexOption{Number: n, Label: text}
			labCol = utf8.RuneCountInString(line) - utf8.RuneCountInString(text)
			descCol = -1
			if loc := codexSplit.FindStringSubmatchIndex(text); loc != nil {
				option.Label = strings.TrimSpace(text[:loc[2]])
				option.Description = strings.TrimSpace(text[loc[3]:])
				descCol = labCol + utf8.RuneCountInString(text[:loc[3]])
			}
			options = append(options, option)
			continue
		}
		if len(options) == 0 || strings.TrimSpace(line) == "" {
			break
		}
		indent := utf8.RuneCountInString(line) - utf8.RuneCountInString(strings.TrimLeft(line, " "))
		last := &options[len(options)-1]
		switch {
		case descCol >= 0 && indent >= descCol-1:
			last.Description = strings.TrimSpace(last.Description + " " + strings.TrimSpace(line))
		case labCol >= 0 && indent >= labCol:
			last.Label = strings.TrimSpace(last.Label + " " + strings.TrimSpace(line))
		default:
			return options, i
		}
	}
	return options, i
}

var (
	codexTitle = regexp.MustCompile(`^\s*(?:Would you like to |Field \d+/\d+|Submit with unanswered questions\?|` +
		`Trust this folder\?|Do you trust the contents|Update available|Folder access)`)
	codexMCPApproval = regexp.MustCompile(`(?i)^\s*Allow the .* MCP server to run tool`)
)

// ReadCodexScreen reads a Codex dialog that is not a request_user_input
// question: a command or edit approval, an MCP tool approval or input form,
// the folder-trust and update prompts, and the confirmation Codex asks before
// submitting a dialog with questions left unanswered.
func ReadCodexScreen(pane string) (Screen, bool) {
	lines := codexLines(pane)
	end := len(lines)
	start := end
	for start > 0 && strings.TrimSpace(lines[start-1]) != "" {
		start--
	}
	legendAt := -1
	for i := start; i < end; i++ {
		if codexLegendHead.MatchString(lines[i]) && !codexOptionRow.MatchString(lines[i]) {
			legendAt = i
			break
		}
	}
	if legendAt < 0 {
		return Screen{}, false
	}
	legend := strings.Join(trimAll(lines[legendAt:end]), " ")
	lower := strings.ToLower(legend)
	if !strings.Contains(lower, "enter") && !strings.Contains(lower, "esc") {
		return Screen{}, false
	}
	top := -1
	for i := legendAt - 1; i >= 0 && i >= legendAt-60; i-- {
		if codexTitle.MatchString(lines[i]) {
			top = i
			break
		}
		if codexHead.MatchString(lines[i]) {
			return Screen{}, false
		}
	}
	if top < 0 {
		return Screen{}, false
	}
	for back := 1; back <= 3 && top-back >= 0; back++ {
		if strings.TrimSpace(lines[top-back]) == "Folder access" {
			top -= back
		}
	}
	screen := Screen{Legend: legend}
	for _, line := range lines[top:legendAt] {
		if strings.TrimSpace(line) == "" && (len(screen.Lines) == 0 || screen.Lines[len(screen.Lines)-1] == "") {
			continue
		}
		screen.Lines = append(screen.Lines, line)
	}
	for len(screen.Lines) > 0 && screen.Lines[len(screen.Lines)-1] == "" {
		screen.Lines = screen.Lines[:len(screen.Lines)-1]
	}
	first := -1
	for i, line := range lines[top:legendAt] {
		if codexOptionRow.MatchString(line) {
			first = top + i
			break
		}
	}
	if first >= 0 {
		options, _ := codexOptions(lines[:legendAt], first)
		for i := first; i < legendAt; i++ {
			if m := codexOptionRow.FindStringSubmatch(lines[i]); m != nil && m[2] != "" {
				cursor, _ := strconv.Atoi(m[3])
				for n := range options {
					if options[n].Number == cursor {
						options[n].Label = options[n].Label + "\x00"
					}
				}
			}
		}
		for _, option := range options {
			label, cursor := strings.CutSuffix(option.Label, "\x00")
			if option.Description != "" {
				label += " -- " + option.Description
			}
			screen.Choices = append(screen.Choices, ScreenChoice{Number: option.Number, Label: label, Cursor: cursor})
		}
	}
	screen.Kind = codexScreenKind(screen)
	return screen, true
}

func trimAll(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if text := strings.TrimSpace(line); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func codexScreenKind(screen Screen) ScreenKind {
	title := strings.TrimSpace(screen.Lines[0])
	switch {
	case strings.HasPrefix(title, "Would you like to "):
		return ScreenPermission
	case strings.HasPrefix(title, "Field "):
		if codexMCPApproval.MatchString(strings.Join(trimAll(screen.Lines[1:]), " ")) {
			return ScreenPermission
		}
		return ScreenElicitation
	case strings.HasPrefix(title, "Submit with unanswered"):
		return ScreenQuestion
	case strings.HasPrefix(title, "Update available"):
		return ScreenUpdate
	}
	return ScreenWorkspaceTrust
}

// CodexUnansweredConfirm reports whether the pane shows the confirmation Codex
// asks before submitting a dialog with questions left unanswered, and the
// 1-based rows of its Proceed and Go back choices.
func CodexUnansweredConfirm(pane string) (proceed, goBack, cursor int, ok bool) {
	screen, ok := ReadCodexScreen(pane)
	if !ok || !strings.HasPrefix(strings.TrimSpace(screen.Lines[0]), "Submit with unanswered") {
		return 0, 0, 0, false
	}
	for _, choice := range screen.Choices {
		switch {
		case strings.HasPrefix(choice.Label, "Proceed"):
			proceed = choice.Number
		case strings.HasPrefix(choice.Label, "Go back"):
			goBack = choice.Number
		}
		if choice.Cursor {
			cursor = choice.Number
		}
	}
	return proceed, goBack, cursor, proceed > 0 && goBack > 0
}

func codexQuestions(pane string, asked []convo.AskQuestion) (Reading, bool) {
	ask, ok := ParseCodexAsk(pane)
	if !ok {
		if _, _, _, confirm := CodexUnansweredConfirm(pane); confirm && len(asked) > 0 {
			return Reading{Questions: fromAsked(asked), OnSubmit: true}, true
		}
		return Reading{}, false
	}
	var out []Question
	if len(asked) == ask.Total {
		out = fromAsked(asked)
	} else {
		for i := range ask.Total {
			out = append(out, Question{Index: i + 1})
		}
	}
	on := ask.Index - 1
	if on < 0 || on >= len(out) {
		return Reading{}, false
	}
	out[on].OnScreen = true
	if out[on].Question == "" || len(out[on].Options) == 0 {
		out[on].Question = ask.Prompt
		out[on].Options = nil
		for _, option := range ask.Options {
			if option.Label == CodexNoneOfTheAbove {
				continue
			}
			out[on].Options = append(out[on].Options, Option{Label: option.Label, Description: option.Description})
		}
	}
	if ask.Unanswered == 0 {
		for i := range out {
			out[i].Answered = true
		}
	}
	return Reading{Questions: out}, true
}

func fromAsked(asked []convo.AskQuestion) []Question {
	out := make([]Question, 0, len(asked))
	for i, q := range asked {
		question := Question{Index: i + 1, ID: q.ID, Header: q.Header, Question: q.Question, MultiSelect: q.MultiSelect}
		for _, option := range q.Options {
			question.Options = append(question.Options, Option{Label: option.Label, Description: option.Description})
		}
		out = append(out, question)
	}
	return out
}

func init() {
	RegisterTool("codex", ToolReader{Questions: codexQuestions, Screen: ReadCodexScreen})
}
