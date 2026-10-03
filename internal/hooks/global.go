package hooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/parentseal"
)

// Global hooks.
//
// A session the board launches carries its hooks on its own command line
// (--settings), and a session somebody started with a plain `claude` carries
// none. Adoption puts the second kind on the board, but its status, its
// dialogs and the notes on what is typed into it all come from hooks it never
// loaded, and Claude Code reads hooks only at startup. So the same hooks are
// also registered in the user's own settings file, where every claude process
// loads them, and each one finds out when it fires whether its session is on
// the board.
//
// Each global command opens with a prelude that ends the hook at once unless
// three things hold, all of them read without starting a process:
//
//   - GATE_INBOX_STATUS_FILE is unset. A session the board launched has it,
//     and its --settings hooks already fire; running both would log every
//     event twice.
//   - The pane the process runs in has an adoption marker, which the board
//     writes under hooks/adopted/ for every adopted pane it holds. The marker
//     is named for the tmux server's pid and the pane id, the two halves of
//     $TMUX and $TMUX_PANE that tell one server's %3 from another's.
//   - The marker names this hook's parent process. Claude Code runs a hook as
//     its own child, and the marker carries the pid of the claude the board
//     found in that pane, so a claude started from inside the adopted one --
//     a `claude -p` from its Bash tool shares the pane and the environment --
//     does not report as it.
//
// When they hold, the prelude exports what a launch would have put in the
// environment and runs the launched session's own command for that event. A
// session that joins the board later starts working on its next hook, with
// nothing restarted.

// globalTag opens every global command, so this package can find the entries
// it wrote in a file that is otherwise the operator's.
const globalTag = ": gate-inbox-global-hook"

// adoptedDirName holds one marker per adopted pane.
const adoptedDirName = "adopted"

// globalDisabledName is the file that keeps the board from registering the
// global hooks again after an operator removed them.
const globalDisabledName = "global-hooks.disabled"

// AdoptedDir is where the board keeps the adoption markers the global hooks
// read.
func (m *Manager) AdoptedDir() string {
	return filepath.Join(m.dir, adoptedDirName)
}

// AdoptedPane is one adopted claude the global hooks should answer for: the
// row it is on the board as, and where its hooks run.
type AdoptedPane struct {
	ID string
	// ServerPID and PaneID are what the hook reads off $TMUX and $TMUX_PANE.
	ServerPID int
	PaneID    string
	// AgentPID is the claude process itself, which is its hooks' parent.
	AgentPID int
}

var paneIDPattern = regexp.MustCompile(`^%[0-9]+$`)

func adoptedMarkerName(pane AdoptedPane) string {
	return strconv.Itoa(pane.ServerPID) + pane.PaneID
}

