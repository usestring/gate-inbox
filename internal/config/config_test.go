// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package config

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestLoadWritesAndParsesDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := writeDefault(path); err != nil {
		t.Fatalf("writeDefault: %v", err)
	}
	var cfg Config
	if err := decodeInto(path, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	cfg.applyDefaults()

	if cfg.PollInterval.Duration != 2*time.Second {
		t.Fatalf("poll interval = %v want 2s", cfg.PollInterval.Duration)
	}
	if _, ok := cfg.Tools["claude"]; !ok {
		t.Fatal("expected claude tool in default config")
	}
	if _, ok := cfg.Tools["opencode"]; !ok {
		t.Fatal("expected opencode tool in default config")
	}
	if _, ok := cfg.Tools["codex"]; !ok {
		t.Fatal("expected codex tool in default config")
	}
	if cfg.Tools["codex"].Command != "codex" {
		t.Fatalf("codex command = %q", cfg.Tools["codex"].Command)
	}
	if got := cfg.Tools["codex"].ReviveCommand; got != "codex resume --last" {
		t.Fatalf("codex revive_command = %q want \"codex resume --last\"", got)
	}
	if got := cfg.Tools["codex"].PromptFlag; got != "" {
		t.Fatalf("codex prompt_flag = %q want empty (positional prompt)", got)
	}
	if cfg.Tools["claude"].Command != "claude" {
		t.Fatalf("claude command = %q", cfg.Tools["claude"].Command)
	}
	if got := cfg.Tools["opencode"].PromptMode; got != "send" {
		t.Fatalf("opencode prompt_mode = %q want send: v2's --prompt fills the composer without submitting", got)
	}
	if got := cfg.Tools["claude"].PromptFlag; got != "" {
		t.Fatalf("claude prompt_flag = %q want empty (positional prompt)", got)
	}
	if got := cfg.Tools["claude"].ForkCommand; got != "claude --resume {id} --fork-session --session-id {new_id} --name {name}" {
		t.Fatalf("claude fork_command = %q", got)
	}
	if got := cfg.Tools["claude"].ForkDialogOption; got != "Resume full session as-is" {
		t.Fatalf("claude fork_dialog_option = %q", got)
	}
	if got := cfg.Tools["claude"].ForkDialogKeys; len(got) != 2 || got[0] != "Down" || got[1] != "Enter" {
		t.Fatalf("claude fork_dialog_keys = %v", got)
	}
	if got := cfg.Tools["codex"].ForkCommand; got != "codex fork {id}" {
		t.Fatalf("codex fork_command = %q want \"codex fork {id}\"", got)
	}
	if got := cfg.Tools["opencode"].ForkCommand; got != "" {
		t.Fatalf("opencode fork_command = %q want none: opencode forks through its API", got)
	}
	if got := cfg.Tools["opencode"].RenameCommand; got != "/rename" {
		t.Fatalf("opencode rename_command = %q want /rename (registered per session through OPENCODE_CONFIG)", got)
	}
	if !cfg.Tools["opencode"].SkipRenameDirective {
		t.Fatal("opencode must skip the launch rename directive: its sessions are named from session.title, /rename and silent instructions")
	}
	if cfg.Tools["claude"].SkipRenameDirective {
		t.Fatal("claude keeps the launch rename directive")
	}
	names := cfg.ToolNames()
	sort.Strings(names)
	if want := []string{"claude", "codex", "opencode", "terminal"}; !slices.Equal(names, want) {
		t.Fatalf("default tools = %v want %v", names, want)
	}
}

func TestLoadDirWritesDefaultInRequestedDirectory(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if _, ok := cfg.Tools["terminal"]; !ok {
		t.Fatal("default terminal tool is missing")
	}
	if _, err := os.Stat(filepath.Join(dir, "config.toml")); err != nil {
		t.Fatalf("config file: %v", err)
	}
}

func TestLoadDirUpgradesLegacyCodexWorkingRule(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	legacy := `
[tools.codex]
command = "codex"
rules = [
  { state = "working", pattern = "(?m)esc to interrupt\\b" },
]
`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	rule := cfg.Tools["codex"].Rules[0]
	if rule.Pattern == `(?m)esc to interrupt\b` {
		t.Fatal("legacy Codex working rule was not upgraded")
	}
	if !strings.Contains(rule.Pattern, `\z`) {
		t.Fatalf("upgraded Codex working rule is not scoped to the activity-region tail: %q", rule.Pattern)
	}
}

