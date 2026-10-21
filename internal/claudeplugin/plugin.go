// Package claudeplugin is Gate Inbox as one Claude Code plugin: the hooks
// every launched claude session carries, the merge-gate mod, and the skills
// and commands the board ships, loaded with a single --plugin-dir.
//
// Before it, a launch reached Claude Code through four channels: the hooks
// in a generated --settings file, the MCP server in --mcp-config, the
// delegation steering in --append-system-prompt-file, and the merge-gate mod
// in a --plugin-dir that only the board's startup wrote, and only when the
// merge authority was configured, so a launch that found it missing went
// without and said nothing. The plugin is written by the launch that loads it.
//
// Two things stay outside it. The --settings file keeps the key-dir
// permission and sandbox denials, which a plugin cannot carry. And the MCP
// server keeps its --mcp-config: Claude Code names a plugin's server tools
// mcp__plugin_<plugin>_<server>__*, and every permission rule, allowed-tools
// list and hook that names mcp__gate-inbox__* would stop matching.
//
// What a unit adds goes in plugindir: a skill under skills/<name>/SKILL.md,
// a command under commands/, a mod module as hooks/<name>.ts, which
// hooks.json lists under modules. A command hook is a Hook passed to
// register from an init function in a file of its own.
package claudeplugin

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/tracing"
)

// Name is the plugin's name, which namespaces its skills and commands
// (gate-inbox:<skill>).
const Name = "gate-inbox"

const description = "Gate Inbox in a managed Claude Code session: status hooks, dialog forwarding, " +
	"the merge gate, and the skills for delegating to board sessions"

// shipped is the plugin as the repository ships it. The dotted directory
// Claude Code wants (.claude-plugin) cannot be embedded, so a source file
// under claude-plugin/ is written under .claude-plugin/. A .gitkeep only
// holds an empty directory open for later units and is not written.
//
//go:embed all:plugindir
var shipped embed.FS

// Hook is one command hook a unit adds beside the launch hooks.
type Hook struct {
	Event   string
	Matcher string
	Command string
}

var contributed []Hook

// register adds command hooks to hooks.json. Call it from an init function;
// entries keep the order files initialise in, after the launch hooks.
func register(hook ...Hook) {
	contributed = append(contributed, hook...)
}

// Dir is where the plugin's builds live under configDir. Each build of
// the content gets a directory of its own (see Path).
func Dir(configDir string) string {
	return filepath.Join(configDir, "plugins", Name)
}

// Path is the directory Ensure writes, without writing it: a dry run needs
// the path the command line will carry.
func Path(configDir string) (string, error) {
	files, err := content()
	if err != nil {
		return "", err
	}
	return filepath.Join(Dir(configDir), stamp(files)), nil
}

// Ensure writes this build's plugin under configDir and returns its
// directory, rewriting any file whose content drifted.
//
// The directory is named for its content, for the reason hooks.GeneratedName
// gives: every process that launches a session writes the plugin it was
// built with, an MCP server can be weeks older than the board, and under one
// fixed directory an old build would put its old hooks back for everyone. An
// interactive claude also watches a --plugin-dir and reloads it on change,
// so a directory a running session loaded must never be rewritten with
// other content; under its own name only the same content ever lands there.
func Ensure(configDir string) (string, error) {
	files, err := content()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(Dir(configDir), stamp(files))
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		if err := writeNew(dir, files); err == nil {
			return dir, nil
		}
		// Another launch may have won the rename; fall through and check
		// its files like any existing directory's.
	}
	for _, rel := range sortedKeys(files) {
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if have, err := os.ReadFile(target); err == nil && bytes.Equal(have, files[rel]) {
			continue
		}
		if err := writeFile(target, files[rel]); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// writeNew builds the directory beside its final place and renames it in,
// so a launch never loads half a plugin.
func writeNew(dir string, files map[string][]byte) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), ".new-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	for rel, data := range files {
		target := filepath.Join(tmp, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return err
		}
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, dir)
}

// writeFile replaces one file through a temporary name of its own, so two
// launches repairing the same file never write into each other's.
func writeFile(target string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), target)
}

// content is every file of the plugin by its slash path, plugin.json
// included.
func content() (map[string][]byte, error) {
	files := map[string][]byte{}
	var modules []string
	err := fs.WalkDir(shipped, "plugindir", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || entry.Name() == ".gitkeep" {
			return err
		}
		rel := strings.TrimPrefix(name, "plugindir/")
		if rest, ok := strings.CutPrefix(rel, "claude-plugin/"); ok {
			rel = ".claude-plugin/" + rest
		}
		if path.Dir(rel) == "hooks" && path.Ext(rel) == ".ts" {
			modules = append(modules, "./"+path.Base(rel))
		}
		data, err := shipped.ReadFile(name)
		if err != nil {
			return err
		}
		files[rel] = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(modules)
	hooksJSON, err := hooksFile(modules)
	if err != nil {
		return nil, err
	}
	files["hooks/hooks.json"] = hooksJSON
	manifest, err := json.MarshalIndent(map[string]string{
		"name":        Name,
		"version":     Version(stamp(files)),
		"description": description,
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	files[".claude-plugin/plugin.json"] = append(manifest, '\n')
	return files, nil
}

// hooksFile is hooks/hooks.json: the launch hooks, then what units
// registered, and the mod modules.
func hooksFile(modules []string) ([]byte, error) {
	events := hooks.LaunchHooks()
	for _, hook := range contributed {
		events[hook.Event] = append(events[hook.Event], hooks.HookMatcher{
			Matcher: hook.Matcher,
			Hooks:   []hooks.HookCommand{{Type: "command", Command: hook.Command}},
		})
	}
	file := struct {
		Modules []string                       `json:"modules,omitempty"`
		Hooks   map[string][]hooks.HookMatcher `json:"hooks"`
	}{modules, events}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// stamp names the content of every file but the manifest, which carries the
// stamp itself.
func stamp(files map[string][]byte) string {
	sum := sha256.New()
	for _, rel := range sortedKeys(files) {
		if rel == ".claude-plugin/plugin.json" {
			continue
		}
		sum.Write([]byte(rel))
		sum.Write([]byte{0})
		sum.Write(files[rel])
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil))[:12]
}

// Version is the manifest's version: the release the binary was built as,
// with the content stamp as build metadata, so two builds of one release
// that ship different plugins still say so.
func Version(contentStamp string) string {
	release := strings.TrimPrefix(strings.TrimSpace(tracing.Release), "v")
	if release == "" {
		release = "0.0.0-dev"
	}
	release, _, _ = strings.Cut(release, "+")
	return release + "+" + contentStamp
}

func sortedKeys(files map[string][]byte) []string {
	keys := make([]string, 0, len(files))
	for key := range files {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// Argv is how the plugin appears on a launched session's command line.
func Argv(quotedDir string) string {
	return "--plugin-dir " + quotedDir
}
