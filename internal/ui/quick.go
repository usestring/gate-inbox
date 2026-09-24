// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/store"
)

func (m *Model) openQuickMode() {
	names, index := m.defaultToolSelection()
	if len(names) == 0 {
		m.errBar.text = "no CLIs enabled: open settings (s), then CLIs, to turn some on"
		return
	}
	input := textarea.New()
	input.CharLimit = 2000
	input.Placeholder = "type and press enter"
	input.ShowLineNumbers = false
	input.SetPromptFunc(2, func(info textarea.PromptInfo) string {
		if info.LineNumber == 0 {
			return keyStyle.Render("❯ ")
		}
		return "  "
	})
	styles := input.Styles()
	styles.Focused.CursorLine = lipgloss.NewStyle()
	input.SetStyles(styles)
	input.SetHeight(1)
	input.Focus()
	m.errBar.text = ""
	if m.showsConversation() {
		m.conversation.compact, m.conversation.dirty, m.conversation.offset = true, true, 0
	}
	m.quick = quickState{
		active:         true,
		composer:       composer{input: input, maxRows: quickBarMaxRows, gen: m.nextComposerGen()},
		toolNames:      names,
		toolIndex:      index,
		closeAfterSend: m.quickCloseAfterSend(),
	}
}

// defaultToolSelection returns enabled tool names with the index of
// the configured default, ready to seed a tool picker.
func (m *Model) defaultToolSelection() ([]string, int) {
	names := m.enabledToolNames()
	current := m.defaultTool()
	index := 0
	for i, name := range names {
		if name == current {
			index = i
		}
	}
	return names, index
}

// handleQuickKey runs while the quick bar is docked in the sidebar: arrows
// keep moving the selection on the list; in the gate they edit the draft. Enter submits
// against whatever is selected, and every other key is typed text.
func (m *Model) handleQuickKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	context := keymap.ContextList
	if m.mode == modeFocus {
		context = keymap.ContextFocus
	}
	if action, bound := m.action(context, msg); bound && action == keymap.ToggleConversation {
		m.toggleConversation()
		return m, nil
	}
	if m.mode == modeFocus {
		switch msg.String() {
		case "alt+enter", "shift+enter":
			m.quick.input.InsertString("\n")
			return m, nil
		case "up", "down", "tab", "alt+m":
			return m, m.quick.typeKey(msg)
		case "pgup":
			return m, m.keyScrollFocus(focusScrollPageUp)
		case "pgdown":
			return m, m.keyScrollFocus(focusScrollPageDown)
		}
	}
	if m.quick.message() == "" && m.canRescindLatestSubmission() {
		if action, bound := m.action(context, msg); bound && action == keymap.Rescind {
			return m.rescindLatestSubmission()
		}
	}
	switch msg.String() {
	case "esc":
		m.quick.active = false
		// Reopening the bar starts a fresh prompt, so the images this one
		// was holding have nowhere left to be referenced from.
		m.quick.release()
		return m, nil
	case "up":
		return m, m.moveCursor(-1)
	case "down":
		return m, m.moveCursor(1)
	case "tab", "alt+m":
		if len(m.quick.toolNames) > 0 {
			m.quick.toolIndex = (m.quick.toolIndex + 1) % len(m.quick.toolNames)
		}
		return m, nil
	case "enter":
		return m.submitQuick()
	}
	// A snippet is the bar's own shortcut: the same message, without typing
	// it. Read before the composer so the chord is not swallowed as text --
	// it carries none, so it would type nothing and look like a dead key --
	// and it leaves whatever is half-written in the input alone, because
	// sending a snippet is not abandoning the prompt being composed. A bare
	// ± is the exception: it is a character, and in an input it types one.
	if snip, ok := m.snippetFor(msg.String()); ok && !snip.Bare() {
		return m.sendSnippetToSelected(snip)
	}
	if cmd, handled := m.composerKey(composerQuick, msg); handled {
		return m, cmd
	}
	return m, m.quick.typeKey(msg)
}