func TestLoadDirUpgradesCodexComposerPatterns(t *testing.T) {
	defaults, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	for _, custom := range []bool{false, true} {
		dir := t.TempDir()
		cutoff, chrome := `(?m)^›`, `^\s*─*\s*$`
		if custom {
			cutoff, chrome = `^custom prompt`, `^custom chrome`
		}
		raw := "[tools.codex]\nactivity_cutoff = '" + cutoff + "'\nchrome_line = '" + chrome + "'\n"
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if !custom {
			cutoff, chrome = defaults.Tools["codex"].ActivityCutoff, defaults.Tools["codex"].ChromeLine
		}
		got := cfg.Tools["codex"]
		if got.ActivityCutoff != cutoff || got.ChromeLine != chrome {
			t.Fatalf("custom=%v: cutoff=%q chrome=%q; want %q, %q", custom, got.ActivityCutoff, got.ChromeLine, cutoff, chrome)
		}
	}
}

func TestLoadDirUpgradesLegacyClaudeBusyLine(t *testing.T) {
	// Every shipped pattern is upgraded when a config carries it verbatim.
	// The assertion is what the pattern has to see rather than how it is
	// written: real summary lines captured off the board. Only the agent wait
	// line is busy; a turn-end summary whose tail names shells, monitors, MCP
	// tasks or background tasks is a turn that ended.
	busy := []string{
		"✻ Waiting for 2 background agents to finish",
		"✻ Waiting for 1 background agent to finish · 13 messages hidden (/focus to show)",
		"✻ Waiting for 1 background agent and 1 dynamic workflow to finish",
		"✻ Waiting for 2 dynamic workflows to finish",
	}
	ended := []string{
		"✻ Crunched for 1h 11m 30s · done 5:37 AM · 1 MCP task still running",
		"✻ Worked for 4m 13s · done 5:44 AM · 2 background tasks still running",
		"✻ Worked for 0s · done 4:28 AM · 8 shells, 2 monitors still running",
		"✻ Cogitated for 2m 44s · done 8:14 PM · 2 monitors still running",
		"✻ Cooked for 4s · 2 shells still running",
	}
	for _, legacyPattern := range []string{busyLineAgentsOnly, busyLineShellsOnly, busyLineEveryKind} {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.toml")
		legacy := `
[tools.claude]
command = "claude"
busy_line = '` + legacyPattern + `'
`
		if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadDir(dir)
		if err != nil {
			t.Fatalf("LoadDir: %v", err)
		}
		got := cfg.Tools["claude"].BusyLine
		re, err := regexp.Compile(got)
		if err != nil {
			t.Fatalf("upgraded claude busy_line does not compile: %q: %v", got, err)
		}
		for _, line := range busy {
			if !re.MatchString(line) {
				t.Errorf("upgraded claude busy_line %q does not see %q", got, line)
			}
		}
		for _, line := range ended {
			if re.MatchString(line) {
				t.Errorf("upgraded claude busy_line %q reads %q as busy", got, line)
			}
		}
	}
}

func TestLoadDirBackfillsLimitLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[tools.claude]\ncommand = \"claude\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if !strings.Contains(cfg.Tools["claude"].LimitLine, "You've hit your") {
		t.Fatalf("claude limit_line was not backfilled: %q", cfg.Tools["claude"].LimitLine)
	}
	if !strings.Contains(cfg.Tools["codex"].LimitLine, "You've hit your usage limit") {
		t.Fatalf("codex limit_line was not backfilled: %q", cfg.Tools["codex"].LimitLine)
	}
	if !strings.Contains(cfg.Tools["opencode"].LimitLine, "limit reached") {
		t.Fatalf("opencode limit_line was not backfilled: %q", cfg.Tools["opencode"].LimitLine)
	}
}

