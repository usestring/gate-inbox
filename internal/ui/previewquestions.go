package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/asks"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/store"
)

// askRead is one session's last read of its transcript for a pending call.
type askRead struct {
	at        time.Time
	key       string
	path      string
	size      int64
	modTime   time.Time
	questions []convo.AskQuestion
}

// pendingAsk is the pending AskUserQuestion call's questions for a Claude
// session whose pane holds a several-question dialog, or nil. next is this
// pass's reads, which replace the previous pass's whole so a session that
// left its dialog takes its read with it.
func (p *poller) pendingAsk(sess store.Session, clean string, next map[string]*askRead) []convo.AskQuestion {
	if sess.Tool != "claude" {
		return p.pendingToolAsk(sess, clean, next)
	}
	if sess.AgentSessionID == "" || !strings.ContainsRune(clean, '✔') {
		return nil
	}
	if stepper, ok := dialog.ParseStepper(clean); !ok || len(stepper.Steps) < 2 {
		return nil
	}
	key := sess.AgentSessionID + "\x00" + sess.Cwd
	read := p.askReads[sess.ID]
	if read == nil || read.key != key || read.path == "" {
		read = &askRead{key: key, path: convo.TranscriptFor(convo.ClaudeHome(), sess.AgentSessionID, sess.Cwd)}
	}
	next[sess.ID] = read
	if read.path == "" {
		return nil
	}
	info, err := os.Stat(read.path)
	if err != nil {
		read.path, read.questions = "", nil
		return nil
	}
	if info.Size() != read.size || !info.ModTime().Equal(read.modTime) {
		read.size, read.modTime = info.Size(), info.ModTime()
		read.questions, _ = convo.PendingAsk(read.path)
	}
	return read.questions
}

const toolAskReread = 2 * time.Second

func (p *poller) pendingToolAsk(sess store.Session, clean string, next map[string]*askRead) []convo.AskQuestion {
	if sess.AgentSessionID == "" || !dialog.HasReader(sess.Tool) {
		return nil
	}
	reading, ok := dialog.ReadQuestions(sess.Tool, clean, nil)
	if !ok {
		return nil
	}
	key := sess.Tool + "\x00" + sess.AgentSessionID + "\x00" + sess.Cwd
	read := p.askReads[sess.ID]
	if read == nil || read.key != key {
		read = &askRead{key: key}
	}
	next[sess.ID] = read
	if read.questions == nil || !coversScreen(read.questions, reading.Questions) || time.Since(read.at) > toolAskReread {
		read.at = time.Now()
		read.questions = asks.PendingQuestions(asks.Target{Tool: sess.Tool, AgentSessionID: sess.AgentSessionID, Cwd: sess.Cwd})
	}
	return read.questions
}

