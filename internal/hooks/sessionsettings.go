package hooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
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
//
// A session whose tier carries an auto-compact window (tools.<name>.compact_window)
// gets its own file for the same reason: the window rides in it as
// autoCompactWindow, beside whatever grants it holds. Claude Code 2.1.304
// reads it at launch only (measured: a session launched on 300000 still
// reported 300000 from /autocompact after the file was rewritten to 600000,
// whether replaced by rename or written in place), and
// CLAUDE_CODE_AUTO_COMPACT_WINDOW in its environment overrides it.

// sessionSettingsPrefix starts every settings file a launch carries, the
// shared one and each session's own. The poller looks for it in a running
// process's argv to tell a wired session from one restarted by hand.
const sessionSettingsPrefix = "claude-settings"

// SessionSettingsPath is the settings file of a session that has been granted
// permissions or given an auto-compact window.
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
// grants merged in, keeping the auto-compact window the file already carries.
// With no grants and no window it removes the file, and the session goes back
// to the shared one at its next launch.
func (m *Manager) WriteSessionSettings(id string, grants []grant.Grant) (string, error) {
	if err := checkID(id); err != nil {
		return "", err
	}
	path := m.SessionSettingsPath(id)
	// A file that cannot be read keeps no window rather than blocking the
	// grant: the poller puts the window back on its next pass.
	window, _ := m.SessionCompactWindow(id)
	grants = append(slices.Clone(grants), m.StandingGrants()...)
	if len(grants) == 0 && window == 0 {
		return path, removeIfExists(path)
	}
	settings, err := m.hookSettings()
	if err != nil {
		return "", err
	}
	grant.Apply(settings, grants)
	if window > 0 {
		settings[compactWindowKey] = window
	}
	return path, writeSettings(m.dir, path, settings)
}

// compactWindowKey is the Claude Code setting the window travels as.
const compactWindowKey = "autoCompactWindow"

// SessionCompactWindow is the auto-compact window id's own settings file
// carries, or 0 when it has no file or the file sets none.
func (m *Manager) SessionCompactWindow(id string) (int, error) {
	settings, err := m.readSessionSettings(id)
	if settings == nil || err != nil {
		return 0, err
	}
	return compactWindow(settings), nil
}

// SetSessionCompactWindow puts window into id's own settings file, making the
// file when the session had none; 0 takes it out, and a file left with
// neither a window nor a grant is removed so the session goes back to the
// shared one. It writes nothing when the file already says window, so the
// poller can call it on every pass.
func (m *Manager) SetSessionCompactWindow(id string, window int) error {
	if err := checkID(id); err != nil {
		return err
	}
	path := m.SessionSettingsPath(id)
	settings, err := m.readSessionSettings(id)
	if err != nil {
		return err
	}
	if settings == nil {
		if window == 0 {
			return nil
		}
		if settings, err = m.hookSettings(); err != nil {
			return err
		}
		grant.Apply(settings, m.StandingGrants())
	}
	if compactWindow(settings) == window {
		return nil
	}
	if window > 0 {
		settings[compactWindowKey] = window
		return writeSettings(m.dir, path, settings)
	}
	delete(settings, compactWindowKey)
	bare, err := m.carriesNothing(settings)
	if err != nil {
		return err
	}
	if bare {
		return removeIfExists(path)
	}
	return writeSettings(m.dir, path, settings)
}

// readSessionSettings is id's own settings file, or nil when it has none.
func (m *Manager) readSessionSettings(id string) (map[string]any, error) {
	path := m.SessionSettingsPath(id)
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	settings := map[string]any{}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return nil, fmt.Errorf("hooks: %s is not JSON: %w", path, err)
	}
	return settings, nil
}

// carriesNothing reports whether settings is no more than the shared hooks
// file would give a session with no grants: nothing a launch on the shared
// file would miss.
func (m *Manager) carriesNothing(settings map[string]any) (bool, error) {
	if len(m.StandingGrants()) > 0 {
		return false, nil
	}
	base, err := m.hookSettings()
	if err != nil {
		return false, err
	}
	trimmed := maps.Clone(settings)
	delete(trimmed, "hooks")
	have, err := json.Marshal(trimmed)
	if err != nil {
		return false, err
	}
	want, err := json.Marshal(base)
	if err != nil {
		return false, err
	}
	return bytes.Equal(have, want), nil
}

// compactWindow reads the window out of a decoded settings file.
func compactWindow(settings map[string]any) int {
	switch v := settings[compactWindowKey].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
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
	// The hooks ride the Gate Inbox plugin now. A file written before it
	// still carries them, and beside the plugin's they would fire twice.
	delete(settings, "hooks")
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