// A [tools.opencode] block written before input_line existed carries none,
// and with the cutoff sitting a row below the caret nothing in it names the
// composer row -- so Left never finds the head of an empty prompt and is
// forwarded into the pane forever, the very thing the field was added for.
// The default fills it like every other marker; a value the author chose
// survives the merge.
func TestLoadDirBackfillsOpencodeInputLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	stale := "[tools.opencode]\ncommand = \"opencode\"\nactivity_cutoff = \"(?m)^\\\\s*╹\"\n"
	if err := os.WriteFile(path, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if got := cfg.Tools["opencode"].InputLine; got != `^[ \x{A0}]*┃` {
		t.Fatalf("opencode input_line was not backfilled: %q", got)
	}

	custom := stale + "input_line = \"^>\"\n"
	if err := os.WriteFile(path, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if got := cfg.Tools["opencode"].InputLine; got != "^>" {
		t.Fatalf("opencode input_line = %q want the author's value kept", got)
	}
}

// codex repaints slower than the chase's default budget, so its block ships a
// longer one; a hand-written block inherits it, and a value its author chose
// survives the merge.
func TestLoadDirBackfillsCodexEchoBudgetAndKeepsACustomOne(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[tools.codex]\ncommand = \"codex\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if got := cfg.Tools["codex"].EchoBudget.Duration; got != 90*time.Millisecond {
		t.Fatalf("codex echo_budget = %v want 90ms", got)
	}
	if got := cfg.Tools["claude"].EchoBudget.Duration; got != 0 {
		t.Fatalf("claude echo_budget = %v want unset, so the chase keeps its default", got)
	}

	custom := t.TempDir()
	path = filepath.Join(custom, "config.toml")
	if err := os.WriteFile(path, []byte("[tools.codex]\ncommand = \"codex\"\necho_budget = \"150ms\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadDir(custom)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if got := cfg.Tools["codex"].EchoBudget.Duration; got != 150*time.Millisecond {
		t.Fatalf("a chosen echo_budget was overwritten: %v", got)
	}
}

func TestLoadDirPreservesCustomClaudeBusyLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	custom := `
[tools.claude]
command = "claude"
busy_line = "my own busy signal"
`
	if err := os.WriteFile(path, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if got := cfg.Tools["claude"].BusyLine; got != "my own busy signal" {
		t.Fatalf("claude busy_line = %q want the user's own pattern", got)
	}
}

func TestLoadDirPreservesCustomCodexWorkingRule(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	custom := `
[tools.codex]
command = "codex"
rules = [
  { state = "working", pattern = "my private status signal" },
]
`
	if err := os.WriteFile(path, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if got := cfg.Tools["codex"].Rules[0].Pattern; got != "my private status signal" {
		t.Fatalf("custom Codex working rule = %q", got)
	}
}

func TestShellToolUsesFlagAndStableName(t *testing.T) {
	cfg := Config{Tools: map[string]Tool{
		"terminal": {Command: "agent", Shell: false},
		"zsh":      {Command: "zsh", Shell: true},
		"bash":     {Command: "bash", Shell: true},
	}}
	name, tool, ok := cfg.ShellTool()
	if !ok || name != "bash" || tool.Command != "bash" {
		t.Fatalf("ShellTool = %q %+v %v, want bash", name, tool, ok)
	}
}

func TestDefaultWaitingRulesPrecedeWorking(t *testing.T) {
	cfg, err := Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}

	for name, tool := range cfg.Tools {
		firstWorking := -1
		lastWaiting := -1
		for i, rule := range tool.Rules {
			switch rule.State {
			case "working":
				if firstWorking < 0 {
					firstWorking = i
				}
			case "waiting":
				lastWaiting = i
			}
		}
		if name == "claude" && (firstWorking < 0 || lastWaiting < 0) {
			t.Fatalf("claude defaults need both working and waiting rules: %#v", tool.Rules)
		}
		if firstWorking >= 0 && lastWaiting > firstWorking {
			t.Errorf("%s waiting rule at %d follows first working rule at %d", name, lastWaiting, firstWorking)
		}
	}
}

func TestBackfillToolDefaults(t *testing.T) {
	cfg := Config{Tools: map[string]Tool{
		"opencode": {Command: "opencode", ReviveCommand: "opencode --continue"},
	}}
	if err := cfg.backfillToolDefaults(); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if got := cfg.Tools["opencode"].PromptMode; got != "send" {
		t.Fatalf("opencode prompt_mode = %q want send (backfilled)", got)
	}
	if got := cfg.Tools["opencode"].ReviveCommand; got != "opencode --continue" {
		t.Fatalf("opencode revive_command = %q want user value kept", got)
	}
	if _, ok := cfg.Tools["claude"]; !ok {
		t.Fatal("expected claude tool added from built-in defaults")
	}
}

// A config written before model_flag was a field carries none, and without a
// backfill every session it launches refuses a chosen model forever -- the
// default says --model, the installed file does not, and the file wins.
func TestBackfillModelFlag(t *testing.T) {
	cfg := Config{Tools: map[string]Tool{
		"codex":  {Command: "codex"},
		"claude": {Command: "claude", ModelFlag: "--pick-model"},
	}}
	if err := cfg.backfillToolDefaults(); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if got := cfg.Tools["codex"].ModelFlag; got != "--model" {
		t.Fatalf("codex model_flag = %q want --model (backfilled)", got)
	}
	if got := cfg.Tools["claude"].ModelFlag; got != "--pick-model" {
		t.Fatalf("claude model_flag = %q want user value kept", got)
	}
}

// A config written before skip_rename_directive was a field carries no key,
// and without a backfill every opencode session it launches keeps getting
// the visible directive -- the default says skip, the installed file does
// not, and the file wins.
func TestBackfillSkipRenameDirective(t *testing.T) {
	cfg := Config{Tools: map[string]Tool{
		"opencode": {Command: "opencode"},
		"claude":   {Command: "claude"},
	}}
	if err := cfg.backfillToolDefaults(); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if !cfg.Tools["opencode"].SkipRenameDirective {
		t.Fatal("opencode skip_rename_directive was not backfilled")
	}
	if cfg.Tools["claude"].SkipRenameDirective {
		t.Fatal("claude must keep the launch rename directive")
	}
}

// The listing a caller needs to pick a model has to survive an older config
// the same way the flag does, or list_models answers "no way to list its
// models" for a CLI whose default has carried the command all along.
func TestBackfillModelListing(t *testing.T) {
	cfg := Config{Tools: map[string]Tool{
		"opencode": {Command: "opencode"},
		"claude":   {Command: "claude", Models: []string{"only-mine"}},
	}}
	if err := cfg.backfillToolDefaults(); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if got := cfg.Tools["opencode"].ModelsCommand; got != "opencode models" {
		t.Fatalf("opencode models_command = %q want \"opencode models\" (backfilled)", got)
	}
	if got := cfg.Tools["claude"].Models; len(got) != 1 || got[0] != "only-mine" {
		t.Fatalf("claude models = %v want the user value kept", got)
	}
}

func TestApplyDefaults(t *testing.T) {
	var cfg Config
	cfg.applyDefaults()
	if cfg.PollInterval.Duration != 2*time.Second {
		t.Fatalf("poll = %v", cfg.PollInterval.Duration)
	}
	if cfg.Tools == nil {
		t.Fatal("tools should be non-nil after defaults")
	}
}

func TestDefaultResumeByIDFields(t *testing.T) {
	cfg, err := Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}
	// Tools that accept a chosen id launch with it and resume by it.
	for _, name := range []string{"claude"} {
		tool := cfg.Tools[name]
		if tool.SessionIDFlag != "--session-id" {
			t.Fatalf("%s session_id_flag = %q want --session-id", name, tool.SessionIDFlag)
		}
		if tool.ResumeByIDCommand == "" || !strings.Contains(tool.ResumeByIDCommand, "{id}") {
			t.Fatalf("%s resume_by_id_command = %q want an {id} template", name, tool.ResumeByIDCommand)
		}
	}
	// Tools that mint their own id declare a store to capture it from.
	for _, name := range []string{"codex", "opencode"} {
		tool := cfg.Tools[name]
		if tool.SessionStore != name {
			t.Fatalf("%s session_store = %q want %q", name, tool.SessionStore, name)
		}
		if tool.SessionIDFlag != "" {
			t.Fatalf("%s session_id_flag = %q want empty (no launch flag)", name, tool.SessionIDFlag)
		}
		if !strings.Contains(tool.ResumeByIDCommand, "{id}") {
			t.Fatalf("%s resume_by_id_command = %q want an {id} template", name, tool.ResumeByIDCommand)
		}
	}
}

