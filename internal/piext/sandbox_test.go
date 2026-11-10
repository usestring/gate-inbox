package piext

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type sandboxFixture struct {
	in      SandboxInput
	project string
}

// newSandboxFixture is a home with a Claude config dir, a git project under
// it, the board's hooks dir with the sandbox files written, and a tmp dir.
func newSandboxFixture(t *testing.T) sandboxFixture {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	project := filepath.Join(home, "src", "app")
	hooksDir := filepath.Join(root, "board", "hooks")
	for _, dir := range []string{filepath.Join(home, ".claude"), filepath.Join(project, ".git"), filepath.Join(project, "pkg"), filepath.Join(root, "tmp")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := EnsureSandbox(hooksDir); err != nil {
		t.Fatal(err)
	}
	return sandboxFixture{
		project: project,
		in: SandboxInput{
			Cwd:        filepath.Join(project, "pkg"),
			Home:       home,
			ClaudeHome: filepath.Join(home, ".claude"),
			KeyDir:     filepath.Join(root, "board", "channel-keys"),
			HooksDir:   hooksDir,
			TmpDir:     filepath.Join(root, "tmp"),
			TmuxDirs:   []string{"/tmp/tmux-1000"},
			UID:        1000,
		},
	}
}

func writeSettings(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSandboxPlanInheritsClaudeSettingsAndDeniesTheKeyDir(t *testing.T) {
	f := newSandboxFixture(t)
	writeSettings(t, filepath.Join(f.in.ClaudeHome, "settings.json"), `{"sandbox":{
		"network":{"allowedDomains":["github.com"],"allowAllUnixSockets":true},
		"filesystem":{"denyRead":["~/.ssh"],"allowWrite":["~/.cache/go-build","debug"]}}}`)
	writeSettings(t, filepath.Join(f.project, ".claude", "settings.json"), `{"sandbox":{
		"network":{"allowedDomains":["proxy.golang.org","github.com"],"allowUnixSockets":["/var/run/docker.sock","/tmp/tmux-1000/default","/x/tmux-1000"]},
		"filesystem":{"allowWrite":["../worktrees","//opt/out"],"denyWrite":["./secrets"]}}}`)
	writeSettings(t, filepath.Join(f.project, ".claude", "settings.local.json"), `{"sandbox":{"filesystem":{"allowRead":["`+f.in.KeyDir+`/caller.key","/srv"]}}}`)

	plan := BuildSandboxPlan(f.in)
	if plan.Mode != SandboxOn || plan.Config == nil {
		t.Fatalf("plan = %+v", plan)
	}
	cfg := plan.Config
	home, project := f.in.Home, f.project
	want := RuntimeConfig{
		Network: RuntimeNetwork{
			AllowedDomains: []string{"github.com", "proxy.golang.org"},
			DeniedDomains:  []string{},
			// allowAllUnixSockets is dropped, and so is every tmux socket.
			AllowUnixSockets: []string{"/var/run/docker.sock"},
		},
		Filesystem: RuntimeFilesystem{
			DenyRead: []string{filepath.Join(home, ".ssh"), f.in.KeyDir, "/tmp/tmux-1000"},
			// An allowRead inside the key directory is dropped.
			AllowRead: []string{"/srv"},
			AllowWrite: []string{f.in.Cwd, f.in.TmpDir, filepath.Join(home, ".cache", "go-build"),
				filepath.Join(f.in.ClaudeHome, "debug"), filepath.Join(filepath.Dir(project), "worktrees"), "/opt/out"},
			DenyWrite: []string{filepath.Join(project, "secrets")},
		},
	}
	got, _ := json.Marshal(cfg)
	wantJSON, _ := json.Marshal(want)
	if string(got) != string(wantJSON) {
		t.Fatalf("config\n got %s\nwant %s", got, wantJSON)
	}
	if !plan.AllowUnsandboxed {
		t.Fatal("no file turned allowUnsandboxedCommands off, yet the plan refuses unsandboxed commands")
	}
	if !strings.HasPrefix(filepath.Base(plan.Runtime), "gate-inbox-pi-sandbox-runtime-") {
		t.Fatalf("runtime = %q", plan.Runtime)
	}
}

func TestSandboxPlanKeepsTheEscapeHatchShutOnceAnyFileShutsIt(t *testing.T) {
	f := newSandboxFixture(t)
	managed := filepath.Join(t.TempDir(), "managed-settings.json")
	f.in.ManagedFiles = []string{managed}
	writeSettings(t, managed, `{"sandbox":{"allowUnsandboxedCommands":false}}`)
	writeSettings(t, filepath.Join(f.project, ".claude", "settings.local.json"), `{"sandbox":{"allowUnsandboxedCommands":true}}`)
	if plan := BuildSandboxPlan(f.in); plan.Mode != SandboxOn || plan.AllowUnsandboxed {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestSandboxPlanFailsClosed(t *testing.T) {
	cases := map[string]func(f *sandboxFixture){
		"a typo in the setting": func(f *sandboxFixture) { f.in.Setting = "of" },
		"a relative cwd":        func(f *sandboxFixture) { f.in.Cwd = "pkg" },
		"no key directory":      func(f *sandboxFixture) { f.in.KeyDir = "" },
		"an unparsable settings file": func(f *sandboxFixture) {
			writeSettings(t, filepath.Join(f.in.ClaudeHome, "settings.json"), `{"sandbox":`)
		},
		"a missing runtime": func(f *sandboxFixture) { f.in.HooksDir = t.TempDir() },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newSandboxFixture(t)
			mutate(&f)
			plan := BuildSandboxPlan(f.in)
			if plan.Mode != SandboxUnavailable || plan.Reason == "" || plan.Config != nil {
				t.Fatalf("plan = %+v", plan)
			}
		})
	}
}

func TestSandboxPlanOffOnlyWhenTheConfigSaysOff(t *testing.T) {
	f := newSandboxFixture(t)
	f.in.Setting = "off"
	if plan := BuildSandboxPlan(f.in); plan.Mode != SandboxOff {
		t.Fatalf("plan = %+v", plan)
	}
	f.in.Setting = "on"
	if plan := BuildSandboxPlan(f.in); plan.Mode != SandboxOn {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestTmuxSocketDirsNamesEveryServerThisSessionCouldReach(t *testing.T) {
	root := t.TempDir()
	custom := filepath.Join(root, "custom")
	inPane := filepath.Join(root, "pane")
	for _, dir := range []string{filepath.Join(custom, "tmux-1000"), inPane} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	env := map[string]string{
		"TMUX_TMPDIR": custom,
		"TMUX":        filepath.Join(inPane, "board") + ",4242,0",
	}
	got := TmuxSocketDirs(func(k string) string { return env[k] }, 1000)
	resolve := func(p string) string { r, _ := filepath.EvalSymlinks(p); return r }
	for _, want := range []string{resolve(filepath.Join(custom, "tmux-1000")), resolve(inPane)} {
		if !slices.Contains(got, want) {
			t.Fatalf("TmuxSocketDirs = %v, missing %s", got, want)
		}
	}
	for _, dir := range got {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("TmuxSocketDirs returned %s, which does not exist", dir)
		}
	}
}

func TestProjectRootStopsAtTheRepository(t *testing.T) {
	f := newSandboxFixture(t)
	// Settings above the repository are not the project's.
	writeSettings(t, filepath.Join(filepath.Dir(f.project), ".claude", "settings.json"), `{}`)
	if got := projectRoot(f.in.Cwd, f.in.Home); got != "" {
		t.Fatalf("projectRoot = %q, want none", got)
	}
	writeSettings(t, filepath.Join(f.project, ".claude", "settings.json"), `{}`)
	if got := projectRoot(f.in.Cwd, f.in.Home); got != f.project {
		t.Fatalf("projectRoot = %q, want %q", got, f.project)
	}
}

func TestRuntimeBundleExportsTheSandboxManager(t *testing.T) {
	tail := string(runtimeSource[max(0, len(runtimeSource)-200):])
	if !strings.Contains(tail, " as SandboxManager}") {
		t.Fatalf("sandbox-runtime.mjs (%d bytes) does not end by exporting SandboxManager; rerun sandboxruntime/build.sh", len(runtimeSource))
	}
}
