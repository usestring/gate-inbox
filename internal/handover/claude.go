package handover

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/usestring/gate-inbox/extension/decline"
)

// A Claude Code transcript, filtered for handover.
//
// Three things are removed, in the order they matter: everything before the
// last compaction boundary, because the summary record at that boundary is
// the context a compaction already decided to keep; records that repeat one
// already in the recent window, because a stuck agent's loop is the bulk of
// what a replacement would otherwise read back; and the prose of a turn that
// declined the task, replaced by a one-line marker.
//
// Tool results past the cap are cut with a marker saying how much is
// missing. Nothing else changes: every other record passes through
// byte-identical, so the taking-over agent reads the format the prompt
// describes.

// toolResultCap bounds one tool result in a filtered transcript. A handover
// read needs what was learnt, not the payload that taught it.
const toolResultCap = 8 << 10

// claudeRecord is the slice of a transcript record the filter reads. The
// rest of the record is only ever copied.
type claudeRecord struct {
	Type    string          `json:"type"`
	Subtype string          `json:"subtype"`
	Message json.RawMessage `json:"message"`
}

// claudeMessage is the slice of a record's message the filter edits.
type claudeMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// Claude filters the transcript at srcPath into dstPath and reports what it
// removed. The source is never modified, and an error leaves dstPath
// unwritten or truncated -- a caller that gets an error must not hand over
// the filtered file.
func Claude(srcPath, dstPath string, opts Options) (Stats, error) {
	var stats Stats
	keepFrom, keepTo := 0, -1
	if opts.KeepTo != nil {
		// A rewind: everything after the deviation point is the drift the
		// rewind exists to remove, and the boundary logic defers to it.
		keepTo = *opts.KeepTo
		if line, err := lastBoundaryLine(srcPath); err != nil {
			return stats, err
		} else if line > 0 && line < keepTo {
			keepFrom = line
			stats.Compacted = true
		}
	} else {
		line, err := lastBoundaryLine(srcPath)
		if err != nil {
			return stats, err
		}
		keepFrom = line
		stats.Compacted = line > 0
	}
	src, err := os.Open(srcPath)
	if err != nil {
		return stats, err
	}
	defer src.Close()
	reader := bufio.NewReader(src)
	for skip := 0; skip < keepFrom; skip++ {
		if _, err := reader.ReadBytes('\n'); err != nil {
			return stats, err
		}
	}
	dst, err := os.Create(dstPath)
	if err != nil {
		return stats, err
	}
	writer := bufio.NewWriter(dst)
	window := &recent{}
	for index := keepFrom; ; index++ {
		line, err := reader.ReadBytes('\n')
		atEOF := err == io.EOF
		if len(line) == 0 && atEOF {
			break
		}
		if err != nil && !atEOF {
			dst.Close()
			return stats, err
		}
		kept, keep := claudeLine(window, line, &stats)
		if keep {
			// A rewritten record carries its own newline; a record passed
			// through verbatim was trimmed of its, so every written record
			// ends the same way the transcript's records do.
			if !bytes.HasSuffix(kept, []byte{'\n'}) {
				kept = append(kept, '\n')
			}
			if _, werr := writer.Write(kept); werr != nil {
				dst.Close()
				return stats, werr
			}
			stats.Kept++
		}
		if atEOF || (keepTo >= 0 && index >= keepTo) {
			break
		}
	}
	if err := writer.Flush(); err != nil {
		dst.Close()
		return stats, err
	}
	return stats, dst.Close()
}

// claudeLine rewrites one transcript record. A nil first return with keep
// false drops it; a modified record is re-marshalled, an untouched one is
// written verbatim.
func claudeLine(window *recent, line []byte, stats *Stats) ([]byte, bool) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return line, true
	}
	var rec claudeRecord
	if json.Unmarshal(trimmed, &rec) != nil {
		return line, true
	}
	if len(rec.Message) == 0 {
		return line, true
	}
	identity := claudeIdentity(rec.Type, rec.Message)
	if identity != "" && window.seen(identity) {
		stats.Dropped++
		return nil, false
	}
	return rewriteClaudeMessage(trimmed, rec, stats)
}

