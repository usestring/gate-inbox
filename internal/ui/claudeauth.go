package ui

import (
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

func (m *Model) claudeAuthNotice(sess store.Session) (label, hint string) {
	if sess.Status != status.Errored || sess.Archived {
		return "", ""
	}
	switch m.authFailures[sess.ID] {
	case hooks.ClaudeSignInRequired:
		return "sign-in required", "Open the session, run /login, then retry your prompt."
	case hooks.ClaudeAuthenticationFailed:
		return "authentication failed", "Check this session's credentials, then retry your prompt."
	default:
		return "", ""
	}
}
