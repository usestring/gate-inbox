package sessioncmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// A child stopped on a dialog waits for whoever spawned it, and most of
// those dialogs are approvals only the parent can put to the user, so the
// wait is however long the parent takes to notice. The parent needs a read
// of its children's dialogs cheap enough to take on every turn: one query
// for the rows, one small file per child for its status, and the saved
// question for each one that is waiting. No tmux, no pane reads.

// ChildDialog is the AskUserQuestion call a waiting child's dialog shows,
// every question as the child wrote it.
type ChildDialog struct {
	ToolUseID string              `json:"tool_use_id" jsonschema:"id of the AskUserQuestion call the dialog belongs to"`
	Questions []convo.AskQuestion `json:"questions" jsonschema:"the dialog's questions verbatim: header, question, options and previews"`
}

// ChildState is one child of the caller and, when it is waiting, on what.
type ChildState struct {
	ID      string `json:"id" jsonschema:"agent session id"`
	Name    string `json:"name" jsonschema:"session name shown in Gate Inbox"`
	Tool    string `json:"tool" jsonschema:"agent CLI the session runs"`
	Status  string `json:"status" jsonschema:"Gate Inbox status: starting, working, waiting, delegating, finished, idle, errored or dead"`
	Waiting bool   `json:"waiting" jsonschema:"whether the child is stopped on a dialog only its parent can get answered"`
	// WaitingSince is when the dialog went up: the question's own time, or
	// the hook's last write for a dialog with no saved question.
	WaitingSince  time.Time    `json:"waiting_since,omitzero" jsonschema:"when the dialog went up"`
	WaitedSeconds int64        `json:"waited_seconds,omitempty" jsonschema:"how long the dialog has stood, in seconds"`
	Dialog        *ChildDialog `json:"dialog,omitempty" jsonschema:"the questions the dialog asks; absent for a permission prompt, which read_session shows"`
}

// ChildBoard is the caller's unarchived children, waiting ones first and
// longest-waiting first among those.
type ChildBoard struct {
	Session  string       `json:"session" jsonschema:"the session whose children these are"`
	Waiting  int          `json:"waiting" jsonschema:"how many children are stopped on a dialog"`
	Children []ChildState `json:"children" jsonschema:"every unarchived child"`
}

// ChildDialogs reads sessionID's children and the dialogs the waiting ones
// stand on, as of now.
func ChildDialogs(configDir, claudeHome, sessionID string, now time.Time) (ChildBoard, error) {
	if err := validSession(sessionID); err != nil {
		return ChildBoard{}, err
	}
	board := ChildBoard{Session: sessionID, Children: []ChildState{}}
	path := filepath.Join(configDir, "state.db")
	if _, err := os.Stat(path); err != nil {
		return board, nil
	}
	rows, err := liveChildren(path, sessionID)
	if err != nil {
		return ChildBoard{}, err
	}
	manager := hooks.NewManager(configDir)
	for _, row := range rows {
		child := childState(manager, claudeHome, row, now)
		if child.Waiting {
			board.Waiting++
		}
		board.Children = append(board.Children, child)
	}
	sortChildren(board.Children)
	return board, nil
}

// liveChildren reads the rows without running migrations, which a board
// that is up has already run, and falls back to a full Open for a file an
// older build left behind.
func liveChildren(path, sessionID string) ([]store.ChildRow, error) {
	st, err := store.OpenExisting(path)
	if err == nil {
		rows, queryErr := st.LiveChildren(sessionID)
		st.Close()
		if queryErr == nil {
			return rows, nil
		}
	}
	st, err = store.Open(path)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	return st.LiveChildren(sessionID)
}

