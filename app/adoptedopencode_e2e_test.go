package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/opencode"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// envProbe prints the row a shell command of the session speaks as.
const envProbe = `echo GIENV=${GATE_INBOX_SESSION_ID:-unset}`

// steerQuestion asks whether the board's steering reached the model, without
// the answer's token appearing in the prompt drawn on screen.
const steerQuestion = "Do not use any tools. Does your system prompt contain a section whose heading is exactly '# Gate Inbox'? " +
	"Reply with the word GISTEER, then a dash, then YES or NO, and nothing else."

// TestAdoptedOpencodeE2E proves, against real OpenCode v2 sessions, that an
// opencode started outside the board gets what a launched one has once the
// board adopts its pane, and nothing before:
//
//	a. one board start writes Gate Inbox's plugin into the global plugins
//	   directory, with no command and no change to the operator's config;
//	b. with the board stopped, a plain opencode works with nothing of Gate
//	   Inbox in its shell and no error on screen;
//	c. the board adopts the pane: the next shell command speaks as the row
//	   (the CLI renames it), the row binds to the opencode session, the
//	   steering reaches the model, the row's status follows the pane, and an
//	   opencode on a server the board does not adopt still gets nothing;
//	d. a board-launched opencode keeps its own row and gets no plugin
//	   steering on top of its own;
//	e. with the board stopped, the released session's shell is plain again.
//
// It spends about seven short turns on the default (free) model of the
// operator's opencode login, so it is opt-in. Everything runs under a
// scratch HOME and XDG directories with only auth.json copied in, on a
// background service port of its own, and on test-owned tmux servers:
//
//	GATE_INBOX_E2E_OPENCODE=1 [GATE_INBOX_E2E_OPENCODE_OUT=<dir>] \
//	  go test ./app -run TestAdoptedOpencodeE2E -timeout 30m -v
func TestAdoptedOpencodeE2E(t *testing.T) {
	if os.Getenv("GATE_INBOX_E2E_OPENCODE") == "" {
		t.Skip("GATE_INBOX_E2E_OPENCODE unset")
	}
	bin, err := exec.LookPath("opencode")
	if err != nil {
		t.Skip("opencode not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("the board polls a tmux server")
	}
	auth := filepath.Join(opencodeDataDir(), "auth.json")
	if _, err := os.Stat(auth); err != nil {
		t.Skip("no opencode login at " + auth)
	}
	e := newOpencodeE2E(t, bin, auth)
	e.stepA()
	e.stepB()
	adopted := e.stepC()
	e.stepD()
	e.stepE(adopted)
}

// opencodeDataDir is where the operator's own opencode keeps its login.
func opencodeDataDir() string {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "opencode")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "opencode")
}

type opencodeE2E struct {
	*adoptedE2E
	home      string
	configDir string
	fresh     tmuxHost
}

