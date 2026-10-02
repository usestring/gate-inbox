package dialog

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/convo"
)

const OpencodeCustom = "Type your own answer"

type OpencodeAsk struct {
	Tabs      []string
	Field     int
	Completed int
	Total     int
	OnSubmit  bool
	Review    []ReviewAnswer
	Prompt    string
	Options   []OpencodeOption
	Multi     bool
	Typing    bool
	Legend    string
}

type OpencodeOption struct {
	Number      int
	Label       string
	Description string
	Checked     bool
	Picked      bool
}

func (a OpencodeAsk) Checked() []int {
	var out []int
	for _, option := range a.Options {
		if option.Checked {
			out = append(out, option.Number)
		}
	}
	return out
}

var (
	opencodeBar      = regexp.MustCompile(`^\s*\x{2503}(.*)$`)
	opencodeField    = regexp.MustCompile(`^Field (\d+) of (\d+)\s+\x{B7}\s+(\d+)/(\d+) completed$`)
	opencodeReview   = regexp.MustCompile(`^Review\s+\x{B7}\s+(\d+)/(\d+) completed$`)
	opencodeOption   = regexp.MustCompile(`^(\d+)\. (?:\[([ \x{2713}])\] )?(.*?)( \x{2713})?$`)
	opencodeTabSplit = regexp.MustCompile(`\s{2,}`)
)

func opencodeBlock(pane, title string) ([]string, bool) {
	var rows []string
	start := -1
	for _, line := range strings.Split(strings.ReplaceAll(ansi.Strip(pane), "\r", ""), "\n") {
		match := opencodeBar.FindStringSubmatch(line)
		if match == nil {
			if start >= 0 && strings.TrimSpace(line) != "" {
				break
			}
			continue
		}
		content := strings.TrimRight(match[1], "  ")
		content = strings.TrimPrefix(content, "  ")
		if strings.TrimSpace(content) == title {
			rows, start = nil, 0
			continue
		}
		if start >= 0 {
			rows = append(rows, content)
		}
	}
	return rows, start >= 0
}

func ParseOpencodeAsk(pane string) (OpencodeAsk, bool) {
	rows, ok := opencodeBlock(pane, "Questions")
	if !ok {
		return OpencodeAsk{}, false
	}
	for len(rows) > 0 && strings.TrimSpace(rows[len(rows)-1]) == "" {
		rows = rows[:len(rows)-1]
	}
	legendAt := len(rows)
	for i := len(rows) - 1; i >= 0 && i >= len(rows)-3; i-- {
		text := strings.TrimSpace(rows[i])
		if text == "" {
			break
		}
		legendAt = i
	}
	var ask OpencodeAsk
	ask.Legend = strings.Join(strings.Fields(strings.Join(rows[legendAt:], " ")), " ")
	lower := strings.ToLower(ask.Legend)
	if !strings.Contains(lower, "esc") || !(strings.Contains(lower, "dismiss") || strings.Contains(lower, "close")) {
		return OpencodeAsk{}, false
	}
	ask.Typing = strings.Contains(lower, "close")
	body := rows[:legendAt]
	i := 0
	for i < len(body) && strings.TrimSpace(body[i]) == "" {
		i++
	}
	if i < len(body) {
		head := strings.TrimSpace(body[i])
		switch {
		case opencodeField.MatchString(head):
			m := opencodeField.FindStringSubmatch(head)
			ask.Field, _ = strconv.Atoi(m[1])
			ask.Total, _ = strconv.Atoi(m[2])
			ask.Completed, _ = strconv.Atoi(m[3])
			i++
		case opencodeReview.MatchString(head):
			m := opencodeReview.FindStringSubmatch(head)
			ask.Completed, _ = strconv.Atoi(m[1])
			ask.Total, _ = strconv.Atoi(m[2])
			ask.OnSubmit = true
			i++
		case strings.HasSuffix(head, "Submit") && !opencodeOption.MatchString(head):
			tabs := opencodeTabSplit.Split(head, -1)
			ask.Tabs = tabs[:len(tabs)-1]
			ask.Total = len(ask.Tabs)
			i++
		}
	}
	var prompt []string
	for ; i < len(body); i++ {
		text := strings.TrimSpace(body[i])
		if opencodeOption.MatchString(text) && !strings.HasPrefix(body[i], " ") {
			break
		}
		if text != "" {
			prompt = append(prompt, text)
		}
	}
	if i == len(body) {
		if len(ask.Tabs) > 0 || ask.OnSubmit {
			ask.OnSubmit = true
			ask.Review = opencodeReviewRows(prompt, ask.Tabs)
			return ask, true
		}
		return OpencodeAsk{}, false
	}
	ask.Prompt = strings.Join(prompt, " ")
	for ; i < len(body); i++ {
		line := body[i]
		text := strings.TrimSpace(line)
		if text == "" {
			continue
		}
		if m := opencodeOption.FindStringSubmatch(text); m != nil && !strings.HasPrefix(line, " ") {
			n, _ := strconv.Atoi(m[1])
			if n != len(ask.Options)+1 {
				continue
			}
			option := OpencodeOption{Number: n, Label: m[3], Picked: m[4] != ""}
			if m[2] != "" {
				ask.Multi = true
				option.Checked = m[2] == "✓"
			}
			ask.Options = append(ask.Options, option)
			continue
		}
		if len(ask.Options) > 0 {
			last := &ask.Options[len(ask.Options)-1]
			last.Description = strings.TrimSpace(last.Description + " " + text)
		}
	}
	if len(ask.Options) < 2 {
		return OpencodeAsk{}, false
	}
	return ask, true
}

