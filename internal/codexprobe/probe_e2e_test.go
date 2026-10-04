package codexprobe

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// TestCodexProbeE2E settles, against the installed codex, the four facts a
// user-global Gate Inbox registration for Codex depends on:
//
//  1. where an interactive codex spawns its MCP servers and hooks: under the
//     pane's process tree, or under the shared app-server daemon (which then
//     serves every pane, and whose environment is the first pane's);
//  2. whether a session honours notifications/tools/list_changed, and whether
//     a missing server shows in the TUI while an empty one does not;
//  3. whether a UserPromptSubmit hook's additionalContext reaches the model,
//     what the hook payload carries, and whether new hooks need trusting;
//  4. how the session's thread id can be learnt from outside the pane.
//
// Each fact is asserted, so a codex release that changes one fails here
// rather than quietly breaking a design built on it. Everything runs under a
// scratch HOME and CODEX_HOME on a test-owned tmux server, and the scratch
// app-server daemon is killed at the end. It spends three short model turns,
// so it is opt-in:
//
//	GATE_INBOX_E2E_CODEX=1 GATE_INBOX_E2E_CODEX_AUTH=<auth.json to copy> \
//	  [GATE_INBOX_E2E_CODEX_MODEL=<model>] [GATE_INBOX_E2E_CODEX_OUT=<dir>] \
//	  go test ./internal/codexprobe -run TestCodexProbeE2E -timeout 15m -v
//
// GATE_INBOX_E2E_CODEX_AUTH may be left unset when OPENAI_API_KEY is set; the
// probe then logs the scratch CODEX_HOME in with that key. A copied ChatGPT
// login gets a fresh last_refresh, so the scratch codex does not rotate the
// refresh token the original file still holds.
func TestCodexProbeE2E(t *testing.T) {
	if os.Getenv("GATE_INBOX_E2E_CODEX") == "" {
		t.Skip("GATE_INBOX_E2E_CODEX unset")
	}
	if runtime.GOOS != "linux" {
		t.Skip("reads process ancestry from /proc")
	}
	for _, bin := range []string{"codex", "tmux"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip(bin + " not installed")
		}
	}
	authFile := os.Getenv("GATE_INBOX_E2E_CODEX_AUTH")
	if authFile == "" && os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("neither GATE_INBOX_E2E_CODEX_AUTH nor OPENAI_API_KEY set")
	}

	p := newProbe(t, authFile)
	defer p.writeReport()

	// Pane A: an interactive codex on the shared daemon. It starts first, so
	// it is the one that brings the daemon up.
	paneA := p.window("a", "codex")
	p.waitComposer(paneA)
	threadA := p.turn(paneA, "Reply in one line with the hook codeword from your context, or NONE. Do not run anything.", 1)
	p.checkHooks(paneA, threadA)

	// Fact 2: a session's tool list is fixed at its start.
	if err := os.WriteFile(p.trigger, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	p.waitFor("the probe sends tools/list_changed", 30*time.Second, func() bool {
		return len(p.records("mcp", "list_changed_sent")) > 0
	})
	triggeredAt := float64(time.Now().UnixNano()) / 1e9
	reply := p.turnReply(paneA, "If you have an MCP tool named "+toolName+", reply with the single word HAVE; otherwise reply NOTOOL. Do not call or run anything.", 2)
	relisted := false
	for _, r := range p.records("mcp", "list") {
		if r.T > triggeredAt && r.N > 0 && slices.Contains(p.preTriggerServers(triggeredAt), r.PID) {
			relisted = true
		}
	}
	p.record("2", "list_changed does not re-list a running session's tools", !relisted && strings.Contains(reply, "NOTOOL"),
		fmt.Sprintf("re-listed=%v; model reply %q (expected NOTOOL: codex does not re-list on list_changed)", relisted, reply))
	if relisted || !strings.Contains(reply, "NOTOOL") {
		t.Errorf("codex now honours tools/list_changed (re-listed=%v, reply %q): an inert relay can light its tools up mid-session", relisted, reply)
	}

	// Pane B: a second interactive codex, started after the trigger, so its
	// server lists the tool. Fact 1: where was that server spawned?
	beforeB := len(p.records("mcp", "start"))
	paneB := p.window("b", "codex")
	p.waitComposer(paneB)
	p.waitFor("pane B's MCP server", 30*time.Second, func() bool { return len(p.records("mcp", "start")) > beforeB })
	startB := p.records("mcp", "start")[beforeB:]
	underB, paneEnvB := false, ""
	for _, r := range startB {
		if slices.Contains(r.Chain, p.panePID(paneB)) {
			underB = true
		}
		paneEnvB = r.Pane
	}
	p.record("1", "daemon: pane B's MCP server is not under pane B, and sees pane A's TMUX_PANE", !underB && paneEnvB != paneB,
		fmt.Sprintf("servers=%s pane B pid=%d TMUX_PANE seen=%q (pane B is %s)", chains(startB), p.panePID(paneB), paneEnvB, paneB))
	if underB {
		t.Errorf("pane B's MCP server runs under pane B: codex no longer routes interactive sessions through a shared daemon")
	}
	if paneEnvB == paneB {
		t.Errorf("pane B's MCP server sees its own TMUX_PANE: the daemon no longer pins the first pane's environment")
	}

	// Fact 4 and the per-call identity: the tool call's _meta names the thread.
	threadB := p.turnApproving(paneB, "Call the MCP tool "+toolName+" and reply with only the codeword it returns.", 3)
	calls := p.records("mcp", "recv")
	metaThread := ""
	for _, r := range calls {
		if r.Method == "tools/call" {
			var params struct {
				Meta struct {
					ThreadID string `json:"threadId"`
					Turn     struct {
						ThreadID string `json:"thread_id"`
					} `json:"x-codex-turn-metadata"`
				} `json:"_meta"`
			}
			json.Unmarshal(r.Params, &params)
			metaThread = params.Meta.ThreadID
			if metaThread == "" {
				metaThread = params.Meta.Turn.ThreadID
			}
		}
	}
	hookPaneB := ""
	for _, r := range p.hooks("UserPromptSubmit") {
		if payloadField(r.Payload, "session_id") == threadB {
			hookPaneB = r.Pane
		}
	}
	p.record("1", "daemon: pane B's hooks see pane A's TMUX_PANE too", hookPaneB != paneB,
		fmt.Sprintf("pane B (%s) UserPromptSubmit hook saw TMUX_PANE=%q", paneB, hookPaneB))
	if hookPaneB == paneB {
		t.Errorf("pane B's hook sees its own TMUX_PANE: hooks no longer inherit the daemon's environment")
	}
	p.record("4", "tools/call _meta carries the calling thread id", metaThread != "" && metaThread == threadB,
		fmt.Sprintf("_meta thread=%q, pane B hook session_id=%q", metaThread, threadB))
	if metaThread == "" || metaThread != threadB {
		t.Errorf("tools/call _meta thread %q != pane B's session %q", metaThread, threadB)
	}

	// Fact 4: /status prints the thread id in the pane, without a model turn.
	p.typeLine(paneA, "/status")
	status := p.waitScreen(paneA, "/status Session line", 20*time.Second, func(s string) bool { return strings.Contains(s, threadA) })
	server := regexp.MustCompile(`Server:\s+(.+?)\s*│`).FindStringSubmatch(status)
	p.record("4", "/status shows the thread id in the pane", strings.Contains(status, threadA),
		fmt.Sprintf("Session %s on screen; server line %q", threadA, group(server)))
	p.keys(paneA, "Escape")

	// Fact 1, the escape hatch: --no-daemon keeps the servers in the pane.
	beforeC := len(p.records("mcp", "start"))
	paneC := p.window("c", "codex --no-daemon")
	p.waitComposer(paneC)
	p.waitFor("pane C's MCP server", 30*time.Second, func() bool { return len(p.records("mcp", "start")) > beforeC })
	underC := false
	for _, r := range p.records("mcp", "start")[beforeC:] {
		if slices.Contains(r.Chain, p.panePID(paneC)) && r.Pane == paneC {
			underC = true
		}
	}
	p.record("1", "--no-daemon: MCP server under the pane, with its TMUX_PANE", underC,
		chains(p.records("mcp", "start")[beforeC:])+fmt.Sprintf(" pane C pid=%d (%s)", p.panePID(paneC), paneC))
	if !underC {
		t.Errorf("codex --no-daemon no longer spawns MCP servers in the pane's tree")
	}
}

