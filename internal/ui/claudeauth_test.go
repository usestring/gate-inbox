package ui

import (
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/status"
)

func TestClaudeAuthenticationFailureIsActionableOnTheBoard(t *testing.T) {
	m := buildModel(t)
	m.poller.statusSources["claude"] = hooks.StatusSourceClaude
	id, _, _ := adoptedRow(t, m, "claude-auth", "")
	writeHookStatus(t, m, id, status.Idle+" SessionStart\n")
	t.Setenv(hooks.EnvStatusFile, m.hooks.StatusFile(id))
	dispatch := func(event, payload string) {
		t.Helper()
		m.hooks.DispatchGlobal(event, []byte(payload))
		m.applyCmd(t, m.refreshCmd())
	}
	check := func(wantStatus, wantLabel, hint string) {
		t.Helper()
		sess, ok := m.selected()
		if !ok || sess.Status != wantStatus || m.displayStatusLabel(sess) != wantLabel {
			t.Errorf("board = %q (%s), want %q (%s)", m.displayStatusLabel(sess), sess.Status, wantLabel, wantStatus)
		}
		preview := conversationInputText(m.contentLines(80, 18))
		if hint != "" && (!strings.Contains(preview, wantLabel) || !strings.Contains(preview, hint)) {
			t.Errorf("preview missing %q or %q:\n%s", wantLabel, hint, preview)
		}
		if hint != "run /login" && strings.Contains(preview, "/login") {
			t.Errorf("preview shows unexpected sign-in advice:\n%s", preview)
		}
		if hint == "" && strings.Contains(preview, "session's credentials") {
			t.Errorf("preview still shows credential advice:\n%s", preview)
		}
	}

	dispatch("UserPromptSubmit", `{}`)
	dispatch("StopFailure", `{"error":"authentication_failed","last_assistant_message":"Not logged in · Please run /login"}`)
	check(status.Errored, "sign-in required", "run /login")

	dispatch("UserPromptSubmit", `{}`)
	dispatch("StopFailure", `{"error":"authentication_failed","last_assistant_message":"API Error: Invalid API key"}`)
	check(status.Errored, "authentication failed", "session's credentials")

	dispatch("UserPromptSubmit", `{}`)
	dispatch("Stop", `{}`)
	check(status.Finished, "finished", "")
}