func opencodeReviewRows(rows, tabs []string) []ReviewAnswer {
	var out []ReviewAnswer
	for _, row := range rows {
		head, answer, found := strings.Cut(row, ": ")
		starts := found && (len(tabs) == 0 || containsFold(tabs, head))
		if starts || len(out) == 0 {
			out = append(out, ReviewAnswer{Question: strings.TrimSpace(head), Answer: strings.TrimSpace(answer)})
			continue
		}
		out[len(out)-1].Answer = strings.TrimSpace(out[len(out)-1].Answer + " " + row)
	}
	return out
}

func containsFold(list []string, s string) bool {
	for _, item := range list {
		if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(s)) ||
			strings.HasSuffix(item, "…") && strings.HasPrefix(s, strings.TrimSuffix(item, "…")) {
			return true
		}
	}
	return false
}

const OpencodeNotAnswered = "(not answered)"

func (a OpencodeAsk) OnScreen(asked []convo.AskQuestion) int {
	if a.OnSubmit {
		return -1
	}
	if a.Field > 0 {
		return a.Field - 1
	}
	for i, q := range asked {
		if SameText(q.Question, a.Prompt) {
			return i
		}
	}
	if len(asked) <= 1 && len(a.Tabs) <= 1 {
		return 0
	}
	return -1
}