func newOpencodeE2E(t *testing.T, opencodeBin, auth string) *opencodeE2E {
	t.Helper()
	root, err := filepath.Abs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	out := os.Getenv("GATE_INBOX_E2E_OPENCODE_OUT")
	if out == "" {
		out = filepath.Join(root, "out")
	}
	e := &opencodeE2E{adoptedE2E: &adoptedE2E{
		t: t, root: root, out: out, title: "Adopted OpenCode session e2e",
		giHome: filepath.Join(root, "gi"),
		workA:  filepath.Join(root, "work", "adopted"),
		workL:  filepath.Join(root, "work", "launched"),
		workN:  filepath.Join(root, "work", "fresh"),
	}}
	e.home = filepath.Join(root, "home")
	e.configDir = filepath.Join(e.home, ".config", "opencode")
	data := filepath.Join(e.home, ".local", "share", "opencode")
	tmp := filepath.Join(root, "tmp")
	for _, dir := range []string{out, e.giHome, e.workA, e.workL, e.workN, e.configDir, data, tmp} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	copyFile(t, auth, filepath.Join(data, "auth.json"), 0o600)
	// The operator's own config, which registering must leave byte for byte.
	if err := os.WriteFile(filepath.Join(e.configDir, "opencode.jsonc"),
		[]byte("{\n  // mine\n  \"autoupdate\": false\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	e.bin = buildBoard(t)
	var env []string
	for _, kv := range tmuxtest.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(key, "OPENCODE") || strings.HasPrefix(key, "GATE_INBOX_") || strings.HasPrefix(key, "XDG_") ||
			key == "HOME" || key == "TMPDIR" || key == "PATH" {
			continue
		}
		env = append(env, kv)
	}
	e.env = append(env, "HOME="+e.home, "TMPDIR="+tmp, "GATE_INBOX_HOME="+e.giHome, "TERM=xterm-256color",
		"PATH="+filepath.Dir(opencodeBin)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"XDG_CONFIG_HOME="+filepath.Join(e.home, ".config"), "XDG_DATA_HOME="+filepath.Join(e.home, ".local", "share"),
		"XDG_STATE_HOME="+filepath.Join(e.home, ".local", "state"), "XDG_CACHE_HOME="+filepath.Join(e.home, ".cache"))
	e.tmpdir = envValue(e.env, "TMUX_TMPDIR")
	for _, rc := range []string{".zshrc", ".bashrc"} {
		if err := os.WriteFile(filepath.Join(e.home, rc), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The background service listens on one port per user; the scratch one
	// takes a free port of its own rather than fighting the operator's.
	if out, err := e.opencode("service", "set", "port", freePort(t)); err != nil {
		t.Fatalf("set the scratch service port: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_, _ = e.opencode("service", "stop")
		reapScratch(e.home)
		_ = os.Remove(filepath.Join(data, "auth.json"))
		e.writeSteps()
	})
	e.seedBoard()

	mk := func(family string) tmuxHost {
		name := tmuxtest.NewSocket(family)
		h := tmuxHost{name: name, path: hostSocket(t, e.tmpdir, name), env: e.env}
		t.Cleanup(func() { killTestServer(t, e.tmpdir, name) })
		return h
	}
	e.board = mk("ocboard")
	e.agents = mk("ocagents")
	e.plain = mk("ocplain")
	e.fresh = mk("ocfresh")
	config := "poll_interval = \"2s\"\n" +
		"tmux_socket = \"" + e.agents.name + "\"\n" +
		"adopt_sockets = [\"" + e.plain.name + "\"]\n" +
		"[log]\nlevel = \"info\"\nfile = \"" + filepath.Join(e.out, "board.log") + "\"\n"
	if err := os.WriteFile(filepath.Join(e.giHome, "config.toml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.stopBoard)
	return e
}

func (e *opencodeE2E) opencode(args ...string) (string, error) {
	cmd := exec.Command("opencode", args...)
	cmd.Env = e.env
	cmd.Dir = e.root
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func copyFile(t *testing.T, from, to string, mode os.FileMode) {
	t.Helper()
	in, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(errors.Join(err, out.Close()))
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

// reapScratch kills what is left running under the scratch HOME: the
// background service and anything it started outlive their panes.
func reapScratch(home string) {
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
		for _, kv := range strings.Split(string(raw), "\x00") {
			if kv == "HOME="+home {
				_ = syscall.Kill(pid, syscall.SIGKILL)
				break
			}
		}
	}
}

// startOpencode runs a plain opencode in its own session on h and waits for
// its composer.
func (e *opencodeE2E) startOpencode(h tmuxHost, session, dir string) {
	e.t.Helper()
	if out, err := h.run("new-session", "-d", "-s", session, "-x", "200", "-y", "50", "-c", dir, "opencode"); err != nil {
		e.t.Fatalf("start opencode on %s: %v\n%s", h.name, err, out)
	}
	e.waitOpencode(h, session)
}

func (e *opencodeE2E) waitOpencode(h tmuxHost, target string) {
	e.t.Helper()
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		if s := h.screen(target); strings.Contains(s, "Ask anything") || strings.Contains(s, "ctrl+p commands") {
			return
		}
		time.Sleep(time.Second)
	}
	e.save("composer-timeout-"+h.name+".txt", h.history(target))
	e.t.Fatalf("opencode on %s never drew its composer", h.name)
}

// ask types a prompt and waits for want to show on the pane after it.
func (e *opencodeE2E) ask(h tmuxHost, target, prompt, want string, limit time.Duration) (string, bool) {
	e.t.Helper()
	before := h.history(target)
	h.typeLine(e.t, target, prompt)
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		screen := h.history(target)
		if strings.Count(screen, want) > strings.Count(before, want) {
			time.Sleep(2 * time.Second)
			return h.history(target), true
		}
		time.Sleep(time.Second)
	}
	return h.history(target), false
}

// opencodeErrors is what opencode draws when a plugin or an MCP server fails.
func opencodeErrors(screen string) []string {
	var found []string
	for _, bad := range []string{"MCP failed", "Plugin", "Schema validation", "Error:"} {
		if strings.Contains(screen, bad) {
			found = append(found, bad)
		}
	}
	return found
}

func (e *opencodeE2E) pluginPath() string {
	return opencode.PluginPath(e.configDir, e.giHome)
}

// a. One board start is the whole setup.
func (e *opencodeE2E) stepA() {
	e.startBoard()
	waitUntilQuiet(30*time.Second, func() bool { _, err := os.Stat(e.pluginPath()); return err == nil })
	plugin, err := os.ReadFile(e.pluginPath())
	config, _ := os.ReadFile(filepath.Join(e.configDir, "opencode.jsonc"))
	entries, _ := os.ReadDir(e.configDir)
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	e.save("a-plugin.js", string(plugin))
	e.record("a", "a board start writes the plugin and leaves the operator's config alone",
		err == nil && strings.Contains(string(plugin), "gate-inbox-global-plugin") &&
			string(config) == "{\n  // mine\n  \"autoupdate\": false\n}\n",
		fmt.Sprintf("plugin %s: %v (%d bytes)\nconfig dir: %v\nopencode.jsonc:\n%s", e.pluginPath(), err, len(plugin), names, config))
	e.stopBoard()
}

// b. Outside the board, with the plugin left in place.
func (e *opencodeE2E) stepB() {
	e.startOpencode(e.plain, "plain", e.workA)
	began := time.Now()
	screen, ok := e.ask(e.plain, "plain", "Run this exact shell command with your shell tool and show its output: "+envProbe, "GIENV=unset", 150*time.Second)
	e.save("b-plain.txt", screen)
	bad := opencodeErrors(screen)
	e.record("b", "a plain opencode with the board stopped has nothing of Gate Inbox in its shell and no error on screen",
		ok && len(bad) == 0 && !strings.Contains(screen, "GIENV=row"),
		fmt.Sprintf("GIENV=unset seen: %v after %s\nerror marks: %v\n%s", ok, time.Since(began).Round(time.Second), bad, lastLines(screen, 25)))
}

// c. The board adopts the pane.
func (e *opencodeE2E) stepC() string {
	t := e.t
	e.startBoard()
	var adopted store.Session
	waitUntil(t, 120*time.Second, "the board to adopt the plain opencode", func() bool {
		st, err := store.Open(filepath.Join(e.giHome, "state.db"))
		if err != nil {
			return false
		}
		defer st.Close()
		rows, _ := st.ListSessions(false)
		for _, r := range rows {
			if r.TmuxSocket == e.plain.name && r.Tool == "opencode" {
				adopted = r
				return true
			}
		}
		return false
	})
	id := adopted.ID
	var marker string
	waitUntilQuiet(30*time.Second, func() bool {
		entries, _ := os.ReadDir(filepath.Join(e.giHome, "hooks", "adopted"))
		if len(entries) != 1 {
			return false
		}
		raw, _ := os.ReadFile(filepath.Join(e.giHome, "hooks", "adopted", entries[0].Name()))
		marker = string(raw)
		return strings.HasPrefix(marker, id+" ")
	})
	e.record("c", "the board adopts the plain opencode's pane and marks it as opencode",
		id != "" && strings.Contains(marker, " opencode"),
		fmt.Sprintf("row %s %q on pane %s, marker %q", id, adopted.Name, adopted.TmuxPaneID, marker))
	e.settle(id)

	statuses := map[string]bool{}
	stop, watched := make(chan struct{}), make(chan struct{})
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
	screen, ok := e.ask(e.plain, "plain", "Run this exact shell command with your shell tool and show its output: "+envProbe+
		` && "$GATE_INBOX_BIN" rename oc-adopted-e2e && echo RENAMED-$((40+2))`, "RENAMED-42", 150*time.Second)
	e.save("c-identity.txt", screen)
	var row store.Session
	waitUntilQuiet(30*time.Second, func() bool {
		row = e.storeRow(id)
		return row.Name == "oc-adopted-e2e" && strings.HasPrefix(row.AgentSessionID, "ses_")
	})
	e.record("c", "the adopted session's shell speaks as its row: the CLI renames it",
		ok && strings.Contains(screen, "GIENV="+id) && row.Name == "oc-adopted-e2e",
		fmt.Sprintf("GIENV=%s seen: %v, row name %q\n%s", id, strings.Contains(screen, "GIENV="+id), row.Name, lastLines(screen, 20)))
	waitUntilQuiet(30*time.Second, func() bool {
		entries, _ := os.ReadDir(filepath.Join(e.giHome, "hooks", "adopted"))
		for _, entry := range entries {
			got, _ := os.ReadFile(filepath.Join(e.giHome, "hooks", "adopted", entry.Name()))
			marker = string(got)
		}
		return row.AgentSessionID != "" && strings.Contains(marker, row.AgentSessionID)
	})
	e.record("c", "the row binds to the opencode session and its marker carries it",
		strings.HasPrefix(row.AgentSessionID, "ses_") && strings.Contains(marker, row.AgentSessionID),
		fmt.Sprintf("agent session %q, marker %q", row.AgentSessionID, marker))

	steer, ok := e.ask(e.plain, "plain", steerQuestion, "GISTEER-", 120*time.Second)
	close(stop)
	<-watched
	e.save("c-steering.txt", steer)
	e.record("c", "the adopted session's model has the board's steering",
		ok && strings.Contains(steer, "GISTEER-YES"), lastLines(steer, 15))
	e.record("c", "the row's status follows the pane through a turn",
		statuses["working"] && (statuses["idle"] || statuses["finished"]),
		fmt.Sprintf("statuses seen: %v", statuses))

	// An opencode on a server the board does not adopt, sharing the same
	// background service and plugin.
	e.startOpencode(e.fresh, "fresh", e.workN)
	screen, ok = e.ask(e.fresh, "fresh", "Run this exact shell command with your shell tool and show its output: "+envProbe, "GIENV=unset", 150*time.Second)
	steer2, ok2 := e.ask(e.fresh, "fresh", steerQuestion, "GISTEER-", 120*time.Second)
	e.save("c-fresh.txt", steer2)
	e.record("c", "an opencode the board did not adopt gets no row and no steering while the board runs",
		ok && ok2 && strings.Contains(steer2, "GISTEER-NO") && len(opencodeErrors(steer2)) == 0,
		fmt.Sprintf("GIENV=unset: %v\n%s", ok, lastLines(steer2, 15)))
	return id
}

// d. A board-launched opencode is left to its own launch path.
func (e *opencodeE2E) stepD() {
	t := e.t
	out, err := e.gi(operator, "spawn", "--tool", "opencode", "--name", "launched", "--directory", e.workL, "--json")
	if err != nil {
		t.Fatalf("spawn: %v\n%s", err, out)
	}
	e.launched = idOf(t, out)
	target := "gi_" + e.launched
	e.waitOpencode(e.agents, target)
	screen, ok := e.ask(e.agents, target, "Run this exact shell command with your shell tool and show its output: "+envProbe, "GIENV="+e.launched, 150*time.Second)
	steer, ok2 := e.ask(e.agents, target, steerQuestion, "GISTEER-", 120*time.Second)
	e.save("d-launched.txt", steer)
	e.record("d", "the launched opencode speaks as its own row and gets no plugin steering on top of its own",
		ok && strings.Contains(screen, "GIENV="+e.launched) && ok2 && strings.Contains(steer, "GISTEER-NO"),
		fmt.Sprintf("GIENV=%s seen: %v\n%s", e.launched, strings.Contains(screen, "GIENV="+e.launched), lastLines(steer, 15)))
}

// e. The board stopped: the adopted pane's session is plain again.
func (e *opencodeE2E) stepE(adopted string) {
	e.stopBoard()
	screen, ok := e.ask(e.plain, "plain", "Run this exact shell command with your shell tool and show its output: echo AFTER-$(echo ${GATE_INBOX_SESSION_ID:-unset})", "AFTER-unset", 150*time.Second)
	e.save("e-released.txt", screen)
	e.record("e", "with the board stopped the released session's shell has no row and no error shows",
		ok && !strings.Contains(screen, "AFTER-"+adopted) && len(opencodeErrors(lastLines(screen, 30))) == 0,
		lastLines(screen, 20))
}
