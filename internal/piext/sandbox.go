package piext

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/parentseal"
)

// The sandbox pi's bash tool runs in.
//
// The extension replaces pi's bash tool with one that wraps every command in
// @anthropic-ai/sandbox-runtime, the runtime Claude Code's own sandbox is
// built on: bubblewrap on Linux, sandbox-exec on macOS. What it may touch is
// decided here, in Go, at the start of the session (`hook pi sandbox`), and
// handed to the extension as a Plan:
//
//   - the operator's Claude Code sandbox settings, merged across the user,
//     project and managed files the way Claude merges them, so a pi session
//     can do what a Claude session in the same folder can and no more;
//   - the board's key directory added to denyRead (hooks.KeyDirDenials), which
//     catches the relative path a pattern rule misses (`cd ~/.config/gate-inbox
//     && cat channel-keys/x`);
//   - no way to a tmux server. A tmux server runs whatever it is told outside
//     every sandbox, so a command that could reach one could have it read the
//     keys. Its socket directories are in denyRead, which mounts an empty
//     directory over them, so the socket is not there to connect to; Claude's
//     allowAllUnixSockets is never inherited, and an allowUnixSockets entry
//     under a tmux socket directory is dropped. This holds whether or not the
//     runtime's seccomp helper, which refuses every unix socket, is installed:
//     it is a binary, so it is not bundled, and the runtime uses a global npm
//     install of itself when it finds one.
//
// The runtime is bundled (sandboxruntime/build.sh) and written beside the
// extension, so nothing is installed into ~/.pi or globally. Whatever goes wrong here, the plan says the sandbox is unavailable
// and the extension asks before every command; only `[tools.pi] sandbox =
// "off"` turns it off without asking.

// runtimeSource is @anthropic-ai/sandbox-runtime bundled with its
// dependencies into one ES module.
//
//go:embed sandbox-runtime.mjs
var runtimeSource []byte

const runtimeName = "gate-inbox-pi-sandbox-runtime.mjs"

// Plan modes.
const (
	SandboxOn          = "on"
	SandboxOff         = "off"
	SandboxUnavailable = "unavailable"
)

// Plan is what `hook pi sandbox` answers the extension.
type Plan struct {
	// Mode is SandboxOn, SandboxOff or SandboxUnavailable.
	Mode string `json:"mode"`
	// Reason says why the sandbox is off or unavailable.
	Reason string `json:"reason,omitempty"`
	// Runtime is the bundled sandbox runtime the extension imports.
	Runtime string `json:"runtime,omitempty"`
	// TmpDir is the private temporary directory commands get as TMPDIR.
	TmpDir string `json:"tmpDir,omitempty"`
	// AllowUnsandboxed is false when the operator's Claude settings set
	// sandbox.allowUnsandboxedCommands to false: a request to run outside
	// the sandbox is then ignored, as Claude ignores it.
	AllowUnsandboxed bool `json:"allowUnsandboxed"`
	// Config is the runtime's SandboxRuntimeConfig.
	Config *RuntimeConfig `json:"config,omitempty"`
}

// RuntimeConfig is the part of sandbox-runtime's SandboxRuntimeConfig the
// board sets.
type RuntimeConfig struct {
	Network                   RuntimeNetwork    `json:"network"`
	Filesystem                RuntimeFilesystem `json:"filesystem"`
	EnableWeakerNestedSandbox bool              `json:"enableWeakerNestedSandbox,omitempty"`
}

type RuntimeNetwork struct {
	AllowedDomains    []string `json:"allowedDomains"`
	DeniedDomains     []string `json:"deniedDomains"`
	AllowUnixSockets  []string `json:"allowUnixSockets,omitempty"`
	AllowLocalBinding bool     `json:"allowLocalBinding,omitempty"`
}

type RuntimeFilesystem struct {
	DenyRead   []string `json:"denyRead"`
	AllowRead  []string `json:"allowRead,omitempty"`
	AllowWrite []string `json:"allowWrite"`
	DenyWrite  []string `json:"denyWrite"`
}

// SandboxInput is what a plan is built from; SandboxHook fills it from the
// machine, tests from fixtures.
type SandboxInput struct {
	// Setting is `[tools.pi] sandbox`: "", "on" or "off".
	Setting string
	// Cwd is the session's working directory, from pi.
	Cwd string
	// Home is the user's home directory and ClaudeHome Claude Code's config
	// directory (~/.claude, or CLAUDE_CONFIG_DIR).
	Home, ClaudeHome string
	// KeyDir is the board's sealing-key directory.
	KeyDir string
	// HooksDir holds the runtime (EnsureSandbox).
	HooksDir string
	// TmpDir is the session's private temporary directory, already made.
	TmpDir string
	// ManagedFiles are Claude's managed settings files, read last.
	ManagedFiles []string
	// TmuxDirs are the directories tmux servers this session could reach
	// keep their sockets in (TmuxSocketDirs).
	TmuxDirs []string
	// UID names the tmux socket directory (tmux-<uid>).
	UID int
}

