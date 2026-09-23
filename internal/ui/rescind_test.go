package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

func TestRescindTargetsTheLatestSubmissionInsteadOfTheSelectedRow(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "submitted", t.TempDir(), "")
	createSession(t, m, "selected", t.TempDir(), "")
	m.selectSessionRow(t, "submitted")
	m.openQuickMode()
	m.quick.input.SetValue("start this")
	if _, _ = m.submitQuick(); m.latestSubmission.sessionID != m.sessionID(t, "submitted") {
		t.Fatalf("latest submission = %q want submitted", m.latestSubmission.sessionID)
	}
	m.quick.active = false
	m.selectSessionRow(t, "selected")

	footer := ansi.Strip(m.peekLegend(m.listBodyHeight()))
	if !strings.Contains(footer, m.tightCap(keymap.ContextList, keymap.Rescind)) ||
		!strings.Contains(footer, "rescind latest") {
		t.Fatalf("footer did not offer rescind: %q", footer)
	}

	tool := m.cfg.Tools["claude"]
	tool.InterruptKeys = []string{"r", "e", "s", "c", "i", "n", "d", "Enter"}
	m.cfg.Tools["claude"] = tool
	updated, _ := m.handleKey(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	m = updated.(*Model)
	if m.latestSubmission.sessionID != "" {
		t.Fatalf("successful rescind left target %q armed", m.latestSubmission.sessionID)
	}
	if !strings.Contains(m.errBar.text, "rescinded the latest submission to submitted") {
		t.Fatalf("rescind result = %q", m.errBar.text)
	}
	if !m.tmux.Exists(m.sessionID(t, "selected")) {
		t.Fatal("rescind interrupted the selected session instead of the submitted one")
	}
	waitForPaneText(t, m, m.sessionID(t, "submitted"), "rescind")
	selectedPane, err := m.tmux.CapturePane(m.sessionID(t, "selected"))
	if err != nil {
		t.Fatalf("capture selected pane: %v", err)
	}
	if strings.Contains(selectedPane, "rescind") {
		t.Fatal("configured interrupt keys reached the selected session")
	}
}

func TestFocusedAnswerRemainsRescindableAfterAutoProceed(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{
		"ask":  status.Waiting,
		"next": status.Waiting,
	})
	m.triage = true
	m.autoProceed = true
	m.rebuildRows()

	m.enterFocusOn(t, "ask")
	answeredID := focusedID(t, m)
	stageDialog(m, answeredID)
	updated, _ := m.handleFocusKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(*Model)
	if got := focusedName(t, m); got != "next" {
		t.Fatalf("after answering, focused %q want next", got)
	}
	if m.latestSubmission.sessionID != answeredID {
		t.Fatalf("rescind target = %q want answered session %q", m.latestSubmission.sessionID, answeredID)
	}
	if footer := ansi.Strip(m.viewFooter()); !strings.Contains(footer, "rescind latest") {
		t.Fatalf("focused footer did not offer rescind after auto-proceed: %q", footer)
	}
}

func TestRescindExpiresWhenTheTurnFinishesOrTheWindowCloses(t *testing.T) {
	statusAt := time.Now().Add(-time.Minute)
	sess := store.Session{ID: "answer", Status: status.Waiting, LastStatusAt: statusAt}
	m := &Model{sessions: []store.Session{sess}}
	m.noteSubmission(sess)
	if !m.canRescindLatestSubmission() {
		t.Fatal("fresh submission was not rescindable")
	}
	m.sessions[0].Status = status.Working
	m.sessions[0].LastStatusAt = time.Now()
	if !m.canRescindLatestSubmission() {
		t.Fatal("submission stopped being rescindable when its turn started")
	}
	m.sessions[0].Status = status.Finished
	m.sessions[0].LastStatusAt = time.Now().Add(time.Second)
	if m.canRescindLatestSubmission() {
		t.Fatal("finished turn remained rescindable")
	}

	m.sessions[0] = sess
	m.noteSubmission(sess)
	m.latestSubmission.sentAt = time.Now().Add(-submissionRescindWindow - time.Second)
	if m.canRescindLatestSubmission() {
		t.Fatal("expired submission remained rescindable")
	}
}

func TestSubmissionInterruptKeysDefaultToEscape(t *testing.T) {
	m := &Model{}
	if got := m.submissionInterruptKeys("custom"); len(got) != 1 || got[0] != "Escape" {
		t.Fatalf("default interrupt keys = %v want Escape", got)
	}
}
