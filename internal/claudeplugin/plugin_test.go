package claudeplugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/usestring/gate-inbox/internal/hooks"
)

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestEnsureWritesTheWholePlugin(t *testing.T) {
	home := t.TempDir()
	dir, err := Ensure(home)
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := Path(home); dir != want {
		t.Fatalf("Ensure wrote %s, Path names %s", dir, want)
	}
	if filepath.Dir(dir) != Dir(home) {
		t.Fatalf("plugin at %s, want under %s", dir, Dir(home))
	}
	for _, rel := range []string{
		".claude-plugin/plugin.json",
		"hooks/hooks.json",
		"hooks/merge-gate.ts",
		"skills/delegate-with-gate-inbox/SKILL.md",
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("%s: %v", rel, err)
		}
	}
	for _, rel := range []string{"skills/.gitkeep", "commands/.gitkeep", "claude-plugin"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			t.Errorf("%s was written; it only holds the source tree open", rel)
		}
	}

	var manifest map[string]string
	if err := json.Unmarshal(readFile(t, filepath.Join(dir, ".claude-plugin/plugin.json")), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest["name"] != Name || manifest["version"] != Version(filepath.Base(dir)) {
		t.Fatalf("manifest = %v, want name %s and the directory's stamp as build metadata", manifest, Name)
	}

	merge := string(readFile(t, filepath.Join(dir, "hooks/merge-gate.ts")))
	for _, want := range []string{"tool.check", "merge-gate-check", "$.process.run"} {
		if !strings.Contains(merge, want) {
			t.Errorf("merge-gate.ts lacks %q", want)
		}
	}
}

// The plugin's hooks are the launch hooks, matcher for matcher, so moving
// them out of the settings file changes nothing a session runs.
func TestHooksFileCarriesTheLaunchHooksAndTheMods(t *testing.T) {
	dir, err := Ensure(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Modules []string                       `json:"modules"`
		Hooks   map[string][]hooks.HookMatcher `json:"hooks"`
	}
	if err := json.Unmarshal(readFile(t, filepath.Join(dir, "hooks/hooks.json")), &file); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(file.Modules, []string{"./merge-gate.ts"}) {
		t.Fatalf("modules = %v", file.Modules)
	}
	want := hooks.LaunchHooks()
	for _, hook := range contributed {
		want[hook.Event] = append(want[hook.Event], hooks.HookMatcher{
			Matcher: hook.Matcher,
			Hooks:   []hooks.HookCommand{{Type: "command", Command: hook.Command}},
		})
	}
	if !reflect.DeepEqual(file.Hooks, want) {
		t.Fatalf("hooks = %+v\nwant the launch hooks then the registered ones %+v", file.Hooks, want)
	}
	if len(file.Hooks) != 9 {
		t.Fatalf("hooks carry %d events, want the 9 the launch has", len(file.Hooks))
	}
}

func TestRegisteredHooksFollowTheLaunchHooks(t *testing.T) {
	saved := contributed
	t.Cleanup(func() { contributed = saved })
	register(Hook{Event: "PreToolUse", Matcher: "Agent", Command: "true"})
	data, err := hooksFile(nil)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Hooks map[string][]hooks.HookMatcher `json:"hooks"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	pre := file.Hooks["PreToolUse"]
	launch := hooks.LaunchHooks()["PreToolUse"]
	if len(pre) != len(launch)+1 || pre[len(pre)-1].Matcher != "Agent" || pre[len(pre)-1].Hooks[0].Command != "true" {
		t.Fatalf("PreToolUse = %+v, want the launch entries then the registered one", pre)
	}
}

func TestEnsureRestoresADriftedFileAndKeepsTheDirectory(t *testing.T) {
	home := t.TempDir()
	dir, err := Ensure(home)
	if err != nil {
		t.Fatal(err)
	}
	skill := filepath.Join(dir, "skills/delegate-with-gate-inbox/SKILL.md")
	want := readFile(t, skill)
	if err := os.WriteFile(skill, []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "hooks/merge-gate.ts")); err != nil {
		t.Fatal(err)
	}
	again, err := Ensure(home)
	if err != nil || again != dir {
		t.Fatalf("second Ensure = %q, %v; want %q", again, err, dir)
	}
	if got := readFile(t, skill); string(got) != string(want) {
		t.Fatalf("drifted skill not restored: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "hooks/merge-gate.ts")); err != nil {
		t.Fatalf("missing module not restored: %v", err)
	}
	entries, err := os.ReadDir(Dir(home))
	if err != nil || len(entries) != 1 {
		t.Fatalf("plugins dir holds %v (%v), want only this build's", entries, err)
	}
}

// Every process that launches a session writes the plugin, so two at once
// must both come away with a whole one.
func TestEnsureIsSafeToRace(t *testing.T) {
	home := t.TempDir()
	var wg sync.WaitGroup
	dirs := make([]string, 8)
	errs := make([]error, 8)
	for i := range dirs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			dirs[i], errs[i] = Ensure(home)
		}()
	}
	wg.Wait()
	for i := range dirs {
		if errs[i] != nil || dirs[i] != dirs[0] {
			t.Fatalf("Ensure %d = %q, %v; want %q", i, dirs[i], errs[i], dirs[0])
		}
	}
	if _, err := os.Stat(filepath.Join(dirs[0], "hooks/hooks.json")); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(Dir(home))
	if len(entries) != 1 {
		t.Fatalf("plugins dir holds %d entries, want one with no leftovers", len(entries))
	}
}

func TestPathWritesNothing(t *testing.T) {
	home := t.TempDir()
	if _, err := Path(home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(Dir(home)); !os.IsNotExist(err) {
		t.Fatalf("Path created %s: %v", Dir(home), err)
	}
}

func TestVersionIsSemverWithTheStamp(t *testing.T) {
	if got := Version("abc123def456"); !strings.HasSuffix(got, "+abc123def456") || strings.HasPrefix(got, "v") {
		t.Fatalf("Version = %q", got)
	}
}
