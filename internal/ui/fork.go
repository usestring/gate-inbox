// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/agentsession"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/opencode"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// forkOpencodeConversation copies a source conversation into a new one
// through opencode's own API and returns the copy's id. A variable so tests
// substitute a fixture instead of a live opencode.
var forkOpencodeConversation = opencode.Fork

type forkState struct {
	source store.Session
	name   textinput.Model
}

func (m *Model) openFork() {
	entry, ok := m.selectedRow()
	if !ok {
		return
	}
	if entry.isGroup {
		m.errBar.text = "select a session to fork"
		return
	}
	tool, ok := m.cfg.Tools[entry.sess.Tool]
	if !ok {
		m.errBar.text = fmt.Sprintf("tool %s is no longer configured", entry.sess.Tool)
		return
	}
	if err := validateForkSource(entry.sess.Tool, tool, entry.sess); err != nil {
		m.errBar.text = err.Error()
		return
	}
	name := textField("fork name", 60)
	name.SetValue(entry.sess.Name + "-fork")
	name.CursorEnd()
	name.Focus()
	m.fork = forkState{source: entry.sess, name: name}
	m.errBar.text = ""
	m.mode = modeFork
}

func (m *Model) handleForkKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeList
		m.errBar.text = ""
		return m, nil
	case "enter":
		return m.submitFork()
	}
	var cmd tea.Cmd
	m.fork.name, cmd = m.fork.name.Update(msg)
	return m, cmd
}

func (m *Model) submitFork() (tea.Model, tea.Cmd) {
	name := strings.ReplaceAll(strings.TrimSpace(m.fork.name.Value()), "/", "-")
	if name == "" {
		m.errBar.text = "name cannot be empty"
		return m, nil
	}
	source, err := m.store.Get(m.fork.source.ID)
	if err != nil {
		m.errBar.text = err.Error()
		return m, nil
	}
	tool, ok := m.cfg.Tools[source.Tool]
	if !ok {
		m.errBar.text = fmt.Sprintf("tool %s is no longer configured", source.Tool)
		return m, nil
	}
	if err := validateForkSource(source.Tool, tool, source); err != nil {
		m.errBar.text = err.Error()
		return m, nil
	}
	if !isDir(source.Cwd) {
		m.errBar.text = "working directory no longer exists: " + source.Cwd
		return m, nil
	}

	managerID := newID()
	account, err := accounts.Select(m.store, tool, source.Account, managerID)
	if err != nil {
		m.reportLaunchError(err)
		return m, nil
	}
	agentID := ""
	if strings.Contains(tool.ForkCommand, "{new_id}") {
		agentID = uuid.NewString()
	}
	sessionFile := ""
	if strings.Contains(tool.ForkCommand, "{session_file}") {
		sessionFile, err = forkSessionFile(tool.SessionStore, source.AgentSessionID)
		if err != nil {
			m.errBar.text = err.Error()
			return m, nil
		}
	}
	baseCommand := expandForkCommand(tool.ForkCommand, source.AgentSessionID, sessionFile, agentID, name)
	if tool.SessionStore == search.ToolOpenCode {
		// opencode's TUI has no fork flag, so the manager copies the
		// conversation through opencode's API and resumes the copy.
		// The copy exists before the pane does, so the fork launches on its
		// own conversation id from the start: no capture race, and a revive
		// resumes the copy rather than the directory's most recent session.
		copied, err := forkOpencodeConversation(source.Cwd, source.AgentSessionID)
		if err != nil {
			m.errBar.text = err.Error()
			return m, nil
		}
		agentID = copied
		baseCommand = strings.ReplaceAll(tool.ResumeByIDCommand, "{id}", tmux.ShellQuote(copied))
	}
	forked := store.Session{
		ID:             managerID,
		Name:           name,
		Tool:           source.Tool,
		Cwd:            source.Cwd,
		Group:          source.Group,
		Status:         status.Starting,
		AgentSessionID: agentID,
		Account:        account,
	}
	if err := m.launchNewSession(forked, tool, baseCommand); err != nil {
		m.reportLaunchError(err)
		return m, nil
	}
	// The resumed CLI may ask which of the source conversation to bring over
	// before it draws anything. Armed here so the poller answers it with the
	// whole conversation, which is the only answer a fork has any use for.
	m.poller.expectForkDialog(managerID, tool.ForkDialogOption, tool.ForkDialogKeys)
	// Forks start as starting, which attention excludes; clear so the row
	// the fork just created is on screen.
	m.statusFilter = statusFilterAll
	// Set before focusing, not instead of it: this is where a focus that
	// refuses -- a row filtered off the tree, a pane already gone -- leaves
	// the operator, and it must not be the form.
	m.mode = modeList
	m.errBar.text = ""
	return m.landInNewSession(managerID)
}

func validateForkSource(toolName string, tool config.Tool, source store.Session) error {
	// A shell has no fork_command either, but saying so names a config
	// field for a row that was never going to have a conversation.
	if tool.Shell {
		return fmt.Errorf("%s is a shell, not an agent - there is no conversation to fork", source.Name)
	}
	if tool.SessionStore == search.ToolOpenCode {
		if source.AgentSessionID == "" {
			return fmt.Errorf("%s has no captured conversation id", source.Name)
		}
		if !strings.Contains(tool.ResumeByIDCommand, "{id}") {
			return fmt.Errorf("tool %s resume_by_id_command must reference the conversation via {id}: opencode forks by copying the conversation and resuming the copy", toolName)
		}
		return nil
	}
	if tool.ForkCommand == "" {
		return fmt.Errorf("tool %s has no fork_command", toolName)
	}
	if !strings.Contains(tool.ForkCommand, "{id}") && !strings.Contains(tool.ForkCommand, "{session_file}") {
		return fmt.Errorf("tool %s fork_command must reference the source via {id} or {session_file}", toolName)
	}
	if source.AgentSessionID == "" {
		return fmt.Errorf("%s has no captured conversation id", source.Name)
	}
	return nil
}

// forkSessionFile locates a source conversation's file for {session_file}.
// A variable so tests stand in for a driver.
var forkSessionFile = agentsession.SessionFile

func expandForkCommand(template, sourceID, sessionFile, newID, name string) string {
	return strings.NewReplacer(
		"{id}", tmux.ShellQuote(sourceID),
		"{session_file}", tmux.ShellQuote(sessionFile),
		"{new_id}", tmux.ShellQuote(newID),
		"{name}", tmux.ShellQuote(name),
	).Replace(template)
}

func (m *Model) viewFork() string {
	body := "  source  " + valueStyle.Render(m.fork.source.Name) + "\n" +
		"  group   " + groupBadge(displayGroup(m.fork.source.Group)) + "\n" +
		formField("name", m.fork.name.View(), true)
	return m.card("↳ Fork Session", body, [][2]string{{"↵", "create"}, {"esc", "cancel"}})
}
