package sessioncmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/hooks"
)

// A parent learns a child is waiting when something puts it in front of it.
// ChildNudgeHook does that from the parent's own hooks: at the start of a
// turn it adds the waiting dialogs to the prompt's context, and when a turn
// would end with one still standing that the turn was not told about, it
// blocks the stop once and says which. Each dialog is raised at most once a
// turn, so a parent that decides to leave one waiting is not held in a loop.
// Every hook also keeps the status line's segment current.

// ChildNudgeSuffix is the file beside a session's status log that records
// the dialogs this turn has already been told about, one key a line.
const ChildNudgeSuffix = ".child-nudged"

// ChildNudgeHook is what a session's Stop, UserPromptSubmit and PostToolUse
// hooks print for the children it tracks, given the hook's stdin payload.
// ledger is the ChildNudgeSuffix file and segDir the status line's directory
// for this user; either empty turns off what it backs. Every fault prints
// nothing.
func ChildNudgeHook(configDir, claudeHome, sessionID, ledger, segDir string, payload []byte, now time.Time) string {
	var event struct {
		SessionID string `json:"session_id"`
		Event     string `json:"hook_event_name"`
	}
	if sessionID == "" || json.Unmarshal(payload, &event) != nil {
		return ""
	}
	board, err := ChildDialogs(configDir, claudeHome, sessionID, now)
	if err != nil {
		return ""
	}
	if segDir != "" {
		_ = WriteChildSegment(segDir, event.SessionID, board)
	}
	if ledger == "" {
		return ""
	}
	switch event.Event {
	case "UserPromptSubmit":
		fresh := unraised(board, nil)
		writeLedger(ledger, keysOf(fresh))
		if len(fresh) == 0 {
			return ""
		}
		return hookJSON(map[string]any{"hookSpecificOutput": map[string]string{
			"hookEventName":     "UserPromptSubmit",
			"additionalContext": nudgeText(fresh),
		}})
	case "Stop":
		raised := readLedger(ledger)
		fresh := unraised(board, raised)
		if len(fresh) == 0 {
			return ""
		}
		writeLedger(ledger, append(raised, keysOf(fresh)...))
		return hookJSON(map[string]string{"decision": "block", "reason": nudgeText(fresh)})
	}
	return ""
}

// dialogKey names one standing dialog: the child and the call it is on, or,
// for a permission prompt with no saved call, the moment it went up.
func dialogKey(child ChildState) string {
	if child.Dialog != nil {
		return child.ID + " " + child.Dialog.ToolUseID
	}
	return child.ID + " @" + strconv.FormatInt(child.WaitingSince.Unix(), 10)
}

func unraised(board ChildBoard, raised []string) []ChildState {
	var fresh []ChildState
	for _, child := range board.Children {
		if child.Waiting && !slices.Contains(raised, dialogKey(child)) {
			fresh = append(fresh, child)
		}
	}
	return fresh
}

func keysOf(children []ChildState) []string {
	keys := make([]string, 0, len(children))
	for _, child := range children {
		keys = append(keys, dialogKey(child))
	}
	return keys
}

func readLedger(path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var keys []string
	for line := range strings.Lines(string(raw)) {
		if key := strings.TrimSpace(line); key != "" {
			keys = append(keys, key)
		}
	}
	return keys
}

func writeLedger(path string, keys []string) {
	if len(keys) == 0 {
		_ = os.Remove(path)
		return
	}
	_ = hooks.WriteWhole(path, strings.Join(keys, "\n")+"\n")
}

func hookJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(raw)
}

// nudgeText is what the parent is told: each waiting child, how long it has
// waited, its questions word for word, and what to do with them.
func nudgeText(children []ChildState) string {
	var b strings.Builder
	for _, child := range children {
		wait := FormatWait(time.Duration(child.WaitedSeconds) * time.Second)
		if child.Dialog == nil {
			fmt.Fprintf(&b, "Child %s (%s) has waited %s on a permission prompt. Read it with read_session, "+
				"then grant_permission or put it to your user.\n", child.Name, child.ID, wait)
			continue
		}
		for _, q := range child.Dialog.Questions {
			fmt.Fprintf(&b, "Child %s (%s) has waited %s on: %s — %s\n", child.Name, child.ID, wait, q.Header, q.Question)
			for _, opt := range q.Options {
				fmt.Fprintf(&b, "  - %s", opt.Label)
				if opt.Description != "" {
					fmt.Fprintf(&b, ": %s", opt.Description)
				}
				b.WriteString("\n")
			}
		}
	}
	b.WriteString("Answer what your brief or your user's standing decisions settle with answer_session. " +
		"Put the rest to your user with AskUserQuestion, copying header, question and options verbatim " +
		"(`gate-inbox children` prints them whole, previews included), then pass their answer to answer_session " +
		"marked as your user's own answer. " +
		"Approval questions are always your user's.")
	return b.String()
}

// The status line reads one short line per source from a directory named for
// the Claude Code session, and ignores files older than ten minutes, so every
// hook rewrites it.
const (
	segmentFile  = "gate-inbox.seg"
	segmentWidth = 24
)

var claudeSessionPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// ChildSegmentDir is where status line segments live for this user:
// ${XDG_RUNTIME_DIR:-/tmp}/claude-statusline-$UID.
func ChildSegmentDir() string {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = "/tmp"
	}
	return filepath.Join(base, "claude-statusline-"+strconv.Itoa(os.Getuid()))
}

// ChildSegment is the status line's text for board: the waiting count and
// the oldest wait, or "" when nothing waits.
func ChildSegment(board ChildBoard) string {
	if board.Waiting == 0 {
		return ""
	}
	var oldest int64
	for _, child := range board.Children {
		if child.Waiting {
			oldest = max(oldest, child.WaitedSeconds)
		}
	}
	noun := "dialogs"
	if board.Waiting == 1 {
		noun = "dialog"
	}
	text := fmt.Sprintf("⚑%d %s %s", board.Waiting, noun, FormatWait(time.Duration(oldest)*time.Second))
	if len([]rune(text)) > segmentWidth {
		text = fmt.Sprintf("⚑%d %s", board.Waiting, noun)
	}
	return text
}

// WriteChildSegment puts board's segment in place for claudeSession, by a
// rename so the status line never reads half a line, and removes it when
// nothing waits.
func WriteChildSegment(dir, claudeSession string, board ChildBoard) error {
	if !claudeSessionPattern.MatchString(claudeSession) {
		return fmt.Errorf("invalid claude session id %q", claudeSession)
	}
	path := filepath.Join(dir, claudeSession, segmentFile)
	text := ChildSegment(board)
	if text == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return hooks.WriteWhole(path, text+"\n")
}
