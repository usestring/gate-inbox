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

	"github.com/usestring/gate-inbox/internal/singleton"
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

// oddSettings is laid out the way no encoder would write it, and has no hooks.
const oddSettings = "{\n\t\"theme\" : \"dark\",\n\t\"permissions\": {\"allow\": [\"Bash(ls)\"],   \"deny\": []},\n\t\"statusLine\": {\"type\": \"command\", \"command\": \"x\"}\n}"

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

// withoutHooks is raw with its hooks member taken out: everything a merge
// must leave alone.
func withoutHooks(t *testing.T, raw string) string {
	t.Helper()
	out, err := spliceHooks([]byte(raw), nil)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// outsideGlobal is raw with the hooks member and configDir's key-dir denials
// taken out, along with any list or object the denials could have needed.
func outsideGlobal(t *testing.T, raw, configDir string) string {
	t.Helper()
	out, err := removeDenials([]byte(withoutHooks(t, raw)), configDir,
		[]string{"permissions", "permissions.deny", "sandbox", "sandbox.filesystem", "sandbox.filesystem.denyRead"})
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestRegisterGlobalWritesOneEntryPerEventOnce(t *testing.T) {
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
	if got := commandsWith(s, globalTag); got != len(globalEvents) {
		t.Fatalf("%d global entries, want one for each of %v", got, globalEvents)
	}
	for _, event := range globalEvents {
		if n := commandsWith(parsedSettings{Hooks: map[string][]hookMatcher{event: s.Hooks[event]}}, globalTag); n != 1 {
			t.Fatalf("%s carries %d global entries", event, n)
		}
	}
	if commandsWith(s, "SessionEnd") != 0 || s.Hooks["SessionEnd"] != nil {
		t.Fatal("SessionEnd was registered; adoption does not need it")
	}
	if strings.Contains(first, `\u00`) {
		t.Fatalf("the commands were written with escapes the operator has to decode:\n%s", first)
	}
	if commandsWith(s, "notify-send done") != 1 || commandsWith(s, "guard.sh") != 1 {
		t.Fatalf("the operator's own hooks did not survive:\n%s", first)
	}
	if s.Hooks["Stop"][0].Hooks[0].Command != "notify-send done" {
		t.Fatalf("the operator's Stop hook is no longer first: %+v", s.Hooks["Stop"])
	}
	if outsideGlobal(t, first, configDir) != outsideGlobal(t, operatorSettings, configDir) {
		t.Fatalf("bytes outside the hooks member changed:\n%s", first)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("mode changed: %v %v", info.Mode(), err)
	}
}

// Registering and then removing the hooks gives back the operator's file byte
// for byte, however it was laid out.
func TestGlobalHooksRoundTripTheOperatorsBytes(t *testing.T) {
	for name, original := range map[string]string{
		"odd layout, no hooks": oddSettings,
		"own hooks":            operatorSettings,
		"empty object":         "{}\n",
		"compact":              `{"a":1,"b":[1,2]}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			writeFile(t, path, original)
			configDir := t.TempDir()
			if _, err := RegisterGlobal(path, configDir, "/bin/gate-inbox"); err != nil {
				t.Fatal(err)
			}
			registered := readFile(t, path)
			parse(t, registered)
			if outsideGlobal(t, registered, configDir) != outsideGlobal(t, original, configDir) {
				t.Fatalf("bytes outside the hooks member changed:\n%q\nfrom\n%q", registered, original)
			}
			if _, err := UnregisterGlobal(path, configDir); err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, path); got != original {
				t.Fatalf("after unregister:\n%q\nwant\n%q", got, original)
			}
		})
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
	if got := commandsWith(s, "/new/gate-inbox"); got != len(globalEvents) {
		t.Fatalf("%d commands name the new binary, want %d", got, len(globalEvents))
	}
	if ok, err := GlobalRegistered(path, configDir, "/new/gate-inbox"); err != nil || !ok {
		t.Fatalf("GlobalRegistered = %v, %v", ok, err)
	}
	if stale, _ := GlobalRegistered(path, configDir, "/old/gate-inbox"); stale {
		t.Fatal("GlobalRegistered matched a binary that is no longer registered")
	}
}

func TestUnregisterGlobalScopesToItsBoard(t *testing.T) {
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
	if commandsWith(s, globalMark(mine)) != 0 || commandsWith(s, globalMark(other)) != len(globalEvents) {
		t.Fatal("unregistering one board touched the other's entries, or left its own")
	}
	if changed, err := UnregisterGlobal(path, ""); err != nil || !changed {
		t.Fatalf("unregister all: changed=%v err=%v", changed, err)
	}
	if got := readFile(t, path); got != operatorSettings {
		t.Fatalf("after removing every board the file is not the operator's:\n%s", got)
	}
	if changed, err := UnregisterGlobal(path, ""); err != nil || changed {
		t.Fatalf("a second unregister: changed=%v err=%v", changed, err)
	}
}

func TestRegisterGlobalOnAMissingFile(t *testing.T) {
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

// A settings file the operator made read-only stays as it is: the directory is
// writable, so a rename would have gone through and undone their choice.
func TestRegisterGlobalRefusesAReadOnlyFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes through any mode")
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	writeFile(t, path, oddSettings)
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterGlobal(path, t.TempDir(), "/bin/gate-inbox"); err == nil || !strings.Contains(err.Error(), "not writable") {
		t.Fatalf("register over a read-only file: %v", err)
	}
	if got := readFile(t, path); got != oddSettings {
		t.Fatalf("a read-only file was rewritten to %q", got)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o444 {
		t.Fatalf("mode = %v", info.Mode())
	}
}

// A settings file that is a link stays a link; its target takes the change.
func TestRegisterGlobalWritesThroughALink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles-settings.json")
	writeFile(t, target, oddSettings)
	link := filepath.Join(dir, "settings.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterGlobal(link, t.TempDir(), "/bin/gate-inbox"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was replaced: %v %v", info, err)
	}
	if commandsWith(parse(t, readFile(t, target)), globalTag) != len(globalEvents) {
		t.Fatal("the link's target did not get the hooks")
	}
}

// DispatchGlobal runs the launch commands whose matchers take the payload,
// which is what a launched session's own hooks do for the same event.
func TestDispatchGlobalRunsTheMatchingLaunchCommands(t *testing.T) {
	configDir := t.TempDir()
	m := NewManager(configDir)
	t.Setenv(EnvSessionID, "adopted1")
	t.Setenv(EnvStatusFile, m.StatusFile("adopted1"))
	t.Setenv(EnvExecutable, filepath.Join(t.TempDir(), "absent"))
	if err := os.MkdirAll(m.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct{ event, payload, want string }{
		{"PreToolUse", `{"tool_name":"Bash"}`, "working PreToolUse"},
		{"PreToolUse", `{"tool_name":"AskUserQuestion"}`, "waiting PreToolUse"},
		{"Notification", `{"notification_type":"idle_prompt"}`, "waiting PreToolUse"},
		{"Notification", `{"notification_type":"elicitation_dialog"}`, "waiting Notification"},
		{"SessionStart", `{"source":"compact"}`, "waiting Notification"},
		{"SessionEnd", `{}`, "waiting Notification"},
		{"StopFailure", `{"error":"rate_limit"}`, "errored StopFailure"},
	} {
		if out := m.DispatchGlobal(step.event, []byte(step.payload)); out != "" {
			t.Fatalf("%s printed %q", step.event, out)
		}
		events, _, _ := m.Events("adopted1", 0)
		if len(events) == 0 || events[len(events)-1].State+" "+events[len(events)-1].Name != step.want {
			t.Fatalf("after %s %s the log ends %v, want %s", step.event, step.payload, events, step.want)
		}
	}
	if _, err := os.Stat(filepath.Join(m.Dir(), "adopted1.status")); err != nil {
		t.Fatal(err)
	}
}

func TestMergeHookOutputsJoinsNotes(t *testing.T) {
	a := `{"hookSpecificOutput":{"hookEventName":"PostToolUse","classifierContext":"one"}}`
	b := `{"hookSpecificOutput":{"hookEventName":"PostToolUse","classifierContext":"two","additionalContext":"x"}}`
	var got struct {
		H map[string]string `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(mergeHookOutputs([]string{a, b})), &got); err != nil {
		t.Fatal(err)
	}
	if got.H["hookEventName"] != "PostToolUse" || got.H["classifierContext"] != "one\n\ntwo" || got.H["additionalContext"] != "x" {
		t.Fatalf("merged = %v", got.H)
	}
	if mergeHookOutputs([]string{a}) != a || mergeHookOutputs(nil) != "" || mergeHookOutputs([]string{"text", a}) != "text" {
		t.Fatal("single, empty or non-JSON outputs were not passed through")
	}
}

// adoptedBoard is a board home with one adopted pane, %7 on server 4242, whose
// claude is this test process, and a running board; and the global hooks
// registered against a stand-in binary that logs every call.
type adoptedBoard struct {
	configDir, bin, calls string
	settings              parsedSettings
}

func newAdoptedBoard(t *testing.T) adoptedBoard {
	t.Helper()
	b := adoptedBoard{configDir: t.TempDir()}
	dir := t.TempDir()
	b.bin, b.calls = filepath.Join(dir, "gate-inbox"), filepath.Join(dir, "calls")
	writeFile(t, b.bin, "#!/bin/sh\ncat >/dev/null\necho \"$* $GATE_INBOX_SESSION_ID $GATE_INBOX_HOME $GATE_INBOX_AGENT_PID\" >> '"+b.calls+"'\n")
	if err := os.Chmod(b.bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(b.configDir, singleton.FileName), strconv.Itoa(os.Getpid()))
	if err := NewManager(b.configDir).SyncAdopted([]AdoptedPane{{ID: "adopted1", ServerPID: 4242, PaneID: "%7", AgentPID: os.Getpid()}}); err != nil {
		t.Fatal(err)
	}
	if err := NewManager(b.configDir).PrepareArrivals(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	if _, err := RegisterGlobal(path, b.configDir, b.bin); err != nil {
		t.Fatal(err)
	}
	b.settings = parse(t, readFile(t, path))
	return b
}

var adoptedEnv = []string{"PATH=/usr/bin:/bin", "TMUX=/tmp/tmux-1/default,4242,3", "TMUX_PANE=%7"}

// fireAll runs every registered command once, as Claude Code would: sh, as
// this process's child. It reports what reached stdout or stderr.
func (b adoptedBoard) fireAll(t *testing.T, env []string, wrap func(string) string) (noise string, each time.Duration) {
	t.Helper()
	var total time.Duration
	runs := 0
	for event, groups := range b.settings.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				cmd := exec.Command("/bin/sh", "-c", wrap(h.Command))
				cmd.Env = env
				cmd.Stdin = strings.NewReader(`{"tool_name":"AskUserQuestion","prompt":"hi"}`)
				start := time.Now()
				out, err := cmd.CombinedOutput()
				total += time.Since(start)
				runs++
				if err != nil {
					t.Fatalf("%s exited %v", event, err)
				}
				noise += string(out)
			}
		}
	}
	return noise, total / time.Duration(runs)
}

