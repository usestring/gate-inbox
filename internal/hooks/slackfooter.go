package hooks

import (
	"encoding/json"
	"strings"
)

// SlackSendTool is the Slack MCP tool (korotovsky slack-mcp-server) a session
// posts a message through.
const SlackSendTool = "mcp__slack__conversations_add_message"

// SlackFooter is the line slackFooterCommand appends to a session's Slack
// post. A launched session can post as its operator, and readers have to be
// able to tell an agent's message from a person's whichever account, plan or
// working directory the session runs on. The board cannot tell an internal
// channel from a shared one, so the footer carries no session detail.
const SlackFooter = "(drafted with AI)"

// slackFooterCommand runs the slack-footer hook (SlackFooterInput) on every
// Slack post a launched session makes.
func slackFooterCommand() string { return hookCommandLine("slack-footer") }

// SlackFooterInput returns the PreToolUse output that appends SlackFooter to
// a Slack post, or "" to leave the post as it is.
//
// A post that already ends in an AI footer is left alone, so a repository
// hook that signs posts with a fuller footer is not signed twice. A Block Kit
// post renders from blocks and shows its text only as the notification, so a
// footer there would be invisible and is not added.
func SlackFooterInput(payload []byte) string {
	var call struct {
		ToolName  string         `json:"tool_name"`
		ToolInput map[string]any `json:"tool_input"`
	}
	if json.Unmarshal(payload, &call) != nil || call.ToolName != SlackSendTool || call.ToolInput == nil {
		return ""
	}
	text, _ := call.ToolInput["text"].(string)
	if strings.TrimSpace(text) == "" || hasBlocks(call.ToolInput["blocks"]) || endsInAIFooter(text) {
		return ""
	}
	input := make(map[string]any, len(call.ToolInput))
	for key, value := range call.ToolInput {
		input[key] = value
	}
	input["text"] = strings.TrimRight(text, "\n") + "\n\n" + SlackFooter
	out, err := json.Marshal(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName": "PreToolUse",
			"updatedInput":  input,
		},
	})
	if err != nil {
		return ""
	}
	return string(out)
}

func hasBlocks(blocks any) bool {
	switch b := blocks.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(b) != ""
	default:
		return true
	}
}

// endsInAIFooter reports whether the last non-blank line of a post is one of
// the AI footers String's tooling stamps: "(drafted with AI)", "(written by 🤖)",
// "(sent by 🤖) ..." and "🤖 via ...".
func endsInAIFooter(text string) bool {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.ToLower(strings.TrimSpace(lines[i]))
		if line == "" {
			continue
		}
		return line == "(drafted with ai)" || line == "(written by 🤖)" ||
			strings.HasPrefix(line, "(sent by 🤖)") || strings.HasPrefix(line, "(sent by :robot_face:)") ||
			strings.HasPrefix(line, "🤖 via ")
	}
	return false
}
