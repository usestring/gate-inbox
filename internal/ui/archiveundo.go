package ui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/store"
)

// Archiving is confirmed because it ends an agent, and it is confirmed every
// single time because nothing could take the answer back. Those are one
// argument, not two: a dialog is the price of an act with no undo. So the
// setting that turns the dialog off arrives with the undo that pays for it,
// and never on its own -- an operator who cannot see they mis-keyed is worse
// off than one who was asked.

const (
	archiveConfirmSetting = "archive_confirm"
	archiveConfirmAlways  = confirmAlways
	archiveConfirmNever   = confirmNever
)

// archiveConfirmModes is the setting's cycle order.
var archiveConfirmModes = confirmAskModes

func storedArchiveConfirm(st *store.Store) string {
	return storedConfirmAsk(st, archiveConfirmSetting)
}

func normalizeArchiveConfirm(chosen string) string {
	return normalizeConfirmAsk(chosen)
}

// archiveUndo is the set the last archive filed away, held so U can put it
// back. Whole rows rather than ids because restoring one revives it, and a
// revive reads the launch record off the row.
type archiveUndo struct {
	sessions []store.Session
	isGroup  bool
	path     string
	label    string
}

// skipsArchiveConfirm reports whether the dialog now built can be answered
// without being shown. It is the archive action's own reading of skipsConfirm,
// kept for callers that mean the archive specifically.
func (m *Model) skipsArchiveConfirm() bool {
	return m.confirm.action == actionArchive && m.skipsConfirm()
}

// answerConfirm says yes to the dialog just built, without drawing it. It
// uses the shared answer handler because the whole answer lives there --
// the snapshot pass, the batch kill, the
// focused pane's exit, the triage drain carrying on -- and a second path
// into an act that ends agents is a second path that can drift from it.
func (m *Model) answerConfirm() (tea.Model, tea.Cmd) {
	return m.handleConfirmAnswer(tea.KeyPressMsg{}, true)
}

// noteArchived records what an archive filed away. Called for a confirmed
// archive as well as a silent one: the dialog says what is about to happen,
// not that it was meant, and a mis-keyed y is as worth taking back as a
// mis-keyed x.
func (m *Model) noteArchived(target confirmTarget) {
	if len(target.sessions) == 0 {
		return
	}
	label := fmt.Sprintf("%d sessions", len(target.sessions))
	switch {
	case target.isGroup:
		label = "group " + displayGroup(target.path)
	case len(target.sessions) == 1:
		label = target.sessions[0].Name
	}
	m.undo = archiveUndo{
		sessions: target.sessions,
		isGroup:  target.isGroup,
		path:     target.path,
		label:    label,
	}
}

// undoArchive puts the last archive back: out of the archive, and running
// again, which is what an operator means by taking one back. It is the
// restore dialog's own answer, aimed at a remembered set instead of at the
// row under the cursor -- an archived row usually leaves the board, so u,
// which reads the cursor, has nothing to aim at right after the act this
// undoes.
func (m *Model) undoArchive() (tea.Model, tea.Cmd) {
	if len(m.undo.sessions) == 0 {
		m.errBar.text = "nothing archived to undo"
		return m, nil
	}
	undo := m.undo
	// Spent whether or not the restore lands. A failed one leaves its
	// reason on the bar, and a second U pressed on top of that would run
	// the same failing restore again rather than report anything new.
	m.undo = archiveUndo{}
	m.confirm = confirmTarget{
		action:   actionRestore,
		sessions: undo.sessions,
		isGroup:  undo.isGroup,
		path:     undo.path,
	}
	model, cmd := m.answerConfirm()
	if m.errBar.text == "" {
		m.reportDone("brought " + undo.label + " back")
	}
	return model, cmd
}

// archivedNotice is what a silent archive leaves on the bar. The dialog it
// replaces was where the act named itself and named its way back, so with
// the dialog gone the notice has to do both.
func (m *Model) archivedNotice(label string) {
	notice := "archived " + label + " · "
	if undo := m.hintKey(keymap.ContextList, keymap.UndoArchive); undo != "" {
		notice += undo + " undoes it, "
	}
	m.reportDone(notice + m.archiveFinder() + " finds it")
}
