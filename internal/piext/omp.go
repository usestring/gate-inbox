package piext

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// omp (oh-my-pi), pi's fork, runs the extension but has no registerMcpServer,
// so an omp launch gets the board's MCP server from a plugin directory it
// loads with --plugin-dir instead: a Claude Code plugin holding the server in
// its .mcp.json, and the board's skills, which omp does not take from an
// extension.
const (
	ompPluginName = "gate-inbox"
	ompServerName = "board"
	// boardServer is the board's server in the launch's MCP config
	// (MCPConfigEnv), under the name Claude Code knows it by.
	boardServer = "gate-inbox"
)

// OmpServer is the board's server as omp names it: <plugin>:<server>.
const OmpServer = ompPluginName + ":" + ompServerName

// OmpToolPrefix is how omp names the board's tools. It names a plugin's
// server <plugin>:<server> and a tool mcp__<server>_<tool>, lowercased with
// every other character an underscore, so the board's rename reaches the
// model as mcp__gate_inbox_board_rename.
const OmpToolPrefix = "mcp__gate_inbox_board_"

// OmpPluginArgv is the launch argument that loads the plugin at quotedPath,
// which the caller has already shell-quoted.
func OmpPluginArgv(quotedPath string) string {
	return "--plugin-dir " + quotedPath
}

// OmpPluginPath is where session id's plugin lives: beside the launch's MCP
// config, so a config holding the session's caller token keeps it in the key
// directory, out of the sandbox's reach.
func OmpPluginPath(mcpConfig, id string) string {
	if id == "" {
		id = "default"
	}
	return filepath.Join(filepath.Dir(mcpConfig), "omp-plugin-"+id)
}

// EnsureOmpPlugin writes session id's plugin from the launch's MCP config and
// links the board's skills directory into it, and returns its path.
func EnsureOmpPlugin(mcpConfig, id, skills string) (string, error) {
	raw, err := os.ReadFile(mcpConfig)
	if err != nil {
		return "", err
	}
	var parsed struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("%s: %w", mcpConfig, err)
	}
	entry, ok := parsed.MCPServers[boardServer]
	if !ok {
		return "", fmt.Errorf("%s has no %s server", mcpConfig, boardServer)
	}
	dir := OmpPluginPath(mcpConfig, id)
	if err := os.MkdirAll(filepath.Join(dir, ".claude-plugin"), 0o700); err != nil {
		return "", err
	}
	manifest, _ := json.Marshal(map[string]string{"name": ompPluginName})
	servers, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]json.RawMessage{ompServerName: entry}}, "", "  ")
	for name, content := range map[string][]byte{
		filepath.Join(".claude-plugin", "plugin.json"): manifest,
		".mcp.json": servers,
	} {
		if err := writeFile(filepath.Join(dir, name), content); err != nil {
			return "", err
		}
	}
	link := filepath.Join(dir, "skills")
	if target, err := os.Readlink(link); err != nil || target != skills {
		_ = os.Remove(link)
		if err := os.Symlink(skills, link); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// writeFile replaces path's content whole, so omp never reads half a file.
func writeFile(path string, content []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".omp-plugin-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
