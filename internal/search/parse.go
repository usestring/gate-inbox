package search

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// Limits bound what one transcript line may put into the index, in bytes.
// Prose is what a query is usually about and is kept nearly whole; a tool's
// input is short by nature; a tool's result is where a 40MB transcript keeps
// its 40MB, so only its head is kept. Every cap lands on a rune boundary.
type Limits struct {
	Text   int
	Input  int
	Result int
}

// DefaultLimits are sanity ceilings, not budgets. Measured on the operator's
// board, indexing every byte of prose, input and result comes to 29MB for 55
// sessions against 21MB with results cut at 4KB, so the whole transcript is
// kept and only a pathological block — a binary dumped into a result — is
// cut. The per-session and total budgets in Options bound the rest.
var DefaultLimits = Limits{Text: 256 << 10, Input: 32 << 10, Result: 256 << 10}

// hugeLine is the size past which a line is assumed to be one tool output
// and read with the prefix scan instead of a full JSON parse. Parsing is the
// dominant cost of a cold index and a line this long is never prose.
const hugeLine = 256 << 10

// Tool names the format of a transcript, which is the agent CLI that wrote it.
const (
	ToolClaude   = "claude"
	ToolCodex    = "codex"
	ToolOpenCode = "opencode"
)

// Kind is who or what wrote a piece of a transcript, which is how much a
// hit in it is worth: a term the operator typed is what they are looking
// for; the same term in a tool's output is often incidental. Each kind is a
// control byte, stored at the head of every indexed line so a hit's kind is
// the first byte of its line, and no query can contain one. A line that
// continues the segment above it carries the kind with continuation set,
// so a turn is counted once however many lines it runs to.
type Kind byte

// continuation marks a line that continues the segment above it.
const continuation = 0x80

const (
	// KindUser is what the operator typed.
	KindUser Kind = 0x01
	// KindAssistant is what the agent said.
	KindAssistant Kind = 0x02
	// KindInput is what the agent handed a tool: a command, a path, a pattern.
	KindInput Kind = 0x03
	// KindResult is what a tool printed back.
	KindResult Kind = 0x04
	// KindSystem is text injected into a user turn by the harness — a
	// system reminder, a task notification, a slash command's expansion —
	// which the operator never typed.
	KindSystem Kind = 0x05
)

// Weight is how much one occurrence counts by kind, before recency.
func (k Kind) Weight() float64 {
	switch k {
	case KindUser:
		return 1
	case KindAssistant:
		return 0.7
	case KindInput:
		return 0.5
	case KindResult:
		return 0.3
	case KindSystem:
		return 0.15
	}
	return 0.5
}

// Segment is one searchable piece of a transcript line and its kind.
type Segment struct {
	Kind Kind
	Text string
}

// LineSegments is the searchable text of one transcript line by kind, or
// nothing when the line carries nothing a query is about: usage records,
// snapshots, progress events, thinking, and the developer turns that inject
// instructions.
func LineSegments(tool string, line []byte, lim Limits) []Segment {
	switch tool {
	case ToolClaude:
		return claudeSegments(line, lim)
	case ToolCodex:
		return codexSegments(line, lim)
	default:
		return genericSegments(line, lim)
	}
}

// LineText is LineSegments joined, one segment per line; the readers'
// tests and benchmarks compare against it.
func LineText(tool string, line []byte, lim Limits) string {
	var sb strings.Builder
	for _, seg := range LineSegments(tool, line, lim) {
		appendPart(&sb, seg.Text)
	}
	return sb.String()
}

var (
	claudeUser      = []byte(`"type":"user"`)
	claudeAssistant = []byte(`"type":"assistant"`)
	toolResultMark  = []byte(`"type":"tool_result"`)
	contentMark     = []byte(`"content":`)
	textMark        = []byte(`"text":"`)
	codexItemMark   = []byte(`"type":"response_item"`)
	codexOutputMark = []byte(`_call_output"`)
	outputMark      = []byte(`"output":`)
)

// claudeSegments reads a Claude Code JSONL row. The type marker is a plain
// byte search first because most rows in a transcript are not turns, and a
// full parse of every progress event would cost more than the turns do.
func claudeSegments(line []byte, lim Limits) []Segment {
	if !bytes.Contains(line, claudeUser) && !bytes.Contains(line, claudeAssistant) {
		return nil
	}
	if len(line) > hugeLine {
		if text, ok := hugeStringHead(line, toolResultMark, contentMark, lim.Result); ok {
			return segment(KindResult, text)
		}
	}
	var row struct {
		Message *struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &row) != nil || row.Message == nil {
		return nil
	}
	switch row.Message.Role {
	case "user":
		return contentSegments(row.Message.Content, KindUser, lim)
	case "assistant":
		return contentSegments(row.Message.Content, KindAssistant, lim)
	}
	return nil
}

