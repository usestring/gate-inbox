package ui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

var errNoChoice = errors.New("nothing to choose")

// toolChooserFake answers ChooseTool with choose, and ChooseAccount with
// the CLI's own login.
type toolChooserFake struct {
	choose func(extension.ToolRequest) (string, error)
}

func (f toolChooserFake) ChooseAccount(context.Context, extension.AccountRequest) (string, error) {
	return "", nil
}

func (f toolChooserFake) ChooseTool(_ context.Context, req extension.ToolRequest) (string, error) {
	return f.choose(req)
}

func useToolChooser(t *testing.T, choose func(extension.ToolRequest) (string, error)) {
	t.Helper()
	fake := toolChooserFake{choose: choose}
	t.Cleanup(accounts.UseChooser(func() (extension.AccountChooser, error) { return fake, nil }))
}

func TestAutoRouteStartsAnEnabledCLI(t *testing.T) {
	m := buildModel(t)
	groupAt(t, m, "proj", filepath.Join(t.TempDir(), "sample-repo"))
	m.newSessionAgent = newSessionAgentAuto
	useToolChooser(t, func(extension.ToolRequest) (string, error) { return "ready-tool", nil })
	_, cmd := m.startNewSession()
	if cmd == nil {
		t.Fatal("auto route did not ask the chooser")
	}
	msg := cmd()
	m.update(msg)
	m.leaveFocusForFixture(t)
	rows := m.sessionRows()
	if len(rows) != 1 || rows[0].Tool != "ready-tool" {
		t.Fatalf("auto route launched %+v, want ready-tool (msg %+v, mode %v, error %q)", rows, msg, m.mode, m.errBar.text)
	}
}

func TestAutoRouteLaunchesInTheGroupSelectedWhenNWasPressed(t *testing.T) {
	m := buildModel(t)
	origin := filepath.Join(t.TempDir(), "origin-repo")
	groupAt(t, m, "origin", origin)
	groupAt(t, m, "elsewhere", filepath.Join(t.TempDir(), "elsewhere-repo"))
	m.selectGroupRow(t, "origin")
	m.newSessionAgent = newSessionAgentAuto
	useToolChooser(t, func(extension.ToolRequest) (string, error) { return "ready-tool", nil })
	_, cmd := m.startNewSession()
	if cmd == nil {
		t.Fatal("auto route did not ask the chooser")
	}
	m.selectGroupRow(t, "elsewhere")
	m.update(cmd())
	m.leaveFocusForFixture(t)
	rows := m.sessionRows()
	if len(rows) != 1 || rows[0].Group != "origin" || rows[0].Cwd != origin {
		t.Fatalf("auto route launched %+v, want it in the origin group (error %q)", rows, m.errBar.text)
	}
}

func TestAutoRouteLaunchesFromAFocusedSession(t *testing.T) {
	m := buildModel(t)
	groupAt(t, m, "proj", filepath.Join(t.TempDir(), "sample-repo"))
	m.newSessionAgent = newSessionAgentAuto
	useToolChooser(t, func(extension.ToolRequest) (string, error) { return "ready-tool", nil })
	m.mode = modeFocus
	_, cmd := m.startNewSession()
	if cmd == nil {
		t.Fatal("auto route did not ask the chooser from focus")
	}
	m.update(cmd())
	m.leaveFocusForFixture(t)
	rows := m.sessionRows()
	if len(rows) != 1 || rows[0].Tool != "ready-tool" {
		t.Fatalf("auto route from focus launched %+v, want ready-tool (mode %v, error %q)", rows, m.mode, m.errBar.text)
	}
}

func TestAutoRouteFallbackPickerKeepsTheGroupSelectedWhenNWasPressed(t *testing.T) {
	m := buildModel(t)
	origin := filepath.Join(t.TempDir(), "origin-repo")
	groupAt(t, m, "origin", origin)
	groupAt(t, m, "elsewhere", filepath.Join(t.TempDir(), "elsewhere-repo"))
	m.selectGroupRow(t, "origin")
	m.newSessionAgent = newSessionAgentAuto
	useToolChooser(t, func(extension.ToolRequest) (string, error) { return "", errNoChoice })
	_, cmd := m.startNewSession()
	m.selectGroupRow(t, "elsewhere")
	m.update(cmd())
	if m.mode != modeAgentPick {
		t.Fatalf("no choice left mode %v, want picker", m.mode)
	}
	typeInto(t, m, "ready-tool")
	pressKey(t, m, enterKey())
	m.leaveFocusForFixture(t)
	rows := m.sessionRows()
	if len(rows) != 1 || rows[0].Group != "origin" || rows[0].Cwd != origin {
		t.Fatalf("fallback picker launched %+v, want it in the origin group (error %q)", rows, m.errBar.text)
	}
}

