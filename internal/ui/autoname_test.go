package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/adopt"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// A row somebody named rides the sweep only for its opening prompts: once
// those are on file, or the tool keeps no transcript the index can read, the
// row costs nothing.
func TestAutoNameScanIgnoresRowsItMayNotRename(t *testing.T) {
	model := buildModel(t)
	model.sessions = []store.Session{
		{ID: "a", Name: "mine", Tool: "claude", Cwd: "/repo", NameSource: store.SourceUser, TmuxPaneID: "%1"},
		{ID: "b", Name: "theirs", Tool: "claude", Cwd: "/repo", NameSource: store.SourceAgent, TmuxPaneID: "%2"},
		{ID: "c", Name: "other", Tool: "opencode", Cwd: "/repo", NameSource: store.SourceUser, TmuxPaneID: "%3"},
	}
	model.firstPrompts = map[string][]string{
		"a": {"one", "two", "three"},
		"b": {"one", "two", "three"},
	}
	if cmd := model.autoNameScan(); cmd != nil {
		t.Error("a board of names nobody may replace still cost a pass")
	}
	model.firstPrompts["a"] = []string{"one"}
	if cmd := model.autoNameScan(); cmd == nil {
		t.Error("a named claude row short of its opening did not ride the sweep")
	}
}

func TestAutoNameScanIgnoresRowsWithNoAdoptedPane(t *testing.T) {
	model := buildModel(t)
	model.sessions = []store.Session{
		{ID: "a", Name: "demo", Tool: "claude", Cwd: "/repo", NameSource: store.SourceDerived},
		{ID: "b", Name: "demo-2", Tool: "claude", Cwd: "/repo", NameSource: store.SourceDerived, TmuxPaneID: "%2", Archived: true},
	}
	if cmd := model.autoNameScan(); cmd != nil {
		t.Error("a row with no pane to read still cost a pass")
	}
}

func TestAutoNameScanRunsForADerivedName(t *testing.T) {
	model := buildModel(t)
	model.sessions = []store.Session{
		{ID: "a", Name: "demo", Tool: "claude", Cwd: "/repo", NameSource: store.SourceDerived, TmuxPaneID: "%1"},
	}
	if cmd := model.autoNameScan(); cmd == nil {
		t.Error("a derived name was not offered for replacement")
	}
}

func TestASecondNamingPassWaitsForTheOneStillRunning(t *testing.T) {
	model := buildModel(t)
	model.sessions = []store.Session{
		{ID: "a", Name: "demo", Tool: "claude", Cwd: "/repo", NameSource: store.SourceDerived, TmuxPaneID: "%1"},
	}
	if cmd := model.autoNameScan(); cmd == nil {
		t.Fatal("the first pass did not start")
	}
	if cmd := model.autoNameScan(); cmd != nil {
		t.Error("a second pass started on top of the first")
	}
	model.Update(autoNamedMsg{})
	if cmd := model.autoNameScan(); cmd == nil {
		t.Error("no pass started after the first one reported back")
	}
}

// The rail is what the user reads, so this asserts on rendered text rather
// than on the store. A rename that lands in sqlite but never reaches
// m.sessions is invisible until the manager is restarted, which is the whole
// feature failing while every store-level test still passes.
//
// Nothing here runs a refresh between the message and the render. That is the
// point: a test that pumps a poll in between passes whether or not the message
// carried the name, because the poll re-reads the store either way.
func TestARenamedSessionReachesTheRailWithoutARefresh(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 120, 34
	createSession(t, m, "demo", t.TempDir(), "")
	id := m.sessionRows()[0].ID
	if err := m.store.RenameSessionAs(id, "demo", store.SourceDerived); err != nil {
		t.Fatal(err)
	}
	m.applyCmd(t, nil)
	if rail := railLinesText(m.railLines(48, m.listBodyHeight())); !strings.Contains(rail, "demo") {
		t.Fatalf("the placeholder name is not on the rail to begin with:\n%s", rail)
	}

	took, err := m.store.AutoRenameSession(id, "uk-business-rates", store.SourceTitle)
	if err != nil || !took {
		t.Fatalf("AutoRenameSession took=%v err=%v", took, err)
	}
	updated, _ := m.Update(autoNamedMsg{renamed: []renamedSession{{id: id, name: "uk-business-rates"}}})
	m = updated.(*Model)

	rail := railLinesText(m.railLines(48, m.listBodyHeight()))
	if !strings.Contains(rail, "uk-business-rates") {
		t.Fatalf("the new name never reached the rail:\n%s", rail)
	}
	if strings.Contains(rail, "demo") {
		t.Errorf("the old name is still on the rail:\n%s", rail)
	}
}

