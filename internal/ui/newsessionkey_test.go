package ui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func ctrlN() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl} }

func TestCtrlNOpensTheFormFromABoxOverTheList(t *testing.T) {
	for name, open := range map[string]func(*Model){
		"settings":      (*Model).openSettings,
		"help":          (*Model).openHelp,
		"quick actions": (*Model).openQuickActions,
	} {
		t.Run(name, func(t *testing.T) {
			m := buildModel(t)
			open(m)
			m.handleKey(ctrlN())
			if m.mode != modeForm {
				t.Fatalf("ctrl+n from %s left mode %v, want the new-session form (error %q)", name, m.mode, m.errBar.text)
			}
		})
	}
}

func TestCtrlNInAFocusedSessionOpensTheCLIBox(t *testing.T) {
	m := enterDrain(t, drainFleet(t))
	m.newSessionAgent = newSessionAgentAsk
	m.handleKey(ctrlN())
	if m.mode != modeAgentPick {
		t.Fatalf("ctrl+n in a focused session left mode %v, want the CLI box (error %q)", m.mode, m.errBar.text)
	}
}

func TestCtrlNIsBoundWhileTheKeyMapWaitsForAKey(t *testing.T) {
	m := buildModel(t)
	m.openHelp()
	m.help.capturing = true
	m.handleKey(ctrlN())
	if m.mode != modeHelp {
		t.Fatalf("ctrl+n during a rebind left mode %v, want the key map still waiting", m.mode)
	}
}

func TestQuickNewSessionMoreOptionsKeepsTheSelection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		key    tea.KeyPressMsg
		label  string
		pinned bool
	}{
		{name: "default", key: ctrlN(), label: "ctrl+n"},
		{name: "rebound with pinned group", key: tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl}, label: "ctrl+o", pinned: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := buildModel(t)
			dir := filepath.Join(t.TempDir(), "project")
			groupAt(t, m, "project", dir)
			groupAt(t, m, "other", filepath.Join(t.TempDir(), "other"))
			m.selectGroupRow(t, "project")
			if tc.pinned {
				writeKeys(t, m, "[list]\nnew_session_form = [\"ctrl+o\"]\n")
				if len(m.keyProblems) != 0 {
					t.Fatalf("rebinding more options: %v", m.keyProblems)
				}
			}
			pressKey(t, m, instantKey())
			typeInto(t, m, "ready-tool")
			if tc.pinned {
				m.agentPick.pinned, m.agentPick.group = true, "project"
				m.selectGroupRow(t, "other")
			}
			view := ansi.Strip(m.viewAgentPick())
			for _, hint := range []string{"↵ start", tc.label + " more options"} {
				if !strings.Contains(view, hint) {
					t.Fatalf("quick picker is missing %q:\n%s", hint, view)
				}
			}
			pressKey(t, m, tc.key)
			if m.mode != modeForm || m.selectedToolName() != "ready-tool" || m.selectedGroupPath() != "project" || m.form.dir.Value() != dir {
				t.Fatalf("more options: mode=%v tool=%q group=%q dir=%q", m.mode, m.selectedToolName(), m.selectedGroupPath(), m.form.dir.Value())
			}
			if len(m.sessionRows()) != 0 {
				t.Fatal("opening more options launched a session")
			}
			if !strings.Contains(ansi.Strip(m.viewForm()), "F2 worktrees") {
				t.Fatal("worktree picker hint is missing from the full form")
			}
		})
	}
}