func TestBackfillFillsResumeFields(t *testing.T) {
	cfg := Config{Tools: map[string]Tool{
		"claude": {Command: "claude", ReviveCommand: "claude --continue"},
	}}
	if err := cfg.backfillToolDefaults(); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	tool := cfg.Tools["claude"]
	if tool.SessionIDFlag != "--session-id" {
		t.Fatalf("claude session_id_flag = %q want backfilled --session-id", tool.SessionIDFlag)
	}
	if tool.ResumeByIDCommand != "claude --resume {id}" {
		t.Fatalf("claude resume_by_id_command = %q want backfilled", tool.ResumeByIDCommand)
	}
	if tool.ForkCommand == "" {
		t.Fatal("claude fork_command was not backfilled")
	}
	// A config written before the dialog answer existed still gets it: the
	// fork it describes lands on the same prompt as a fresh one.
	if tool.ForkDialogOption != "Resume full session as-is" {
		t.Fatalf("claude fork_dialog_option = %q want backfilled", tool.ForkDialogOption)
	}
	if len(tool.ForkDialogKeys) != 2 {
		t.Fatalf("claude fork_dialog_keys = %v want backfilled", tool.ForkDialogKeys)
	}
}

// A tool block that names its own keys keeps them: the pair is what the
// operator tuned when their CLI drew the dialog differently.
func TestBackfillKeepsCustomForkDialogKeys(t *testing.T) {
	cfg := Config{Tools: map[string]Tool{
		"claude": {Command: "claude", ForkDialogKeys: []string{"2"}},
	}}
	if err := cfg.backfillToolDefaults(); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if got := cfg.Tools["claude"].ForkDialogKeys; len(got) != 1 || got[0] != "2" {
		t.Fatalf("claude fork_dialog_keys = %v want the config's own", got)
	}
}

