package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// hogTool is the stand-in agent for TestHogNoticesE2E: a line prompt whose
// "$" row is its input line, so the board's delivery gates read it the way
// they read a real CLI's composer.
const hogTool = `
[tools.hogger]
command = "sh"
default_status = "idle"
activity_cutoff = "(?m)^\\$"
skip_rename_directive = true
rules = [{ state = "waiting", pattern = "Enter to confirm" }]
`

// hogConfig brings the tiers down to seconds. CPU tiers differ only in how
// long the load has held, so one busy loop walks notice, warn and stop in
// order; the percentage is low because this has to pass on a loaded box,
// where one busy loop does not get a whole core.
const hogConfig = `
[hogs]
sample_every = "2s"
reset_after = "10s"
cooldown = "1h"

[hogs.cpu]
notice = [{ percent = 20, for = "10s" }]
warn = [{ percent = 20, for = "20s" }]
stop = [{ percent = 20, for = "30s" }]

[hogs.memory]
notice = [{ gib = 0.5 }]
warn = [{ gib = 0.5, for = "10s" }, { growth_gib_per_min = 0.25, for = "10s" }]
stop = [{ gib = 0.5, for = "20s" }]
`

// hogAgent is one pane running testdata/hogagent.sh.
type hogAgent struct {
	id, dir string
}

func (a hogAgent) typed() string {
	b, _ := os.ReadFile(filepath.Join(a.dir, "typed.log"))
	return string(b)
}