func opencodeQuestions(pane string, asked []convo.AskQuestion) (Reading, bool) {
	ask, ok := ParseOpencodeAsk(pane)
	if !ok {
		return Reading{}, false
	}
	count := max(len(asked), ask.Total, 1)
	var out []Question
	if len(asked) == count {
		out = fromAsked(asked)
	} else {
		for i := range count {
			q := Question{Index: i + 1}
			if i < len(ask.Tabs) {
				q.Header = ask.Tabs[i]
			}
			out = append(out, q)
		}
	}
	if ask.OnSubmit {
		for _, row := range ask.Review {
			for i := range out {
				if strings.EqualFold(out[i].Header, row.Question) && row.Answer != OpencodeNotAnswered {
					out[i].Answered, out[i].Answer = true, row.Answer
				}
			}
		}
		return Reading{Questions: out, OnSubmit: true}, true
	}
	on := ask.OnScreen(asked)
	if on < 0 || on >= len(out) {
		return Reading{Questions: out}, true
	}
	out[on].OnScreen = true
	if out[on].Question == "" {
		out[on].Question, out[on].MultiSelect = ask.Prompt, ask.Multi
		for _, option := range ask.Options {
			if option.Label == OpencodeCustom {
				continue
			}
			out[on].Options = append(out[on].Options, Option{Label: option.Label, Description: option.Description})
		}
	}
	var picked []string
	for _, option := range ask.Options {
		if option.Picked || option.Checked {
			picked = append(picked, option.Label)
		}
		if option.Checked {
			for n := range out[on].Options {
				if SameText(out[on].Options[n].Label, option.Label) {
					out[on].Options[n].Checked = true
				}
			}
		}
	}
	if len(picked) > 0 && !ask.Multi {
		out[on].Answered, out[on].Answer = true, strings.Join(picked, ", ")
	}
	return Reading{Questions: out}, true
}

func ReadOpencodeScreen(pane string) (Screen, bool) {
	rows, ok := opencodeBlock(pane, "△ Permission required")
	if !ok {
		return Screen{}, false
	}
	screen := Screen{Kind: ScreenPermission, Lines: []string{"△ Permission required"}}
	choicesAt := -1
	for i, row := range rows {
		text := strings.TrimSpace(row)
		if at := strings.Index(text, "Reject"); strings.Contains(text, "Allow once") && at >= 0 {
			choicesAt = i
			for _, label := range opencodeTabSplit.Split(strings.TrimSpace(text[:at+len("Reject")]), -1) {
				screen.Choices = append(screen.Choices, ScreenChoice{Label: label})
			}
			if rest := strings.TrimSpace(text[at+len("Reject"):]); rest != "" {
				screen.Legend = rest
			}
			break
		}
		if text == "" && (len(screen.Lines) == 0 || screen.Lines[len(screen.Lines)-1] == "") {
			continue
		}
		screen.Lines = append(screen.Lines, row)
	}
	if choicesAt < 0 {
		return Screen{}, false
	}
	for len(screen.Lines) > 0 && screen.Lines[len(screen.Lines)-1] == "" {
		screen.Lines = screen.Lines[:len(screen.Lines)-1]
	}
	screen.Legend = strings.Join(strings.Fields(screen.Legend+" "+strings.Join(rows[choicesAt+1:], " ")), " ")
	if cursor := opencodeSelectedChoice(pane, screen.Choices); cursor >= 0 {
		screen.Choices[cursor].Cursor = true
	}
	return screen, true
}

var sgrBackground = regexp.MustCompile(`\x1b\[[0-9;]*48;[0-9;]*m`)

func opencodeSelectedChoice(pane string, choices []ScreenChoice) int {
	if len(choices) < 2 || !strings.Contains(pane, "\x1b[") {
		return -1
	}
	for _, line := range strings.Split(pane, "\n") {
		if !strings.Contains(ansi.Strip(line), choices[0].Label) || !strings.Contains(ansi.Strip(line), choices[len(choices)-1].Label) {
			continue
		}
		backgrounds := make([]string, len(choices))
		for i, choice := range choices {
			at := strings.Index(line, choice.Label)
			if at < 0 {
				return -1
			}
			seen := sgrBackground.FindAllString(line[:at], -1)
			if len(seen) > 0 {
				backgrounds[i] = seen[len(seen)-1]
			}
		}
		counts := map[string]int{}
		for _, bg := range backgrounds {
			counts[bg]++
		}
		for i, bg := range backgrounds {
			if counts[bg] == 1 {
				return i
			}
		}
		return -1
	}
	return -1
}

func init() {
	RegisterTool("opencode", ToolReader{Questions: opencodeQuestions, Screen: ReadOpencodeScreen})
}
