package ui

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

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

// migrateAuto heads the card's targets when the build has a tool chooser,
// and leaves the CLI to it the way the new-session box's auto does.
const migrateAuto = "auto"

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
	// skip and childrenSkip open on the source's settings, so a move left
	// alone keeps them. focus is the card row ↑↓ is on: the name, then
	// these two.
	skip         bool
	childrenSkip bool
	focus        int
	// generation tells an auto answer for this card from one for a card
	// since closed. autoPick and autoReason are the chooser's last answer,
	// for the CLIs as busy as autoActive counted them; autoSubmit is set
	// while enter waits on one.
	generation uint64
	autoPick   string
	autoReason string
	autoActive map[string]int
	autoSubmit bool
}

// migrateAutoMsg is the build's chooser's answer for the migrate card.
type migrateAutoMsg struct {
	generation uint64
	active     map[string]int
	name       string
	reason     string
	err        error
	submit     bool
}

const (
	migrateFocusName = iota
	migrateFocusSkip
	migrateFocusChildrenSkip
	migrateFocusCount
)

func (m *Model) openMigrate() tea.Cmd {
	entry, ok := m.selectedRow()
	if !ok {
		return nil
	}
	if entry.isGroup {
		m.errBar.text = "select a session to migrate"
		return nil
	}
	tool, ok := m.cfg.Tools[entry.sess.Tool]
	if !ok {
		m.errBar.text = fmt.Sprintf("tool %s is no longer configured", entry.sess.Tool)
		return nil
	}
	transcript, err := migrate.Locate(migrateRoots(), entry.sess.Tool, tool, entry.sess)
	if err != nil {
		m.errBar.text = err.Error()
		return nil
	}
	tools := m.enabledToolNames()
	if len(tools) == 0 {
		m.errBar.text = "no agent CLI is configured to migrate to"
		return nil
	}
	if _, ok := toolChooser(); ok && !slices.Contains(tools, migrateAuto) {
		tools = append([]string{migrateAuto}, tools...)
	}
	m.migrate = migrateState{generation: m.migrate.generation + 1, source: entry.sess, toolNames: tools, transcript: transcript,
		skip: entry.sess.SkipPermissions, childrenSkip: entry.sess.ChildrenSkipPermissions}
	name := textField("session name", 60)
	name.SetValue(m.migrateProposedName())
	name.CursorEnd()
	name.Focus()
	m.migrate.name = name
	m.errBar.text = ""
	m.mode = modeMigrate
	return m.migrateAutoCmd(false)
}

func (m *Model) migrateOnAuto() bool {
	return len(m.migrate.toolNames) > 0 && m.migrate.toolNames[m.migrate.toolIndex] == migrateAuto
}

// migrateProposedName is the name the card offers for the target, which
// reads "auto" until the chooser has answered.
func (m *Model) migrateProposedName() string {
	target := m.migrateTool()
	if target == "" {
		target = migrateAuto
	}
	return m.migrate.source.Name + "-" + target
}

// migrateAutoCmd asks the build's chooser, off the event loop, which CLI the
// card's auto entry moves to, as the new-session box's auto does. submit
// migrates once it answers. A card without auto asks nothing.
func (m *Model) migrateAutoCmd(submit bool) tea.Cmd {
	if !slices.Contains(m.migrate.toolNames, migrateAuto) {
		return nil
	}
	chooser, ok := toolChooser()
	if !ok {
		return nil
	}
	mode, err := accounts.Mode(m.store)
	if err != nil {
		return nil
	}
	req := m.toolRequest(mode)
	generation := m.migrate.generation
	m.migrate.autoSubmit = submit
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		msg := migrateAutoMsg{generation: generation, active: req.Active, submit: submit}
		msg.name, msg.reason, msg.err = chooseTool(ctx, chooser, req)
		return msg
	}
}