func direct(command string) string { return command }

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

func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

// Every way a session can be outside an adopted pane -- or Gate Inbox can be
// half gone -- leaves every global hook silent: exit 0, nothing on stdout or
// stderr, the binary never run, nothing written. The one write allowed is
// SessionStart's announcement of a pane the running board has no marker
// for, which the cases in announces expect.
func TestGlobalHooksStaySilentUnlessAdoptedAndAlive(t *testing.T) {
	announces := map[string]string{
		"a pane nobody adopted": "/hooks/arrivals/4242%8",
		"another server's pane": "/hooks/arrivals/999%7",
	}
	cases := map[string]func(t *testing.T, b *adoptedBoard) ([]string, func(string) string){
		"no tmux": func(t *testing.T, b *adoptedBoard) ([]string, func(string) string) {
			return []string{"PATH=/usr/bin:/bin"}, direct
		},
		"a pane nobody adopted": func(t *testing.T, b *adoptedBoard) ([]string, func(string) string) {
			return []string{"PATH=/usr/bin:/bin", "TMUX=/tmp/s,4242,0", "TMUX_PANE=%8"}, direct
		},
		"another server's pane": func(t *testing.T, b *adoptedBoard) ([]string, func(string) string) {
			return []string{"PATH=/usr/bin:/bin", "TMUX=/tmp/s,999,0", "TMUX_PANE=%7"}, direct
		},
		"a session the board launched": func(t *testing.T, b *adoptedBoard) ([]string, func(string) string) {
			return append(adoptedEnv, EnvStatusFile+"="+filepath.Join(t.TempDir(), "launched.status")), direct
		},
		// One process further down: the hook's parent is a claude started
		// inside the adopted one.
		"a claude nested in the adopted one": func(t *testing.T, b *adoptedBoard) ([]string, func(string) string) {
			return adoptedEnv, func(c string) string { return "/bin/sh -c " + shellQuote(c) + "; :" }
		},
		// A codex adopted in the pane, whose pid is even the hook's parent:
		// its marker's third field keeps a claude's hooks out of its row.
		"another agent's marker": func(t *testing.T, b *adoptedBoard) ([]string, func(string) string) {
			if err := NewManager(b.configDir).SyncAdopted([]AdoptedPane{{ID: "adopted1", ServerPID: 4242, PaneID: "%7", AgentPID: os.Getpid(), Tool: "codex"}}); err != nil {
				t.Fatal(err)
			}
			return adoptedEnv, direct
		},
		"a marker left by a pane that is gone": func(t *testing.T, b *adoptedBoard) ([]string, func(string) string) {
			if err := NewManager(b.configDir).SyncAdopted([]AdoptedPane{{ID: "adopted1", ServerPID: 4242, PaneID: "%7", AgentPID: deadPID(t)}}); err != nil {
				t.Fatal(err)
			}
			return adoptedEnv, direct
		},
		"the board is not running": func(t *testing.T, b *adoptedBoard) ([]string, func(string) string) {
			writeFile(t, filepath.Join(b.configDir, singleton.FileName), strconv.Itoa(deadPID(t)))
			return adoptedEnv, direct
		},
		"an unmarked pane with the board down": func(t *testing.T, b *adoptedBoard) ([]string, func(string) string) {
			writeFile(t, filepath.Join(b.configDir, singleton.FileName), strconv.Itoa(deadPID(t)))
			return []string{"PATH=/usr/bin:/bin", "TMUX=/tmp/s,4242,0", "TMUX_PANE=%8"}, direct
		},
		"an unmarked pane the board launched": func(t *testing.T, b *adoptedBoard) ([]string, func(string) string) {
			return []string{"PATH=/usr/bin:/bin", "TMUX=/tmp/s,4242,0", "TMUX_PANE=%8", EnvStatusFile + "=" + filepath.Join(t.TempDir(), "launched.status")}, direct
		},
		"an unmarked pane before the board made its arrivals": func(t *testing.T, b *adoptedBoard) ([]string, func(string) string) {
			if err := os.Remove(NewManager(b.configDir).ArrivalsDir()); err != nil {
				t.Fatal(err)
			}
			return []string{"PATH=/usr/bin:/bin", "TMUX=/tmp/s,4242,0", "TMUX_PANE=%8"}, direct
		},
		"the board never ran": func(t *testing.T, b *adoptedBoard) ([]string, func(string) string) {
			os.Remove(filepath.Join(b.configDir, singleton.FileName))
			return adoptedEnv, direct
		},
		"the binary is gone": func(t *testing.T, b *adoptedBoard) ([]string, func(string) string) {
			os.Remove(b.bin)
			return adoptedEnv, direct
		},
		"the config directory is gone": func(t *testing.T, b *adoptedBoard) ([]string, func(string) string) {
			if err := os.RemoveAll(b.configDir); err != nil {
				t.Fatal(err)
			}
			return adoptedEnv, direct
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			b := newAdoptedBoard(t)
			env, wrap := setup(t, &b)
			before := dirEntries(t, b.configDir)
			noise, each := b.fireAll(t, env, wrap)
			if noise != "" {
				t.Fatalf("the hooks printed %q", noise)
			}
			if _, err := os.Stat(b.calls); err == nil {
				t.Fatalf("the hooks ran the binary: %s", readFile(t, b.calls))
			}
			after := dirEntries(t, b.configDir)
			if want, ok := announces[name]; ok {
				kept := after[:0:0]
				for _, entry := range after {
					if entry != want {
						kept = append(kept, entry)
					}
				}
				if len(kept) != len(after)-1 {
					t.Fatalf("the pane was not announced as %s: %v", want, after)
				}
				after = kept
			}
			if strings.Join(after, ",") != strings.Join(before, ",") {
				t.Fatalf("the hooks wrote under the config dir: %v -> %v", before, after)
			}
			t.Logf("%v per hook (sh start included)", each)
		})
	}
}

