package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/migrate"
	"github.com/usestring/gate-inbox/internal/sessioncmd"
	"github.com/usestring/gate-inbox/internal/sessionhooks"
	"github.com/usestring/gate-inbox/internal/store"
)

const migrateKey = "M"

// migrateRoots resolves where the agent CLIs keep their transcripts. A
// variable so tests point it at fixtures without touching the real state.
var migrateRoots = migrate.DefaultRoots

type migrateState struct {
	source    store.Session
	name      textinput.Model
	toolNames []string
	toolIndex int
	account   *string
	// transcript is located when the card opens, so a session with nothing
	// to move is refused before a name is typed.
	transcript migrate.Transcript
}

func (m *Model) openMigrate() {
	entry, ok := m.selectedRow()
	if !ok {
		return
	}
	if entry.isGroup {
		m.errBar.text = "select a session to migrate"
		return
	}
	tool, ok := m.cfg.Tools[entry.sess.Tool]
	if !ok {
		m.errBar.text = fmt.Sprintf("tool %s is no longer configured", entry.sess.Tool)
		return
	}
	transcript, err := migrate.Locate(migrateRoots(), entry.sess.Tool, tool, entry.sess)
	if err != nil {
		m.errBar.text = err.Error()
		return
	}
	tools := m.enabledToolNames()
	if len(tools) == 0 {
		m.errBar.text = "no agent CLI is configured to migrate to"
		return
	}
	name := textField("session name", 60)
	name.SetValue(entry.sess.Name + "-" + tools[0])
	name.CursorEnd()
	name.Focus()
	m.migrate = migrateState{source: entry.sess, name: name, toolNames: tools, transcript: transcript}
	m.errBar.text = ""
	m.mode = modeMigrate
}

func (m *Model) handleMigrateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeList
		m.errBar.text = ""
		return m, nil
	case "tab":
		m.cycleMigrateTool(1)
		return m, nil
	case "shift+tab":
		m.cycleMigrateTool(-1)
		return m, nil
	case "enter":
		return m.submitMigrate()
	}
	var cmd tea.Cmd
	m.migrate.name, cmd = m.migrate.name.Update(msg)
	return m, cmd
}

// cycleMigrateTool steps the target and follows it in the name when the
// name is still the one the card proposed, so a picked tool reads back in
// the row without retyping.
func (m *Model) cycleMigrateTool(delta int) {
	n := len(m.migrate.toolNames)
	if n == 0 {
		return
	}
	proposed := m.migrate.source.Name + "-" + m.migrateTool()
	m.migrate.toolIndex = (m.migrate.toolIndex + delta + n) % n
	if m.migrate.name.Value() == proposed {
		m.migrate.name.SetValue(m.migrate.source.Name + "-" + m.migrateTool())
		m.migrate.name.CursorEnd()
	}
}

func (m *Model) migrateTool() string {
	if len(m.migrate.toolNames) == 0 {
		return ""
	}
	return m.migrate.toolNames[m.migrate.toolIndex]
}

func (m *Model) submitMigrate() (tea.Model, tea.Cmd) {
	name := strings.ReplaceAll(strings.TrimSpace(m.migrate.name.Value()), "/", "-")
	if name == "" {
		m.errBar.text = "name cannot be empty"
		return m, nil
	}
	source, err := m.store.Get(m.migrate.source.ID)
	if err != nil {
		m.errBar.text = err.Error()
		return m, nil
	}
	toolName := m.migrateTool()
	tool, ok := m.cfg.Tools[toolName]
	if !ok {
		m.errBar.text = fmt.Sprintf("tool %s is no longer configured", toolName)
		return m, nil
	}
	if !isDir(source.Cwd) {
		m.errBar.text = "working directory no longer exists: " + source.Cwd
		return m, nil
	}
	words := sessioncmd.VocabularyFor(toolName, tool)
	prompt := migrate.Prompt(migrate.Brief{
		Source:        source,
		SourceTool:    source.Tool,
		Transcript:    m.migrate.transcript,
		SourceRunning: m.tmux.Exists(source.ID) && !source.Archived,
		ReadAction:    words.Read,
		SendAction:    words.Send,
	})
	// The source's model comes across when the destination can take one,
	// and so does its account, else the board's default.
	named := ""
	if source.Account != "" && tool.AccountEnv != "" {
		named = source.Account
	}
	id := newID()
	// Before an account is borrowed, so a refusal leaves nothing behind.
	shape, err := sessionhooks.Shape(migrate.NewSession(id, name, toolName, source, launch.Plan{Model: source.Model}), extension.LaunchMigrate, source.ID)
	if err != nil {
		m.reportLaunchError(err)
		return m, nil
	}
	prompt = shape.Prefixed(prompt)
	var account string
	if m.migrate.account != nil {
		account = *m.migrate.account
		err = accounts.CarryBorrower(m.store, source.ID, id)
	} else {
		account, err = accounts.Select(m.store, tool, named, id)
	}
	if err != nil {
		m.reportLaunchError(err)
		return m, nil
	}
	plan, err := launch.Assemble(toolName, tool, prompt, "", false, source.Model, account)
	if err != nil {
		m.reportLaunchError(err)
		return m, nil
	}
	moved := migrate.NewSession(id, name, toolName, source, plan)
	moved.MigrationOpening = m.openingPrompts(source)
	if err := m.launchNewSession(moved, tool, plan.Command); err != nil {
		m.reportLaunchError(err)
		return m, nil
	}
	m.sessions, err = m.poller.listSessions(true)
	if err != nil {
		m.errBar.text = err.Error()
		return m, nil
	}
	m.statusFilter = statusFilterAll
	m.mode = modeList
	m.errBar.text = ""
	return m.landInNewSession(moved.ID)
}

func (m *Model) viewMigrate() string {
	// The card is narrower than a transcript path, so the file is named by
	// its base name -- the conversation id -- and the whole path goes to the
	// new session's prompt, where it is read rather than glanced at.
	from := m.migrate.transcript.Command
	if m.migrate.transcript.Path != "" {
		from = filepath.Base(m.migrate.transcript.Path)
	}
	body := "  source  " + valueStyle.Render(m.migrate.source.Name) + "  " + mutedStyle.Render("on "+m.migrate.source.Tool) + "\n" +
		"  reads   " + mutedStyle.Render(from) + "\n" +
		"  to      " + valueStyle.Render(m.migrateTool()) + "  " + mutedStyle.Render("tab cycles") + "\n" +
		formField("name", m.migrate.name.View(), true)
	return m.card("⇄ Migrate Session", body, [][2]string{{"↵", "create"}, {"tab", "tool"}, {"esc", "cancel"}})
}
