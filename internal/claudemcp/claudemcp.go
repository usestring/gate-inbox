// Package claudemcp reads the MCP servers a Claude Code session started in a
// directory would connect to, so the board can give a pi or omp session the
// same ones.
//
// Claude Code keeps them in three scopes:
//
//   - local: projects[<project>].mcpServers in ~/.claude.json, for the
//     session's project (ProjectKey);
//   - user: the top-level mcpServers in ~/.claude.json;
//   - project: a .mcp.json in the session's directory or one above it, used
//     only once approved (enableAllProjectMcpServers, or the server named in
//     enabledMcpjsonServers) and never when named in disabledMcpjsonServers.
//
// A name in more than one scope takes the first of local, user and project,
// which is the entry `claude mcp get` reports for it. A server the project
// entry lists in disabledMcpServers (Claude Code's /mcp disable) is left out.
package claudemcp

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Servers are mcpServers entries by name, each in the shape Claude Code
// writes them.
type Servers map[string]json.RawMessage

// claudeJSON is the part of ~/.claude.json read here.
type claudeJSON struct {
	MCPServers Servers                 `json:"mcpServers"`
	Projects   map[string]projectEntry `json:"projects"`
}

type projectEntry struct {
	MCPServers                 Servers  `json:"mcpServers"`
	DisabledMCPServers         []string `json:"disabledMcpServers"`
	EnabledMCPJSONServers      []string `json:"enabledMcpjsonServers"`
	DisabledMCPJSONServers     []string `json:"disabledMcpjsonServers"`
	EnableAllProjectMCPServers bool     `json:"enableAllProjectMcpServers"`
}

// Read returns the servers a Claude Code session in dir would connect to,
// for the user whose home is home, less those named in skip. Values in each
// entry have ${VAR} and ${VAR:-default} expanded from lookup, as Claude Code
// expands them. A missing file is no servers, not an error.
func Read(home, dir string, skip []string, lookup func(string) (string, bool)) (Servers, error) {
	var user claudeJSON
	if err := readJSON(filepath.Join(home, ".claude.json"), &user); err != nil {
		return nil, err
	}
	dir = filepath.Clean(dir)
	local := user.Projects[ProjectKey(dir)]
	enableAll := local.EnableAllProjectMCPServers
	for _, file := range settingsFiles(home, dir) {
		var s struct {
			EnableAll *bool    `json:"enableAllProjectMcpServers"`
			Enabled   []string `json:"enabledMcpjsonServers"`
			Disabled  []string `json:"disabledMcpjsonServers"`
		}
		if err := readJSON(file, &s); err != nil {
			continue // Claude Code reports a broken settings file itself.
		}
		if s.EnableAll != nil {
			enableAll = enableAll || *s.EnableAll
		}
		local.EnabledMCPJSONServers = append(local.EnabledMCPJSONServers, s.Enabled...)
		local.DisabledMCPJSONServers = append(local.DisabledMCPJSONServers, s.Disabled...)
	}

	out := Servers{}
	add := func(name string, entry json.RawMessage) {
		if _, taken := out[name]; taken || slices.Contains(skip, name) || slices.Contains(local.DisabledMCPServers, name) {
			return
		}
		out[name] = expand(entry, lookup)
	}
	for name, entry := range local.MCPServers {
		add(name, entry)
	}
	for name, entry := range user.MCPServers {
		add(name, entry)
	}
	// The nearest .mcp.json wins over one further up.
	for d := dir; ; d = filepath.Dir(d) {
		var project struct {
			MCPServers Servers `json:"mcpServers"`
		}
		if err := readJSON(filepath.Join(d, ".mcp.json"), &project); err == nil {
			for name, entry := range project.MCPServers {
				approved := enableAll || slices.Contains(local.EnabledMCPJSONServers, name)
				if approved && !slices.Contains(local.DisabledMCPJSONServers, name) {
					add(name, entry)
				}
			}
		}
		if filepath.Dir(d) == d {
			break
		}
	}
	return out, nil
}

// ProjectKey is the projects entry in ~/.claude.json that holds a session's
// local scope: the root of the git repository dir is in, with a linked
// worktree's taken as its main repository's, or dir itself outside one.
// That is how Claude Code keys it, so every worktree of a repository shares
// the repository's local servers while a submodule keeps its own.
func ProjectKey(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		dotGit := filepath.Join(d, ".git")
		info, err := os.Stat(dotGit)
		if err == nil {
			if info.IsDir() {
				return d
			}
			return mainWorktree(d, dotGit)
		}
		if filepath.Dir(d) == d {
			return dir
		}
	}
}

// mainWorktree is the main repository's root for a checkout at root whose
// .git is a file: a linked worktree's gitdir names, in its commondir file,
// the repository's own .git; a submodule's gitdir has none, and the
// submodule is its own root.
func mainWorktree(root, dotGit string) string {
	raw, err := os.ReadFile(dotGit)
	if err != nil {
		return root
	}
	gitDir, ok := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir:")
	if !ok {
		return root
	}
	gitDir = strings.TrimSpace(gitDir)
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(root, gitDir)
	}
	common, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return root
	}
	commonDir := strings.TrimSpace(string(common))
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(gitDir, commonDir)
	}
	commonDir = filepath.Clean(commonDir)
	if filepath.Base(commonDir) != ".git" {
		return root
	}
	return filepath.Dir(commonDir)
}

// settingsFiles are the Claude Code settings files that can approve or
// refuse a project's .mcp.json servers for a session in dir.
func settingsFiles(home, dir string) []string {
	return []string{
		filepath.Join(home, ".claude", "settings.json"),
		filepath.Join(dir, ".claude", "settings.json"),
		filepath.Join(dir, ".claude", "settings.local.json"),
	}
}

// readJSON decodes path into v; a file that is not there leaves v alone.
func readJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

// expand replaces ${VAR} and ${VAR:-default} in every string of entry. A
// variable that is unset and has no default stays as written.
func expand(entry json.RawMessage, lookup func(string) (string, bool)) json.RawMessage {
	var v any
	if json.Unmarshal(entry, &v) != nil {
		return entry
	}
	var walk func(any) any
	walk = func(v any) any {
		switch t := v.(type) {
		case string:
			return envRef.ReplaceAllStringFunc(t, func(ref string) string {
				m := envRef.FindStringSubmatch(ref)
				if value, ok := lookup(m[1]); ok && value != "" {
					return value
				}
				if len(m[2]) > 0 || ref != "${"+m[1]+"}" {
					return m[2]
				}
				return ref
			})
		case []any:
			for i := range t {
				t[i] = walk(t[i])
			}
		case map[string]any:
			for k := range t {
				t[k] = walk(t[k])
			}
		}
		return v
	}
	out, err := json.Marshal(walk(v))
	if err != nil {
		return entry
	}
	return out
}