// claudeSandbox is the part of a Claude settings file's "sandbox" object pi
// inherits.
type claudeSandbox struct {
	AllowUnsandboxedCommands  *bool `json:"allowUnsandboxedCommands"`
	EnableWeakerNestedSandbox *bool `json:"enableWeakerNestedSandbox"`
	Network                   struct {
		AllowedDomains      []string `json:"allowedDomains"`
		DeniedDomains       []string `json:"deniedDomains"`
		AllowUnixSockets    []string `json:"allowUnixSockets"`
		AllowAllUnixSockets *bool    `json:"allowAllUnixSockets"`
		AllowLocalBinding   *bool    `json:"allowLocalBinding"`
	} `json:"network"`
	Filesystem struct {
		AllowWrite []string `json:"allowWrite"`
		DenyWrite  []string `json:"denyWrite"`
		DenyRead   []string `json:"denyRead"`
		AllowRead  []string `json:"allowRead"`
	} `json:"filesystem"`
}

// settingsFile is one Claude settings file and the directory its relative
// sandbox paths are read against.
type settingsFile struct {
	path, base string
}

// BuildSandboxPlan is the plan for one pi session. It never fails: anything it
// cannot read or make sense of becomes SandboxUnavailable, which the extension
// answers by asking before every command.
func BuildSandboxPlan(in SandboxInput) Plan {
	switch strings.TrimSpace(in.Setting) {
	case SandboxOff:
		return Plan{Mode: SandboxOff, Reason: `[tools.pi] sandbox = "off" in Gate Inbox's config`, AllowUnsandboxed: true}
	case "", SandboxOn:
	default:
		// A typo must not turn the sandbox off.
		return unavailable(fmt.Sprintf(`[tools.pi] sandbox = %q is neither "on" nor "off"`, in.Setting))
	}
	if !filepath.IsAbs(in.Cwd) {
		return unavailable(fmt.Sprintf("pi reported no absolute working directory (%q)", in.Cwd))
	}
	if in.KeyDir == "" || in.TmpDir == "" || in.HooksDir == "" {
		return unavailable("the board could not name its key directory, hooks directory or a temporary directory")
	}
	runtimePath := filepath.Join(in.HooksDir, hooks.GeneratedName(runtimeName, runtimeSource))
	if _, err := os.Stat(runtimePath); err != nil {
		return unavailable(fmt.Sprintf("the sandbox runtime is missing: %v", err))
	}
	cfg := &RuntimeConfig{}
	allowUnsandboxed := true
	for _, file := range claudeSettingsFiles(in) {
		s, err := readClaudeSandbox(file.path)
		if err != nil {
			return unavailable(err.Error())
		}
		if s == nil {
			continue
		}
		resolve := func(paths []string) []string { return resolvePaths(paths, file.base, in.Home) }
		cfg.Network.AllowedDomains = append(cfg.Network.AllowedDomains, s.Network.AllowedDomains...)
		cfg.Network.DeniedDomains = append(cfg.Network.DeniedDomains, s.Network.DeniedDomains...)
		cfg.Network.AllowUnixSockets = append(cfg.Network.AllowUnixSockets, resolve(s.Network.AllowUnixSockets)...)
		if s.Network.AllowLocalBinding != nil {
			cfg.Network.AllowLocalBinding = *s.Network.AllowLocalBinding
		}
		cfg.Filesystem.AllowWrite = append(cfg.Filesystem.AllowWrite, resolve(s.Filesystem.AllowWrite)...)
		cfg.Filesystem.DenyWrite = append(cfg.Filesystem.DenyWrite, resolve(s.Filesystem.DenyWrite)...)
		cfg.Filesystem.DenyRead = append(cfg.Filesystem.DenyRead, resolve(s.Filesystem.DenyRead)...)
		cfg.Filesystem.AllowRead = append(cfg.Filesystem.AllowRead, resolve(s.Filesystem.AllowRead)...)
		if s.EnableWeakerNestedSandbox != nil {
			cfg.EnableWeakerNestedSandbox = *s.EnableWeakerNestedSandbox
		}
		// Any file that turns the escape hatch off keeps it off: a managed
		// or project setting is not undone by the user's own file.
		if s.AllowUnsandboxedCommands != nil && !*s.AllowUnsandboxedCommands {
			allowUnsandboxed = false
		}
	}
	_, keyDenial := hooks.KeyDirDenials(in.KeyDir)
	cfg.Filesystem.DenyRead = append(cfg.Filesystem.DenyRead, keyDenial.Filesystem.DenyRead...)
	cfg.Filesystem.DenyRead = append(cfg.Filesystem.DenyRead, in.TmuxDirs...)
	cfg.Filesystem.AllowWrite = append([]string{in.Cwd, in.TmpDir}, cfg.Filesystem.AllowWrite...)
	cfg.Network.AllowUnixSockets = slices.DeleteFunc(cfg.Network.AllowUnixSockets, func(path string) bool {
		return reachesTmux(path, in.UID)
	})
	// A read the operator re-opened inside the key directory would undo
	// the denial; allowRead never reaches into it.
	cfg.Filesystem.AllowRead = slices.DeleteFunc(cfg.Filesystem.AllowRead, func(path string) bool {
		return within(path, in.KeyDir) || within(in.KeyDir, path)
	})
	for _, list := range []*[]string{&cfg.Network.AllowedDomains, &cfg.Network.DeniedDomains, &cfg.Network.AllowUnixSockets,
		&cfg.Filesystem.AllowWrite, &cfg.Filesystem.DenyWrite, &cfg.Filesystem.DenyRead, &cfg.Filesystem.AllowRead} {
		*list = dedupe(*list)
	}
	return Plan{Mode: SandboxOn, Runtime: runtimePath, TmpDir: in.TmpDir, AllowUnsandboxed: allowUnsandboxed, Config: cfg}
}