type probe struct {
	t        *testing.T
	root     string
	out      string
	home     string
	codex    string
	log      string
	trigger  string
	socket   string
	env      []string
	model    string
	windows  map[string]string
	stopSeen int
	steps    []string
}

func newProbe(t *testing.T, authFile string) *probe {
	t.Helper()
	root := t.TempDir()
	out := os.Getenv("GATE_INBOX_E2E_CODEX_OUT")
	if out == "" {
		out = filepath.Join(root, "out")
	}
	model := os.Getenv("GATE_INBOX_E2E_CODEX_MODEL")
	if model == "" {
		model = "gpt-6-luna"
	}
	p := &probe{
		t: t, root: root, out: out, model: model,
		home:    filepath.Join(root, "home"),
		codex:   filepath.Join(root, "home", ".codex"),
		log:     filepath.Join(root, "probe.jsonl"),
		trigger: filepath.Join(root, "trigger"),
		windows: map[string]string{},
	}
	for _, d := range []string{out, p.codex, filepath.Join(root, "work", "a"), filepath.Join(root, "work", "b"), filepath.Join(root, "work", "c")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A zsh with no startup file stops on its new-user wizard.
	for _, rc := range []string{".zshrc", ".bashrc"} {
		if err := os.WriteFile(filepath.Join(p.home, rc), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var env []string
	for _, kv := range tmuxtest.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(key, "CODEX") || strings.HasPrefix(key, "GATE_INBOX_") || key == "HOME" || key == "XDG_CONFIG_HOME" {
			continue
		}
		env = append(env, kv)
	}
	p.env = append(env, "HOME="+p.home, "CODEX_HOME="+p.codex, "TERM=xterm-256color")

	p.seedAuth(authFile)
	if err := os.WriteFile(filepath.Join(p.codex, "config.toml"), []byte(p.config()), 0o600); err != nil {
		t.Fatal(err)
	}
	// The daemon outlives the panes it served; end it, and what it spawned.
	t.Cleanup(p.killScratchProcesses)
	p.socket = tmuxtest.Socket(t, "codexprobe")
	if out, err := p.tmux("new-session", "-d", "-s", "p", "-x", "200", "-y", "50", "-c", filepath.Join(root, "work", "a")); err != nil {
		t.Fatalf("start tmux: %v\n%s", err, out)
	}
	return p
}

func (p *probe) seedAuth(authFile string) {
	t := p.t
	dst := filepath.Join(p.codex, "auth.json")
	if authFile == "" {
		cmd := exec.Command("codex", "login", "--with-api-key")
		cmd.Env = p.env
		cmd.Stdin = strings.NewReader(os.Getenv("OPENAI_API_KEY"))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("codex login --with-api-key: %v\n%s", err, out)
		}
		return
	}
	raw, err := os.ReadFile(authFile)
	if err != nil {
		t.Fatalf("read auth: %v", err)
	}
	var auth map[string]any
	if err := json.Unmarshal(raw, &auth); err != nil {
		t.Fatalf("auth file is not JSON: %v", err)
	}
	if _, ok := auth["last_refresh"]; ok {
		auth["last_refresh"] = time.Now().UTC().Format(time.RFC3339Nano)
	}
	raw, _ = json.Marshal(auth)
	if err := os.WriteFile(dst, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(dst) })
}

