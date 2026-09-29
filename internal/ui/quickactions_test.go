package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/keymap"
)

func TestQuickActionsOpensOnColonAndCtrlP(t *testing.T) {
	for _, press := range []tea.KeyPressMsg{
		{Code: ':', Text: ":"},
		{Code: 'p', Mod: tea.ModCtrl},
	} {
		m := buildModel(t)
		pressKey(t, m, press)
		if m.mode != modeQuickActions {
			t.Fatalf("%q left the board in %v, want the quick actions palette", press.String(), m.mode)
		}
		pressKey(t, m, key("esc"))
		if m.mode != modeList {
			t.Fatalf("esc left the board in %v, want the list", m.mode)
		}
	}
}

func TestQuickActionsRunsTheActionTypedAndTeachesItsKey(t *testing.T) {
	m := buildModel(t)
	hidden := m.hideEmptyGroups
	m.openQuickActions()
	typeInto(t, m, "empty groups")
	if got := m.quickActionMatches(); len(got) == 0 || got[0].action != keymap.EmptyGroups {
		t.Fatalf("\"empty groups\" matched %+v first, want %s", got, keymap.EmptyGroups)
	}
	pressKey(t, m, key("enter"))
	if m.mode != modeList {
		t.Fatalf("running an action left the board in %v, want the list", m.mode)
	}
	if m.hideEmptyGroups == hidden {
		t.Fatal("the palette did not run empty_groups")
	}
	want := "next time press " + m.cap(keymap.ContextList, keymap.EmptyGroups) + " for empty_groups"
	if m.errBar.text != want || !m.errBar.worked() {
		t.Fatalf("status reads %q (worked %v), want %q", m.errBar.text, m.errBar.worked(), want)
	}
}

func TestQuickActionsRanksAnExactKeyFirst(t *testing.T) {
	m := buildModel(t)
	m.openQuickActions()
	typeInto(t, m, "n")
	got := m.quickActionMatches()
	if len(got) == 0 || got[0].action != keymap.NewSession {
		t.Fatalf("\"n\" matched %+v first, want %s", got, keymap.NewSession)
	}
}

func TestQuickActionsRanksTheActionsOwnNameAboveALabelMention(t *testing.T) {
	m := buildModel(t)
	m.openQuickActions()
	typeInto(t, m, "fold")
	got := m.quickActionMatches()
	if len(got) == 0 || got[0].action != keymap.FoldAll {
		t.Fatalf("\"fold\" matched %+v first, want %s", got, keymap.FoldAll)
	}
}

func TestQuickActionsLeadsWithWhatWasRunLast(t *testing.T) {
	m := buildModel(t)
	m.rememberQuickAction(keymap.Settings)
	m.rememberQuickAction(keymap.FoldAll)
	m.rememberQuickAction(keymap.Settings)
	m.openQuickActions()
	got := m.quickActionMatches()
	if len(got) < 2 || got[0].action != keymap.Settings || got[1].action != keymap.FoldAll {
		t.Fatalf("empty palette starts %+v, want settings then fold_all", got[:min(2, len(got))])
	}
}

func TestQuickActionsLeavesOutCursorSteps(t *testing.T) {
	m := buildModel(t)
	for _, entry := range m.quickActionEntries() {
		if quickActionsHidden[entry.action] {
			t.Errorf("palette offers %s", entry.action)
		}
	}
}

func TestQuickActionsCardShowsKeyLabelAndName(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 120, 40
	m.openQuickActions()
	typeInto(t, m, "archived view")
	view := ansi.Strip(m.viewQuickActions())
	for _, want := range []string{"Quick actions", m.cap(keymap.ContextList, keymap.ArchivedView), "archived view", "archived_view"} {
		if !strings.Contains(view, want) {
			t.Errorf("card is missing %q:\n%s", want, view)
		}
	}
}

func TestQuickActionsNamesTheKeyMapForAnUnboundAction(t *testing.T) {
	m := buildModel(t)
	next, problems := m.km().Rebind(keymap.ContextList, keymap.FoldAll, nil)
	if len(problems) > 0 {
		t.Fatalf("unbinding fold_all: %v", problems)
	}
	m.keys = next
	m.openQuickActions()
	typeInto(t, m, "fold all")
	pressKey(t, m, key("enter"))
	if !strings.Contains(m.errBar.text, "fold_all has no key") {
		t.Fatalf("status reads %q, want the unbound note", m.errBar.text)
	}
}
