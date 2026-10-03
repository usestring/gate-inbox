package hooks

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/parentseal"
)

// operatorSettings is a user settings file with hooks of its own on events the
// global hooks also use, and keys in an order no encoder would choose.
const operatorSettings = `{
  "model": "opus",
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "notify-send done"
          }
        ]
      }
    ],
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "~/bin/guard.sh # mentions gate-inbox but is not ours"
          }
        ]
      }
    ]
  },
  "env": {
    "A": "1"
  }
}
`

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

type parsedSettings struct {
	Hooks map[string][]hookMatcher `json:"hooks"`
}

func parse(t *testing.T, raw string) parsedSettings {
	t.Helper()
	var s parsedSettings
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("settings are not JSON: %v\n%s", err, raw)
	}
	return s
}

// commandsWith counts the commands across every event that contain needle.
func commandsWith(s parsedSettings, needle string) int {
	n := 0
	for _, groups := range s.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				if strings.Contains(h.Command, needle) {
					n++
				}
			}
		}
	}
	return n
}

func launchCommandCount(t *testing.T, configDir string) int {
	t.Helper()
	content, err := settingsContent(parentseal.KeyDir(configDir))
	if err != nil {
		t.Fatal(err)
	}
	return commandsWith(parse(t, string(content)), "")
}

func TestRegisterGlobalMergesOnceAndKeepsTheOperatorsEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	writeFile(t, path, operatorSettings)
	configDir := filepath.Join(dir, "gi home")

	changed, err := RegisterGlobal(path, configDir, "/opt/gi/bin/gate-inbox")
	if err != nil || !changed {
		t.Fatalf("first register: changed=%v err=%v", changed, err)
	}
	first := readFile(t, path)
	changed, err = RegisterGlobal(path, configDir, "/opt/gi/bin/gate-inbox")
	if err != nil || changed {
		t.Fatalf("second register: changed=%v err=%v, want an untouched file", changed, err)
	}
	if again := readFile(t, path); again != first {
		t.Fatalf("a second register rewrote the file:\n%s\nthen\n%s", first, again)
	}

	s := parse(t, first)
	if got, want := commandsWith(s, globalTag), launchCommandCount(t, configDir); got != want {
		t.Fatalf("%d global commands, want one per launch command (%d)", got, want)
	}
	if commandsWith(s, "notify-send done") != 1 || commandsWith(s, "guard.sh") != 1 {
		t.Fatalf("the operator's own hooks did not survive:\n%s", first)
	}
	// Order: the operator's keys where they were, and their own hook ahead of
	// ours on a shared event.
	if !(strings.Index(first, `"model"`) < strings.Index(first, `"hooks"`) && strings.Index(first, `"hooks"`) < strings.Index(first, `"env"`)) {
		t.Fatalf("top-level keys reordered:\n%s", first)
	}
	if s.Hooks["Stop"][0].Hooks[0].Command != "notify-send done" {
		t.Fatalf("the operator's Stop hook is no longer first: %+v", s.Hooks["Stop"])
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("mode changed: %v %v", info.Mode(), err)
	}
}

func TestRegisterGlobalReplacesItsOwnEntriesWhenTheBinaryMoves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	writeFile(t, path, operatorSettings)
	configDir := t.TempDir()
	if _, err := RegisterGlobal(path, configDir, "/old/gate-inbox"); err != nil {
		t.Fatal(err)
	}
	changed, err := RegisterGlobal(path, configDir, "/new/gate-inbox")
	if err != nil || !changed {
		t.Fatalf("register with a new binary: changed=%v err=%v", changed, err)
	}
	s := parse(t, readFile(t, path))
	if n := commandsWith(s, "/old/gate-inbox"); n != 0 {
		t.Fatalf("%d commands still name the old binary", n)
	}
	if got, want := commandsWith(s, "/new/gate-inbox"), launchCommandCount(t, configDir); got != want {
		t.Fatalf("%d commands name the new binary, want %d", got, want)
	}
	registered, err := GlobalRegistered(path, configDir, "/new/gate-inbox")
	if err != nil || !registered {
		t.Fatalf("GlobalRegistered = %v, %v", registered, err)
	}
	if stale, _ := GlobalRegistered(path, configDir, "/old/gate-inbox"); stale {
		t.Fatal("GlobalRegistered matched a binary that is no longer registered")
	}
}

