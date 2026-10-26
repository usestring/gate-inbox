package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/store"
)

// The new-session card's fast toggle starts off and flips on its own row.
func TestFormFastModeToggle(t *testing.T) {
	m := buildModel(t)
	m.openForm()
	if m.form.fast {
		t.Fatal("fast mode should start off")
	}
	focusFormField(t, m, fieldFast, "fieldFast")
	m.handleFormKey(tea.KeyPressMsg{Code: tea.KeySpace})
	if !m.form.fast || m.form.skip || m.form.childrenSkip {
		t.Fatalf("space on fast flipped fast=%v skip=%v children=%v", m.form.fast, m.form.skip, m.form.childrenSkip)
	}
	if view := ansi.Strip(m.viewForm()); !strings.Contains(view, "fast mode") {
		t.Fatalf("card should show fast mode on:\n%s", view)
	}
}

// The fork and migrate cards open on the source's fast mode and turn it off
// on their last row without touching the permission rows.
func TestForkAndMigrateCardsCarryFastMode(t *testing.T) {
	m := buildModel(t)
	source := store.Session{Tool: "claude", FastMode: true}
	m.fork = forkState{source: source, name: textField("fork name", 60), fast: source.FastMode}
	m.handleForkKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if view := ansi.Strip(m.viewFork()); !strings.Contains(view, "fast mode") {
		t.Fatalf("fork card should open on fast mode:\n%s", view)
	}
	m.handleForkKey(tea.KeyPressMsg{Code: tea.KeySpace})
	if m.fork.fast || m.fork.skip || m.fork.childrenSkip || m.fork.name.Value() != "" {
		t.Fatalf("fork card = %+v, want only fast mode turned off", m.fork)
	}

	m.migrate = migrateState{source: source, name: textField("session name", 60), toolNames: []string{"claude"}, fast: source.FastMode}
	m.handleMigrateKey(tea.KeyPressMsg{Code: tea.KeyUp})
	m.handleMigrateKey(tea.KeyPressMsg{Code: tea.KeyRight})
	if m.migrate.fast || m.migrate.skip || m.migrate.childrenSkip {
		t.Fatalf("migrate card = %+v, want only fast mode turned off", m.migrate)
	}
}