// submitQuick answers the selected session, or spawns a new session with
// the prompt embedded when a group is selected. The bar stays active by
// default so consecutive prompts flow without re-arming; the "after quick
// send" setting closes it instead.
func (m *Model) submitQuick() (tea.Model, tea.Cmd) {
	entry, ok := m.selectedRow()
	if !ok {
		m.errBar.text = "nothing selected"
		return m, nil
	}
	if m.quick.pasting() {
		m.errBar.text = "still reading the pasted image - try again in a moment"
		return m, nil
	}
	text := m.quick.message()
	if text == "" {
		m.errBar.text = "prompt cannot be empty"
		return m, nil
	}
	if entry.isGroup {
		return m.quickSpawn(entry.group, text)
	}
	if m.isShell(entry.sess.Tool) {
		m.errBar.text = shellPromptHint(entry.sess.Name)
		return m, nil
	}
	if !m.tmux.Exists(entry.sess.ID) {
		m.errBar.text = m.deadSessionHint()
		return m, nil
	}
	// The quick prompt pastes and presses Enter exactly as a snippet does, so a dialog
	// on the pane eats it the same way. See dialogHold.
	if hold := m.dialogHold(entry.sess); hold != "" {
		m.errBar.text = hold
		return m, nil
	}
	if err := m.tmux.SendText(entry.sess.ID, text); err != nil {
		m.errBar.text = err.Error()
		return m, nil
	}
	m.noteSubmission(entry.sess)
	m.noteOperator(entry.sess, extension.OperatorPrompt, text, false)
	// The prompt is delivered: clear the input before anything else can
	// fail, so a retry cannot send it twice.
	m.clearQuickAfterSend()
	m.errBar.text = ""
	// A queued answer means the user expects a fresh finished alert.
	if err := m.store.SetAcked(entry.sess.ID, false); err != nil {
		m.errBar.text = "prompt sent, but clearing the alert ack failed: " + err.Error()
	}
	m.requestRefresh()
	if m.autoProceeds() {
		return m, m.handOverFocused(entry.sess)
	}
	return m, nil
}

func (m *Model) quickSpawn(group, prompt string) (tea.Model, tea.Cmd) {
	if strings.HasPrefix(prompt, "-") {
		m.errBar.text = `prompt cannot start with "-": the tool would read it as a flag`
		return m, nil
	}
	toolName := m.quickTool()
	if toolName == "" {
		m.errBar.text = "no tools configured"
		return m, nil
	}
	dir, ok := resolveExistingDir(m.groupPaths[group], m.groupDefaultDir(group))
	if !ok {
		m.errBar.text = "group has no valid default path: " + dir
		return m, nil
	}
	name := toolName + "-" + newID()[:4]
	// The quick prompt has no model field: it is the one-key spawn, and the
	// card is where a session is configured.
	id, err := m.spawnSessionAs(toolName, "", name, dir, group, prompt, true, store.SourceUser)
	if err != nil {
		m.reportLaunchError(err)
		// A spawn the hint dialog refused leaves nothing to send, so the
		// bar closes instead of swallowing the list keys behind the dialog.
		if m.mode == modeLaunchHint {
			m.quick.active = false
			m.quick.release()
		}
		return m, nil
	}
	// Spawned sessions start outside the attention set; clear so the new row shows.
	m.statusFilter = statusFilterAll
	m.clearQuickAfterSend()
	// And then it goes whatever the "after quick send" setting says. That
	// setting governs answering an existing session, where staying open lets
	// consecutive prompts flow; a spawn hands the keyboard to the agent it
	// just made, and a bar left armed behind a focused pane would take the
	// first keystroke meant for that agent. release frees the images the
	// prompt was holding, which nothing can reference once the bar is gone.
	m.quick.active = false
	m.quick.release()
	m.errBar.text = ""
	return m.landInNewSession(id)
}

// clearQuickAfterSend empties the bar for the next prompt, and dismisses it
// entirely when the settings toggle asks for that.
func (m *Model) clearQuickAfterSend() {
	m.quick.input.SetValue("")
	m.quick.attachments = nil
	if m.quick.closeAfterSend {
		m.quick.active = false
	}
}

// quickTool is the spawn CLI for the current quick-mode run: the settings
// default until tab cycles it.
func (m *Model) quickTool() string {
	if len(m.quick.toolNames) == 0 {
		return ""
	}
	return m.quick.toolNames[m.quick.toolIndex]
}

// quickCloseAfterSend reports whether the quick bar should dismiss itself
// once a prompt is delivered. Staying open is the default; a stored "close"
// choice opts in. A store error is surfaced but still yields the default.
func (m *Model) quickCloseAfterSend() bool {
	chosen, err := m.store.Setting(quickCloseSetting)
	if err != nil {
		m.errBar.text = "reading quick prompt setting: " + err.Error()
		return false
	}
	return chosen == "close"
}