// In an adopted pane, with the board up, each hook hands its event to the
// binary under the row's id, the board's home and the claude's pid, and
// prints nothing itself.
func TestGlobalHooksHandAnAdoptedPaneToTheBinary(t *testing.T) {
	b := newAdoptedBoard(t)
	noise, _ := b.fireAll(t, adoptedEnv, direct)
	if noise != "" {
		t.Fatalf("the hooks printed %q", noise)
	}
	log := readFile(t, b.calls)
	for _, event := range globalEvents {
		if !strings.Contains(log, "hook global "+event+" adopted1 "+b.configDir+" "+strconv.Itoa(os.Getpid())+"\n") {
			t.Fatalf("%s was not handed over as adopted1's claude on %s:\n%s", event, b.configDir, log)
		}
	}
	if n := strings.Count(log, "\n"); n != len(globalEvents) {
		t.Fatalf("%d binary runs for %d events:\n%s", n, len(globalEvents), log)
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

// A command an adopted claude runs speaks as the row its pane's marker names,
// and only when that claude is above it: the pane, the server and the agent
// all have to agree, as they do for the hook prelude.
func TestAdoptedCallerNamesTheMarkedRowForTheAgentsDescendants(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.SyncAdopted([]AdoptedPane{{ID: "a1b2c3d4", ServerPID: 4242, PaneID: "%7", AgentPID: 900}}); err != nil {
		t.Fatal(err)
	}
	under := func() []int { return []int{901, 900, 1} }
	cases := []struct {
		name, tmux, pane string
		ancestors        func() []int
		want             string
	}{
		{"agent's descendant", "/run/tmux-test/default,4242,0", "%7", under, "a1b2c3d4"},
		{"another claude in the pane", "/run/tmux-test/default,4242,0", "%7", func() []int { return []int{77, 1} }, ""},
		{"same pane id, another server", "/run/tmux-test/other,5151,0", "%7", under, ""},
		{"another pane", "/run/tmux-test/default,4242,0", "%8", under, ""},
		{"no server in $TMUX", "", "%7", under, ""},
		{"pane id that is a path", "/run/tmux-test/default,4242,0", "/../7", under, ""},
	}
	for _, c := range cases {
		got, ok := m.AdoptedCaller(c.tmux, c.pane, c.ancestors)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("%s: AdoptedCaller = %q, %v; want %q", c.name, got, ok, c.want)
		}
	}
}

// Every adopted agent's marker names its row and pid in the two fields a
// claude's always has, and an agent other than a hooks-driven claude adds its
// tool, one word whatever the tool is called, then the conversation its row
// is bound to when it has one that is a single word.
func TestSyncAdoptedMarksEveryAgentAndNamesTheOthersTool(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.SyncAdopted([]AdoptedPane{
		{ID: "c1", ServerPID: 10, PaneID: "%1", AgentPID: 101},
		{ID: "x1", ServerPID: 10, PaneID: "%2", AgentPID: 102, Tool: "codex"},
		{ID: "o1", ServerPID: 10, PaneID: "%3", AgentPID: 103, Tool: "opencode"},
		{ID: "w1", ServerPID: 10, PaneID: "%4", AgentPID: 104, Tool: "my tool"},
		{ID: "o2", ServerPID: 10, PaneID: "%5", AgentPID: 105, Tool: "opencode", Conversation: "ses_abc"},
		{ID: "c2", ServerPID: 10, PaneID: "%6", AgentPID: 106, Conversation: "4c1a0f6e-0000-4000-8000-000000000000"},
		{ID: "o3", ServerPID: 10, PaneID: "%8", AgentPID: 108, Tool: "opencode", Conversation: "two words"},
	}); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"10%1": "c1 101\n",
		"10%2": "x1 102 codex\n",
		"10%3": "o1 103 opencode\n",
		"10%4": "w1 104 agent\n",
		"10%5": "o2 105 opencode ses_abc\n",
		"10%6": "c2 106\n",
		"10%8": "o3 108 opencode\n",
	} {
		if got := readFile(t, filepath.Join(m.AdoptedDir(), name)); got != want {
			t.Errorf("marker %s = %q, want %q", name, got, want)
		}
	}
}

