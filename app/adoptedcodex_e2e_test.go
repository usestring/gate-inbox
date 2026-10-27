package app

import (
	"encoding/json"
	"fmt"
	"io/fs"
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

	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// adoptedCodexSteering is a line of the steering an adopted codex hears.
const adoptedCodexSteering = "Gate Inbox adopted this session"

// codexHookNoise is what codex draws when a hook misbehaves or warns.
var codexHookNoise = regexp.MustCompile(`(?i)hook (?:failed|error)|failed to run hook|\d+ warnings?`)

// TestAdoptedCodexE2E proves, against a real codex, that the global codex
// hooks give a codex started outside the board what the board needs once it
// adopts the pane, and nothing before:
//
//	a. a board start registers the two hook entries in the scratch
//	   CODEX_HOME's config.toml, keeping the operator's bytes, with no
//	   command;
//	b. a plain codex with no board running shows codex's one-time "Hooks
//	   need review" and, once trusted, nothing at all: no hook output, no
//	   warning, no board file written, even across a model turn;
//	c. the board's next start leaves config.toml byte for byte, so the
//	   trust holds and a second codex opens with no review;
//	d. the board adopts the pane and binds the codex thread to the row from
//	   a prompt, with nothing typed by the test into the pane but prompts;
//	   the row's status follows a turn through the hooks; the steering
//	   arrives exactly once; and the model's own `gate-inbox sessions` and
//	   `gate-inbox send`, run from its shell, speak as the adopted row.
//
// It spends three or four short model turns, so it is opt-in, with the
// probe's switches:
//
//	GATE_INBOX_E2E_CODEX=1 GATE_INBOX_E2E_CODEX_AUTH=<auth.json to copy> \
//	  [GATE_INBOX_E2E_CODEX_MODEL=<model>] [GATE_INBOX_E2E_CODEX_DIR=<scratch dir>] \
//	  [GATE_INBOX_E2E_CODEX_OUT=<dir>] go test ./app -run TestAdoptedCodexE2E -timeout 20m -v
//
// The copied login gets a fresh last_refresh so the scratch codex never
// rotates the original's refresh token. The scratch codex runs with
// sandbox_mode = "danger-full-access" and approval_policy = "never": the
// board's state lives outside the work directory, and an approval dialog
// per gate-inbox command would test codex's sandbox rather than the board.
func TestAdoptedCodexE2E(t *testing.T) {
	if os.Getenv("GATE_INBOX_E2E_CODEX") == "" {
		t.Skip("GATE_INBOX_E2E_CODEX unset")
	}
	for _, bin := range []string{"codex", "tmux"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip(bin + " not installed")
		}
	}
	auth := os.Getenv("GATE_INBOX_E2E_CODEX_AUTH")
	if auth == "" {
		t.Skip("GATE_INBOX_E2E_CODEX_AUTH unset")
	}
	e := newCodexE2E(t, auth)
	e.stepA()
	e.stepB()
	e.stepC()
	e.stepD()
}

type codexE2E struct {
	*adoptedE2E
	codexHome string
	model     string
	fresh     tmuxHost
}