func (m *Model) finishMigrateAuto(msg migrateAutoMsg) (tea.Model, tea.Cmd) {
	if m.mode != modeMigrate || msg.generation != m.migrate.generation || msg.submit != m.migrate.autoSubmit {
		return m, nil
	}
	m.migrate.autoSubmit = false
	proposed := m.migrateProposedName()
	m.migrate.autoPick, m.migrate.autoReason, m.migrate.autoActive = "", msg.reason, msg.active
	if msg.err == nil && slices.Contains(m.enabledToolNames(), msg.name) {
		m.migrate.autoPick = msg.name
	}
	if m.migrate.name.Value() == proposed {
		m.migrate.name.SetValue(m.migrateProposedName())
		m.migrate.name.CursorEnd()
	}
	if !msg.submit || !m.migrateOnAuto() {
		return m, nil
	}
	if m.migrate.autoPick == "" {
		m.errBar.text = "no CLI chosen: tab to choose one"
		if msg.reason != "" {
			m.errBar.text = "no CLI chosen (" + msg.reason + "): tab to choose one"
		}
		return m, nil
	}
	return m.submitMigrate()
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
		// Auto moves to what the chooser picks for the CLIs as busy as they
		// are now, so a pick made against other counts is asked again.
		if m.migrateOnAuto() && (m.migrate.autoPick == "" || !maps.Equal(m.migrate.autoActive, m.activeByTool())) {
			if m.migrate.autoSubmit {
				return m, nil
			}
			cmd := m.migrateAutoCmd(true)
			if cmd == nil {
				m.errBar.text = "launch accounts unavailable: tab to choose a CLI"
			}
			return m, cmd
		}
		return m.submitMigrate()
	case "up":
		m.moveMigrateFocus(-1)
		return m, nil
	case "down":
		m.moveMigrateFocus(1)
		return m, nil
	case "space", "left", "right":
		switch m.migrate.focus {
		case migrateFocusSkip:
			m.migrate.skip = !m.migrate.skip
			return m, nil
		case migrateFocusChildrenSkip:
			m.migrate.childrenSkip = !m.migrate.childrenSkip
			return m, nil
		}
	}
	if m.migrate.focus != migrateFocusName {
		return m, nil
	}
	var cmd tea.Cmd
	m.migrate.name, cmd = m.migrate.name.Update(msg)
	return m, cmd
}

func (m *Model) moveMigrateFocus(delta int) {
	m.migrate.focus = (m.migrate.focus + delta + migrateFocusCount) % migrateFocusCount
	if m.migrate.focus == migrateFocusName {
		m.migrate.name.Focus()
	} else {
		m.migrate.name.Blur()
	}
}

// migratePermissions is what the card asks of migrate.SkipPermissions: a
// setting only where the operator changed it, so one left alone follows the
// source the way an agent's move does.
func (m *Model) migratePermissions() (self, children *bool) {
	if m.migrate.skip != m.migrate.source.SkipPermissions {
		self = &m.migrate.skip
	}
	if m.migrate.childrenSkip != m.migrate.source.ChildrenSkipPermissions {
		children = &m.migrate.childrenSkip
	}
	return self, children
}

// cycleMigrateTool steps the target and follows it in the name when the
// name is still the one the card proposed, so a picked tool reads back in
// the row without retyping.
func (m *Model) cycleMigrateTool(delta int) {
	n := len(m.migrate.toolNames)
	if n == 0 {
		return
	}
	proposed := m.migrateProposedName()
	m.migrate.toolIndex = (m.migrate.toolIndex + delta + n) % n
	m.migrate.autoSubmit = false
	if m.migrate.name.Value() == proposed {
		m.migrate.name.SetValue(m.migrateProposedName())
		m.migrate.name.CursorEnd()
	}
}

// migrateTool is the CLI the card moves to: on auto, the chooser's pick, or
// nothing until it has answered.
func (m *Model) migrateTool() string {
	if len(m.migrate.toolNames) == 0 {
		return ""
	}
	if m.migrateOnAuto() {
		return m.migrate.autoPick
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
		SourceRunning: (m.migrate.account == nil || source.TmuxPaneID != "") && m.tmux.Exists(source.ID) && !source.Archived,
		ReadAction:    words.Read,
		SendAction:    words.Send,
	})
	// The source's model comes across when the destination can take one.
	// The account is the build's extension's choice, never the source's:
	// see accounts.Route. Only the switch-account card, which has already
	// chosen, arrives with one.
	id := newID()
	// Before an account is chosen, so a refusal leaves nothing behind.
	shape, err := sessionhooks.Shape(migrate.NewSession(id, name, toolName, source, launch.Plan{Model: source.Model}), extension.LaunchMigrate, source.ID)
	if err != nil {
		m.reportLaunchError(err)
		return m, nil
	}
	prompt = shape.Shaped(prompt)
	var account string
	if m.migrate.account != nil {
		account = *m.migrate.account
	} else {
		account, err = accounts.Route(tool, accounts.Request{SessionID: id, ToolName: toolName, Reason: extension.LaunchMigrate, From: source.ID})
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
	self, children := m.migratePermissions()
	moved.SkipPermissions, moved.ChildrenSkipPermissions, err = migrate.SkipPermissions(source, toolName, tool, self, children)
	if err != nil {
		m.reportLaunchError(err)
		return m, nil
	}
	if err := m.launchNewSession(moved, tool, plan.Command); err != nil {
		m.reportLaunchError(err)
		return m, nil
	}
	var retireErr error
	if m.migrate.account != nil {
		retireErr = m.retireMigrated(moved)
	}
	m.sessions, err = m.poller.listSessions(true)
	if err != nil {
		m.errBar.text = err.Error()
		return m, nil
	}
	m.statusFilter = statusFilterAll
	m.mode = modeList
	m.errBar.text = ""
	if retireErr != nil {
		m.errBar.text = retireErr.Error()
	}
	return m.landInNewSession(moved.ID)
}

