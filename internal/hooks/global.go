package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/parentseal"
	"github.com/usestring/gate-inbox/internal/singleton"
)

// Global hooks.
//
// A session the board launches carries its hooks on its own command line
// (--settings), and a session somebody started with a plain `claude` carries
// none. Adoption puts the second kind on the board, but its status, its
// dialogs and the notes on what is typed into it all come from hooks it never
// loaded, and Claude Code reads hooks only at startup. So the board also
// registers one entry per event it needs in the user's own settings file,
// where every claude process loads them, and each finds out when it fires
// whether its session is on the board.
//
// The file is the operator's, and most of the sessions reading it never meet
// the board, so an entry has to cost them nothing and say nothing. Each is a
// prelude of shell builtins that ends the hook, exit 0, no output, unless:
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
//     does not report as it, and a marker outliving its pane matches nothing.
//     A marker for an adopted codex or opencode carries a third field, the
//     tool, and sometimes a fourth, its conversation id, which the prelude's
//     two-variable read takes into the pid, so it never matches either.
//   - The board is running: the pid in its singleton lock answers kill -0.
//   - The installed binary is still there and executable.
//
// SessionStart alone does one thing more when the pane has no marker: with
// the board running, it announces the pane (arrivals.go), so the board can
// take it at once rather than on its next scan.
//
// Gate Inbox deleted without unregistering leaves entries that fail the first
// test they reach and stay silent. The prelude's own stderr goes to
// /dev/null, so nothing it trips over reaches the session either.
//
// When every test holds, the prelude exports what a launch would have put in
// the environment, with the claude's pid beside it, and hands the event to
// `gate-inbox hook global <event>`, which runs the launch's own commands for
// it (DispatchGlobal). A session that joins the board later starts working on
// its next hook, with nothing restarted.

// globalTag opens every global command, so this package can find the entries
// it wrote in a file that is otherwise the operator's.
const globalTag = ": gate-inbox-global-hook"

// adoptedDirName holds one marker per adopted pane.
const adoptedDirName = "adopted"

// adoptedSteeringDirName holds, for each adopted row, the pid of the claude
// that has been given the board's standing instructions; see
// AdoptedSteering.
const adoptedSteeringDirName = "adopted-steering"

// globalDisabledName is the file that keeps the board from registering the
// global hooks again after an operator removed them.
const globalDisabledName = "global-hooks.disabled"

// globalEvents are the events an adopted session needs: lifecycle status, the
// pending question a dialog relay reads, and the notes on prompts and
// questions. SessionEnd is left out; the poller drops a dead agent's status
// on its own, and a launch's SessionEnd also prunes git worktrees, which no
// session outside the board asked for.
var globalEvents = []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Notification", "Stop", "StopFailure"}

// matchField is the payload field an event's matchers are compared with.
var matchField = map[string]string{
	"PreToolUse":   "tool_name",
	"PostToolUse":  "tool_name",
	"Notification": "notification_type",
	"SessionStart": "source",
}

// AdoptedDir is where the board keeps the adoption markers the global hooks
// read.
func (m *Manager) AdoptedDir() string {
	return filepath.Join(m.dir, adoptedDirName)
}

// AdoptedPane is one adopted agent the board answers for from outside its
// launch: the row it is on the board as, and where it runs.
//
// Every adopted agent gets one, so a command it runs can find its row
// (AdoptedCaller). Only a claude's is read by the global hooks and the MCP
// relay, and Tool is how they tell: a claude's marker is the two fields the
// registered hook prelude reads, "<row> <pid>", and any other agent's adds
// its tool as a third, which that prelude reads as a pid that is never its
// parent's.
type AdoptedPane struct {
	ID string
	// ServerPID and PaneID are what the hook reads off $TMUX and $TMUX_PANE.
	ServerPID int
	PaneID    string
	// AgentPID is the agent process itself. A claude's is its hooks' parent;
	// any agent's is an ancestor of every command it runs.
	AgentPID int
	// Tool is the configured tool of an agent that is not a hooks-driven
	// claude, and empty for one that is.
	Tool string
	// Conversation is the agent's own conversation id when the row has one,
	// written as a fourth field after Tool. OpenCode's global plugin reads
	// it: the plugin runs in opencode's shared background service, where
	// only a session id says which conversation a model request is for, so
	// a row bound earlier is recognised again without waiting for the pane
	// to show itself. A claude's marker never carries it.
	Conversation string
}

