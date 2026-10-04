package hooks

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/parentseal"
)

type denialLists struct {
	Permissions struct {
		Allow []string `json:"allow"`
		Deny  []string `json:"deny"`
	} `json:"permissions"`
	Sandbox struct {
		Enabled    bool `json:"enabled"`
		Filesystem struct {
			DenyRead []string `json:"denyRead"`
		} `json:"filesystem"`
	} `json:"sandbox"`
}

func parseDenials(t *testing.T, raw string) denialLists {
	t.Helper()
	var d denialLists
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatalf("settings are not JSON: %v\n%s", err, raw)
	}
	return d
}

func keyDirRules(configDir string) []string {
	keyDir := parentseal.KeyDir(configDir)
	return []string{"Read(/" + keyDir + "/**)", "Edit(/" + keyDir + "/**)"}
}

// The user's settings carry the denials a launch puts on its command line, so
// a claude started anywhere loads them, and registering twice changes nothing.
func TestRegisterGlobalDeniesTheKeyDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	writeFile(t, path, operatorSettings)
	configDir := t.TempDir()
	if _, err := RegisterGlobal(path, configDir, "/bin/gate-inbox"); err != nil {
		t.Fatal(err)
	}
	first := readFile(t, path)
	d := parseDenials(t, first)
	if !slices.Equal(d.Permissions.Deny, keyDirRules(configDir)) {
		t.Fatalf("permissions.deny = %v, want %v", d.Permissions.Deny, keyDirRules(configDir))
	}
	if !slices.Equal(d.Sandbox.Filesystem.DenyRead, []string{parentseal.KeyDir(configDir)}) {
		t.Fatalf("sandbox.filesystem.denyRead = %v", d.Sandbox.Filesystem.DenyRead)
	}
	if changed, err := RegisterGlobal(path, configDir, "/bin/gate-inbox"); err != nil || changed {
		t.Fatalf("second register: changed=%v err=%v", changed, err)
	}
	if ok, err := GlobalRegistered(path, configDir, "/bin/gate-inbox"); err != nil || !ok {
		t.Fatalf("GlobalRegistered = %v, %v", ok, err)
	}

	// An operator who deletes one by hand has it put back on the next pass,
	// as a deleted hook is.
	writeFile(t, path, strings.Replace(first, `"`+keyDirRules(configDir)[1]+`"`, `"Bash(rm:*)"`, 1))
	if ok, _ := GlobalRegistered(path, configDir, "/bin/gate-inbox"); ok {
		t.Fatal("GlobalRegistered missed a deleted denial")
	}
	if changed, err := RegisterGlobal(path, configDir, "/bin/gate-inbox"); err != nil || !changed {
		t.Fatalf("repair: changed=%v err=%v", changed, err)
	}
	d = parseDenials(t, readFile(t, path))
	if !slices.Equal(d.Permissions.Deny, []string{keyDirRules(configDir)[0], "Bash(rm:*)", keyDirRules(configDir)[1]}) {
		t.Fatalf("after repair permissions.deny = %v", d.Permissions.Deny)
	}
}

