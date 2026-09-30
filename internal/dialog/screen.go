package dialog

import (
	"regexp"
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
	// Legend is the key legend under it.
	Legend string
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
// when the pane's last lines are not a dialog legend.
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
		return Screen{}, false
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
	screen.Choices = screenChoices(screen.Lines)
	screen.Kind = screenKind(screen)
	return screen, true
}

// isRule is a line drawn as a horizontal rule across the pane: a dialog's top
// edge.
func isRule(line string) bool {
	line = strings.TrimSpace(line)
	return len([]rune(line)) >= 10 && strings.Trim(line, "─━┄┈╌") == ""
}

// screenChoices reads the choices: the numbered lines when there are any,
// each with the deeper-indented lines under it that its label wrapped onto;
// otherwise the block of lines around the marker, one choice a line, a line
// that starts in lower case taken as the one above it wrapped.
func screenChoices(lines []string) []ScreenChoice {
	var choices []ScreenChoice
	labelIndent := -1
	for _, line := range lines {
		if match := screenNumbered.FindStringSubmatch(line); match != nil {
			n := 0
			for _, r := range match[2] {
				n = n*10 + int(r-'0')
			}
			choices = append(choices, ScreenChoice{
				Number: n,
				Label:  strings.TrimSpace(match[3]),
				Cursor: screenMarked.MatchString(line),
			})
			labelIndent = len([]rune(line)) - len([]rune(strings.TrimLeft(match[3], " ")))
			continue
		}
		if len(choices) > 0 && labelIndent >= 0 && strings.TrimSpace(line) != "" &&
			indentOf(line) >= labelIndent {
			last := &choices[len(choices)-1]
			last.Label += " " + strings.TrimSpace(line)
			continue
		}
		labelIndent = -1
	}
	if len(choices) >= 2 {
		return choices
	}
	marked := -1
	for i, line := range lines {
		if screenMarked.MatchString(line) {
			marked = i
		}
	}
	if marked < 0 {
		return nil
	}
	from, to := marked, marked
	for from > 0 && strings.TrimSpace(lines[from-1]) != "" {
		from--
	}
	for to+1 < len(lines) && strings.TrimSpace(lines[to+1]) != "" {
		to++
	}
	choices = nil
	for i := from; i <= to; i++ {
		label := lines[i]
		cursor := false
		if match := screenMarked.FindStringSubmatch(label); match != nil {
			label, cursor = match[2], true
		}
		label = strings.TrimSpace(label)
		if first, _ := utf8.DecodeRuneInString(label); !cursor && len(choices) > 0 && unicode.IsLower(first) {
			choices[len(choices)-1].Label += " " + label
			continue
		}
		choices = append(choices, ScreenChoice{Label: label, Cursor: cursor})
	}
	return choices
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
		strings.Contains(text, "yes, proceed"):
		return ScreenPermission
	case strings.Contains(text, "enter to select"), strings.Contains(text, "submit answer"):
		return ScreenQuestion
	}
	return ScreenOther
}
