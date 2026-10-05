package app

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// adoptedModel keeps every turn this test spends cheap.
const adoptedModel = "claude-haiku-4-5-20251001"

// awaitingFeatures names the checks whose feature has not landed on this
// branch yet. Such a check still runs and reports what it saw, but a miss is
// logged as PENDING rather than failing the test. Delete an entry once its
// feature is merged, so a miss fails from then on.
var awaitingFeatures = map[string]string{}

// toolPrompt names the board's tools as native MCP tools: haiku otherwise
// sometimes reaches for a made-up shell command, which stops the turn on a
// Bash approval and proves nothing about the tools.
const toolPrompt = "The gate-inbox tools named below are native MCP tools, not shell commands; " +
	"if one is deferred, load it with ToolSearch first. "

// steeringMark is a heading of the delegation steering an adopted session's
// first prompt carries.
const steeringMark = "Delegating work: use Gate Inbox sessions"

// hookErrorPattern is what Claude Code draws when a hook or an MCP server
// misbehaves.
var hookErrorPattern = regexp.MustCompile(`(?i)hook error|hook failed|blocked by hook|non-blocking status code|mcp servers? failed|failed to connect`)

// TestAdoptedClaudeE2E proves, against real Claude Code sessions, that a
// claude started outside the board gets what a launched one has once the
// board adopts its pane, and costs nothing while it is not adopted:
//
//	a. a board start registers the global hooks in settings.json and the MCP
//	   relay in .claude.json under CLAUDE_CONFIG_DIR, with no CLI command;
//	b. a plain claude outside the board shows no hook or MCP errors, its hook
//	   preludes print nothing and return at once, `claude mcp list` shows the
//	   relay connected and /mcp lists it;
//	c. the board adopts the pane: hook status reaches the row, the board's MCP
//	   tools appear mid-session and list_sessions works, steering arrives, a
//	   message from a launched session carries the verified-sender note, the
//	   adopted session replies with send_session, and its AskUserQuestion is
//	   captured and answered from the session that placed it;
//	e. a board-launched claude still works and no hook event is logged twice;
//	d. with the board stopped and its binary deleted, the released session and
//	   a fresh claude run with no errors, and the relay still shows connected.
//
// It spends about a dozen short haiku turns on ANTHROPIC_API_KEY, so it is
// opt-in. Everything runs under a scratch HOME and CLAUDE_CONFIG_DIR and on
// test-owned tmux servers:
//
//	GATE_INBOX_E2E_CLAUDE=1 [GATE_INBOX_E2E_CLAUDE_DIR=<scratch dir>] \
//	  [GATE_INBOX_E2E_CLAUDE_OUT=<dir>] go test ./app -run TestAdoptedClaudeE2E -timeout 30m -v
//
// The board skips registration for a home under the temp dir, so every
// process here runs with TMPDIR pointed at a sibling of the scratch home.
// GATE_INBOX_E2E_CLAUDE_OUT keeps the screens, configs and a per-step
// verdict (steps.md).
func TestAdoptedClaudeE2E(t *testing.T) {
	if os.Getenv("GATE_INBOX_E2E_CLAUDE") == "" {
		t.Skip("GATE_INBOX_E2E_CLAUDE unset")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("the board polls a tmux server")
	}
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if len(apiKey) < 20 {
		t.Skip("ANTHROPIC_API_KEY unset")
	}

	e := newAdoptedE2E(t, apiKey)
	e.stepA()
	e.stepB()
	adopted := e.stepC()
	e.stepE(adopted)
	e.stepD(adopted)
}

type adoptedE2E struct {
	t        *testing.T
	bin      string
	root     string
	out      string
	giHome   string
	claudeD  string
	env      []string
	tmpdir   string
	board    tmuxHost
	agents   tmuxHost
	plain    tmuxHost
	workA    string
	workL    string
	workN    string
	launched string
	steps    []string
}

// tmuxHost is one test-owned tmux server, addressed by its full socket path.
type tmuxHost struct {
	name, path string
	env        []string
}

