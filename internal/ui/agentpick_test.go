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
