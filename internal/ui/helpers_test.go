// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/priority"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/sysstat"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// soleWriter sends a stand-in's own copy of what it reads to /dev/null,
// leaving the pty's line discipline as the pane's only writer. With both
// writing, the tool's first write lands part way through the echo of the
// same paste and splices the text mid-word ("gate-inbox senrebase on
// main"), which shreds the words a test reads back and leaves fragments the
// activity region scores as working, shutting the delivery gate for good.
// One writer is also what a real agent CLI looks like: it holds its tty in
// raw mode and draws the composer itself.
const soleWriter = " > /dev/null"

func buildModel(t testing.TB) *Model {
	t.Helper()
	m, _ := buildModelWithStorePath(t)
	return m
}

// buildModelWithStorePath is buildModel, also reporting where it put the
// database. A test that needs a second connection to the same file -- to hold
// the write lock the way the mcp processes do -- cannot find it otherwise.
func buildModelWithStorePath(t testing.TB) (*Model, string) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	cfg := config.Config{
		Tools: map[string]config.Tool{
			"claude": {Command: "cat", DefaultStatus: status.Idle},
			"claude-hooked": {
				// A hooks-backed tool gets --settings appended, which bare
				// cat rejects by exiting: the launch script then execs a
				// shell, which runs what is delivered to the pane and sprays
				// its prompt through the text a test reads back.
				//
				// Don't exec the shell away: --settings is on its command
				// line, and the board reads that flag off the live process
				// tree to decide whether anything is still writing the
				// session's hook file.
				Command:        "sh -c 'cat" + soleWriter + "'",
				StatusSource:   "claude-hooks",
				DefaultStatus:  status.Idle,
				ActivityCutoff: "(?m)^❯",
				TurnEnd:        `^[✻✳✶✽✢·✦✧+*] \S+ for \d.*$`,
				BusyLine:       `^[✻✳✶✽✢·✦✧+*] (?:Waiting for \d+ background agents? to finish|.*· \d+ shells? still running)`,
				LimitLine:      `(?m)You've hit your .+limit`,
				ScrolledLine:   `Jump to bottom \(ctrl\+End\)`,
				Rules: []config.Rule{
					{State: status.Waiting, Pattern: "Enter to confirm"},
					{State: status.Errored, Pattern: `(?im)^\s*error:`},
				},
			},
			// claude-hooked as Claude Code actually is: it queues what is
			// typed into it mid-turn, and draws a spinner while it works.
			"claude-typeahead": {
				Command:        "sh -c 'cat" + soleWriter + "'",
				StatusSource:   "claude-hooks",
				DefaultStatus:  status.Idle,
				ActivityCutoff: "(?m)^❯",
				TurnEnd:        `^[✻✳✶✽✢·✦✧+*] \S+ for \d.*$`,
				TypeAhead:      true,
				InterruptKeys:  []string{"Escape"},
				Rules: []config.Rule{
					{State: status.Waiting, Pattern: "Enter to confirm"},
					{State: status.Working, Pattern: "esc to interrupt"},
				},
			},
			// The terminal tab, carrying no command and the shell flag, the
			// way the generated config ships it.
			"terminal": {Shell: true, DefaultStatus: status.Idle},
			"quietchat": {
				Command:        "cat" + soleWriter,
				DefaultStatus:  status.Idle,
				ActivityCutoff: "(?m)^›",
			},
			// claude-hooked's twin in every rule, in a pane that never draws
			// what it is given: stty -echo takes the tty's own echo away and
			// the reader prints nothing of its own. A paste into this pane
			// costs the driver's whole echo window, which is what an agent
			// mid-turn looks like from outside -- too busy to repaint for
			// the text it was just handed.
			"deaf-tool": {
				Command:        "sh -c 'stty -echo; exec cat > /dev/null'",
				StatusSource:   "claude-hooks",
				DefaultStatus:  status.Idle,
				ActivityCutoff: "(?m)^❯",
				TurnEnd:        `^[✻✳✶✽✢·✦✧+*] \S+ for \d.*$`,
				Rules: []config.Rule{
					{State: status.Waiting, Pattern: "Enter to confirm"},
				},
			},
			"ready-tool": {
				Command:        `printf '❯ ' && cat` + soleWriter,
				DefaultStatus:  status.Idle,
				ActivityCutoff: "(?m)^❯",
			},
			// ready-tool drawing its own input. The line discipline's
			// echo is dropped on macOS once tmux falls behind reading the
			// pty, so a paste of several hundred bytes can stop mid-word on
			// screen though every byte reached the reader. cat blocks on a
			// full pty instead of dropping, and with echo off it stays the
			// pane's only writer.
			"tall-tool": {
				Command:        `sh -c 'stty -echo; printf "❯ "; exec cat'`,
				DefaultStatus:  status.Idle,
				ActivityCutoff: "(?m)^❯",
			},
			"send-tool": {
				Command:        `printf '❯ ' && cat` + soleWriter,
				PromptMode:     "send",
				DefaultStatus:  status.Idle,
				ActivityCutoff: "(?m)^❯",
			},
			// Draws its input line at once and takes the prompt it launched
			// with half a second later, the way an agent finishes booting.
			"slow-take-tool": {
				Command:        `sh -c 'printf "❯ "; sleep 0.5; printf "\n❯ %s\n❯ " "$0"; cat` + soleWriter + `'`,
				DefaultStatus:  status.Idle,
				ActivityCutoff: "(?m)^❯",
			},
			// Stands in for the agent CLIs, which turn on mouse tracking and
			// scroll themselves instead of leaving history for tmux.
			"mouse-tool": {
				Command:       `printf '\033[?1003h\033[?1006h' && cat`,
				DefaultStatus: status.Idle,
			},
			// Same claim on the mouse without asking for SGR, which is the
			// one case the reports have to fall back to the original
			// encoding.
			"x10-tool": {
				Command:       `printf '\033[?1003h' && cat`,
				DefaultStatus: status.Idle,
			},
		},
	}
	dbPath := filepath.Join(tmuxtest.ScratchDir(t), "state.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	driver := newTestDriver(t, testSocket)
	engine, err := status.NewEngine(cfg)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	m := New(cfg, st, driver, engine, hooks.NewManager(t.TempDir()), "dev")
	m.width = 120
	m.height = 40
	t.Cleanup(func() {
		for _, s := range m.sessions {
			driver.Kill(s.ID)
		}
	})
	return m, dbPath
}