func unavailable(reason string) Plan {
	return Plan{Mode: SandboxUnavailable, Reason: reason}
}

// claudeSettingsFiles are the files Claude Code reads sandbox settings from,
// lowest precedence first: the user's, the project's shared and local
// files, then managed settings. The project is the nearest folder at or
// above cwd, up to the repository root, that has either settings file.
func claudeSettingsFiles(in SandboxInput) []settingsFile {
	files := []settingsFile{{path: filepath.Join(in.ClaudeHome, "settings.json"), base: in.ClaudeHome}}
	if project := projectRoot(in.Cwd, in.Home); project != "" {
		for _, name := range []string{"settings.json", "settings.local.json"} {
			files = append(files, settingsFile{path: filepath.Join(project, ".claude", name), base: project})
		}
	}
	for _, path := range in.ManagedFiles {
		files = append(files, settingsFile{path: path, base: filepath.Dir(path)})
	}
	return files
}

func projectRoot(cwd, home string) string {
	for dir := filepath.Clean(cwd); ; dir = filepath.Dir(dir) {
		if dir != home {
			for _, name := range []string{"settings.json", "settings.local.json"} {
				if _, err := os.Stat(filepath.Join(dir, ".claude", name)); err == nil {
					return dir
				}
			}
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return ""
		}
		if parent := filepath.Dir(dir); parent == dir || dir == home {
			return ""
		}
	}
}

// readClaudeSandbox is a settings file's sandbox object, nil when the file
// or the object is absent. A file that is there but unreadable is an error:
// skipping it could drop a denial the operator wrote.
func readClaudeSandbox(path string) (*claudeSandbox, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read Claude settings %s: %v", path, err)
	}
	var file struct {
		Sandbox *claudeSandbox `json:"sandbox"`
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("cannot parse Claude settings %s: %v", path, err)
	}
	return file.Sandbox, nil
}

// resolvePaths reads Claude's sandbox path spellings: "/x" and "//x" are
// absolute, "~/x" is under home, and anything else is relative to the
// settings file's base (the project root, or ~/.claude for the user's file).
func resolvePaths(paths []string, base, home string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		switch {
		case p == "":
			continue
		case strings.HasPrefix(p, "//"):
			p = p[1:]
		case p == "~":
			p = home
		case strings.HasPrefix(p, "~/"):
			p = filepath.Join(home, p[2:])
		case !filepath.IsAbs(p):
			p = filepath.Join(base, p)
		}
		out = append(out, filepath.Clean(p))
	}
	return out
}

// reachesTmux reports whether a unix socket path is, or holds, a tmux
// server's socket: under a tmux-<uid> directory, wherever TMUX_TMPDIR put it.
func reachesTmux(path string, uid int) bool {
	dir := "tmux-" + strconv.Itoa(uid)
	for _, part := range strings.Split(filepath.ToSlash(filepath.Clean(path)), "/") {
		if part == dir {
			return true
		}
	}
	return false
}