// A row the store refused -- the user renamed it between the snapshot and the
// write -- must not be put on the rail, or the rail would show a name the
// store disagrees with until the next poll took it away again.
func TestARowTheStoreRefusedNeverReachesTheRail(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 120, 34
	createSession(t, m, "mine", t.TempDir(), "")
	id := m.sessionRows()[0].ID
	m.applyCmd(t, nil)

	updated, _ := m.Update(autoNamedMsg{})
	m = updated.(*Model)
	rail := railLinesText(m.railLines(48, m.listBodyHeight()))
	if !strings.Contains(rail, "mine") || strings.Contains(rail, "uk-business-rates") {
		t.Fatalf("a pass that renamed nothing changed the rail:\n%s", rail)
	}
	if got := m.sessionRows()[0]; got.ID != id || got.Name != "mine" {
		t.Errorf("row = %+v", got)
	}
}

func TestTheNamingPassPutsTheTitleOnTheRail(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 120, 34
	dir := t.TempDir()
	createSession(t, m, "demo", dir, "")
	sess := m.sessionRows()[0]
	id := sess.ID

	paneID := strings.TrimSpace(mustTmux(t, "list-panes", "-t", tmux.SessionName(id), "-F", "#{pane_id}"))
	if paneID == "" {
		t.Fatal("no pane for the session")
	}
	if err := m.store.SetTmuxTarget(id, testSocket, paneID); err != nil {
		t.Fatal(err)
	}
	if err := m.store.RenameSessionAs(id, "demo", store.SourceDerived); err != nil {
		t.Fatal(err)
	}

	// The pane has to be showing prose from the transcript, which is the
	// signal that attributes a conversation to a pane when no agent process
	// records its own pid.
	const excerpt = "the poller now hashes the activity region before it compares it so a resize cannot read as streaming"
	mustTmux(t, "send-keys", "-t", paneID, "printf '%s\\n' "+shellQuote(excerpt), "Enter")

	home := t.TempDir()
	writeFakeTranscript(t, home, dir, "11111111-2222-3333-4444-555555555555",
		"Build UK business rates overpayment detection system", excerpt)
	m.convos = convo.New(home, "")

	m.applyCmd(t, nil)
	if got := railLinesText(m.railLines(48, m.listBodyHeight())); !strings.Contains(got, "demo") {
		t.Fatalf("the placeholder is not on the rail to begin with:\n%s", got)
	}

	waitForAdoptedPaneText(t, paneID, excerpt)
	cmd := m.autoNameScan()
	if cmd == nil {
		t.Fatal("the naming pass did not start")
	}
	msg := cmd()
	named, ok := msg.(autoNamedMsg)
	if !ok {
		t.Fatalf("naming pass returned %T", msg)
	}
	if named.err != nil {
		t.Fatalf("naming pass: %v", named.err)
	}
	if len(named.renamed) != 1 || named.renamed[0].name != "uk-business-rates" {
		t.Fatalf("renamed = %+v, want one row named uk-business-rates", named.renamed)
	}
	// No refresh between the message and the render, deliberately: the rail
	// has to be right on the next frame, not on the next poll.
	updated, _ := m.Update(msg)
	m = updated.(*Model)

	rail := railLinesText(m.railLines(48, m.listBodyHeight()))
	if !strings.Contains(rail, "uk-business-rates") {
		t.Fatalf("the title never reached the rail:\n%s", rail)
	}
}

func mustTmux(t *testing.T, args ...string) string {
	t.Helper()
	out, err := tmuxCmd(args...).CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %v: %v: %s", args, err, out)
	}
	return string(out)
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// waitForAdoptedPaneText blocks until the pane has actually drawn the line, so
// the test is not racing the shell it just typed into. It reads through the
// adopted-pane path rather than the driver, which is how the naming pass reads.
func waitForAdoptedPaneText(t *testing.T, paneID, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		text, err := adopt.CaptureLines(testSocket, paneID, 200)
		if err == nil && strings.Contains(convo.Normalize(text), convo.Normalize(want)) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the pane never drew the excerpt")
}

