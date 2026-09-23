package ui

import (
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

func (m *Model) applies(ctx keymap.Context, action keymap.Action, row treeRow) bool {
	if ctx != keymap.ContextList {
		return true
	}
	if row.isArtifact() {
		return artifactRowActions[action]
	}
	hasRow := row.isGroup || row.sess.ID != ""
	sessions := m.legendRowSessions(row)
	anySession := func(match func(store.Session) bool) bool {
		for _, candidate := range sessions {
			if match(candidate) {
				return true
			}
		}
		return false
	}

	switch action {
	case keymap.Open, keymap.StepIn, keymap.StepOut:
		return hasRow
	case keymap.Attach:
		return row.sess.ID != ""
	case keymap.LastPane:
		return m.prevFocusID != ""
	case keymap.Rescind:
		return m.canRescindLatestSubmission()
	case keymap.ReorderUp:
		if !hasRow || row.isRoot() || m.listSortRefusal() != "" {
			return false
		}
		_, ok := m.visibleReorderTarget(row, -1)
		return ok
	case keymap.ReorderDown:
		if !hasRow || row.isRoot() || m.listSortRefusal() != "" {
			return false
		}
		_, ok := m.visibleReorderTarget(row, 1)
		return ok
	case keymap.Fork, keymap.Migrate, keymap.QuickInput, keymap.RenameSelf:
		return row.sess.ID != "" && !row.sess.Archived && !m.isShell(row.sess.Tool)
	case keymap.Revive:
		return anySession(func(candidate store.Session) bool {
			return !candidate.Archived && candidate.Status == status.Dead
		})
	case keymap.ReviveAll:
		for _, sess := range m.listedSessions() {
			if !sess.Archived && sess.Status == status.Dead {
				return true
			}
		}
		return false
	case keymap.SwitchAccount:
		if row.sess.ID == "" || row.sess.Archived || row.sess.TmuxPaneID != "" {
			return false
		}
		tool, ok := m.cfg.Tools[row.sess.Tool]
		return ok && tool.AccountEnv != ""
	case keymap.Restart:
		return row.sess.ID != "" && !row.sess.Archived && row.sess.Status != status.Dead
	case keymap.Archive:
		return anySession(func(candidate store.Session) bool { return !candidate.Archived })
	case keymap.ArchiveAll:
		for _, sess := range m.listedSessions() {
			if !sess.Archived {
				return true
			}
		}
		return false
	case keymap.Restore:
		return anySession(func(candidate store.Session) bool { return candidate.Archived })
	case keymap.Dismiss:
		return row.sess.ID != "" && !row.sess.Archived &&
			(m.isMuted(row.sess) || row.sess.Status == status.Finished ||
				(m.triage && m.triageWalkable(row.sess)))
	case keymap.Priority:
		return row.isGroup || (row.sess.ID != "" && !row.sess.Archived)
	case keymap.HandOver:
		return row.sess.ID != "" && m.triage && m.triageWalkable(row.sess)
	case keymap.Rename, keymap.Move:
		return hasRow && !m.showArchived
	case keymap.Editor:
		return hasRow
	case keymap.StatusFilter, keymap.Triage:
		return !m.showArchived
	case keymap.FoldAll:
		return m.allFoldsCollapsed() || len(m.collapsed) > 0 || len(m.rows) > 1
	case keymap.ArchivedView:
		return m.showArchived || len(m.archivedGroups) > 0 || len(m.archivedChildren) > 0
	}
	return true
}

func (m *Model) legendRowSessions(row treeRow) []store.Session {
	if row.isGroup {
		var sessions []store.Session
		for _, sess := range m.sessions {
			if inGroupSubtree(sess.Group, row.group) {
				sessions = append(sessions, sess)
			}
		}
		return sessions
	}
	if row.sess.ID != "" {
		return []store.Session{row.sess}
	}
	return nil
}

func (m *Model) currentLegendRow() treeRow {
	row, _ := m.cursorRow()
	return row
}
