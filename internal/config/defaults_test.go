package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func useDefaults(t *testing.T, text string) {
	t.Helper()
	restore, err := UseDefaults(text)
	if err != nil {
		t.Fatalf("UseDefaults: %v", err)
	}
	t.Cleanup(restore)
}

func loadFile(t *testing.T, file string) Config {
	t.Helper()
	dir := t.TempDir()
	if file != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(file), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	return cfg
}

// The operator's file wins key by key, the defaults fill what it leaves out,
// and the built-in blocks fill what neither sets.
func TestDefaultsLieUnderTheOperatorsFile(t *testing.T) {
	useDefaults(t, `poll_interval = "5s"
editor = "distribution-editor"
adopt_sockets = ["from-defaults"]

[tools.claude]
account_env = "DIST_TOKEN"
account_secret = "DIST_{account}"
accounts_command = "list-dist"

[tools.extra]
command = "extra-cli"

[extensions.sample]
base_url = "https://dist.example"
key_command = "dist-key"
`)
	cfg := loadFile(t, `editor = "mine"
adopt_sockets = ["mine"]

[tools.claude]
command = "claude --mine"
accounts_command = ""

[extensions.sample]
key_command = "my-key"
`)
	if cfg.PollInterval.Duration != 5*time.Second {
		t.Errorf("poll_interval = %v, want the defaults' 5s", cfg.PollInterval.Duration)
	}
	if cfg.Editor != "mine" {
		t.Errorf("editor = %q, want the operator's", cfg.Editor)
	}
	if len(cfg.AdoptSockets) != 1 || cfg.AdoptSockets[0] != "mine" {
		t.Errorf("adopt_sockets = %v, want the operator's array whole", cfg.AdoptSockets)
	}
	claude := cfg.Tools["claude"]
	if claude.Command != "claude --mine" || claude.AccountEnv != "DIST_TOKEN" || claude.AccountSecret != "DIST_{account}" {
		t.Errorf("claude = %+v, want the operator's command over the defaults' account settings", claude)
	}
	if claude.AccountsCommand != "" {
		t.Errorf("accounts_command = %q; an operator's empty value turns a default off", claude.AccountsCommand)
	}
	if claude.ModelFlag != "--model" || claude.SessionIDFlag != "--session-id" {
		t.Errorf("claude lost its built-in settings: model_flag %q, session_id_flag %q", claude.ModelFlag, claude.SessionIDFlag)
	}
	if cfg.Tools["extra"].Command != "extra-cli" {
		t.Errorf("tools.extra = %+v, want the block only the defaults define", cfg.Tools["extra"])
	}
	if _, ok := cfg.Tools["codex"]; !ok {
		t.Error("a built-in tool neither layer names is missing")
	}
	sample := cfg.Extensions["sample"]
	if sample["base_url"] != "https://dist.example" || sample["key_command"] != "my-key" {
		t.Errorf("extensions.sample = %v, want the section merged key by key", sample)
	}
}

// A first run writes the core's own file, never the defaults, and loading
// with defaults never rewrites a file that exists.
func TestDefaultsAreNeverWrittenToTheOperatorsFile(t *testing.T) {
	useDefaults(t, "[tools.claude]\naccount_env = \"DIST_TOKEN\"\n")
	dir := t.TempDir()
	cfg, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if cfg.Tools["claude"].AccountEnv != "DIST_TOKEN" {
		t.Fatalf("a first run lost the defaults: %+v", cfg.Tools["claude"])
	}
	written, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != defaultConfig {
		t.Fatal("the first-run file is not the core's default")
	}
	if _, err := LoadDir(dir); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
	if string(again) != defaultConfig {
		t.Fatal("loading rewrote config.toml")
	}
}

// Every config key a distribution is meant to supply can be supplied from
// the defaults, and reaches the loaded config.
func TestDefaultsCanSupplyEveryDistributionKey(t *testing.T) {
	type want struct{ section, key, value string }
	var wants []want
	sections := map[string][]string{}
	var order []string
	for _, s := range DistributionSupplied {
		if s.Env || s.Extension {
			continue
		}
		dot := strings.LastIndex(s.Key, ".")
		section, key := s.Key[:dot], s.Key[dot+1:]
		value := "supplied-" + key
		if _, seen := sections[section]; !seen {
			order = append(order, section)
		}
		sections[section] = append(sections[section], key+" = \""+value+"\"")
		wants = append(wants, want{section, key, value})
	}
	if len(wants) == 0 {
		t.Fatal("no config key is listed, so nothing was checked")
	}
	var b strings.Builder
	for _, section := range order {
		b.WriteString("[" + section + "]\n" + strings.Join(sections[section], "\n") + "\n\n")
	}
	useDefaults(t, b.String())
	cfg := loadFile(t, "")
	for _, w := range wants {
		var got any
		switch name, ok := strings.CutPrefix(w.section, "tools."); {
		case ok:
			got = toolField(t, cfg.Tools[name], w.key).Interface()
		case strings.HasPrefix(w.section, "extensions."):
			got = cfg.Extensions[strings.TrimPrefix(w.section, "extensions.")][w.key]
		default:
			t.Fatalf("%s.%s: no check for this section", w.section, w.key)
		}
		if got != w.value {
			t.Errorf("%s.%s = %v, want %q from the defaults", w.section, w.key, got, w.value)
		}
	}
}

func TestMalformedDefaultsAreRefused(t *testing.T) {
	for name, tc := range map[string]struct{ text, want string }{
		"syntax":      {"poll_interval = \n", "parse config defaults"},
		"unknown key": {"[tools.claude]\naccount_evn = \"X\"\n", "unknown key(s): tools.claude.account_evn"},
		"wrong type":  {"poll_interval = 5\n", "parse config defaults"},
		"retired":     {"[supervisor]\n", "[supervisor] is no longer read"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := UseDefaults(tc.text)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("UseDefaults(%q) = %v, want an error containing %q", tc.text, err, tc.want)
			}
		})
	}
}

// A mistake in the operator's file is still reported against that file when
// defaults lie under it.
func TestTheOperatorsMistakeNamesTheirFile(t *testing.T) {
	useDefaults(t, "editor = \"dist\"\n")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("editor = \"x\"\npoll_interval = 5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadDir(dir)
	if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("LoadDir = %v, want the error to name %s and its line", err, path)
	}
}

func TestEmptyDefaultsLeaveLoadingAsItWas(t *testing.T) {
	useDefaults(t, "editor = \"dist\"\n")
	useDefaults(t, "  \n")
	if cfg := loadFile(t, "poll_interval = \"2s\"\n"); cfg.Editor != "" {
		t.Fatalf("editor = %q after the defaults were cleared", cfg.Editor)
	}
}
