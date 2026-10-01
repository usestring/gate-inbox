package dialog

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// Reading a held dialog whole, for a person to decide on.
//
// Inspect reads what a keystroke may answer and knows the legends it can key
// against. The screens a child stops on are more than that: Claude Code
// 2.1.284 draws its permission prompt, its workspace-trust dialog and its
// MCP-server dialog with legends Inspect does not take ("Esc to cancel · Tab to
// amend") and, on the two trust dialogs, choices with no numbers at all.
// A parent told only "a dialog it cannot read" goes looking at the pane, or
// waits; told the dialog itself it can put it to its user word for word.
//
// So this reads any screen that ends in a dialog legend, whatever the legend
// and whether or not its choices are numbered, and keeps it as drawn: the
// dialog's own lines from its top edge to the legend, which choice the marker
// is on, and which kind of dialog it looks like. It answers nothing.

// ScreenKind is what a held dialog is asking about.
type ScreenKind string

const (
	ScreenPermission     ScreenKind = "permission prompt"
	ScreenWorkspaceTrust ScreenKind = "workspace-trust dialog"
	ScreenMCPTrust       ScreenKind = "MCP-server trust dialog"
	ScreenQuestion       ScreenKind = "question dialog"
	ScreenElicitation    ScreenKind = "MCP server input form"
	ScreenUpdate         ScreenKind = "update prompt"
	ScreenOther          ScreenKind = "dialog"
)

// Screen is a held dialog as drawn.
type Screen struct {
	Kind ScreenKind
	// Lines are the dialog's own lines, top edge to legend, trimmed on the
	// right and with blank runs collapsed.
	Lines []string
	// Choices are the choices in screen order, a wrapped one joined back.
	Choices []ScreenChoice
	// Legend is the key legend under it, or "" for a dialog drawn without
	// one.
	Legend string
	// choiceLines are the indexes into Lines the choices were read from.
	choiceLines map[int]bool
}

// ScreenChoice is one choice of a held dialog.
type ScreenChoice struct {
	// Number is the digit drawn before it, 0 for an unnumbered choice.
	Number int
	Label  string
	// Cursor is the choice the marker is on.
	Cursor bool
}

// Cursor is the 0-based choice the marker is on, or -1.
func (s Screen) Cursor() int {
	for i, choice := range s.Choices {
		if choice.Cursor {
			return i
		}
	}
	return -1
}

// Identity names the dialog by its kind and text with the marker left out, so
// moving the cursor does not make it a different dialog.
func (s Screen) Identity() string {
	var id strings.Builder
	id.WriteString(string(s.Kind))
	for _, line := range s.Lines {
		if match := screenMarked.FindStringSubmatch(line); match != nil {
			line = match[2]
		}
		id.WriteString("\x00" + strings.TrimSpace(line))
	}
	return id.String()
}

