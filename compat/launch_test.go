package compat

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/launch"
)

const (
	launchPrompt   = "Fix the flaky widget test and report what changed."
	launchID       = "cafe0001"
	agentSessionID = "11111111-2222-3333-4444-555555555555"
	movedWorkdir   = "/srv/example/moved"
)

// TestLaunchPlans records, for every agent CLI the default config knows,
// what a spawn launches: the command line, the input typed in after it,
// the environment, and the command a revive resumes on. Values that are
// secrets are printed as <redacted>, and ids minted per launch as <uuid>.
func TestLaunchPlans(t *testing.T) {
	s := newScratch(t)
	cfg, err := config.LoadDir(s.home)
	if err != nil {
		t.Fatal(err)
	}
	manager := hooks.NewManager(s.home)
	names := cfg.ToolNames()
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		tool := cfg.Tools[name]
		if tool.Shell {
			fmt.Fprintf(&b, "\n######## %s: a shell, not launched through a plan\n", name)
			continue
		}
		fmt.Fprintf(&b, "\n######## %s\n", name)
		model := ""
		if tool.ModelFlag != "" || launch.ModelRidesConfig(name, tool) {
			model = "example-model"
		}
		for _, variant := range []struct {
			label     string
			workdir   string
			autoNamed bool
			model     string
		}{
			{label: "auto-named spawn", autoNamed: true},
			{label: "named spawn", autoNamed: false},
			{label: "spawn whose directory could not be opened", workdir: movedWorkdir, autoNamed: true},
			{label: "spawn on a chosen model", autoNamed: true, model: model},
		} {
			if variant.label == "spawn on a chosen model" && model == "" {
				_, err := launch.WithModel(name, tool, tool.Command, "example-model")
				fmt.Fprintf(&b, "\n== %s\nrefused: %v\n", variant.label, err)
				continue
			}
			b.WriteString(describePlan(t, s, manager, name, tool, variant.label, variant.workdir, variant.autoNamed, variant.model, ""))
		}
		for _, revive := range []struct{ label, id string }{
			{"revive without a captured conversation id", ""},
			{"revive with a captured conversation id", agentSessionID},
		} {
			command, err := launch.ReviveCommand(name, tool, revive.id, "")
			fmt.Fprintf(&b, "\n== %s\ncommand: %s\n", revive.label, command)
			if err != nil {
				fmt.Fprintf(&b, "error: %v\n", err)
			}
		}
	}

	// The account path exists for claude only, and only once a secret store
	// is configured.
	s.writeFile(t, "config.toml", accountConfig)
	cfg, err = config.LoadDir(s.home)
	if err != nil {
		t.Fatal(err)
	}
	claude := cfg.Tools["claude"]
	b.WriteString("\n######## claude, launched on a pooled account\n")
	b.WriteString(describePlan(t, s, manager, "claude", claude, "spawn on account ada1", "", true, "", "ada1"))
	b.WriteString("\n== secret store calls made\n" + s.calls(t))
	golden(t, "launch/plans.golden", s.redact(b.String()))
}

// TestGeneratedLaunchFiles records the files a real launch writes for the
// CLIs that are pointed at the MCP server and at the status hooks through a
// generated config: claude's settings and MCP config, and opencode's.
func TestGeneratedLaunchFiles(t *testing.T) {
	s := newScratch(t)
	cfg, err := config.LoadDir(s.home)
	if err != nil {
		t.Fatal(err)
	}
	manager := hooks.NewManager(s.home)
	for _, name := range []string{"claude", "opencode"} {
		tool := cfg.Tools[name]
		if _, _, err := launch.Environment(manager, name, tool, tool.Command, launchID, "", ""); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	var b strings.Builder
	var paths []string
	filepath.WalkDir(manager.Dir(), func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			paths = append(paths, path)
		}
		return nil
	})
	sort.Strings(paths)
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(s.home, path)
		fmt.Fprintf(&b, "=== %s\n%s\n", rel, raw)
	}
	golden(t, "launch/generated-files.golden", s.redact(b.String()))
}

func describePlan(t *testing.T, s *scratch, manager *hooks.Manager, name string, tool config.Tool, label, workdir string, autoNamed bool, model, account string) string {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "\n== %s\n", label)
	plan, err := launch.Assemble(name, tool, launchPrompt, workdir, autoNamed, model, account)
	if err != nil {
		fmt.Fprintf(&b, "refused: %v\n", err)
		return b.String()
	}
	fmt.Fprintf(&b, "plan.command: %s\n", plan.Command)
	fmt.Fprintf(&b, "plan.launch_prompt: %q\n", plan.LaunchPrompt)
	for i, input := range plan.PendingInputs {
		fmt.Fprintf(&b, "plan.pending_inputs[%d]: %q\n", i, input)
	}
	fmt.Fprintf(&b, "plan.agent_session_id: %q\n", plan.AgentSessionID)
	fmt.Fprintf(&b, "plan.model: %q\nplan.account: %q\n", plan.Model, plan.Account)
	command, env, err := launch.Compose(manager, name, tool, plan.Command, launchID, plan.Model, plan.Account)
	if err != nil {
		fmt.Fprintf(&b, "compose refused: %v\n", err)
		return b.String()
	}
	fmt.Fprintf(&b, "launch.command: %s\n", command)
	for _, key := range sortedKeys(env) {
		value := env[key]
		if value != "" && secretName.MatchString(key) {
			if key == tool.AccountEnv && value != fakeTokenStem+fakeSecretStem+plan.Account {
				t.Errorf("%s carries %q, not the fake token for %s", key, value, plan.Account)
			}
			value = "<redacted>"
		}
		fmt.Fprintf(&b, "launch.env.%s=%s\n", key, value)
	}
	return b.String()
}

// calls is every call the fake secret store answered, in order, and
// clears the log for the next reader.
func (s *scratch) calls(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(s.callLog)
	if os.IsNotExist(err) {
		return "(none)\n"
	}
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(s.callLog)
	return string(raw)
}
