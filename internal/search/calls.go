package search

import (
	"bytes"
	"encoding/json"
	"regexp"
)

// toolCall is one half of a tool invocation as a transcript records it: the
// command a call ran, or the output its result carried, joined by id.
type toolCall struct {
	id     string
	result bool
	text   string
	poll   bool
}

var (
	// createsPR is the command a session opens a pull request with. The
	// repo's create-pr skill routes every PR through gh-pr-create.sh and
	// hard-blocks raw gh, so the wrapper is the common case and gh the
	// fallback for a standalone checkout.
	createsPR = regexp.MustCompile(`gh-pr-create\.sh|\bgh\s+pr\s+create\b`)
	pullURL   = regexp.MustCompile(`https://github\.com/[^/\s"]+/[^/\s"]+/pull/\d+`)

	// Prefilters, so the full parse below runs on the few rows that can
	// carry a creation and not on every turn of every transcript.
	prCreateMark   = []byte(`pr-create`)
	ghPrMark       = []byte(`gh pr create`)
	pullMark       = []byte(`/pull/`)
	codexCallOut   = []byte(`_call_output"`)
	pollMark       = []byte(`write_stdin`)
	waitMark       = []byte(`"wait"`)
	pollsCommand   = regexp.MustCompile(`\btools\.write_stdin\s*\(`)
	wrapperCreated = regexp.MustCompile(`gh-pr-create: labeled PR #([0-9]+) as '[^'\r\n]+'(?:\r?\n|\\n)(https://github\.com/[^/\s"]+/[^/\s"]+/pull/([0-9]+))(?:\r?\n|\\n|$)`)
)

// lineCalls reads the tool calls out of one transcript row. Most rows carry
// none and are rejected by a byte search before any JSON is decoded.
func lineCalls(tool string, line []byte) []toolCall {
	command := bytes.Contains(line, prCreateMark) || bytes.Contains(line, ghPrMark) || bytes.Contains(line, pollMark) || bytes.Contains(line, waitMark)
	result := bytes.Contains(line, pullMark) && bytes.Contains(line, toolResultMark) || bytes.Contains(line, codexCallOut)
	if !command && !result {
		return nil
	}
	switch tool {
	case ToolClaude:
		return claudeCalls(line)
	case ToolCodex:
		return codexCalls(line)
	}
	return nil
}

// claudeCalls: tool_use blocks sit in assistant rows and tool_result blocks in
// the user rows that follow, each result naming its call by tool_use_id.
func claudeCalls(line []byte) []toolCall {
	var row struct {
		Message *struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &row) != nil || row.Message == nil || len(row.Message.Content) == 0 || row.Message.Content[0] != '[' {
		return nil
	}
	var blocks []struct {
		Type      string          `json:"type"`
		ID        string          `json:"id"`
		ToolUseID string          `json:"tool_use_id"`
		Input     json.RawMessage `json:"input"`
		Content   json.RawMessage `json:"content"`
	}
	if json.Unmarshal(row.Message.Content, &blocks) != nil {
		return nil
	}
	var calls []toolCall
	for _, block := range blocks {
		switch block.Type {
		case "tool_use":
			var input struct {
				Command string `json:"command"`
			}
			if json.Unmarshal(block.Input, &input) == nil && input.Command != "" {
				calls = append(calls, toolCall{id: block.ID, text: input.Command})
			}
		case "tool_result":
			calls = append(calls, toolCall{id: block.ToolUseID, result: true, text: resultText(block.Content, resultScan)})
		}
	}
	return calls
}

// resultScan is how far into a result the URL is looked for. The wrapper
// prints it last, after the principles and the label line, and a result
// longer than this is a different command's output.
const resultScan = 64 << 10

// codexCalls: function_call rows carry the shell arguments as a JSON string
// with the command inside; function_call_output rows carry the output and the
// call_id that names their call.
func codexCalls(line []byte) []toolCall {
	var row struct {
		Payload *struct {
			Type      string          `json:"type"`
			CallID    string          `json:"call_id"`
			Arguments string          `json:"arguments"`
			Input     string          `json:"input"`
			Name      string          `json:"name"`
			Output    json.RawMessage `json:"output"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &row) != nil || row.Payload == nil {
		return nil
	}
	p := row.Payload
	switch p.Type {
	case "function_call":
		return []toolCall{{id: p.CallID, text: p.Arguments, poll: p.Name == "write_stdin" || p.Name == "wait"}}
	case "custom_tool_call":
		return []toolCall{{id: p.CallID, text: p.Input, poll: pollsCommand.MatchString(p.Input)}}
	case "function_call_output", "custom_tool_call_output":
		return []toolCall{{id: p.CallID, result: true, text: resultText(p.Output, resultScan)}}
	}
	return nil
}