// claudeIdentity is what makes two records "the same turn". Tool use ids are
// minted per call, so a loop of identical calls carries fresh ids every time
// and the raw message never repeats; the identity is the payload a loop
// actually repeats -- the calls, their inputs, the prose, the results --
// with ids and timestamps left out. Empty when the message carries nothing
// comparable.
func claudeIdentity(recType string, message json.RawMessage) string {
	var msg claudeMessage
	if json.Unmarshal(message, &msg) != nil || len(msg.Content) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(recType)
	b.WriteByte(0)
	var single string
	if json.Unmarshal(msg.Content, &single) == nil {
		b.WriteString(single)
		return b.String()
	}
	var blocks []map[string]any
	if json.Unmarshal(msg.Content, &blocks) != nil {
		return ""
	}
	for _, block := range blocks {
		switch block["type"] {
		case "text", "thinking":
			b.WriteString(fmt.Sprint(block["type"]))
			b.WriteString(claudeText(block))
		case "tool_use":
			input, _ := json.Marshal(block["input"])
			b.WriteString("tool_use:")
			b.WriteString(fmt.Sprint(block["name"]))
			b.WriteString(string(input))
		case "tool_result":
			b.WriteString("tool_result:")
			b.WriteString(claudeResultText(block["content"]))
		}
	}
	return b.String()
}

// rewriteClaudeMessage applies the two edits a record can need -- stubbing a
// declined turn and capping a tool result -- writing the original bytes back
// when neither applies.
func rewriteClaudeMessage(trimmed []byte, rec claudeRecord, stats *Stats) ([]byte, bool) {
	var msg claudeMessage
	if json.Unmarshal(rec.Message, &msg) != nil || len(msg.Content) == 0 {
		return trimmed, true
	}
	var single string
	if json.Unmarshal(msg.Content, &single) == nil {
		if rec.Type == "assistant" && decline.LooksLike(single) {
			stats.Stubbed++
			return replaceClaudeContent(trimmed, declineStub), true
		}
		return trimmed, true
	}
	var blocks []map[string]any
	if json.Unmarshal(msg.Content, &blocks) != nil {
		return trimmed, true
	}
	// Two passes, because a stub anywhere in the record decides the whole
	// record: its thinking goes with its prose, and a thinking block that
	// precedes the prose cannot know that yet.
	stubbed := rec.Type == "assistant" && anyDecline(blocks)
	capped := false
	kept := make([]map[string]any, 0, len(blocks))
	for _, block := range blocks {
		switch block["type"] {
		case "text":
			if stubbed {
				block["text"] = declineStub
			}
		case "thinking":
			if stubbed {
				// The essay's source is the reasoning that led to it;
				// stubbing the prose and leaving the reasoning would hand
				// the replacement the very anchor this filter removes.
				continue
			}
		case "tool_result":
			if capToolResult(block) {
				capped = true
			}
		}
		kept = append(kept, block)
	}
	if !stubbed && !capped {
		return trimmed, true
	}
	if stubbed {
		stats.Stubbed++
	}
	return rewriteClaudeRecord(trimmed, kept), true
}

// anyDecline reports whether any text block in an assistant record declines
// the task.
func anyDecline(blocks []map[string]any) bool {
	for _, block := range blocks {
		if block["type"] == "text" && decline.LooksLike(claudeText(block)) {
			return true
		}
	}
	return false
}

// claudeText pulls the text out of a text or thinking block.
func claudeText(block map[string]any) string {
	text, _ := block["text"].(string)
	return text
}