// SyncAdopted makes the markers exactly panes: each one written, and every
// other marker removed, so a pane the board let go of stops reporting. A pane
// that cannot be named safely is skipped rather than failing the rest.
func (m *Manager) SyncAdopted(panes []AdoptedPane) error {
	dir := m.AdoptedDir()
	wanted := map[string]string{}
	for _, pane := range panes {
		if checkID(pane.ID) != nil || !paneIDPattern.MatchString(pane.PaneID) || pane.ServerPID <= 0 || pane.AgentPID <= 0 {
			continue
		}
		wanted[adoptedMarkerName(pane)] = pane.ID + " " + strconv.Itoa(pane.AgentPID) + "\n"
	}
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var errs []error
	for _, entry := range entries {
		if _, keep := wanted[entry.Name()]; keep {
			continue
		}
		errs = append(errs, removeIfExists(filepath.Join(dir, entry.Name())))
	}
	if len(wanted) == 0 {
		return errors.Join(errs...)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for name, content := range wanted {
		path := filepath.Join(dir, name)
		if existing, err := os.ReadFile(path); err == nil && string(existing) == content {
			continue
		}
		errs = append(errs, WriteWhole(path, content))
	}
	return errors.Join(errs...)
}

// shellQuote makes s one word for sh, whatever it holds.
func shellQuote(s string) string {
	return `'` + strings.ReplaceAll(s, `'`, `'\''`) + `'`
}

// globalMark is the tag with the config directory it belongs to, so two
// boards on one machine -- each with its own GATE_INBOX_HOME -- each replace
// only their own entries.
func globalMark(configDir string) string {
	return globalTag + " " + shellQuote(configDir) + "; "
}

// globalPrelude is what every global command runs before the launched
// session's own; see the package comment above.
func globalPrelude(configDir, bin string) string {
	hooksDir := filepath.Join(configDir, "hooks")
	return globalMark(configDir) +
		`[ -z "$` + EnvStatusFile + `" ] || exit 0; [ -n "$TMUX_PANE" ] || exit 0; ` +
		`t="${TMUX#*,}"; m=` + shellQuote(filepath.Join(hooksDir, adoptedDirName)+"/") + `"${t%%,*}$TMUX_PANE"; ` +
		`[ -f "$m" ] || exit 0; read -r i p < "$m" || exit 0; [ "$p" = "$PPID" ] || exit 0; ` +
		`case "$i" in ''|*[!A-Za-z0-9_-]*) exit 0;; esac; ` +
		EnvSessionID + `="$i"; ` +
		EnvStatusFile + `=` + shellQuote(hooksDir+"/") + `"$i.status"; ` +
		EnvExecutable + `=` + shellQuote(bin) + `; ` +
		config.HomeEnv + `=` + shellQuote(configDir) + `; ` +
		`export ` + EnvSessionID + ` ` + EnvStatusFile + ` ` + EnvExecutable + ` ` + config.HomeEnv + `; `
}

// globalHooks is the launched session's hooks, event for event and matcher
// for matcher, each behind the prelude. The permission and sandbox denials a
// launch carries are not among them: they would bind every claude on the
// machine, and an operator's own list is not this package's to edit.
func globalHooks(configDir, bin string) (map[string][]hookMatcher, error) {
	content, err := settingsContent(parentseal.KeyDir(configDir))
	if err != nil {
		return nil, err
	}
	var launched settingsFile
	if err := json.Unmarshal(content, &launched); err != nil {
		return nil, err
	}
	prelude := globalPrelude(configDir, bin)
	for event, matchers := range launched.Hooks {
		for i := range matchers {
			for j := range matchers[i].Hooks {
				matchers[i].Hooks[j].Command = prelude + matchers[i].Hooks[j].Command
			}
		}
		launched.Hooks[event] = matchers
	}
	return launched.Hooks, nil
}

// GlobalSettingsPath is the user settings file every claude process reads:
// under CLAUDE_CONFIG_DIR when it is set, as Claude Code itself does, and
// ~/.claude otherwise.
func GlobalSettingsPath() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return filepath.Join(dir, "settings.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

// RegisterGlobal puts this board's global hooks into the settings file at
// path, replacing any it wrote before and leaving every other entry where it
// was. changed is false when the file already said exactly this.
func RegisterGlobal(path, configDir, bin string) (changed bool, err error) {
	hooks, err := globalHooks(configDir, bin)
	if err != nil {
		return false, err
	}
	return rewriteSettings(path, globalMark(configDir), hooks)
}

// UnregisterGlobal removes the global hooks of the board at configDir, or of
// every board when configDir is empty.
func UnregisterGlobal(path, configDir string) (changed bool, err error) {
	mark := globalTag + " "
	if configDir != "" {
		mark = globalMark(configDir)
	}
	return rewriteSettings(path, mark, nil)
}

// GlobalRegistered reports whether the settings file at path carries this
// board's global hooks exactly as this build would write them.
func GlobalRegistered(path, configDir, bin string) (bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	hooks, err := globalHooks(configDir, bin)
	if err != nil {
		return false, err
	}
	merged, err := mergeSettings(raw, globalMark(configDir), hooks)
	if err != nil {
		return false, err
	}
	return bytes.Equal(merged, normalizeSettings(raw)), nil
}

// GlobalDisabled reports that an operator removed the global hooks and the
// board must not put them back.
func (m *Manager) GlobalDisabled() bool {
	_, err := os.Stat(filepath.Join(m.dir, globalDisabledName))
	return err == nil
}

// SetGlobalDisabled records, or clears, the operator's removal.
func (m *Manager) SetGlobalDisabled(disabled bool) error {
	path := filepath.Join(m.dir, globalDisabledName)
	if !disabled {
		return removeIfExists(path)
	}
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, nil, 0o644)
}

func rewriteSettings(path, mark string, hooks map[string][]hookMatcher) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	missing := err != nil
	if missing && len(hooks) == 0 {
		return false, nil
	}
	merged, err := mergeSettings(raw, mark, hooks)
	if err != nil {
		return false, err
	}
	if !missing && bytes.Equal(merged, normalizeSettings(raw)) {
		return false, nil
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*.json")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(merged); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	return true, os.Rename(tmp.Name(), path)
}

// normalizeSettings is raw as mergeSettings would print it, so a file that
// differs only in whitespace is not rewritten.
func normalizeSettings(raw []byte) []byte {
	var out bytes.Buffer
	if err := json.Indent(&out, bytes.TrimSpace(raw), "", "  "); err != nil {
		return raw
	}
	out.WriteByte('\n')
	return out.Bytes()
}

// member is one key of a JSON object, in the order the file has it.
type member struct {
	key   string
	value json.RawMessage
}

func decodeObject(raw []byte) ([]member, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, errors.New("not a JSON object")
	}
	var members []member
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := tok.(string)
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		members = append(members, member{key: key, value: value})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return members, nil
}

