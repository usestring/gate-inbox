package ui

import (
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/store"
)

const triageContentSetting = "triage_content"

const (
	triageContentBoth     = "both"
	triageContentSessions = "sessions"
	triageContentReviews  = "reviews"
)

var triageContents = []string{triageContentBoth, triageContentSessions, triageContentReviews}

func normalizeTriageContent(content string) string {
	if slices.Contains(triageContents, content) {
		return content
	}
	return triageContentBoth
}

func storedTriageContent(st *store.Store) string {
	content, err := st.Setting(triageContentSetting)
	if err != nil {
		return triageContentBoth
	}
	return normalizeTriageContent(content)
}

func (m *Model) inTriageContent(sess store.Session) bool {
	content := normalizeTriageContent(m.triageContent)
	if !m.reviewQueue || content == triageContentBoth {
		return true
	}
	review := slices.ContainsFunc(m.reviews, func(r store.ReviewRequest) bool {
		return r.SessionID == sess.ID
	})
	return review == (content == triageContentReviews)
}

func (m *Model) cycleTriageContent() tea.Cmd {
	if !m.reviewQueue {
		m.errBar.text = "turn on the review queue in Settings → Experimental features to choose reviews"
		return nil
	}
	content := cycleMode(triageContents, normalizeTriageContent(m.triageContent), 1)
	if err := m.store.SetSetting(triageContentSetting, content); err != nil {
		m.errBar.text = "saving triage content: " + err.Error()
		return nil
	}
	cmd := m.applyTriageContent(content)
	m.reportDone("triage includes " + content)
	return cmd
}

func (m *Model) applyTriageContent(content string) tea.Cmd {
	previous := normalizeTriageContent(m.triageContent)
	m.triageContent = normalizeTriageContent(content)
	if !m.reviewQueue || previous == m.triageContent {
		return nil
	}
	m.triageStartupEnter = false
	var cmd tea.Cmd
	if m.mode == modeFocus {
		if sess, ok := m.selected(); ok && (!m.inTriageContent(sess) || m.triageContent == triageContentReviews) {
			cmd = m.leaveFocus()
		}
	}
	switch m.triageContent {
	case triageContentSessions:
		m.reviewsTab = false
	case triageContentReviews:
		m.reviewsTab = true
		m.triageResume = false
	}
	return cmd
}
