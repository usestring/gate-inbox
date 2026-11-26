package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The records a Claude Code transcript holds around a question dialog.
const (
	promptRecord = `{"type":"user","message":{"role":"user","content":"ship the fix"}}`
	askRecord    = `{"type":"assistant","message":{"role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_1","name":"AskUserQuestion","input":{}}]}}`
	// Escape on the dialog writes these two and fires no hook.
	rejectedRecord    = `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","is_error":true,"content":"The user doesn't want to proceed with this tool use."}]}}`
	interruptedRecord = `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]}}`
	replyRecord       = `{"type":"assistant","message":{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"Done."}]}}`
)

func TestDialogOpen(t *testing.T) {
	logged := time.Now().Add(-time.Minute)
	for _, tc := range []struct {
		name   string
		source string
		log    string
		// records is the transcript; nil means none is known. written is
		// when it was last written, relative to the log's last line.
		records []string
		written time.Duration
		want    bool
	}{
		{name: "pi dialog", source: StatusSourcePi, log: "waiting " + PiPromptStart + "\n", want: true},
		{name: "pi dialog closed", source: StatusSourcePi, log: "waiting " + PiPromptStart + "\nidle ui_prompt_end\n"},
		{name: "claude question", source: StatusSourceClaude, log: "waiting PreToolUse\n", records: []string{promptRecord, askRecord}, written: -time.Second, want: true},
		{name: "claude permission prompt", source: StatusSourceClaude, log: "working PreToolUse\nwaiting Notification\n", records: []string{promptRecord, askRecord}, written: -time.Second, want: true},
		{name: "claude answered", source: StatusSourceClaude, log: "waiting PreToolUse\nworking PostToolUse\n"},
		{name: "claude escaped", source: StatusSourceClaude, log: "waiting Notification\n", records: []string{promptRecord, askRecord, rejectedRecord, interruptedRecord}, written: time.Second},
		{name: "claude transcript closed before the dialog", source: StatusSourceClaude, log: "waiting Notification\n", records: []string{promptRecord, replyRecord}, written: -time.Second, want: true},
		{name: "claude transcript unknown", source: StatusSourceClaude, log: "waiting Notification\n", want: true},
		{name: "claude pinned waiting", source: StatusSourceClaude, log: "waiting manager\n"},
		{name: "another tool", source: "", log: "waiting Notification\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewManager(t.TempDir())
			if err := os.MkdirAll(m.Dir(), 0o755); err != nil {
				t.Fatal(err)
			}
			statusFile := m.StatusFile("abcd1234")
			if err := os.WriteFile(statusFile, []byte(tc.log), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(statusFile, logged, logged); err != nil {
				t.Fatal(err)
			}
			path := ""
			if tc.records != nil {
				path = filepath.Join(t.TempDir(), "conversation.jsonl")
				if err := os.WriteFile(path, []byte(strings.Join(tc.records, "\n")+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				at := logged.Add(tc.written)
				if err := os.Chtimes(path, at, at); err != nil {
					t.Fatal(err)
				}
			}
			lines := strings.Split(strings.TrimSpace(tc.log), "\n")
			waiting := strings.HasPrefix(lines[len(lines)-1], "waiting")
			transcript := func() string {
				if !waiting {
					t.Error("the transcript was looked up for a log that does not say waiting")
				}
				return path
			}
			if got := m.DialogOpen(tc.source, "abcd1234", transcript); got != tc.want {
				t.Fatalf("DialogOpen = %v, want %v", got, tc.want)
			}
		})
	}
}