// claudeResultText flattens a tool result's content: a bare string, or the
// text of its text blocks, in order.
func claudeResultText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var b strings.Builder
		for _, item := range v {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := m["text"].(string); ok {
				b.WriteString(text)
			}
		}
		return b.String()
	}
	return ""
}

// capToolResult truncates an oversized tool result in place, reporting
// whether it changed anything.
func capToolResult(block map[string]any) bool {
	switch content := block["content"].(type) {
	case string:
		if len(content) > toolResultCap {
			block["content"] = truncate(content, toolResultCap)
			return true
		}
	case []any:
		changed := false
		for _, item := range content {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := m["text"].(string); ok && len(text) > toolResultCap {
				m["text"] = truncate(text, toolResultCap)
				changed = true
			}
		}
		return changed
	}
	return false
}

// replaceClaudeContent swaps a bare-string message content for another
// string, keeping the rest of the record.
func replaceClaudeContent(trimmed []byte, content string) []byte {
	var obj map[string]any
	if json.Unmarshal(trimmed, &obj) != nil {
		return trimmed
	}
	msg, ok := obj["message"].(map[string]any)
	if !ok {
		return trimmed
	}
	msg["content"] = content
	out, err := json.Marshal(obj)
	if err != nil {
		return trimmed
	}
	return append(out, '\n')
}

// rewriteClaudeRecord writes a record back with its content blocks replaced,
// keeping everything the filter did not touch.
func rewriteClaudeRecord(trimmed []byte, blocks []map[string]any) []byte {
	var obj map[string]any
	if json.Unmarshal(trimmed, &obj) != nil {
		return trimmed
	}
	inner, ok := obj["message"].(map[string]any)
	if !ok {
		return trimmed
	}
	inner["content"] = blocks
	out, err := json.Marshal(obj)
	if err != nil {
		return trimmed
	}
	return append(out, '\n')
}

// claudeRecordSays reports whether a record's conversation text carries the
// normalized snippet: an assistant turn's prose and thinking, or a user
// turn's typed text. A compaction summary never matches: it is a
// reconstruction that quotes the whole history, so it would pull every
// search to the newest boundary and rewind to a summary rather than a turn.
func claudeRecordSays(trimmed []byte, snippet string) bool {
	var rec claudeRecord
	if json.Unmarshal(trimmed, &rec) != nil || len(rec.Message) == 0 {
		return false
	}
	var meta struct {
		Summary bool `json:"isCompactSummary"`
	}
	if json.Unmarshal(trimmed, &meta) == nil && meta.Summary {
		return false
	}
	var msg claudeMessage
	if json.Unmarshal(rec.Message, &msg) != nil || len(msg.Content) == 0 {
		return false
	}
	var single string
	if json.Unmarshal(msg.Content, &single) == nil {
		return strings.Contains(normalize(single), snippet)
	}
	var blocks []map[string]any
	if json.Unmarshal(msg.Content, &blocks) != nil {
		return false
	}
	for _, block := range blocks {
		switch block["type"] {
		case "text", "thinking":
			if strings.Contains(normalize(claudeText(block)), snippet) {
				return true
			}
		}
	}
	return false
}

// lastBoundaryLine is the index of the final compact_boundary record, or 0
// when the transcript has none. A compaction is the one place Claude Code
// itself decided what to keep, so a handover starts there.
func lastBoundaryLine(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	found, index := 0, 0
	for {
		line, err := reader.ReadBytes('\n')
		atEOF := err == io.EOF
		if len(line) == 0 && atEOF {
			break
		}
		if err != nil && !atEOF {
			return 0, err
		}
		if bytes.Contains(line, []byte("compact_boundary")) {
			var rec claudeRecord
			if json.Unmarshal(bytes.TrimSpace(line), &rec) == nil &&
				rec.Type == "system" && rec.Subtype == "compact_boundary" {
				found = index
			}
		}
		index++
		if atEOF {
			break
		}
	}
	return found, nil
}
