package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/migrate"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// seedMigrateSource puts a claude row with a transcript on disk on the
// board, and points the migration at the fixture home holding it.
func seedMigrateSource(t *testing.T, m *Model) (store.Session, string) {
	t.Helper()
	claudeHome := t.TempDir()
	prev := migrateRoots
	migrateRoots = func() migrate.Roots { return migrate.Roots{ClaudeHome: claudeHome, CodexRoot: t.TempDir()} }
	t.Cleanup(func() { migrateRoots = prev })

	dir := t.TempDir()
	source := store.Session{
		ID:             "migrate-source",
		Name:           "source",
		Tool:           "claude",
		Cwd:            dir,
		Group:          "work",
		Status:         status.Idle,
		AgentSessionID: "conv-abc",
	}
	if err := m.store.CreateGroup("work", dir); err != nil {
		t.Fatal(err)
	}
	if err := m.store.CreateSession(source); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(claudeHome, "projects", "any-project", "conv-abc.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, []byte(`{"type":"user"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loadStoredRows(t, m)
	m.selectSessionRow(t, "source")
	return source, transcript
}

func TestMigrateKeyIncludesSameToolAndFollowsItInTheName(t *testing.T) {
	m := buildModel(t)
	source, transcript := seedMigrateSource(t, m)
	m.cfg.Tools["codex"] = config.Tool{Command: "cat", DefaultStatus: status.Idle}
	m.cfg.Tools["gemini"] = config.Tool{Command: "cat", DefaultStatus: status.Idle}

	updated, _ := m.handleKey(tea.KeyPressMsg{Code: 'M', Text: "M", Mod: tea.ModShift})
	m = updated.(*Model)
	if m.mode != modeMigrate || m.migrate.source.ID != source.ID {
		t.Fatalf("mode = %v, source = %q, err = %q", m.mode, m.migrate.source.ID, m.errBar.text)
	}
	if m.migrate.transcript.Path != transcript {
		t.Fatalf("transcript = %+v", m.migrate.transcript)
	}
	if got := m.migrateTool(); got != "claude" || m.migrate.name.Value() != "source-claude" {
		t.Fatalf("opened on %q named %q", got, m.migrate.name.Value())
	}
	m.handleMigrateKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if got := m.migrateTool(); got != "codex" || m.migrate.name.Value() != "source-codex" {
		t.Fatalf("after tab: %q named %q", got, m.migrate.name.Value())
	}
	// A name the operator typed is theirs; the tool no longer rewrites it.
	m.migrate.name.SetValue("payments-take-two")
	m.handleMigrateKey(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if got := m.migrateTool(); got != "claude" || m.migrate.name.Value() != "payments-take-two" {
		t.Fatalf("after shift+tab: %q named %q", got, m.migrate.name.Value())
	}
	view := m.viewMigrate()
	for _, want := range []string{"Migrate Session", "source", "claude", filepath.Base(transcript)} {
		if !strings.Contains(view, want) {
			t.Fatalf("card lacks %q:\n%s", want, view)
		}
	}
	updated, _ = m.handleMigrateKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m = updated.(*Model); m.mode != modeList {
		t.Fatalf("esc left mode %v", m.mode)
	}
}

func TestMigrateLaunchesOnTheTranscript(t *testing.T) {
	for _, target := range []string{"claude", "codex"} {
		t.Run(target, func(t *testing.T) { testMigrateLaunch(t, target) })
	}
}

func testMigrateLaunch(t *testing.T, target string) {
	t.Helper()
	m := buildModel(t)
	source, transcript := seedMigrateSource(t, m)
	promptFile := filepath.Join(t.TempDir(), "prompt")
	// The prompt rides the command line as its last argument, so the tool
	// writes what it was handed and then sits, as an agent would.
	m.cfg.Tools[target] = config.Tool{
		Command:       "sh -c 'printf %s \"$1\" > " + tmux.ShellQuote(promptFile) + "; exec cat' sh",
		DefaultStatus: status.Idle,
	}

	m.openMigrate()
	if target == "codex" {
		m.cycleMigrateTool(1)
	}
	if m.mode != modeMigrate {
		t.Fatalf("mode = %v, err = %q", m.mode, m.errBar.text)
	}
	updated, cmd := m.handleMigrateKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(*Model)
	if m.mode != modeFocus || m.errBar.text != "" {
		t.Fatalf("after migrate: mode=%v err=%q", m.mode, m.errBar.text)
	}
	m.applyCmd(t, cmd)
	m.leaveFocusForFixture(t)

	var moved store.Session
	for _, sess := range m.sessionRows() {
		if sess.Name == "source-"+target {
			moved = sess
		}
	}
	if moved.ID == "" {
		t.Fatal("migrated session not found")
	}
	if moved.Tool != target || moved.Cwd != source.Cwd || moved.Group != source.Group {
		t.Fatalf("moved = %+v, source = %+v", moved, source)
	}
	if moved.ID == source.ID || moved.AgentSessionID == source.AgentSessionID {
		t.Fatalf("migration did not create a fresh session: %+v", moved)
	}
	if moved.NameSource != store.SourceUser {
		t.Fatalf("name source = %q", moved.NameSource)
	}
	kept, err := m.store.Get(source.ID)
	if err != nil || kept.Tool != "claude" || kept.Archived {
		t.Fatalf("source after migrate = %+v, err %v", kept, err)
	}
	if !store.Linked(kept, moved) {
		t.Fatalf("migration sessions are not linked: %+v, %+v", kept, moved)
	}

	deadline := time.Now().Add(3 * time.Second)
	var raw []byte
	for time.Now().Before(deadline) {
		if raw, err = os.ReadFile(promptFile); err == nil && len(raw) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	prompt := string(raw)
	for _, want := range []string{transcript, "ran on claude", `"source" (id migrate-source)`, "continue the work"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("launch prompt lacks %q:\n%s", want, prompt)
		}
	}
	// The source pane is not up on this board, so the new agent is not sent
	// to ask a session that cannot answer.
	if strings.Contains(prompt, "still running") {
		t.Fatalf("a dead source was offered for questions:\n%s", prompt)
	}
}

func TestMigrateRequiresTranscriptButAllowsOnlyCurrentTool(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	if err := m.store.CreateGroup("work", dir); err != nil {
		t.Fatal(err)
	}
	m.applyCmd(t, m.refreshCmd())
	createSession(t, m, "fresh", dir, "work")
	m.selectSessionRow(t, "fresh")
	m.openMigrate()
	if m.mode != modeList || !strings.Contains(m.errBar.text, "no captured conversation id") {
		t.Fatalf("mode = %v, err = %q", m.mode, m.errBar.text)
	}

	seedMigrateSource(t, m)
	for name := range m.cfg.Tools {
		if name != "claude" && !m.cfg.Tools[name].Shell {
			delete(m.cfg.Tools, name)
		}
	}
	m.openMigrate()
	if m.mode != modeMigrate || m.migrateTool() != "claude" || len(m.migrate.toolNames) != 1 {
		t.Fatalf("mode = %v, err = %q", m.mode, m.errBar.text)
	}
}

func TestMigrationPairReordersTogetherInNormalAndFilteredViews(t *testing.T) {
	m := buildModel(t)
	for _, sess := range []store.Session{
		{ID: "before", Name: "keep-before", Tool: "claude", Status: status.Idle},
		{ID: "source", Name: "original", Tool: "claude", Status: status.Idle},
		{ID: "after", Name: "keep-after", Tool: "claude", Status: status.Idle},
		{ID: "replacement", Name: "keep-replacement", Tool: "codex", Status: status.Idle, MigrationID: "source"},
	} {
		if err := m.store.CreateSession(sess); err != nil {
			t.Fatal(err)
		}
	}
	loadStoredRows(t, m)
	assertRows := func(want string) {
		t.Helper()
		var ids []string
		for _, sess := range m.sessionRows() {
			ids = append(ids, sess.ID)
		}
		if got := strings.Join(ids, ","); got != want {
			t.Fatalf("rows = %s, want %s", got, want)
		}
	}
	assertRows("before,source,replacement,after")
	m.selectSessionRow(t, "keep-replacement")
	m.handleKey(tea.KeyPressMsg{Code: 'K', Text: "K"})
	assertRows("source,replacement,before,after")
	selected, _ := m.selected()
	if selected.ID != "replacement" {
		t.Fatalf("cursor = %s", selected.ID)
	}
	m.search = "keep"
	m.rebuildRows()
	m.handleKey(tea.KeyPressMsg{Code: 'J', Text: "J"})
	assertRows("before,replacement,after")
	m.search = ""
	loadStoredRows(t, m)
	assertRows("before,source,replacement,after")
	if !strings.Contains(m.frame(), "⇄") {
		t.Fatal("linked pane indicator missing")
	}
}

func TestMigratedInitialPromptUsesTheOriginalTask(t *testing.T) {
	m := buildModel(t)
	source := store.Session{ID: "original", MigrationID: "original", LaunchPrompt: "Original launch task"}
	moved := store.Session{ID: "replacement", MigrationID: "original", MigrationOpening: []string{"Original launch task"}, LaunchPrompt: "Generated handoff instructions"}
	m.sessions = []store.Session{source, moved}
	m.firstPrompts = map[string][]string{
		"original":    {"Original task from the transcript", "Original follow-up"},
		"replacement": {"Generated handoff instructions", "Continue"},
	}
	opening := m.openingPrompts(moved)
	if strings.Join(opening, "|") != "Original task from the transcript|Original follow-up" {
		t.Fatalf("opening = %q", opening)
	}
	m.sessions = []store.Session{moved}
	if got := m.openingPrompts(moved); len(got) != 1 || got[0] != "Original launch task" {
		t.Fatalf("after source removal = %q", got)
	}
	if moved.LaunchPrompt != "Generated handoff instructions" {
		t.Fatal("display rewrote actual launch prompt")
	}
}