// Prompt is what the dialog asks, without its choices: every line that is
// not a choice, in order, joined with single spaces. A word the pane broke
// across two lines comes back with a space inside it; Compact undoes that.
func (s Screen) Prompt() string {
	var kept []string
	for i, line := range s.Lines {
		if s.choiceLines[i] {
			continue
		}
		if line = strings.TrimSpace(line); line != "" {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, " ")
}

// Labels are the choices' labels in screen order.
func (s Screen) Labels() []string {
	labels := make([]string, len(s.Choices))
	for i, choice := range s.Choices {
		labels[i] = choice.Label
	}
	return labels
}

// Choose finds the choice an answer names by its text, 1-based, or 0 for an
// answer that names none of them or more than one: the label itself, or the
// start of exactly one label, compared as Compact. Never a number read as a
// position, and never a label found inside a longer answer -- "Yes, delete
// everything" is not the choice "Yes" on a dialog asking permission.
func (s Screen) Choose(answer string) int {
	return chooseLabel(s.Labels(), answer)
}

// Compact is text with every space removed and folded to lower case, the
// form two renderings of one dialog are compared in: the pane breaks a long
// path or command inside a word wherever its row ends, and the width it ends
// at is the pane's, not the dialog's.
func Compact(text string) string {
	var out strings.Builder
	for _, r := range strings.ToLower(text) {
		if !unicode.IsSpace(r) {
			out.WriteRune(r)
		}
	}
	return out.String()
}

// Text is the dialog's lines joined.
func (s Screen) Text() string { return strings.Join(s.Lines, "\n") }

// screenLegend is a key legend: segments joined by middle dots, one of which
// names Esc or Enter doing what dialogs do.
var screenLegend = regexp.MustCompile(`(?i)^[ \x{A0}]*(?:[^\x{B7}\n]+ \x{B7} )*` +
	`(?:esc to (?:cancel|exit|reject)|enter to (?:confirm|select|continue)|press enter to [^\n]+)` +
	`(?: \x{B7} [^\x{B7}\n]+)*[ \x{A0}]*$`)

var (
	screenMarked   = regexp.MustCompile(`^([ \x{A0}]*)[\x{276F}\x{203A}][ \x{A0}]+(.*)$`)
	screenNumbered = regexp.MustCompile(`^([ \x{A0}]*)(?:[\x{276F}\x{203A}][ \x{A0}]+)?(\d+)\.[ \x{A0}]+(.+)$`)
)

// ReadScreen reads the dialog a pane is holding, raw or stripped. ok is false
// when the pane's last lines are neither a dialog legend nor a numbered
// choice the marker is on.
func ReadScreen(pane string) (Screen, bool) {
	lines := strings.Split(strings.ReplaceAll(ansi.Strip(pane), "\r", ""), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t ")
	}
	end := len(lines) - 1
	for end >= 0 && strings.TrimSpace(lines[end]) == "" {
		end--
	}
	if end < 0 {
		return Screen{}, false
	}
	legendAt, legend := -1, ""
	for back := 0; back <= 2 && end-back >= 0; back++ {
		joined := strings.TrimSpace(lines[end-back])
		for _, next := range lines[end-back+1 : end+1] {
			joined += " " + strings.TrimSpace(next)
		}
		if screenLegend.MatchString(joined) {
			legendAt, legend = end-back, joined
			break
		}
	}
	if legendAt < 0 {
		// Claude Code 2.1.286 draws its WebFetch permission prompt with no
		// legend at all: the pane ends on the last numbered choice. Such a
		// pane is read as a dialog only when its last lines are a numbered run
		// from 1 with the marker on one of them, under a top edge -- a worker's
		// own numbered prose has no marker, and its input line is a bare ❯.
		if legendAt = unlabelledChoicesEnd(lines[:end+1]); legendAt < 0 {
			return Screen{}, false
		}
	}
	top := 0
	for i := legendAt - 1; i >= 0; i-- {
		if isRule(lines[i]) {
			top = i + 1
			break
		}
	}
	if legendAt-top > 40 {
		top = legendAt - 40
	}
	screen := Screen{Legend: legend}
	for _, line := range lines[top:legendAt] {
		if isSeparator(line) {
			continue
		}
		if strings.TrimSpace(line) == "" && (len(screen.Lines) == 0 || screen.Lines[len(screen.Lines)-1] == "") {
			continue
		}
		screen.Lines = append(screen.Lines, line)
	}
	for len(screen.Lines) > 0 && screen.Lines[len(screen.Lines)-1] == "" {
		screen.Lines = screen.Lines[:len(screen.Lines)-1]
	}
	if len(screen.Lines) == 0 {
		return Screen{}, false
	}
	screen.Choices, screen.choiceLines = screenChoices(screen.Lines)
	if legend == "" && len(screen.Choices) < 2 {
		return Screen{}, false
	}
	screen.Kind = screenKind(screen)
	return screen, true
}

// unlabelledChoicesEnd is len(lines) when they end in a dialog drawn with no
// legend, or -1. The last line must be a numbered choice or a wrapped
// label's continuation under one, and the numbered rows above it, up to a
// top edge, must count 1, 2, 3... with the marker on exactly one.
func unlabelledChoicesEnd(lines []string) int {
	var numbers []int
	marked, edge := 0, false
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-40; i-- {
		if isRule(lines[i]) {
			edge = true
			break
		}
		match := screenNumbered.FindStringSubmatch(lines[i])
		if match == nil {
			if len(numbers) == 0 && indentOf(lines[i]) < 4 {
				return -1
			}
			continue
		}
		n, _ := strconv.Atoi(match[2])
		numbers = append(numbers, n)
		if screenMarked.MatchString(lines[i]) {
			marked++
		}
	}
	if !edge || len(numbers) < 2 || marked != 1 {
		return -1
	}
	for i, n := range numbers {
		if n != len(numbers)-i {
			return -1
		}
	}
	return len(lines)
}

// isRule is a line drawn as a solid horizontal rule across the pane: a
// dialog's top edge.
func isRule(line string) bool {
	line = strings.TrimSpace(line)
	return len([]rune(line)) >= 10 && strings.Trim(line, "─━") == ""
}

// isSeparator is a dashed rule a dialog draws inside itself -- Claude Code
// 2.1.286 sets a permission prompt's command between two -- which is chrome,
// not text and not the dialog's top edge.
func isSeparator(line string) bool {
	line = strings.TrimSpace(line)
	return len([]rune(line)) >= 10 && strings.Trim(line, "┄┈╌") == ""
}

// screenChoices reads the choices: the numbered lines when there are any,
// each with the deeper-indented lines under it that its label wrapped onto;
// otherwise the block of lines around the marker, one choice a line, a line
// that starts in lower case taken as the one above it wrapped. used marks the
// lines the choices were read from.
func screenChoices(lines []string) (choices []ScreenChoice, used map[int]bool) {
	used = map[int]bool{}
	labelIndent := -1
	for i, line := range lines {
		if match := screenNumbered.FindStringSubmatch(line); match != nil {
			n, _ := strconv.Atoi(match[2])
			choices = append(choices, ScreenChoice{
				Number: n,
				Label:  strings.TrimSpace(match[3]),
				Cursor: screenMarked.MatchString(line),
			})
			used[i] = true
			labelIndent = len([]rune(line)) - len([]rune(strings.TrimLeft(match[3], " ")))
			continue
		}
		if len(choices) > 0 && labelIndent >= 0 && strings.TrimSpace(line) != "" &&
			indentOf(line) >= labelIndent {
			last := &choices[len(choices)-1]
			last.Label += " " + strings.TrimSpace(line)
			used[i] = true
			continue
		}
		labelIndent = -1
	}
	if len(choices) >= 2 {
		return choices, used
	}
	marked := -1
	for i, line := range lines {
		if screenMarked.MatchString(line) {
			marked = i
		}
	}
	if marked < 0 {
		return nil, map[int]bool{}
	}
	from, to := marked, marked
	for from > 0 && strings.TrimSpace(lines[from-1]) != "" {
		from--
	}
	for to+1 < len(lines) && strings.TrimSpace(lines[to+1]) != "" {
		to++
	}
	choices, used = nil, map[int]bool{}
	for i := from; i <= to; i++ {
		label := lines[i]
		cursor := false
		if match := screenMarked.FindStringSubmatch(label); match != nil {
			label, cursor = match[2], true
		}
		label = strings.TrimSpace(label)
		used[i] = true
		if first, _ := utf8.DecodeRuneInString(label); !cursor && len(choices) > 0 && unicode.IsLower(first) {
			choices[len(choices)-1].Label += " " + label
			continue
		}
		choices = append(choices, ScreenChoice{Label: label, Cursor: cursor})
	}
	return choices, used
}

func indentOf(line string) int {
	return len([]rune(line)) - len([]rune(strings.TrimLeft(line, "  ")))
}

// screenKind names the dialog by the words its harness draws in it.
func screenKind(screen Screen) ScreenKind {
	text := strings.ToLower(screen.Text() + "\n" + screen.Legend)
	switch {
	case strings.Contains(text, "new mcp server"), strings.Contains(text, "mcp server found"):
		return ScreenMCPTrust
	case strings.Contains(text, "trust this folder"), strings.Contains(text, "accessing workspace"),
		strings.Contains(text, "do you trust the contents"):
		return ScreenWorkspaceTrust
	case strings.Contains(text, "do you want to proceed"), strings.Contains(text, "requires approval"),
		strings.Contains(text, "tab to amend"), strings.Contains(text, "do you want to make this edit"),
		strings.Contains(text, "do you want to create"), strings.Contains(text, "allow once"),
		strings.Contains(text, "do you want to allow"),
		strings.Contains(text, "yes, proceed"):
		return ScreenPermission
	case strings.Contains(text, "enter to select"), strings.Contains(text, "submit answer"):
		return ScreenQuestion
	}
	return ScreenOther
}
