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

// A pi session, filtered for handover.
//
// A pi session file is a tree, not a log: every entry after the header names
// its parent, and /tree, /fork-in-place and branch summaries leave abandoned
// branches in the same file. The conversation pi itself would resume is the
// path from the leaf (the last entry written) back to the root, so that path
// is all the copy keeps. On it, the latest compaction entry replaces what it
// summarised -- the copy keeps the compaction, the entries from its
// firstKeptEntryId up to it, and everything after -- and then the same three
// removals the other layouts get: a repeated entry collapses into the first
// of it, an assistant turn that declines is stubbed, and a tool result past
// the cap is cut with a marker. A removed entry's children are re-parented to
// its parent, so the copy is still one unbroken path.
//
// The header line passes through, and entries with no id (none in pi 1.5)
// are kept in place, since there is no way to say which branch they are on.

// piResultCap bounds one tool result in a filtered pi session.
const piResultCap = 8 << 10

type piEntry struct {
	raw      []byte
	id       string
	parentID string
	kind     string
}

type piFields struct {
	Type             string  `json:"type"`
	ID               string  `json:"id"`
	ParentID         *string `json:"parentId"`
	FirstKeptEntryID string  `json:"firstKeptEntryId"`
	Message          *struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// Pi filters the pi session at srcPath into dstPath, on the same terms as
// Claude: the source is never modified, and an error means the caller must
// not hand over the output file. KeepTo ends the copy at that line, and the
// entry there becomes the leaf: the rewind case.
func Pi(srcPath, dstPath string, opts Options) (Stats, error) {
	var stats Stats
	entries, err := readPiEntries(srcPath, opts.KeepTo)
	if err != nil {
		return stats, err
	}
	keep := piActivePath(entries)
	keep, stats.Compacted = piCompactionCut(entries, keep)

	dst, err := os.Create(dstPath)
	if err != nil {
		return stats, err
	}
	writer := bufio.NewWriter(dst)
	window := &recent{}
	// reparent maps a dropped entry's id to the parent its children take.
	reparent := map[string]string{}
	for i, entry := range entries {
		if entry.id != "" && !keep[i] {
			continue
		}
		out, kept := piLine(window, entry, reparent, &stats)
		if !kept {
			continue
		}
		if !bytes.HasSuffix(out, []byte{'\n'}) {
			out = append(out, '\n')
		}
		if _, werr := writer.Write(out); werr != nil {
			dst.Close()
			return stats, werr
		}
		stats.Kept++
	}
	if err := writer.Flush(); err != nil {
		dst.Close()
		return stats, err
	}
	return stats, dst.Close()
}

// readPiEntries reads the file's lines, up to keepTo inclusive when set.
func readPiEntries(path string, keepTo *int) ([]piEntry, error) {
	src, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	reader := bufio.NewReader(src)
	var entries []piEntry
	for index := 0; ; index++ {
		line, err := reader.ReadBytes('\n')
		atEOF := err == io.EOF
		if len(line) == 0 && atEOF {
			break
		}
		if err != nil && !atEOF {
			return nil, err
		}
		entry := piEntry{raw: line}
		var fields piFields
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 && trimmed[0] == '{' && json.Unmarshal(trimmed, &fields) == nil {
			entry.kind = fields.Type
			if fields.Type != "session" {
				entry.id = fields.ID
				if fields.ParentID != nil {
					entry.parentID = *fields.ParentID
				}
			}
		}
		entries = append(entries, entry)
		if atEOF || (keepTo != nil && index >= *keepTo) {
			break
		}
	}
	return entries, nil
}

// piActivePath marks the entries on the path from the leaf -- the last entry
// with an id -- back to the root.
func piActivePath(entries []piEntry) map[int]bool {
	byID := make(map[string]int, len(entries))
	leaf := -1
	for i, entry := range entries {
		if entry.id != "" {
			byID[entry.id] = i
			leaf = i
		}
	}
	keep := map[int]bool{}
	for at := leaf; at >= 0 && !keep[at]; {
		keep[at] = true
		parent, ok := byID[entries[at].parentID]
		if entries[at].parentID == "" || !ok {
			break
		}
		at = parent
	}
	return keep
}

// piCompactionCut applies the latest compaction on the active path the way
// pi rebuilds its context: the compaction entry, then the entries from its
// firstKeptEntryId up to it, then everything after it.
func piCompactionCut(entries []piEntry, keep map[int]bool) (map[int]bool, bool) {
	compaction, firstKept := -1, ""
	for i, entry := range entries {
		if !keep[i] || entry.kind != "compaction" {
			continue
		}
		var fields piFields
		if json.Unmarshal(bytes.TrimSpace(entry.raw), &fields) == nil {
			compaction, firstKept = i, fields.FirstKeptEntryID
		}
	}
	if compaction < 0 {
		return keep, false
	}
	from := compaction
	for i := 0; i < compaction; i++ {
		if keep[i] && entries[i].id == firstKept {
			from = i
			break
		}
	}
	cut := make(map[int]bool, len(keep))
	for i := range keep {
		if i >= from {
			cut[i] = true
		}
	}
	return cut, len(cut) < len(keep)
}

// piLine applies the loop, decline and size removals to one kept entry.
func piLine(window *recent, entry piEntry, reparent map[string]string, stats *Stats) ([]byte, bool) {
	trimmed := bytes.TrimSpace(entry.raw)
	if len(trimmed) == 0 || trimmed[0] != '{' || entry.id == "" {
		return entry.raw, true
	}
	var obj map[string]any
	if json.Unmarshal(trimmed, &obj) != nil {
		return entry.raw, true
	}
	parent := entry.parentID
	for {
		next, dropped := reparent[parent]
		if !dropped {
			break
		}
		parent = next
	}
	if identity := piIdentity(obj); identity != "" && window.seen(identity) {
		stats.Dropped++
		reparent[entry.id] = parent
		return nil, false
	}
	changed := parent != entry.parentID
	if changed {
		if parent == "" {
			obj["parentId"] = nil
		} else {
			obj["parentId"] = parent
		}
	}
	if msg, ok := obj["message"].(map[string]any); ok {
		changed = piRewriteMessage(msg, stats) || changed
	}
	if !changed {
		return entry.raw, true
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return entry.raw, true
	}
	return append(out, '\n'), true
}

// piIdentity is what makes two entries "the same turn": the message's role
// and texts, an assistant's tool calls by name and arguments, a result's
// text. Ids, timestamps and parents differ on every entry and are left out.
// Entries that are not messages have no identity and are never collapsed.
func piIdentity(obj map[string]any) string {
	if obj["type"] != "message" {
		return ""
	}
	msg, ok := obj["message"].(map[string]any)
	if !ok {
		return ""
	}
	role, _ := msg["role"].(string)
	if role == "system" {
		return ""
	}
	var b strings.Builder
	b.WriteString(role)
	switch content := msg["content"].(type) {
	case string:
		b.WriteString("\x00" + content)
	case []any:
		for _, item := range content {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			switch block["type"] {
			case "text":
				text, _ := block["text"].(string)
				b.WriteString("\x00" + text)
			case "toolCall":
				name, _ := block["name"].(string)
				args, _ := json.Marshal(block["arguments"])
				b.WriteString("\x00" + name + "\x00" + string(args))
			}
		}
	}
	return b.String()
}

// piRewriteMessage stubs a declining assistant text and caps a tool
// result's text, reporting whether it changed anything.
func piRewriteMessage(msg map[string]any, stats *Stats) bool {
	role, _ := msg["role"].(string)
	items, ok := msg["content"].([]any)
	if !ok {
		return false
	}
	changed, stubbed := false, false
	for _, item := range items {
		block, ok := item.(map[string]any)
		if !ok || block["type"] != "text" {
			continue
		}
		text, _ := block["text"].(string)
		switch role {
		case "assistant":
			if decline.LooksLike(text) {
				block["text"] = declineStub
				changed, stubbed = true, true
			}
		case "toolResult":
			if len(text) > piResultCap {
				block["text"] = truncate(text, piResultCap)
				changed = true
			}
		}
	}
	if stubbed {
		stats.Stubbed++
	}
	return changed
}

// piRecordSays reports whether a user or assistant message entry's text
// carries the normalized snippet.
func piRecordSays(trimmed []byte, snippet string) bool {
	var fields piFields
	if json.Unmarshal(trimmed, &fields) != nil || fields.Type != "message" || fields.Message == nil {
		return false
	}
	if fields.Message.Role != "user" && fields.Message.Role != "assistant" {
		return false
	}
	var single string
	if json.Unmarshal(fields.Message.Content, &single) == nil {
		return strings.Contains(normalize(single), snippet)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(fields.Message.Content, &blocks) != nil {
		return false
	}
	for _, block := range blocks {
		if block.Type == "text" && strings.Contains(normalize(block.Text), snippet) {
			return true
		}
	}
	return false
}