// Registering and removing gives back the operator's file byte for byte,
// whether the lists were there, empty, or missing with or without the object
// above them, in any layout.
func TestGlobalDenialsRoundTripTheOperatorsBytes(t *testing.T) {
	for name, original := range map[string]string{
		"own deny list": `{
  "permissions": {
    "allow": [
      "Bash(ls:*)"
    ],
    "deny": [
      "Bash(rm -rf:*)"
    ]
  },
  "sandbox": {
    "enabled": true,
    "filesystem": {
      "denyRead": [
        "~/.ssh"
      ]
    }
  }
}
`,
		"empty lists":           "{\n  \"permissions\": {\n    \"deny\": []\n  },\n  \"sandbox\": {\n    \"filesystem\": {\n      \"denyRead\": []\n    }\n  }\n}\n",
		"objects with no lists": "{\n  \"permissions\": {\n    \"allow\": [\"Bash(ls)\"]\n  },\n  \"sandbox\": {\n    \"enabled\": true\n  }\n}\n",
		"empty objects":         "{\n  \"permissions\": {},\n  \"sandbox\": {}\n}\n",
		"inline lists":          `{"permissions": {"deny": ["Bash(rm:*)"]}, "sandbox": {"filesystem": {"denyRead": ["/x", "/y"]}}}`,
		"tabs":                  "{\n\t\"permissions\": {\n\t\t\"deny\": [\n\t\t\t\"Bash(rm:*)\"\n\t\t]\n\t}\n}\n",
		"odd layout":            oddSettings,
		"own hooks":             operatorSettings,
		"empty object":          "{}\n",
		"compact":               `{"a":1,"b":[1,2]}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			writeFile(t, path, original)
			configDir := t.TempDir()
			if _, err := RegisterGlobal(path, configDir, "/bin/gate-inbox"); err != nil {
				t.Fatal(err)
			}
			registered := readFile(t, path)
			d := parseDenials(t, registered)
			for _, rule := range keyDirRules(configDir) {
				if !slices.Contains(d.Permissions.Deny, rule) {
					t.Fatalf("permissions.deny lacks %s:\n%s", rule, registered)
				}
			}
			if !slices.Contains(d.Sandbox.Filesystem.DenyRead, parentseal.KeyDir(configDir)) {
				t.Fatalf("denyRead lacks the key dir:\n%s", registered)
			}
			before := parseDenials(t, original)
			if len(before.Permissions.Deny) > 0 && d.Permissions.Deny[0] != before.Permissions.Deny[0] {
				t.Fatalf("the operator's deny rule is no longer first: %v", d.Permissions.Deny)
			}
			if before.Sandbox.Enabled != d.Sandbox.Enabled || !slices.Equal(before.Permissions.Allow, d.Permissions.Allow) {
				t.Fatalf("the operator's other settings changed:\n%s", registered)
			}
			if ok, err := GlobalRegistered(path, configDir, "/bin/gate-inbox"); err != nil || !ok {
				t.Fatalf("GlobalRegistered = %v, %v\n%s", ok, err, registered)
			}
			if _, err := UnregisterGlobal(path, configDir); err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, path); got != original {
				t.Fatalf("after unregister:\n%q\nwant\n%q\nregistered was\n%s", got, original, registered)
			}
			if _, err := os.Stat(filepath.Join(configDir, "hooks", createdName)); err == nil {
				t.Fatal("the record of created lists outlived the unregister")
			}
		})
	}
}

// A rule the operator adds to a list the board created keeps the list.
func TestUnregisterGlobalKeepsAListTheOperatorAddedTo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	writeFile(t, path, "{}\n")
	configDir := t.TempDir()
	if _, err := RegisterGlobal(path, configDir, "/bin/gate-inbox"); err != nil {
		t.Fatal(err)
	}
	raw, _, err := addToList([]byte(readFile(t, path)), denyPath, []string{"Bash(rm:*)"})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, string(raw))
	if _, err := UnregisterGlobal(path, configDir); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)
	d := parseDenials(t, got)
	if !slices.Equal(d.Permissions.Deny, []string{"Bash(rm:*)"}) || strings.Contains(got, "sandbox") {
		t.Fatalf("after unregister:\n%s", got)
	}
}

// Two boards share the lists; each takes out only its own entries, and the
// last one out takes the lists the first one created.
func TestGlobalDenialsScopeToTheirBoard(t *testing.T) {
	for _, all := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "settings.json")
		writeFile(t, path, operatorSettings)
		first, second := t.TempDir(), t.TempDir()
		for _, dir := range []string{first, second} {
			if _, err := RegisterGlobal(path, dir, "/bin/gate-inbox"); err != nil {
				t.Fatal(err)
			}
		}
		if all {
			if _, err := UnregisterGlobal(path, ""); err != nil {
				t.Fatal(err)
			}
		} else {
			if _, err := UnregisterGlobal(path, first); err != nil {
				t.Fatal(err)
			}
			d := parseDenials(t, readFile(t, path))
			if !slices.Equal(d.Permissions.Deny, keyDirRules(second)) {
				t.Fatalf("after removing the first board, deny = %v", d.Permissions.Deny)
			}
			if _, err := UnregisterGlobal(path, second); err != nil {
				t.Fatal(err)
			}
		}
		if got := readFile(t, path); got != operatorSettings {
			t.Fatalf("all=%v: the file is not the operator's:\n%s", all, got)
		}
	}
}

func TestRegisterGlobalRefusesADenyThatIsNotAList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	odd := `{"permissions": {"deny": "Bash(rm:*)"}}`
	writeFile(t, path, odd)
	if _, err := RegisterGlobal(path, t.TempDir(), "/bin/gate-inbox"); err == nil || !strings.Contains(err.Error(), "permissions.deny") {
		t.Fatalf("register over a deny that is a string: %v", err)
	}
	if got := readFile(t, path); got != odd {
		t.Fatalf("the file was rewritten to %s", got)
	}
}

func TestShellUnquoteReadsShellQuote(t *testing.T) {
	for _, s := range []string{"/plain", "/with space", "/it's", "''", "/a'b'c"} {
		got, ok := shellUnquote(shellQuote(s) + "; rest")
		if !ok || got != s {
			t.Fatalf("shellUnquote(shellQuote(%q)) = %q, %v", s, got, ok)
		}
	}
}

func decision(t *testing.T, out string) (string, string) {
	t.Helper()
	if out == "" {
		return "", ""
	}
	var parsed struct {
		H struct {
			Event    string `json:"hookEventName"`
			Decision string `json:"permissionDecision"`
			Reason   string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("hook output is not JSON: %v: %s", err, out)
	}
	if parsed.H.Event != "PreToolUse" {
		t.Fatalf("hook output for %s: %s", parsed.H.Event, out)
	}
	return parsed.H.Decision, parsed.H.Reason
}

// An adopted claude that started before the denials were registered is
// refused any tool call that names the key directory, however it spells it.
func TestKeyDirDecisionRefusesCallsNamingTheKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configDir := filepath.Join(home, ".config", "gate-inbox")
	keyDir := parentseal.KeyDir(configDir)
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	m := NewManager(configDir)
	call := func(tool string, input map[string]any, cwd string) string {
		payload, _ := json.Marshal(map[string]any{"tool_name": tool, "tool_input": input, "cwd": cwd})
		got, reason := decision(t, m.KeyDirDecision(payload))
		if got == "deny" && !strings.Contains(reason, keyDir) {
			t.Fatalf("the refusal does not name the directory: %s", reason)
		}
		return got
	}
	for name, c := range map[string]struct {
		tool  string
		input map[string]any
		cwd   string
	}{
		"Read a key":              {"Read", map[string]any{"file_path": filepath.Join(keyDir, "s1")}, ""},
		"Edit a key":              {"Edit", map[string]any{"file_path": filepath.Join(keyDir, "s1"), "old_string": "a", "new_string": "b"}, ""},
		"Write a key":             {"Write", map[string]any{"file_path": filepath.Join(keyDir, "s1"), "content": "x"}, ""},
		"Read by a relative path": {"Read", map[string]any{"file_path": "channel-keys/s1"}, configDir},
		"Read through ..":         {"Read", map[string]any{"file_path": filepath.Join(configDir, "hooks", "..", "channel-keys", "s1")}, ""},
		"Read by ~":               {"Read", map[string]any{"file_path": "~/.config/gate-inbox/channel-keys/s1"}, ""},
		"Bash cat":                {"Bash", map[string]any{"command": "cat " + keyDir + "/s1"}, ""},
		"Bash with ~":             {"Bash", map[string]any{"command": "ls ~/.config/gate-inbox/channel-keys"}, ""},
		"Bash with $HOME":         {"Bash", map[string]any{"command": "xxd \"$HOME/.config/gate-inbox/channel-keys/s1\""}, ""},
		"Grep the board home":     {"Grep", map[string]any{"pattern": "x", "path": configDir}, ""},
		"Glob the key dir":        {"Glob", map[string]any{"pattern": keyDir + "/*"}, ""},
	} {
		if got := call(c.tool, c.input, c.cwd); got != "deny" {
			t.Errorf("%s: decision %q, want deny", name, got)
		}
	}
	for name, c := range map[string]struct {
		tool  string
		input map[string]any
		cwd   string
	}{
		"Read elsewhere":               {"Read", map[string]any{"file_path": filepath.Join(configDir, "config.toml")}, ""},
		"Bash elsewhere":               {"Bash", map[string]any{"command": "ls " + configDir}, ""},
		"a sibling with a longer name": {"Read", map[string]any{"file_path": keyDir + "-old/s1"}, ""},
		"Grep wider than the home":     {"Grep", map[string]any{"pattern": "x", "path": home}, ""},
		"Write content naming it":      {"Write", map[string]any{"file_path": filepath.Join(home, "notes.md"), "content": keyDir}, ""},
	} {
		if got := call(c.tool, c.input, c.cwd); got != "" {
			t.Errorf("%s: decision %q, want none", name, got)
		}
	}
}

// The dispatch an adopted pane's PreToolUse runs refuses a call naming the
// keys and still reports the status the launch hooks would.
func TestDispatchGlobalRefusesTheKeyDir(t *testing.T) {
	configDir := t.TempDir()
	m := NewManager(configDir)
	t.Setenv(EnvSessionID, "adopted1")
	t.Setenv(EnvStatusFile, m.StatusFile("adopted1"))
	t.Setenv(EnvExecutable, filepath.Join(t.TempDir(), "absent"))
	if err := os.MkdirAll(m.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"tool_name": "Read", "tool_input": map[string]any{"file_path": filepath.Join(parentseal.KeyDir(configDir), "s1")}})
	if got, _ := decision(t, m.DispatchGlobal("PreToolUse", payload)); got != "deny" {
		t.Fatalf("PreToolUse on a key = %q, want deny", got)
	}
	if events, _, _ := m.Events("adopted1", 0); len(events) == 0 || events[len(events)-1].Name != "PreToolUse" {
		t.Fatalf("the status was not reported: %v", events)
	}
	if out := m.DispatchGlobal("PostToolUse", payload); out != "" {
		t.Fatalf("PostToolUse printed %q", out)
	}
	if out := m.DispatchGlobal("PreToolUse", []byte(`{"tool_name":"Read","tool_input":{"file_path":"/etc/hosts"}}`)); out != "" {
		t.Fatalf("an unrelated read printed %q", out)
	}
}

// A session nobody adopted runs none of it: the registered PreToolUse
// command stops in its prelude, so a call naming the keys meets only the
// settings' own denials.
func TestGlobalKeyDirGuardIsSilentOutsideAnAdoptedPane(t *testing.T) {
	b := newAdoptedBoard(t)
	command := b.settings.Hooks["PreToolUse"][0].Hooks[0].Command
	payload, _ := json.Marshal(map[string]any{"tool_name": "Read", "tool_input": map[string]any{"file_path": filepath.Join(parentseal.KeyDir(b.configDir), "s1")}})
	for name, env := range map[string][]string{
		"no tmux":               {"PATH=/usr/bin:/bin"},
		"a pane nobody adopted": {"PATH=/usr/bin:/bin", "TMUX=sock,4242,0", "TMUX_PANE=%8"},
		"a launched session":    append(adoptedEnv, EnvStatusFile+"="+filepath.Join(t.TempDir(), "launched.status")),
	} {
		out := runHook(t, command, env, payload)
		if out != "" {
			t.Fatalf("%s: the hook printed %q", name, out)
		}
		if _, err := os.Stat(b.calls); err == nil {
			t.Fatalf("%s: the hook ran the binary: %s", name, readFile(t, b.calls))
		}
	}
	runHook(t, command, adoptedEnv, payload)
	if !strings.Contains(readFile(t, b.calls), "hook global PreToolUse adopted1") {
		t.Fatal("the adopted pane's PreToolUse did not reach the binary")
	}
}

func runHook(t *testing.T, command string, env []string, payload []byte) string {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(payload)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the hook exited %v: %s", err, out)
	}
	return string(out)
}
