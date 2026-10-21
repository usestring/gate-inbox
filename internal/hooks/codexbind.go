package hooks

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/usestring/gate-inbox/internal/status"
)

// Codex rows and threads.
//
// A codex hook runs in the shared daemon, so the one thing it can be sure of
// is the thread its payload names. The board publishes, under hooks/codex/,
// what a hook needs to act on that:
//
//   - threads/<thread>: the row a thread belongs to and how the row came on
//     the board, "<row> adopted" or "<row> launched". Every live codex row
//     whose thread the board knows has one.
//   - adopted.json: every live adopted codex row with the pane it is in, so
//     a hook for a thread nobody has claimed can find the pane that thread
//     is typing in.
//   - watch: present while either of the above names anything. The hook
//     prelude goes no further without it.
//
// A thread the board has not bound is bound from its prompt: the
// UserPromptSubmit payload carries the prompt text, and codex has already
// drawn that prompt as the newest user message in its pane when the hook
// runs. The hook reads the screens of the adopted codex rows (preferring
// those in the payload's working directory) and binds the thread to the
// one row whose screen shows that prompt, and only when exactly one does.
// Nothing is typed into any pane. The binding goes to the row's
// conversation mailbox, which the poller applies as it does a Claude Code
// conversation id, and to threads/ at once, so the turn's own Stop hook
// already finds it.

const codexDirName = "codex"

// codexSteeredDirName holds, per adopted codex row, the thread that has been
// given the board's standing instructions.
const codexSteeredDirName = "steered"

// CodexRow is one live codex row the hooks should answer for.
type CodexRow struct {
	ID string
	// Thread is the codex thread id the row is bound to, or "" when the
	// board does not know it yet.
	Thread string
	// Adopted rows also carry the pane they are in.
	Adopted bool
	Socket  string
	Pane    string
	Cwd     string
}

type codexAdoptedPane struct {
	ID     string `json:"id"`
	Socket string `json:"socket"`
	Pane   string `json:"pane"`
	Cwd    string `json:"cwd,omitempty"`
}

func (m *Manager) codexDir() string { return filepath.Join(m.dir, codexDirName) }

func validThread(thread string) bool {
	parsed, err := uuid.Parse(thread)
	return err == nil && parsed.String() == thread
}

