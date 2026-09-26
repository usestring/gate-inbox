package convo

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Reading a conversation forward from where a caller left off.
//
// The rest of this package answers "which conversation is in this pane", and
// reads a transcript's last 64KiB to do it. A parent checking a child wants
// the other question: what has this conversation said since I last looked.
// A pane capture cannot answer it -- it is the last screenful, so an hour of
// work and thirty seconds of work look the same -- and re-reading the tail
// answers it only by accident, when the gap happens to be smaller than the
// window.
//
// A Claude Code transcript is JSONL and append-only, which makes a byte
// offset an honest cursor: the bytes before it never change, so everything
// after it is exactly what is new. That is the whole mechanism. It is not a
// sequence number, because the file has no counter to read, and not a
// timestamp, because records carry the model's clock and a reader comparing
// against its own would drop or duplicate a turn at every boundary.

// deltaCap bounds a single delta read, so a caller returning after a long
// absence pays for a bounded window rather than for the whole file. The end
// is the part worth having, so the window is taken from the end and the
// caller is told its cursor was moved.
const deltaCap = 256 << 10

// Delta is what a conversation added after a byte offset.
type Delta struct {
	// Prompts are the user messages in the window, oldest first.
	Prompts []string
	// Turns are the assistant's prose turns in the window, oldest first.
	// Tool traffic is deliberately absent: it is the bulk of a transcript
	// and none of it is what a parent came to read.
	Turns []string
	// Results are the tool results the user side of the conversation sent
	// back, oldest first. They carry no text: a reader wanting to know that a
	// dialog was answered needs only that a result arrived and whether it was
	// the operator turning the call down.
	Results []Result
	// Next is the offset to pass to the next call.
	Next int64
	// Rewound is set when the requested offset could not be honoured -- the
	// file was shorter than it (a transcript replaced under the same id), or
	// the gap was larger than deltaCap. The window still ends at Next; it
	// just does not begin where the caller asked.
	Rewound bool
}

// Result is one tool_result a user record carried.
type Result struct {
	ToolUseID string
	// Rejected is a result the operator refused from the permission dialog,
	// which Claude Code writes as an error naming the rejection.
	Rejected bool
}

// LastTurn is the conversation's most recent prose, which is the closest
// thing a transcript holds to a result.
func (d Delta) LastTurn() string {
	if len(d.Turns) == 0 {
		return ""
	}
	return d.Turns[len(d.Turns)-1]
}

// Empty reports a window that added nothing a reader would want.
func (d Delta) Empty() bool { return len(d.Prompts) == 0 && len(d.Turns) == 0 }

// Since reads path from offset and returns what was appended after it.
//
// An offset of 0 reads the tail rather than the whole file: a first call has
// no cursor to be faithful to, and the end is what it wants. The cursor it
// gets back is the real end of the file either way, so the second call is a
// true delta however large the first one's file was.
func Since(path string, offset int64) (Delta, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Delta{}, err
	}
	size := info.Size()
	start, rewound := offset, false
	if start < 0 || start > size {
		// A shorter file than the cursor means this is not the file the
		// cursor was taken from. Saying so beats returning another
		// conversation's bytes as if they were this one's.
		start, rewound = 0, offset > 0
	}
	if size-start > deltaCap {
		start, rewound = size-deltaCap, true
	}
	// One byte before the window, to tell the two kinds of boundary apart. A
	// cursor handed back by a previous read sits exactly after a newline, and
	// its first line is whole; a window cut to a size lands mid-record, and
	// its first line is half of one. Dropping the first line unconditionally
	// loses a turn every time a caller comes back to a cursor of its own,
	// which is the common case rather than the rare one.
	probe := start
	if probe > 0 {
		probe--
	}
	raw, err := readRange(path, probe, size)
	if err != nil {
		return Delta{}, err
	}
	partial := false
	if start > 0 {
		if len(raw) > 0 && raw[0] == '\n' {
			raw = raw[1:]
		} else {
			partial = true
		}
	}
	delta := parseDelta(raw, partial)
	delta.Next = size
	delta.Rewound = rewound
	return delta, nil
}

