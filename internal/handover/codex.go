package handover

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/usestring/gate-inbox/extension/decline"
)

// A Codex rollout, filtered for handover.
//
// The same three removals a claude transcript gets, over the rollout's
// response_item rows: a repeated call collapses into the first of it (call
// ids are minted per call, so the identity is the call's name and arguments,
// never the id), an assistant turn that declines is stubbed, and an output
// past the cap is cut with a marker. Rows that carry no payload -- session
// meta, event_msg -- pass through untouched.

// codexOutputCap bounds one tool output in a filtered rollout.
const codexOutputCap = 8 << 10

// Codex filters the rollout at srcPath into dstPath, on the same terms as
// Claude: the source is never modified, and an error means the caller must
// not hand over the output file. CutLine set starts the copy there; a
// rollout has no compaction boundary of its own to cut to.
func Codex(srcPath, dstPath string, opts Options) (Stats, error) {
	var stats Stats
	src, err := os.Open(srcPath)
	if err != nil {
		return stats, err
	}
	defer src.Close()
	dst, err := os.Create(dstPath)
	if err != nil {
		return stats, err
	}
	reader := bufio.NewReader(src)
	writer := bufio.NewWriter(dst)
	window := &recent{}
	for index := 0; ; index++ {
		line, err := reader.ReadBytes('\n')
		atEOF := err == io.EOF
		if len(line) == 0 && atEOF {
			break
		}
		if err != nil && !atEOF {
			dst.Close()
			return stats, err
		}
		kept, keep := codexLine(window, line, &stats)
		if keep {
			if !bytes.HasSuffix(kept, []byte{'\n'}) {
				kept = append(kept, '\n')
			}
			if _, werr := writer.Write(kept); werr != nil {
				dst.Close()
				return stats, werr
			}
			stats.Kept++
		}
		if atEOF || (opts.KeepTo != nil && index >= *opts.KeepTo) {
			break
		}
	}
	if err := writer.Flush(); err != nil {
		dst.Close()
		return stats, err
	}
	return stats, dst.Close()
}

// codexRecordSays reports whether a response_item message's text carries the
// normalized snippet. event_msg rows repeat the same messages and are
// skipped, so a match names the conversation row and not its echo.
func codexRecordSays(trimmed []byte, snippet string) bool {
	var row struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(trimmed, &row) != nil || row.Type != "response_item" || len(row.Payload) == 0 {
		return false
	}
	var p struct {
		Type    string          `json:"type"`
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(row.Payload, &p) != nil || p.Type != "message" {
		return false
	}
	return strings.Contains(normalize(codexTexts(p.Content)), snippet)
}

func codexLine(window *recent, line []byte, stats *Stats) ([]byte, bool) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return line, true
	}
	var row struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(trimmed, &row) != nil || len(row.Payload) == 0 {
		return line, true
	}
	identity := row.Type + "\x00" + codexIdentity(row.Payload)
	// An identity the layout reader does not recognise is not loop spam: a
	// row shape this filter cannot compare is a row it passes through, the
	// way the claude filter treats records with no comparable message.
	if identity != row.Type+"\x00" && window.seen(identity) {
		stats.Dropped++
		return nil, false
	}
	return rewriteCodexPayload(trimmed, row, stats)
}

// codexIdentity is what makes two payloads "the same turn": the call's name
// and arguments, the message's texts, the output's text -- with call ids,
// which are minted per call, left out.
func codexIdentity(payload json.RawMessage) string {
	var p struct {
		Type      string          `json:"type"`
		Role      string          `json:"role"`
		Content   json.RawMessage `json:"content"`
		Name      string          `json:"name"`
		Input     string          `json:"input"`
		Arguments string          `json:"arguments"`
		Output    json.RawMessage `json:"output"`
	}
	if json.Unmarshal(payload, &p) != nil {
		return ""
	}
	switch p.Type {
	case "message":
		return p.Role + "\x00" + codexTexts(p.Content)
	case "function_call":
		return p.Name + "\x00" + p.Arguments
	case "custom_tool_call":
		return p.Name + "\x00" + p.Input
	case "function_call_output", "custom_tool_call_output":
		return codexTexts(p.Output)
	}
	return ""
}

// codexTexts flattens a payload field that is either a JSON string or a list
// of {type,text} items into the text it carries.
func codexTexts(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var single string
	if json.Unmarshal(raw, &single) == nil {
		return single
	}
	var items []map[string]any
	if json.Unmarshal(raw, &items) != nil {
		return ""
	}
	var b bytes.Buffer
	for _, item := range items {
		if text, ok := item["text"].(string); ok {
			b.WriteString(text)
		}
	}
	return b.String()
}

func rewriteCodexPayload(trimmed []byte, row struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}, stats *Stats) ([]byte, bool) {
	var obj map[string]any
	if json.Unmarshal(trimmed, &obj) != nil {
		return trimmed, true
	}
	payload, ok := obj["payload"].(map[string]any)
	if !ok {
		return trimmed, true
	}
	ptype, _ := payload["type"].(string)
	switch ptype {
	case "message":
		role, _ := payload["role"].(string)
		if role != "assistant" {
			return trimmed, true
		}
		items, ok := payload["content"].([]any)
		if !ok {
			return trimmed, true
		}
		stubbed := false
		for _, item := range items {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := m["text"].(string); ok && decline.LooksLike(text) {
				m["text"] = declineStub
				stubbed = true
			}
		}
		if !stubbed {
			return trimmed, true
		}
		stats.Stubbed++
	case "function_call_output", "custom_tool_call_output":
		if out, ok := payload["output"].(string); ok && len(out) > codexOutputCap {
			payload["output"] = truncate(out, codexOutputCap)
		} else if items, ok := payload["output"].([]any); ok {
			for _, item := range items {
				m, ok := item.(map[string]any)
				if !ok {
					continue
				}
				if text, ok := m["text"].(string); ok && len(text) > codexOutputCap {
					m["text"] = truncate(text, codexOutputCap)
				}
			}
		}
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return trimmed, true
	}
	return append(out, '\n'), true
}
