package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

func TestFocusTypingVisibleEndToEnd(t *testing.T) {
	for _, tool := range []string{"claude", "codex", "opencode"} {
		for _, variant := range []string{"default", "legacy-conversation", "explicit-experiment"} {
			t.Run(tool+"/"+variant, func(t *testing.T) {
				m := buildModel(t)
				cfg, err := config.Default()
				if err != nil {
					t.Fatal(err)
				}
				m.cfg.Tools[tool] = cfg.Tools[tool]
				if variant == "legacy-conversation" {
					if err := m.store.SetSetting(focusViewSetting, focusViewConversation); err != nil {
						t.Fatal(err)
					}
				}
				m = New(m.cfg, m.store, m.tmux, liveEngine(t), m.hooks, "dev")
				m.width, m.height = 120, 40
				update := func(msg tea.Msg) {
					next, _ := m.Update(msg)
					m = next.(*Model)
				}
				if variant == "explicit-experiment" {
					update(key("s"))
					for range settingsFieldExperimental {
						update(key("down"))
					}
					update(key("enter"))
					// Up wraps past the JEV finish check to compressed focus.
					update(key("up"))
					update(key("up"))
					update(tea.KeyPressMsg{Code: tea.KeyRight})
					update(key("esc"))
					update(key("esc"))
				}
				dir := t.TempDir()
				submitted := filepath.Join(dir, "submitted")
				binary, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				command := "GATE_INBOX_COMPOSER_STANDIN=" + tool + " GATE_INBOX_SUBMITTED=" + shellQuote(submitted) + " " + shellQuote(binary) + " -test.run=^TestFocusComposerStandin$"
				sess := store.Session{ID: newID(), Name: "typing-e2e", Tool: tool, Cwd: dir,
					Status: status.Idle, CreatedAt: time.Now(), LastStatusAt: time.Now()}
				if err := m.tmux.Create(sess.ID, dir, command, nil, 100, 35); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { m.tmux.Kill(sess.ID) })
				if err := m.store.CreateSession(sess); err != nil {
					t.Fatal(err)
				}
				m.applyCmd(t, m.refreshCmd())
				m.selectSessionRow(t, sess.Name)
				update(key("enter"))
				if m.mode != modeFocus {
					t.Fatalf("Enter did not open focus: %v (%s)", m.mode, m.errBar.text)
				}
				m.applyConversation(conversationMsg{key: conversationKey(sess.ID, ""),
					messages: []search.Message{{Role: "assistant", Text: "Saved concise assistant reply."}}})
				capture := func() string {
					m.applyCmd(t, m.previewCmd(sess, m.previewGen, false))
					return m.frame()
				}
				wait := func(what string, check func(string) bool) string {
					t.Helper()
					deadline := time.Now().Add(5 * time.Second)
					for {
						frame := capture()
						if check(frame) {
							return frame
						}
						if time.Now().After(deadline) {
							t.Fatalf("%s missing from rendered focus; downstream pane:\n%s\nrendered frame:\n%s", what, ansi.Strip(m.preview), ansi.Strip(frame))
						}
						time.Sleep(10 * time.Millisecond)
					}
				}
				prefix := focusComposerPrefix(tool)
				before := wait("empty native composer", func(frame string) bool {
					return strings.Contains(ansi.Strip(m.preview), strings.TrimSpace(prefix))
				})
				plain := ansi.Strip(before)
				if variant == "explicit-experiment" {
					if !strings.Contains(plain, "Saved concise assistant reply.") || strings.Contains(plain, "RAW TOOL OUTPUT") {
						t.Fatalf("explicit experiment did not compress the transcript:\n%s", plain)
					}
				} else if !strings.Contains(plain, "RAW TOOL OUTPUT") || strings.Contains(plain, "Saved concise assistant reply.") {
					t.Fatalf("default/legacy preference hid the live terminal:\n%s", plain)
				}
				assertDraft := func(draft string) string {
					t.Helper()
					lit := wait("visible draft "+draft, func(frame string) bool {
						for _, row := range strings.Split(ansi.Strip(m.preview), "\n") {
							if strings.TrimSpace(row) == strings.TrimSpace(prefix+draft) {
								return regexp.MustCompile(regexp.QuoteMeta(strings.TrimSpace(row)) + ` *│`).MatchString(ansi.Strip(frame))
							}
						}
						return false
					})
					update(cursorBlinkMsg{gen: m.blinkGen})
					dark := m.frame()
					if ansi.Strip(lit) != ansi.Strip(dark) {
						t.Fatalf("blinking the caret changed the visible text:\nlit:\n%s\ndark:\n%s", ansi.Strip(lit), ansi.Strip(dark))
					}
					litRows, darkRows := strings.Split(lit, "\n"), strings.Split(dark, "\n")
					changed := 0
					for i, row := range litRows {
						if row == darkRows[i] {
							continue
						}
						changed++
						caretCell := regexp.MustCompile(regexp.QuoteMeta(draft) + `\x1b\[[0-9;]*m `)
						if !strings.Contains(ansi.Strip(row), draft) || caretCell.FindString(row) == "" || caretCell.FindString(row) == caretCell.FindString(darkRows[i]) {
							t.Fatalf("caret was not rendered immediately after the draft: %q", row)
						}
					}
					if changed != 1 {
						t.Fatalf("caret blink changed %d rendered rows, want exactly the draft row", changed)
					}
					update(cursorBlinkMsg{gen: m.blinkGen})
					return lit
				}
				for _, r := range "visible" {
					update(tea.KeyPressMsg{Code: r, Text: string(r)})
				}
				if typed := assertDraft("visible"); ansi.Strip(typed) == ansi.Strip(before) {
					t.Fatal("typing left the visible focus frame unchanged")
				}
				update(tea.KeyPressMsg{Code: tea.KeyBackspace})
				assertDraft("visibl")
				update(tea.PasteMsg{Content: "e draft"})
				assertDraft("visible draft")
				update(key("enter"))
				wait("submitted input and cleared composer", func(frame string) bool {
					got, err := os.ReadFile(submitted)
					return err == nil && string(got) == "visible draft" && !strings.Contains(ansi.Strip(frame), "visible draft")
				})
			})
		}
	}
}