var paneIDPattern = regexp.MustCompile(`^%[0-9]+$`)

// markerToolPattern is what a marker's tool field may hold: one word, so
// the marker still splits into three fields. A tool named otherwise is
// written as markerToolFallback, which still marks the agent as no claude.
var markerToolPattern = regexp.MustCompile(`^[A-Za-z0-9_.+-]+$`)

const markerToolFallback = "agent"

func adoptedMarkerName(pane AdoptedPane) string {
	return strconv.Itoa(pane.ServerPID) + pane.PaneID
}

func adoptedMarkerContent(pane AdoptedPane) string {
	content := pane.ID + " " + strconv.Itoa(pane.AgentPID)
	if pane.Tool != "" {
		tool := pane.Tool
		if !markerToolPattern.MatchString(tool) {
			tool = markerToolFallback
		}
		content += " " + tool
		if markerToolPattern.MatchString(pane.Conversation) {
			content += " " + pane.Conversation
		}
	}
	return content + "\n"
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
		wanted[adoptedMarkerName(pane)] = adoptedMarkerContent(pane)
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
	errs = append(errs, m.pruneAdoptedSteering())
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

// AdoptedSteering is the hook output that gives an adopted claude what a
// launch would have given it from the start: text, the MCP server's
// instructions and the launch's steering, as additionalContext. Neither
// reaches it any other way, since Claude Code reads both only as a session
// starts, and that was before the board knew of it.
//
// It is said once per adoption. The stamp under hooks/adopted-steering/ is
// named for the row and holds the pid of the claude it was said to, so the
// row's later prompts pass in silence while a new claude adopted into the
// same row hears it again. A SessionStart says it whatever the stamp holds:
// in a session that is already running it fires only for a /clear, a
// compaction or a resume, each of which leaves the model without what was
// said before. A stamp that cannot be written says nothing, rather than
// saying it on every prompt.
func (m *Manager) AdoptedSteering(event, id string, agentPID int, text func() string) string {
	if (event != "UserPromptSubmit" && event != "SessionStart") || checkID(id) != nil || agentPID <= 0 {
		return ""
	}
	dir := filepath.Join(m.dir, adoptedSteeringDirName)
	path := filepath.Join(dir, id)
	stamp := strconv.Itoa(agentPID) + "\n"
	if event == "UserPromptSubmit" {
		if existing, err := os.ReadFile(path); err == nil && string(existing) == stamp {
			return ""
		}
	}
	body := strings.TrimSpace(text())
	if body == "" {
		return ""
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	if err := WriteWhole(path, stamp); err != nil {
		return ""
	}
	out, err := marshalPlain(map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName":     event,
		"additionalContext": body,
	}})
	if err != nil {
		return ""
	}
	return string(out)
}

// pruneAdoptedSteering drops the stamps of claudes that have exited, whose
// pid can match no marker again. A stamp is kept while its claude runs, even
// when the row is let go: the board lets every row go as it exits, and the
// session that hears the steering again on the board's next start already
// has it.
func (m *Manager) pruneAdoptedSteering() error {
	dir := filepath.Join(m.dir, adoptedSteeringDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	var errs []error
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && pid > 0 && syscall.Kill(pid, 0) != syscall.ESRCH {
			continue
		}
		errs = append(errs, removeIfExists(path))
	}
	return errors.Join(errs...)
}

