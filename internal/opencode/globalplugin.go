package opencode

// An opencode the operator started outside the board never loaded the config
// a launch generates: no gate-inbox MCP server, no steering, no row id in its
// environment. OpenCode v2 also runs every such session in one background
// service per user, so neither a hook nor an MCP server it spawns sits in the
// pane's process tree, and a global MCP server could not stay hidden until
// adoption: the service starts one per project directory for every session
// there, lists it in the session sidebar, and asks for its tools with no
// session named.
//
// What reaches every session instead is a plugin in the global config's
// plugins directory, loaded by the service with no config entry. The board
// writes one file there per board home. It does nothing for a session unless
// the board is running and the session shows in a pane the board adopted,
// which it learns from the session's shell: the service runs shell commands
// with the environment of the opencode client showing the session, so their
// $TMUX and $TMUX_PANE name the pane, and the adoption marker for that pane
// names the row. Then:
//
//   - every shell command the session runs gets the row's GATE_INBOX_SESSION_ID,
//     GATE_INBOX_BIN and GATE_INBOX_HOME, so the gate-inbox CLI speaks as the row;
//   - the session id is left in the row's conversation mailbox, which the
//     poller binds the row to, and which the marker then carries, so the
//     plugin knows the session again after it reloads;
//   - every model request of a bound session carries the board's steering as
//     system text, the way a launched session's server instructions ride
//     every request.
//
// A launched session runs on a private server whose environment already
// names its row, so the plugin leaves it alone and nothing is said twice.

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed globalplugin.js
var pluginTemplate string

// pluginTag opens every plugin file this package writes, followed by the
// board home it belongs to, so the files can be told from the operator's own
// plugins and one board's from another's.
const pluginTag = "// gate-inbox-global-plugin "

const pluginPrefix = "gate-inbox-"

// ConfigDirEnv is OpenCode's own override of its global config directory.
const ConfigDirEnv = "OPENCODE_CONFIG_DIR"

// GlobalConfigDir is the directory every opencode reads its global config and
// plugins from: OPENCODE_CONFIG_DIR, else $XDG_CONFIG_HOME/opencode, else
// ~/.config/opencode, as `opencode debug paths` resolves it.
func GlobalConfigDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv(ConfigDirEnv)); dir != "" {
		return dir, nil
	}
	if dir := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); dir != "" {
		return filepath.Join(dir, "opencode"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "opencode"), nil
}

// Plugin is what one board's plugin file is written from.
type Plugin struct {
	// Home is the board's config directory: its lock, markers and mailboxes.
	Home string
	// Bin is the installed gate-inbox binary the shell commands are pointed at.
	Bin string
	// Tools are the configured tools that run opencode, whose adoption
	// markers name a session the plugin may speak for.
	Tools []string
	// Steering is the system text a bound session's requests carry.
	Steering string
}

// PluginPath is where the plugin of the board at home lives under the global
// config directory dir.
func PluginPath(dir, home string) string {
	sum := sha256.Sum256([]byte(home))
	return filepath.Join(dir, "plugins", pluginPrefix+hex.EncodeToString(sum[:6])+".js")
}

// Source is the plugin file's content.
func (p Plugin) Source() []byte {
	quote := func(v any) string {
		raw, _ := json.Marshal(v)
		return string(raw)
	}
	tools := p.Tools
	if tools == nil {
		tools = []string{}
	}
	key := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(PluginPath("", p.Home)), pluginPrefix), ".js")
	body := strings.NewReplacer(
		"__HOME__", quote(p.Home),
		"__BIN__", quote(p.Bin),
		"__TOOLS__", quote(tools),
		"__STEERING__", quote(p.Steering),
		"__KEY__", quote(key),
	).Replace(pluginTemplate)
	return []byte(pluginTag + quote(p.Home) + "\n" + body)
}

// RegisterPlugin writes the plugin of p.Home into the global config directory
// dir, creating its plugins directory when needed. changed is false when the
// file already held exactly this content, which is the common case and
// writes nothing. Nothing else in the directory is touched.
func RegisterPlugin(dir string, p Plugin) (changed bool, err error) {
	path := PluginPath(dir, p.Home)
	want := p.Source()
	if have, err := os.ReadFile(path); err == nil && bytes.Equal(have, want) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	staging, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.part")
	if err != nil {
		return false, err
	}
	defer os.Remove(staging.Name())
	if _, err := staging.Write(want); err != nil {
		return false, errors.Join(err, staging.Close())
	}
	if err := staging.Close(); err != nil {
		return false, err
	}
	if err := os.Chmod(staging.Name(), 0o644); err != nil {
		return false, err
	}
	return true, os.Rename(staging.Name(), path)
}

// UnregisterPlugin removes the plugin of the board at home from the global
// config directory dir, or every board's when home is empty. Only a file this
// package wrote is removed: one carrying the tag on its first line.
func UnregisterPlugin(dir, home string) (changed bool, err error) {
	paths := []string{PluginPath(dir, home)}
	if home == "" {
		paths, err = filepath.Glob(filepath.Join(dir, "plugins", pluginPrefix+"*.js"))
		if err != nil {
			return false, err
		}
	}
	var errs []error
	for _, path := range paths {
		owner, ok := pluginOwner(path)
		if !ok || home != "" && owner != home {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
			continue
		}
		changed = true
	}
	return changed, errors.Join(errs...)
}

// PluginRegistered reports whether dir holds p's plugin exactly as this build
// writes it.
func PluginRegistered(dir string, p Plugin) bool {
	have, err := os.ReadFile(PluginPath(dir, p.Home))
	return err == nil && bytes.Equal(have, p.Source())
}

// pluginOwner reads the board home a plugin file names on its tag line.
func pluginOwner(path string) (string, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	first, _, _ := strings.Cut(string(raw), "\n")
	rest, ok := strings.CutPrefix(first, pluginTag)
	if !ok {
		return "", false
	}
	var home string
	if err := json.Unmarshal([]byte(rest), &home); err != nil {
		return "", false
	}
	return home, true
}

// Installed reports whether an opencode binary is on this machine: on PATH,
// or where its installer puts it.
func Installed(lookPath func(string) (string, error)) bool {
	if _, err := lookPath("opencode"); err == nil {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	info, err := os.Stat(filepath.Join(home, ".opencode", "bin", "opencode"))
	return err == nil && !info.IsDir()
}

// ErrNotInstalled is the reason the board leaves the plugin out.
var ErrNotInstalled = fmt.Errorf("no opencode installed")