// within reports whether path is dir or under it.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

func dedupe(list []string) []string {
	seen := make(map[string]bool, len(list))
	out := make([]string, 0, len(list))
	for _, s := range list {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// EnsureSandbox writes the runtime under hooksDir. Its name carries its
// content's stamp, as the extension's does, so a build only ever loads its
// own.
func EnsureSandbox(hooksDir string) error {
	_, err := hooks.WriteGenerated(hooksDir, runtimeName, runtimeSource)
	return err
}

// TmuxSocketDirs are the directories that hold the sockets of the tmux
// servers a process with this environment could reach: tmux's default
// /tmp/tmux-<uid>, the same under TMUX_TMPDIR, and the directory of the
// server it runs in ($TMUX). Only the ones that exist are returned.
func TmuxSocketDirs(getenv func(string) string, uid int) []string {
	name := "tmux-" + strconv.Itoa(uid)
	candidates := []string{filepath.Join("/tmp", name)}
	if dir := getenv("TMUX_TMPDIR"); dir != "" {
		candidates = append(candidates, filepath.Join(dir, name))
	}
	if socket, _, _ := strings.Cut(getenv("TMUX"), ","); filepath.IsAbs(socket) {
		candidates = append(candidates, filepath.Dir(socket))
	}
	var dirs []string
	for _, dir := range candidates {
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil {
			continue
		}
		dirs = append(dirs, resolved)
	}
	return dedupe(dirs)
}

// managedSettingsFiles are Claude Code's managed settings on this OS.
func managedSettingsFiles(goos string) []string {
	switch goos {
	case "darwin":
		return []string{"/Library/Application Support/ClaudeCode/managed-settings.json"}
	case "linux":
		files := []string{"/etc/claude-code/managed-settings.json"}
		dropIns, _ := filepath.Glob("/etc/claude-code/managed-settings.d/*.json")
		slices.Sort(dropIns)
		return append(files, dropIns...)
	}
	return nil
}

// SandboxHook answers `gate-inbox hook pi sandbox`: the payload is pi's
// {"cwd": ...}, the answer the session's Plan as JSON.
func SandboxHook(in io.Reader, configDir, claudeHome string) string {
	var payload struct {
		Cwd string `json:"cwd"`
	}
	raw, err := io.ReadAll(io.LimitReader(in, 1<<20))
	if err == nil {
		err = json.Unmarshal(raw, &payload)
	}
	var plan Plan
	if err != nil {
		plan = unavailable(fmt.Sprintf("the board could not read pi's request: %v", err))
	} else {
		plan = sandboxPlanFor(configDir, claudeHome, payload.Cwd)
	}
	out, _ := json.Marshal(plan)
	return string(out)
}

func sandboxPlanFor(configDir, claudeHome, cwd string) Plan {
	cfg, err := config.LoadDir(configDir)
	if err != nil {
		return unavailable(fmt.Sprintf("cannot read Gate Inbox's config: %v", err))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return unavailable(fmt.Sprintf("no home directory: %v", err))
	}
	tmp := filepath.Join(os.TempDir(), "gate-inbox-pi-"+strconv.Itoa(os.Getuid()))
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return unavailable(fmt.Sprintf("cannot make %s: %v", tmp, err))
	}
	return BuildSandboxPlan(SandboxInput{
		Setting:      piTool(cfg).Sandbox,
		Cwd:          cwd,
		Home:         home,
		ClaudeHome:   claudeHome,
		KeyDir:       parentseal.KeyDir(configDir),
		HooksDir:     hooks.NewManager(configDir).Dir(),
		TmpDir:       tmp,
		ManagedFiles: managedSettingsFiles(runtime.GOOS),
		TmuxDirs:     TmuxSocketDirs(os.Getenv, os.Getuid()),
		UID:          os.Getuid(),
	})
}

// piTool is the configured tool whose sessions report through this
// extension; a board with several takes the strictest setting, so one
// tool's "off" never turns another's sandbox off.
func piTool(cfg config.Config) config.Tool {
	var found config.Tool
	for _, name := range slices.Sorted(maps.Keys(cfg.Tools)) {
		tool := cfg.Tools[name]
		if tool.StatusSource != hooks.StatusSourcePi {
			continue
		}
		if found.StatusSource == "" || tool.Sandbox != SandboxOff {
			found = tool
		}
	}
	return found
}
