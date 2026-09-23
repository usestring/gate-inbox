package ui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/tracing"
)

func (m mode) String() string {
	switch m {
	case modeList:
		return "list"
	case modeForm:
		return "form"
	case modeConfirmDelete:
		return "confirm-delete"
	case modeHelp:
		return "help"
	case modeRename:
		return "rename"
	case modeFork:
		return "fork"
	case modeMove:
		return "move"
	case modeGroupForm:
		return "group-form"
	case modeSettings:
		return "settings"
	case modeLaunchHint:
		return "launch-hint"
	case modeFocus:
		return "focus"
	case modeNameSweep:
		return "name-sweep"
	case modeRestorePrompt:
		return "restore-prompt"
	case modeWelcome:
		return "welcome"
	case modeTmuxHint:
		return "tmux-hint"
	case modeAgentPick:
		return "agent-pick"
	}
	return fmt.Sprintf("mode(%d)", int(m))
}

// Update is the logging seam around the model's own update. What a key press
// dispatched to does not survive the call, and none of it may be read from a
// returned command: that is a data race, and a log line is not worth one. The
// snapshot is taken here instead, on the Bubble Tea event loop.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	before := m.mode
	key, isKey := msg.(tea.KeyPressMsg)
	logged := logging.Enabled(logging.LevelInfo)
	// Asked once, here: the clock has to be read before the handler runs,
	// and a board nobody is tracing should not read it at all.
	traced := tracing.Enabled()
	var started time.Time
	if traced {
		started = time.Now()
		// Armed for the length of the handler, so a blocking call inside it
		// knows to hand its span to the dispatch rather than emit one of its
		// own. traceDispatch disarms it.
		m.dispatching = true
	}
	var snap dispatchSnapshot
	if isKey && logged {
		snap = m.snapshotDispatch(key)
	}
	model, cmd := m.update(msg)
	if traced {
		m.traceDispatch(model, msg, key, isKey, started)
	}
	if !logged {
		return model, cmd
	}
	// The mode is read off what update returned rather than off m, so a
	// handler that ever answers with a different model is still reported as
	// the one that took effect.
	after := m.mode
	if updated, ok := model.(*Model); ok {
		after = updated.mode
	}
	if isKey {
		snap.log(after)
		return model, cmd
	}
	if after != before {
		logging.Info("mode changed", "from", before.String(), "to", after.String(), "reason", fmt.Sprintf("%T", msg))
	}
	return model, cmd
}

// dispatchSnapshot is what the handler is about to consume, read before it
// runs and therefore before it can change any of it.
type dispatchSnapshot struct {
	key       string
	mode      mode
	branch    string
	searching bool
	quick     bool
	triage    bool
	focusable bool
	cursor    int
	rows      int
	row       string
	session   string
	name      string
	tool      string
	status    string
	archived  bool
	search    string
}

func (m *Model) snapshotDispatch(key tea.KeyPressMsg) dispatchSnapshot {
	snap := dispatchSnapshot{
		key:       key.String(),
		mode:      m.mode,
		branch:    m.dispatchBranch(),
		searching: m.searching,
		quick:     m.quick.active,
		triage:    m.triage,
		focusable: m.enterFocuses(),
		cursor:    m.cursor,
		rows:      len(m.rows),
		row:       "none",
		search:    m.search,
	}
	entry, ok := m.cursorRow()
	if !ok {
		return snap
	}
	switch {
	case entry.isGroup:
		snap.row, snap.name = "group", entry.group
	case entry.isArtifact():
		snap.row, snap.name = "artifact", entry.art.label
	default:
		snap.row, snap.name = "session", entry.sess.Name
	}
	snap.session = entry.sess.ID
	snap.tool = entry.sess.Tool
	snap.status = entry.sess.Status
	snap.archived = entry.sess.Archived
	return snap
}

// dispatchBranch names the handler handleKey is about to pick, in the order
// it picks them, so a key that "did nothing" can be traced to the branch that
// swallowed it rather than guessed at.
func (m *Model) dispatchBranch() string {
	if m.split.resizeMode {
		return "resize"
	}
	if m.mode != modeList {
		return m.mode.String()
	}
	if m.searching {
		return "search"
	}
	if m.quick.active {
		return "quick"
	}
	if entry, ok := m.cursorRow(); ok && entry.isArtifact() {
		return "artifact"
	}
	return "list"
}

func (s dispatchSnapshot) log(after mode) {
	fields := []any{
		"key", s.key,
		"branch", s.branch,
		"mode", s.mode.String(),
		"row", s.row,
		"name", s.name,
		"cursor", fmt.Sprintf("%d/%d", s.cursor, s.rows),
		"searching", s.searching,
		"quick", s.quick,
		"triage", s.triage,
		"enterFocuses", s.focusable,
	}
	if s.session != "" {
		fields = append(fields, "session", s.session, "tool", s.tool, "status", s.status, "archived", s.archived)
	}
	if strings.TrimSpace(s.search) != "" {
		fields = append(fields, "query", s.search)
	}
	if after != s.mode {
		fields = append(fields, "became", after.String())
	}
	logging.Info("key", fields...)
}
