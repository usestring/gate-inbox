package search

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/usestring/gate-inbox/internal/opencode"

	_ "modernc.org/sqlite"
)

// opencodeReader is the read-only handle on opencode's own database, opened
// on first use and skipped entirely when the database's files have not moved.
type opencodeReader struct {
	db   *sql.DB
	seen opencodeStamp
}

func (r *opencodeReader) close() error {
	if r.db == nil {
		return nil
	}
	return r.db.Close()
}

// opencodeStamp is the database and its WAL, which together prove nothing was
// written since the last pass. Both halves matter: a commit that lands wholly
// in the WAL leaves the database file untouched.
type opencodeStamp struct {
	db, wal stamp
}

// unchanged answers for both halves and returns the stamp to carry forward,
// which may have had to read a tail to decide.
func (s opencodeStamp) unchanged(path string, prev opencodeStamp) (bool, opencodeStamp) {
	dbSame, db := s.db.unchanged(path, prev.db)
	walSame, wal := s.wal.unchanged(path+"-wal", prev.wal)
	return dbSame && walSame, opencodeStamp{db: db, wal: wal}
}

func stampOf(path string) opencodeStamp {
	return opencodeStamp{db: stampOfFile(path), wal: stampOfFile(path + "-wal")}
}

// OpenCode completes tools by updating their existing rows. Re-read the
// cursor's millisecond too: another commit can share that timestamp.
//
// opencodePartsQueryV2 is the same read against the v2 layout, where one
// session_message row holds a whole projected message instead of one part.
// The type lives outside data, so restore it for the envelope parsers.
const opencodePartsQueryV2 = `SELECT id, json_set(data, '$.type', type), time_updated, ''
	FROM session_message
	WHERE session_id = ? AND time_updated >= ?
	ORDER BY time_updated, id`

type opencodeCursor struct {
	updated  int64
	boundary map[string][32]byte
}

type opencodeBatch struct {
	segments []Segment
	calls    []toolCall
	cursor   opencodeCursor
}

// refreshOpenCode catches every opencode target up.
func (x *Index) refreshOpenCode(targets []Target) (changed bool, rows int, err error) {
	if x.opencodePath == "" {
		return false, 0, nil
	}
	var wanted []Target
	for _, t := range targets {
		if t.Tool == ToolOpenCode && t.AgentID != "" {
			wanted = append(wanted, t)
		}
	}
	if len(wanted) == 0 {
		return false, 0, nil
	}
	seen := stampOf(x.opencodePath)
	x.mu.RLock()
	allKnown := true
	cursors := make(map[string]opencodeCursor, len(wanted))
	for _, t := range wanted {
		prev, ok := x.sessions[t.Key]
		if !ok || prev.path != t.AgentID {
			allKnown = false
			continue
		}
		cursors[t.Key] = prev.cursor
	}
	x.mu.RUnlock()
	same, seen := seen.unchanged(x.opencodePath, x.opencode.seen)
	if allKnown && same {
		return false, 0, nil
	}
	if x.opencode.db == nil {
		db, err := sql.Open("sqlite", "file:"+x.opencodePath+"?mode=ro&_pragma=busy_timeout(2000)")
		if err != nil {
			return false, 0, err
		}
		db.SetMaxOpenConns(1)
		x.opencode.db = db
	}
	for _, t := range wanted {
		cursor, known := cursors[t.Key]
		batch, err := x.readOpenCodeParts(t.AgentID, cursor)
		if err != nil {
			return changed, rows, err
		}
		if !known || len(batch.segments) > 0 || len(batch.calls) > 0 {
			var sb bytes.Buffer
			lines := writeSegments(&sb, batch.segments)
			target := Target{Key: t.Key, Tool: ToolOpenCode, Path: t.AgentID}
			x.splice(target, !known, 0, stamp{}, sb.Bytes(), batch.calls, lines)
			changed = true
			rows += lines
		}
		x.mu.Lock()
		if s := x.sessions[t.Key]; s != nil {
			s.cursor = batch.cursor
		}
		x.mu.Unlock()
	}
	x.opencode.seen = seen
	return changed, rows, nil
}

