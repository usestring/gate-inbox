package agentsession

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Lines shaped as Claude Code writes them, cut down to the fields read.
const (
	bashStarted   = `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_bash","type":"tool_result","content":"Command running in background with ID: bjudge1."}]},"toolUseResult":{"stdout":"","stderr":"","interrupted":false,"backgroundTaskId":"bjudge1"}}`
	bashStarted2  = `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_bash2","type":"tool_result","content":"Command running in background with ID: bjudge2."}]},"toolUseResult":{"stdout":"","backgroundTaskId":"bjudge2"}}`
	monitorCalled = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_mon","name":"Monitor","input":{"command":"tail -f x"}}]}}`
	monitorResult = `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_mon","type":"tool_result","content":"started"}]},"toolUseResult":{"taskId":"bmon1","timeoutMs":1800000,"persistent":false}}`
	// A taskId on another tool's result is not a launch.
	otherTaskID  = `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_other","type":"tool_result","content":"x"}]},"toolUseResult":{"taskId":"bnotone"}}`
	agentStarted = `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_agent","type":"tool_result","content":[{"type":"text","text":"Async agent launched successfully."}]}]},"toolUseResult":{"isAsync":true,"status":"async_launched","agentId":"a1234567890abcdef"}}`
	mcpMoved     = `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_mcp","type":"tool_result","content":[{"type":"text","text":"x"}]}]},"toolUseResult":[{"type":"text","text":"MCP tool \"gate-inbox/wait_for_session\" is still running after 120s. It was moved to the background as task kwait1 and keeps running; you'll receive a notification with the result."}]}`
	// Somebody's grep output quoting that same text is not a launch.
	mcpQuoted = `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_grep","type":"tool_result","content":"x"}]},"toolUseResult":{"stdout":"log: MCP tool \"a/b\" is still running after 120s. It was moved to the background as task kquoted and keeps running","stderr":""}}`

	bash1Notified = `{"type":"queue-operation","operation":"enqueue","timestamp":"2026-10-02T00:50:00Z","content":"<task-notification>\n<task-id>bjudge1</task-id>\n<status>completed</status>\n</task-notification>"}`
	agentNotified = `{"type":"attachment","attachment":{"type":"queued_command","prompt":"<task-notification>\n<task-id>a1234567890abcdef</task-id>\n<status>completed</status>\n</task-notification>"}}`
	mcpNotified   = `{"type":"user","message":{"role":"user","content":"<task-notification>\n<task-id>kwait1</task-id>\n<status>completed</status>\n</task-notification>"}}`
	monitorStop   = `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_stop","type":"tool_result","content":"stopped"}]},"toolUseResult":{"message":"Successfully stopped task: bmon1 (tail -f x)","task_id":"bmon1","task_type":"local_bash"}}`
	// A notification quoted inside a tool result ends nothing.
	bash2Quoted   = `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_cat","type":"tool_result","content":"<task-notification>\n<task-id>bjudge2</task-id>\n<status>completed</status>"}]},"toolUseResult":{"stdout":"<task-notification>\n<task-id>bjudge2</task-id>"}}`
	bash2Notified = `{"type":"user","message":{"role":"user","content":"<task-notification>\n<task-id>bjudge2</task-id>\n<status>killed</status>\n</task-notification>"}}`
)

func appendLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		t.Fatal(err)
	}
}

func pendingNow(t *testing.T, b *Background, path string) []string {
	t.Helper()
	ids, ok := b.Pending(path)
	if !ok {
		t.Fatalf("Pending(%s) could not read the transcript", path)
	}
	return ids
}

// Each kind of work that outlives a turn stays pending until Claude's own
// notification for it, or the stop that ends it, lands in the transcript --
// followed from where the last read stopped.
func TestBackgroundFollowsTasksUntilTheyEnd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conv.jsonl")
	b := NewBackground()
	appendLines(t, path, bashStarted, bashStarted2, monitorCalled, monitorResult, otherTaskID, agentStarted, mcpMoved, mcpQuoted)
	want := []string{"a1234567890abcdef", "bjudge1", "bjudge2", "bmon1", "kwait1"}
	if got := pendingNow(t, b, path); !slices.Equal(got, want) {
		t.Fatalf("pending = %v, want %v", got, want)
	}
	appendLines(t, path, bash1Notified, agentNotified, mcpNotified, monitorStop, bash2Quoted)
	if got := pendingNow(t, b, path); !slices.Equal(got, []string{"bjudge2"}) {
		t.Fatalf("pending = %v, want only the shell whose notification was merely quoted", got)
	}
	appendLines(t, path, bash2Notified)
	if got := pendingNow(t, b, path); len(got) != 0 {
		t.Fatalf("pending = %v, want none once every task ended", got)
	}
}

// A line the writer has not finished is held until it is, rather than read
// as garbage and lost.
func TestBackgroundWaitsOutAHalfWrittenLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conv.jsonl")
	b := NewBackground()
	half := len(bashStarted) / 2
	if err := os.WriteFile(path, []byte(bashStarted[:half]), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := pendingNow(t, b, path); len(got) != 0 {
		t.Fatalf("pending = %v from half a line", got)
	}
	appendLines(t, path, bashStarted[half:])
	if got := pendingNow(t, b, path); !slices.Equal(got, []string{"bjudge1"}) {
		t.Fatalf("pending = %v, want the shell once its line was complete", got)
	}
}

// A transcript that cannot be read says nothing either way, and one that
// shrank is a new file, read from its start.
func TestBackgroundOnAMissingOrReplacedTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conv.jsonl")
	b := NewBackground()
	if _, ok := b.Pending(path); ok {
		t.Fatal("a missing transcript read as known")
	}
	appendLines(t, path, bashStarted, bashStarted2, monitorCalled, monitorResult)
	if got := pendingNow(t, b, path); len(got) != 3 {
		t.Fatalf("pending = %v, want three", got)
	}
	if err := os.WriteFile(path, []byte(bashStarted+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := pendingNow(t, b, path); !slices.Equal(got, []string{"bjudge1"}) {
		t.Fatalf("pending = %v, want the replacement's one shell", got)
	}
}

// A launch stamped with another conversation is history a fork copied in:
// no process of this one is running it.
func TestBackgroundCountsOnlyItsOwnConversationsLaunches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conv-own.jsonl")
	b := NewBackground()
	copied := strings.Replace(bashStarted, `{"type":"user",`, `{"type":"user","sessionId":"conv-before-fork",`, 1)
	own := strings.Replace(bashStarted2, `{"type":"user",`, `{"type":"user","sessionId":"conv-own",`, 1)
	appendLines(t, path, copied, own)
	if got := pendingNow(t, b, path); !slices.Equal(got, []string{"bjudge2"}) {
		t.Fatalf("pending = %v, want only this conversation's own launch", got)
	}
}