func readRange(path string, start, end int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if start > 0 {
		if _, err := file.Seek(start, io.SeekStart); err != nil {
			return nil, err
		}
	}
	return io.ReadAll(io.LimitReader(file, end-start))
}

// parseDelta reads the records in a window of a transcript. partial drops the
// first line, which a window that did not start at byte zero cut in half.
func parseDelta(raw []byte, partial bool) Delta {
	lines := bytes.Split(raw, []byte{'\n'})
	if partial && len(lines) > 0 {
		lines = lines[1:]
	}
	var delta Delta
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var rec record
		if json.Unmarshal(line, &rec) != nil {
			continue
		}
		switch rec.Type {
		case "user":
			if rec.IsMeta {
				continue
			}
			if prompt := typedPrompt(rec.Message.Content); prompt != "" {
				delta.Prompts = append(delta.Prompts, prompt)
			}
			delta.Results = append(delta.Results, toolResults(rec.Message.Content)...)
		case "assistant":
			// Not Normalize: that folds text for comparing against a pane
			// capture, and this text is for a reader.
			for _, text := range assistantProse(rec.Message.Content) {
				delta.Turns = append(delta.Turns, text)
			}
		}
	}
	return delta
}

// rejectionMarks are the phrases Claude Code writes into the tool_result of a
// call the operator declined at its permission dialog.
var rejectionMarks = []string{"doesn't want to proceed", "tool use was rejected"}

// toolResults reads the tool_result parts of one user record.
func toolResults(content json.RawMessage) []Result {
	if len(content) == 0 || content[0] != '[' {
		return nil
	}
	var parts []struct {
		Type      string          `json:"type"`
		ToolUseID string          `json:"tool_use_id"`
		IsError   bool            `json:"is_error"`
		Content   json.RawMessage `json:"content"`
	}
	if json.Unmarshal(content, &parts) != nil {
		return nil
	}
	var out []Result
	for _, part := range parts {
		if part.Type != "tool_result" {
			continue
		}
		result := Result{ToolUseID: part.ToolUseID}
		if part.IsError {
			body := string(part.Content)
			for _, mark := range rejectionMarks {
				if strings.Contains(body, mark) {
					result.Rejected = true
					break
				}
			}
		}
		out = append(out, result)
	}
	return out
}

// assistantProse is assistantText without the normalising, so the prose comes
// back as the model wrote it.
func assistantProse(content json.RawMessage) []string {
	if len(content) == 0 {
		return nil
	}
	var single string
	if json.Unmarshal(content, &single) == nil {
		if text := strings.TrimSpace(single); text != "" {
			return []string{text}
		}
		return nil
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return nil
	}
	var out []string
	for _, block := range blocks {
		if block.Type != "text" {
			continue
		}
		if text := strings.TrimSpace(block.Text); text != "" {
			out = append(out, text)
		}
	}
	return out
}

// TranscriptFor is the file a Claude Code conversation is written to, or ""
// when nothing on disk holds it.
//
// The direct spelling is claudeHome/projects/<mangled cwd>/<id>.jsonl, and it
// is right whenever the caller's idea of the directory matches the one Claude
// Code recorded. It often does not -- a session created in one directory and
// resumed in another, a symlinked path, a row whose cwd was filled in from a
// pane -- so a miss falls back to looking for the id across the project
// directories. That walk lists names and opens nothing.
func TranscriptFor(claudeHome, conversationID, cwd string) string {
	if claudeHome == "" || conversationID == "" {
		return ""
	}
	if cwd != "" {
		direct := filepath.Join(claudeHome, "projects", projectDir(cwd), conversationID+".jsonl")
		if _, err := os.Stat(direct); err == nil {
			return direct
		}
	}
	root := filepath.Join(claudeHome, "projects")
	projects, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, project := range projects {
		if !project.IsDir() {
			continue
		}
		path := filepath.Join(root, project.Name(), conversationID+".jsonl")
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}
