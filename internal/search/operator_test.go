package search

import (
	"reflect"
	"testing"
)

func TestOperatorMessagesKeepsOnlyTheUserExchange(t *testing.T) {
	messages := []Message{
		{Role: "user", Text: "fix the login bug"},
		{Role: "assistant", Text: "fixed"},
		{Role: "user", Text: "[gate-inbox] From the \"cache-warden\" extension running on this board, not from the user, sent 2026-10-01 22:53.\n\n----EXTENSION-MESSAGE-cache-warden-QX7M2K4D----\nAutomatic prompt-cache keep-alive, not a task. Reply with only the word ok.\n----EXTENSION-MESSAGE-cache-warden-QX7M2K4D----"},
		{Role: "assistant", Text: "ok"},
		{Role: "user", Text: `[gate-inbox] From agent "worker" (session abc), sent 2026-11-14 10:00.`},
		{Role: "assistant", Text: "replied to worker"},
		{Role: "user", Text: "<pasted_content id=\"x\">[gate-inbox] Message from another agent session, not from the user</pasted_content>"},
		{Role: "user", Text: "Auto wake-up: carry on"},
		{Role: "assistant", Text: "carrying on"},
		{Role: "user", Text: "<pasted_content id=\"y\">ship it</pasted_content>"},
		{Role: "assistant", Text: "shipped"},
	}
	want := []Message{
		{Role: "user", Text: "fix the login bug"},
		{Role: "assistant", Text: "fixed"},
		{Role: "assistant", Text: "replied to worker"},
		{Role: "assistant", Text: "carrying on"},
		{Role: "user", Text: "<pasted_content id=\"y\">ship it</pasted_content>"},
		{Role: "assistant", Text: "shipped"},
	}
	if got := OperatorMessages(messages); !reflect.DeepEqual(got, want) {
		t.Fatalf("OperatorMessages:\n got %#v\nwant %#v", got, want)
	}
}

// Another agent's words are its own to choose, so a message that happens to
// say "not a task" must not take the session's reply down with it.
func TestOperatorMessagesKeepsTheReplyToAnAgentSayingNotATask(t *testing.T) {
	messages := []Message{
		{Role: "user", Text: `[gate-inbox] From agent "worker" (session abc), sent 2026-11-14 10:00. FYI, not a task: the deploy is done.`},
		{Role: "assistant", Text: "noted the deploy"},
	}
	want := []Message{{Role: "assistant", Text: "noted the deploy"}}
	if got := OperatorMessages(messages); !reflect.DeepEqual(got, want) {
		t.Fatalf("OperatorMessages:\n got %#v\nwant %#v", got, want)
	}
}