func writeFakeTranscript(t *testing.T, home, cwd, sessionID, title, excerpt string) {
	t.Helper()
	dir := filepath.Join(home, "projects", claudeProjectDir(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	records := []string{
		fmt.Sprintf(`{"type":"ai-title","aiTitle":%q,"sessionId":%q}`, title, sessionID),
		fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":%q,"message":{"role":"assistant","content":[{"type":"text","text":%q}]}}`, sessionID, cwd, excerpt),
		fmt.Sprintf(`{"type":"last-prompt","lastPrompt":"look at the rates table","leafUuid":"a"}`),
	}
	body := strings.Join(records, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, sessionID+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// claudeProjectDir mirrors Claude Code's mangling; the package under test has
// its own unexported copy, and a fixture that disagreed with it would pass by
// accident through the directory scan instead of proving the lookup.
func claudeProjectDir(cwd string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		}
		return '-'
	}, cwd)
}

// Drift writes through the same route as a first naming, so it has the same
// way of going stale, and it is the path a long-running session actually takes.
func TestADriftRenameAlsoReachesTheRail(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 120, 34
	dir := t.TempDir()
	createSession(t, m, "demo", dir, "")
	id := m.sessionRows()[0].ID
	paneID := strings.TrimSpace(mustTmux(t, "list-panes", "-t", tmux.SessionName(id), "-F", "#{pane_id}"))
	if err := m.store.SetTmuxTarget(id, testSocket, paneID); err != nil {
		t.Fatal(err)
	}
	if err := m.store.RenameSessionAs(id, "demo", store.SourceDerived); err != nil {
		t.Fatal(err)
	}
	const excerpt = "the poller now hashes the activity region before it compares it so a resize cannot read as streaming"
	mustTmux(t, "send-keys", "-t", paneID, "printf '%s\\n' "+shellQuote(excerpt), "Enter")

	// The title is about business rates; every recent prompt is about
	// something else and they agree with each other, which is drift.
	home := t.TempDir()
	writeDriftedTranscript(t, home, dir, "22222222-3333-4444-5555-666666666666",
		"Build UK business rates overpayment detection system", excerpt,
		"the grafana dashboard is empty again",
		"grafana still shows nothing for the loki panel",
		"fix the loki grafana panel query")
	m.convos = convo.New(home, "")
	m.applyCmd(t, nil)
	waitForAdoptedPaneText(t, paneID, excerpt)

	// Drift only lands once the same replacement has been derived twice, so
	// the first pass must leave the row on the title's own name.
	first := m.autoNameScan()
	if first == nil {
		t.Fatal("the first pass did not start")
	}
	updated, _ := m.Update(first())
	m = updated.(*Model)
	if rail := railLinesText(m.railLines(48, m.listBodyHeight())); !strings.Contains(rail, "uk-business-rates") {
		t.Fatalf("the first pass did not name the row from its title:\n%s", rail)
	}

	second := m.autoNameScan()
	if second == nil {
		t.Fatal("the second pass did not start")
	}
	updated, _ = m.Update(second())
	m = updated.(*Model)

	rail := railLinesText(m.railLines(48, m.listBodyHeight()))
	if !strings.Contains(rail, "grafana") || !strings.Contains(rail, "loki") {
		t.Fatalf("the drifted name never reached the rail:\n%s", rail)
	}
	stored, err := m.store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored.Name, "grafana") {
		t.Errorf("store name = %q, want the drifted name", stored.Name)
	}
}

// A pane taken by an adopt scan is named on the first sweep that can see the
// row, not on the naming ticker half a minute later.
func TestAdoptionNamesOnTheNextSweepRatherThanTheNextTick(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 120, 34
	createSession(t, m, "demo", t.TempDir(), "")
	id := m.sessionRows()[0].ID
	if err := m.store.SetTmuxTarget(id, testSocket, "%0"); err != nil {
		t.Fatal(err)
	}
	if err := m.store.RenameSessionAs(id, "demo", store.SourceDerived); err != nil {
		t.Fatal(err)
	}
	m.applyCmd(t, nil)

	updated, _ := m.Update(adoptedMsg{taken: 1})
	m = updated.(*Model)
	if !m.nameAfterRefresh {
		t.Fatal("adoption did not ask for a naming pass")
	}
	m.applyCmd(t, nil)
	if m.nameAfterRefresh {
		t.Error("the sweep that could see the new row did not run a naming pass")
	}
}

// The request survives a sweep that could not start a pass, so an adoption
// landing while one is already running is not silently dropped.
func TestAnEagerNamingRequestSurvivesAPassAlreadyRunning(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 120, 34
	createSession(t, m, "demo", t.TempDir(), "")
	id := m.sessionRows()[0].ID
	if err := m.store.SetTmuxTarget(id, testSocket, "%0"); err != nil {
		t.Fatal(err)
	}
	if err := m.store.RenameSessionAs(id, "demo", store.SourceDerived); err != nil {
		t.Fatal(err)
	}
	m.applyCmd(t, nil)

	m.autoNaming = true
	updated, _ := m.Update(adoptedMsg{taken: 1})
	m = updated.(*Model)
	m.applyCmd(t, nil)
	if !m.nameAfterRefresh {
		t.Fatal("the request was consumed by a sweep that started no pass")
	}
	m.autoNaming = false
	m.applyCmd(t, nil)
	if m.nameAfterRefresh {
		t.Error("the request never ran once the way was clear")
	}
}

