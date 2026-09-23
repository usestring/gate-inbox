package convo

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/process"

	"github.com/usestring/gate-inbox/internal/band"
)

// tailBytes is how much of a transcript's end is read.
//
// Claude Code rewrites the ai-title and last-prompt records on every turn, so
// the end of the file carries the current values however long the session ran.
// 64KiB covers a full turn on 249 of the 251 titled transcripts on the machine
// this was measured against; the two it misses are single-turn sessions whose
// one turn is larger than the window, and they are missing a name for one
// refresh rather than forever.
const tailBytes = 64 << 10

// pidSkew bounds how far a live process's start time may sit from the one the
// sidecar recorded. The two differed by 1.5 to 11 seconds in practice. It is
// the only thing standing between a recycled pid and another session's name.
const pidSkew = 2 * time.Minute

// fallbackWindow is how recently a transcript with no live process behind it
// must have been written to be worth offering as a candidate. Anything older
// belongs to a session that has ended.
const fallbackWindow = 30 * time.Minute

// excerptCount is how many recent assistant messages are kept for matching a
// conversation to a pane. The pane shows the last screenful; more than a
// handful of messages is older than anything still visible.
const excerptCount = 6

// promptCount is how many recent user prompts are kept, which the drift rule
// then reads the last three of.
const promptCount = 8

