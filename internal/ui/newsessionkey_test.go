package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
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