// The override exists so a trial can move this program's state without moving
// every other program's. XDG_CONFIG_HOME would take gh's credentials with it.
func TestHomeEnvMovesOnlyThisProgram(t *testing.T) {
	scratch := t.TempDir()
	t.Setenv(HomeEnv, scratch)
	dir, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if dir != scratch {
		t.Errorf("Dir() = %q, want the override %q", dir, scratch)
	}

	t.Setenv(HomeEnv, "")
	fallback, err := Dir()
	if err != nil {
		t.Fatalf("Dir with no override: %v", err)
	}
	if fallback == scratch {
		t.Error("an empty override still redirected the directory")
	}
	if !filepath.IsAbs(fallback) {
		t.Errorf("default dir %q is not absolute", fallback)
	}
	// Off the home directory, not inside the repository or a temp dir that a
	// reaper will take.
	home, err := os.UserHomeDir()
	if err == nil && !strings.HasPrefix(fallback, home) {
		t.Errorf("default dir %q is not under %q", fallback, home)
	}
}

// The log section is pass-through: an unset field means "take the built-in
// default", and a config written before the section existed keeps working.
func TestLogSectionIsOptionalAndParses(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("poll_interval = \"2s\"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if cfg.Log != (Log{}) {
		t.Fatalf("a config with no [log] section produced %+v", cfg.Log)
	}

	body := "poll_interval = \"2s\"\n\n[log]\nlevel = \"debug\"\nfile = \"/var/log/am.log\"\n" +
		"max_size_mb = 4\nmax_backups = 3\nmax_total_mb = 16\nno_compress = true\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err = LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	want := Log{Level: "debug", File: "/var/log/am.log", MaxSizeMB: 4, MaxBackups: 3, MaxTotalMB: 16, NoCompress: true}
	if cfg.Log != want {
		t.Fatalf("log = %+v, want %+v", cfg.Log, want)
	}
}