// SyncCodex makes hooks/codex/ describe exactly rows. A thread an adopted
// row's hook has just bound, still waiting in the row's conversation
// mailbox for the poller, is kept, so the board does not unbind a thread
// between the hook binding it and the poller storing it.
func (m *Manager) SyncCodex(rows []CodexRow) error {
	dir := m.codexDir()
	threads := map[string]string{}
	var adopted []codexAdoptedPane
	steered := map[string]bool{}
	for _, row := range rows {
		if checkID(row.ID) != nil {
			continue
		}
		kind := "launched"
		if row.Adopted {
			kind = "adopted"
			if !paneIDPattern.MatchString(row.Pane) {
				continue
			}
			adopted = append(adopted, codexAdoptedPane{ID: row.ID, Socket: row.Socket, Pane: row.Pane, Cwd: row.Cwd})
			steered[row.ID] = true
			if pending, found := m.ReadConversation(row.ID); found && validThread(pending) {
				threads[pending] = row.ID + " " + kind + "\n"
			}
		}
		if validThread(row.Thread) {
			threads[row.Thread] = row.ID + " " + kind + "\n"
		}
	}
	var errs []error
	threadDir := filepath.Join(dir, "threads")
	entries, err := os.ReadDir(threadDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, entry := range entries {
		if _, keep := threads[entry.Name()]; !keep {
			errs = append(errs, removeIfExists(filepath.Join(threadDir, entry.Name())))
		}
	}
	steeredDir := filepath.Join(dir, codexSteeredDirName)
	if entries, err := os.ReadDir(steeredDir); err == nil {
		for _, entry := range entries {
			if !steered[entry.Name()] {
				errs = append(errs, removeIfExists(filepath.Join(steeredDir, entry.Name())))
			}
		}
	}
	if len(threads) == 0 && len(adopted) == 0 {
		errs = append(errs, removeIfExists(filepath.Join(dir, "watch")), removeIfExists(filepath.Join(dir, "adopted.json")))
		return errors.Join(errs...)
	}
	if err := os.MkdirAll(threadDir, 0o755); err != nil {
		return err
	}
	for thread, content := range threads {
		errs = append(errs, writeIfChanged(filepath.Join(threadDir, thread), content))
	}
	slices.SortFunc(adopted, func(a, b codexAdoptedPane) int { return strings.Compare(a.ID, b.ID) })
	if len(adopted) == 0 {
		errs = append(errs, removeIfExists(filepath.Join(dir, "adopted.json")))
	} else if raw, err := json.Marshal(adopted); err == nil {
		errs = append(errs, writeIfChanged(filepath.Join(dir, "adopted.json"), string(raw)+"\n"))
	}
	errs = append(errs, writeIfChanged(filepath.Join(dir, "watch"), ""))
	return errors.Join(errs...)
}

func writeIfChanged(path, content string) error {
	if existing, err := os.ReadFile(path); err == nil && string(existing) == content {
		return nil
	}
	return WriteWhole(path, content)
}

// CodexThreadRow names the row a codex thread is bound to, and whether that
// row was adopted rather than launched.
func (m *Manager) CodexThreadRow(thread string) (id string, adopted bool, ok bool) {
	if !validThread(thread) {
		return "", false, false
	}
	raw, err := os.ReadFile(filepath.Join(m.codexDir(), "threads", thread))
	if err != nil {
		return "", false, false
	}
	fields := strings.Fields(string(raw))
	if len(fields) != 2 || checkID(fields[0]) != nil {
		return "", false, false
	}
	return fields[0], fields[1] == "adopted", true
}

// CodexPayload is the part of a codex hook payload the board reads.
type CodexPayload struct {
	Thread string `json:"session_id"`
	Prompt string `json:"prompt"`
	Cwd    string `json:"cwd"`
}

// PaneCapture reads the visible screen of a pane on a tmux server.
type PaneCapture func(socket, pane string) (string, error)

// DispatchCodex is a codex hook's work for event, and its output: nothing,
// or a UserPromptSubmit additionalContext carrying steering's text the first
// time an adopted row's thread is heard from. It writes nothing and says
// nothing for a thread the board does not hold and cannot bind.
func (m *Manager) DispatchCodex(event string, raw []byte, capture PaneCapture, steering func() string) string {
	if !slices.Contains(CodexEvents, event) {
		return ""
	}
	var payload CodexPayload
	if json.Unmarshal(raw, &payload) != nil || !validThread(payload.Thread) {
		return ""
	}
	id, adopted, ok := m.CodexThreadRow(payload.Thread)
	if !ok && event == "UserPromptSubmit" && capture != nil {
		id, ok = m.bindCodexThread(payload, capture)
		adopted = ok
	}
	if !ok {
		return ""
	}
	state := status.Working
	if event == "Stop" {
		state = status.Finished
	}
	_ = m.appendCodexEvent(id, state, event)
	if !adopted || event != "UserPromptSubmit" || steering == nil {
		return ""
	}
	return m.codexSteering(id, payload.Thread, steering)
}

func (m *Manager) appendCodexEvent(id, state, event string) error {
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(m.StatusFile(id), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(state + " " + event + "\n"); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// codexSteering says the board's standing instructions once per thread of
// an adopted row: the stamp under steered/ holds the thread they were said
// to, so a /new in the pane, which starts a new thread, hears them again.
// A stamp that cannot be written says nothing, rather than saying it on
// every prompt.
func (m *Manager) codexSteering(id, thread string, text func() string) string {
	dir := filepath.Join(m.codexDir(), codexSteeredDirName)
	path := filepath.Join(dir, id)
	if existing, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(existing)) == thread {
		return ""
	}
	body := strings.TrimSpace(text())
	if body == "" {
		return ""
	}
	if os.MkdirAll(dir, 0o755) != nil || WriteWhole(path, thread+"\n") != nil {
		return ""
	}
	out, err := marshalPlain(map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName":     "UserPromptSubmit",
		"additionalContext": body,
	}})
	if err != nil {
		return ""
	}
	return string(out)
}