func (x *Index) readOpenCodeParts(sessionID string, cursor opencodeCursor) (opencodeBatch, error) {
	batch := opencodeBatch{cursor: opencodeCursor{updated: cursor.updated, boundary: map[string][32]byte{}}}
	schema, err := opencode.Layout(x.opencode.db)
	if err != nil || schema != opencode.SchemaV2 {
		return batch, err
	}
	rows, err := x.opencode.db.Query(opencodePartsQueryV2, sessionID, cursor.updated)
	if err != nil {
		return batch, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, data, message string
		var at int64
		if err := rows.Scan(&id, &data, &at, &message); err != nil {
			return batch, err
		}
		if at > batch.cursor.updated {
			batch.cursor = opencodeCursor{updated: at, boundary: map[string][32]byte{}}
		}
		digest := sha256.Sum256([]byte(data + "\x00" + message))
		batch.cursor.boundary[id] = digest
		if previous, ok := cursor.boundary[id]; at == cursor.updated && ok && previous == digest {
			continue
		}
		batch.segments = append(batch.segments, opencodeMessageSegments(data, x.limits)...)
		batch.calls = append(batch.calls, opencodeMessageCalls(id, data)...)
	}
	return batch, rows.Err()
}

// opencodeMessageSegments is the searchable text of one v2 projected
// message: the operator's text for a user message; each text content item,
// the tool name with its input strings, then the head of its output, for an
// assistant message. Reasoning items are skipped, as Claude's thinking
// blocks are.
func opencodeMessageSegments(data string, lim Limits) []Segment {
	var message struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			Name  string          `json:"name"`
			State json.RawMessage `json:"state"`
		} `json:"content"`
	}
	if json.Unmarshal([]byte(data), &message) != nil {
		return nil
	}
	switch message.Type {
	case "user":
		return segment(KindUser, capBytes(message.Text, lim.Text))
	case "assistant":
		var out []Segment
		for _, item := range message.Content {
			switch item.Type {
			case "text":
				out = append(out, segment(KindAssistant, capBytes(item.Text, lim.Text))...)
			case "tool":
				tool := v2ToolState(item.State)
				var sb strings.Builder
				appendPart(&sb, item.Name)
				appendPart(&sb, inputText(tool.Input, lim.Input))
				out = append(out, segment(KindInput, sb.String())...)
				out = append(out, segment(KindResult, capBytes(tool.Output, lim.Result))...)
			}
		}
		return out
	}
	return nil
}

// v2Tool flattens a v2 assistant tool item's state into the input object
// the PR regex reads and the output text the index keeps.
type v2Tool struct {
	Input  json.RawMessage
	Output string
}

func v2ToolState(raw json.RawMessage) v2Tool {
	var state struct {
		Input   json.RawMessage `json:"input"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	_ = json.Unmarshal(raw, &state)
	tool := v2Tool{Input: state.Input}
	var sb strings.Builder
	for _, item := range state.Content {
		if item.Type == "text" {
			sb.WriteString(item.Text)
		}
	}
	tool.Output = sb.String()
	return tool
}

// opencodeMessageCalls finds the PR-creating shell calls in v2 assistant
// tool items. The shell tool is matched as both "bash" and "shell".
func opencodeMessageCalls(id, data string) []toolCall {
	var message struct {
		Type    string `json:"type"`
		Content []struct {
			Type  string          `json:"type"`
			Name  string          `json:"name"`
			State json.RawMessage `json:"state"`
		} `json:"content"`
	}
	if json.Unmarshal([]byte(data), &message) != nil || message.Type != "assistant" {
		return nil
	}
	var out []toolCall
	for _, item := range message.Content {
		if item.Type != "tool" || (item.Name != "bash" && item.Name != "shell") {
			continue
		}
		var state struct {
			Status string          `json:"status"`
			Input  json.RawMessage `json:"input"`
		}
		if json.Unmarshal(item.State, &state) != nil || state.Status != "completed" {
			continue
		}
		var input struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(state.Input, &input) != nil || !createsPR.MatchString(input.Command) {
			continue
		}
		tool := v2ToolState(item.State)
		out = append(out, toolCall{id: id, text: input.Command},
			toolCall{id: id, result: true, text: capBytes(tool.Output, resultScan)})
	}
	return out
}
