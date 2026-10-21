package hooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/usestring/gate-inbox/internal/singleton"
)

// The command's bytes are what codex's trust record hashes: a change here
// makes every user review the hooks again. This pins them, so a change is a
// decision rather than an accident.
func TestCodexCommandIsByteStable(t *testing.T) {
	got := codexCommand("/srv/gi", "/srv/gi/bin/gate-inbox", "UserPromptSubmit")
	want := `: gate-inbox-codex-hook '/srv/gi'; exec 2>/dev/null; ` +
		`[ -f '/srv/gi/hooks/codex/watch' ] || exit 0; ` +
		`read -r b < '/srv/gi/manager.lock'; [ -n "$b" ] && kill -0 "$b" || exit 0; ` +
		`[ -x '/srv/gi/bin/gate-inbox' ] || exit 0; ` +
		`GATE_INBOX_HOME='/srv/gi' '/srv/gi/bin/gate-inbox' hook codex UserPromptSubmit; exit 0`
	if got != want {
		t.Fatalf("codex hook command changed; every user would be asked to trust it again.\n got %s\nwant %s", got, want)
	}
}

const userCodexConfig = `# my codex config
model = "gpt-6-luna"   # trailing comment

[projects."/work"]
trust_level = "trusted"

# my own hook
[[hooks.Stop]]
[[hooks.Stop.hooks]]
type = "command"
command = "notify-send done"

[tui]
# keep this
`

func writeCodexConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	writeFile(t, path, content)
	return path
}

func codexEntries(t *testing.T, raw string) map[string][]string {
	t.Helper()
	var cfg struct {
		Hooks map[string]any `toml:"hooks"`
	}
	if _, err := toml.Decode(raw, &cfg); err != nil {
		t.Fatalf("config is not TOML: %v\n%s", err, raw)
	}
	out := map[string][]string{}
	for event, value := range cfg.Hooks {
		groups, ok := value.([]map[string]any)
		if !ok {
			continue
		}
		for _, g := range groups {
			handlers, _ := g["hooks"].([]map[string]any)
			for _, h := range handlers {
				command, _ := h["command"].(string)
				out[event] = append(out[event], command)
			}
		}
	}
	return out
}

// Registering keeps every byte of the operator's file, adds one entry per
// event after their own, and a second pass writes nothing; removing gives
// back the file exactly as it was.
func TestRegisterCodexKeepsTheOperatorsFile(t *testing.T) {
	path := writeCodexConfig(t, userCodexConfig)
	changed, err := RegisterCodex(path, "/cfg", "/cfg/bin/gate-inbox")
	if err != nil || !changed {
		t.Fatalf("register: changed=%v err=%v", changed, err)
	}
	raw := readFile(t, path)
	if !strings.HasPrefix(raw, userCodexConfig) {
		t.Fatalf("the operator's bytes were not kept:\n%s", raw)
	}
	entries := codexEntries(t, raw)
	if len(entries["Stop"]) != 2 || entries["Stop"][0] != "notify-send done" || !strings.HasPrefix(entries["Stop"][1], codexMark("/cfg")) {
		t.Fatalf("Stop entries = %q", entries["Stop"])
	}
	if len(entries["UserPromptSubmit"]) != 1 || entries["UserPromptSubmit"][0] != codexCommand("/cfg", "/cfg/bin/gate-inbox", "UserPromptSubmit") {
		t.Fatalf("UserPromptSubmit entries = %q", entries["UserPromptSubmit"])
	}
	if ok, err := CodexRegistered(path, "/cfg", "/cfg/bin/gate-inbox"); err != nil || !ok {
		t.Fatalf("registered = %v, %v", ok, err)
	}
	info, _ := os.Stat(path)
	if changed, err := RegisterCodex(path, "/cfg", "/cfg/bin/gate-inbox"); err != nil || changed {
		t.Fatalf("second register: changed=%v err=%v", changed, err)
	}
	if again, _ := os.Stat(path); !os.SameFile(info, again) {
		t.Fatal("a register with nothing to change rewrote the file")
	}
	if changed, err := UnregisterCodex(path, "/cfg"); err != nil || !changed {
		t.Fatalf("unregister: changed=%v err=%v", changed, err)
	}
	if got := readFile(t, path); got != userCodexConfig {
		t.Fatalf("unregister did not restore the file:\n%s", got)
	}
}

// Codex writes its trust records for our entries under [hooks.state] right
// after them, and codex's own edits keep comments. Those records must leave
// the entries counted as in place -- rewriting them would move them and ask
// for trust again -- and survive their removal.
func TestRegisterCodexLeavesCodexTrustRecordsAlone(t *testing.T) {
	path := writeCodexConfig(t, userCodexConfig)
	if _, err := RegisterCodex(path, "/cfg", "/cfg/bin/gate-inbox"); err != nil {
		t.Fatal(err)
	}
	trusted := strings.Replace(readFile(t, path), "timeout = 30\n", "timeout = 30\n\n[hooks.state]\n\n"+
		`[hooks.state."/c/config.toml:stop:1:0"]`+"\n"+`trusted_hash = "sha256:00"`+"\n", 1)
	trusted += "\n[hooks.state.\"/c/config.toml:user_prompt_submit:0:0\"]\ntrusted_hash = \"sha256:11\"\n"
	writeFile(t, path, trusted)
	if changed, err := RegisterCodex(path, "/cfg", "/cfg/bin/gate-inbox"); err != nil || changed {
		t.Fatalf("register over trust records: changed=%v err=%v\n%s", changed, err, readFile(t, path))
	}
	if _, err := UnregisterCodex(path, "/cfg"); err != nil {
		t.Fatal(err)
	}
	raw := readFile(t, path)
	if strings.Contains(raw, codexTag) || !strings.Contains(raw, `trusted_hash = "sha256:00"`) || !strings.Contains(raw, `trusted_hash = "sha256:11"`) ||
		!strings.HasPrefix(raw, userCodexConfig) {
		t.Fatalf("after unregister:\n%s", raw)
	}
	codexEntries(t, raw)
}

