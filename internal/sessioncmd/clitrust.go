package sessioncmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/usestring/gate-inbox/extension/gitroot"
	"github.com/usestring/gate-inbox/internal/mcprelay"
)

// trustedByCLI reports whether the CLI behind trust_store has already
// accepted its first-run trust dialog for dir, so a pane opened there starts
// without one. Only an entry for dir itself, or for the root of the git
// repository it sits in, counts. The CLIs' inheritance rules are undocumented
// and have changed (a submodule of a trusted repository asks again), and a
// wrong yes opens the pane on the very dialog the divert avoids, where a
// wrong no costs only the divert. A store it cannot read is a no.
var trustedByCLI = func(trustStore, dir string) bool {
	if strings.TrimSpace(trustStore) != "claude" || strings.TrimSpace(dir) == "" {
		return false
	}
	path, err := mcprelay.ClaudeStatePath()
	if err != nil {
		return false
	}
	return claudeTrusts(path, dir)
}

// claudeTrusts reads Claude Code's state file for a projects entry on dir,
// or on its repository root, whose trust dialog has been accepted.
func claudeTrusts(statePath, dir string) bool {
	raw, err := os.ReadFile(statePath)
	if err != nil {
		return false
	}
	var state struct {
		Projects map[string]struct {
			HasTrustDialogAccepted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return false
	}
	candidates := []string{filepath.Clean(dir)}
	if root, err := gitroot.Toplevel(dir, nil); err == nil && root != "" {
		candidates = append(candidates, filepath.Clean(root))
	}
	for _, candidate := range candidates {
		if state.Projects[candidate].HasTrustDialogAccepted {
			return true
		}
	}
	return false
}