func TestUnregisterGlobalRestoresTheOperatorsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	writeFile(t, path, operatorSettings)
	mine, other := t.TempDir(), t.TempDir()
	for _, dir := range []string{mine, other} {
		if _, err := RegisterGlobal(path, dir, "/bin/gate-inbox"); err != nil {
			t.Fatal(err)
		}
	}

	if changed, err := UnregisterGlobal(path, mine); err != nil || !changed {
		t.Fatalf("unregister one board: changed=%v err=%v", changed, err)
	}
	s := parse(t, readFile(t, path))
	if commandsWith(s, globalMark(mine)) != 0 || commandsWith(s, globalMark(other)) == 0 {
		t.Fatal("unregistering one board touched the other's entries, or left its own")
	}

	if changed, err := UnregisterGlobal(path, ""); err != nil || !changed {
		t.Fatalf("unregister all: changed=%v err=%v", changed, err)
	}
	if got, want := readFile(t, path), string(normalizeSettings([]byte(operatorSettings))); got != want {
		t.Fatalf("after removing every board the file is not the operator's:\n%s\nwant\n%s", got, want)
	}
	if changed, err := UnregisterGlobal(path, ""); err != nil || changed {
		t.Fatalf("a second unregister: changed=%v err=%v", changed, err)
	}
}

func TestRegisterGlobalOnAMissingOrEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "settings.json")
	if changed, err := UnregisterGlobal(path, ""); err != nil || changed {
		t.Fatalf("unregister on no file: changed=%v err=%v", changed, err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("unregister created a settings file")
	}
	if _, err := RegisterGlobal(path, dir, "/bin/gate-inbox"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("new settings file: %v %v", info, err)
	}
	if _, err := UnregisterGlobal(path, dir); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(readFile(t, path)); got != "{}" {
		t.Fatalf("after unregister the created file holds %s, want {}", got)
	}
}

func TestRegisterGlobalRefusesAFileItCannotRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	broken := `{"hooks": {"Stop": [}`
	writeFile(t, path, broken)
	if _, err := RegisterGlobal(path, t.TempDir(), "/bin/gate-inbox"); err == nil {
		t.Fatal("register succeeded over a malformed settings file")
	}
	if got := readFile(t, path); got != broken {
		t.Fatalf("a malformed file was rewritten to %q", got)
	}
}

// Every launch command is in the global set under the same event and
// matcher, behind the prelude, so an adopted session runs exactly what a
// launched one does.
func TestGlobalHooksMirrorTheLaunchHooks(t *testing.T) {
	configDir := t.TempDir()
	content, err := settingsContent(parentseal.KeyDir(configDir))
	if err != nil {
		t.Fatal(err)
	}
	launched := parse(t, string(content))
	global, err := globalHooks(configDir, "/bin/gate-inbox")
	if err != nil {
		t.Fatal(err)
	}
	prelude := globalPrelude(configDir, "/bin/gate-inbox")
	if len(global) != len(launched.Hooks) {
		t.Fatalf("%d global events, %d launch events", len(global), len(launched.Hooks))
	}
	for event, groups := range launched.Hooks {
		if len(global[event]) != len(groups) {
			t.Fatalf("%s: %d global groups, want %d", event, len(global[event]), len(groups))
		}
		for i, g := range groups {
			got := global[event][i]
			if got.Matcher != g.Matcher || len(got.Hooks) != len(g.Hooks) {
				t.Fatalf("%s[%d]: matcher %q, want %q", event, i, got.Matcher, g.Matcher)
			}
			for j, h := range g.Hooks {
				if got.Hooks[j].Command != prelude+h.Command {
					t.Fatalf("%s[%d][%d] is not the launch command behind the prelude:\n%s", event, i, j, got.Hooks[j].Command)
				}
			}
		}
	}
}

// hookRun is one hook firing: the command Claude Code would run and the
// payload on its stdin.
type hookRun struct {
	event, matchOn, payload string
}

// standardRuns fire the events a turn goes through, with a question in it.
var standardRuns = []hookRun{
	{"SessionStart", "startup", `{"source":"startup"}`},
	{"UserPromptSubmit", "", `{"prompt":"hello"}`},
	{"PreToolUse", "AskUserQuestion", `{"tool_name":"AskUserQuestion","tool_use_id":"t1","tool_input":{"questions":[{"question":"q?","header":"h","options":[{"label":"a"},{"label":"b"}]}]}}`},
	{"PostToolUse", "AskUserQuestion", `{"tool_name":"AskUserQuestion"}`},
	{"Notification", "permission_prompt", `{"notification_type":"permission_prompt"}`},
	{"Stop", "", `{}`},
}