// contentSegments renders a message's content: a bare string, or a block
// list where text, tool_use and tool_result are the searchable kinds. prose
// is the kind of the message's own text; tool blocks carry their own.
func contentSegments(content json.RawMessage, prose Kind, lim Limits) []Segment {
	if len(content) == 0 {
		return nil
	}
	if content[0] == '"' {
		var plain string
		if json.Unmarshal(content, &plain) != nil {
			return nil
		}
		return segment(proseKind(prose, plain), capBytes(plain, lim.Text))
	}
	var blocks []struct {
		Type    string          `json:"type"`
		Text    string          `json:"text"`
		Name    string          `json:"name"`
		Input   json.RawMessage `json:"input"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return nil
	}
	var out []Segment
	for _, block := range blocks {
		switch block.Type {
		case "text", "input_text", "output_text":
			out = append(out, segment(proseKind(prose, block.Text), capBytes(block.Text, lim.Text))...)
		case "tool_use":
			var sb strings.Builder
			appendPart(&sb, block.Name)
			appendPart(&sb, inputText(block.Input, lim.Input))
			out = append(out, segment(KindInput, sb.String())...)
		case "tool_result":
			out = append(out, segment(KindResult, resultText(block.Content, lim.Result))...)
		}
	}
	return out
}

// injectedPrefixes open the user-turn text a harness writes on the
// operator's behalf; a user turn starting with one was not typed.
var injectedPrefixes = []string{
	"<system-reminder>", "<task-notification>", "<command-name>", "<command-message>",
	"<command-args>", "<local-command-stdout>", "<local-command-caveat>", "<bash-input>",
	"<bash-stdout>", "<bash-stderr>", "<user-prompt-submit-hook>", "<ide_",
	"caveat: the messages below were generated by the user while running local commands",
	"this session is being continued from a previous conversation",
}

// proseKind is the kind of a message's own text: the user's, unless the
// harness wrote it into the user turn.
func proseKind(prose Kind, text string) Kind {
	if prose != KindUser {
		return prose
	}
	head := strings.ToLower(strings.TrimSpace(text))
	for _, prefix := range injectedPrefixes {
		if strings.HasPrefix(head, prefix) {
			return KindSystem
		}
	}
	return KindUser
}

func segment(kind Kind, text string) []Segment {
	if text == "" {
		return nil
	}
	return []Segment{{Kind: kind, Text: text}}
}

// inputText is a tool call's string arguments: the command, the path, the
// pattern. Nested objects and numbers are skipped; a query names what was
// typed, not the schema around it.
func inputText(input json.RawMessage, limit int) string {
	if len(input) == 0 || input[0] != '{' {
		return ""
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(input, &fields) != nil {
		return ""
	}
	var sb strings.Builder
	for _, raw := range fields {
		if len(raw) == 0 || raw[0] != '"' {
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) != nil {
			continue
		}
		appendPart(&sb, capBytes(value, limit))
		if sb.Len() >= limit {
			break
		}
	}
	return capBytes(sb.String(), limit)
}

// resultText is the head of a tool result, whether it came as a string or as
// text blocks.
func resultText(content json.RawMessage, limit int) string {
	if len(content) == 0 {
		return ""
	}
	if content[0] == '"' {
		var plain string
		if json.Unmarshal(content, &plain) != nil {
			return ""
		}
		return capBytes(plain, limit)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return ""
	}
	var sb strings.Builder
	for _, block := range blocks {
		switch block.Type {
		case "text", "input_text", "output_text":
		default:
			continue
		}
		appendPart(&sb, block.Text)
		if sb.Len() >= limit {
			break
		}
	}
	return capBytes(sb.String(), limit)
}

// hugeStringHead reads the head of a very long tool-output row without
// parsing the row: a Claude tool_result's content, or a Codex call output.
// It finds the block named by mark, the string under key beside it, takes a
// prefix that stops before any escape sequence, and decodes that prefix
// alone. ok is false whenever the row is not shaped as expected, and the
// caller falls back to the full parse, so this is a fast path and never a
// different answer.
func hugeStringHead(line, mark, key []byte, limit int) (string, bool) {
	at := bytes.Index(line, mark)
	if at < 0 {
		return "", false
	}
	// The value's key sits after the type key as the tools write it, and
	// before it when the row was re-encoded with sorted keys; the nearest
	// one on either side is the block's own.
	var rest []byte
	if c := bytes.Index(line[at:], key); c >= 0 {
		rest = line[at+c+len(key):]
	} else if c := bytes.LastIndex(line[:at], key); c >= 0 {
		rest = line[c+len(key):]
	} else {
		return "", false
	}
	if len(rest) == 0 {
		return "", false
	}
	switch rest[0] {
	case '"':
		rest = rest[1:]
	case '[':
		t := bytes.Index(rest, textMark)
		if t < 0 {
			return "", false
		}
		rest = rest[t+len(textMark):]
	default:
		return "", false
	}
	// Twice the cap leaves room for escapes that shrink on decode; the cut
	// then backs off to the last byte that is neither a closing quote nor
	// part of an escape, so the prefix is a complete JSON string body.
	window := rest[:min(len(rest), 2*limit)]
	end := len(window)
	if q := bytes.IndexByte(window, '"'); q >= 0 {
		end = q
	}
	if bs := bytes.IndexByte(window[:end], '\\'); bs >= 0 {
		end = bs
	}
	if end == 0 {
		return "", true
	}
	return capBytes(string(window[:end]), limit), true
}

// codexSegments reads a Codex rollout row. Only response_item rows carry
// the conversation; event_msg rows repeat the same messages and are skipped
// so a turn is indexed once.
func codexSegments(line []byte, lim Limits) []Segment {
	if !bytes.Contains(line, codexItemMark) {
		return nil
	}
	if len(line) > hugeLine && bytes.Contains(line, codexOutputMark) {
		if text, ok := hugeStringHead(line, codexOutputMark, outputMark, lim.Result); ok {
			return segment(KindResult, text)
		}
	}
	var row struct {
		Payload *struct {
			Type      string          `json:"type"`
			Role      string          `json:"role"`
			Content   json.RawMessage `json:"content"`
			Name      string          `json:"name"`
			Input     string          `json:"input"`
			Arguments string          `json:"arguments"`
			Output    json.RawMessage `json:"output"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &row) != nil || row.Payload == nil {
		return nil
	}
	p := row.Payload
	switch p.Type {
	case "message":
		switch p.Role {
		case "user":
			return contentSegments(p.Content, KindUser, lim)
		case "assistant":
			return contentSegments(p.Content, KindAssistant, lim)
		}
	case "function_call":
		var sb strings.Builder
		appendPart(&sb, p.Name)
		appendPart(&sb, capBytes(p.Arguments, lim.Input))
		return segment(KindInput, sb.String())
	case "custom_tool_call":
		var sb strings.Builder
		appendPart(&sb, p.Name)
		appendPart(&sb, capBytes(p.Input, lim.Input))
		return segment(KindInput, sb.String())
	case "function_call_output", "custom_tool_call_output":
		return segment(KindResult, resultText(p.Output, lim.Result))
	}
	return nil
}

