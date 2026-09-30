package convo

import (
	"strings"
	"testing"
)

func pendingCall(id, name string) string {
	return `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"` + id + `","name":"` + name + `"}]}}` + "\n"
}

func pendingResult(id string) string {
	return `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"` + id + `","content":"ok"}]}}` + "\n"
}

func TestPendingToolIdentifiesOnlyAnUnambiguousUnansweredCall(t *testing.T) {
	for _, tc := range []struct{ name, body, tool, want string }{
		{"single", pendingCall("ask", "AskUserQuestion"), "", "ask"},
		{"background call", pendingCall("bg", "Bash") + pendingCall("ask", "AskUserQuestion"), "AskUserQuestion", "ask"},
		{"ambiguous permission", pendingCall("bg", "Bash") + pendingCall("ask", "Read"), "", ""},
		{"ambiguous question", pendingCall("a", "AskUserQuestion") + pendingCall("b", "AskUserQuestion"), "AskUserQuestion", ""},
		{"already returned", pendingCall("ask", "AskUserQuestion") + pendingResult("ask"), "", ""},
		{"interrupted old call", pendingCall("old", "Bash") + prompt("[Request interrupted by user]") + pendingCall("ask", "Read"), "", "ask"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, next, err := PendingTool(writeDeltaTranscript(t, tc.body), tc.tool)
			if err != nil || id != tc.want || next != int64(len(tc.body)) {
				t.Fatalf("snapshot = %q, %d, %v; want %q, %d", id, next, err, tc.want, len(tc.body))
			}
		})
	}
}

func TestPendingToolLeavesPartialRecordsForTheNextRead(t *testing.T) {
	call, result := pendingCall("ask", "AskUserQuestion"), pendingResult("ask")
	path := writeDeltaTranscript(t, call+result[:len(result)/2])
	id, next, err := PendingTool(path, "AskUserQuestion")
	if err != nil || id != "ask" || next != int64(len(call)) {
		t.Fatalf("snapshot = %q, %d, %v", id, next, err)
	}
	appendDeltaTranscript(t, path, result[len(result)/2:])
	delta, err := Since(path, next)
	if err != nil || len(delta.Results) != 1 || delta.Results[0].ToolUseID != id {
		t.Fatalf("completed record = %+v, %v", delta, err)
	}
}

func TestPendingToolBoundsItsSnapshot(t *testing.T) {
	body := pendingCall("old", "Bash") + strings.Repeat("padding\n", deltaCap/4) + pendingCall("ask", "AskUserQuestion")
	id, _, err := PendingTool(writeDeltaTranscript(t, body), "")
	if err != nil || id != "" {
		t.Fatalf("bounded snapshot = %q, %v", id, err)
	}
	body += prompt("a new turn") + pendingCall("current", "AskUserQuestion")
	id, _, err = PendingTool(writeDeltaTranscript(t, body), "")
	if err != nil || id != "current" {
		t.Fatalf("bounded complete turn = %q, %v", id, err)
	}
}