// codexAdopted is the board's adopted codex panes.
func (m *Manager) codexAdopted() []codexAdoptedPane {
	raw, err := os.ReadFile(filepath.Join(m.codexDir(), "adopted.json"))
	if err != nil {
		return nil
	}
	var panes []codexAdoptedPane
	if json.Unmarshal(raw, &panes) != nil {
		return nil
	}
	return panes
}

// bindCodexThread finds the adopted row whose pane shows payload's prompt,
// and binds the thread to it. Panes in the payload's directory are read
// first; the rest only when none of those shows it.
func (m *Manager) bindCodexThread(payload CodexPayload, capture PaneCapture) (string, bool) {
	needle := promptNeedle(payload.Prompt)
	if needle == "" {
		return "", false
	}
	panes := m.codexAdopted()
	if len(panes) == 0 {
		return "", false
	}
	var near, far []codexAdoptedPane
	for _, pane := range panes {
		if checkID(pane.ID) != nil || !paneIDPattern.MatchString(pane.Pane) {
			continue
		}
		if payload.Cwd != "" && filepath.Clean(pane.Cwd) == filepath.Clean(payload.Cwd) {
			near = append(near, pane)
		} else {
			far = append(far, pane)
		}
	}
	for _, group := range [][]codexAdoptedPane{near, far} {
		var hits []string
		for _, pane := range group {
			screen, err := capture(pane.Socket, pane.Pane)
			if err == nil && ScreenShowsPrompt(screen, needle) {
				hits = append(hits, pane.ID)
			}
		}
		switch len(hits) {
		case 0:
			continue
		case 1:
			id := hits[0]
			if err := WriteWhole(m.ConversationFile(id), payload.Thread+"\n"); err != nil {
				return "", false
			}
			threadDir := filepath.Join(m.codexDir(), "threads")
			if os.MkdirAll(threadDir, 0o755) != nil || WriteWhole(filepath.Join(threadDir, payload.Thread), id+" adopted\n") != nil {
				return "", false
			}
			return id, true
		default:
			// Two panes show the same prompt: binding either could
			// speak for the wrong one.
			return "", false
		}
	}
	return "", false
}

// promptNeedleLen is how much of a prompt's first line is matched: enough to
// tell prompts apart, short enough to sit on one row of a narrow pane.
const promptNeedleLen = 40

// promptNeedle is the start of the prompt's first non-blank line, with its
// whitespace runs collapsed, as codex draws it.
func promptNeedle(prompt string) string {
	for _, line := range strings.Split(prompt, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			continue
		}
		runes := []rune(line)
		if len(runes) > promptNeedleLen {
			runes = runes[:promptNeedleLen]
		}
		return strings.TrimSpace(string(runes))
	}
	return ""
}

// ScreenShowsPrompt reports whether a codex screen shows a user message
// starting with needle: codex draws one as a row opening with "›", the same
// glyph its composer uses, so the composer's own row can match only once
// its text is the prompt, which is no less the pane's.
func ScreenShowsPrompt(screen, needle string) bool {
	for _, line := range strings.Split(screen, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimLeft(line, "  "), "›")
		if !ok {
			continue
		}
		if strings.HasPrefix(strings.Join(strings.Fields(rest), " "), needle) {
			return true
		}
	}
	return false
}

// CodexHookStatus is the newest status a codex row's hooks logged and how
// long ago they logged it. The file outlives the turn that wrote it, so a
// caller weighs it by age.
func (m *Manager) CodexHookStatus(id string) (string, time.Duration, bool) {
	info, err := os.Stat(m.StatusFile(id))
	if err != nil {
		return "", 0, false
	}
	state, ok := m.Read(id)
	if !ok {
		return "", 0, false
	}
	return state, time.Since(info.ModTime()), true
}