func (p *probe) config() string {
	self, err := os.Executable()
	if err != nil {
		p.t.Fatal(err)
	}
	q := strconv.Quote
	envFor := func(role string) string {
		return fmt.Sprintf("{ %s = %s, %s = %s, %s = %s, TMUX_TMPDIR = %s, GATE_INBOX_TEST_TMUX_TMPDIR = %s }",
			roleEnv, q(role), logEnv, q(p.log), triggerEnv, q(p.trigger), q(os.Getenv("TMUX_TMPDIR")), q(os.Getenv("TMUX_TMPDIR")))
	}
	hook := func(event string) string {
		cmd := fmt.Sprintf("%s=hook:%s %s=%s %s", roleEnv, event, logEnv, shellQuote(p.log), shellQuote(self))
		return fmt.Sprintf("[[hooks.%s]]\n[[hooks.%s.hooks]]\ntype = \"command\"\ncommand = %s\ntimeout = 10\n\n", event, event, q(cmd))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "model = %s\nmodel_reasoning_effort = \"low\"\ncheck_for_update_on_startup = false\n\n", q(p.model))
	for _, w := range []string{"a", "b", "c"} {
		fmt.Fprintf(&b, "[projects.%s]\ntrust_level = \"trusted\"\n\n", q(filepath.Join(p.root, "work", w)))
	}
	fmt.Fprintf(&b, "[mcp_servers.probe]\ncommand = %s\nenv_vars = [\"TMUX_PANE\"]\nenv = %s\n\n", q(self), envFor("mcp"))
	fmt.Fprintf(&b, "[mcp_servers.probeempty]\ncommand = %s\nenv = %s\n\n", q(self), envFor("mcp-empty"))
	fmt.Fprintf(&b, "[mcp_servers.probemissing]\ncommand = %s\n\n", q(filepath.Join(p.root, "no-such-server")))
	for _, ev := range []string{"SessionStart", "UserPromptSubmit", "Stop"} {
		b.WriteString(hook(ev))
	}
	return b.String()
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func (p *probe) tmux(args ...string) (string, error) {
	cmd := exec.Command("tmux", append([]string{"-L", p.socket}, args...)...)
	cmd.Env = p.env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// window opens a window in its own work dir running command, and returns its
// pane id.
func (p *probe) window(name, command string) string {
	p.t.Helper()
	target := "p:0"
	if len(p.windows) > 0 {
		out, err := p.tmux("new-window", "-t", "p", "-P", "-F", "#{window_index}", "-c", filepath.Join(p.root, "work", name))
		if err != nil {
			p.t.Fatalf("new window: %v\n%s", err, out)
		}
		target = "p:" + strings.TrimSpace(out)
	}
	out, err := p.tmux("display-message", "-p", "-t", target, "#{pane_id}")
	if err != nil {
		p.t.Fatalf("pane id: %v\n%s", err, out)
	}
	pane := strings.TrimSpace(out)
	p.windows[pane] = name
	time.Sleep(time.Second)
	p.typeLine(pane, command)
	return pane
}

func (p *probe) panePID(pane string) int {
	out, _ := p.tmux("display-message", "-p", "-t", pane, "#{pane_pid}")
	pid, _ := strconv.Atoi(strings.TrimSpace(out))
	return pid
}

func (p *probe) screen(pane string) string {
	out, _ := p.tmux("capture-pane", "-p", "-J", "-t", pane)
	return ansi.Strip(out)
}

func (p *probe) keys(pane string, keys ...string) {
	p.tmux(append([]string{"send-keys", "-t", pane}, keys...)...)
}

func (p *probe) typeLine(pane, text string) {
	p.t.Helper()
	if out, err := p.tmux("send-keys", "-t", pane, "-l", text); err != nil {
		p.t.Fatalf("type into %s: %v\n%s", pane, err, out)
	}
	time.Sleep(700 * time.Millisecond)
	p.keys(pane, "Enter")
}

func (p *probe) waitFor(what string, limit time.Duration, ok func() bool) {
	p.t.Helper()
	deadline := time.Now().Add(limit)
	for !ok() {
		if time.Now().After(deadline) {
			p.t.Fatalf("timed out after %s waiting for %s", limit, what)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func (p *probe) waitScreen(pane, what string, limit time.Duration, ok func(string) bool) string {
	p.t.Helper()
	var s string
	deadline := time.Now().Add(limit)
	for {
		if s = p.screen(pane); ok(s) {
			return s
		}
		if time.Now().After(deadline) {
			p.save("timeout-"+p.windows[pane]+".txt", s)
			p.t.Fatalf("timed out after %s waiting for %s in %s:\n%s", limit, what, pane, s)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// waitComposer gets a fresh codex to its composer, answering the hooks
// review (trust all: fact 3) and an update offer (skip) on the way.
func (p *probe) waitComposer(pane string) {
	p.t.Helper()
	trusted := false
	var settled time.Time
	p.waitScreen(pane, "codex composer", 90*time.Second, func(s string) bool {
		switch {
		case strings.Contains(s, "Hooks need review"):
			if !trusted {
				p.record("3", "new hooks need a one-time trust review", true, firstLine(s, "hooks are new or changed"))
			}
			trusted = true
			p.keys(pane, "Down")
			time.Sleep(300 * time.Millisecond)
			p.keys(pane, "Enter")
			time.Sleep(time.Second)
		case strings.Contains(s, "Update available") && strings.Contains(s, "Skip"):
			p.keys(pane, "Down")
			time.Sleep(300 * time.Millisecond)
			p.keys(pane, "Enter")
			time.Sleep(time.Second)
		}
		// The hooks review can draw over a composer that is already up.
		if !strings.Contains(s, "for shortcuts") {
			settled = time.Time{}
			return false
		}
		if settled.IsZero() {
			settled = time.Now()
		}
		return time.Since(settled) > 5*time.Second
	})
	p.save("boot-"+p.windows[pane]+".txt", p.screen(pane))
}

// turn submits prompt and waits for the turn's Stop hook; it returns the
// session id the hooks reported.
func (p *probe) turn(pane, prompt string, n int) string {
	p.t.Helper()
	p.typeLine(pane, prompt)
	p.waitFor(fmt.Sprintf("Stop hook of turn %d", n), 3*time.Minute, func() bool {
		p.save(fmt.Sprintf("turn%d-%s.txt", n, p.windows[pane]), p.screen(pane))
		return len(p.hooks("Stop")) >= n
	})
	stops := p.hooks("Stop")
	return payloadField(stops[n-1].Payload, "session_id")
}

func (p *probe) turnReply(pane, prompt string, n int) string {
	p.turn(pane, prompt, n)
	return payloadField(p.hooks("Stop")[n-1].Payload, "last_assistant_message")
}

// turnApproving is turn for a prompt whose tool call codex asks to approve.
func (p *probe) turnApproving(pane, prompt string, n int) string {
	p.t.Helper()
	p.typeLine(pane, prompt)
	approved := false
	p.waitFor(fmt.Sprintf("Stop hook of turn %d", n), 3*time.Minute, func() bool {
		if s := p.screen(pane); !approved && strings.Contains(s, "Allow the probe MCP server") {
			p.save("approval.txt", s)
			p.keys(pane, "Enter")
			approved = true
		}
		return len(p.hooks("Stop")) >= n
	})
	return payloadField(p.hooks("Stop")[n-1].Payload, "session_id")
}

// checkHooks records fact 3 and the hook half of facts 1 and 4 from turn 1.
func (p *probe) checkHooks(pane, thread string) {
	t := p.t
	byEvent := map[string]record{}
	for _, r := range p.records("hook", "") {
		if _, ok := byEvent[r.HookName]; !ok {
			byEvent[r.HookName] = r
		}
	}
	for _, ev := range []string{"SessionStart", "UserPromptSubmit", "Stop"} {
		if _, ok := byEvent[ev]; !ok {
			t.Fatalf("no %s hook ran", ev)
		}
	}
	reply := payloadField(byEvent["Stop"].Payload, "last_assistant_message")
	p.record("3", "UserPromptSubmit additionalContext reaches the model", strings.Contains(reply, hookCodeword),
		fmt.Sprintf("reply %q", reply))
	if !strings.Contains(reply, hookCodeword) {
		t.Errorf("UserPromptSubmit additionalContext did not reach the model: reply %q", reply)
	}
	var keys []string
	var fields map[string]any
	json.Unmarshal(byEvent["UserPromptSubmit"].Payload, &fields)
	for k := range fields {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	transcript := payloadField(byEvent["SessionStart"].Payload, "transcript_path")
	_, statErr := os.Stat(transcript)
	inSessions := strings.HasPrefix(transcript, filepath.Join(p.codex, "sessions")+string(filepath.Separator)) && strings.Contains(transcript, thread)
	p.record("4", "hook payload names the thread and its rollout under CODEX_HOME/sessions", thread != "" && inSessions && statErr == nil,
		fmt.Sprintf("session_id=%s; UserPromptSubmit keys %v; transcript under sessions/ and named for it: %v", thread, keys, inSessions))
	if thread == "" || !inSessions {
		t.Errorf("hook payload thread %q / transcript %q", thread, filepath.Base(transcript))
	}
	start := byEvent["SessionStart"]
	underPane := slices.Contains(start.Chain, p.panePID(pane))
	p.record("1", "hooks run under the pane's codex (first pane: the daemon is its child)", underPane,
		fmt.Sprintf("chain %v, pane pid %d, TMUX_PANE %q", start.Chain, p.panePID(pane), start.Pane))
	s := p.screen(pane)
	p.save("turn1-"+p.windows[pane]+".txt", s)
	silent := !strings.Contains(s, "SessionStart") && !strings.Contains(strings.ToLower(s), "stop hook")
	p.record("3", "a hook that prints nothing and exits 0 leaves no trace in the TUI", silent, "screen after turn 1 saved as turn1-a.txt")
	warn := regexp.MustCompile(`(\d+) warnings?`).FindStringSubmatch(s)
	if warn == nil {
		warn = []string{"", ""}
	}
	p.record("2", "a missing server warns in the TUI, an empty one does not", warn[1] == "1",
		fmt.Sprintf("footer %q (probemissing missing, probeempty lists 0 tools)", warn[0]))
}

func (p *probe) records(kind, ev string) []record {
	f, err := os.Open(p.log)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var r record
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		if kind == "hook" && !strings.HasPrefix(r.Role, "hook:") || kind == "mcp" && r.Role != "mcp" || ev != "" && r.Ev != ev {
			continue
		}
		out = append(out, r)
	}
	return out
}

func (p *probe) hooks(event string) []record {
	var out []record
	for _, r := range p.records("hook", "") {
		if r.HookName == event {
			out = append(out, r)
		}
	}
	return out
}

// preTriggerServers are the MCP servers that started before at.
func (p *probe) preTriggerServers(at float64) []int {
	var pids []int
	for _, r := range p.records("mcp", "start") {
		if r.T < at {
			pids = append(pids, r.PID)
		}
	}
	return pids
}

// killScratchProcesses ends every process running out of, or pointed at, the
// scratch CODEX_HOME: the app-server daemon, its updater and their children.
func (p *probe) killScratchProcesses() {
	entries, _ := os.ReadDir("/proc")
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		cmdline, _ := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		environ, _ := os.ReadFile(filepath.Join("/proc", e.Name(), "environ"))
		if strings.Contains(string(cmdline), p.codex) || slices.Contains(strings.Split(string(environ), "\x00"), "CODEX_HOME="+p.codex) {
			pids = append(pids, pid)
		}
	}
	for _, pid := range pids {
		syscall.Kill(pid, syscall.SIGTERM)
	}
	time.Sleep(2 * time.Second)
	for _, pid := range pids {
		syscall.Kill(pid, syscall.SIGKILL)
	}
	p.t.Logf("ended %d scratch codex processes", len(pids))
}

func (p *probe) record(fact, check string, ok bool, evidence string) {
	verdict := "PASS"
	if !ok {
		verdict = "FAIL"
	}
	line := fmt.Sprintf("| %s | %s | %s | %s |", fact, check, verdict, strings.ReplaceAll(evidence, "|", "/"))
	p.steps = append(p.steps, line)
	p.t.Log(line)
}

func (p *probe) writeReport() {
	body := "| fact | check | verdict | evidence |\n|---|---|---|---|\n" + strings.Join(p.steps, "\n") + "\n"
	p.save("probe.md", body)
	if raw, err := os.ReadFile(p.log); err == nil {
		p.save("probe.jsonl", string(raw))
	}
}

func (p *probe) save(name, body string) {
	_ = os.WriteFile(filepath.Join(p.out, name), []byte(body), 0o644)
}

func payloadField(raw json.RawMessage, key string) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

func chains(rs []record) string {
	var parts []string
	for _, r := range rs {
		parts = append(parts, fmt.Sprintf("%d<-%v pane=%q", r.PID, r.Chain[min(1, len(r.Chain)):], r.Pane))
	}
	return strings.Join(parts, "; ")
}

// group is a FindStringSubmatch's first group, or "" when nothing matched.
func group(m []string) string {
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

func firstLine(s, sub string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, sub) {
			return strings.TrimSpace(line)
		}
	}
	return ""
}