// Any agent's marker names the row for a command it runs, so a codex or
// opencode can use the board's CLI as itself; only a claude's names it for
// the MCP relay, which belongs to the claude in the pane and not to whatever
// agent it was started under.
func TestAdoptedClaudeCallerTakesOnlyAClaudesMarker(t *testing.T) {
	m := NewManager(t.TempDir())
	const tmuxEnv = "/run/tmux-test/default,4242,0"
	under := func() []int { return []int{901, 900, 1} }
	for _, c := range []struct {
		tool             string
		anyAgent, claude bool
	}{
		{"", true, true},
		{"codex", true, false},
		{"opencode", true, false},
	} {
		if err := m.SyncAdopted([]AdoptedPane{{ID: "a1b2c3d4", ServerPID: 4242, PaneID: "%7", AgentPID: 900, Tool: c.tool}}); err != nil {
			t.Fatal(err)
		}
		if id, ok := m.AdoptedCaller(tmuxEnv, "%7", under); ok != c.anyAgent || (ok && id != "a1b2c3d4") {
			t.Errorf("tool %q: AdoptedCaller = %q, %v; want %v", c.tool, id, ok, c.anyAgent)
		}
		if id, ok := m.AdoptedClaudeCaller(tmuxEnv, "%7", under); ok != c.claude || (ok && id != "a1b2c3d4") {
			t.Errorf("tool %q: AdoptedClaudeCaller = %q, %v; want %v", c.tool, id, ok, c.claude)
		}
		if _, ok := m.AdoptedClaudeCaller(tmuxEnv, "%7", func() []int { return []int{77, 1} }); ok {
			t.Errorf("tool %q: AdoptedClaudeCaller named the row for a process not under its agent", c.tool)
		}
	}
	// A fourth field is the agent's conversation, which changes nothing
	// here; a marker with more fields than that is nobody's.
	writeFile(t, filepath.Join(m.AdoptedDir(), "4242%7"), "a1b2c3d4 900 opencode ses_abc\n")
	if id, ok := m.AdoptedCaller(tmuxEnv, "%7", under); !ok || id != "a1b2c3d4" {
		t.Errorf("AdoptedCaller with a conversation field = %q, %v", id, ok)
	}
	if _, ok := m.AdoptedClaudeCaller(tmuxEnv, "%7", under); ok {
		t.Error("AdoptedClaudeCaller took an opencode marker with a conversation")
	}
	writeFile(t, filepath.Join(m.AdoptedDir(), "4242%7"), "a1b2c3d4 900 codex ses_abc extra\n")
	if _, ok := m.AdoptedCaller(tmuxEnv, "%7", under); ok {
		t.Error("AdoptedCaller read a five-field marker")
	}
}

