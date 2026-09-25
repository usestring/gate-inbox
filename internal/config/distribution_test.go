package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// toolField finds the Tool field a tools.<name>.<key> entry names, so an entry
// whose key was renamed or misspelt fails here instead of guarding nothing.
func toolField(t *testing.T, tool Tool, key string) reflect.Value {
	t.Helper()
	v := reflect.ValueOf(tool)
	for i := 0; i < v.NumField(); i++ {
		if v.Type().Field(i).Tag.Get("toml") == key {
			return v.Field(i)
		}
	}
	t.Fatalf("tools.*.%s is not a tool setting", key)
	return reflect.Value{}
}

// Every tool setting a distribution supplies ships empty in the core: in the
// built-in blocks, in what backfill hands an older config, and in the file a
// first run writes.
func TestTheCoreShipsNoSuppliedToolSetting(t *testing.T) {
	builtin, err := Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}
	loaded, err := LoadDir(t.TempDir())
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	checked := 0
	for _, s := range DistributionSupplied {
		rest, ok := strings.CutPrefix(s.Key, "tools.")
		if s.Env || !ok {
			continue
		}
		name, key, _ := strings.Cut(rest, ".")
		for label, cfg := range map[string]Config{"built-in": builtin, "first run": loaded} {
			if got := toolField(t, cfg.Tools[name], key); !got.IsZero() {
				t.Errorf("%s %s = %v; the core must leave it for a distribution to supply", label, s.Key, got)
			}
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no tool setting is listed, so nothing was checked")
	}
}

// The first-run config is text an operator reads, so it must not name a
// distribution's store, project or credential even in a comment.
func TestTheWrittenDefaultNamesNoDistributionStore(t *testing.T) {
	for _, marker := range []string{"gcloud", "--project=", "secrets versions access"} {
		if strings.Contains(defaultConfig, marker) {
			t.Errorf("the default config names %q", marker)
		}
	}
}

func TestEverySuppliedSettingSaysWhatHappensWithoutIt(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range DistributionSupplied {
		if s.Key == "" || strings.TrimSpace(s.Without) == "" {
			t.Errorf("%+v: a supplied setting needs a key and what the board does without it", s)
		}
		if seen[s.Key] {
			t.Errorf("%s is listed twice", s.Key)
		}
		seen[s.Key] = true
	}
}

// The effective account settings for the three shapes of config.toml a board
// meets. An absent or partial block gets nothing filled in; a value the file
// carries -- whether an older release wrote it there or the operator did -- is
// kept exactly; and loading never rewrites a file that exists.
func TestAccountSettingsComeOnlyFromTheFile(t *testing.T) {
	type accountSettings struct{ env, secret, command, list string }
	named := accountSettings{"CLAUDE_CODE_OAUTH_TOKEN", "TOKEN_{account}", "secret-tool lookup name {secret}", "secret-tool search --all name"}
	block := `[tools.claude]
command = "claude"
account_env = "` + named.env + `"
account_secret = "` + named.secret + `"
account_command = "` + named.command + `"
accounts_command = "` + named.list + `"
`
	cases := map[string]struct {
		file string // "" means no config.toml at all
		want accountSettings
	}{
		"absent":   {"", accountSettings{}},
		"partial":  {"[tools.claude]\ncommand = \"claude\"\n", accountSettings{}},
		"old":      {"poll_interval = \"2s\"\n\n" + block, named},
		"explicit": {block + "\n[tools.codex]\ncommand = \"codex\"\n", named},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")
			if tc.file != "" {
				if err := os.WriteFile(path, []byte(tc.file), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			cfg, err := LoadDir(dir)
			if err != nil {
				t.Fatalf("LoadDir: %v", err)
			}
			claude := cfg.Tools["claude"]
			got := accountSettings{claude.AccountEnv, claude.AccountSecret, claude.AccountCommand, claude.AccountsCommand}
			if got != tc.want {
				t.Fatalf("account settings = %+v, want %+v", got, tc.want)
			}
			if tc.file == "" {
				return
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != tc.file {
				t.Fatalf("loading rewrote config.toml:\n%s", after)
			}
		})
	}
}