func (m *Model) applyCmd(t testing.TB, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		// Actions poke the background poller instead of returning a
		// command; tests run the equivalent refresh synchronously.
		cmd = m.refreshCmd()
	}
	msg := cmd()
	if msg == nil {
		return
	}
	updated, _ := m.Update(msg)
	*m = *updated.(*Model)
	m.settleResize(t)
}

// settleResize runs the off-loop resize pass to completion, which is what the
// runtime does with the command Update returned. It rebuilds that command
// rather than being handed it, so a test never has to thread one out of every
// Update in the package; the resizing flag is what keeps that honest, since
// it is set only when Update actually issued a pass.
func (m *Model) settleResize(t testing.TB) {
	t.Helper()
	// A pass re-runs only when the preview box moved under it, so a handful
	// of rounds is a bound on a test that has gone wrong rather than a limit
	// any real sequence reaches.
	for i := 0; i < 4 && m.pane.resizing; i++ {
		m.pane.resizing = false
		cmd := m.resizeSessions()
		if cmd == nil {
			return
		}
		updated, _ := m.Update(cmd())
		*m = *updated.(*Model)
	}
}

// applyTickBatch drives one Update and every command in the batch it
// returns, so a test exercises the wiring rather than calling the commands
// it expects. The repeating timers a tick re-arms are dropped: feeding one
// back would loop forever.
func (m *Model) applyTickBatch(t *testing.T, msg tea.Msg) {
	t.Helper()
	updated, cmd := m.Update(msg)
	*m = *updated.(*Model)
	m.settleResize(t)
	if cmd == nil {
		return
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		return
	}
	for _, one := range batch {
		if one == nil {
			continue
		}
		out := one()
		if _, repeat := out.(previewTickMsg); out == nil || repeat {
			continue
		}
		updated, _ := m.Update(out)
		*m = *updated.(*Model)
	}
}

// clearRequestOnCleanup drops the server-global detach marker when the test
// ends: one left behind reaches every later test that reads it.
func clearRequestOnCleanup(t *testing.T, m *Model) {
	t.Helper()
	t.Cleanup(func() {
		if err := m.tmux.ClearRequest(); err != nil {
			t.Errorf("ClearRequest: %v", err)
		}
	})
}

func (m *Model) sessionRows() []store.Session {
	var sessions []store.Session
	for _, r := range m.rows {
		if r.isSession() {
			sessions = append(sessions, r.sess)
		}
	}
	return sessions
}

func (m *Model) selectSessionRow(t testing.TB, name string) {
	t.Helper()
	for i, r := range m.rows {
		if r.isSession() && r.sess.Name == name {
			m.cursor = i
			return
		}
	}
	t.Fatalf("no session row named %q", name)
}

func (m *Model) selectGroupRow(t *testing.T, path string) {
	t.Helper()
	for i, r := range m.rows {
		if r.isGroup && r.group == path {
			m.cursor = i
			return
		}
	}
	t.Fatalf("no group row for %q", path)
}

// groupRowPaths lists the stored groups the tree paints, skipping root.
func (m *Model) groupRowPaths() []string {
	var paths []string
	for _, r := range m.rows {
		if r.isGroup && !r.isRoot() {
			paths = append(paths, r.group)
		}
	}
	return paths
}