func newCodexE2E(t *testing.T, authFile string) *codexE2E {
	t.Helper()
	root := os.Getenv("GATE_INBOX_E2E_CODEX_DIR")
	if root == "" {
		root = t.TempDir()
	} else {
		root = filepath.Join(root, "run-"+time.Now().Format("20060102-150405"))
	}
	root, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	out := os.Getenv("GATE_INBOX_E2E_CODEX_OUT")
	if out == "" {
		out = filepath.Join(root, "out")
	}
	model := os.Getenv("GATE_INBOX_E2E_CODEX_MODEL")
	if model == "" {
		model = "gpt-6-luna"
	}
	e := &codexE2E{
		adoptedE2E: &adoptedE2E{
			t: t, root: root, out: out,
			giHome: filepath.Join(root, "gi"),
			workA:  filepath.Join(root, "work", "adopted"),
			workN:  filepath.Join(root, "work", "fresh"),
		},
		codexHome: filepath.Join(root, "codex"),
		model:     model,
	}
	home := filepath.Join(root, "home")
	tmp := filepath.Join(root, "tmp")
	for _, dir := range []string{out, e.giHome, e.codexHome, e.workA, e.workN, home, tmp} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		e.killScratchCodex()
		_ = os.Remove(filepath.Join(e.codexHome, "auth.json"))
		e.writeSteps()
	})
	e.bin = buildBoard(t)

	var env []string
	for _, kv := range tmuxtest.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(key, "CODEX") || strings.HasPrefix(key, "GATE_INBOX_") ||
			key == "HOME" || key == "TMPDIR" || key == "XDG_CONFIG_HOME" {
			continue
		}
		env = append(env, kv)
	}
	// The board installs its binary under its home; on PATH there, a bare
	// `gate-inbox` in codex's shell is the installed one.
	for i, kv := range env {
		if path, ok := strings.CutPrefix(kv, "PATH="); ok {
			env[i] = "PATH=" + filepath.Join(e.giHome, "bin") + string(os.PathListSeparator) + path
		}
	}
	e.env = append(env, "HOME="+home, "TMPDIR="+tmp, "CODEX_HOME="+e.codexHome,
		"GATE_INBOX_HOME="+e.giHome, "TERM=xterm-256color")
	e.tmpdir = envValue(e.env, "TMUX_TMPDIR")
	for _, rc := range []string{".zshrc", ".bashrc"} {
		if err := os.WriteFile(filepath.Join(home, rc), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e.seedCodex(authFile)
	e.seedBoard()
	mk := func(family string) tmuxHost {
		name := tmuxtest.NewSocket(family)
		h := tmuxHost{name: name, path: hostSocket(t, e.tmpdir, name), env: e.env}
		t.Cleanup(func() { killTestServer(t, e.tmpdir, name) })
		return h
	}
	e.board = mk("cxboard")
	e.agents = mk("cxagents")
	e.plain = mk("cxplain")
	e.fresh = mk("cxfresh")
	if err := os.WriteFile(filepath.Join(e.giHome, "config.toml"), []byte(e.codexBoardConfig()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.stopBoard)
	return e
}

func (e *codexE2E) codexBoardConfig() string {
	return "poll_interval = \"2s\"\n" +
		"tmux_socket = \"" + e.agents.name + "\"\n" +
		"adopt_sockets = [\"" + e.plain.name + "\", \"" + e.fresh.name + "\"]\n" +
		"[log]\nlevel = \"info\"\nfile = \"" + filepath.Join(e.out, "board.log") + "\"\n" +
		"[claude_code]\nsetup = false\n"
}

// userCodexConfig is the operator's own config.toml, comments and all.
func (e *codexE2E) userCodexConfig() string {
	q := strconv.Quote
	return "# the operator's own codex config\n" +
		"model = " + q(e.model) + "   # cheap\n" +
		"model_reasoning_effort = \"low\"\n" +
		"check_for_update_on_startup = false\n" +
		"sandbox_mode = \"danger-full-access\"\n" +
		"approval_policy = \"never\"\n\n" +
		"[projects." + q(e.workA) + "]\ntrust_level = \"trusted\"\n\n" +
		"[projects." + q(e.workN) + "]\ntrust_level = \"trusted\"\n\n" +
		"[tui]\n# keep this comment\n"
}

func (e *codexE2E) codexConfig() string { return filepath.Join(e.codexHome, "config.toml") }

func (e *codexE2E) seedCodex(authFile string) {
	t := e.t
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
	if err := os.WriteFile(filepath.Join(e.codexHome, "auth.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.codexConfig(), []byte(e.userCodexConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
}

// killScratchCodex ends the scratch app-server daemon and everything else
// running out of, or pointed at, the scratch CODEX_HOME.
func (e *codexE2E) killScratchCodex() {
	entries, _ := os.ReadDir("/proc")
	var pids []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		cmdline, _ := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		environ, _ := os.ReadFile(filepath.Join("/proc", entry.Name(), "environ"))
		if strings.Contains(string(cmdline), e.codexHome) || slices.Contains(strings.Split(string(environ), "\x00"), "CODEX_HOME="+e.codexHome) {
			pids = append(pids, pid)
		}
	}
	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	time.Sleep(2 * time.Second)
	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	e.t.Logf("ended %d scratch codex processes", len(pids))
}

func (e *codexE2E) installedBin() string { return filepath.Join(e.giHome, "bin", "gate-inbox") }

func (e *codexE2E) registered() bool {
	ok, _ := hooks.CodexRegistered(e.codexConfig(), e.giHome, e.installedBin())
	return ok
}

// startCodex opens codex in a fresh window of h, working in dir, and gets
// it to its composer. It answers codex's hook review with "Trust all" and
// reports whether the review was shown.
func (e *codexE2E) startCodex(h tmuxHost, session, dir string) (reviewed bool, screen string) {
	t := e.t
	if out, err := h.run("new-session", "-d", "-s", session, "-x", "200", "-y", "50", "-c", dir, "bash", "--noprofile", "--norc"); err != nil {
		t.Fatalf("start %s: %v\n%s", session, err, out)
	}
	time.Sleep(time.Second)
	h.typeLine(t, session, "codex")
	var settled time.Time
	deadline := time.Now().Add(120 * time.Second)
	for {
		s := h.screen(session)
		switch {
		case strings.Contains(s, "Hooks need review"):
			if !reviewed {
				e.save("review-"+session+".txt", s)
			}
			reviewed = true
			h.keys(session, "Down")
			time.Sleep(300 * time.Millisecond)
			h.keys(session, "Enter")
			time.Sleep(time.Second)
			settled = time.Time{}
			continue
		case strings.Contains(s, "Update available") && strings.Contains(s, "Skip"):
			h.keys(session, "Down")
			time.Sleep(300 * time.Millisecond)
			h.keys(session, "Enter")
			time.Sleep(time.Second)
			continue
		}
		if strings.Contains(s, "for shortcuts") {
			if settled.IsZero() {
				settled = time.Now()
			}
			if time.Since(settled) > 5*time.Second {
				return reviewed, s
			}
		} else {
			settled = time.Time{}
		}
		if time.Now().After(deadline) {
			e.save("composer-timeout-"+session+".txt", s)
			t.Fatalf("timed out waiting for codex's composer in %s:\n%s", session, lastLines(s, 20))
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// rollouts is every rollout codex wrote under the scratch home, joined.
func (e *codexE2E) rollouts() string {
	var all strings.Builder
	filepath.WalkDir(filepath.Join(e.codexHome, "sessions"), func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".jsonl") {
			raw, _ := os.ReadFile(path)
			all.Write(raw)
		}
		return nil
	})
	return all.String()
}

// threads are the ids of the rollouts under the scratch home, from their
// names.
func (e *codexE2E) threads() []string {
	var ids []string
	pattern := regexp.MustCompile(`rollout-.*-([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\.jsonl$`)
	filepath.WalkDir(filepath.Join(e.codexHome, "sessions"), func(path string, d os.DirEntry, err error) error {
		if m := pattern.FindStringSubmatch(path); err == nil && m != nil {
			ids = append(ids, m[1])
		}
		return nil
	})
	return ids
}

// assistantReplied reports whether an assistant message in the rollouts
// carries token.
func (e *codexE2E) assistantReplied(token string) bool {
	for _, line := range strings.Split(e.rollouts(), "\n") {
		if strings.Contains(line, token) && (strings.Contains(line, `"agent_message"`) || strings.Contains(line, `"role":"assistant"`)) {
			return true
		}
	}
	return false
}

func (e *codexE2E) codexTurn(h tmuxHost, session, prompt, token string) bool {
	h.typeLine(e.t, session, prompt)
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		if e.assistantReplied(token) {
			return true
		}
		time.Sleep(time.Second)
	}
	return false
}

// a. Registration on a plain board start.
func (e *codexE2E) stepA() {
	e.startBoard()
	waitUntilQuiet(60*time.Second, e.registered)
	raw, _ := os.ReadFile(e.codexConfig())
	e.save("a-config.toml", string(raw))
	status, err := e.gi("", "codex-hooks", "status")
	e.record("a", "board start registers the two codex hooks in <CODEX_HOME>/config.toml, keeping the operator's bytes",
		e.registered() && strings.HasPrefix(string(raw), e.userCodexConfig()) && err == nil && strings.Contains(status, "codex hooks registered"),
		fmt.Sprintf("entries: %d\n%s", strings.Count(string(raw), ": gate-inbox-codex-hook"), strings.ReplaceAll(status, e.root, "<scratch>")))
	// b runs with no board at all.
	e.stopBoard()
}

// b. A plain codex outside the board.
func (e *codexE2E) stepB() {
	var commands []string
	raw, _ := os.ReadFile(e.codexConfig())
	for _, line := range strings.Split(string(raw), "\n") {
		if value, ok := strings.CutPrefix(line, "command = "); ok && strings.Contains(value, "gate-inbox-codex-hook") {
			if unquoted, err := strconv.Unquote(value); err == nil {
				commands = append(commands, unquoted)
			}
		}
	}
	var noisy []string
	var worst time.Duration
	for _, command := range commands {
		cmd := exec.Command("/bin/sh", "-c", command)
		cmd.Env = e.env
		cmd.Stdin = strings.NewReader(`{"session_id":"0190a000-0000-7000-8000-00000000000a","prompt":"hi"}`)
		began := time.Now()
		out, err := cmd.CombinedOutput()
		worst = max(worst, time.Since(began))
		if err != nil || len(out) > 0 {
			noisy = append(noisy, fmt.Sprintf("%v %q", err, out))
		}
	}
	e.record("b", "codex hook preludes with no board print nothing, exit 0, and return at once",
		len(commands) == 2 && len(noisy) == 0 && worst < 250*time.Millisecond,
		fmt.Sprintf("runs: %d, slowest: %s, noisy: %v", len(commands), worst.Round(time.Millisecond), noisy))

	reviewed, boot := e.startCodex(e.plain, "plain", e.workA)
	e.record("b", "a plain codex asks once to trust the new hooks (the documented one-time review), then starts clean",
		reviewed && !codexHookNoise.MatchString(boot), lastLines(boot, 8))
	ok := e.codexTurn(e.plain, "plain", "Reply with the word READY and the letter B joined by a hyphen, and nothing else. Do not run anything.", "READY-B")
	time.Sleep(2 * time.Second)
	screen := e.plain.screen("plain")
	e.save("b-turn.txt", screen)
	// The board's start in a made the arrivals directory a claude announces
	// itself into; a file anywhere under hooks is what would count.
	var boardFiles []string
	_ = filepath.WalkDir(filepath.Join(e.giHome, "hooks"), func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			boardFiles = append(boardFiles, path)
		}
		return nil
	})
	e.record("b", "a turn outside the board answers, shows no hook output or warning, and writes nothing for the board",
		ok && !codexHookNoise.MatchString(screen) && !strings.Contains(screen, adoptedCodexSteering) && len(boardFiles) == 0,
		fmt.Sprintf("answered: %v, board hook files: %v\n%s", ok, boardFiles, lastLines(screen, 10)))
}

// c. The trust holds across a board start.
func (e *codexE2E) stepC() {
	before, _ := os.ReadFile(e.codexConfig())
	e.save("c-config-trusted.toml", string(before))
	e.startBoard()
	// The board's setup pass runs before it draws; give it that long and a
	// margin, since a pass that changed nothing leaves nothing to wait on.
	time.Sleep(10 * time.Second)
	after, _ := os.ReadFile(e.codexConfig())
	e.record("c", "codex recorded its trust beside the entries, and a board start leaves config.toml byte for byte",
		strings.Count(string(before), "trusted_hash") >= 2 && string(before) == string(after),
		grepLines(string(after), "hooks.state", "trusted_hash"))
	reviewed, _ := e.startCodex(e.fresh, "fresh", e.workN)
	e.record("c", "a second codex, after the board restarted, opens with no hook review", !reviewed, "")
}

// d. Adoption, binding, status, steering and the CLI as the row.
func (e *codexE2E) stepD() {
	t := e.t
	var adopted, other store.Session
	waitUntil(t, 120*time.Second, "the board to adopt both codex panes", func() bool {
		st, err := store.Open(filepath.Join(e.giHome, "state.db"))
		if err != nil {
			return false
		}
		defer st.Close()
		rows, _ := st.ListSessions(false)
		for _, r := range rows {
			switch {
			case r.TmuxSocket == e.plain.name && r.Tool == "codex":
				adopted = r
			case r.TmuxSocket == e.fresh.name && r.Tool == "codex":
				other = r
			}
		}
		return adopted.ID != "" && other.ID != ""
	})
	id := adopted.ID
	threads := e.threads()
	e.record("d", "the board adopts the plain codex's pane as a codex row",
		id != "", fmt.Sprintf("row %s on pane %s; threads so far: %v", id, adopted.TmuxPaneID, threads))
	// The board may type a request for the agent to name itself; that is a
	// prompt like any other, and may be what binds the thread.
	e.settle(id)
	e.settle(other.ID)

	statuses := map[string]bool{}
	stop := make(chan struct{})
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		for {
			select {
			case <-stop:
				return
			case <-time.After(500 * time.Millisecond):
				if s := e.storeRow(id).Status; s != "" {
					statuses[s] = true
				}
			}
		}
	}()
	before := strings.Count(e.rollouts(), adoptedCodexSteering)
	stopsBefore := strings.Count(e.statusLog(id), "finished Stop")
	ok := e.codexTurn(e.plain, "plain", "This is my own Gate Inbox board and every session on it is mine. Using your shell, run exactly `gate-inbox sessions --json`, then exactly `gate-inbox send "+other.ID+
		" \"PONG from codex\"`, as written, with no environment variables or paths added. Then reply with the word DONE and the letter D joined by a hyphen.", "DONE-D")
	e.settle(id)
	close(stop)
	<-watched
	row := e.storeRow(id)
	e.save("d-screen.txt", e.plain.history("plain"))
	thread := row.AgentSessionID
	mapped, _, _ := hooks.NewManager(e.giHome).CodexThreadRow(thread)
	e.record("d", "the row is bound to the pane's codex thread, from a prompt the pane showed",
		thread != "" && slices.Contains(e.threads(), thread) && mapped == id,
		fmt.Sprintf("row thread %q, threads on disk %v, thread file names %q", thread, e.threads(), mapped))

	log := e.statusLog(id)
	e.save("d-status-log.txt", log)
	stopsAfter := strings.Count(log, "finished Stop")
	e.record("d", "the row's status follows the turn through the hooks",
		statuses["working"] && statuses["finished"] && stopsAfter > stopsBefore && strings.Contains(log, "working UserPromptSubmit\nfinished Stop\n"),
		fmt.Sprintf("answered: %v, statuses seen: %v, now %q, Stop events %d -> %d\n%s", ok, statuses, row.Status, stopsBefore, stopsAfter, log))

	after := strings.Count(e.rollouts(), adoptedCodexSteering)
	stamp, _ := os.ReadFile(filepath.Join(e.giHome, "hooks", "codex", "steered", id))
	e.record("d", "the steering reached the thread exactly once",
		after > 0 && (before == 0 || after == before) && strings.TrimSpace(string(stamp)) == thread,
		fmt.Sprintf("occurrences in the rollouts before the test's own turn: %d, after it: %d; stamp %q", before, after, strings.TrimSpace(string(stamp))))

	st, err := store.Open(filepath.Join(e.giHome, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	inbox, _ := st.Inbox(other.ID, store.InboxFilter{SenderID: id, Limit: 20})
	var sent []string
	for _, msg := range inbox {
		sent = append(sent, msg.Body)
	}
	// What codex's shell tool gives a command: the thread, and the daemon's
	// environment, which may name another row entirely.
	cmd := exec.Command(e.bin, "sessions", "--json")
	cmd.Env = append(slices.Clone(e.env), "CODEX_THREAD_ID="+thread, "GATE_INBOX_SESSION_ID="+operator)
	listing, _ := cmd.Output()
	var listed struct {
		Sessions []struct {
			ID   string `json:"id"`
			Self bool   `json:"self"`
		} `json:"sessions"`
	}
	_ = json.Unmarshal(listing, &listed)
	self := ""
	for _, r := range listed.Sessions {
		if r.Self {
			self = r.ID
		}
	}
	// The model ran both bare: no session id of its own on the line.
	bare := true
	ranSessions := false
	for _, line := range strings.Split(e.rollouts(), "\n") {
		if !strings.Contains(line, `"function_call"`) && !strings.Contains(line, `exec_command`) {
			continue
		}
		if strings.Contains(line, "gate-inbox sessions") {
			ranSessions = true
		}
		if (strings.Contains(line, "gate-inbox sessions") || strings.Contains(line, "gate-inbox send")) && strings.Contains(line, "GATE_INBOX_SESSION_ID") {
			bare = false
		}
	}
	e.record("d", "the model's own gate-inbox sessions and send, run from its shell, speak as the adopted row",
		self == id && ranSessions && bare && strings.Contains(strings.Join(sent, "\n"), "PONG from codex"),
		fmt.Sprintf("sessions under CODEX_THREAD_ID marks self=%q; the model ran sessions: %v, with no session id on its command lines: %v; messages to %s from %s: %q",
			self, ranSessions, bare, other.ID, id, sent))
}

func (e *codexE2E) statusLog(id string) string {
	raw, _ := os.ReadFile(filepath.Join(e.giHome, "hooks", id+".status"))
	return string(raw)
}