func coversScreen(asked []convo.AskQuestion, shown []dialog.Question) bool {
	for _, q := range shown {
		if !q.OnScreen || q.Question == "" {
			continue
		}
		found := false
		for _, a := range asked {
			if dialog.SameText(a.Question, q.Question) {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// questionCard is the rendered card, kept until the pane, the call or the
// room it was drawn in changes.
type questionCard struct {
	sessID string
	pane   string
	asked  *convo.AskQuestion
	count  int
	width  int
	height int
	theme  int
	lines  []string
}

// Card detail levels, tried in order until the card fits the preview's
// height. The question on the screen always keeps everything; the others give
// up their option descriptions, then set their options on one line, then
// their question text.
const (
	cardFull = iota
	cardNoDescriptions
	cardInlineOptions
	cardHeadersOnly
)

// previewQuestions is the card for the selected session, or nil when it is
// not standing on a several-question dialog whose call the transcript holds.
// Without the call only the tab on the screen can be read in full, so the
// preview stays as it was.
func (m *Model) previewQuestions(width, height int) []string {
	sess, ok := m.selected()
	if !ok {
		return nil
	}
	asked := m.askQuestions[sess.ID]
	least := 2
	if sess.Tool != "claude" {
		least = 1
	}
	if len(asked) < least {
		if sess.Tool != "claude" {
			return m.previewScreen(width, height)
		}
		return nil
	}
	c := &m.questionCard
	if c.sessID == sess.ID && c.pane == m.preview && c.asked == &asked[0] && c.count == len(asked) &&
		c.width == width && c.height == height && c.theme == renderGen {
		return c.lines
	}
	*c = questionCard{sessID: sess.ID, pane: m.preview, asked: &asked[0], count: len(asked), width: width, height: height, theme: renderGen}
	reading, ok := dialog.ReadQuestions(sess.Tool, m.preview, asked)
	if !ok || len(reading.Questions) != len(asked) {
		return nil
	}
	for detail := cardFull; detail <= cardHeadersOnly; detail++ {
		c.lines = questionCardLines(reading.Questions, reading.OnSubmit, width, detail)
		if len(c.lines) <= height {
			break
		}
	}
	return c.lines
}

// questionCardLines draws the questions as one box the width of a
// conversation message: each question's header, whether it is answered and
// with what, the question and its options for the rest, which one the dialog
// is showing, and the Submit tab last.
func questionCardLines(questions []dialog.Question, onSubmit bool, width, detail int) []string {
	border := newFastStyle(lipgloss.NewStyle().Foreground(colorCard))
	bold := newFastStyle(lipgloss.NewStyle().Foreground(colorText).Bold(true))
	showing := newFastStyle(lipgloss.NewStyle().Foreground(colorCard).Bold(true))
	inner := max(1, width-4)
	answered := 0
	for _, q := range questions {
		if q.Answered {
			answered++
		}
	}
	var body []string
	add := func(style fastStyle, first, rest, text string) {
		room := max(1, inner-textfmt.Width(first))
		for i, piece := range textfmt.Wrap(text, room) {
			lead := rest
			if i == 0 {
				lead = first
			}
			body = append(body, lead+style.Render(piece))
		}
	}
	for i, q := range questions {
		if i > 0 && detail < cardHeadersOnly {
			body = append(body, "")
		}
		mark := "□"
		if q.Answered {
			mark = "■"
		}
		header := textfmt.OneLine(q.Header)
		if header == "" {
			header = fmt.Sprintf("Question %d", q.Index)
		}
		head := fmt.Sprintf("%s %d. %s", mark, q.Index, header)
		style := bold
		if q.OnScreen {
			style = showing
		}
		body = append(body, headLine(style.Render(head), head, q.OnScreen, inner, border))
		if q.Answered {
			answer := textfmt.OneLine(q.Answer)
			if answer == "" {
				answer = "answered"
			}
			add(doneStyle, "   → ", "     ", answer)
			continue
		}
		level := detail
		if q.OnScreen {
			level = cardFull
		}
		if text := textfmt.OneLine(q.Question); text != "" && level < cardHeadersOnly {
			add(valueStyle, "   ", "   ", text)
		}
		box := ""
		if q.MultiSelect {
			box = "[ ] "
		}
		if level >= cardInlineOptions {
			labels := make([]string, len(q.Options))
			for n, option := range q.Options {
				labels[n] = fmt.Sprintf("%d. %s%s", n+1, box, textfmt.OneLine(option.Label))
			}
			for _, line := range packItems(labels, max(1, inner-3)) {
				add(mutedStyle, "   ", "   ", line)
			}
			continue
		}
		for n, option := range q.Options {
			number := fmt.Sprintf("   %d. %s", n+1, box)
			indent := strings.Repeat(" ", textfmt.Width(number))
			add(valueStyle, number, indent, textfmt.OneLine(option.Label))
			if description := textfmt.OneLine(option.Description); description != "" && level == cardFull {
				add(mutedStyle, indent, indent, description)
			}
		}
	}
	if len(questions) < 2 {
		title := fmt.Sprintf(" 1 question · %d answered ", answered)
		title = textfmt.TruncateWidth(title, max(1, width-2), "")
		lines := []string{border.Render("╭" + title + strings.Repeat("─", max(0, width-2-textfmt.Width(title))) + "╮")}
		for _, line := range body {
			lines = append(lines, border.Render("│")+" "+padRight(line, inner)+" "+border.Render("│"))
		}
		return append(lines, border.Render("╰"+strings.Repeat("─", max(0, width-2))+"╯"))
	}
	body = append(body, "")
	submit := "▸ Submit"
	if left := len(questions) - answered; onSubmit && left > 0 {
		submit += fmt.Sprintf(" · %d unanswered", left)
	}
	style := bold
	if onSubmit {
		style = showing
	}
	body = append(body, headLine(style.Render(submit), submit, onSubmit, inner, border))

	title := fmt.Sprintf(" %d questions · %d answered ", len(questions), answered)
	title = textfmt.TruncateWidth(title, max(1, width-2), "")
	lines := []string{border.Render("╭" + title + strings.Repeat("─", max(0, width-2-textfmt.Width(title))) + "╮")}
	for _, line := range body {
		lines = append(lines, border.Render("│")+" "+padRight(line, inner)+" "+border.Render("│"))
	}
	return append(lines, border.Render("╰"+strings.Repeat("─", max(0, width-2))+"╯"))
}

// headLine is a question's header row, with the mark saying the dialog is
// showing it when there is room for one.
func headLine(styled, plain string, onScreen bool, inner int, marker fastStyle) string {
	const showing = "  ◂ on screen"
	if !onScreen || textfmt.Width(plain)+textfmt.Width(showing) > inner {
		return styled
	}
	return styled + marker.Render(showing)
}

// packItems sets items on as few lines of width as it can, two spaces apart,
// breaking between items rather than inside one that fits a line.
func packItems(items []string, width int) []string {
	var lines []string
	line := ""
	for _, item := range items {
		switch {
		case line == "":
			line = item
		case textfmt.Width(line)+2+textfmt.Width(item) <= width:
			line += "  " + item
		default:
			lines, line = append(lines, line), item
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

func (m *Model) previewScreen(width, height int) []string {
	sess, ok := m.selected()
	if !ok {
		return nil
	}
	c := &m.screenCard
	if c.sessID == sess.ID && c.pane == m.preview && c.width == width && c.height == height && c.theme == renderGen {
		return c.lines
	}
	*c = screenCard{sessID: sess.ID, pane: m.preview, width: width, height: height, theme: renderGen}
	screen, ok := dialog.ReadScreenFor(sess.Tool, ansi.Strip(m.preview))
	if !ok {
		return nil
	}
	c.lines = screenCardLines(screen, width)
	if len(c.lines) > height {
		c.lines = append(c.lines[:max(0, height-1)], c.lines[len(c.lines)-1])
	}
	return c.lines
}

type screenCard struct {
	sessID string
	pane   string
	width  int
	height int
	theme  int
	lines  []string
}

func screenCardLines(screen dialog.Screen, width int) []string {
	border := newFastStyle(lipgloss.NewStyle().Foreground(colorCard))
	bold := newFastStyle(lipgloss.NewStyle().Foreground(colorText).Bold(true))
	inner := max(1, width-4)
	var body []string
	add := func(style fastStyle, lead, text string) {
		room := max(1, inner-textfmt.Width(lead))
		for i, piece := range textfmt.Wrap(text, room) {
			if i > 0 {
				lead = strings.Repeat(" ", textfmt.Width(lead))
			}
			body = append(body, lead+style.Render(piece))
		}
	}
	marked := len(screen.Choices) > 0
	for _, line := range screen.Lines {
		if marked && screenChoiceLine(screen, line) {
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		add(valueStyle, "", textfmt.OneLine(line))
	}
	if marked {
		body = append(body, "")
		for i, choice := range screen.Choices {
			lead := fmt.Sprintf("%d. ", i+1)
			if choice.Cursor {
				lead = "▸ " + lead
			} else {
				lead = "  " + lead
			}
			style := valueStyle
			if choice.Cursor {
				style = bold
			}
			add(style, lead, textfmt.OneLine(choice.Label))
		}
	}
	title := textfmt.TruncateWidth(" "+string(screen.Kind)+" · a person's to answer ", max(1, width-2), "")
	lines := []string{border.Render("╭" + title + strings.Repeat("─", max(0, width-2-textfmt.Width(title))) + "╮")}
	for _, line := range body {
		lines = append(lines, border.Render("│")+" "+padRight(line, inner)+" "+border.Render("│"))
	}
	return append(lines, border.Render("╰"+strings.Repeat("─", max(0, width-2))+"╯"))
}

func screenChoiceLine(screen dialog.Screen, line string) bool {
	text := strings.TrimSpace(line)
	for _, choice := range screen.Choices {
		label := strings.TrimSpace(choice.Label)
		if label != "" && strings.Contains(text, label) {
			return true
		}
	}
	return false
}
