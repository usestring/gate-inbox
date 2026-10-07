package hooks

import (
	"encoding/json"
	"testing"
)

func TestSlackFooterInput(t *testing.T) {
	cases := []struct {
		name     string
		payload  string
		wantText string // "" means the hook leaves the post alone
	}{
		{
			name:     "plain post gets the footer",
			payload:  `{"tool_name":"mcp__slack__conversations_add_message","tool_input":{"channel_id":"D1","text":"hello"}}`,
			wantText: "hello\n\n(drafted with AI)",
		},
		{
			name:     "trailing newlines are folded into one gap",
			payload:  `{"tool_name":"mcp__slack__conversations_add_message","tool_input":{"channel_id":"D1","text":"hello\n\n"}}`,
			wantText: "hello\n\n(drafted with AI)",
		},
		{
			name:    "already signed by the repository hook",
			payload: `{"tool_name":"mcp__slack__conversations_add_message","tool_input":{"channel_id":"D1","text":"hello\n\n(sent by 🤖) <!-- agent=claude -->"}}`,
		},
		{
			name:    "already carries the minimal footer, any case",
			payload: `{"tool_name":"mcp__slack__conversations_add_message","tool_input":{"channel_id":"D1","text":"hello\n(Drafted with AI)\n"}}`,
		},
		{
			name:    "legacy written-by marker",
			payload: `{"tool_name":"mcp__slack__conversations_add_message","tool_input":{"channel_id":"D1","text":"hello\n\n(written by 🤖)"}}`,
		},
		{
			name:    "Block Kit post renders from blocks",
			payload: `{"tool_name":"mcp__slack__conversations_add_message","tool_input":{"channel_id":"D1","text":"fallback","blocks":"[{\"type\":\"section\"}]"}}`,
		},
		{
			name:    "empty text",
			payload: `{"tool_name":"mcp__slack__conversations_add_message","tool_input":{"channel_id":"D1","text":"  "}}`,
		},
		{
			name:    "another tool",
			payload: `{"tool_name":"Bash","tool_input":{"command":"ls","text":"hello"}}`,
		},
		{
			name:    "unparseable payload",
			payload: `not json`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SlackFooterInput([]byte(tc.payload))
			if tc.wantText == "" {
				if got != "" {
					t.Fatalf("SlackFooterInput = %s, want no output", got)
				}
				return
			}
			var out struct {
				HookSpecificOutput struct {
					HookEventName string         `json:"hookEventName"`
					UpdatedInput  map[string]any `json:"updatedInput"`
				} `json:"hookSpecificOutput"`
			}
			if err := json.Unmarshal([]byte(got), &out); err != nil {
				t.Fatalf("output %q is not JSON: %v", got, err)
			}
			if out.HookSpecificOutput.HookEventName != "PreToolUse" {
				t.Fatalf("hookEventName = %q, want PreToolUse", out.HookSpecificOutput.HookEventName)
			}
			if text := out.HookSpecificOutput.UpdatedInput["text"]; text != tc.wantText {
				t.Fatalf("text = %q, want %q", text, tc.wantText)
			}
			// updatedInput replaces the whole tool input, so every other field has to survive.
			if channel := out.HookSpecificOutput.UpdatedInput["channel_id"]; channel != "D1" {
				t.Fatalf("channel_id = %v, want D1 carried over", channel)
			}
		})
	}
}
