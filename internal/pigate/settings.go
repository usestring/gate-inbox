package pigate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
)

// Source is one Claude Code settings file a pi session's calls are judged by.
type Source struct {
	// Label names the file in a decision's reason.
	Label string
	Path  string
	// Base is the directory a rule written as /path is read from.
	Base string
}

// Rules is one source's permission lists.
type Rules struct {
	Source           Source
	Allow, Deny, Ask []string
}

// managedDir is where Claude Code reads the machine's managed settings.
func managedDir() string {
	if runtime.GOOS == "darwin" {
		return "/Library/Application Support/ClaudeCode"
	}
	return "/etc/claude-code"
}

// Sources is every settings file Claude Code would read for a session in
// cwd, as the operator decided pi inherits them: the machine's managed
// settings (the file and its drop-in directory), the user's, and the
// project's shared and local files. Order does not decide anything, since
// deny beats ask beats allow whichever file holds them.
func Sources(claudeHome, home, cwd string) []Source {
	var out []Source
	managed := managedDir()
	out = append(out, Source{Label: "managed settings", Path: filepath.Join(managed, "managed-settings.json"), Base: managed})
	if drops, err := filepath.Glob(filepath.Join(managed, "managed-settings.d", "*.json")); err == nil {
		slices.Sort(drops)
		for _, p := range drops {
			out = append(out, Source{Label: "managed settings", Path: p, Base: managed})
		}
	}
	if claudeHome != "" {
		out = append(out, Source{Label: "user settings", Path: filepath.Join(claudeHome, "settings.json"), Base: home})
	}
	if cwd != "" && filepath.IsAbs(cwd) {
		out = append(out,
			Source{Label: "project settings", Path: filepath.Join(cwd, ".claude", "settings.json"), Base: cwd},
			Source{Label: "local project settings", Path: filepath.Join(cwd, ".claude", "settings.local.json"), Base: cwd},
		)
	}
	return out
}

// Load reads each source's permission lists. A file that is missing or does
// not parse contributes nothing, as Claude Code skips it.
func Load(sources []Source) []Rules {
	var out []Rules
	for _, s := range sources {
		raw, err := os.ReadFile(s.Path)
		if err != nil {
			continue
		}
		var doc struct {
			Permissions struct {
				Allow []string `json:"allow"`
				Deny  []string `json:"deny"`
				Ask   []string `json:"ask"`
			} `json:"permissions"`
		}
		if json.Unmarshal(raw, &doc) != nil {
			continue
		}
		p := doc.Permissions
		if len(p.Allow)+len(p.Deny)+len(p.Ask) == 0 {
			continue
		}
		out = append(out, Rules{Source: s, Allow: p.Allow, Deny: p.Deny, Ask: p.Ask})
	}
	return out
}
