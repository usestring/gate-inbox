package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/keymap"
)

// A drained triage queue used to take the whole screen with the new-session
// form: every field the form has, for an operator who mostly wants to either
// start the next thing or look at the board. The inbox-cleared pane keeps the
// list up and asks only that: one prompt, which CLI, which subscription
// (auto routing by default), launch or dismiss. Everything else -- the group
// and directory under the cursor, an agent-chosen name -- comes from the
// defaults the form would have opened on, because the form's state backs it.

// clearedArt is the congratulations drawn at the head of the pane. Plain
// ASCII, so it measures the same in every terminal font.
var clearedArt = []string{
	`.  *  .  +  .  *  .`,
	`\o/   \o/   \o/`,
	` |     |     | `,
	`/ \   / \   / \`,
}

const (
	// clearedColumn caps the width of the pane's picker, prompt and keys.
	clearedColumn        = 56
	clearedPromptMaxRows = 8
)

// clearedState is the pane's subscription picker: the accounts listed for
// tool, auto first, and which one is chosen. The list is read the first time
// the operator steps through it, since listing runs the tool's accounts
// command and most launches stay on auto.
type clearedState struct {
	tool     string
	accounts []string
	index    int
	// routed is whether auto hands the launch to the account chooser rather
	// than the CLI's own login, read once when the pane opens.
	routed bool
}

// openCleared raises the pane over a drained queue. The form is opened
// underneath it for its defaults and its prompt box; a form that cannot open
// (no CLI installed) leaves its error on the bar and the operator on the list.
func (m *Model) openCleared() {
	m.openForm()
	if m.mode != modeForm {
		return
	}
	m.mode = modeCleared
	m.formFocus(fieldPrompt - m.form.focus)
	m.form.prompt.input.Placeholder = "what's next?"
	m.form.prompt.input.DynamicHeight = true
	m.cleared = clearedState{routed: m.accountRouted()}
}

func (m *Model) accountRouted() bool {
	if m.store == nil {
		return false
	}
	mode, err := accounts.Mode(m.store)
	return err == nil && mode == accounts.Extension
}

// clearedYields reports whether a later triage pickup may take the operator
// away: always from the list, and from the pane only while nothing has been
// typed into it, so a half-written prompt is never thrown out under them.
func (m *Model) clearedYields() bool {
	switch m.mode {
	case modeList:
		return true
	case modeCleared:
		return strings.TrimSpace(m.form.prompt.input.Value()) == "" && len(m.form.prompt.attachments) == 0
	}
	return false
}

func (m *Model) dismissCleared() {
	m.form.prompt.release()
	m.mode = modeList
}

// clearedPassThrough is the list keys that still act with the pane up: the
// list is on screen beside it, so leaving triage or switching tabs should not
// first need a dismiss. The pane closes and the list handles the key.
var clearedPassThrough = []keymap.Action{keymap.Triage, keymap.ReviewsTab, keymap.TriageContent}

// Inbox Zero is a text field, so a printable Back binding must still type.
func clearedBackChord(key string) bool {
	chord := "+" + key
	return strings.Contains(chord, "+ctrl+") || strings.Contains(chord, "+alt+")
}

func (m *Model) clearedBackCap() string {
	for _, context := range [...]keymap.Context{keymap.ContextList, keymap.ContextFocus} {
		for _, key := range m.km().Keys(context, keymap.LastPane) {
			if clearedBackChord(key) {
				return keymap.Compact(key)
			}
		}
	}
	return ""
}

func (m *Model) handleClearedKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := keyName(msg)
	listAction, _ := m.km().Action(keymap.ContextList, key)
	focusAction, _ := m.km().Action(keymap.ContextFocus, key)
	if clearedBackChord(key) && (listAction == keymap.LastPane || focusAction == keymap.LastPane) {
		model, cmd := m.focusBack()
		if m.mode == modeFocus {
			m.form.prompt.release()
		}
		return model, cmd
	}
	// A board can have nothing local to launch or triage and still have
	// remote sessions to open. Leave an in-progress launch prompt alone.
	if len(m.cfg.Hosts) > 0 && m.clearedYields() &&
		(m.isAction(keymap.ContextList, keymap.QuickActions, msg) ||
			m.isAction(keymap.ContextList, keymap.RemoteSessions, msg)) {
		m.dismissCleared()
		return m.handleKey(msg)
	}
	for _, action := range clearedPassThrough {
		if m.isAction(keymap.ContextList, action, msg) {
			m.dismissCleared()
			return m.handleKey(msg)
		}
	}
	switch msg.String() {
	case "esc":
		m.dismissCleared()
		return m, nil
	case "enter":
		m.form.account = m.clearedAccount()
		return m.submitForm()
	case "tab":
		m.cycleClearedTool(1)
		return m, nil
	case "shift+tab":
		m.cycleClearedTool(-1)
		return m, nil
	case "up", "down":
		if cmd, stepped := m.form.prompt.stepRow(msg); stepped {
			return m, cmd
		}
		delta := 1
		if msg.String() == "up" {
			delta = -1
		}
		m.cycleClearedAccount(delta)
		return m, nil
	}
	if cmd, handled := m.composerKey(composerForm, msg); handled {
		return m, cmd
	}
	return m, m.form.prompt.typeKey(msg)
}

func (m *Model) cycleClearedTool(delta int) {
	m.cycleTool(delta)
	m.cleared = clearedState{routed: m.cleared.routed}
}

// clearedRoutable reports whether the selected CLI can be launched on a
// chosen subscription at all.
func (m *Model) clearedRoutable() bool {
	return m.cfg.Tools[m.selectedToolName()].AccountEnv != ""
}

// cycleClearedAccount steps the subscription picker, listing the CLI's
// accounts on first use. A failed listing stays on auto and says why.
func (m *Model) cycleClearedAccount(delta int) {
	if !m.clearedRoutable() {
		return
	}
	toolName := m.selectedToolName()
	if m.cleared.tool != toolName || len(m.cleared.accounts) == 0 {
		names := []string{accountAuto}
		listed, err := accounts.ListCached(m.cfg.Tools[toolName])
		if err != nil {
			m.errBar.text = "listing " + toolName + " subscriptions: " + err.Error()
		}
		m.cleared = clearedState{tool: toolName, accounts: append(names, listed...), routed: m.cleared.routed}
	}
	n := len(m.cleared.accounts)
	m.cleared.index = (m.cleared.index + delta + n) % n
}

// clearedAccount is the subscription to pin the launch to: empty for auto,
// which leaves it to the operator's account routing.
func (m *Model) clearedAccount() string {
	if !m.clearedRoutable() || m.cleared.tool != m.selectedToolName() || len(m.cleared.accounts) == 0 {
		return ""
	}
	if name := m.cleared.accounts[m.cleared.index]; name != accountAuto {
		return name
	}
	return ""
}

// clearedAutoLabel says where auto sends a launch, so the default reads as a
// choice rather than as a blank.
func (m *Model) clearedAutoLabel() string {
	if m.cleared.routed {
		return "auto · routed"
	}
	return "auto · " + ownLogin
}

// clearedLines draws the pane: the art and the line under it centered, then
// the picker row, the prompt and the keys.
func (m *Model) clearedLines(width, height int) []string {
	accent := lipgloss.NewStyle().Foreground(colorAccent)
	var lines []string
	for _, row := range clearedArt {
		lines = append(lines, centerLine(accent.Render(row), width))
	}
	lines = append(lines,
		"",
		centerLine(lipgloss.NewStyle().Bold(true).Foreground(colorBright).Render("inbox zero"), width),
		centerLine(mutedStyle.Render("nothing needs you. start something?"), width),
		"",
	)
	headerRows := len(lines)

	// The picker, prompt and keys share one column centered under the art,
	// so the pane reads as one card rather than a header over a form.
	column := min(width, clearedColumn)
	pad := spaces(max((width-column)/2, 0))
	arrows := func(value string) string {
		return subtleStyle.Render("◂ ") + valueStyle.Render(value) + subtleStyle.Render(" ▸")
	}
	picker := arrows(m.selectedToolName())
	if m.clearedRoutable() {
		sub := m.clearedAutoLabel()
		if name := m.clearedAccount(); name != "" {
			sub = name
		}
		picker += mutedStyle.Render("  on  ") + arrows(sub)
	}
	lines = append(lines, pad+textfmt.TruncateWidth(picker, column, "…"))

	prompt := &m.form.prompt
	// Reserve a row each for the picker and keys. Keep the spacer only if it fits.
	prompt.maxRows = min(clearedPromptMaxRows, height-2)
	prompt.input.SetWidth(max(column-2, 8))
	prompt.input.SetHeight(min(prompt.input.Height(), prompt.maxRows))
	for _, row := range splitLines(prompt.renderChips(prompt.input.View())) {
		lines = append(lines, pad+row)
	}
	if len(lines)-headerRows+1 < height {
		lines = append(lines, "")
	}

	keys := []string{keyCap("↵", "launch"), keyCapQuiet("tab", "cli")}
	if m.clearedRoutable() {
		keys = append(keys, keyCapQuiet("↑↓", "subscription"))
	}
	keys = append(keys, keyCapQuiet("esc", "dismiss"))
	lines = append(lines, pad+textfmt.TruncateWidth(strings.Join(keys, mutedStyle.Render(" · ")), column, "…"))

	// Short panes drop the header first so the prompt and controls stay visible.
	if len(lines) > height {
		lines = lines[len(lines)-height:]
	}
	top := max((height-len(lines))/3, 0)
	return append(make([]string, top), lines...)
}