// sidecar is the file Claude Code keeps per running process, at
// ~/.claude/sessions/<pid>.json. It is the whole reason attribution here is
// exact rather than a guess: it names the conversation and the process at once,
// and the process is in a pane's own tree or it is not.
type sidecar struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	StartedAt int64  `json:"startedAt"`
	ProcStart string `json:"procStart"`
	Tmux      string `json:"tmux"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
}

type tail struct {
	size    int64
	modTime time.Time
	convo   Conversation
}

// headBytes is how much of a transcript's start is read for its opening
// prompts. The first prompt is the first record with a message in it, so
// the window only has to reach past the mode and snapshot records ahead of
// it and the tool results that follow the first turn.
const headBytes = 64 << 10

// FirstPromptCount is how many opening prompts a conversation keeps.
const FirstPromptCount = 3

type head struct {
	size    int64
	prompts []string
	// complete is set once the head holds FirstPromptCount prompts or the
	// window was full: a file only grows at its end, so neither can change.
	complete bool
}

func (ix *Index) claudeConversations(dirs []string, cost *Cost) []Conversation {
	if ix.claude == "" {
		return nil
	}
	mark := time.Now()
	live := ix.liveSidecars()
	cost.Sidecars = time.Since(mark)

	byID := make(map[string]sidecar, len(live))
	for _, sc := range live {
		byID[sc.SessionID] = sc
	}

	mark = time.Now()
	paths := ix.transcriptPaths(byID, dirs)
	cost.Paths = time.Since(mark)

	mark = time.Now()
	defer func() { cost.Tails = time.Since(mark) }()

	var out []Conversation
	for path, sc := range paths {
		convo, ok := ix.readTranscript(path, cost)
		if !ok {
			continue
		}
		convo.Tool = "claude"
		convo.TranscriptPath = path
		if sc.SessionID != "" {
			convo.ID = sc.SessionID
			convo.PID = sc.PID
			convo.ProcStart = sc.ProcStart
			convo.TmuxHint = sc.Tmux
			if sc.Cwd != "" {
				convo.Cwd = sc.Cwd
			}
		}
		if convo.ID == "" {
			convo.ID = strings.TrimSuffix(filepath.Base(path), ".jsonl")
		}
		out = append(out, convo)
	}
	return out
}

// liveSidecars is the sidecar files whose process is still the process that
// wrote them. A stale file is left alone rather than deleted: it belongs to
// Claude Code, and this package does not write.
func (ix *Index) liveSidecars() []sidecar {
	entries, err := os.ReadDir(filepath.Join(ix.claude, "sessions"))
	if err != nil {
		return nil
	}
	var out []sidecar
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(ix.claude, "sessions", entry.Name()))
		if err != nil {
			continue
		}
		var sc sidecar
		if json.Unmarshal(raw, &sc) != nil || sc.PID <= 0 || sc.SessionID == "" {
			continue
		}
		if !pidMatches(sc) {
			continue
		}
		out = append(out, sc)
	}
	return out
}

func pidMatches(sc sidecar) bool {
	proc, err := process.NewProcess(int32(sc.PID))
	if err != nil {
		return false
	}
	created, err := proc.CreateTime()
	if err != nil {
		return false
	}
	skew := created - sc.StartedAt
	if skew < 0 {
		skew = -skew
	}
	return time.Duration(skew)*time.Millisecond <= pidSkew
}

// transcriptPaths pairs every transcript worth reading with the sidecar that
// names it, if any.
func (ix *Index) transcriptPaths(byID map[string]sidecar, dirs []string) map[string]sidecar {
	out := map[string]sidecar{}
	var missing []string
	for id, sc := range byID {
		path := filepath.Join(ix.claude, "projects", projectDir(sc.Cwd), id+".jsonl")
		if _, err := os.Stat(path); err == nil {
			out[path] = sc
			continue
		}
		missing = append(missing, id)
	}
	// A cwd that mangles to something other than the directory Claude Code
	// chose, and every pane whose tool leaves no sidecar, are both answered by
	// listing the project directories. That is names only -- no transcript is
	// opened for it.
	if len(missing) > 0 || len(dirs) > 0 {
		found := ix.scanProjects(missing, dirs)
		for path, sc := range found {
			if _, ok := out[path]; !ok {
				out[path] = sc
			}
		}
	}
	return out
}

func (ix *Index) scanProjects(missing []string, dirs []string) map[string]sidecar {
	want := make(map[string]bool, len(missing))
	for _, id := range missing {
		want[id] = true
	}
	interesting := make(map[string]bool, len(dirs))
	for _, dir := range dirs {
		interesting[projectDir(dir)] = true
	}
	root := filepath.Join(ix.claude, "projects")
	projects, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	cutoff := time.Now().Add(-fallbackWindow)
	out := map[string]sidecar{}
	for _, project := range projects {
		if !project.IsDir() {
			continue
		}
		wanted := interesting[project.Name()]
		files, err := os.ReadDir(filepath.Join(root, project.Name()))
		if err != nil {
			continue
		}
		for _, file := range files {
			name := file.Name()
			if !strings.HasSuffix(name, ".jsonl") {
				continue
			}
			id := strings.TrimSuffix(name, ".jsonl")
			path := filepath.Join(root, project.Name(), name)
			if want[id] {
				out[path] = sidecar{}
				continue
			}
			if !wanted {
				continue
			}
			info, err := file.Info()
			if err != nil || info.ModTime().Before(cutoff) {
				continue
			}
			out[path] = sidecar{}
		}
	}
	return out
}

// projectDir is Claude Code's own mangling of a working directory into the
// name of the folder its transcripts live in: every character that is not a
// letter or a digit becomes a hyphen.
func projectDir(cwd string) string {
	if cwd == "" {
		return ""
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		}
		return '-'
	}, cwd)
}

type record struct {
	Type       string `json:"type"`
	AITitle    string `json:"aiTitle"`
	LastPrompt string `json:"lastPrompt"`
	SessionID  string `json:"sessionId"`
	Cwd        string `json:"cwd"`
	Timestamp  string `json:"timestamp"`
	IsMeta     bool   `json:"isMeta"`
	Message    struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// readTranscript parses a transcript's tail, reusing the last parse when the
// file has not been appended to since.
func (ix *Index) readTranscript(path string, cost *Cost) (Conversation, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return Conversation{}, false
	}
	if cached, ok := ix.tails[path]; ok && cached.size == info.Size() && cached.modTime.Equal(info.ModTime()) {
		cost.Cached++
		return cached.convo, true
	}
	raw, err := readTail(path, info.Size(), tailBytes)
	if err != nil {
		return Conversation{}, false
	}
	cost.Files++
	cost.Bytes += int64(len(raw))
	convo := parseTail(raw, info.Size() > int64(len(raw)))
	convo.UpdatedAt = info.ModTime()
	convo.FirstPrompts = ix.readHead(path, info.Size(), cost)
	ix.tails[path] = tail{size: info.Size(), modTime: info.ModTime(), convo: convo}
	return convo, true
}

// readHead is the transcript's opening prompts, read once and then served
// from the cache: a finished head is final, and an unfinished one is only
// re-read when the file has grown since.
func (ix *Index) readHead(path string, size int64, cost *Cost) []string {
	if cached, ok := ix.heads[path]; ok && (cached.complete || cached.size == size) {
		return cached.prompts
	}
	raw, err := readHeadBytes(path, headBytes)
	if err != nil {
		return nil
	}
	cost.Heads++
	cost.Bytes += int64(len(raw))
	prompts := parseHead(raw)
	ix.heads[path] = head{size: size, prompts: prompts, complete: len(prompts) >= FirstPromptCount || len(raw) >= headBytes}
	return prompts
}

func readHeadBytes(path string, window int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, window))
}

// parseHead reads the first user prompts out of a transcript's opening
// bytes. The last line is dropped when the window cut it short. Tool
// results arrive as user records too and are not prompts, and neither is a
// slash command's expansion or a note the manager typed in on its own
// account.
func parseHead(raw []byte) []string {
	lines := bytes.Split(raw, []byte{'\n'})
	if len(lines) > 0 && int64(len(raw)) >= headBytes {
		lines = lines[:len(lines)-1]
	}
	var prompts []string
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var rec record
		if json.Unmarshal(line, &rec) != nil || rec.Type != "user" || rec.IsMeta {
			continue
		}
		prompt := typedPrompt(rec.Message.Content)
		if prompt == "" {
			continue
		}
		prompts = append(prompts, prompt)
		if len(prompts) >= FirstPromptCount {
			break
		}
	}
	return prompts
}

// typedPrompt is the text a person typed in a user record: a bare string, or
// the text parts of a content list. A list made of tool results has none,
// and a text part that is a system reminder or a slash command's expansion
// was not typed by anybody.
func typedPrompt(content json.RawMessage) string {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return typedText(text)
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &parts) != nil {
		return ""
	}
	var typed []string
	for _, part := range parts {
		if part.Type != "text" {
			continue
		}
		if piece := typedText(part.Text); piece != "" {
			typed = append(typed, piece)
		}
	}
	return strings.Join(typed, "\n")
}

// typedText drops what a user record carries that nobody typed: reminders
// the harness attaches, the command a slash expanded to, and the manager's
// own banded notes.
func typedText(text string) string {
	text = strings.TrimSpace(text)
	switch {
	case text == "":
		return ""
	case strings.HasPrefix(text, "<system-reminder>"), strings.HasPrefix(text, "<command-"),
		strings.HasPrefix(text, "<local-command"), band.Has(text):
		return ""
	}
	return text
}

func readTail(path string, size, window int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	offset := size - window
	if offset < 0 {
		offset = 0
	}
	if offset > 0 {
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return nil, err
		}
	}
	return io.ReadAll(file)
}

// parseTail reads the JSONL records in a transcript's final bytes. partial
// drops the first line, which a window into the middle of a file cuts in half.
func parseTail(raw []byte, partial bool) Conversation {
	lines := bytes.Split(raw, []byte{'\n'})
	if partial && len(lines) > 0 {
		lines = lines[1:]
	}
	var convo Conversation
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var rec record
		if json.Unmarshal(line, &rec) != nil {
			continue
		}
		if rec.Cwd != "" {
			convo.Cwd = rec.Cwd
		}
		if rec.SessionID != "" && convo.ID == "" {
			convo.ID = rec.SessionID
		}
		switch rec.Type {
		case "ai-title":
			if rec.AITitle != "" {
				convo.Title = rec.AITitle
			}
		case "last-prompt":
			prompt := strings.TrimSpace(rec.LastPrompt)
			// The record is rewritten on every turn whether or not the prompt
			// changed, so consecutive duplicates are one prompt.
			if prompt == "" || (len(convo.Prompts) > 0 && convo.Prompts[len(convo.Prompts)-1] == prompt) {
				continue
			}
			convo.Prompts = append(convo.Prompts, prompt)
			if len(convo.Prompts) > promptCount {
				convo.Prompts = convo.Prompts[1:]
			}
		case "assistant":
			for _, text := range assistantText(rec.Message.Content) {
				convo.Excerpts = append(convo.Excerpts, text)
				if len(convo.Excerpts) > excerptCount {
					convo.Excerpts = convo.Excerpts[1:]
				}
			}
		}
	}
	return convo
}

// assistantText pulls the prose out of an assistant message, which is either a
// bare string or a list of blocks of which only the text ones were on screen.
func assistantText(content json.RawMessage) []string {
	if len(content) == 0 {
		return nil
	}
	var single string
	if json.Unmarshal(content, &single) == nil {
		if text := Normalize(single); text != "" {
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
		if text := Normalize(block.Text); text != "" {
			out = append(out, text)
		}
	}
	return out
}

// Normalize folds text to the form a pane capture can be compared against:
// lowercase, single spaces. A terminal rewraps prose to its own width, so
// anything that survives has to be indifferent to where the line breaks fell.
func Normalize(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}