func loadStoredRows(t *testing.T, m *Model) {
	t.Helper()
	sessions, err := m.store.ListSessions(true)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	groups, err := m.store.Groups()
	if err != nil {
		t.Fatalf("list groups: %v", err)
	}
	m.sessions = sessions
	m.groups = make([]string, len(groups))
	m.groupPaths = make(map[string]string, len(groups))
	m.archivedGroups = make(map[string]bool, len(groups))
	m.priorityGroups = make(map[string]priority.Tier, len(groups))
	for i, group := range groups {
		m.groups[i] = group.Name
		m.groupPaths[group.Name] = group.Path
		if group.Archived {
			m.archivedGroups[group.Name] = true
		}
		if group.Priority != priority.Unset {
			m.priorityGroups[group.Name] = group.Priority
		}
	}
	m.rebuildRows()
}

func listSessionIDs(t *testing.T, st *store.Store) []string {
	t.Helper()
	sessions, err := st.ListSessions(false)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	ids := make([]string, len(sessions))
	for i, sess := range sessions {
		ids[i] = sess.ID
	}
	return ids
}

func pickGroup(t testing.TB, m *Model, path string) {
	t.Helper()
	for i, opt := range m.form.groups {
		if opt.path == path && opt.sessID == "" {
			m.form.groupIndex = i
			return
		}
	}
	t.Fatalf("group %q not in picker options %v", path, m.form.groups)
}

func createSession(t testing.TB, m *Model, name, dir, group string) {
	t.Helper()
	m.openForm()
	m.form.name.SetValue(name)
	m.form.dir.SetValue(dir)
	m.form.toolIndex = 0
	pickGroup(t, m, group)
	_, cmd := m.submitForm()
	// A create now lands inside the session it made, which is the behaviour
	// under test elsewhere and only a fixture step here: these callers want a
	// session on the board, so this leaves focus and hands them the list.
	if m.mode != modeFocus {
		t.Fatalf("after submit, mode = %v, err = %q", m.mode, m.errBar.text)
	}
	m.applyCmd(t, cmd)
	m.leaveFocusForFixture(t)
}

// leaveFocusForFixture returns a fixture-built session to the list, so a test
// about list behaviour is not written against a focused frame.
func (m *Model) leaveFocusForFixture(t testing.TB) {
	t.Helper()
	if m.mode != modeFocus {
		return
	}
	m.applyCmd(t, m.leaveFocus())
	if m.mode != modeList {
		t.Fatalf("leaving focus left mode %v, err = %q", m.mode, m.errBar.text)
	}
}

// seedRepo builds a committed repo the worktree tests can branch from.
// It sits one level inside the temp directory so the sibling
// "<name>-worktrees" tree is cleaned up with it.
func seedRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := filepath.Join(tmuxtest.ScratchDir(t), "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@test"},
		{"config", "user.name", "test"},
		{"add", "."},
		{"commit", "-m", "seed"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

// initGitRepo turns an existing directory into a git repo, for a caller that
// wants to control when the directory is created separately from when it
// becomes a repo.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@test"},
		{"config", "user.name", "test"},
		{"commit", "--allow-empty", "-m", "seed"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

func windowWidth(t *testing.T, id string) int {
	t.Helper()
	out, err := tmuxCmd("display-message", "-p", "-t", "gi_"+id, "#{window_width}").CombinedOutput()
	if err != nil {
		t.Fatalf("display-message: %v: %s", err, out)
	}
	w, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("parse width %q: %v", out, err)
	}
	return w
}

func sessionNames(m *Model) []string {
	var names []string
	for _, sess := range m.sessionRows() {
		names = append(names, sess.Name)
	}
	return names
}

// footModel is a store-backed model with the machine meters already
// populated, for the rail foot and the modal rows that read them.
func footModel(t *testing.T) *Model {
	t.Helper()
	st, err := store.Open(filepath.Join(tmuxtest.ScratchDir(t), "state.db"))
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	m := &Model{store: st, version: "v0.2.0"}
	m.snap = sysstat.Snapshot{
		CPUPercent: 42, CPUOK: true,
		MemPercent: 63, MemOK: true, MemUsed: 10 << 30, MemTotal: 16 << 30,
		DiskPercent: 71, DiskOK: true, DiskFree: 120 << 30,
	}
	return m
}

func key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	}
	return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
}

// resizeNow runs the resize pass a test wants finished before it looks at
// what tmux thinks the window is.
// resizeWindow delivers a terminal size and the settle timer that follows
// it, then runs the per-session resizes to completion.
func (m *Model) resizeWindow(t testing.TB, width, height int) {
	t.Helper()
	updated, cmd := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	*m = *updated.(*Model)
	if cmd == nil {
		t.Fatalf("a resize to %dx%d armed no settle timer", width, height)
	}
	updated, cmd = m.Update(resizeSettleMsg{seq: m.resizeSeq})
	*m = *updated.(*Model)
	if cmd != nil {
		updated, _ = m.Update(cmd())
		*m = *updated.(*Model)
	}
	m.settleResize(t)
}

func (m *Model) resizeNow(t testing.TB) {
	t.Helper()
	// The chain, not just the first command: a resize while the pane is
	// scrolled back re-reads the region at the new size, and dropping that
	// read leaves one outstanding for good -- one read is out at a time, so
	// the next notch would find the wheel silent.
	drain(m, m.resizeSessions())
	m.settleResize(t)
}
