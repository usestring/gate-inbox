package ui

import (
	"github.com/usestring/gate-inbox/internal/keymap"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// keyR and keyAltR are the two bindings under test, pressed the way the
// runtime delivers them. Every test here goes through handleKey rather than
// calling the handler: the binding is the change, and the rename tests that
// existed before this all called openRename directly, so nothing in the suite
// would have noticed r being wired to something else entirely.
var (
	keyR = tea.KeyPressMsg{Code: 'r', Text: "r"}
	// No Text on the alt press: a KeyPressMsg carrying Text reports that text
	// as its String(), so a Text-carrying alt press arrives at the switch as
	// plain "r" and would silently test the wrong binding.
	keyAltR = tea.KeyPressMsg{Code: 'r', Mod: tea.ModAlt}
)

// adoptPane hands a created session the pane it is running in, the way an
// adopt scan would, and returns the pane id.
func adoptPane(t *testing.T, m *Model, id string) string {
	t.Helper()
	paneID := strings.TrimSpace(mustTmux(t, "list-panes", "-t", tmux.SessionName(id), "-F", "#{pane_id}"))
	if paneID == "" {
		t.Fatal("no pane for the session")
	}
	if err := m.store.SetTmuxTarget(id, testSocket, paneID); err != nil {
		t.Fatal(err)
	}
	return paneID
}

// pressR presses r and runs the command it returned to completion, which is
// where every expensive part of a smart rename lives.
func (m *Model) pressR(t *testing.T) smartRenamedMsg {
	t.Helper()
	updated, cmd := m.handleKey(keyR)
	*m = *updated.(*Model)
	if cmd == nil {
		t.Fatal("r returned no command, so nothing was ever going to happen")
	}
	msg := cmd()
	named, ok := msg.(smartRenamedMsg)
	if !ok {
		t.Fatalf("r's command returned %T, want smartRenamedMsg", msg)
	}
	updated, _ = m.Update(named)
	*m = *updated.(*Model)
	return named
}

// r takes the name the agent already wrote for the conversation in the pane.
//
// This asserts on the rendered rail rather than on the store: a rename that
// lands in sqlite and never reaches m.sessions is invisible until the manager
// restarts, which is the feature failing while a store-level assertion still
// passes.
func TestRNamesTheSelectedRowAfterItsConversation(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 120, 34
	dir := t.TempDir()
	createSession(t, m, "sample-repo", dir, "")
	id := m.sessionRows()[0].ID
	paneID := adoptPane(t, m, id)

	const excerpt = "the poller now hashes the activity region before it compares it so a resize cannot read as streaming"
	mustTmux(t, "send-keys", "-t", paneID, "printf '%s\\n' "+shellQuote(excerpt), "Enter")

	home := t.TempDir()
	writeFakeTranscript(t, home, dir, "11111111-2222-3333-4444-555555555555",
		"Build UK business rates overpayment detection system", excerpt)
	m.convos = convo.New(home, "")

	m.applyCmd(t, nil)
	if rail := m.rail(); !strings.Contains(rail, "sample-repo") {
		t.Fatalf("the placeholder is not on the rail to begin with:\n%s", rail)
	}
	waitForAdoptedPaneText(t, paneID, excerpt)
	m.selectSessionRow(t, "sample-repo")

	named := m.pressR(t)
	if named.err != nil {
		t.Fatalf("smart rename: %v", named.err)
	}
	if named.name != "uk-business-rates" {
		t.Fatalf("name = %q, reason = %q", named.name, named.reason)
	}
	rail := m.rail()
	if !strings.Contains(rail, "uk-business-rates") {
		t.Fatalf("the title never reached the rail:\n%s", rail)
	}
	if strings.Contains(rail, "sample-repo") {
		t.Errorf("the placeholder is still on the rail:\n%s", rail)
	}
}

// The whole reason this key can exist. On the board it was built for, 23 of
// 26 agent panes sit in one directory, so a rename that resolved on the
// working directory would refuse every row the operator actually presses it
// on. Two sessions here share a directory exactly and are told apart only by
// the conversation's own words showing in the pane; nothing about the rows
// differs otherwise.
func TestRTellsApartTwoPanesInOneDirectory(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 120, 34
	dir := t.TempDir()
	home := t.TempDir()

	const rates = "reading the rates table now to see which overpayments the detector already catches"
	const poller = "the poller hashes the activity region before comparing so a resize cannot read as streaming"

	createSession(t, m, "sample-repo", dir, "")
	createSession(t, m, "sample-repo-2", dir, "")
	rows := m.sessionRows()
	if len(rows) != 2 {
		t.Fatalf("want two rows in one directory, got %d", len(rows))
	}
	byName := map[string]string{}
	for _, row := range rows {
		byName[row.Name] = row.ID
		if row.Cwd != dir {
			t.Fatalf("row %q is in %q, not the shared %q", row.Name, row.Cwd, dir)
		}
	}
	first := adoptPane(t, m, byName["sample-repo"])
	second := adoptPane(t, m, byName["sample-repo-2"])
	mustTmux(t, "send-keys", "-t", first, "printf '%s\\n' "+shellQuote(rates), "Enter")
	mustTmux(t, "send-keys", "-t", second, "printf '%s\\n' "+shellQuote(poller), "Enter")

	writeFakeTranscript(t, home, dir, "11111111-1111-1111-1111-111111111111",
		"Build UK business rates overpayment detection system", rates)
	writeFakeTranscript(t, home, dir, "22222222-2222-2222-2222-222222222222",
		"Stop the poller reading a resize as streaming", poller)
	m.convos = convo.New(home, "")

	m.applyCmd(t, nil)
	waitForAdoptedPaneText(t, first, rates)
	waitForAdoptedPaneText(t, second, poller)

	m.selectSessionRow(t, "sample-repo")
	if named := m.pressR(t); named.name != "uk-business-rates" {
		t.Fatalf("the first pane took %q (reason %q), want uk-business-rates", named.name, named.reason)
	}
	m.selectSessionRow(t, "sample-repo-2")
	if named := m.pressR(t); named.name != "stop-poller-reading" {
		t.Fatalf("the second pane took %q (reason %q)", named.name, named.reason)
	}

	rail := m.rail()
	for _, want := range []string{"uk-business-rates", "stop-poller-reading"} {
		if !strings.Contains(rail, want) {
			t.Fatalf("%q never reached the rail:\n%s", want, rail)
		}
	}
}

// The periodic pass refuses a name a person chose, and must: it is a ticker,
// and a ticker that overwrote deliberate names would be unusable. Pressing the
// key is the deliberate act, so it is allowed to do exactly what the pass may
// not, and this is the only thing separating the two paths.
func TestRReplacesANameAPersonChose(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 120, 34
	dir := t.TempDir()
	createSession(t, m, "my-own-name", dir, "")
	id := m.sessionRows()[0].ID
	paneID := adoptPane(t, m, id)
	if err := m.store.RenameSessionAs(id, "my-own-name", store.SourceUser); err != nil {
		t.Fatal(err)
	}

	const excerpt = "reading the rates table now to see which overpayments the detector already catches"
	mustTmux(t, "send-keys", "-t", paneID, "printf '%s\\n' "+shellQuote(excerpt), "Enter")
	home := t.TempDir()
	writeFakeTranscript(t, home, dir, "11111111-2222-3333-4444-555555555555",
		"Build UK business rates overpayment detection system", excerpt)
	m.convos = convo.New(home, "")

	m.applyCmd(t, nil)
	waitForAdoptedPaneText(t, paneID, excerpt)
	m.selectSessionRow(t, "my-own-name")

	// The periodic pass is asked first, and has to refuse, or this test would
	// pass without the forcing path existing at all.
	took, err := m.store.AutoRenameSession(id, "uk-business-rates", store.SourceTitle)
	if err != nil {
		t.Fatal(err)
	}
	if took {
		t.Fatal("the automatic path renamed a user-chosen row, so this proves nothing")
	}

	if named := m.pressR(t); named.name != "uk-business-rates" {
		t.Fatalf("name = %q, reason = %q", named.name, named.reason)
	}
	if rail := m.rail(); !strings.Contains(rail, "uk-business-rates") {
		t.Fatalf("the forced name never reached the rail:\n%s", rail)
	}
	// The store as well as the rail. A name reported back and painted, but
	// never written, is a rename that vanishes at the next poll -- and it is
	// exactly what the automatic path does when it declines a row, since it
	// reports that by returning false rather than an error.
	after, err := m.store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if after.Name != "uk-business-rates" {
		t.Fatalf("the store still holds %q, so the rename never persisted", after.Name)
	}
}

// Pressing r on a row that already carries the right name renames it again
// rather than reporting that there was nothing to do: the operator asked for
// the conversation's name and gets it, and the row is recorded as title-named
// either way.
func TestASecondPressRenamesAgainInsteadOfDeclining(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 120, 34
	dir := t.TempDir()
	createSession(t, m, "sample-repo", dir, "")
	paneID := adoptPane(t, m, m.sessionRows()[0].ID)

	const excerpt = "reading the rates table now to see which overpayments the detector already catches"
	mustTmux(t, "send-keys", "-t", paneID, "printf '%s\\n' "+shellQuote(excerpt), "Enter")
	home := t.TempDir()
	writeFakeTranscript(t, home, dir, "11111111-2222-3333-4444-555555555555",
		"Build UK business rates overpayment detection system", excerpt)
	m.convos = convo.New(home, "")
	m.applyCmd(t, nil)
	waitForAdoptedPaneText(t, paneID, excerpt)
	m.selectSessionRow(t, "sample-repo")

	if named := m.pressR(t); named.name != "uk-business-rates" {
		t.Fatalf("the first press took %q (reason %q)", named.name, named.reason)
	}
	m.selectSessionRow(t, "uk-business-rates")

	named := m.pressR(t)
	if named.name != "uk-business-rates" {
		t.Fatalf("the second press took %q (reason %q), not the name again", named.name, named.reason)
	}
	if !m.errBar.worked() {
		t.Errorf("the outcome renders as a failure: %q", m.errBar.text)
	}
	if !strings.Contains(m.errBar.text, "renamed to uk-business-rates") {
		t.Errorf("the bar reported %q, not the rename", m.errBar.text)
	}
	if rail := m.rail(); !strings.Contains(rail, "uk-business-rates") {
		t.Errorf("the row lost its name to a no-op rename:\n%s", rail)
	}
	// The write is the point: a row left on whatever source named it last is
	// one the periodic pass will not keep current.
	after, err := m.store.Get(m.sessionRows()[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Name != "uk-business-rates" || after.NameSource != store.SourceTitle {
		t.Fatalf("store holds name %q from %q", after.Name, after.NameSource)
	}
}

// A pane nothing can be attributed to has to say so on the frame. A key that
// silently does nothing is worse than no key: the operator presses it again.
func TestRSaysWhyWhenNothingCanBeAttributed(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 120, 34
	dir := t.TempDir()
	createSession(t, m, "sample-repo", dir, "")
	adoptPane(t, m, m.sessionRows()[0].ID)
	// An index pointed at an empty tree: there is no conversation to find.
	m.convos = convo.New(t.TempDir(), "")
	m.applyCmd(t, nil)
	m.selectSessionRow(t, "sample-repo")

	named := m.pressR(t)
	if named.name != "" {
		t.Fatalf("a pane with no conversation was renamed to %q", named.name)
	}
	if named.reason == "" {
		t.Fatal("the refusal carried no reason")
	}
	frame := ansi.Strip(m.frame())
	if !strings.Contains(frame, "smart rename") {
		t.Fatalf("the refusal never reached the frame:\n%s", frame)
	}
	if !strings.Contains(m.rail(), "sample-repo") {
		t.Error("the row lost its name to a rename that did not happen")
	}
}

// alt+r is the manual form: the one r used to open.
func TestAltROpensTheRenameForm(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 120, 34
	createSession(t, m, "sample-repo", t.TempDir(), "")
	m.applyCmd(t, nil)
	m.selectSessionRow(t, "sample-repo")

	updated, _ := m.handleKey(keyAltR)
	m = updated.(*Model)
	if m.mode != modeRename {
		t.Fatalf("alt+r left mode = %v, err = %q", m.mode, m.errBar.text)
	}
	if got := m.rename.input.Value(); got != "sample-repo" {
		t.Errorf("the form opened on %q, not the selected row", got)
	}
	frame := ansi.Strip(m.frame())
	if !strings.Contains(frame, "sample-repo") {
		t.Fatalf("the rename form never rendered:\n%s", frame)
	}
}

// A group has no conversation to be named after, so r opens the group card
// there. That is what r already meant on a group and the only thing it could.
func TestROnAGroupStillOpensTheGroupForm(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 120, 34
	if err := m.store.CreateGroup("backend", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	m.applyCmd(t, nil)
	m.selectGroupRow(t, "backend")

	updated, _ := m.handleKey(keyR)
	m = updated.(*Model)
	if m.mode != modeGroupForm || m.groupForm.editing != "backend" {
		t.Fatalf("r on a group left mode = %v, editing = %q, err = %q",
			m.mode, m.groupForm.editing, m.errBar.text)
	}
}

// The key handler must not capture a pane, refresh an index or write to
// sqlite: the store keeps one connection and a poll pass holds it for seconds
// on a loaded board, so a write issued from Update is felt as a key that did
// nothing. Everything expensive belongs to the returned command.
func TestRDoesNoWorkOnTheEventLoop(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 120, 34
	dir := t.TempDir()
	createSession(t, m, "sample-repo", dir, "")
	id := m.sessionRows()[0].ID
	paneID := adoptPane(t, m, id)
	const excerpt = "reading the rates table now to see which overpayments the detector already catches"
	mustTmux(t, "send-keys", "-t", paneID, "printf '%s\\n' "+shellQuote(excerpt), "Enter")
	home := t.TempDir()
	writeFakeTranscript(t, home, dir, "11111111-2222-3333-4444-555555555555",
		"Build UK business rates overpayment detection system", excerpt)
	m.convos = convo.New(home, "")
	m.applyCmd(t, nil)
	waitForAdoptedPaneText(t, paneID, excerpt)
	m.selectSessionRow(t, "sample-repo")

	updated, cmd := m.handleKey(keyR)
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("r did the work inline instead of returning a command")
	}
	after, err := m.store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if after.Name != "sample-repo" {
		t.Fatalf("the key handler wrote %q to the store before returning", after.Name)
	}
	if !m.smartNaming {
		t.Error("nothing marked the attempt in flight, so a held key would stack passes")
	}

	// And a second press while one is running starts nothing.
	if _, second := m.handleKey(keyR); second != nil {
		t.Error("a second press started a pass on top of the first")
	}
}

// Both keys have to be findable. A rebind nobody can read about is a
// regression, and ? is what the README calls the complete and current list.
func TestTheKeyMapShowsBothRenameKeys(t *testing.T) {
	rows := map[string]string{}
	for _, section := range helpModel().resolvedHelp() {
		for _, row := range section.rows {
			if rows[row.key] == "" {
				rows[row.key] = row.text
			}
		}
	}
	if !strings.Contains(rows["r"], "conversation") {
		t.Errorf("r is documented as %q, which does not describe a smart rename", rows["r"])
	}
	if rows[keymap.Display("alt+r")] == "" {
		t.Error("the key map never mentions alt+r")
	}

	// And the rows actually paint. The catalog overflows a 30-row terminal,
	// so this needs a frame tall enough to reach them rather than the default
	// help model, or it would pass on a screen that simply scrolled past.
	m := helpModel()
	m.height = 200
	frame := ansi.Strip(m.frame())
	for _, want := range []string{keymap.Display("alt+r"), "conversation running in it"} {
		if !strings.Contains(frame, want) {
			t.Errorf("the key map never paints %q:\n%s", want, frame)
		}
	}
}

// TestLiveSmartRenameOnARealPane is the only proof that the process signal --
// the one that separates panes sharing a directory when no prose is on screen
// -- resolves against a real Claude Code sidecar. It reads the operator's own
// ~/.claude, so it runs only when asked for by name, and it never writes: it
// links and reports, and asserts nothing about which name comes back.
//
//	GATE_INBOX_LIVE_SMART=1 GATE_INBOX_LIVE_SMART_CWD=/home/user/repos/sample-repo \
//	  go test ./internal/ui/ -run TestLiveSmartRenameOnARealPane -v
func TestLiveSmartRenameOnARealPane(t *testing.T) {
	if os.Getenv("GATE_INBOX_LIVE_SMART") == "" {
		t.Skip("live smart rename: set GATE_INBOX_LIVE_SMART=1 to read the real ~/.claude")
	}
	cwd := os.Getenv("GATE_INBOX_LIVE_SMART_CWD")
	if cwd == "" {
		t.Fatal("GATE_INBOX_LIVE_SMART_CWD must name a directory real agent panes are running in")
	}
	index := newConvoIndex()
	if err := index.Refresh([]string{cwd}); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	convos := index.Conversations()
	sharing := 0
	for _, c := range convos {
		if c.Cwd == cwd {
			sharing++
		}
	}
	t.Logf("%d conversations, %d of them in %s", len(convos), sharing, cwd)
	if sharing < 2 {
		t.Skipf("only %d conversation in %s: nothing to tell apart", sharing, cwd)
	}
	// One synthetic pane per conversation that carries a live pid, all in the
	// one directory, each holding only the pid its own sidecar declared. This
	// is the operator's board reduced to the thing under test: same tool, same
	// cwd, no prose on screen, nothing to separate them but the process. No
	// tmux is touched -- the pids come from the sidecar files, so this can
	// never reach a pane the operator is using.
	var panes []convo.Pane
	want := map[string]string{}
	for _, c := range convos {
		if c.Cwd != cwd || c.PID <= 0 {
			continue
		}
		key := "pane-" + c.ID
		panes = append(panes, convo.Pane{Key: key, Tool: c.Tool, Cwd: cwd, PIDs: []int{c.PID}})
		want[key] = c.ID
	}
	t.Logf("%d of the %d share the directory and carry a live pid", len(panes), sharing)
	if len(panes) == 0 {
		t.Fatal("no conversation in the shared directory carries a pid: " +
			"nothing but prose could separate these panes")
	}

	assigned := convo.Link(panes, convos)
	wrong := 0
	for key, wantID := range want {
		match, ok := assigned.For(key)
		switch {
		case !ok:
			t.Errorf("%s resolved to nothing", key)
			wrong++
		case match.Conversation.ID != wantID:
			t.Errorf("%s took conversation %s, want %s", key, match.Conversation.ID, wantID)
			wrong++
		}
	}
	t.Logf("linked %d of %d same-directory panes to their own conversation, %d unresolved",
		len(want)-wrong, len(want), len(assigned.Unresolved))
}

// A tool that can be asked to name itself is asked. r types its rename command
// into the pane and stops there: the agent reads its own conversation and
// answers with the rename subcommand, which the poller picks up. Deriving a
// name from the transcript is the fallback for tools that cannot be asked.
func TestRAsksTheAgentWhenTheToolHasARenameCommand(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 120, 34
	tool := m.cfg.Tools["claude"]
	tool.RenameCommand = "/rename"
	m.cfg.Tools["claude"] = tool
	createSession(t, m, "sample-repo", t.TempDir(), "")
	m.applyCmd(t, nil)
	m.selectSessionRow(t, "sample-repo")

	updated, cmd := m.handleKey(keyR)
	m = updated.(*Model)
	if cmd != nil {
		t.Fatal("asking the agent runs no command of ours: the answer arrives through the poller")
	}
	if !strings.Contains(m.errBar.text, "name itself") {
		t.Fatalf("the bar said %q", m.errBar.text)
	}
	if !m.errBar.worked() {
		t.Errorf("asking renders as a failure: %q", m.errBar.text)
	}
	waitForPaneText(t, m, m.sessionRows()[0].ID, "/rename")
	// The name is the agent's answer, not ours to guess at in the meantime.
	if rail := m.rail(); !strings.Contains(rail, "sample-repo") {
		t.Errorf("the row was renamed before the agent answered:\n%s", rail)
	}
}

// A tool with no slash command is asked in prose rather than left to the
// derived name: installing a command is a convenience some CLIs offer, not the
// line between an agent that can name itself and one that cannot.
func TestRAsksInProseWhenTheToolHasNoRenameCommand(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 120, 34
	if m.cfg.Tools["claude"].RenameCommand != "" {
		t.Fatal("this test needs a tool with no rename command")
	}
	createSession(t, m, "sample-repo", t.TempDir(), "")
	m.applyCmd(t, nil)
	m.selectSessionRow(t, "sample-repo")

	updated, cmd := m.handleKey(keyR)
	m = updated.(*Model)
	if cmd != nil {
		t.Fatal("asking the agent runs no command of ours: the answer arrives through the poller")
	}
	if !strings.Contains(m.errBar.text, "name itself") {
		t.Fatalf("the bar said %q", m.errBar.text)
	}
	waitForPaneText(t, m, m.sessionRows()[0].ID, `"$GATE_INBOX_BIN" rename`)
}

// Opencode sessions carry the same /rename their generated config registers,
// so r types the command rather than prose: the agent reads its own
// conversation and answers with the rename subcommand, which the poller picks
// up. Deriving a name from the timestamp placeholder is what this replaces.
func TestRAsksOpencodeThroughItsRenameCommand(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 120, 34
	tool := m.cfg.Tools["claude"]
	tool.RenameCommand = "/rename"
	m.cfg.Tools["opencode"] = tool
	createSession(t, m, "sample-repo", t.TempDir(), "")
	m.applyCmd(t, nil)
	m.selectSessionRow(t, "sample-repo")
	// Rows carry copies taken at rebuild time, so the tool change has to land
	// on the row the key reads rather than on the stored session behind it.
	for i := range m.rows {
		if m.rows[i].isSession() {
			m.rows[i].sess.Tool = "opencode"
		}
	}

	updated, cmd := m.handleKey(keyR)
	m = updated.(*Model)
	if cmd != nil {
		t.Fatal("asking the agent runs no command of ours: the answer arrives through the poller")
	}
	if !strings.Contains(m.errBar.text, "name itself") {
		t.Fatalf("the bar said %q", m.errBar.text)
	}
	waitForPaneText(t, m, m.sessionRows()[0].ID, "/rename")
	if rail := m.rail(); !strings.Contains(rail, "sample-repo") {
		t.Errorf("the row was renamed before the agent answered:\n%s", rail)
	}
}

// An adopted pane carries neither the manager's config directory nor its
// session id, so the rename subcommand run inside it has nothing to write to.
// Those rows keep the derived name rather than being sent a command that
// cannot work.
func TestRDerivesForAnAdoptedPaneEvenWithARenameCommand(t *testing.T) {
	m := buildModel(t)
	tool := m.cfg.Tools["claude"]
	tool.RenameCommand = "/rename"
	m.cfg.Tools["claude"] = tool
	createSession(t, m, "sample-repo", t.TempDir(), "")
	adoptPane(t, m, m.sessionRows()[0].ID)
	m.applyCmd(t, nil)

	if command := m.renameCommandFor(m.sessionRows()[0]); command != "" {
		t.Fatalf("an adopted row would be sent %q", command)
	}
}