// A moved binary has the entries rewritten; another board's entries are
// never touched; --all removes every board's.
func TestRegisterCodexReplacesOnlyItsOwnBoard(t *testing.T) {
	path := writeCodexConfig(t, userCodexConfig)
	if _, err := RegisterCodex(path, "/other", "/other/bin/gate-inbox"); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterCodex(path, "/cfg", "/old/gate-inbox"); err != nil {
		t.Fatal(err)
	}
	if changed, err := RegisterCodex(path, "/cfg", "/cfg/bin/gate-inbox"); err != nil || !changed {
		t.Fatalf("moved binary: changed=%v err=%v", changed, err)
	}
	raw := readFile(t, path)
	if strings.Contains(raw, "/old/gate-inbox") || strings.Count(raw, codexMark("/other")) != 2 || strings.Count(raw, codexMark("/cfg")) != 2 {
		t.Fatalf("after the move:\n%s", raw)
	}
	if _, err := UnregisterCodex(path, ""); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != userCodexConfig {
		t.Fatalf("--all left:\n%s", got)
	}
}

// A file that is not TOML, or one whose hooks the entries cannot join, is
// refused and left exactly as it was. A missing file gets just the entries.
func TestRegisterCodexRefusesWhatItCannotMerge(t *testing.T) {
	for name, content := range map[string]string{
		"malformed":    "model = \n[[",
		"inline hooks": "[hooks]\nStop = [{ hooks = [{ type = \"command\", command = \"x\" }] }]\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := writeCodexConfig(t, content)
			if _, err := RegisterCodex(path, "/cfg", "/cfg/bin/gate-inbox"); err == nil {
				t.Fatal("merged")
			}
			if got := readFile(t, path); got != content {
				t.Fatalf("the file changed:\n%s", got)
			}
		})
	}
	path := filepath.Join(t.TempDir(), "codex", "config.toml")
	if changed, err := UnregisterCodex(path, "/cfg"); err != nil || changed {
		t.Fatalf("unregister on no file: %v %v", changed, err)
	}
	if _, err := RegisterCodex(path, "/cfg", "/cfg/bin/gate-inbox"); err != nil {
		t.Fatal(err)
	}
	if entries := codexEntries(t, readFile(t, path)); len(entries) != 2 {
		t.Fatalf("entries in a new file = %v", entries)
	}
}

// The prelude is silent and runs nothing until the board holds a codex row,
// its lock names a live process and the binary is there; then it hands the
// payload to `hook codex <event>` with the board's home, and still prints
// only what that prints.
func TestCodexPreludeStaysSilentUntilTheBoardWatches(t *testing.T) {
	configDir := t.TempDir()
	dir := t.TempDir()
	bin, calls := filepath.Join(dir, "gate-inbox"), filepath.Join(dir, "calls")
	writeFile(t, bin, "#!/bin/sh\npayload=$(cat)\necho \"$* $GATE_INBOX_HOME $payload\" >> '"+calls+"'\n")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	fire := func() string {
		t.Helper()
		cmd := exec.Command("/bin/sh", "-c", codexCommand(configDir, bin, "Stop"))
		cmd.Env = []string{"PATH=/usr/bin:/bin", "GATE_INBOX_SESSION_ID=daemons", "TMUX_PANE=%0"}
		cmd.Stdin = strings.NewReader(`{"session_id":"x"}`)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("hook exited %v: %s", err, out)
		}
		return string(out)
	}
	steps := []struct {
		name  string
		setup func()
	}{
		{"no watch file", func() {}},
		{"no board lock", func() {
			os.MkdirAll(filepath.Dir(CodexWatchFile(configDir)), 0o755)
			writeFile(t, CodexWatchFile(configDir), "")
		}},
		{"a dead board", func() { writeFile(t, filepath.Join(configDir, singleton.FileName), strconv.Itoa(deadPID(t))) }},
		{"no binary", func() {
			writeFile(t, filepath.Join(configDir, singleton.FileName), strconv.Itoa(os.Getpid()))
			os.Chmod(bin, 0o644)
		}},
	}
	for _, step := range steps {
		step.setup()
		if out := fire(); out != "" {
			t.Fatalf("%s: printed %q", step.name, out)
		}
		if _, err := os.Stat(calls); err == nil {
			t.Fatalf("%s: the binary ran", step.name)
		}
	}
	os.Chmod(bin, 0o755)
	if out := fire(); out != "" {
		t.Fatalf("printed %q", out)
	}
	if got := strings.TrimSpace(readFile(t, calls)); got != "hook codex Stop "+configDir+` {"session_id":"x"}` {
		t.Fatalf("binary called with %q", got)
	}
}