func TestAutoRouteFallbackTerminalKeepsTheGroupSelectedWhenNWasPressed(t *testing.T) {
	m := buildModel(t)
	origin := filepath.Join(t.TempDir(), "origin-repo")
	groupAt(t, m, "origin", origin)
	groupAt(t, m, "elsewhere", filepath.Join(t.TempDir(), "elsewhere-repo"))
	m.selectGroupRow(t, "origin")
	m.newSessionAgent = newSessionAgentAuto
	useToolChooser(t, func(extension.ToolRequest) (string, error) { return "", errNoChoice })
	_, cmd := m.startNewSession()
	m.selectGroupRow(t, "elsewhere")
	m.update(cmd())
	typeInto(t, m, "term")
	pressKey(t, m, enterKey())
	m.leaveFocusForFixture(t)
	rows := m.sessionRows()
	if len(rows) != 1 || !m.isShell(rows[0].Tool) || rows[0].Group != "origin" || rows[0].Cwd != origin {
		t.Fatalf("fallback terminal launched %+v, want a shell in the origin group (error %q)", rows, m.errBar.text)
	}
}

func TestAutoRouteFallbackTerminalKeepsTheSessionSelectedWhenNWasPressed(t *testing.T) {
	m := buildModel(t)
	groupDir, sessionDir := t.TempDir(), t.TempDir()
	if err := m.store.CreateGroup("backend", groupDir); err != nil {
		t.Fatal(err)
	}
	groupAt(t, m, "elsewhere", filepath.Join(t.TempDir(), "elsewhere-repo"))
	createSession(t, m, "agent", sessionDir, "backend")
	m.selectSessionRow(t, "agent")
	agent, _ := m.selected()
	m.newSessionAgent = newSessionAgentAuto
	useToolChooser(t, func(extension.ToolRequest) (string, error) { return "", errNoChoice })
	_, cmd := m.startNewSession()
	m.selectGroupRow(t, "elsewhere")
	m.update(cmd())
	typeInto(t, m, "term")
	pressKey(t, m, enterKey())
	m.leaveFocusForFixture(t)
	var shell store.Session
	for _, sess := range m.sessionRows() {
		if m.isShell(sess.Tool) {
			shell = sess
		}
	}
	if shell.ParentID != agent.ID || shell.Group != "backend" || shell.Cwd != resolved(t, sessionDir) {
		t.Fatalf("fallback terminal = %+v, want nested under %q in %q (error %q)", shell, agent.ID, sessionDir, m.errBar.text)
	}
}

func TestAutoRouteRefusesALaunchAfterLaunchAccountsChanged(t *testing.T) {
	m := buildModel(t)
	groupAt(t, m, "proj", filepath.Join(t.TempDir(), "sample-repo"))
	m.newSessionAgent = newSessionAgentAuto
	tool := m.cfg.Tools["ready-tool"]
	tool.AccountEnv = "SHARED_TOKEN"
	m.cfg.Tools["ready-tool"] = tool
	useToolChooser(t, func(extension.ToolRequest) (string, error) { return "ready-tool", nil })
	_, cmd := m.startNewSession()
	if cmd == nil {
		t.Fatal("auto route did not ask the chooser")
	}
	if err := m.store.SetSetting(store.AccountRoutingSetting, accounts.Extension); err != nil {
		t.Fatal(err)
	}
	m.update(cmd())
	if m.mode != modeAgentPick || len(m.sessionRows()) != 0 {
		t.Fatalf("launch-account change left mode %v with %d sessions, want the picker and none", m.mode, len(m.sessionRows()))
	}
	if !strings.Contains(m.errBar.text, "launch accounts changed") {
		t.Fatalf("missing launch-account-change explanation: %q", m.errBar.text)
	}
}