func childState(manager *hooks.Manager, claudeHome string, row store.ChildRow, now time.Time) ChildState {
	child := ChildState{ID: row.ID, Name: row.Name, Tool: row.Tool, Status: row.Status}
	// The hook log is written the instant the dialog goes up and comes down;
	// the row's status waits for the board's next pass, and for a board that
	// is not running, forever. A child with no log is one whose CLI has no
	// hooks, and the row is all there is.
	since := row.LastStatusAt
	// A dead child is not waiting on anything, though a process killed under
	// its dialog leaves "waiting" as its log's last word.
	if row.Status == status.Dead {
		return child
	}
	if state, ok := manager.Read(row.ID); ok {
		child.Status = state
		if info, err := os.Stat(manager.StatusFile(row.ID)); err == nil {
			since = info.ModTime()
		}
	}
	if child.Status != status.Waiting {
		return child
	}
	child.Waiting = true
	transcript := ""
	if row.AgentSessionID != "" {
		transcript = convo.TranscriptFor(claudeHome, row.AgentSessionID, row.Cwd)
	}
	if call, ok := convo.PendingAskFile(transcript, manager.PendingAskFile(row.ID)); ok {
		child.Dialog = &ChildDialog{ToolUseID: call.ToolUseID, Questions: call.Questions}
		if !call.AskedAt.IsZero() {
			since = call.AskedAt
		}
	}
	if !since.IsZero() {
		child.WaitingSince = since.UTC()
		child.WaitedSeconds = int64(max(now.Sub(since), 0) / time.Second)
	}
	return child
}

func sortChildren(children []ChildState) {
	sort.SliceStable(children, func(i, j int) bool {
		a, b := children[i], children[j]
		if a.Waiting != b.Waiting {
			return a.Waiting
		}
		if a.Waiting && a.WaitedSeconds != b.WaitedSeconds {
			return a.WaitedSeconds > b.WaitedSeconds
		}
		return a.ID < b.ID
	})
}

// FormatChildBoard prints every waiting child's dialog word for word, ready
// to put to the user, then one line for the rest.
func FormatChildBoard(board ChildBoard) string {
	if len(board.Children) == 0 {
		return "no children"
	}
	var b strings.Builder
	if board.Waiting == 0 {
		fmt.Fprintf(&b, "no child is waiting on a dialog (%d children)\n", len(board.Children))
	} else {
		fmt.Fprintf(&b, "%d of %d children waiting on a dialog\n", board.Waiting, len(board.Children))
	}
	var rest []string
	for _, child := range board.Children {
		if !child.Waiting {
			rest = append(rest, fmt.Sprintf("%s (%s) %s", child.Name, child.ID, child.Status))
			continue
		}
		b.WriteString("\n")
		fmt.Fprintf(&b, "child %s (%s) has waited %s", child.Name, child.ID, FormatWait(time.Duration(child.WaitedSeconds)*time.Second))
		if child.Dialog == nil {
			b.WriteString(" on a permission prompt; read_session shows it\n")
			continue
		}
		b.WriteString(" on:\n")
		for i, q := range child.Dialog.Questions {
			fmt.Fprintf(&b, "  [%d] header: %s\n", i+1, q.Header)
			fmt.Fprintf(&b, "      question: %s\n", q.Question)
			if q.MultiSelect {
				b.WriteString("      multiSelect: true\n")
			}
			for _, opt := range q.Options {
				fmt.Fprintf(&b, "      - %s", opt.Label)
				if opt.Description != "" {
					fmt.Fprintf(&b, ": %s", opt.Description)
				}
				b.WriteString("\n")
				if opt.Preview != "" {
					fmt.Fprintf(&b, "        preview:\n%s\n", indent(opt.Preview, "          "))
				}
			}
		}
	}
	if len(rest) > 0 {
		fmt.Fprintf(&b, "\nothers: %s\n", strings.Join(rest, "; "))
	}
	return strings.TrimRight(b.String(), "\n")
}

// FormatWait is a wait as a person reads it: seconds under a minute,
// minutes under an hour, then hours and minutes.
func FormatWait(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	default:
		return fmt.Sprintf("%dh%02dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
	}
}

func indent(text, prefix string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}