// AdoptedCaller names the board row a command speaks as when it runs inside
// an adopted agent, from the pane's adoption marker. tmuxEnv and paneID are
// $TMUX and $TMUX_PANE, and ancestors lists the pids above the command, asked
// for only once a marker is there to check. A hook is the claude's own child;
// a command an agent's shell tool runs sits under a shell or two, so the
// marker's pid only has to be among them. Any agent's marker counts.
func (m *Manager) AdoptedCaller(tmuxEnv, paneID string, ancestors func() []int) (string, bool) {
	marker, ok := m.adoptedMarker(tmuxEnv, paneID)
	if !ok || !slices.Contains(ancestors(), marker.AgentPID) {
		return "", false
	}
	return marker.ID, true
}

// AdoptedClaudeCaller is AdoptedCaller for what only an adopted claude may
// use, the MCP relay in its user config: a codex or opencode marker in the
// same pane names a row that is not the claude asking, even when that claude
// was started from inside the agent the marker names.
func (m *Manager) AdoptedClaudeCaller(tmuxEnv, paneID string, ancestors func() []int) (string, bool) {
	marker, ok := m.adoptedMarker(tmuxEnv, paneID)
	if !ok || marker.Tool != "" || !slices.Contains(ancestors(), marker.AgentPID) {
		return "", false
	}
	return marker.ID, true
}