func (h tmuxHost) run(args ...string) (string, error) {
	cmd := exec.Command("tmux", append([]string{"-S", h.path}, args...)...)
	cmd.Env = h.env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// screen is the pane's visible text with wrapped lines joined.
func (h tmuxHost) screen(target string) string {
	out, _ := h.run("capture-pane", "-p", "-J", "-t", target)
	return ansi.Strip(out)
}

// history is the pane's scrollback and screen.
func (h tmuxHost) history(target string) string {
	out, _ := h.run("capture-pane", "-p", "-J", "-S", "-2000", "-t", target)
	return ansi.Strip(out)
}

func (h tmuxHost) typeLine(t *testing.T, target, text string) {
	t.Helper()
	if out, err := h.run("send-keys", "-t", target, "-l", text); err != nil {
		t.Fatalf("type into %s: %v\n%s", target, err, out)
	}
	time.Sleep(700 * time.Millisecond)
	if out, err := h.run("send-keys", "-t", target, "Enter"); err != nil {
		t.Fatalf("enter into %s: %v\n%s", target, err, out)
	}
}

func (h tmuxHost) keys(target string, keys ...string) {
	_, _ = h.run(append([]string{"send-keys", "-t", target}, keys...)...)
}

func newAdoptedE2E(t *testing.T, apiKey string) *adoptedE2E {
	t.Helper()
	root := os.Getenv("GATE_INBOX_E2E_CLAUDE_DIR")
	if root == "" {
		root = t.TempDir()
	} else {
		root = filepath.Join(root, "run-"+time.Now().Format("20060102-150405"))
	}
	root, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	out := os.Getenv("GATE_INBOX_E2E_CLAUDE_OUT")
	if out == "" {
		out = filepath.Join(root, "out")
	}
	e := &adoptedE2E{
		t: t, root: root, out: out,
		giHome:  filepath.Join(root, "gi"),
		claudeD: filepath.Join(root, "claude"),
		workA:   filepath.Join(root, "work", "adopted"),
		workL:   filepath.Join(root, "work", "launched"),
		workN:   filepath.Join(root, "work", "fresh"),
	}
	home := filepath.Join(root, "home")
	tmp := filepath.Join(root, "tmp")
	for _, dir := range []string{out, e.giHome, e.claudeD, e.workA, e.workL, e.workN, home, tmp} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The API key's own Claude Code config lives under the scratch config
	// dir; remove it, and the scratch HOME, once the run is over. This runs
	// after the tmux servers are killed, and a claude losing its pane still
	// writes its transcript on the way out, so it waits for them first.
	t.Cleanup(func() {
		awaitScratchClaudes(home, 15*time.Second)
		_ = os.RemoveAll(filepath.Join(e.claudeD, ".claude.json"))
		_ = os.RemoveAll(filepath.Join(e.claudeD, ".claude.json.backup"))
		_ = os.RemoveAll(filepath.Join(e.claudeD, "backups"))
		_ = os.RemoveAll(home)
		e.writeSteps()
	})

	e.bin = buildBoard(t)

	// The child environment: the test's tmux isolation, a scratch HOME and
	// Claude config, and nothing of the Claude Code or Gate Inbox session
	// this test may itself be running under.
	var env []string
	for _, kv := range tmuxtest.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(key, "CLAUDE") || strings.HasPrefix(key, "GATE_INBOX_") ||
			key == "HOME" || key == "TMPDIR" || key == "XDG_CONFIG_HOME" {
			continue
		}
		env = append(env, kv)
	}
	e.env = append(env, "HOME="+home, "TMPDIR="+tmp, "CLAUDE_CONFIG_DIR="+e.claudeD,
		"GATE_INBOX_HOME="+e.giHome, "DISABLE_AUTOUPDATER=1", "TERM=xterm-256color")
	e.tmpdir = envValue(e.env, "TMUX_TMPDIR")

	// The board launches through the login shell, and a zsh with no startup
	// file at all stops on its new-user wizard instead of running the agent.
	for _, rc := range []string{".zshrc", ".bashrc"} {
		if err := os.WriteFile(filepath.Join(home, rc), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e.seedClaude(apiKey)
	e.seedBoard()

	mk := func(family string) tmuxHost {
		name := tmuxtest.NewSocket(family)
		h := tmuxHost{name: name, path: hostSocket(t, e.tmpdir, name), env: e.env}
		t.Cleanup(func() { killTestServer(t, e.tmpdir, name) })
		return h
	}
	e.board = mk("adoptboard")
	e.agents = mk("adoptagents")
	e.plain = mk("adoptplain")
	if err := os.WriteFile(filepath.Join(e.giHome, "config.toml"), []byte(e.boardConfig()), 0o644); err != nil {
		t.Fatal(err)
	}
	// Stop the board first on the way out, so its own teardown runs.
	t.Cleanup(e.stopBoard)
	// GATE_INBOX_E2E_CLAUDE_HOLD keeps everything up for that long before the
	// teardown, so a failure can be looked at live.
	if hold, err := time.ParseDuration(os.Getenv("GATE_INBOX_E2E_CLAUDE_HOLD")); err == nil && hold > 0 {
		t.Cleanup(func() {
			e.save("hold.txt", fmt.Sprintf("TMUX_TMPDIR=%s\nboard=%s\nagents=%s\nplain=%s\nroot=%s\n",
				e.tmpdir, e.board.path, e.agents.path, e.plain.path, e.root))
			t.Logf("holding for %s; sockets in %s", hold, filepath.Join(e.out, "hold.txt"))
			time.Sleep(hold)
		})
	}
	return e
}

// awaitScratchClaudes waits up to wait for every process running under the
// scratch HOME to exit, then kills the ones still running, so none writes
// into the scratch directories while they are being removed.
func awaitScratchClaudes(home string, wait time.Duration) {
	scratch := func() []int {
		var pids []int
		entries, _ := os.ReadDir("/proc")
		for _, entry := range entries {
			pid, err := strconv.Atoi(entry.Name())
			if err != nil || pid <= 1 || pid == os.Getpid() {
				continue
			}
			raw, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "environ"))
			if err != nil {
				continue
			}
			if slices.Contains(strings.Split(string(raw), "\x00"), "HOME="+home) {
				pids = append(pids, pid)
			}
		}
		return pids
	}
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if len(scratch()) == 0 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	for _, pid := range scratch() {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

func (e *adoptedE2E) boardConfig() string {
	return "poll_interval = \"2s\"\n" +
		"tmux_socket = \"" + e.agents.name + "\"\n" +
		"adopt_sockets = [\"" + e.plain.name + "\"]\n" +
		"[log]\nlevel = \"info\"\nfile = \"" + filepath.Join(e.out, "board.log") + "\"\n" +
		"[tools.claude]\ncommand = \"claude --model " + adoptedModel + "\"\n"
}

// seedClaude writes the scratch Claude Code config: onboarding done, the
// three work directories trusted, the API key approved by its last 20
// characters (how Claude Code itself records the answer), and an allow rule
// for the board's tools, so no dialog stands between a turn and its result.
func (e *adoptedE2E) seedClaude(apiKey string) {
	e.t.Helper()
	projects := map[string]any{}
	for _, dir := range []string{e.workA, e.workL, e.workN} {
		projects[dir] = map[string]any{"hasTrustDialogAccepted": true, "hasCompletedProjectOnboarding": true}
	}
	version := ""
	if out, err := exec.Command("claude", "--version").Output(); err == nil {
		version, _, _ = strings.Cut(strings.TrimSpace(string(out)), " ")
	}
	state := map[string]any{
		"hasCompletedOnboarding": true,
		"lastReleaseNotesSeen":   version,
		"customApiKeyResponses":  map[string]any{"approved": []string{apiKey[len(apiKey)-20:]}, "rejected": []string{}},
		"projects":               projects,
	}
	writeJSON(e.t, filepath.Join(e.claudeD, ".claude.json"), state)
	writeJSON(e.t, filepath.Join(e.claudeD, "settings.json"), map[string]any{
		"permissions": map[string]any{"allow": []string{"mcp__gate-inbox"}},
	})
}

func (e *adoptedE2E) seedBoard() {
	e.t.Helper()
	db := filepath.Join(e.giHome, "state.db")
	skipWelcome(e.t, db)
	skipTmuxHint(e.t, db)
	st, err := store.Open(db)
	if err != nil {
		e.t.Fatal(err)
	}
	defer st.Close()
	if err := st.CreateSession(store.Session{ID: operator, Name: "e2e operator", Tool: "claude",
		Status: "working", CreatedAt: time.Now()}); err != nil {
		e.t.Fatal(err)
	}
	// Keep adopted panes as they are: the takeover would relaunch the pane
	// as a board session, which is not what this test is about.
	if err := st.SetSetting("outside_panes", "adopt"); err != nil {
		e.t.Fatal(err)
	}
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// record logs one check's verdict and keeps it for steps.md. A miss fails the
// test unless the check waits on a feature in awaitingFeatures.
func (e *adoptedE2E) record(step, check string, ok bool, evidence string, feature ...string) {
	e.t.Helper()
	verdict := "PASS"
	if !ok {
		verdict = "FAIL"
		if len(feature) > 0 && awaitingFeatures[feature[0]] != "" {
			verdict = "PENDING (" + awaitingFeatures[feature[0]] + ")"
		}
	}
	evidence = strings.TrimSpace(evidence)
	e.steps = append(e.steps, fmt.Sprintf("### %s. %s: %s\n\n```\n%s\n```\n", step, check, verdict, evidence))
	e.t.Logf("STEP %s %s: %s\n%s", step, check, verdict, evidence)
	if verdict == "FAIL" {
		e.t.Errorf("step %s failed: %s", step, check)
	}
}

func (e *adoptedE2E) writeSteps() {
	body := "# Adopted Claude Code session e2e\n\n" + strings.Join(e.steps, "\n")
	_ = os.WriteFile(filepath.Join(e.out, "steps.md"), []byte(body), 0o644)
}

func (e *adoptedE2E) save(name, body string) {
	_ = os.WriteFile(filepath.Join(e.out, name), []byte(body), 0o644)
}

// gi runs the gate-inbox CLI as session caller, or as the operator when
// caller is empty.
func (e *adoptedE2E) gi(caller string, args ...string) (string, error) {
	cmd := exec.Command(e.bin, args...)
	cmd.Env = e.env
	if caller != "" {
		cmd.Env = append(slices.Clone(e.env), "GATE_INBOX_SESSION_ID="+caller)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (e *adoptedE2E) claudeCmd(dir string, args ...string) (string, error) {
	cmd := exec.Command("claude", args...)
	cmd.Env = e.env
	cmd.Dir = dir
	done := make(chan struct{})
	var out []byte
	var err error
	go func() { out, err = cmd.CombinedOutput(); close(done) }()
	select {
	case <-done:
	case <-time.After(90 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		return string(out), fmt.Errorf("claude %s timed out", strings.Join(args, " "))
	}
	return string(out), err
}

func (e *adoptedE2E) startBoard() {
	e.t.Helper()
	out, err := e.board.run("new-session", "-d", "-s", "board", "-x", "200", "-y", "50", "-c", e.root, e.bin)
	if err != nil {
		e.t.Fatalf("start the board: %v\n%s", err, out)
	}
	waitUntil(e.t, 30*time.Second, "the board's lock", func() bool { return e.boardPID() > 0 })
}

// boardPID is the running board's pid from its singleton lock, or 0.
func (e *adoptedE2E) boardPID() int {
	raw, err := os.ReadFile(filepath.Join(e.giHome, "manager.lock"))
	if err != nil {
		return 0
	}
	first, _, _ := strings.Cut(strings.TrimSpace(string(raw)), "\n")
	pid, err := strconv.Atoi(strings.TrimSpace(first))
	if err != nil || pid <= 0 || syscall.Kill(pid, 0) != nil {
		return 0
	}
	return pid
}

// stopBoard quits the board the way a closed terminal does not: SIGTERM,
// which Bubble Tea turns into a quit that runs the board's own teardown.
func (e *adoptedE2E) stopBoard() {
	pid := e.boardPID()
	if pid == 0 {
		return
	}
	e.save("board-screen-at-stop.txt", e.board.screen("board"))
	_ = syscall.Kill(pid, syscall.SIGTERM)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && syscall.Kill(pid, 0) == nil {
		time.Sleep(200 * time.Millisecond)
	}
	if syscall.Kill(pid, 0) == nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

type e2eRow struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Directory string `json:"directory"`
	Running   bool   `json:"running"`
	ParentID  string `json:"parent_id"`
	SpawnedBy string `json:"spawned_by"`
}

// operator is the seeded row the test's own CLI calls speak as: every
// session command acts as the session it runs in.
const operator = "ca11e400"

// listing is `gate-inbox sessions --json`, kept as evidence.
func (e *adoptedE2E) listing() string {
	out, _ := e.gi(operator, "sessions", "--json")
	return out
}

// row is a session as the board's store holds it.
func (e *adoptedE2E) row(id string) e2eRow {
	s := e.storeRow(id)
	return e2eRow{ID: s.ID, Name: s.Name, Status: s.Status, Directory: s.Cwd,
		Running: s.TmuxPaneID != "" || s.ID != "", ParentID: s.ParentID, SpawnedBy: s.SpawnedBy}
}

func (e *adoptedE2E) storeRow(id string) store.Session {
	st, err := store.Open(filepath.Join(e.giHome, "state.db"))
	if err != nil {
		return store.Session{}
	}
	defer st.Close()
	sess, _ := st.Get(id)
	return sess
}

// transcript is every conversation Claude Code keeps for a session started
// in dir, under the scratch config.
func (e *adoptedE2E) transcript(dir string) string {
	slug := regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(dir, "-")
	files, _ := filepath.Glob(filepath.Join(e.claudeD, "projects", slug, "*.jsonl"))
	var all strings.Builder
	for _, f := range files {
		raw, _ := os.ReadFile(f)
		all.Write(raw)
	}
	return all.String()
}

func transcriptLines(text string, all ...string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		keep := true
		for _, want := range all {
			if !strings.Contains(line, want) {
				keep = false
				break
			}
		}
		if keep && line != "" {
			out = append(out, line)
		}
	}
	return out
}

func assistantSaid(text, token string) bool {
	return len(transcriptLines(text, `"type":"assistant"`, token)) > 0
}

func usedTool(text, name string) bool {
	return len(transcriptLines(text, `"type":"tool_use"`, `"name":"`+name+`"`)) > 0
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// waitComposer waits for a claude in target to draw its input box.
func (e *adoptedE2E) waitComposer(h tmuxHost, target string) time.Duration {
	e.t.Helper()
	began := time.Now()
	deadline := began.Add(90 * time.Second)
	for {
		s := h.screen(target)
		if strings.Contains(s, "trust") && strings.Contains(s, "Enter to confirm") {
			h.keys(target, "Enter")
		} else if strings.Contains(s, "❯") && !strings.Contains(s, "esc to interrupt") {
			return time.Since(began)
		}
		if time.Now().After(deadline) {
			e.save("composer-timeout-"+strings.ReplaceAll(target, ":", "-")+".txt", h.history(target))
			e.t.Fatalf("timed out waiting for claude's composer in %s:\n%s", target, lastLines(s, 20))
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// turn types prompt into target and waits for the assistant to say token.
func (e *adoptedE2E) turn(h tmuxHost, target, dir, prompt, token string, limit time.Duration) bool {
	e.t.Helper()
	h.typeLine(e.t, target, prompt)
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if assistantSaid(e.transcript(dir), token) {
			return true
		}
		time.Sleep(time.Second)
	}
	return false
}

// a. Registration on a plain board start.
func (e *adoptedE2E) stepA() {
	e.startBoard()
	settings := filepath.Join(e.claudeD, "settings.json")
	state := filepath.Join(e.claudeD, ".claude.json")
	var hooksOK, relayOK bool
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) && !(hooksOK && relayOK) {
		raw, _ := os.ReadFile(settings)
		hooksOK = strings.Count(string(raw), ": gate-inbox-global-hook") == 7
		relayOK = relayEntry(state) != ""
		time.Sleep(500 * time.Millisecond)
	}
	raw, _ := os.ReadFile(settings)
	e.save("a-settings.json", string(raw))
	var parsed struct {
		Permissions map[string]any              `json:"permissions"`
		Hooks       map[string][]map[string]any `json:"hooks"`
	}
	_ = json.Unmarshal(raw, &parsed)
	events := make([]string, 0, len(parsed.Hooks))
	for event := range parsed.Hooks {
		events = append(events, event)
	}
	slices.Sort(events)
	e.record("a", "board start registers the 7 global hooks in <CLAUDE_CONFIG_DIR>/settings.json, keeping the operator's permissions",
		hooksOK && parsed.Permissions != nil,
		fmt.Sprintf("events: %v\npermissions kept: %v", events, parsed.Permissions))
	entry := relayEntry(state)
	e.save("a-relay-entry.json", entry)
	e.record("a", "board start registers the gate-inbox MCP relay in <CLAUDE_CONFIG_DIR>/.claude.json",
		relayOK && strings.Contains(entry, "gate-inbox-relay"), clip(entry, 400))
	status, err := e.gi("", "claude-hooks", "status")
	e.save("a-claude-hooks-status.txt", status)
	e.record("a", "`gate-inbox claude-hooks status` reports both registered",
		err == nil && strings.Contains(status, ": registered") && strings.Contains(status, "MCP relay registered"),
		strings.ReplaceAll(status, e.root, "<scratch>"))
	e.stopBoard()

	// The config opt-out takes the entries back out, and turning it back on
	// restores them, each on a plain board start.
	config := filepath.Join(e.giHome, "config.toml")
	if err := os.WriteFile(config, []byte(e.boardConfig()+"[claude_code]\nsetup = false\n"), 0o644); err != nil {
		e.t.Fatal(err)
	}
	e.startBoard()
	gone := false
	waitUntilQuiet(60*time.Second, func() bool {
		raw, _ := os.ReadFile(settings)
		gone = !strings.Contains(string(raw), "gate-inbox-global-hook") && relayEntry(state) == ""
		return gone
	})
	off, _ := e.gi("", "claude-hooks", "status")
	e.stopBoard()
	raw, _ = os.ReadFile(settings)
	e.record("a", "[claude_code] setup = false removes the hooks and the relay on the next board start",
		gone && strings.Contains(string(raw), "mcp__gate-inbox"),
		strings.ReplaceAll(off, e.root, "<scratch>")+"\noperator permissions kept: "+
			strconv.FormatBool(strings.Contains(string(raw), "mcp__gate-inbox")))
	if err := os.WriteFile(config, []byte(e.boardConfig()), 0o644); err != nil {
		e.t.Fatal(err)
	}
	e.startBoard()
	back := false
	waitUntilQuiet(60*time.Second, func() bool {
		raw, _ := os.ReadFile(settings)
		back = strings.Count(string(raw), ": gate-inbox-global-hook") == 7 && relayEntry(state) != ""
		return back
	})
	// b runs with no board at all, so the plain claude is outside it.
	e.stopBoard()
	e.record("a", "removing the opt-out restores both on the next board start", back, "")
}

// relayEntry is the gate-inbox server in a Claude Code user config, as JSON.
func relayEntry(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var state struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if json.Unmarshal(raw, &state) != nil {
		return ""
	}
	return string(state.MCPServers["gate-inbox"])
}

// b. A plain claude outside the board.
func (e *adoptedE2E) stepB() {
	t := e.t
	// Every registered hook prelude, run as a hook in a pane the board does
	// not hold: no output, exit 0, and next to no time.
	raw, _ := os.ReadFile(filepath.Join(e.claudeD, "settings.json"))
	var parsed struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	_ = json.Unmarshal(raw, &parsed)
	var worst time.Duration
	var noisy []string
	runs := 0
	for event, groups := range parsed.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				if !strings.Contains(h.Command, "gate-inbox-global-hook") {
					continue
				}
				for _, extra := range [][]string{nil, {"TMUX=" + e.plain.path + ",1,0", "TMUX_PANE=%0"}} {
					cmd := exec.Command("/bin/sh", "-c", h.Command)
					cmd.Env = append(slices.Clone(e.env), extra...)
					cmd.Stdin = strings.NewReader(`{"hook_event_name":"` + event + `"}`)
					began := time.Now()
					out, err := cmd.CombinedOutput()
					took := time.Since(began)
					worst = max(worst, took)
					runs++
					if err != nil || len(out) > 0 {
						noisy = append(noisy, fmt.Sprintf("%s: %v %q", event, err, out))
					}
				}
			}
		}
	}
	e.record("b", "global hook preludes outside the board print nothing, exit 0, and return at once",
		runs == 14 && len(noisy) == 0 && worst < 250*time.Millisecond,
		fmt.Sprintf("runs: %d, slowest: %s, noisy: %v", runs, worst.Round(time.Millisecond), noisy))

	out, err := e.claudeCmd(e.workA, "mcp", "list")
	e.save("b-claude-mcp-list.txt", out)
	e.record("b", "`claude mcp list` shows gate-inbox connected (no board running)",
		err == nil && regexp.MustCompile(`gate-inbox: .*Connected`).MatchString(out),
		strings.ReplaceAll(strings.TrimSpace(out), e.root, "<scratch>"))

	if out, err := e.plain.run("new-session", "-d", "-s", "plain", "-x", "200", "-y", "50", "-c", e.workA,
		"bash", "--noprofile", "--norc"); err != nil {
		t.Fatalf("plain server: %v\n%s", err, out)
	}
	time.Sleep(time.Second)
	e.plain.typeLine(t, "plain", "claude --model "+adoptedModel)
	startup := e.waitComposer(e.plain, "plain")
	time.Sleep(3 * time.Second)
	screen := e.plain.screen("plain")
	e.save("b-pane-startup.txt", screen)
	e.record("b", "plain claude starts with no hook or MCP error on screen",
		!hookErrorPattern.MatchString(screen),
		fmt.Sprintf("composer up after %s\n%s", startup.Round(100*time.Millisecond), lastLines(screen, 12)))

	// The dialog can miss the first Enter while a startup notice draws, so
	// /mcp is retried until it opens.
	var list string
	for attempt := 0; attempt < 3 && !strings.Contains(list, "Manage MCP servers"); attempt++ {
		e.plain.keys("plain", "Escape")
		time.Sleep(time.Second)
		e.plain.typeLine(t, "plain", "/mcp")
		time.Sleep(4 * time.Second)
		list = e.plain.screen("plain")
	}
	e.plain.keys("plain", "Enter")
	time.Sleep(3 * time.Second)
	detail := e.plain.screen("plain")
	e.save("b-pane-mcp.txt", list+"\n----- detail -----\n"+detail)
	e.plain.keys("plain", "Escape")
	time.Sleep(time.Second)
	e.plain.keys("plain", "Escape")
	time.Sleep(time.Second)
	e.record("b", "/mcp lists gate-inbox connected, with no tools",
		regexp.MustCompile(`gate-inbox\s+no tools · connected`).MatchString(list),
		grepLines(list, "gate-inbox", "Manage")+"\n----- detail -----\n"+grepLines(detail, "Status:", "Capabilities:", "Tools:"))

	ok := e.turn(e.plain, "plain", e.workA, "Reply with exactly the word READY-B and nothing else.", "READY-B", 90*time.Second)
	time.Sleep(2 * time.Second)
	screen = e.plain.screen("plain")
	e.save("b-pane-turn.txt", screen)
	statusFiles, _ := filepath.Glob(filepath.Join(e.giHome, "hooks", "*.status"))
	e.record("b", "a turn outside the board answers, shows no hook error, and writes no board status",
		ok && !hookErrorPattern.MatchString(screen) && len(statusFiles) == 0,
		fmt.Sprintf("answered: %v, status files: %v\n%s", ok, statusFiles, lastLines(screen, 10)))
}

// c. The board adopts the pane.
func (e *adoptedE2E) stepC() string {
	t := e.t
	e.startBoard()
	var adopted store.Session
	waitUntil(t, 120*time.Second, "the board to adopt the plain claude", func() bool {
		st, err := store.Open(filepath.Join(e.giHome, "state.db"))
		if err != nil {
			return false
		}
		defer st.Close()
		rows, _ := st.ListSessions(false)
		for _, r := range rows {
			if r.TmuxSocket == e.plain.name && r.Tool == "claude" {
				adopted = r
				return true
			}
		}
		return false
	})
	id := adopted.ID
	e.save("c-sessions-adopted.json", e.listing())
	// The marker follows the row by a poll.
	var markers []os.DirEntry
	waitUntilQuiet(20*time.Second, func() bool {
		markers, _ = os.ReadDir(filepath.Join(e.giHome, "hooks", "adopted"))
		return len(markers) == 1
	})
	e.record("c", "the board adopts the plain claude's pane and writes its adoption marker",
		id != "" && len(markers) == 1,
		fmt.Sprintf("row %s %q on pane %s, markers: %d", id, adopted.Name, adopted.TmuxPaneID, len(markers)))

	// Every session command sees the live adopted pane as running.
	var listed struct {
		Sessions []e2eRow `json:"sessions"`
	}
	running := false
	if json.Unmarshal([]byte(e.listing()), &listed) == nil {
		for _, r := range listed.Sessions {
			if r.ID == id {
				running = r.Running
			}
		}
	}
	waited, _ := e.gi(operator, "wait", id, "--timeout", "5s", "--json")
	e.save("c-wait.json", waited)
	var wait struct {
		Outcome string `json:"outcome"`
		Session struct {
			Status  string `json:"status"`
			Running bool   `json:"running"`
		} `json:"session"`
	}
	_ = json.Unmarshal([]byte(waited), &wait)
	e.record("c", "list_sessions shows the adopted row running and wait_for_session does not call it dead",
		running && wait.Outcome != "" && wait.Outcome != "died" && wait.Session.Status != "dead",
		fmt.Sprintf("sessions running: %v\nwait outcome: %q, status: %q, running: %v",
			running, wait.Outcome, wait.Session.Status, wait.Session.Running))

	// The board may queue a request for the agent to name itself; let any
	// such turn finish before this test types its own.
	e.settle(id)

	statuses := map[string]bool{}
	stop := make(chan struct{})
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		for {
			select {
			case <-stop:
				return
			case <-time.After(700 * time.Millisecond):
				if s := e.row(id).Status; s != "" {
					statuses[s] = true
				}
			}
		}
	}()
	statusLog := filepath.Join(e.giHome, "hooks", id+".status")
	before := fileSize(statusLog)
	ok := e.turn(e.plain, "plain", e.workA,
		toolPrompt+"Call mcp__gate-inbox__list_sessions, then reply with LIST-OK followed by how many sessions it returned.",
		"LIST-OK", 120*time.Second)
	time.Sleep(5 * time.Second)
	close(stop)
	<-watched
	text := e.transcript(e.workA)
	logTail := readFrom(statusLog, before)
	seen := make([]string, 0, len(statuses))
	for s := range statuses {
		seen = append(seen, s)
	}
	slices.Sort(seen)
	e.record("c", "hook status reaches the adopted row: working during the turn, at rest after it",
		statuses["working"] && (statuses["finished"] || statuses["idle"]) &&
			strings.Contains(logTail, "UserPromptSubmit") && strings.Contains(logTail, "Stop"),
		fmt.Sprintf("board statuses seen: %v\nstatus log during the turn:\n%s", seen, logTail))
	e.record("c", "the board's MCP tools appear mid-session and the model calls list_sessions",
		ok && usedTool(text, "mcp__gate-inbox__list_sessions"),
		fmt.Sprintf("answered: %v\n%s", ok, clip(strings.Join(transcriptLines(text, `"type":"assistant"`, "LIST-OK"), "\n"), 600)))
	e.save("c-pane-list-sessions.txt", e.plain.history("plain"))
	convos, _ := filepath.Glob(filepath.Join(e.claudeD, "projects",
		regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(e.workA, "-"), "*.jsonl"))
	var live string
	if len(convos) == 1 {
		live = strings.TrimSuffix(filepath.Base(convos[0]), ".jsonl")
	}
	var bound string
	waitUntilQuiet(15*time.Second, func() bool {
		bound = e.storeRow(id).AgentSessionID
		return bound != "" && bound == live
	})
	e.record("c", "the adopted row is bound to the pane's conversation id",
		bound != "" && bound == live, fmt.Sprintf("row agent session id: %q\nconversation file: %q", bound, live))
	e.stopAfter("c-tools")

	// The steering rides the first prompt after adoption, once: the
	// transcript keeps each hook's additionalContext as an attachment.
	steered := 0
	for _, line := range transcriptLines(text, steeringMark) {
		if !strings.Contains(line, `"type":"assistant"`) {
			steered++
		}
	}
	ok = e.turn(e.plain, "plain", e.workA, "Answer from what you were told in this conversation, without calling any tool: "+
		"were you given standing instructions about Gate Inbox? If so, reply with STEER-YES followed by the name of the tool "+
		"they tell you to delegate real work with; if not, reply with STEER-NO.", "STEER-", 90*time.Second)
	text = e.transcript(e.workA)
	answer := strings.Join(transcriptLines(text, `"type":"assistant"`, "STEER-"), "\n")
	again := 0
	for _, line := range transcriptLines(text, steeringMark) {
		if !strings.Contains(line, `"type":"assistant"`) {
			again++
		}
	}
	e.record("c", "steering reaches the model on the first prompt after adoption, and only once",
		steered == 1 && again == 1 && ok && strings.Contains(answer, "STEER-YES") && strings.Contains(answer, "create_session"),
		fmt.Sprintf("transcript entries carrying the steering after the first turn: %d, after the next: %d\nmodel: %s",
			steered, again, clip(assistantText(answer), 300)))

	// A launched session to talk to.
	out, err := e.gi(operator, "spawn", "--tool", "claude", "--name", "launched", "--directory", e.workL, "--json")
	if err != nil {
		t.Fatalf("spawn: %v\n%s", err, out)
	}
	e.launched = idOf(t, out)
	e.save("c-spawn.json", out)
	// A spawn nests under its caller, and the tree is one level deep, so the
	// launched session is handed back to the top level: it has to be able to
	// take the adopted row as its own child below.
	released, err := e.gi(operator, "place", e.launched, "--release", "--json")
	e.save("c-release.txt", released)
	if err != nil {
		e.record("c", "the launched session is released to the top level", false, released)
	}
	target := "gi_" + e.launched
	e.waitComposer(e.agents, target)
	e.save("c-pane-launched-start.txt", e.agents.screen(target))
	e.settle(e.launched)

	nonce := strconv.FormatInt(time.Now().Unix()%100000, 10)
	pong := "PONG-" + nonce
	msg := toolPrompt + "E2E check: call mcp__gate-inbox__send_session with session_id " + e.launched +
		" and message \"" + pong + " (test acknowledgement, no reply needed)\", then reply with SENT-" + nonce + "."
	sent, err := e.gi(e.launched, "send", id, msg, "--json")
	e.save("c-send.txt", sent)
	if err != nil {
		e.record("c", "send_session from the launched session reaches the adopted one", false, sent)
	}
	var note []string
	waitUntilQuiet(120*time.Second, func() bool {
		note = transcriptLines(e.transcript(e.workA), "Gate Inbox verified the message")
		return len(note) > 0
	})
	e.record("c", "the message arrives with the verified-sender note from the UserPromptSubmit hook",
		len(note) > 0, clip(strings.Join(note, "\n"), 700))
	var replied bool
	waitUntilQuiet(150*time.Second, func() bool {
		replied = assistantSaid(e.transcript(e.workA), "SENT-"+nonce) &&
			strings.Contains(e.transcript(e.workL), pong)
		return replied
	})
	text = e.transcript(e.workA)
	e.record("c", "the adopted session replies through the send_session tool and the launched one receives it",
		replied && usedTool(text, "mcp__gate-inbox__send_session"),
		fmt.Sprintf("send_session used: %v, launched transcript holds %s: %v",
			usedTool(text, "mcp__gate-inbox__send_session"), pong, strings.Contains(e.transcript(e.workL), pong)))
	e.save("c-pane-send.txt", e.plain.history("plain"))
	e.settle(id)
	e.settle(e.launched)

	// A question, relayed and answered.
	e.plain.typeLine(t, "plain", "Use the AskUserQuestion tool to ask me one question, \"Pick a colour\", "+
		"with exactly two options, Red and Blue. After I answer, reply with COLOUR- followed by my choice.")
	askFile := filepath.Join(e.giHome, "hooks", id+".ask.json")
	var ask []byte
	waitUntilQuiet(120*time.Second, func() bool {
		ask, _ = os.ReadFile(askFile)
		return len(ask) > 0 && e.row(id).Status == "waiting"
	})
	e.save("c-ask.json", string(ask))
	e.save("c-pane-ask.txt", e.plain.screen("plain"))
	e.record("c", "the AskUserQuestion is captured as the row's pending question and the row reads waiting",
		len(ask) > 0 && e.row(id).Status == "waiting",
		fmt.Sprintf("row status: %s\nask: %s", e.row(id).Status, clip(string(ask), 500)))

	placed, placeErr := e.gi(e.launched, "place", id, "--json")
	e.save("c-place.txt", placed)
	answered, answerErr := e.gi(e.launched, "answer", id, "Blue", "--json")
	e.save("c-answer.txt", answered)
	got := false
	if answerErr == nil {
		waitUntilQuiet(90*time.Second, func() bool {
			got = assistantSaid(e.transcript(e.workA), "COLOUR-Blue")
			return got
		})
	}
	e.record("c", "the session that placed the adopted row answers its question with answer_session",
		answerErr == nil && got,
		fmt.Sprintf("place: %v %s\nanswer: %v %s\nCOLOUR-Blue in transcript: %v", placeErr, clip(placed, 300),
			answerErr, clip(answered, 400), got), "answer")
	if !got {
		// Clear the dialog by hand so the rest of the run has a composer.
		e.plain.keys("plain", "Down")
		time.Sleep(500 * time.Millisecond)
		e.plain.keys("plain", "Enter")
		time.Sleep(2 * time.Second)
		if strings.Contains(e.plain.screen("plain"), "Submit") {
			e.plain.keys("plain", "Enter")
		}
		waitUntilQuiet(60*time.Second, func() bool { return assistantSaid(e.transcript(e.workA), "COLOUR-") })
	}
	e.save("c-pane-answer.txt", e.plain.history("plain"))
	e.settle(id)
	return id
}

// e. A board-launched claude still works and no event is logged twice.
func (e *adoptedE2E) stepE(adopted string) {
	e.save("e-pane-launched.txt", e.agents.history("gi_"+e.launched))
	launchedText := e.transcript(e.workL)
	worked := answeredAfter(launchedText, "PONG-")
	log, _ := os.ReadFile(filepath.Join(e.giHome, "hooks", e.launched+".status"))
	e.save("e-launched-status.log", string(log))
	dups := adjacentDuplicates(string(log))
	e.record("e", "the board-launched claude took the message and logged each hook event once",
		worked && len(log) > 0 && len(dups) == 0 &&
			strings.Count(string(log), "UserPromptSubmit") == len(userPrompts(launchedText)),
		fmt.Sprintf("assistant turn after the message: %v\nUserPromptSubmit lines: %d, prompts in transcript: %d\nadjacent duplicates: %v\nlog:\n%s",
			worked, strings.Count(string(log), "UserPromptSubmit"), len(userPrompts(launchedText)), dups, string(log)))
	alog, _ := os.ReadFile(filepath.Join(e.giHome, "hooks", adopted+".status"))
	e.save("e-adopted-status.log", string(alog))
	adups := adjacentDuplicates(string(alog))
	e.record("e", "the adopted claude logged each hook event once",
		len(alog) > 0 && len(adups) == 0,
		fmt.Sprintf("adjacent duplicates: %v\nlog:\n%s", adups, string(alog)))
}

// d. The board stopped and its binary deleted.
func (e *adoptedE2E) stepD(adopted string) {
	t := e.t
	e.stopBoard()
	markers, _ := os.ReadDir(filepath.Join(e.giHome, "hooks", "adopted"))
	installed := filepath.Join(e.giHome, "bin", "gate-inbox")
	rmErr := errors2(os.Remove(installed), os.Remove(e.bin))
	e.record("d", "stopping the board releases the pane, and the binaries are deleted",
		len(markers) == 0 && rmErr == nil, fmt.Sprintf("markers left: %d, delete error: %v", len(markers), rmErr))

	statusLog := filepath.Join(e.giHome, "hooks", adopted+".status")
	before := fileSize(statusLog)
	ok := e.turn(e.plain, "plain", e.workA, "Reply with exactly the word STILL-D and nothing else.", "STILL-D", 90*time.Second)
	time.Sleep(2 * time.Second)
	screen := e.plain.screen("plain")
	e.save("d-pane-released.txt", screen)
	e.record("d", "the released session still answers, with no hook or MCP error and no status written",
		ok && !hookErrorPattern.MatchString(screen) && fileSize(statusLog) == before,
		fmt.Sprintf("answered: %v, status log grew: %v\n%s", ok, fileSize(statusLog) != before, lastLines(screen, 10)))

	out, err := e.claudeCmd(e.workN, "mcp", "list")
	e.save("d-claude-mcp-list.txt", out)
	e.record("d", "with the binary gone, `claude mcp list` still shows gate-inbox connected (sh fallback)",
		err == nil && regexp.MustCompile(`gate-inbox: .*Connected`).MatchString(out),
		strings.ReplaceAll(strings.TrimSpace(out), e.root, "<scratch>"))

	if out, err := e.plain.run("new-window", "-t", "plain", "-n", "fresh", "-c", e.workN, "bash", "--noprofile", "--norc"); err != nil {
		t.Fatalf("fresh window: %v\n%s", err, out)
	}
	time.Sleep(time.Second)
	e.plain.typeLine(t, "plain:fresh", "claude --model "+adoptedModel)
	e.waitComposer(e.plain, "plain:fresh")
	ok = e.turn(e.plain, "plain:fresh", e.workN, "Reply with exactly the word FRESH-D and nothing else.", "FRESH-D", 90*time.Second)
	time.Sleep(2 * time.Second)
	screen = e.plain.screen("plain:fresh")
	e.save("d-pane-fresh.txt", screen)
	e.record("d", "a new claude with the binary gone starts and answers with no hook or MCP error",
		ok && !hookErrorPattern.MatchString(screen), fmt.Sprintf("answered: %v\n%s", ok, lastLines(screen, 10)))
	e.plain.keys("plain:fresh", "C-c")
	e.plain.keys("plain:fresh", "C-c")
	e.plain.keys("plain", "C-c")
	e.plain.keys("plain", "C-c")
	time.Sleep(2 * time.Second)
}

// stopAfter ends the run at a named point when GATE_INBOX_E2E_CLAUDE_STOP
// names it, for a look at the state there (with GATE_INBOX_E2E_CLAUDE_HOLD).
func (e *adoptedE2E) stopAfter(point string) {
	if os.Getenv("GATE_INBOX_E2E_CLAUDE_STOP") == point {
		e.t.Logf("stopping after %s as asked", point)
		e.t.FailNow()
	}
}

// settle waits for a row to be at rest with nothing queued for it.
func (e *adoptedE2E) settle(id string) {
	quiet := 0
	waitUntilQuiet(180*time.Second, func() bool {
		r := e.row(id)
		s := e.storeRow(id)
		if (r.Status == "idle" || r.Status == "finished") && len(s.PendingInputs) == 0 {
			quiet++
		} else {
			quiet = 0
		}
		return quiet >= 4
	})
}

// waitUntilQuiet is waitUntil that gives up without failing; the check that
// follows says what was missing.
func waitUntilQuiet(limit time.Duration, ok func() bool) {
	deadline := time.Now().Add(limit)
	for !ok() && time.Now().Before(deadline) {
		time.Sleep(time.Second)
	}
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func readFrom(path string, offset int64) string {
	raw, err := os.ReadFile(path)
	if err != nil || int64(len(raw)) < offset {
		return ""
	}
	return string(raw[offset:])
}

// adjacentDuplicates finds the same lifecycle event logged twice in a row,
// which is what two hooks firing for one event leave.
func adjacentDuplicates(log string) []string {
	var dups []string
	lines := strings.Split(strings.TrimSpace(log), "\n")
	for i := 1; i < len(lines); i++ {
		event := strings.Fields(lines[i])
		if len(event) != 2 {
			continue
		}
		switch event[1] {
		case "UserPromptSubmit", "Stop", "SessionStart", "StopFailure":
			if lines[i] == lines[i-1] {
				dups = append(dups, fmt.Sprintf("line %d: %s", i+1, lines[i]))
			}
		}
	}
	return dups
}

// userPrompts are the prompts typed into a session, as its transcript keeps
// them: user entries whose content is text rather than a tool result.
func userPrompts(text string) []string {
	var out []string
	for _, line := range transcriptLines(text, `"type":"user"`) {
		var entry struct {
			IsMeta  bool `json:"isMeta"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &entry) != nil || entry.IsMeta {
			continue
		}
		var s string
		if json.Unmarshal(entry.Message.Content, &s) == nil {
			if !strings.HasPrefix(s, "<command-") && !strings.HasPrefix(s, "<local-command") {
				out = append(out, s)
			}
			continue
		}
		var parts []map[string]any
		if json.Unmarshal(entry.Message.Content, &parts) == nil && len(parts) > 0 && parts[0]["type"] == "text" {
			out = append(out, fmt.Sprint(parts[0]["text"]))
		}
	}
	return out
}

// assistantText pulls the text blocks out of transcript entries.
func assistantText(lines string) string {
	var out []string
	for _, line := range strings.Split(lines, "\n") {
		var entry struct {
			Message struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		for _, c := range entry.Message.Content {
			if c.Type == "text" {
				out = append(out, c.Text)
			}
		}
	}
	return strings.Join(out, " ")
}

// grepLines keeps the lines of text that hold any of words.
func grepLines(text string, words ...string) string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		for _, w := range words {
			if strings.Contains(line, w) {
				out = append(out, strings.TrimSpace(line))
				break
			}
		}
	}
	return strings.Join(out, "\n")
}

// answeredAfter reports whether an assistant entry follows the first user
// entry carrying token.
func answeredAfter(text, token string) bool {
	seen := false
	for _, line := range strings.Split(text, "\n") {
		if !seen && strings.Contains(line, `"type":"user"`) && strings.Contains(line, token) {
			seen = true
			continue
		}
		if seen && strings.Contains(line, `"type":"assistant"`) {
			return true
		}
	}
	return false
}

func errors2(a, b error) error {
	if a != nil {
		return a
	}
	return b
}
