package hooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/usestring/gate-inbox/grant"
	"github.com/usestring/gate-inbox/internal/parentseal"
)

// A session's own settings file.
//
// Every claude session launches with the one shared hooks file. A session
// whose parent granted it a permission launches with a file of its own
// instead: the same hooks, plus the grants. It cannot be the shared file,
// which every session reads, and it cannot be a project's
// settings.local.json, which every session in that directory reads --
// including the parent, its siblings and the user's own, and for a spawn
// diverted past the trust dialog, the parent's checkout itself. And it cannot
// be a second --settings flag beside the shared one: Claude Code 2.1.286 keeps
// only the last --settings it is given (measured: a deny in the first file of
// two was not applied), so the hooks and the grants have to travel in one
// file.

// sessionSettingsPrefix starts every settings file a launch carries, the
// shared one and each session's own. The poller looks for it in a running
// process's argv to tell a wired session from one restarted by hand.
const sessionSettingsPrefix = "claude-settings"

// SessionSettingsPath is the settings file of a session that has been granted
// permissions.
func (m *Manager) SessionSettingsPath(id string) string {
	return filepath.Join(m.dir, sessionSettingsPrefix+"."+id+".json")
}

// LaunchSettingsPath is the settings file a launch of id carries: its own
// when it has one, the shared file otherwise.
func (m *Manager) LaunchSettingsPath(id string) string {
	if id != "" {
		if _, err := os.Stat(m.SessionSettingsPath(id)); err == nil {
			return m.SessionSettingsPath(id)
		}
	}
	return m.SettingsPath()
}

// WriteSessionSettings writes id's own settings file: the shared hooks with
// grants merged in. With no grants it removes the file, and the session goes
// back to the shared one at its next launch.
func (m *Manager) WriteSessionSettings(id string, grants []grant.Grant) (string, error) {
	if err := checkID(id); err != nil {
		return "", err
	}
	path := m.SessionSettingsPath(id)
	grants = append(slices.Clone(grants), m.StandingGrants()...)
	if len(grants) == 0 {
		return path, removeIfExists(path)
	}
	settings, err := m.hookSettings()
	if err != nil {
		return "", err
	}
	grant.Apply(settings, grants)
	return path, writeSettings(m.dir, path, settings)
}

// RefreshSessionSettings rewrites id's own settings file, when it has one,
// with the current hooks and the grants it already carries. A launch calls it
// for the same reason it refreshes the shared file: an upgrade can change the
// hooks, and a granted session must not keep the old ones.
func (m *Manager) RefreshSessionSettings(id string) error {
	if id == "" {
		return nil
	}
	path := m.SessionSettingsPath(id)
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if len(m.StandingGrants()) == 0 {
			return nil
		}
		_, err := m.WriteSessionSettings(id, nil)
		return err
	}
	if err != nil {
		return err
	}
	settings := map[string]any{}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return fmt.Errorf("hooks: %s is not JSON: %w", path, err)
	}
	fresh, err := m.hookSettings()
	if err != nil {
		return err
	}
	settings["hooks"] = fresh["hooks"]
	grant.Apply(settings, m.StandingGrants())
	return writeSettings(m.dir, path, settings)
}

// Standing grants.
//
// A permission the user approved once for every session stands until it is
// revoked: each Claude Code session Gate Inbox launches carries it in its own
// settings file. Before this a grant lasted two hours on one child, so the
// same command prefix came back as a fresh approval question for the same
// child after its window, for its siblings, and for the next session (8 of 20
// grants measured to 2026-10-11 re-granted a prefix already approved). The
// store keeps the record; this file is the copy a launch reads, because a
// launch does not open the store.

// StandingGrantsFile is where the standing grants are kept for launches.
func (m *Manager) StandingGrantsFile() string {
	return filepath.Join(m.dir, "standing-grants.json")
}

// WriteStandingGrants replaces the standing grants a launch reads.
func (m *Manager) WriteStandingGrants(grants []grant.Grant) error {
	if len(grants) == 0 {
		return removeIfExists(m.StandingGrantsFile())
	}
	body, err := json.Marshal(grants)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return err
	}
	return WriteWhole(m.StandingGrantsFile(), string(body))
}

// StandingGrants is every standing grant, each passed through grant.Check
// again so a hand-edited file cannot widen past what a grant may be. A file
// that cannot be read grants nothing.
func (m *Manager) StandingGrants() []grant.Grant {
	raw, err := os.ReadFile(m.StandingGrantsFile())
	if err != nil {
		return nil
	}
	var grants []grant.Grant
	if err := json.Unmarshal(raw, &grants); err != nil {
		return nil
	}
	return slices.DeleteFunc(grants, func(g grant.Grant) bool { return grant.Check(grant.Normalise(g)) != nil })
}

func (m *Manager) hookSettings() (map[string]any, error) {
	content, err := settingsContent(parentseal.KeyDir(m.root))
	if err != nil {
		return nil, err
	}
	settings := map[string]any{}
	return settings, json.Unmarshal(content, &settings)
}

func writeSettings(dir, path string, settings map[string]any) error {
	body, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(body, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// checkID refuses an id that would put the file anywhere but beside the
// shared one.
func checkID(id string) error {
	if id == "" || strings.ContainsAny(id, `/\.`) {
		return fmt.Errorf("hooks: %q is not a session id", id)
	}
	return nil
}