// commandsFor is what Claude Code runs for one firing: every command whose
// group's matcher takes the value.
func commandsFor(t *testing.T, s parsedSettings, run hookRun) []string {
	t.Helper()
	var out []string
	for _, g := range s.Hooks[run.event] {
		if g.Matcher != "" && g.Matcher != "*" {
			matched := false
			for _, alt := range strings.Split(g.Matcher, "|") {
				matched = matched || alt == run.matchOn
			}
			if !matched {
				continue
			}
		}
		for _, h := range g.Hooks {
			out = append(out, h.Command)
		}
	}
	return out
}

// runHook runs one command as Claude Code does, with sh, from this process,
// which makes this process the hook's parent.
func runHook(t *testing.T, command, payload string, env []string) (string, time.Duration) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Env = env
	cmd.Stdin = strings.NewReader(payload)
	start := time.Now()
	out, err := cmd.CombinedOutput()
	took := time.Since(start)
	if err != nil {
		t.Fatalf("hook exited %v: %s\n%s", err, out, command)
	}
	return string(out), took
}

func registered(t *testing.T, configDir, bin string) parsedSettings {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	if _, err := RegisterGlobal(path, configDir, bin); err != nil {
		t.Fatal(err)
	}
	return parse(t, readFile(t, path))
}

// fakeBin stands in for the installed binary: it logs the verb it was run
// with and the identity the prelude gave it.
func fakeBin(t *testing.T) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	bin, log = filepath.Join(dir, "gate-inbox"), filepath.Join(dir, "calls")
	writeFile(t, bin, "#!/bin/sh\ncat >/dev/null\necho \"$* $GATE_INBOX_SESSION_ID $GATE_INBOX_HOME\" >> '"+log+"'\n")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	var names []string
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && path != dir {
			names = append(names, strings.TrimPrefix(path, dir))
		}
		return nil
	})
	return names
}

// A claude that is not on the board -- no tmux, a pane nobody adopted, or a
// session the board launched with its own hooks -- runs every global hook to
// nothing: exit 0, no output, no file written, no binary run.
func TestGlobalHooksDoNothingOutsideAnAdoptedPane(t *testing.T) {
	configDir := t.TempDir()
	bin, calls := fakeBin(t)
	s := registered(t, configDir, bin)
	m := NewManager(configDir)
	if err := m.SyncAdopted([]AdoptedPane{{ID: "adopted1", ServerPID: 4242, PaneID: "%7", AgentPID: os.Getpid()}}); err != nil {
		t.Fatal(err)
	}
	before := dirEntries(t, configDir)
	launchedStatus := filepath.Join(t.TempDir(), "launched.status")

	base := []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir()}
	cases := map[string][]string{
		"no tmux":               base,
		"unadopted pane":        append(base, "TMUX=/tmp/s,4242,0", "TMUX_PANE=%8"),
		"another server's pane": append(base, "TMUX=/tmp/s,999,0", "TMUX_PANE=%7"),
		// The launch's own --settings hooks report this one.
		"launched by the board": append(base, "TMUX=/tmp/s,4242,0", "TMUX_PANE=%7", EnvStatusFile+"="+launchedStatus),
	}
	var total time.Duration
	runs := 0
	for name, env := range cases {
		for _, run := range standardRuns {
			for _, command := range commandsFor(t, s, run) {
				out, took := runHook(t, command, run.payload, env)
				if out != "" {
					t.Fatalf("%s: %s printed %q", name, run.event, out)
				}
				total += took
				runs++
			}
		}
	}
	if after := dirEntries(t, configDir); strings.Join(after, ",") != strings.Join(before, ",") {
		t.Fatalf("hooks wrote under the config dir: %v -> %v", before, after)
	}
	if _, err := os.Stat(launchedStatus); err == nil {
		t.Fatal("a global hook wrote a launched session's status file: its events would be logged twice")
	}
	if _, err := os.Stat(calls); err == nil {
		t.Fatalf("a global hook ran the binary: %s", readFile(t, calls))
	}
	t.Logf("%d no-op hook runs, %v each on average (sh start included)", runs, total/time.Duration(runs))
}

