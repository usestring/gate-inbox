package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/extension/textfmt"
)

// lastPromptChars caps the newest prompt shown under the live pane; a long
// paste would otherwise push the agent's own screen out of the panel.
const lastPromptChars = 500

// lastPromptMaxRows keeps the block to a strip even on a narrow panel.
const lastPromptMaxRows = 12

// unwrapPastedPrompt drops the pasted-content wrapper a pasted prompt keeps
// in the transcript, so the strip shows what was said rather than the tag.
func unwrapPastedPrompt(text string) string {
	t := strings.TrimSpace(text)
	if strings.HasPrefix(t, "<pasted_content") {
		if _, rest, ok := strings.Cut(t, ">"); ok {
			t = rest
		}
		if body, _, ok := strings.Cut(t, "</pasted_content"); ok {
			t = body
		}
	}
	return t
}

// truncatePrompt cuts a prompt to lastPromptChars runes, marking the cut.
func truncatePrompt(prompt string) string {
	runes := []rune(prompt)
	if len(runes) <= lastPromptChars {
		return prompt
	}
	return strings.TrimRightFunc(string(runes[:lastPromptChars]), func(r rune) bool { return r == ' ' }) + "…"
}

// lastPromptLines is the selected session's newest prompt, drawn under the
// live pane's input box so what was last asked stays in view. Nothing when
// the panel is too small to spare the rows or no prompt is on file.
func (m *Model) lastPromptLines(width, height int) []contentLine {
	sess, ok := m.selected()
	if !ok || m.conversation == nil || m.tokens.key != m.conversationIdentity(sess) {
		return nil
	}
	prompt := promptPlain(m.tokens.lastPrompt)
	room := width - 4
	if prompt == "" || room < 8 || height < 12 {
		return nil
	}
	wrapped := strings.Split(ansi.Wordwrap(truncatePrompt(prompt), room, ""), "\n")
	maxRows := min(lastPromptMaxRows, height/3)
	if len(wrapped) > maxRows {
		wrapped = wrapped[:maxRows]
		last := wrapped[maxRows-1]
		wrapped[maxRows-1] = textfmt.TruncateWidth(last, max(textfmt.Width(last)-1, 1), "…")
	}
	lines := []contentLine{{text: "  " + subtleStyle.Render("last prompt")}}
	for i, line := range wrapped {
		lead := "    "
		if i == 0 {
			lead = "  " + subtleStyle.Render("❯ ")
		}
		lines = append(lines, contentLine{text: lead + valueStyle.Render(line)})
	}
	return lines
}
