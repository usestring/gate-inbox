package mergegate

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// pluginFiles is the merge-gate mod as the repository ships it. The dotted
// directory Claude Code wants (.claude-plugin) cannot be embedded, so the
// source spells it without the dot and EnsurePluginDir restores it on write.
//
//go:embed plugindir
var pluginFiles embed.FS

// PluginDir is where the board keeps the merge-gate mod it loads into the
// sessions it launches: under the manager's own home, beside its config and
// state, so every checkout and every install finds the same path.
func PluginDir(configDir string) string {
	return filepath.Join(configDir, "plugins", "merge-gate")
}

// EnsurePluginDir writes the shipped mod under configDir, refreshing files
// whose content drifted, and returns its directory. Files it does not know
// are left alone.
func EnsurePluginDir(configDir string) (string, error) {
	dir := PluginDir(configDir)
	err := fs.WalkDir(pluginFiles, "plugindir", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, err := filepath.Rel("plugindir", path)
		if err != nil {
			return err
		}
		if rest, ok := strings.CutPrefix(rel, "claude-plugin"); ok {
			rel = filepath.Join(".claude-plugin", rest)
		}
		want, err := pluginFiles.ReadFile(path)
		if err != nil {
			return err
		}
		target := filepath.Join(dir, rel)
		if have, err := os.ReadFile(target); err == nil && string(have) == string(want) {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, want, 0o644)
	})
	if err != nil {
		return "", err
	}
	return dir, nil
}