func TestAutoRouteRefusesAGroupChangedWhileTheChooserAnswers(t *testing.T) {
	for _, change := range []struct {
		name  string
		apply func(*testing.T, *Model)
	}{
		{"renamed", func(t *testing.T, m *Model) {
			if err := m.store.RenameGroup("origin", "renamed"); err != nil {
				t.Fatal(err)
			}
			m.renameGroupLocally("origin", "renamed", m.groupPaths["origin"])
		}},
		{"archived", func(t *testing.T, m *Model) {
			if err := m.store.SetGroupArchived("origin", true); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(change.name, func(t *testing.T) {
			m := buildModel(t)
			groupAt(t, m, "origin", filepath.Join(t.TempDir(), "origin-repo"))
			m.newSessionAgent = newSessionAgentAuto
			useToolChooser(t, func(extension.ToolRequest) (string, error) { return "ready-tool", nil })
			_, cmd := m.startNewSession()
			if cmd == nil {
				t.Fatal("auto route did not ask the chooser")
			}
			change.apply(t, m)
			m.update(cmd())
			if m.mode != modeAgentPick || len(m.sessionRows()) != 0 {
				t.Fatalf("%s group left mode %v with %d sessions, want the picker and none", change.name, m.mode, len(m.sessionRows()))
			}
			if !strings.Contains(m.errBar.text, "group changed") {
				t.Fatalf("missing group-change explanation: %q", m.errBar.text)
			}
		})
	}
}

func TestAutoRouteFallbackPickerDropsAGroupArchivedWhileTheChooserAnswers(t *testing.T) {
	m := buildModel(t)
	groupAt(t, m, "elsewhere", filepath.Join(t.TempDir(), "elsewhere-repo"))
	groupAt(t, m, "origin", filepath.Join(t.TempDir(), "origin-repo"))
	m.newSessionAgent = newSessionAgentAuto
	useToolChooser(t, func(extension.ToolRequest) (string, error) { return "", errNoChoice })
	_, cmd := m.startNewSession()
	if err := m.store.SetGroupArchived("origin", true); err != nil {
		t.Fatal(err)
	}
	m.selectGroupRow(t, "elsewhere")
	m.update(cmd())
	if m.mode != modeAgentPick || m.agentPick.pinned {
		t.Fatalf("archived group left mode %v pinned=%v, want an unpinned picker", m.mode, m.agentPick.pinned)
	}
	if !strings.Contains(m.errBar.text, "group changed") {
		t.Fatalf("missing group-change explanation: %q", m.errBar.text)
	}
	typeInto(t, m, "ready-tool")
	pressKey(t, m, enterKey())
	m.leaveFocusForFixture(t)
	for _, sess := range m.sessionRows() {
		if sess.Group == "origin" {
			t.Fatalf("fallback picker launched %+v into the archived group", sess)
		}
	}
}

func TestAutoRouteFallbackPickerRechecksTheGroupOnSubmit(t *testing.T) {
	m := buildModel(t)
	groupAt(t, m, "elsewhere", filepath.Join(t.TempDir(), "elsewhere-repo"))
	groupAt(t, m, "origin", filepath.Join(t.TempDir(), "origin-repo"))
	m.selectGroupRow(t, "origin")
	m.newSessionAgent = newSessionAgentAuto
	useToolChooser(t, func(extension.ToolRequest) (string, error) { return "", errNoChoice })
	_, cmd := m.startNewSession()
	m.update(cmd())
	if m.mode != modeAgentPick || !m.agentPick.pinned {
		t.Fatalf("no choice left mode %v pinned=%v, want a pinned picker", m.mode, m.agentPick.pinned)
	}
	if _, _, err := m.store.RemoveGroup("origin"); err != nil {
		t.Fatal(err)
	}
	m.selectGroupRow(t, "elsewhere")
	typeInto(t, m, "ready-tool")
	pressKey(t, m, enterKey())
	if m.mode != modeAgentPick || m.agentPick.pinned || len(m.sessionRows()) != 0 {
		t.Fatalf("deleted group left mode %v pinned=%v rows %+v, want an unpinned picker and no launch", m.mode, m.agentPick.pinned, m.sessionRows())
	}
	if !strings.Contains(m.errBar.text, "group changed") {
		t.Fatalf("missing group-change explanation: %q", m.errBar.text)
	}
	pressKey(t, m, enterKey())
	m.leaveFocusForFixture(t)
	groups, err := m.store.Groups()
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range groups {
		if g.Name == "origin" {
			t.Fatal("fallback picker recreated the deleted group")
		}
	}
	rows := m.sessionRows()
	if len(rows) != 1 || rows[0].Group != "elsewhere" {
		t.Fatalf("fallback picker launched %+v, want it in the current group", rows)
	}
}

func TestAutoRouteFallbackTerminalLeavesASessionArchivedWhileTheChooserAnswers(t *testing.T) {
	m := buildModel(t)
	groupDir, sessionDir := t.TempDir(), t.TempDir()
	if err := m.store.CreateGroup("backend", groupDir); err != nil {
		t.Fatal(err)
	}
	loadStoredRows(t, m)
	createSession(t, m, "agent", sessionDir, "backend")
	m.selectSessionRow(t, "agent")
	agent, _ := m.selected()
	m.newSessionAgent = newSessionAgentAuto
	useToolChooser(t, func(extension.ToolRequest) (string, error) { return "", errNoChoice })
	_, cmd := m.startNewSession()
	if err := m.store.SetArchived(agent.ID, true); err != nil {
		t.Fatal(err)
	}
	for i := range m.sessions {
		if m.sessions[i].ID == agent.ID {
			m.sessions[i].Archived = true
		}
	}
	m.update(cmd())
	typeInto(t, m, "term")
	pressKey(t, m, enterKey())
	var shell store.Session
	for _, sess := range m.sessions {
		if m.isShell(sess.Tool) {
			shell = sess
		}
	}
	if shell.ID == "" || shell.ParentID != "" || shell.Group != "backend" || shell.Cwd != resolved(t, groupDir) {
		t.Fatalf("fallback terminal = %+v, want an unnested shell in the backend group at %q (error %q)", shell, groupDir, m.errBar.text)
	}
}

func TestAutoRouteAsksAgainWhenWorkStartsWhileTheChooserAnswers(t *testing.T) {
	m := buildModel(t)
	groupAt(t, m, "proj", filepath.Join(t.TempDir(), "sample-repo"))
	m.newSessionAgent = newSessionAgentAuto
	// The chooser takes ready-tool only while nothing is running on it.
	useToolChooser(t, func(req extension.ToolRequest) (string, error) {
		if req.Active["ready-tool"] > 0 {
			return "", errNoChoice
		}
		return "ready-tool", nil
	})
	_, cmd := m.startNewSession()
	if cmd == nil {
		t.Fatal("auto route did not ask the chooser")
	}
	m.sessions = append(m.sessions, store.Session{ID: "busy", Tool: "ready-tool", Status: status.Working})
	_, again := m.update(cmd())
	if again == nil {
		t.Fatalf("new in-flight work did not ask the chooser again (mode %v, error %q)", m.mode, m.errBar.text)
	}
	m.update(again())
	for _, sess := range m.sessions {
		if sess.ID != "busy" && sess.Tool == "ready-tool" {
			t.Fatalf("auto route launched %+v on an answer given before the work started", sess)
		}
	}
	if m.mode != modeAgentPick || !strings.Contains(m.errBar.text, "no CLI chosen") {
		t.Fatalf("second answer left mode %v with error %q, want the fallback picker", m.mode, m.errBar.text)
	}
}

func TestAutoRouteFallsBackToCLIPickerWhenNothingIsChosen(t *testing.T) {
	m := buildModel(t)
	m.newSessionAgent = newSessionAgentAuto
	useToolChooser(t, func(extension.ToolRequest) (string, error) { return "", errNoChoice })
	_, cmd := m.startNewSession()
	m.update(cmd())
	if m.mode != modeAgentPick || len(m.sessionRows()) != 0 {
		t.Fatalf("no choice left mode %v with %d sessions", m.mode, len(m.sessionRows()))
	}
	if !strings.Contains(m.errBar.text, "no CLI chosen") {
		t.Fatalf("missing fallback explanation: %q", m.errBar.text)
	}
}

func TestAutoRouteNeverOffersAHiddenCLI(t *testing.T) {
	m := buildModel(t)
	m.newSessionAgent = newSessionAgentAuto
	if err := m.store.SetSetting(hiddenToolsSetting, "ready-tool"); err != nil {
		t.Fatal(err)
	}
	useToolChooser(t, func(req extension.ToolRequest) (string, error) {
		for _, c := range req.Candidates {
			if c.Name == "ready-tool" {
				t.Error("hidden CLI was offered to the chooser")
			}
		}
		return "", errNoChoice
	})
	_, cmd := m.startNewSession()
	m.update(cmd())
	if m.mode != modeAgentPick {
		t.Fatalf("no choice left mode %v, want picker", m.mode)
	}
}

// The chooser is told which CLIs take a named account and whether the
// extension is choosing accounts, which is what it needs to leave those CLIs
// out when their own login is not what the launch would run on.
func TestAutoRouteTellsTheChooserAboutAccounts(t *testing.T) {
	m := buildModel(t)
	m.newSessionAgent = newSessionAgentAuto
	if err := m.store.SetSetting(store.AccountRoutingSetting, accounts.Extension); err != nil {
		t.Fatal(err)
	}
	tool := m.cfg.Tools["ready-tool"]
	tool.AccountEnv = "SHARED_TOKEN"
	m.cfg.Tools["ready-tool"] = tool
	var got extension.ToolRequest
	useToolChooser(t, func(req extension.ToolRequest) (string, error) {
		got = req
		return "", errNoChoice
	})
	_, cmd := m.startNewSession()
	m.update(cmd())
	if !got.ChoosingAccounts {
		t.Fatal("the chooser was not told the extension chooses accounts")
	}
	for _, c := range got.Candidates {
		if (c.Account != nil) != (c.Name == "ready-tool") {
			t.Fatalf("candidate %+v: only ready-tool takes an account", c)
		}
		if c.Name == "ready-tool" && c.Account.Env != "SHARED_TOKEN" {
			t.Fatalf("candidate %+v lost its account settings", c)
		}
	}
}

// A build whose extension chooses no CLI has nobody to ask: n opens the box,
// and settings does not offer auto.
func TestAutoWithNoChooserOpensTheBox(t *testing.T) {
	m := buildModel(t)
	t.Cleanup(accounts.UseChooser(func() (extension.AccountChooser, error) { return nil, nil }))
	m.newSessionAgent = newSessionAgentAuto
	if _, cmd := m.startNewSession(); cmd != nil || m.mode != modeAgentPick {
		t.Fatalf("auto with no chooser: cmd %v, mode %v", cmd != nil, m.mode)
	}
	m.settings.field = settingsFieldNewSessionAgent
	m.settings.newSessionAgent = newSessionAgentDefault
	m.cycleSetting(1)
	if m.settings.newSessionAgent == newSessionAgentAuto {
		t.Fatal("settings offered auto with no chooser")
	}
}

func typeInto(t *testing.T, m *Model, text string) {
	t.Helper()
	for _, r := range text {
		pressKey(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// n asks, until somebody says otherwise. The box is the default because the
// CLI is a decision rather than a setting somebody has to go and change; the
// modes below are for the operator who has made that decision once and does
// not want the question again.
func TestNAsksWhichAgentByDefault(t *testing.T) {
	m := buildModel(t)
	groupAt(t, m, "proj", filepath.Join(t.TempDir(), "sample-repo"))

	pressKey(t, m, instantKey())

	if m.mode != modeAgentPick {
		t.Fatalf("n opened %v, want the agent box", m.mode)
	}
	if len(m.sessionRows()) != 0 {
		t.Fatal("n spawned before anybody answered which agent")
	}
}

// The box opens on the CLI the last spawn used, not on the settings default,
// so the common case is n then enter.
func TestAgentBoxOpensOnTheLastCLIUsed(t *testing.T) {
	m := buildModel(t)
	if err := m.store.SetSetting("default_tool", "claude"); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetSetting(lastToolSetting, "ready-tool"); err != nil {
		t.Fatal(err)
	}
	m.openAgentPick()
	if got := m.agentPick.input.Value(); got != "ready-tool" {
		t.Errorf("box opened on %q, want the last CLI used", got)
	}
	if got := m.agentPickName(); got != "ready-tool" {
		t.Errorf("selection = %q, want the last CLI used", got)
	}
}

// A CLI turned off since it was last used is not offered back: the box falls
// through to the settings default rather than opening on something that would
// refuse to launch.
func TestAgentBoxSkipsALastCLIThatIsNowHidden(t *testing.T) {
	m := buildModel(t)
	if err := m.store.SetSetting("default_tool", "claude"); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetSetting(lastToolSetting, "ready-tool"); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetSetting(hiddenToolsSetting, "ready-tool"); err != nil {
		t.Fatal(err)
	}
	m.openAgentPick()
	if got := m.agentPickName(); got != "claude" {
		t.Errorf("selection = %q, want the settings default once the last CLI is off", got)
	}
}

// The prefill is overridable in one keystroke. Typing over a box that opened
// on a value replaces it, the way every other field that opens on a value
// behaves -- not "ready-toolc".
func TestTypingReplacesTheWholePrefill(t *testing.T) {
	m := buildModel(t)
	if err := m.store.SetSetting(lastToolSetting, "ready-tool"); err != nil {
		t.Fatal(err)
	}
	m.openAgentPick()
	typeInto(t, m, "cl")
	if got := m.agentPick.input.Value(); got != "cl" {
		t.Fatalf("box = %q, want only what was typed", got)
	}
	if got := m.agentPickName(); got != "claude" {
		t.Fatalf("selection = %q, want the CLI the typing names", got)
	}
	// And a second character extends the filter rather than replacing again.
	typeInto(t, m, "a")
	if got := m.agentPick.input.Value(); got != "cla" {
		t.Fatalf("box = %q, want the filter extended", got)
	}
}

// Backspace on a prefill means "clear it", not "trim a character": the text
// reads as selected, so it goes whole.
func TestBackspaceClearsThePrefillWhole(t *testing.T) {
	m := buildModel(t)
	if err := m.store.SetSetting(lastToolSetting, "ready-tool"); err != nil {
		t.Fatal(err)
	}
	m.openAgentPick()
	pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if got := m.agentPick.input.Value(); got != "" {
		t.Fatalf("box = %q, want the prefill gone whole", got)
	}
}

// Enter starts the CLI the box is showing, in the group under the cursor,
// with nothing else asked.
func TestAgentBoxSpawnsWhatWasTyped(t *testing.T) {
	m := buildModel(t)
	dir := filepath.Join(t.TempDir(), "sample-repo")
	groupAt(t, m, "proj", dir)

	pressKey(t, m, instantKey())
	typeInto(t, m, "ready-tool")
	pressKey(t, m, enterKey())

	m.leaveFocusForFixture(t)
	rows := m.sessionRows()
	if len(rows) != 1 {
		t.Fatalf("sessions = %d, want the one the box started (err %q)", len(rows), m.errBar.text)
	}
	if rows[0].Tool != "ready-tool" {
		t.Errorf("tool = %q, want the CLI that was typed", rows[0].Tool)
	}
	if rows[0].Group != "proj" || rows[0].Cwd != dir {
		t.Errorf("group = %q cwd = %q, want the group under the cursor", rows[0].Group, rows[0].Cwd)
	}
	last, err := m.store.Setting(lastToolSetting)
	if err != nil {
		t.Fatal(err)
	}
	if last != "ready-tool" {
		t.Errorf("last tool = %q, want the box to open on it next time", last)
	}
}

// A filter naming nothing refuses rather than launching whatever was
// selected before it was typed.
func TestAgentBoxRefusesAFilterThatMatchesNothing(t *testing.T) {
	m := buildModel(t)
	groupAt(t, m, "proj", filepath.Join(t.TempDir(), "sample-repo"))

	pressKey(t, m, instantKey())
	typeInto(t, m, "zzz")
	pressKey(t, m, enterKey())

	if m.mode != modeAgentPick {
		t.Fatalf("mode = %v, want the box still open on a name that matches nothing", m.mode)
	}
	if len(m.sessionRows()) != 0 {
		t.Fatal("a filter matching nothing still launched something")
	}
	if !strings.Contains(m.errBar.text, "zzz") {
		t.Errorf("error = %q, want it to name what matched nothing", m.errBar.text)
	}
}

// The arrows move the selection and write it into the box, so what enter
// starts is always what the box says.
func TestArrowsWriteTheSelectionIntoTheBox(t *testing.T) {
	m := buildModel(t)
	m.openAgentPick()
	first := m.agentPickName()
	m.cycleAgentPick(1)
	second := m.agentPickName()
	if second == first {
		t.Fatal("the arrow did not move the selection")
	}
	if got := m.agentPick.input.Value(); got != second {
		t.Fatalf("box = %q, want the name the arrow selected (%q)", got, second)
	}
	// And the arrowed-to name is still one keystroke from being replaced.
	typeInto(t, m, "c")
	if got := m.agentPick.input.Value(); got != "c" {
		t.Fatalf("box = %q, want typing to replace an arrowed-to name too", got)
	}
}

// esc leaves without a session, and without moving the box's memory.
func TestEscLeavesTheAgentBoxWithoutSpawning(t *testing.T) {
	m := buildModel(t)
	groupAt(t, m, "proj", filepath.Join(t.TempDir(), "sample-repo"))

	pressKey(t, m, instantKey())
	pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})

	if m.mode != modeList {
		t.Fatalf("mode = %v, want the list back", m.mode)
	}
	if len(m.sessionRows()) != 0 {
		t.Fatal("esc still spawned")
	}
}

// The card does not wrap a field value, so the alternatives are cut to what
// fits rather than run past the border on a narrow terminal.
func TestAgentBoxFitsANarrowTerminal(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 44, 30
	m.openAgentPick()
	for _, line := range strings.Split(ansi.Strip(m.viewAgentPick()), "\n") {
		if w := len([]rune(line)); w > m.width {
			t.Fatalf("a line ran %d columns past a %d-column terminal: %q", w-m.width, m.width, line)
		}
	}
}

// The prefill is a value, not a filter: the box opens showing the other CLIs,
// and the arrows move through all of them. Narrowing to the name it opened on
// would leave a prefilled box with no visible alternatives, which reads as a
// label rather than as a question, and arrows that appeared dead.
func TestThePrefillDoesNotNarrowTheBoxToItself(t *testing.T) {
	m := buildModel(t)
	if err := m.store.SetSetting(lastToolSetting, "ready-tool"); err != nil {
		t.Fatal(err)
	}
	m.openAgentPick()
	if got, want := len(m.agentPickMatches()), len(m.agentPick.names); got != want {
		t.Fatalf("matches on open = %d, want all %d CLIs", got, want)
	}
	card := ansi.Strip(m.viewAgentPick())
	if !strings.Contains(card, "claude") {
		t.Fatalf("the alternatives are not on the card it opens as:\n%s", card)
	}
	// The arrows have somewhere to go, which a list narrowed to the prefill
	// would not have given them.
	before := m.agentPickName()
	m.cycleAgentPick(1)
	if m.agentPickName() == before {
		t.Fatalf("the arrow could not leave %q", before)
	}
	// And typing does filter, so the box is still a search box.
	typeInto(t, m, "ready")
	if got := m.agentPickMatches(); len(got) != 1 || got[0] != "ready-tool" {
		t.Fatalf("typed matches = %v, want only ready-tool", got)
	}
}

// While the box holds a filter rather than a name, the card still says which
// CLI enter would start -- "co" on its own does not.
func TestTheCardNamesTheCLIAFilterResolvedTo(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 96, 13
	m.openAgentPick()
	typeInto(t, m, "quiet")
	if got := m.agentPickName(); got != "quietchat" {
		t.Fatalf("selection = %q, want quietchat", got)
	}
	card := ansi.Strip(m.viewAgentPick())
	if !strings.Contains(card, "quietchat") {
		t.Fatalf("the card never names the CLI the filter resolved to:\n%s", card)
	}
}

// Nothing is cut: names that run past the budget wrap onto a line of their
// own rather than dropping off the card.
func TestAgentPickRowsWrapInsteadOfCutting(t *testing.T) {
	names := []string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc"}
	rows := agentPickRows(names, "cccccccccc", 12)
	if len(rows) != 3 {
		t.Fatalf("rows = %q, want one name per line at this width", rows)
	}
	joined := ansi.Strip(strings.Join(rows, "\n"))
	for _, name := range names {
		if !strings.Contains(joined, name) {
			t.Fatalf("rows = %q, missing %s", joined, name)
		}
	}
}

// The box offers the terminal beside the agents, and picking it opens the
// shell T would, not an agent.
func TestAgentBoxLaunchesATerminal(t *testing.T) {
	m := buildModel(t)
	groupAt(t, m, "proj", filepath.Join(t.TempDir(), "sample-repo"))
	pressKey(t, m, instantKey())
	card := ansi.Strip(m.viewAgentPick())
	if !strings.Contains(card, "terminal") {
		t.Fatalf("card does not offer the terminal:\n%s", card)
	}
	typeInto(t, m, "term")
	pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m.leaveFocusForFixture(t)
	rows := m.sessionRows()
	if len(rows) != 1 || !m.isShell(rows[0].Tool) {
		t.Fatalf("sessions = %+v, want one shell (err %q)", rows, m.errBar.text)
	}
}

// The saved order is the order the box offers, terminal included, and a CLI
// the order leaves out follows the ones it names.
func TestPickerFollowsTheSavedOrder(t *testing.T) {
	m := buildModel(t)
	if err := m.store.SetSetting(toolOrderSetting, "terminal,ready-tool,gone"); err != nil {
		t.Fatal(err)
	}
	names := m.pickerNames()
	if len(names) < 3 || names[0] != "terminal" || names[1] != "ready-tool" {
		t.Fatalf("picker = %v, want terminal then ready-tool first", names)
	}
	for _, name := range m.enabledToolNames() {
		if m.isShell(name) {
			t.Fatalf("enabledToolNames = %v, want agents only", m.enabledToolNames())
		}
	}
}

// Reordering in the CLIs panel is saved on the way out.
func TestCLIPanelReorderIsSaved(t *testing.T) {
	m := buildModel(t)
	m.openSettings()
	m.openCLIPicker()
	first := m.settings.cliNames[0]
	m.handleCLIPickerKey(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift})
	if m.settings.cliNames[1] != first || m.settings.cliCursor != 1 {
		t.Fatalf("names = %v cursor %d, want %s moved down", m.settings.cliNames, m.settings.cliCursor, first)
	}
	m.handleCLIPickerKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if got := m.orderedToolNames(); got[1] != first {
		t.Fatalf("saved order = %v, want %s second", got, first)
	}
}

// The whole point of the setting: n starts an agent instead of asking which,
// and the one it starts is the one the last spawn used.
func TestNStartsTheLastCLIWhenTheBoxIsOff(t *testing.T) {
	m := buildModel(t)
	groupAt(t, m, "proj", filepath.Join(t.TempDir(), "sample-repo"))
	if err := m.store.SetSetting(lastToolSetting, "ready-tool"); err != nil {
		t.Fatal(err)
	}
	m.newSessionAgent = newSessionAgentLast

	pressKey(t, m, instantKey())

	if m.mode == modeAgentPick {
		t.Fatal("n opened the box the setting turned off")
	}
	m.leaveFocusForFixture(t)
	rows := m.sessionRows()
	if len(rows) != 1 {
		t.Fatalf("sessions = %d, want the one n made (err %q)", len(rows), m.errBar.text)
	}
	if rows[0].Tool != "ready-tool" {
		t.Errorf("spawned %q, want the CLI the last spawn used", rows[0].Tool)
	}
}

// The other automatic answer is the settings default, which is the one that
// does not move when a one-off spawn runs something else.
func TestNStartsTheDefaultToolWhenTheBoxIsOff(t *testing.T) {
	m := buildModel(t)
	groupAt(t, m, "proj", filepath.Join(t.TempDir(), "sample-repo"))
	if err := m.store.SetSetting("default_tool", "ready-tool"); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetSetting(lastToolSetting, "quietchat"); err != nil {
		t.Fatal(err)
	}
	m.newSessionAgent = newSessionAgentDefault

	pressKey(t, m, instantKey())

	m.leaveFocusForFixture(t)
	rows := m.sessionRows()
	if len(rows) != 1 {
		t.Fatalf("sessions = %d, want the one n made (err %q)", len(rows), m.errBar.text)
	}
	if rows[0].Tool != "ready-tool" {
		t.Errorf("spawned %q, want the settings default rather than the last CLI used", rows[0].Tool)
	}
}

// A CLI turned off in settings is not launched behind anybody's back just
// because nobody is being asked any more: the automatic answer falls through
// the same way the box does.
func TestTheAutomaticAnswerSkipsAHiddenCLI(t *testing.T) {
	m := buildModel(t)
	groupAt(t, m, "proj", filepath.Join(t.TempDir(), "sample-repo"))
	if err := m.store.SetSetting(lastToolSetting, "ready-tool"); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetSetting(hiddenToolsSetting, "ready-tool"); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetSetting("default_tool", "quietchat"); err != nil {
		t.Fatal(err)
	}
	m.newSessionAgent = newSessionAgentLast

	pressKey(t, m, instantKey())

	m.leaveFocusForFixture(t)
	rows := m.sessionRows()
	if len(rows) != 1 {
		t.Fatalf("sessions = %d, want the one n made (err %q)", len(rows), m.errBar.text)
	}
	if rows[0].Tool != "quietchat" {
		t.Errorf("spawned %q, want the settings default: the last CLI used is turned off", rows[0].Tool)
	}
}

// ctrl+n is what makes turning the box off safe: the full form still asks,
// so a spawn that wants a different CLI is one key away rather than a trip
// through settings.
func TestTheFormStillAsksWhenTheBoxIsOff(t *testing.T) {
	m := buildModel(t)
	groupAt(t, m, "proj", filepath.Join(t.TempDir(), "sample-repo"))
	m.newSessionAgent = newSessionAgentLast

	pressKey(t, m, advancedKey())

	if m.mode != modeForm {
		t.Fatalf("ctrl+n opened %v, want the form (err %q)", m.mode, m.errBar.text)
	}
	if len(m.sessionRows()) != 0 {
		t.Fatal("ctrl+n spawned instead of asking")
	}
}

// A stored mode the build no longer has is not a reason to stop asking.
func TestNewSessionAgentFallsBackToAsking(t *testing.T) {
	m := buildModel(t)
	if err := m.store.SetSetting(newSessionAgentSetting, "whatever-it-was"); err != nil {
		t.Fatal(err)
	}
	if got := storedNewSessionAgent(m.store); got != newSessionAgentAsk {
		t.Errorf("stored mode = %q, want %q", got, newSessionAgentAsk)
	}
}

// The setting is reachable and it sticks: cycling the row and closing the
// card is the whole interaction.
func TestSettingsCyclesTheNewSessionAgent(t *testing.T) {
	m := buildModel(t)
	m.openSettings()
	if m.settings.newSessionAgent != newSessionAgentAsk {
		t.Fatalf("settings opened on %q, want %q", m.settings.newSessionAgent, newSessionAgentAsk)
	}
	for i := 0; i < settingsFieldNewSessionAgent; i++ {
		m.handleSettingsKey(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if m.settings.field != settingsFieldNewSessionAgent {
		t.Fatalf("stepping down reached field %d, want the new session agent row", m.settings.field)
	}
	m.handleSettingsKey(tea.KeyPressMsg{Code: tea.KeyRight})
	m.handleSettingsKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	if m.newSessionAgent != newSessionAgentLast {
		t.Errorf("mode = %q, want %q", m.newSessionAgent, newSessionAgentLast)
	}
	if got := storedNewSessionAgent(m.store); got != newSessionAgentLast {
		t.Errorf("stored mode = %q, want %q", got, newSessionAgentLast)
	}
}

func TestSettingsCanSelectAutoRouting(t *testing.T) {
	m := buildModel(t)
	useToolChooser(t, func(extension.ToolRequest) (string, error) { return "", errNoChoice })
	m.openSettings()
	for i := 0; i < settingsFieldNewSessionAgent; i++ {
		m.handleSettingsKey(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	for range 3 {
		m.handleSettingsKey(tea.KeyPressMsg{Code: tea.KeyRight})
	}
	if m.settings.newSessionAgent != newSessionAgentAuto {
		t.Fatalf("settings selected %q, want auto", m.settings.newSessionAgent)
	}
	m.handleSettingsKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := storedNewSessionAgent(m.store); got != newSessionAgentAuto {
		t.Fatalf("saved mode %q, want auto", got)
	}
}

// The card has to name the setting, or it is a behaviour change nobody can
// find.
func TestSettingsShowsTheNewSessionAgentRow(t *testing.T) {
	m := buildModel(t)
	m.openSettings()
	out := ansi.Strip(m.viewSettings())
	if !strings.Contains(out, "new session agent") {
		t.Errorf("settings card missing the new session agent row: %q", out)
	}
	if !strings.Contains(out, newSessionAgentAsk) {
		t.Errorf("settings card missing the current mode: %q", out)
	}
}
