package ui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/autoroute"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

func TestAutoRouteStartsAnEnabledCLI(t *testing.T) {
	m := buildModel(t)
	groupAt(t, m, "proj", filepath.Join(t.TempDir(), "sample-repo"))
	m.newSessionAgent = newSessionAgentAuto
	m.autoRouter = autoroute.New(func(_ context.Context, name string) (autoroute.Reading, error) {
		if name != "ready-tool" {
			return autoroute.Reading{}, autoroute.ErrNoQuota
		}
		now := time.Now()
		return autoroute.Reading{ObservedAt: now, Windows: []autoroute.Window{{Used: 10, ResetsAt: now.Add(4 * time.Hour), Duration: 5 * time.Hour}}}, nil
	})
	_, cmd := m.startNewSession()
	if cmd == nil {
		t.Fatal("auto route did not start a quota read")
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
	m.autoRouter = autoroute.New(func(_ context.Context, name string) (autoroute.Reading, error) {
		if name != "ready-tool" {
			return autoroute.Reading{}, autoroute.ErrNoQuota
		}
		now := time.Now()
		return autoroute.Reading{ObservedAt: now, Windows: []autoroute.Window{{Used: 10, ResetsAt: now.Add(4 * time.Hour), Duration: 5 * time.Hour}}}, nil
	})
	_, cmd := m.startNewSession()
	if cmd == nil {
		t.Fatal("auto route did not start a quota read")
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
	m.autoRouter = autoroute.New(func(_ context.Context, name string) (autoroute.Reading, error) {
		if name != "ready-tool" {
			return autoroute.Reading{}, autoroute.ErrNoQuota
		}
		now := time.Now()
		return autoroute.Reading{ObservedAt: now, Windows: []autoroute.Window{{Used: 10, ResetsAt: now.Add(4 * time.Hour), Duration: 5 * time.Hour}}}, nil
	})
	m.mode = modeFocus
	_, cmd := m.startNewSession()
	if cmd == nil {
		t.Fatal("auto route did not start a quota read from focus")
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
	m.autoRouter = autoroute.New(func(context.Context, string) (autoroute.Reading, error) {
		return autoroute.Reading{}, autoroute.ErrNoQuota
	})
	_, cmd := m.startNewSession()
	m.selectGroupRow(t, "elsewhere")
	m.update(cmd())
	if m.mode != modeAgentPick {
		t.Fatalf("unavailable quota left mode %v, want picker", m.mode)
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
	m.autoRouter = autoroute.New(func(context.Context, string) (autoroute.Reading, error) {
		return autoroute.Reading{}, autoroute.ErrNoQuota
	})
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
	m.autoRouter = autoroute.New(func(context.Context, string) (autoroute.Reading, error) {
		return autoroute.Reading{}, autoroute.ErrNoQuota
	})
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

func TestAutoRouteRefusesALaunchAfterAccountRoutingChanged(t *testing.T) {
	m := buildModel(t)
	groupAt(t, m, "proj", filepath.Join(t.TempDir(), "sample-repo"))
	m.newSessionAgent = newSessionAgentAuto
	tool := m.cfg.Tools["ready-tool"]
	tool.AccountEnv = "SHARED_TOKEN"
	m.cfg.Tools["ready-tool"] = tool
	m.autoRouter = autoroute.New(func(_ context.Context, name string) (autoroute.Reading, error) {
		if name != "ready-tool" {
			return autoroute.Reading{}, autoroute.ErrNoQuota
		}
		now := time.Now()
		return autoroute.Reading{ObservedAt: now, Windows: []autoroute.Window{{Used: 10, ResetsAt: now.Add(4 * time.Hour), Duration: 5 * time.Hour}}}, nil
	})
	_, cmd := m.startNewSession()
	if cmd == nil {
		t.Fatal("auto route did not start a quota read")
	}
	if err := m.store.SetSetting(store.AccountRoutingSetting, accounts.Smart); err != nil {
		t.Fatal(err)
	}
	m.update(cmd())
	if m.mode != modeAgentPick || len(m.sessionRows()) != 0 {
		t.Fatalf("routing change left mode %v with %d sessions, want the picker and none", m.mode, len(m.sessionRows()))
	}
	if !strings.Contains(m.errBar.text, "account routing changed") {
		t.Fatalf("missing routing-change explanation: %q", m.errBar.text)
	}
}

func TestAutoRouteRefusesAGroupChangedDuringTheQuotaRead(t *testing.T) {
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
			m.autoRouter = autoroute.New(func(_ context.Context, name string) (autoroute.Reading, error) {
				if name != "ready-tool" {
					return autoroute.Reading{}, autoroute.ErrNoQuota
				}
				now := time.Now()
				return autoroute.Reading{ObservedAt: now, Windows: []autoroute.Window{{Used: 10, ResetsAt: now.Add(4 * time.Hour), Duration: 5 * time.Hour}}}, nil
			})
			_, cmd := m.startNewSession()
			if cmd == nil {
				t.Fatal("auto route did not start a quota read")
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

func TestAutoRouteFallbackPickerDropsAGroupArchivedDuringTheQuotaRead(t *testing.T) {
	m := buildModel(t)
	groupAt(t, m, "elsewhere", filepath.Join(t.TempDir(), "elsewhere-repo"))
	groupAt(t, m, "origin", filepath.Join(t.TempDir(), "origin-repo"))
	m.newSessionAgent = newSessionAgentAuto
	m.autoRouter = autoroute.New(func(context.Context, string) (autoroute.Reading, error) {
		return autoroute.Reading{}, autoroute.ErrNoQuota
	})
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
	m.autoRouter = autoroute.New(func(context.Context, string) (autoroute.Reading, error) {
		return autoroute.Reading{}, autoroute.ErrNoQuota
	})
	_, cmd := m.startNewSession()
	m.update(cmd())
	if m.mode != modeAgentPick || !m.agentPick.pinned {
		t.Fatalf("unavailable quota left mode %v pinned=%v, want a pinned picker", m.mode, m.agentPick.pinned)
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

func TestAutoRouteFallbackTerminalLeavesASessionArchivedDuringTheQuotaRead(t *testing.T) {
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
	m.autoRouter = autoroute.New(func(context.Context, string) (autoroute.Reading, error) {
		return autoroute.Reading{}, autoroute.ErrNoQuota
	})
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

func TestAutoRouteRescoresWhenWorkStartsDuringTheQuotaRead(t *testing.T) {
	m := buildModel(t)
	groupAt(t, m, "proj", filepath.Join(t.TempDir(), "sample-repo"))
	m.newSessionAgent = newSessionAgentAuto
	m.autoRouter = autoroute.New(func(_ context.Context, name string) (autoroute.Reading, error) {
		if name != "ready-tool" {
			return autoroute.Reading{}, autoroute.ErrNoQuota
		}
		now := time.Now()
		// 85% used clears the 10-point reserve for no active work but not
		// the 15 points one running session adds.
		return autoroute.Reading{ObservedAt: now, Windows: []autoroute.Window{{Used: 85, ResetsAt: now.Add(4 * time.Hour), Duration: 5 * time.Hour}}}, nil
	})
	_, cmd := m.startNewSession()
	if cmd == nil {
		t.Fatal("auto route did not start a quota read")
	}
	m.sessions = append(m.sessions, store.Session{ID: "busy", Tool: "ready-tool", Status: status.Working})
	_, rescore := m.update(cmd())
	if rescore == nil {
		t.Fatalf("new in-flight work did not trigger a rescore (mode %v, error %q)", m.mode, m.errBar.text)
	}
	m.update(rescore())
	for _, sess := range m.sessions {
		if sess.ID != "busy" && sess.Tool == "ready-tool" {
			t.Fatalf("auto route launched %+v past the reserve for in-flight work", sess)
		}
	}
	if m.mode != modeAgentPick || !strings.Contains(m.errBar.text, "quota unavailable") {
		t.Fatalf("rescore left mode %v with error %q, want the quota fallback picker", m.mode, m.errBar.text)
	}
}

func TestAutoRouteFallsBackToCLIPickerWhenQuotaUnavailable(t *testing.T) {
	m := buildModel(t)
	m.newSessionAgent = newSessionAgentAuto
	m.autoRouter = autoroute.New(func(context.Context, string) (autoroute.Reading, error) {
		return autoroute.Reading{}, autoroute.ErrNoQuota
	})
	_, cmd := m.startNewSession()
	m.update(cmd())
	if m.mode != modeAgentPick || len(m.sessionRows()) != 0 {
		t.Fatalf("unavailable quota left mode %v with %d sessions", m.mode, len(m.sessionRows()))
	}
	if !strings.Contains(m.errBar.text, "quota unavailable") {
		t.Fatalf("missing fallback explanation: %q", m.errBar.text)
	}
}

func TestAutoRouteNeverReadsAHiddenCLI(t *testing.T) {
	m := buildModel(t)
	m.newSessionAgent = newSessionAgentAuto
	if err := m.store.SetSetting(hiddenToolsSetting, "ready-tool"); err != nil {
		t.Fatal(err)
	}
	m.autoRouter = autoroute.New(func(_ context.Context, name string) (autoroute.Reading, error) {
		if name == "ready-tool" {
			t.Fatal("hidden CLI was sent to quota routing")
		}
		return autoroute.Reading{}, autoroute.ErrNoQuota
	})
	_, cmd := m.startNewSession()
	m.update(cmd())
	if m.mode != modeAgentPick {
		t.Fatalf("no readable enabled quota left mode %v, want picker", m.mode)
	}
}

func TestAutoRouteSkipsSharedAccountTools(t *testing.T) {
	m := buildModel(t)
	m.newSessionAgent = newSessionAgentAuto
	if err := m.store.SetSetting(store.AccountRoutingSetting, accounts.Smart); err != nil {
		t.Fatal(err)
	}
	tool := m.cfg.Tools["ready-tool"]
	tool.AccountEnv = "SHARED_TOKEN"
	m.cfg.Tools["ready-tool"] = tool
	m.autoRouter = autoroute.New(func(_ context.Context, name string) (autoroute.Reading, error) {
		if name == "ready-tool" {
			t.Fatal("own-login quota was read for a shared-account tool")
		}
		return autoroute.Reading{}, autoroute.ErrNoQuota
	})
	_, cmd := m.startNewSession()
	if cmd != nil {
		m.update(cmd())
	}
	if m.mode != modeAgentPick {
		t.Fatalf("shared-account routing left mode %v, want picker", m.mode)
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