func TestFocusComposerStandin(t *testing.T) {
	tool := os.Getenv("GATE_INBOX_COMPOSER_STANDIN")
	if tool == "" {
		return
	}
	cmd := exec.Command("stty", "raw", "-echo")
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	prefix := focusComposerPrefix(tool)
	draft := ""
	draw := func() {
		rows := []string{"Native " + tool + " local composer", "RAW TOOL OUTPUT: command details", "", "", "", prefix + draft}
		switch tool {
		case "claude":
			rows[4] = "────────────────────────────────────────"
			rows = append(rows, "────────────────────────────────────────", "  ? for shortcuts")
		case "codex":
			rows = append(rows, "", "  gpt-6 · 100% context left", "  ? for shortcuts")
		case "opencode":
			rows = append(rows, "  ┃ Build auto", "  ╹▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀", "  ctrl+p commands")
		}
		fmt.Print("\x1b[2J\x1b[H" + strings.Join(rows, "\r\n"))
		fmt.Printf("\x1b[6;%dH", ansi.StringWidth(prefix+draft)+1)
	}
	draw()
	var input [1]byte
	for {
		if _, err := os.Stdin.Read(input[:]); err != nil {
			return
		}
		switch input[0] {
		case '\r', '\n':
			if err := os.WriteFile(os.Getenv("GATE_INBOX_SUBMITTED"), []byte(draft), 0600); err != nil {
				t.Fatal(err)
			}
			draft = ""
		case 127, 8:
			if len(draft) > 0 {
				draft = draft[:len(draft)-1]
			}
		default:
			draft += string(input[0])
		}
		draw()
	}
}

func focusComposerPrefix(tool string) string {
	switch tool {
	case "claude":
		return "❯\u00a0"
	case "codex":
		return "› "
	default:
		return "  ┃ "
	}
}