// A config written before AskUserQuestion was recognized keeps its own
// rules verbatim, so shipping the new rule in the built-in defaults would
// never reach the installs that have the bug.
func TestLoadDirTeachesExistingClaudeRulesTheQuestionDialog(t *testing.T) {
	dir := t.TempDir()
	legacy := `
[tools.claude]
command = "claude"
rules = [
  { state = "waiting", pattern = "Enter to confirm" },
  { state = "working", pattern = "esc to interrupt" },
]
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	rules := cfg.Tools["claude"].Rules
	added := -1
	for i, r := range rules {
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			t.Fatalf("compile %q: %v", r.Pattern, err)
		}
		if r.State == "waiting" && re.MatchString(askUserQuestionLegend) {
			added = i
		}
	}
	if added < 0 {
		t.Fatalf("existing claude rules did not gain a rule for the question dialog: %+v", rules)
	}
	for _, r := range rules[:added] {
		if r.State != "waiting" {
			t.Fatalf("the added rule landed after the working rules, at index %d: %+v", added, rules)
		}
	}
	review := -1
	for i, r := range rules {
		re := regexp.MustCompile(r.Pattern)
		if r.State == "waiting" && re.MatchString(askUserQuestionReview) {
			review = i
		}
	}
	if review < 0 {
		t.Fatalf("existing claude rules did not gain a rule for the dialog's review page: %+v", rules)
	}
	for _, r := range rules[:review] {
		if r.State != "waiting" {
			t.Fatalf("the review-page rule landed after the working rules, at index %d: %+v", review, rules)
		}
	}
	if len(rules) != 4 {
		t.Fatalf("want the user's two rules plus one per dialog page, got %+v", rules)
	}
}

// A user who already wrote a pattern covering the dialog keeps theirs,
// since a second rule for the same thing is noise they did not ask for.
func TestLoadDirKeepsAnExistingQuestionDialogRule(t *testing.T) {
	dir := t.TempDir()
	legacy := `
[tools.claude]
command = "claude"
rules = [
  { state = "waiting", pattern = "Enter to select" },
  { state = "waiting", pattern = "Ready to submit" },
  { state = "working", pattern = "esc to interrupt" },
]
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if got := cfg.Tools["claude"].Rules; len(got) != 3 {
		t.Fatalf("a config that already recognizes the dialog gained a rule: %+v", got)
	}
}

// A config written for an older build that had a [supervisor] section still
// loads: the section is ignored rather than failing the board's start.
func TestARetiredSupervisorSectionStillLoads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := "poll_interval = \"3s\"\n\n[supervisor]\nenabled = true\nmodel = \"opus\"\ntimeout = \"10m\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if cfg.PollInterval.Duration != 3*time.Second {
		t.Fatalf("poll_interval = %v, want the rest of the file honoured", cfg.PollInterval.Duration)
	}
}

// The live board's config carries the bare "Enter to confirm" rule verbatim, so
// anchoring the default alone would leave every existing install reading any
// turn that prints the phrase as waiting. The upgrade is in place: the rule
// keeps its position, and a hand-written pattern is left alone.
func TestLoadDirUpgradesTheBareEnterToConfirmRule(t *testing.T) {
	dir := t.TempDir()
	legacy := `
[tools.claude]
command = "claude"
rules = [
  { state = "waiting", pattern = "Enter to confirm" },
  { state = "working", pattern = "esc to interrupt" },
]

[tools.acme]
command = "acme"
rules = [
  { state = "waiting", pattern = "Enter to confirm" },
]
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	rules := cfg.Tools["claude"].Rules
	if len(rules) == 0 || rules[0].State != "waiting" {
		t.Fatalf("claude rules lost their leading waiting rule: %+v", rules)
	}
	if rules[0].Pattern == waitingEnterToConfirmBare {
		t.Fatal("the bare pattern survived the upgrade")
	}
	re, err := regexp.Compile(rules[0].Pattern)
	if err != nil {
		t.Fatalf("compile upgraded pattern: %v", err)
	}
	if !re.MatchString(" Enter to confirm · Esc to cancel") {
		t.Fatalf("upgraded pattern %q no longer reads a legend", rules[0].Pattern)
	}
	if re.MatchString(`   waitForPaneText(t, m, sessID, "Enter to confirm")`) {
		t.Fatalf("upgraded pattern %q still reads a quoted legend", rules[0].Pattern)
	}
	// Another tool's identical rule is its own author's: only claude shipped
	// the bare one as a default.
	if got := cfg.Tools["acme"].Rules[0].Pattern; got != waitingEnterToConfirmBare {
		t.Fatalf("acme rule rewritten to %q", got)
	}
}

// A config written before either opencode overlay was recognized carries a
// rules array with no waiting rule at all, so both a permission ask and an
// agent's select prompt read working off the spinner row that sits above the
// overlay. The rules block below is the one such a config actually holds.
func TestLoadDirTeachesExistingOpencodeRulesItsDialogs(t *testing.T) {
	dir := t.TempDir()
	legacy := `
[tools.opencode]
command = "opencode"
rules = [
  { state = "errored", pattern = "(?i)requires more credits" },
  { state = "working", pattern = "(?m)^\\s*▣ +[^·\\n]+· [^·\\n]+$" },
  { state = "working", pattern = "esc interrupt" },
]
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	rules := cfg.Tools["opencode"].Rules
	for _, sample := range opencodeDialogSamples {
		added := -1
		for i, r := range rules {
			re, err := regexp.Compile(r.Pattern)
			if err != nil {
				t.Fatalf("compile %q: %v", r.Pattern, err)
			}
			if r.State == "waiting" && re.MatchString(sample) {
				added = i
			}
		}
		if added < 0 {
			t.Fatalf("existing opencode rules did not gain a rule for %q: %+v", sample, rules)
		}
		for _, r := range rules[:added] {
			if r.State != "waiting" {
				t.Fatalf("the rule for %q landed after the working rules, at index %d: %+v", sample, added, rules)
			}
		}
	}
}

