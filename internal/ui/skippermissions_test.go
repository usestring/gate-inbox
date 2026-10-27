package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/store"
)

// Both toggles start off, flip with space on their own rows, and say so on
// the card; the session's own toggle says when the CLI cannot honour it.
func TestFormSkipPermissionToggles(t *testing.T) {
	m := buildModel(t)
	m.openForm()
	if m.form.skip || m.form.childrenSkip {
		t.Fatal("skip permissions should start off")
	}
	focusFormField(t, m, fieldSkip, "fieldSkip")
	m.handleFormKey(tea.KeyPressMsg{Code: tea.KeySpace})
	if !m.form.skip || m.form.childrenSkip {
		t.Fatalf("space on skip perms flipped skip=%v children=%v", m.form.skip, m.form.childrenSkip)
	}
	focusFormField(t, m, fieldChildrenSkip, "fieldChildrenSkip")
	m.handleFormKey(tea.KeyPressMsg{Code: tea.KeyRight})
	if !m.form.childrenSkip {
		t.Fatal("right on children did not flip it")
	}
	view := ansi.Strip(m.viewForm())
	if strings.Count(view, "skip all prompts") != 2 {
		t.Fatalf("card should show both toggles on:\n%s", view)
	}
	if !m.formSkipSupported() && !strings.Contains(view, "no flag on") {
		t.Fatalf("card should say the selected CLI cannot skip permissions:\n%s", view)
	}
}

// The relaunch path carries the flag for a session that has it on, so a
// revive or restart of a codex or claude session keeps its prompts off.
func TestBuildLaunchCarriesTheSkipPermissionsFlag(t *testing.T) {
	m := buildModel(t)
	tool := m.cfg.Tools["claude-hooked"]
	tool.SkipPermissionsFlag = "--dangerously-bypass-approvals-and-sandbox"
	command, _, err := m.buildLaunch("claude", tool, tool.Command, "skip0001", "", "", true, nil)
	if err != nil {
		t.Fatalf("buildLaunch: %v", err)
	}
	if !strings.HasPrefix(command, tool.Command+" --dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("command = %q, want the flag on the agent's command line", command)
	}
	tool.SkipPermissionsFlag = ""
	if _, _, err := m.buildLaunch("claude", tool, tool.Command, "skip0002", "", "", true, nil); err == nil {
		t.Fatal("a CLI with no flag launched with skip permissions on")
	}
}

// The migrate card opens on the source's settings, flips them on their own
// rows, and asks for a change only where the operator made one.
func TestMigrateCardSkipPermissionToggles(t *testing.T) {
	m := buildModel(t)
	m.migrate = migrateState{
		source:       store.Session{SkipPermissions: true},
		name:         textField("session name", 60),
		toolNames:    []string{"claude"},
		skip:         true,
		childrenSkip: false,
	}
	if self, children := m.migratePermissions(); self != nil || children != nil {
		t.Fatal("an untouched card asked to change the source's settings")
	}
	m.handleMigrateKey(tea.KeyPressMsg{Code: tea.KeyDown})
	m.handleMigrateKey(tea.KeyPressMsg{Code: tea.KeySpace})
	m.handleMigrateKey(tea.KeyPressMsg{Code: tea.KeyDown})
	m.handleMigrateKey(tea.KeyPressMsg{Code: tea.KeyRight})
	self, children := m.migratePermissions()
	if self == nil || *self || children == nil || !*children {
		t.Fatalf("card asked self=%v children=%v, want self off and children on", self, children)
	}
	if m.migrate.name.Value() != "" {
		t.Fatalf("toggle keys typed into the name: %q", m.migrate.name.Value())
	}
}

// The fork card opens on the source's settings, flips them on their own rows
// without typing into the name, and says when the source's CLI has no flag.
func TestForkCardSkipPermissionToggles(t *testing.T) {
	m := buildModel(t)
	tool := m.cfg.Tools["claude"]
	tool.SkipPermissionsFlag = ""
	m.cfg.Tools["claude"] = tool
	m.fork = forkState{
		source:       store.Session{Tool: "claude", ChildrenSkipPermissions: true},
		name:         textField("fork name", 60),
		childrenSkip: true,
	}
	m.handleForkKey(tea.KeyPressMsg{Code: tea.KeyDown})
	m.handleForkKey(tea.KeyPressMsg{Code: tea.KeySpace})
	m.handleForkKey(tea.KeyPressMsg{Code: tea.KeyDown})
	m.handleForkKey(tea.KeyPressMsg{Code: tea.KeyRight})
	if !m.fork.skip || m.fork.childrenSkip {
		t.Fatalf("card holds skip=%v children=%v, want skip on and children off", m.fork.skip, m.fork.childrenSkip)
	}
	if m.fork.name.Value() != "" {
		t.Fatalf("toggle keys typed into the name: %q", m.fork.name.Value())
	}
	view := ansi.Strip(m.viewFork())
	if !strings.Contains(view, "skip all prompts") || !strings.Contains(view, "no flag on claude") {
		t.Fatalf("card should show perms on and that claude has no flag here:\n%s", view)
	}
}

// opencode's skip rides its generated config, so its card carries the
// toggle with no note that it cannot be honoured.
func TestForkCardOffersSkipPermissionsOnOpencode(t *testing.T) {
	m := buildModel(t)
	m.cfg.Tools["opencode"] = config.Tool{Command: "cat", MCP: "opencode"}
	m.fork = forkState{
		source: store.Session{Tool: "opencode", SkipPermissions: true},
		name:   textField("fork name", 60),
		skip:   true,
	}
	view := ansi.Strip(m.viewFork())
	if !strings.Contains(view, "skip all prompts") || strings.Contains(view, "no flag on") {
		t.Fatalf("card should show perms on with no refusal note for opencode:\n%s", view)
	}
}