// genericKeys are the fields a transcript of unknown layout is read through.
var genericKeys = map[string]bool{
	"text": true, "content": true, "prompt": true, "response": true,
	"command": true, "output": true, "result": true, "message": true,
}

// genericSegments is the fallback for a tool whose transcript format the
// index does not know: every string under a conversational key, anywhere in
// the row, capped as prose and weighted as the agent's own words.
func genericSegments(line []byte, lim Limits) []Segment {
	var row any
	if json.Unmarshal(line, &row) != nil {
		return nil
	}
	var sb strings.Builder
	collectGeneric(&sb, row, false, lim.Text)
	return segment(KindAssistant, capBytes(sb.String(), lim.Text))
}

func collectGeneric(sb *strings.Builder, node any, wanted bool, limit int) {
	if sb.Len() >= limit {
		return
	}
	switch v := node.(type) {
	case string:
		if wanted {
			appendPart(sb, v)
		}
	case []any:
		for _, item := range v {
			collectGeneric(sb, item, wanted, limit)
		}
	case map[string]any:
		for key, item := range v {
			collectGeneric(sb, item, wanted || genericKeys[key], limit)
		}
	}
}

func appendPart(sb *strings.Builder, part string) {
	if part == "" {
		return
	}
	if sb.Len() > 0 {
		sb.WriteByte('\n')
	}
	sb.WriteString(part)
}

// capBytes truncates to at most n bytes without splitting a rune.
func capBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