// A user who wrote their own rule for a dialog keeps it: the sample is what
// says the dialog is already covered, not the built-in pattern's presence.
func TestLoadDirKeepsACustomOpencodePermissionRule(t *testing.T) {
	dir := t.TempDir()
	legacy := `
[tools.opencode]
command = "opencode"
rules = [
  { state = "waiting", pattern = "Permission required" },
  { state = "working", pattern = "esc interrupt" },
]
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	for _, r := range cfg.Tools["opencode"].Rules {
		if r.Pattern == "\\x{25B3} Permission required" {
			t.Fatalf("built-in permission rule was added over the user's own: %+v", cfg.Tools["opencode"].Rules)
		}
	}
}

// Claude Code queues what is typed into it mid-turn, and a user's own claude
// block written before the field existed still gets it.
func TestClaudeTypesAheadEvenInAnOlderUserBlock(t *testing.T) {
	def, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if !def.Tools["claude"].TypeAhead {
		t.Fatal("the built-in claude block does not set type_ahead")
	}
	merged := mergeTool("claude", Tool{Command: "claude"}, def.Tools["claude"])
	if !merged.TypeAhead {
		t.Fatal("a user's claude block lost type_ahead in the merge")
	}
	if def.Tools["codex"].TypeAhead {
		t.Fatal("type_ahead reached a tool that was never shown to queue typed input")
	}
}

// Only claude, codex and opencode ship as built-ins, but any other agent CLI
// can still be described in config.toml. Such a block is loaded as written:
// nothing is backfilled onto it, whatever its name, since there is no
// built-in for it to inherit from.
func TestLoadDirKeepsACustomToolAsWritten(t *testing.T) {
	dir := t.TempDir()
	custom := `
[tools.gemini]
command = "gemini"
session_id_flag = "--session-id"
resume_by_id_command = "gemini --resume {id}"
activity_cutoff = "(?m)^\\s*> "
rules = [
  { state = "working", pattern = "esc to cancel" },
]
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	tool, ok := cfg.Tools["gemini"]
	if !ok {
		t.Fatal("the custom tool was dropped")
	}
	if tool.Command != "gemini" || tool.SessionIDFlag != "--session-id" || tool.ResumeByIDCommand != "gemini --resume {id}" {
		t.Fatalf("custom tool = %+v", tool)
	}
	if len(tool.Rules) != 1 || tool.Rules[0].Pattern != "esc to cancel" {
		t.Fatalf("custom rules = %+v", tool.Rules)
	}
	if tool.SessionStore != "" || tool.MCP != "" || tool.ForkCommand != "" || tool.LimitLine != "" {
		t.Fatalf("a custom tool inherited built-in defaults: %+v", tool)
	}
	if tool.DefaultStatus != "idle" {
		t.Fatalf("default_status = %q want idle", tool.DefaultStatus)
	}
	for _, name := range []string{"claude", "codex", "opencode"} {
		if _, ok := cfg.Tools[name]; !ok {
			t.Fatalf("built-in %s missing beside a custom tool", name)
		}
	}
}