func encodeObject(members []member) []byte {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, m := range members {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, _ := json.Marshal(m.key)
		buf.Write(key)
		buf.WriteByte(':')
		buf.Write(m.value)
	}
	buf.WriteByte('}')
	return buf.Bytes()
}

func lookup(members []member, key string) (int, bool) {
	for i, m := range members {
		if m.key == key {
			return i, true
		}
	}
	return -1, false
}

// mergeSettings is raw with every hook command starting with mark taken out
// and hooks added at the end of their events. Everything else keeps its
// place and its value; a matcher group left empty by the removal goes, and
// so does an event or a hooks object left empty by it.
func mergeSettings(raw []byte, mark string, hooks map[string][]hookMatcher) ([]byte, error) {
	var top []member
	if len(bytes.TrimSpace(raw)) > 0 {
		var err error
		if top, err = decodeObject(raw); err != nil {
			return nil, fmt.Errorf("hooks: the settings file is not a JSON object: %w", err)
		}
	}
	var events []member
	at, hasHooks := lookup(top, "hooks")
	if hasHooks {
		var err error
		if events, err = decodeObject(top[at].value); err != nil {
			return nil, fmt.Errorf("hooks: the settings file's hooks are not a JSON object: %w", err)
		}
	}
	removedAny := false
	kept := events[:0:0]
	for _, ev := range events {
		var groups []json.RawMessage
		if err := json.Unmarshal(ev.value, &groups); err != nil {
			kept = append(kept, ev)
			continue
		}
		var survivors []json.RawMessage
		removed := false
		for _, group := range groups {
			pruned, dropped, empty, err := pruneGroup(group, mark)
			if err != nil {
				return nil, err
			}
			removed = removed || dropped
			if !empty || !dropped {
				survivors = append(survivors, pruned)
			}
		}
		removedAny = removedAny || removed
		if !removed {
			kept = append(kept, ev)
			continue
		}
		if len(survivors) == 0 {
			continue
		}
		value, err := json.Marshal(survivors)
		if err != nil {
			return nil, err
		}
		kept = append(kept, member{key: ev.key, value: value})
	}
	events = kept
	for _, name := range sortedEvents(hooks) {
		add, err := json.Marshal(hooks[name])
		if err != nil {
			return nil, err
		}
		i, ok := lookup(events, name)
		if !ok {
			events = append(events, member{key: name, value: add})
			continue
		}
		var groups []json.RawMessage
		if err := json.Unmarshal(events[i].value, &groups); err != nil {
			return nil, fmt.Errorf("hooks: the settings file's %s hooks are not a list: %w", name, err)
		}
		var ours []json.RawMessage
		if err := json.Unmarshal(add, &ours); err != nil {
			return nil, err
		}
		value, err := json.Marshal(append(groups, ours...))
		if err != nil {
			return nil, err
		}
		events[i].value = value
	}
	switch {
	case hasHooks && len(events) == 0 && removedAny:
		top = append(top[:at:at], top[at+1:]...)
	case hasHooks:
		top[at].value = encodeObject(events)
	case len(events) > 0:
		top = append(top, member{key: "hooks", value: encodeObject(events)})
	}
	var out bytes.Buffer
	if err := json.Indent(&out, encodeObject(top), "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// pruneGroup drops the commands starting with mark from one matcher group.
// dropped says some were, empty that none are left.
func pruneGroup(group json.RawMessage, mark string) (pruned json.RawMessage, dropped, empty bool, err error) {
	fields, err := decodeObject(group)
	if err != nil {
		return group, false, false, nil
	}
	at, ok := lookup(fields, "hooks")
	if !ok {
		return group, false, false, nil
	}
	var commands []json.RawMessage
	if json.Unmarshal(fields[at].value, &commands) != nil {
		return group, false, false, nil
	}
	var keep []json.RawMessage
	for _, raw := range commands {
		var command struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(raw, &command) == nil && strings.HasPrefix(command.Command, mark) {
			dropped = true
			continue
		}
		keep = append(keep, raw)
	}
	if !dropped {
		return group, false, false, nil
	}
	if keep == nil {
		keep = []json.RawMessage{}
	}
	value, err := json.Marshal(keep)
	if err != nil {
		return nil, false, false, err
	}
	fields[at].value = value
	return encodeObject(fields), true, len(keep) == 0, nil
}

// sortedEvents fixes the order new events are added in, so two runs write
// the same bytes.
func sortedEvents(hooks map[string][]hookMatcher) []string {
	names := make([]string, 0, len(hooks))
	for name := range hooks {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