// adoptedMarker reads the marker for the pane $TMUX and $TMUX_PANE name, in
// either form SyncAdopted writes.
func (m *Manager) adoptedMarker(tmuxEnv, paneID string) (AdoptedPane, bool) {
	_, rest, ok := strings.Cut(tmuxEnv, ",")
	if !ok || !paneIDPattern.MatchString(paneID) {
		return AdoptedPane{}, false
	}
	server, _, _ := strings.Cut(rest, ",")
	serverPID, err := strconv.Atoi(server)
	if err != nil || serverPID <= 0 {
		return AdoptedPane{}, false
	}
	raw, err := os.ReadFile(filepath.Join(m.AdoptedDir(), server+paneID))
	if err != nil {
		return AdoptedPane{}, false
	}
	fields := strings.Fields(string(raw))
	if len(fields) < 2 || len(fields) > 4 || checkID(fields[0]) != nil {
		return AdoptedPane{}, false
	}
	agent, err := strconv.Atoi(fields[1])
	if err != nil || agent <= 0 {
		return AdoptedPane{}, false
	}
	marker := AdoptedPane{ID: fields[0], ServerPID: serverPID, PaneID: paneID, AgentPID: agent}
	if len(fields) >= 3 {
		marker.Tool = fields[2]
	}
	if len(fields) == 4 {
		marker.Conversation = fields[3]
	}
	return marker, true
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

// globalCommand is the one command registered for event; see the package
// comment above. The tests run cheapest first, so a session nowhere near the
// board stops at the first or second.
func globalCommand(configDir, bin, event string) string {
	hooksDir := filepath.Join(configDir, "hooks")
	lock := shellQuote(filepath.Join(configDir, singleton.FileName))
	unmarked := `[ -f "$m" ] || exit 0; `
	if event == arrivalEvent {
		// A pane with no marker yet is one the board may not know about:
		// announce it (see arrivals.go), still only with builtins, and
		// only to a board that is running.
		unmarked = `[ -f "$m" ] || { read -r b < ` + lock + `; [ -n "$b" ] && kill -0 "$b" && ` +
			`printf '%s\n' "$TMUX" "$TMUX_PANE" "$PPID" > ` +
			shellQuote(filepath.Join(hooksDir, arrivalsDirName)+"/") + `"${t%%,*}$TMUX_PANE"; exit 0; }; `
	}
	return globalMark(configDir) + `exec 2>/dev/null; ` +
		`[ -z "$` + EnvStatusFile + `" ] || exit 0; [ -n "$TMUX_PANE" ] || exit 0; ` +
		`t="${TMUX#*,}"; m=` + shellQuote(filepath.Join(hooksDir, adoptedDirName)+"/") + `"${t%%,*}$TMUX_PANE"; ` +
		unmarked + `read -r i p < "$m"; [ "$p" = "$PPID" ] || exit 0; ` +
		`case "$i" in ''|*[!A-Za-z0-9_-]*) exit 0;; esac; ` +
		`read -r b < ` + lock + `; [ -n "$b" ] && kill -0 "$b" || exit 0; ` +
		`[ -x ` + shellQuote(bin) + ` ] || exit 0; ` +
		EnvSessionID + `="$i"; ` +
		EnvStatusFile + `=` + shellQuote(hooksDir+"/") + `"$i.status"; ` +
		EnvExecutable + `=` + shellQuote(bin) + `; ` +
		EnvAgentPID + `="$p"; ` +
		config.HomeEnv + `=` + shellQuote(configDir) + `; ` +
		`export ` + EnvSessionID + ` ` + EnvStatusFile + ` ` + EnvExecutable + ` ` + EnvAgentPID + ` ` + config.HomeEnv + `; ` +
		shellQuote(bin) + ` hook global ` + event + `; exit 0`
}

// globalHooks is one matcher-less entry per global event. The permission and
// sandbox denials a launch carries are registered beside them, in the
// settings' own lists (globaldeny.go).
func globalHooks(configDir, bin string) map[string][]hookMatcher {
	out := make(map[string][]hookMatcher, len(globalEvents))
	for _, event := range globalEvents {
		out[event] = []hookMatcher{{Hooks: []hookCommand{{Type: "command", Command: globalCommand(configDir, bin, event)}}}}
	}
	return out
}

// globalDispatchTimeout bounds the launch commands one global hook runs, well
// inside Claude Code's own hook timeout.
const globalDispatchTimeout = 30 * time.Second

// DispatchGlobal runs, for an adopted session, the launch commands of event
// whose matchers take the payload, each with the payload on stdin and the
// environment the prelude exported, and returns what they printed as one
// hook output. Nothing fails: a command that errors adds nothing, and its
// stderr is dropped. A PreToolUse that names the sealing keys is also refused
// (KeyDirDecision), which a launch does with the denials on its command line.
func (m *Manager) DispatchGlobal(event string, payload []byte) string {
	if !slices.Contains(globalEvents, event) {
		return ""
	}
	content, err := settingsContent(parentseal.KeyDir(m.root))
	if err != nil {
		return ""
	}
	var launched settingsFile
	if json.Unmarshal(content, &launched) != nil {
		return ""
	}
	value := ""
	if field, ok := matchField[event]; ok {
		var fields map[string]any
		if json.Unmarshal(payload, &fields) == nil {
			value, _ = fields[field].(string)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), globalDispatchTimeout)
	defer cancel()
	var outputs []string
	if event == "PreToolUse" {
		if denied := m.KeyDirDecision(payload); denied != "" {
			outputs = append(outputs, denied)
		}
	}
	for _, group := range launched.Hooks[event] {
		if !matcherTakes(group.Matcher, value) {
			continue
		}
		for _, hook := range group.Hooks {
			cmd := exec.CommandContext(ctx, "/bin/sh", "-c", hook.Command)
			cmd.Stdin = bytes.NewReader(payload)
			out, _ := cmd.Output()
			if text := strings.TrimSpace(string(out)); text != "" {
				outputs = append(outputs, text)
			}
		}
	}
	return mergeHookOutputs(outputs)
}

// RecordConversation leaves the conversation id an adopted session's hook
// payload carries in its mailbox (ConversationFile), for the poller to bind
// the row to. A launch tells claude its id; an adopted claude chose its own,
// and adoption finds it only when claude's per-process session file was
// there to read, so without this a row could go its whole life with no
// transcript behind it. Every event carries the id, so the first hook after
// adoption fills it, and a /clear or a resume inside the pane, which moves
// claude to a new conversation, moves the row with it on the next one.
//
// The file is written only when it would change: a hook fires on every tool
// call, and the id it carries is nearly always the one already waiting.
func (m *Manager) RecordConversation(id string, payload []byte) error {
	if err := checkID(id); err != nil {
		return err
	}
	var fields struct {
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(payload, &fields) != nil {
		return nil
	}
	conversation := fields.SessionID
	if parsed, err := uuid.Parse(conversation); err != nil || parsed.String() != conversation {
		return nil
	}
	if existing, found := m.ReadConversation(id); found && existing == conversation {
		return nil
	}
	return WriteWhole(m.ConversationFile(id), conversation+"\n")
}

// matcherTakes reads the matchers the launch hooks use: empty or "*" for
// everything, otherwise names separated by |.
func matcherTakes(matcher, value string) bool {
	if matcher == "" || matcher == "*" {
		return true
	}
	return slices.Contains(strings.Split(matcher, "|"), value)
}

// MergeHookOutputs is mergeHookOutputs over the outputs that are not empty,
// for a caller adding its own output to DispatchGlobal's.
func MergeHookOutputs(outputs ...string) string {
	var kept []string
	for _, out := range outputs {
		if out = strings.TrimSpace(out); out != "" {
			kept = append(kept, out)
		}
	}
	return mergeHookOutputs(kept)
}

// mergeHookOutputs makes one hook output of several. Claude Code reads a
// hook's stdout as one JSON object, and two launch commands on one event can
// each print one -- a prompt note and an attestation note, say -- which as
// separate hooks Claude Code would have merged itself. Text fields under the
// same key are joined; anything that is not a JSON object keeps only the
// first output.
func mergeHookOutputs(outputs []string) string {
	if len(outputs) <= 1 {
		return strings.Join(outputs, "")
	}
	merged := map[string]any{}
	for _, out := range outputs {
		var next map[string]any
		if json.Unmarshal([]byte(out), &next) != nil {
			return outputs[0]
		}
		mergeInto(merged, next)
	}
	raw, err := marshalPlain(merged)
	if err != nil {
		return outputs[0]
	}
	return string(raw)
}

func mergeInto(dst, src map[string]any) {
	for key, value := range src {
		existing, ok := dst[key]
		if !ok {
			dst[key] = value
			continue
		}
		switch old := existing.(type) {
		case map[string]any:
			if next, ok := value.(map[string]any); ok {
				mergeInto(old, next)
			}
		case string:
			if next, ok := value.(string); ok && next != old && key != "hookEventName" {
				dst[key] = old + "\n\n" + next
			}
		}
	}
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
// path, replacing any it wrote before, and the key-dir denials beside them
// (see globaldeny.go). Only the file's "hooks" member and the two deny lists
// are rewritten; every other byte stays as it was. changed is false when the
// file already carried exactly these entries.
func RegisterGlobal(path, configDir, bin string) (changed bool, err error) {
	var created []string
	changed, resolved, err := rewriteSettings(path, true, func(raw []byte) ([]byte, error) {
		merged, err := mergeSettings(raw, globalMark(configDir), globalHooks(configDir, bin))
		if err != nil {
			return nil, err
		}
		merged, created, err = addDenials(merged, configDir)
		return merged, err
	})
	if err != nil || !changed {
		return changed, err
	}
	return true, writeCreated(configDir, resolved, created, false)
}

// UnregisterGlobal removes the global hooks and key-dir denials of the board
// at configDir, or of every board when configDir is empty. A hooks member, a
// list or an object this package added is removed with them, so a file that
// had none is left as it was before.
func UnregisterGlobal(path, configDir string) (changed bool, err error) {
	mark := globalTag + " "
	if configDir != "" {
		mark = globalMark(configDir)
	}
	var boards, kept, remaining []string
	changed, resolved, err := rewriteSettings(path, false, func(raw []byte) ([]byte, error) {
		boards = []string{configDir}
		if configDir == "" {
			boards = registeredBoards(raw)
		}
		var created []string
		for _, dir := range boards {
			created = append(created, readCreated(dir, resolvedSettings(path))...)
		}
		out := raw
		for _, dir := range boards {
			var err error
			if out, err = removeDenials(out, dir, created); err != nil {
				return nil, err
			}
		}
		out, err := mergeSettings(out, mark, nil)
		if err != nil {
			return nil, err
		}
		kept, remaining = stillThere(out, created), registeredBoards(out)
		return out, nil
	})
	if err != nil {
		return changed, err
	}
	var errs []error
	for _, dir := range boards {
		errs = append(errs, writeCreated(dir, resolved, nil, true))
	}
	// A container another board's entries still fill stays, and becomes
	// theirs to take out with the last of them.
	for _, dir := range remaining {
		errs = append(errs, writeCreated(dir, resolved, kept, false))
	}
	return changed, errors.Join(errs...)
}

// GlobalRegistered reports whether the settings file at path carries this
// board's global hooks and key-dir denials exactly as this build would write
// them.
func GlobalRegistered(path, configDir, bin string) (bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	merged, err := mergeSettings(raw, globalMark(configDir), globalHooks(configDir, bin))
	if err != nil {
		return false, err
	}
	merged, _, err = addDenials(merged, configDir)
	if err != nil {
		return false, err
	}
	return bytes.Equal(merged, raw), nil
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

// rewriteSettings writes edit's result back. A settings file that is a link
// -- one a dotfiles checkout manages, say -- is written through, so the link
// stays a link. A file the operator made read-only is refused rather than
// replaced: a rename into its directory would succeed and undo their choice.
// A missing file is created only when create is set. resolved is the path
// written, after links.
func rewriteSettings(path string, create bool, edit func(raw []byte) ([]byte, error)) (changed bool, resolved string, err error) {
	path = resolvedSettings(path)
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, path, err
	}
	missing := err != nil
	if missing && !create {
		return false, path, nil
	}
	merged, err := edit(raw)
	if err != nil {
		return false, path, err
	}
	if !missing && bytes.Equal(merged, raw) {
		return false, path, nil
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
		probe, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			return false, path, fmt.Errorf("hooks: %s is not writable: %w", path, err)
		}
		probe.Close()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, path, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*.json")
	if err != nil {
		return false, path, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(merged); err != nil {
		tmp.Close()
		return false, path, err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return false, path, err
	}
	if err := tmp.Close(); err != nil {
		return false, path, err
	}
	return true, path, os.Rename(tmp.Name(), path)
}

// resolvedSettings is path after links, or path when it does not resolve.
func resolvedSettings(path string) string {
	if target, err := filepath.EvalSymlinks(path); err == nil {
		return target
	}
	return path
}

// mergeSettings is raw with every hook command starting with mark taken out
// and hooks added at the end of their events. Only the "hooks" member
// changes: a matcher group left empty by the removal goes, and so do an
// event and the hooks member itself when the removal empties them. A merge
// that changes nothing returns raw, byte for byte.
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
		value, err := marshalPlain(survivors)
		if err != nil {
			return nil, err
		}
		kept = append(kept, member{key: ev.key, value: value})
	}
	events = kept
	for _, name := range sortedEvents(hooks) {
		add, err := marshalPlain(hooks[name])
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
		value, err := marshalPlain(append(groups, ours...))
		if err != nil {
			return nil, err
		}
		events[i].value = value
	}
	var value []byte
	switch {
	case len(events) == 0 && (removedAny || !hasHooks):
		value = nil
	default:
		value = encodeObject(events)
	}
	if hasHooks && value != nil && compactEqual(top[at].value, value) {
		return raw, nil
	}
	if !hasHooks && value == nil {
		return raw, nil
	}
	return spliceHooks(raw, value)
}

// marshalPlain is json.Marshal without HTML escaping: the commands carry > and
// &, and the operator reads this file.
func marshalPlain(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func compactEqual(a, b []byte) bool {
	var ca, cb bytes.Buffer
	if json.Compact(&ca, a) != nil || json.Compact(&cb, b) != nil {
		return false
	}
	return bytes.Equal(ca.Bytes(), cb.Bytes())
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// spliceHooks returns raw with its top-level "hooks" member set to value, or
// removed when value is nil, and every other byte where it was. A member it
// adds goes after the last one, in the file's own indentation, so removing it
// again gives back the bytes it was added to.
func spliceHooks(raw, value []byte) ([]byte, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		if value == nil {
			return raw, nil
		}
		return []byte("{\n  \"hooks\": " + indentJSON(value, "  ") + "\n}\n"), nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errors.New("hooks: the settings file is not a JSON object")
	}
	open := int(dec.InputOffset()) - 1
	found := false
	var keyStart, valueStart, valueEnd, firstKey int
	firstKey = -1
	for dec.More() {
		from := int(dec.InputOffset())
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := tok.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		start := from
		for start < len(raw) && (isSpace(raw[start]) || raw[start] == ',') {
			start++
		}
		if firstKey < 0 {
			firstKey = start
		}
		if key == "hooks" {
			found = true
			keyStart = start
			valueEnd = int(dec.InputOffset())
			valueStart = valueEnd - len(v)
		}
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	closing := int(dec.InputOffset()) - 1

	indent := "  "
	if firstKey > 0 {
		if nl := bytes.LastIndexByte(raw[:firstKey], '\n'); nl > open {
			indent = string(raw[nl+1 : firstKey])
		} else {
			indent = ""
		}
	}
	join := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

	switch {
	case found && value != nil:
		return join(raw[:valueStart], []byte(indentJSON(value, indent)), raw[valueEnd:]), nil
	case found:
		before := keyStart - 1
		for before > open && isSpace(raw[before]) {
			before--
		}
		if raw[before] == ',' {
			return join(raw[:before], raw[valueEnd:]), nil
		}
		after := valueEnd
		for after < closing && isSpace(raw[after]) {
			after++
		}
		if raw[after] == ',' {
			next := after + 1
			for next < closing && isSpace(raw[next]) {
				next++
			}
			return join(raw[:keyStart], raw[next:]), nil
		}
		return join(raw[:open+1], raw[closing:]), nil
	case value != nil:
		last := closing - 1
		for last > open && isSpace(raw[last]) {
			last--
		}
		if indent == "" && firstKey < 0 {
			indent = "  "
		}
		sep := "\n" + indent
		if indent == "" {
			sep = ""
		}
		colon := ": "
		if indent == "" {
			colon = ":"
		}
		entry := `"hooks"` + colon + indentJSON(value, indent)
		if last == open {
			return join(raw[:open+1], []byte(sep+entry+strings.TrimSuffix(sep, indent)), raw[closing:]), nil
		}
		return join(raw[:last+1], []byte(","+sep+entry), raw[last+1:]), nil
	}
	return raw, nil
}

// indentJSON lays out value as a member nested one level under indent.
func indentJSON(value []byte, indent string) string {
	if indent == "" {
		var out bytes.Buffer
		if json.Compact(&out, value) != nil {
			return string(value)
		}
		return out.String()
	}
	var out bytes.Buffer
	if json.Indent(&out, value, indent, indent) != nil {
		return string(value)
	}
	return out.String()
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
	value, err := marshalPlain(keep)
	if err != nil {
		return nil, false, false, err
	}
	fields[at].value = value
	return encodeObject(fields), true, len(keep) == 0, nil
}