// TestHogNoticesE2E runs the real board, built from this module, against
// stand-in agents whose panes hold real CPU and memory, and proves what a
// session is actually typed:
//
//   - a busy loop walks notice, warn and stop, each typed into the pane in
//     that order under the Gate Inbox fence, and the row's badge clears once
//     the loop is killed and reset_after has passed;
//   - a process holding 600 MiB walks the memory tiers the same way;
//   - a process growing steadily trips the growth rule while it is still
//     under every size rule;
//   - nothing is typed over a dialog or over a line the operator has part
//     written, and the newest notice is typed once each clears.
//
// Everything runs on test-owned tmux servers under a scratch GATE_INBOX_HOME.
// It burns about one core for a minute and holds about 1 GiB, so it is an
// operator check rather than CI:
//
//	GATE_INBOX_E2E_HOGS=1 [GATE_INBOX_E2E_HOGS_OUT=<dir>] \
//	  go test ./app -run TestHogNoticesE2E -timeout 10m -v
//
// GATE_INBOX_E2E_HOGS_OUT keeps each pane's final screen, the board's
// screens and the typed logs for a PR body.
func TestHogNoticesE2E(t *testing.T) {
	if os.Getenv("GATE_INBOX_E2E_HOGS") == "" {
		t.Skip("GATE_INBOX_E2E_HOGS unset")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("the board polls a tmux server")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("the memory loads are python3")
	}
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("hog detection reads /proc")
	}
	out := os.Getenv("GATE_INBOX_E2E_HOGS_OUT")
	if out == "" {
		out = t.TempDir()
	} else if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join("testdata", "hogagent.sh"))
	if err != nil {
		t.Fatal(err)
	}

	bin := buildBoard(t)
	agents := tmuxtest.NewSocket("hogs")
	boardHost := tmuxtest.NewSocket("hogboard")
	env := fixtureHome(t, "tmux_socket = \""+agents+"\"\n"+hogTool+hogConfig)
	home := envValue(env, "GATE_INBOX_HOME")
	tmpdir := envValue(env, "TMUX_TMPDIR")
	t.Cleanup(func() {
		killTestServer(t, tmpdir, boardHost)
		killTestServer(t, tmpdir, agents)
	})
	db := filepath.Join(home, "state.db")
	skipWelcome(t, db)
	driver, err := tmux.NewWithSocket(agents)
	if err != nil {
		t.Fatal(err)
	}

	start := func(id, load, screen string) hogAgent {
		t.Helper()
		dir := t.TempDir()
		// The trailing ":" keeps sh as the pane's root with the agent as its
		// child, the shape a launched CLI has.
		cmd := "bash " + script + " " + load + " " + screen + " " + dir + "; :"
		if err := driver.Create(id, dir, cmd, nil, 120, 40); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
		t.Cleanup(func() {
			stopAgent(dir)
			_ = driver.Kill(id)
		})
		st, err := store.Open(db)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		if err := st.CreateSession(store.Session{ID: id, Name: "hog " + load + " " + screen, Tool: "hogger", Status: "idle", Cwd: dir, CreatedAt: time.Now()}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
		return hogAgent{id: id, dir: dir}
	}
	pane := func(a hogAgent) string {
		text, _ := driver.CapturePane(a.id)
		return ansi.Strip(text)
	}

	cpu := start("c0de0001", "cpu", "prompt")
	mem := start("c0de0002", "mem", "prompt")
	grow := start("c0de0003", "grow", "prompt")
	dialog := start("c0de0004", "mem", "dialog")
	draft := start("c0de0005", "mem", "prompt")
	waitUntil(t, 15*time.Second, "the draft pane's prompt", func() bool { return strings.Contains(pane(draft), "$") })
	// The operator's part-written line, typed before the board is up, so the
	// notice is queued behind it rather than racing it.
	if err := driver.SendKeys(draft.id, "half typed by the operator"); err != nil {
		t.Fatal(err)
	}

	// The board runs in a pane of its own server, so its screen can be read.
	hostPath := hostSocket(t, tmpdir, boardHost)
	host := exec.Command("tmux", "-S", hostPath, "new-session", "-d", "-s", "board", "-x", "200", "-y", "50", bin)
	host.Env = env
	if msg, err := host.CombinedOutput(); err != nil {
		t.Fatalf("start the board: %v\n%s", err, msg)
	}
	board := func() string {
		text, _ := exec.Command("tmux", "-S", hostPath, "capture-pane", "-p", "-t", "board").Output()
		return string(text)
	}
	save := func(name, text string) {
		_ = os.WriteFile(filepath.Join(out, name), []byte(text), 0o644)
	}
	t.Cleanup(func() {
		save("board-final.txt", board())
		for _, a := range []hogAgent{cpu, mem, grow, dialog, draft} {
			save(a.id+"-pane.txt", pane(a))
			save(a.id+"-typed.log", a.typed())
		}
	})
	began := time.Now()

	// Nothing is typed while the dialog is up or the draft stands, however
	// long the memory tiers take to walk. Checked continuously while the
	// other cases run, so the holds are proved over the whole stop window.
	holdsUntil := began.Add(35 * time.Second)
	checkHolds := func(t *testing.T) {
		t.Helper()
		if time.Now().Before(holdsUntil) {
			if got := dialog.typed(); got != "" {
				t.Fatalf("typed over a dialog:\n%s", got)
			}
			if got := draft.typed(); got != "" {
				t.Fatalf("typed over the operator's draft:\n%s", got)
			}
		}
	}

	t.Run("cpu walks notice, warn and stop in order", func(t *testing.T) {
		for _, tier := range []string{"CPU notice", "CPU warn", "CPU stop"} {
			waitUntil(t, 90*time.Second, tier+" typed into the pane", func() bool {
				checkHolds(t)
				return strings.Contains(cpu.typed(), tier)
			})
			t.Logf("%s typed after %s", tier, time.Since(began).Round(time.Second))
		}
		typed := cpu.typed()
		assertOrdered(t, typed, "CPU notice", "CPU warn", "CPU stop")
		if n := noticesIn(typed); n != 3 {
			t.Fatalf("want three fenced notices, got %d:\n%s", n, typed)
		}
		for _, want := range []string{"Notice from Gate Inbox", "sh -c while :; do :; done", "Stop the named processes now"} {
			if !strings.Contains(typed, want) {
				t.Fatalf("typed text lacks %q:\n%s", want, typed)
			}
		}
		if !strings.Contains(board(), "cpu hog") {
			t.Fatalf("the board does not badge the row:\n%s", board())
		}
		save("board-cpu-hog.txt", board())
	})

	t.Run("memory held walks notice, warn and stop in order", func(t *testing.T) {
		waitUntil(t, 60*time.Second, "memory stop typed", func() bool {
			checkHolds(t)
			return strings.Contains(mem.typed(), "Memory stop")
		})
		typed := mem.typed()
		assertOrdered(t, typed, "Memory notice", "Memory warn", "Memory stop")
		if !strings.Contains(typed, "python3 -c") || !strings.Contains(typed, "available") {
			t.Fatalf("memory notice does not name the holder or the host:\n%s", typed)
		}
	})

	t.Run("steady growth trips the growth rule under every size rule", func(t *testing.T) {
		waitUntil(t, 120*time.Second, "memory warn typed for growth", func() bool {
			checkHolds(t)
			return strings.Contains(grow.typed(), "Memory warn")
		})
		typed := grow.typed()
		if !strings.Contains(typed, "growing by at least") || strings.Contains(typed, "Memory notice") {
			t.Fatalf("want a growth warn and no size notice:\n%s", typed)
		}
	})

	t.Run("a dialog and a draft hold the notice until they clear", func(t *testing.T) {
		for time.Now().Before(holdsUntil) {
			checkHolds(t)
			time.Sleep(time.Second)
		}
		if n := queuedCounts(t, db)[dialog.id]; n != 1 {
			t.Fatalf("dialog session has %d queued, want the one newest notice", n)
		}
		if n := queuedCounts(t, db)[draft.id]; n != 1 {
			t.Fatalf("draft session has %d queued, want the one newest notice", n)
		}
		save("dialog-held-pane.txt", pane(dialog))
		save("draft-held-pane.txt", pane(draft))
		if err := os.WriteFile(filepath.Join(dialog.dir, "go"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		// Ctrl-U is the terminal's kill-line: the operator clears the draft.
		if err := driver.SendKeys(draft.id, "C-u"); err != nil {
			t.Fatal(err)
		}
		for _, a := range []hogAgent{dialog, draft} {
			waitUntil(t, 30*time.Second, a.id+" notice typed once clear", func() bool {
				return strings.Contains(a.typed(), "Memory stop")
			})
			// Only the newest notice reaches the pane: the ones queued while
			// it was held were superseded, not stacked.
			if n := noticesIn(a.typed()); n != 1 {
				t.Fatalf("%s: want one notice typed, got %d:\n%s", a.id, n, a.typed())
			}
			if strings.Contains(a.typed(), "half typed") {
				t.Fatalf("%s: the operator's draft was submitted with the notice:\n%s", a.id, a.typed())
			}
		}
	})

	t.Run("the badge clears once the load stops", func(t *testing.T) {
		stopLoad(cpu.dir)
		killed := time.Now()
		waitUntil(t, 45*time.Second, "cpu hog badge gone", func() bool {
			return !strings.Contains(board(), "cpu hog")
		})
		t.Logf("cpu badge cleared %s after the loop was killed", time.Since(killed).Round(time.Second))
		save("board-after-kill.txt", board())
	})
}

// buildBoard builds this module's own gate-inbox binary.
func buildBoard(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "gate-inbox")
	build := exec.Command("go", "build", "-buildvcs=false", "-o", bin, ".")
	build.Dir = root
	build.Env = append(tmuxtest.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

// stopLoad kills the load process a stand-in agent started.
func stopLoad(dir string) {
	killPids(filepath.Join(dir, "load.pid"))
}

// stopAgent kills everything a stand-in agent started. Its children outlive
// the pane: killing the tmux session hangs up the shell, not a background
// job, so they are reaped by pid.
func stopAgent(dir string) {
	killPids(filepath.Join(dir, "load.pid"))
	killPids(filepath.Join(dir, "fanout.pids"))
}

func killPids(path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, field := range strings.Fields(string(raw)) {
		if pid, err := strconv.Atoi(field); err == nil && pid > 1 {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}

func waitUntil(t *testing.T, limit time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", limit, what)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func assertOrdered(t *testing.T, text string, parts ...string) {
	t.Helper()
	at := 0
	for _, part := range parts {
		i := strings.Index(text[at:], part)
		if i < 0 {
			t.Fatalf("%q missing or out of order in:\n%s", part, text)
		}
		at += i + len(part)
	}
}

// noticesIn counts the fenced Gate Inbox notices in typed text: each one's
// header names its fence once and the fence then opens and closes it.
func noticesIn(text string) int {
	return strings.Count(text, "----GATE-INBOX-NOTICE-") / 3
}

// TestHogStopReachesClaudeMidTurnE2E proves the notice against a real Claude
// Code session: one turn runs a busy loop as a foreground Bash call, the stop
// tier interrupts that turn, and the notice is typed in as the next input,
// which the agent answers. It spends two short haiku turns on the operator's
// own Claude login, so it is opt-in twice over:
//
//	GATE_INBOX_E2E_HOGS=1 GATE_INBOX_E2E_HOGS_CLAUDE=1 \
//	  go test ./app -run TestHogStopReachesClaudeMidTurnE2E -timeout 10m -v
func TestHogStopReachesClaudeMidTurnE2E(t *testing.T) {
	if os.Getenv("GATE_INBOX_E2E_HOGS") == "" || os.Getenv("GATE_INBOX_E2E_HOGS_CLAUDE") == "" {
		t.Skip("GATE_INBOX_E2E_HOGS and GATE_INBOX_E2E_HOGS_CLAUDE unset")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("the board polls a tmux server")
	}
	out := os.Getenv("GATE_INBOX_E2E_HOGS_OUT")
	if out == "" {
		out = t.TempDir()
	} else if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}

	bin := buildBoard(t)
	agents := tmuxtest.NewSocket("hogclaude")
	boardHost := tmuxtest.NewSocket("hogcboard")
	// Only the stop tier, so the one notice is the interrupting one; haiku
	// and a single allowed command keep the turns short and dialog-free.
	config := "tmux_socket = \"" + agents + "\"\n" +
		"[log]\nlevel = \"info\"\nfile = \"" + filepath.Join(out, "claude-board.log") + "\"\n" + `
[tools.claude]
command = "claude --model haiku --allowedTools 'Bash(timeout:*)'"

[hogs]
sample_every = "2s"
reset_after = "10s"
cooldown = "1h"

[hogs.cpu]
notice = []
warn = []
stop = [{ percent = 20, for = "15s" }]

[hogs.memory]
notice = []
warn = []
stop = []
`
	env := fixtureHome(t, config)
	home := envValue(env, "GATE_INBOX_HOME")
	tmpdir := envValue(env, "TMUX_TMPDIR")
	t.Cleanup(func() {
		killTestServer(t, tmpdir, boardHost)
		killTestServer(t, tmpdir, agents)
	})
	db := filepath.Join(home, "state.db")
	seedSessions(t, db)
	skipWelcome(t, db)

	hostPath := hostSocket(t, tmpdir, boardHost)
	host := exec.Command("tmux", "-S", hostPath, "new-session", "-d", "-s", "board", "-x", "200", "-y", "50", bin)
	host.Env = append(withoutKey(withoutKey(env, "GATE_INBOX_LOG_LEVEL"), "GATE_INBOX_LOG_FILE"),
		"GATE_INBOX_LOG_LEVEL=info", "GATE_INBOX_LOG_FILE="+filepath.Join(out, "claude-board.log"))
	if msg, err := host.CombinedOutput(); err != nil {
		t.Fatalf("start the board: %v\n%s", err, msg)
	}

	work := t.TempDir()
	// Registered before the pane's kill so it runs after it: cleanups run
	// last-registered first, and a live Claude rewrites its transcript.
	transcript := claudeTranscript(t, work)
	prompt := "Run exactly this shell command in the foreground with the Bash tool and wait for it: " +
		"timeout 300 sh -c 'while :; do :; done' -- then reply with the single word done."
	caller := append(withoutKey(env, "GATE_INBOX_SESSION_ID"), "GATE_INBOX_SESSION_ID=ca11e400")
	spawn := exec.Command(bin, "spawn", "--tool", "claude", "--name", "claude-hog", "--directory", work, "--json")
	spawn.Env = caller
	raw, err := spawn.CombinedOutput()
	if err != nil {
		t.Fatalf("spawn: %v\n%s", err, raw)
	}
	id := idOf(t, string(raw))
	driver, err := tmux.NewWithSocket(agents)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = driver.Kill(id) })
	// The whole conversation, wrapped lines joined, so a notice longer than
	// one screen can still be read back.
	agentsPath := hostSocket(t, tmpdir, agents)
	pane := func() string {
		text, _ := exec.Command("tmux", "-S", agentsPath, "capture-pane", "-p", "-J", "-S", "-1000", "-t", driver.TargetName(id)).Output()
		return ansi.Strip(string(text))
	}
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(out, "claude-pane.txt"), []byte(pane()), 0o644)
		t.Logf("queued at the end: %v", queuedCounts(t, db))
		board, _ := exec.Command("tmux", "-S", hostPath, "capture-pane", "-p", "-t", "board").CombinedOutput()
		_ = os.WriteFile(filepath.Join(out, "claude-board.txt"), board, 0o644)
	})

	// A fresh directory asks whether to trust it; the default answer is yes.
	// The turn is typed in once the composer is up, the way the operator
	// would start one.
	waitUntil(t, 90*time.Second, "claude's composer", func() bool {
		p := pane()
		if strings.Contains(p, "trust") && strings.Contains(p, "Enter to confirm") {
			_ = driver.SendKeys(id, "Enter")
			return false
		}
		return strings.Contains(p, "❯") && strings.Contains(p, "Claude Code")
	})
	time.Sleep(2 * time.Second)
	if err := driver.SendText(id, prompt); err != nil {
		t.Fatalf("type the turn: %v", err)
	}
	waitUntil(t, 90*time.Second, "claude running the busy loop", burnerRunning)
	began := time.Now()
	// Claude Code draws a full-screen UI, so the pane holds only the tail of
	// the conversation. What the agent was given, and what it did with it, is
	// read from its own transcript.
	waitUntil(t, 90*time.Second, "the notice in claude's transcript", func() bool {
		return strings.Contains(transcript(), "Notice from Gate Inbox")
	})
	t.Logf("stop notice reached claude %s after the loop started", time.Since(began).Round(time.Second))
	waitUntil(t, 120*time.Second, "claude's reply to the notice", func() bool {
		return claudeAnsweredNotice(transcript())
	})
	_ = os.WriteFile(filepath.Join(out, "claude-pane-notice.txt"), []byte(pane()), 0o644)
	_ = os.WriteFile(filepath.Join(out, "claude-transcript.jsonl"), []byte(transcript()), 0o644)
	// The stop tier interrupted the running turn to be read now, rather than
	// queueing behind a five-minute Bash call.
	text := transcript()
	notice := strings.Index(text, "Notice from Gate Inbox")
	if at := strings.Index(text, "Request interrupted by user"); at < 0 || at > notice {
		t.Fatalf("the running turn was not interrupted before the notice")
	}
	if !strings.Contains(pane(), "----GATE-INBOX-NOTICE-") {
		t.Fatalf("the notice's fence is not on the agent's screen:\n%s", pane())
	}
}