func writeDriftedTranscript(t *testing.T, home, cwd, sessionID, title, excerpt string, prompts ...string) {
	t.Helper()
	dir := filepath.Join(home, "projects", claudeProjectDir(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	records := []string{
		fmt.Sprintf(`{"type":"ai-title","aiTitle":%q,"sessionId":%q}`, title, sessionID),
		fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":%q,"message":{"role":"assistant","content":[{"type":"text","text":%q}]}}`, sessionID, cwd, excerpt),
	}
	for i, prompt := range prompts {
		records = append(records, fmt.Sprintf(`{"type":"last-prompt","lastPrompt":%q,"leafUuid":"p%d"}`, prompt, i))
	}
	body := strings.Join(records, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, sessionID+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAutoNameScanReachesARowTheManagerLaunched(t *testing.T) {
	model := buildModel(t)
	model.sessions = []store.Session{
		{ID: "a", Name: "demo", Tool: "claude", Cwd: "/repo", NameSource: store.SourceDerived,
			AgentSessionID: "11111111-2222-3333-4444-555555555555"},
	}
	if cmd := model.autoNameScan(); cmd == nil {
		t.Error("a launched row carrying its own conversation id was passed over")
	}
}

// The crux of the instant spawn: a row nobody named, and nobody asked to name
// itself, still ends up with a real name.
//
// It has no adopted pane -- that column belongs to adoption and a launched
// session never fills it -- and nothing is typed into it here, so the only
// thing tying the row to a conversation is the id it launched with. If that
// path does not fire, an instant spawn wears its directory forever.
func TestTheNamingPassNamesASessionTheManagerLaunched(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 120, 34
	dir := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := m.store.AddGroup("proj", dir); err != nil {
		t.Fatal(err)
	}
	loadStoredRows(t, m)
	m.selectGroupRow(t, "proj")

	instantSpawn(t, m)

	sess := m.sessionRows()[0]
	if sess.Name != "demo" || sess.TmuxPaneID != "" {
		t.Fatalf("spawned row = %+v, want the derived name and no adopted pane", sess)
	}

	// A tool with a session_id_flag launches under an id the manager minted;
	// one that mints its own has it read back by the poller. Either way the
	// row carries it by the time a naming pass runs, which is what this sets.
	const agentID = "77777777-8888-9999-aaaa-bbbbbbbbbbbb"
	if err := m.store.SetAgentSessionID(sess.ID, agentID); err != nil {
		t.Fatal(err)
	}
	m.applyCmd(t, nil)

	home := t.TempDir()
	writeFakeTranscript(t, home, dir, agentID,
		"Build UK business rates overpayment detection system", "nothing this pane ever drew")
	// A second conversation of the same tool in the same directory, so the
	// row cannot be resolved by being the only candidate there. The id it
	// launched with is then the only thing that can tell the two apart, which
	// is what this test is for.
	writeFakeTranscript(t, home, dir, "99999999-8888-7777-6666-555555555555",
		"Rewrite the nightly warehouse export", "nor this")
	m.convos = convo.New(home, "")

	before := railLinesText(m.railLines(48, m.listBodyHeight()))
	if !strings.Contains(before, "demo") {
		t.Fatalf("the derived name is not on the rail to begin with:\n%s", before)
	}
	t.Logf("rail on the keypress:\n%s", before)

	scan := m.autoNameScan()
	if scan == nil {
		t.Fatal("the naming pass did not start for a launched row")
	}
	msg := scan()
	named, ok := msg.(autoNamedMsg)
	if !ok {
		t.Fatalf("naming pass returned %T", msg)
	}
	if named.err != nil {
		t.Fatalf("naming pass: %v", named.err)
	}
	if len(named.renamed) != 1 || named.renamed[0].name != "uk-business-rates" {
		t.Fatalf("renamed = %+v, want one row named uk-business-rates", named.renamed)
	}
	updated, _ := m.Update(msg)
	m = updated.(*Model)

	rail := railLinesText(m.railLines(48, m.listBodyHeight()))
	t.Logf("rail once the title resolved:\n%s", rail)
	if !strings.Contains(rail, "uk-business-rates") {
		t.Fatalf("the conversation's own title never reached the rail:\n%s", rail)
	}
	stored, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != "uk-business-rates" || stored.NameSource != store.SourceTitle {
		t.Errorf("stored name = %q source = %q", stored.Name, stored.NameSource)
	}
	// The pane says who it is too, and a bar left on the old name is the same
	// session calling itself two things on one screen.
	label := strings.TrimSpace(mustTmux(t, "show-option", "-t", tmux.SessionName(sess.ID), "-qv", "status-left"))
	if !strings.Contains(label, "uk-business-rates") {
		t.Errorf("status bar = %q, want the name the title pass chose", label)
	}
}