// retireMigrated hands the source's children and their queued reports to
// sess, the session an account switch just migrated to, and files the source
// away with its pane ended. A migration from the migrate card leaves the
// source for the operator to end. Only once sess's pane is up, so a launch that died
// leaves the source as it was; and never for an adopted source, whose pane
// belongs to whoever started it and which keeps its children. An error is only reported: the migration
// itself already happened.
func (m *Model) retireMigrated(sess store.Session) error {
	if !m.tmux.Exists(sess.ID) {
		return nil
	}
	source, err := m.store.Get(sess.MigrationID)
	if err != nil || source.TmuxPaneID != "" {
		return nil
	}
	if err := m.store.CompleteMigration(source.ID, sess.ID); err != nil {
		return fmt.Errorf("%s's children could not be handed to %s: %w", source.Name, sess.Name, err)
	}
	if err := m.killSession(source); err != nil {
		return fmt.Errorf("%s took over, but ending %s failed: %w", sess.Name, source.Name, err)
	}
	return nil
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
		"  to      " + m.viewMigrateTarget() + "  " + mutedStyle.Render("tab cycles") + "\n" +
		m.viewMigrateAutoRows() +
		formField("name", m.migrate.name.View(), m.migrate.focus == migrateFocusName) +
		formField("perms", m.viewMigrateSkipToggle(m.migrate.skip), m.migrate.focus == migrateFocusSkip) +
		formField("children", m.viewSkipToggle(m.migrate.childrenSkip, true), m.migrate.focus == migrateFocusChildrenSkip)
	return m.card("⇄ Migrate Session", body, [][2]string{{"↵", "create"}, {"tab", "tool"}, {"↑↓", "move"}, {"←→", "change"}, {"esc", "cancel"}})
}

func (m *Model) viewMigrateTarget() string {
	if !m.migrateOnAuto() {
		return valueStyle.Render(m.migrateTool())
	}
	// An answer always carries the counts it was asked against, so a card
	// without them is still waiting on the chooser.
	switch {
	case m.migrate.autoActive == nil:
		return valueStyle.Render(migrateAuto) + " " + mutedStyle.Render("choosing…")
	case m.migrate.autoPick == "":
		return valueStyle.Render(migrateAuto) + " " + mutedStyle.Render("no pick")
	}
	return valueStyle.Render(migrateAuto) + " " + mutedStyle.Render("→ "+m.migrate.autoPick)
}

// viewMigrateAutoRows says why auto picks what it picks, in the
// new-session box's words, under the target while auto is on it.
func (m *Model) viewMigrateAutoRows() string {
	if !m.migrateOnAuto() || m.migrate.autoReason == "" {
		return ""
	}
	var b strings.Builder
	for _, row := range agentPickQuotaRows(nil, nil, "", m.migrate.autoReason, cardInnerWidth(m.cardWidth())-formLabelColumn)[1:] {
		b.WriteString(spaces(formLabelColumn) + row + "\n")
	}
	return b.String()
}

// viewMigrateSkipToggle is the session's own toggle on the migrate card,
// which says when the destination CLI cannot honour it.
func (m *Model) viewMigrateSkipToggle(on bool) string {
	tool := m.migrateTool()
	// Auto has not picked yet, so there is no CLI to say it of.
	return m.viewSkipToggleFor(on, tool == "" || launch.SkipsPermissions(tool, m.cfg.Tools[tool]), tool)
}