// In an adopted pane the same hooks report as the row: status into its log,
// and the subcommands run with its id and the board's home.
func TestGlobalHooksReportForAnAdoptedPane(t *testing.T) {
	configDir := t.TempDir()
	bin, calls := fakeBin(t)
	s := registered(t, configDir, bin)
	m := NewManager(configDir)
	if err := m.SyncAdopted([]AdoptedPane{{ID: "adopted1", ServerPID: 4242, PaneID: "%7", AgentPID: os.Getpid()}}); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir(), "TMUX=/tmp/tmux-1/default,4242,3", "TMUX_PANE=%7"}
	for _, run := range standardRuns {
		for _, command := range commandsFor(t, s, run) {
			runHook(t, command, run.payload, env)
		}
	}

	events, _, ok := m.Events("adopted1", 0)
	if !ok {
		t.Fatal("no status log for the adopted row")
	}
	var got []string
	for _, ev := range events {
		got = append(got, ev.State+" "+ev.Name)
	}
	want := []string{"idle SessionStart", "working UserPromptSubmit", "waiting PreToolUse", "working PostToolUse", "waiting Notification", "finished Stop"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("status log = %v, want %v", got, want)
	}
	if state, _ := m.Read("adopted1"); state != "finished" {
		t.Fatalf("Read = %q, want finished", state)
	}
	log := readFile(t, calls)
	for _, verb := range []string{"session-start", "prompt-submit", "ask-pending", "ask-answered"} {
		if !strings.Contains(log, "hook "+verb+" adopted1 "+configDir+"\n") {
			t.Fatalf("hook %s was not run as adopted1 on %s:\n%s", verb, configDir, log)
		}
	}
}

// A claude started from inside the adopted one -- a `claude -p` from its Bash
// tool -- shares the pane and its environment but is not the row's agent, so
// its hooks must stay quiet.
func TestGlobalHooksIgnoreAClaudeNestedInAnAdoptedPane(t *testing.T) {
	configDir := t.TempDir()
	bin, calls := fakeBin(t)
	s := registered(t, configDir, bin)
	m := NewManager(configDir)
	if err := m.SyncAdopted([]AdoptedPane{{ID: "adopted1", ServerPID: 4242, PaneID: "%7", AgentPID: os.Getpid()}}); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=/usr/bin:/bin", "TMUX=/tmp/s,4242,0", "TMUX_PANE=%7"}
	for _, run := range standardRuns {
		for _, command := range commandsFor(t, s, run) {
			// One process further down: the hook's parent is the nested claude,
			// here an intermediate sh.
			nested := exec.Command("/bin/sh", "-c", `/bin/sh -c "$HOOK"; :`)
			nested.Env = append(env, "HOOK="+command)
			nested.Stdin = strings.NewReader(run.payload)
			if out, err := nested.CombinedOutput(); err != nil || len(out) != 0 {
				t.Fatalf("nested %s: %v %q", run.event, err, out)
			}
		}
	}
	if _, ok := m.Read("adopted1"); ok {
		t.Fatal("a nested claude's hooks reported as the adopted row")
	}
	if _, err := os.Stat(calls); err == nil {
		t.Fatalf("a nested claude's hooks ran the binary: %s", readFile(t, calls))
	}
}

func TestSyncAdoptedKeepsExactlyTheGivenPanes(t *testing.T) {
	m := NewManager(t.TempDir())
	pid := os.Getpid()
	if err := m.SyncAdopted([]AdoptedPane{
		{ID: "a1", ServerPID: 10, PaneID: "%1", AgentPID: pid},
		{ID: "a2", ServerPID: 10, PaneID: "%2", AgentPID: pid},
		{ID: "../x", ServerPID: 10, PaneID: "%3", AgentPID: pid},
		{ID: "a4", ServerPID: 10, PaneID: "1;rm", AgentPID: pid},
	}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(dirEntries(t, m.AdoptedDir()), ","); got != "/10%1,/10%2" {
		t.Fatalf("markers = %s", got)
	}
	if got := readFile(t, filepath.Join(m.AdoptedDir(), "10%1")); got != "a1 "+strconv.Itoa(pid)+"\n" {
		t.Fatalf("marker = %q", got)
	}
	if err := m.SyncAdopted([]AdoptedPane{{ID: "a2", ServerPID: 10, PaneID: "%2", AgentPID: pid}}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(dirEntries(t, m.AdoptedDir()), ","); got != "/10%2" {
		t.Fatalf("after letting a1 go, markers = %s", got)
	}
	if err := m.SyncAdopted(nil); err != nil {
		t.Fatal(err)
	}
	if got := dirEntries(t, m.AdoptedDir()); len(got) != 0 {
		t.Fatalf("markers left with nothing adopted: %v", got)
	}
}
