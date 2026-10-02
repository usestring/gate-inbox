package ui

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/uuid"
	"github.com/usestring/gate-inbox/internal/agentsession"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

func TestConversationFocusLiveHarnessSubmission(t *testing.T) {
	live := os.Getenv("GATE_INBOX_E2E_COMPRESSED_INPUT")
	if live == "" {
		t.Skip("GATE_INBOX_E2E_COMPRESSED_INPUT unset; uses installed CLIs and one model turn per harness")
	}
	for _, tool := range []string{"claude", "codex", "opencode"} {
		t.Run(tool, func(t *testing.T) {
			if _, err := exec.LookPath(tool); err != nil {
				t.Fatal(err)
			}
			m := buildModel(t)
			m.engine = liveEngine(t)
			defaults, err := config.Default()
			if err != nil {
				t.Fatal(err)
			}
			m.cfg.Tools[tool] = defaults.Tools[tool]
			m.focusView = focusViewConversation
			m.compressedFocus = true
			dir := t.TempDir()
			sess := store.Session{ID: newID(), Name: "compressed-input", Tool: tool, Cwd: dir,
				Status: status.Idle, CreatedAt: time.Now(), LastStatusAt: time.Now()}
			command := tool
			if tool == "codex" {
				command += " --no-daemon"
			}
			if tool == "claude" {
				sess.AgentSessionID = uuid.NewString()
				command += " --disable-slash-commands --session-id " + sess.AgentSessionID
			}
			if err := m.tmux.Create(sess.ID, dir, command, nil, 100, 35); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if t.Failed() {
					t.Logf("full startup pane:\n%s", ansi.Strip(m.preview))
				}
				m.tmux.Kill(sess.ID)
			})
			if err := m.store.CreateSession(sess); err != nil {
				t.Fatal(err)
			}
			m.sessions = append(m.sessions, sess)
			m.rebuildRows()
			m.selectSessionRow(t, sess.Name)
			m.mode = modeFocus
			capture := func() string {
				m.applyCmd(t, m.previewCmd(sess, m.previewGen, false))
				return m.preview
			}
			trusted := false
			e2eWaitFor(t, 90*time.Second, "live composer", func(pane string) bool {
				plain := ansi.Strip(pane)
				if !trusted && strings.Contains(plain, "trust") && (strings.Contains(plain, "Yes, continue") || strings.Contains(plain, "Yes, I trust") || strings.Contains(plain, "Trust and continue")) {
					trusted = true
					if tool == "claude" && strings.Contains(plain, "❯ No, exit") {
						if err := m.tmux.SendKeys(sess.ID, "Down"); err != nil {
							t.Fatal(err)
						}
					}
					if err := m.tmux.SendKeys(sess.ID, "Enter"); err != nil {
						t.Fatal(err)
					}
					return false
				}
				rows := strings.Split(strings.TrimSuffix(plain, "\n"), "\n")
				if !m.pane.cursor.ok || m.pane.cursor.y < 0 || m.pane.cursor.y >= len(rows) {
					return false
				}
				_, ok := m.engine.InputPrefix(tool, rows[m.pane.cursor.y])
				return ok
			}, capture)
			const prompt = "Reply exactly COMPRESSED-SUBMIT-OK. Do not use any tools."
			updated, _ := m.Update(tea.PasteMsg{Content: prompt})
			*m = *updated.(*Model)
			e2eWaitFor(t, 30*time.Second, "visible unsubmitted input", func(_ string) bool {
				if !strings.Contains(ansi.Strip(capture()), prompt) {
					return false
				}
				view := conversationInputText(m.previewLines(100, 30, ""))
				_, _, caret := m.cursorCell(m.pane.box.height)
				return strings.Contains(view, prompt) && caret
			}, func() string { return m.preview })
			t.Logf("%s draft and caret visible in compressed focus before submission", tool)
			if live == "composer" {
				return
			}
			updated, _ = m.handleFocusKey(tea.KeyPressMsg{Code: tea.KeyEnter})
			*m = *updated.(*Model)
			e2eWaitFor(t, 4*time.Minute, "submitted response", func(pane string) bool {
				plain := ansi.Strip(pane)
				return liveSubmissionResponse.MatchString(plain)
			}, capture)
			e2eWaitFor(t, time.Minute, "response in compressed conversation", func(_ string) bool {
				if sess.AgentSessionID == "" {
					id, found := agentsession.Capture(tool, dir, sess.CreatedAt, nil)
					if !found {
						return false
					}
					sess.AgentSessionID = id
					m.rows[m.cursor].sess.AgentSessionID = id
				}
				cmd := m.readConversation()
				if cmd == nil {
					return false
				}
				m.applyConversation(cmd().(conversationMsg))
				answered := false
				for _, message := range m.conversation.messages {
					if message.Role == "assistant" && strings.TrimSpace(message.Text) == "COMPRESSED-SUBMIT-OK" {
						answered = true
					}
				}
				rendered := conversationInputText(m.previewLines(100, 30, ""))
				return answered && strings.Contains(rendered, "COMPRESSED-SUBMIT-OK")
			}, capture)
			t.Logf("%s accepted Enter and displayed the assistant response in compressed focus", tool)
		})
	}
}

var liveSubmissionResponse = regexp.MustCompile(`(?m)^[ \t]*(?:[●•┃│][ \t]*)?COMPRESSED-SUBMIT-OK[ \t│]*$`)

func TestLiveHarnessSubmissionRequiresAssistantResponse(t *testing.T) {
	for _, pane := range []string{
		"❯ Reply exactly COMPRESSED-SUBMIT-OK.\n❯ Reply exactly COMPRESSED-SUBMIT-OK.\n",
		"› COMPRESSED-SUBMIT-OK\n",
		"Waiting for COMPRESSED-SUBMIT-OK\n",
	} {
		if liveSubmissionResponse.MatchString(pane) {
			t.Fatalf("draft/status accepted as a response: %q", pane)
		}
	}
	for _, pane := range []string{"● COMPRESSED-SUBMIT-OK\n", "• COMPRESSED-SUBMIT-OK\n", "     COMPRESSED-SUBMIT-OK\n"} {
		if !liveSubmissionResponse.MatchString(pane) {
			t.Fatalf("assistant response missed: %q", pane)
		}
	}
}
