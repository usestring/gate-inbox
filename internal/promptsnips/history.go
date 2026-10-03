package promptsnips

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const tailBytes = 16 << 20

const (
	ToolClaude = "claude"
	ToolCodex  = "codex"
)

// ReadHistory reads submission logs, which record each prompt once even when
// resumed or forked transcripts copy earlier turns into new files.
func ReadHistory(claudeHome, codexHome string, since time.Time) ([]Submission, error) {
	var out []Submission
	for _, source := range []struct{ tool, path string }{
		{ToolClaude, filepath.Join(claudeHome, "history.jsonl")},
		{ToolCodex, filepath.Join(codexHome, "history.jsonl")},
	} {
		subs, err := readLog(source.tool, source.path, since)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		out = append(out, subs...)
	}
	return out, nil
}

func readLog(tool, path string, since time.Time) ([]Submission, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	start := max(0, info.Size()-tailBytes)
	data, err := io.ReadAll(io.NewSectionReader(f, start, info.Size()-start))
	if err != nil {
		return nil, err
	}
	if start > 0 {
		var previous [1]byte
		if _, err := f.ReadAt(previous[:], start-1); err != nil {
			return nil, err
		}
		if previous[0] != '\n' {
			_, data, _ = bytes.Cut(data, []byte("\n"))
		}
	}
	var out []Submission
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64<<10), len(data)+1)
	for row := 0; scanner.Scan(); row++ {
		if sub, ok := parseHistoryRow(tool, scanner.Bytes()); ok && !sub.At.Before(since) {
			sub.ID = tool + ":" + strconv.Itoa(row)
			out = append(out, sub)
		}
	}
	return out, scanner.Err()
}

func parseHistoryRow(tool string, line []byte) (Submission, bool) {
	var row struct {
		Display   string `json:"display"`
		Timestamp int64  `json:"timestamp"`
		Text      string `json:"text"`
		TS        int64  `json:"ts"`
	}
	if json.Unmarshal(line, &row) != nil {
		return Submission{}, false
	}
	var sub Submission
	switch tool {
	case ToolClaude:
		sub.Text, sub.At = row.Display, time.UnixMilli(row.Timestamp)
	case ToolCodex:
		sub.Text, sub.At = row.Text, time.Unix(row.TS, 0)
	}
	return sub, strings.TrimSpace(sub.Text) != ""
}