// Reading the process chain is the expensive half, so a pane with no marker
// never asks for it.
func TestAdoptedCallerWalksNoProcessesWithoutAMarker(t *testing.T) {
	m := NewManager(t.TempDir())
	_, ok := m.AdoptedCaller("/run/tmux-test/default,4242,0", "%7", func() []int {
		t.Fatal("walked the process chain for a pane with no marker")
		return nil
	})
	if ok {
		t.Fatal("named a caller with no marker")
	}
}

type steeringNote struct {
	H struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

func parseSteering(t *testing.T, out string) steeringNote {
	t.Helper()
	var note steeringNote
	if err := json.Unmarshal([]byte(out), &note); err != nil {
		t.Fatalf("not hook JSON: %q (%v)", out, err)
	}
	return note
}

// The standing instructions reach an adopted claude once: on its first
// prompt, not its second, again for a new claude adopted into the row, and
// again on a SessionStart, which in a running session means its context
// was cleared, compacted or swapped.
func TestAdoptedSteeringIsSaidOncePerAdoption(t *testing.T) {
	m := NewManager(t.TempDir())
	text := func() string { return "steer" }
	say := func(event string, pid int) string { return m.AdoptedSteering(event, "adopted1", pid, text) }

	note := parseSteering(t, say("UserPromptSubmit", 900))
	if note.H.HookEventName != "UserPromptSubmit" || note.H.AdditionalContext != "steer" {
		t.Fatalf("first prompt = %+v", note.H)
	}
	if out := say("UserPromptSubmit", 900); out != "" {
		t.Fatalf("the second prompt was steered again: %q", out)
	}
	if out := say("UserPromptSubmit", 901); parseSteering(t, out).H.AdditionalContext != "steer" {
		t.Fatalf("a new claude in the row was not steered: %q", out)
	}
	if out := say("UserPromptSubmit", 901); out != "" {
		t.Fatalf("the new claude's second prompt was steered again: %q", out)
	}
	note = parseSteering(t, say("SessionStart", 901))
	if note.H.HookEventName != "SessionStart" || note.H.AdditionalContext != "steer" {
		t.Fatalf("SessionStart after a clear = %+v", note.H)
	}
	if out := say("UserPromptSubmit", 901); out != "" {
		t.Fatalf("the prompt after SessionStart was steered again: %q", out)
	}
	if out := m.AdoptedSteering("UserPromptSubmit", "other1", 900, text); out == "" {
		t.Fatal("another row shared adopted1's stamp")
	}
	for _, event := range []string{"PreToolUse", "Stop", "Notification"} {
		if out := m.AdoptedSteering(event, "fresh1", 900, text); out != "" {
			t.Fatalf("%s carried the steering: %q", event, out)
		}
	}
	if m.AdoptedSteering("UserPromptSubmit", "../x", 900, text) != "" || m.AdoptedSteering("UserPromptSubmit", "fresh2", 0, text) != "" {
		t.Fatal("steered a row that cannot be named or a claude with no pid")
	}
	if m.AdoptedSteering("UserPromptSubmit", "fresh3", 900, func() string { return " " }) != "" {
		t.Fatal("an empty text was said")
	}
	if out := m.AdoptedSteering("UserPromptSubmit", "fresh3", 900, text); out == "" {
		t.Fatal("an empty text used up the adoption's one telling")
	}
}

// The steering joins whatever the launch's own prompt hook said, both notes
// in one additionalContext, rather than one replacing the other.
func TestAdoptedSteeringMergesWithTheLaunchNote(t *testing.T) {
	m := NewManager(t.TempDir())
	launch := `{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":"sealed by parent"}}`
	merged := MergeHookOutputs(launch, m.AdoptedSteering("UserPromptSubmit", "adopted1", 900, func() string { return "steer" }))
	note := parseSteering(t, merged)
	if note.H.HookEventName != "UserPromptSubmit" || note.H.AdditionalContext != "sealed by parent\n\nsteer" {
		t.Fatalf("merged = %+v", note.H)
	}
	if MergeHookOutputs(launch, "") != launch || MergeHookOutputs("", "  ") != "" {
		t.Fatal("an empty output was not dropped from the merge")
	}
}

// A stamp goes when its claude does, and stays while it runs, whatever the
// board holds.
func TestSyncAdoptedPrunesTheStampsOfExitedClaudes(t *testing.T) {
	m := NewManager(t.TempDir())
	text := func() string { return "steer" }
	m.AdoptedSteering("UserPromptSubmit", "live1", os.Getpid(), text)
	m.AdoptedSteering("UserPromptSubmit", "gone1", deadPID(t), text)
	if err := m.SyncAdopted(nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(dirEntries(t, filepath.Join(m.Dir(), adoptedSteeringDirName)), ","); got != "/live1" {
		t.Fatalf("stamps = %s", got)
	}
	if out := m.AdoptedSteering("UserPromptSubmit", "live1", os.Getpid(), text); out != "" {
		t.Fatalf("a running claude was steered again after the board let its row go: %q", out)
	}
}
