package ui

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/search"
)

// tokenUsageRead is the selected session's context reading while its
// conversation is off screen. The conversation read carries the reading
// whenever the conversation is drawn; focused on the agent's own pane it is
// not drawn, so this keeps the dock's context line current without
// reading the whole transcript every tick.
type tokenUsageRead struct {
	key, stamp string
	usage      search.TokenUsage
	busy       bool
}

type tokenUsageMsg struct {
	key, stamp string
	usage      search.TokenUsage
	unchanged  bool
}

// readTokenUsage reads the selected session's context use from its
// transcript tail, skipping the read when the file has not changed.
func (m *Model) readTokenUsage() tea.Cmd {
	sess, ok := m.selected()
	if m.conversation == nil || !ok || m.isShell(sess.Tool) || m.tokens.busy {
		return nil
	}
	key := m.conversationIdentity(sess)
	m.tokens.busy = true
	previousKey, previousStamp := m.tokens.key, m.tokens.stamp
	locate := m.conversationTarget(sess)
	return func() tea.Msg {
		msg := tokenUsageMsg{key: key}
		target, ok := locate()
		if !ok || target.Tool == search.ToolOpenCode {
			return msg
		}
		info, err := os.Stat(target.Path)
		if err != nil {
			return msg
		}
		msg.stamp = fmt.Sprintf("%s:%d:%d", target.Path, info.Size(), info.ModTime().UnixNano())
		if key == previousKey && msg.stamp == previousStamp {
			msg.unchanged = true
			return msg
		}
		msg.usage = search.ReadTokenUsage(target)
		return msg
	}
}

func (m *Model) applyTokenUsage(msg tokenUsageMsg) {
	m.tokens.busy = false
	if msg.unchanged {
		return
	}
	m.tokens.key, m.tokens.stamp, m.tokens.usage = msg.key, msg.stamp, msg.usage
}