// claudeTranscript returns a reader for the conversation Claude Code keeps
// for a session started in dir, and removes that project's transcripts when
// the test ends: they are the operator's store, not the test's.
func claudeTranscript(t *testing.T, dir string) func() string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, ".claude", "projects")
	if configured := os.Getenv("CLAUDE_CONFIG_DIR"); configured != "" {
		root = filepath.Join(configured, "projects")
	}
	slug := regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(dir, "-")
	project := filepath.Join(root, slug)
	t.Cleanup(func() {
		// The session was killed just before this runs; give it the moment
		// it takes to flush on the way out, or it recreates the directory.
		time.Sleep(3 * time.Second)
		_ = os.RemoveAll(project)
	})
	return func() string {
		files, _ := filepath.Glob(filepath.Join(project, "*.jsonl"))
		var all strings.Builder
		for _, f := range files {
			raw, _ := os.ReadFile(f)
			all.Write(raw)
		}
		return all.String()
	}
}

// claudeAnsweredNotice reports whether the transcript holds an assistant
// entry after the user turn carrying the notice.
func claudeAnsweredNotice(transcript string) bool {
	lines := strings.Split(transcript, "\n")
	seen := false
	for _, line := range lines {
		if strings.Contains(line, "Notice from Gate Inbox") && strings.Contains(line, `"type":"user"`) {
			seen = true
			continue
		}
		if seen && strings.Contains(line, `"type":"assistant"`) {
			return true
		}
	}
	return false
}

// burnerRunning reports whether the Claude test's busy loop is alive.
func burnerRunning() bool {
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err == nil && strings.Contains(string(raw), "timeout\x00300\x00sh\x00-c\x00while :; do :; done") {
			return true
		}
	}
	return false
}

// hostSocket is the path of a test-owned tmux server that hosts the board
// itself. tmux creates a socket but not the directory it sits in, and a
// server that cannot bind still lets new-session -d exit 0.
func hostSocket(t *testing.T, tmpdir, socket string) string {
	t.Helper()
	dir := filepath.Join(tmpdir, "tmux-"+strconv.Itoa(os.Getuid()))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, socket)
}
