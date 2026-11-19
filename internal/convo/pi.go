package convo

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/band"
)

// pi keeps each conversation in one append-only JSONL file,
// <sessions>/--<cwd>--/<timestamp>_<id>.jsonl. The first line is a header
// ({"type":"session","id","cwd"}); after it come tree entries linked by
// id/parentId. A "message" entry carries an agent message whose role is
// user, assistant, toolResult or system, and a "session_info" entry carries
// the name /name, --name or the board's own extension gave the session.
//
// pi writes no model title of its own. The name is the operator's or the
// board's, which is the same name the row would want, so the latest
// session_info is the title and nothing is generated here either.

// PiTool is the tool name and session store pi conversations are filed under.
const PiTool = "pi"

// piWindow bounds the session files worth reading to ones touched recently
// enough that they could still be in a pane. A pane launched with an id is
// found by that id however old its file is.
const piWindow = 24 * time.Hour

// piFilesPerDir caps how many recent files are read per directory, newest
// first, so a directory with months of pi sessions costs a bounded pass.
const piFilesPerDir = 20

// PiSessionsDir is where pi keeps conversations, resolved the way pi resolves
// it: PI_CODING_AGENT_SESSION_DIR, else the sessions directory under
// PI_CODING_AGENT_DIR, else ~/.pi/agent/sessions. Empty when there is no home
// directory to resolve against.
func PiSessionsDir() string {
	if dir := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_SESSION_DIR")); dir != "" {
		return dir
	}
	if dir := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); dir != "" {
		return filepath.Join(dir, "sessions")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pi", "agent", "sessions")
}

// PiProjectDir is pi's folder name for cwd: the path without its leading
// separator, every "/", "\" and ":" turned into "-", inside "--".
func PiProjectDir(cwd string) string {
	trimmed := strings.TrimLeft(cwd, `/\`)
	return "--" + strings.NewReplacer("/", "-", `\`, "-", ":", "-").Replace(trimmed) + "--"
}

// PiTranscriptFor is the session file holding conversation id under root, or
// "" when none does. The file is named <timestamp>_<id>.jsonl inside the
// folder for the directory pi was started in, which is tried first; a session
// resumed or forked from elsewhere sits in another folder, so a miss globs
// across them. The header must name the id, so a file merely ending in it is
// not taken.
func PiTranscriptFor(root, id, cwd string) string {
	if root == "" || id == "" || strings.ContainsAny(id, `/\*?[`) {
		return ""
	}
	var patterns []string
	if cwd != "" {
		patterns = append(patterns, filepath.Join(root, PiProjectDir(cwd), "*_"+id+".jsonl"))
	}
	patterns = append(patterns, filepath.Join(root, "*", "*_"+id+".jsonl"))
	for _, pattern := range patterns {
		paths, _ := filepath.Glob(pattern)
		for _, path := range paths {
			if header, ok := ReadPiHeader(path); ok && header.ID == id {
				return path
			}
		}
	}
	return ""
}

// PiHeader is a pi session file's first line.
type PiHeader struct {
	Type          string `json:"type"`
	Version       int    `json:"version"`
	ID            string `json:"id"`
	Timestamp     string `json:"timestamp"`
	Cwd           string `json:"cwd"`
	ParentSession string `json:"parentSession"`
}

// ReadPiHeader reads the header of the session file at path.
func ReadPiHeader(path string) (PiHeader, bool) {
	file, err := os.Open(path)
	if err != nil {
		return PiHeader{}, false
	}
	defer file.Close()
	line, err := bufio.NewReaderSize(file, 4096).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return PiHeader{}, false
	}
	var header PiHeader
	if json.Unmarshal(line, &header) != nil || header.Type != "session" || header.ID == "" {
		return PiHeader{}, false
	}
	return header, true
}

// PiNewestFor is the most recently written session file pi keeps for cwd,
// with its header, for a pane whose conversation id nothing recorded. ok is
// false when the directory has none.
func PiNewestFor(root, cwd string) (string, PiHeader, bool) {
	paths := piFilesFor(root, cwd, time.Time{}, 0)
	for _, path := range paths {
		if header, ok := ReadPiHeader(path); ok && sameDir(header.Cwd, cwd) {
			return path, header, true
		}
	}
	return "", PiHeader{}, false
}

// piFilesFor lists the session files for cwd modified at or after since,
// newest first, at most limit of them (0 for all). Both the spelling given
// and the resolved path are tried, because the folder is named after the
// path as pi saw it.
func piFilesFor(root, cwd string, since time.Time, limit int) []string {
	if root == "" || cwd == "" {
		return nil
	}
	folders := []string{PiProjectDir(cwd)}
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil && resolved != cwd {
		folders = append(folders, PiProjectDir(resolved))
	}
	type file struct {
		path string
		mod  time.Time
	}
	var files []file
	for _, folder := range folders {
		paths, _ := filepath.Glob(filepath.Join(root, folder, "*.jsonl"))
		for _, path := range paths {
			info, err := os.Stat(path)
			if err != nil || info.ModTime().Before(since) {
				continue
			}
			files = append(files, file{path, info.ModTime()})
		}
	}
	sort.Slice(files, func(i, j int) bool {
		if !files[i].mod.Equal(files[j].mod) {
			return files[i].mod.After(files[j].mod)
		}
		return files[i].path > files[j].path
	})
	if limit > 0 && len(files) > limit {
		files = files[:limit]
	}
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.path
	}
	return out
}

func sameDir(a, b string) bool {
	if a == b {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// piFile is one session file as far as it has been read. The file only grows
// at its end, so a refresh reads from offset and folds the new entries into
// what is already known rather than re-reading megabytes of system prompt.
type piFile struct {
	offset  int64
	modTime time.Time
	convo   Conversation
}

// piConversations reads the pi session files for the panes' directories and
// for any id a pane was launched with.
func (ix *Index) piConversations(dirs []string, cost *Cost) []Conversation {
	if ix.pi == "" {
		return nil
	}
	since := time.Now().Add(-piWindow)
	seen := map[string]bool{}
	var out []Conversation
	for _, dir := range dirs {
		for _, path := range piFilesFor(ix.pi, dir, since, piFilesPerDir) {
			if seen[path] {
				continue
			}
			seen[path] = true
			if convo, ok := ix.readPi(path, cost); ok {
				out = append(out, convo)
			}
		}
	}
	return out
}

// readPi folds whatever the file gained since the last pass into its cached
// conversation.
func (ix *Index) readPi(path string, cost *Cost) (Conversation, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return Conversation{}, false
	}
	state, ok := ix.piFiles[path]
	if ok && state.offset == info.Size() && state.modTime.Equal(info.ModTime()) {
		cost.Cached++
		return state.convo, state.convo.ID != ""
	}
	if !ok || info.Size() < state.offset {
		// New, or shorter than what was read: not the file it was.
		state = &piFile{}
	}
	file, err := os.Open(path)
	if err != nil {
		return Conversation{}, false
	}
	defer file.Close()
	if _, err := file.Seek(state.offset, io.SeekStart); err != nil {
		return Conversation{}, false
	}
	raw, err := io.ReadAll(io.LimitReader(file, info.Size()-state.offset))
	if err != nil {
		return Conversation{}, false
	}
	cost.Files++
	cost.Bytes += int64(len(raw))
	// Only whole lines; a line still being written is read next pass.
	end := bytes.LastIndexByte(raw, '\n') + 1
	foldPi(&state.convo, raw[:end])
	state.offset += int64(end)
	state.modTime = info.ModTime()
	state.convo.TranscriptPath = path
	state.convo.UpdatedAt = info.ModTime()
	ix.piFiles[path] = state
	return state.convo, state.convo.ID != ""
}

// piEntry is the part of a session entry the reader looks at.
type piEntry struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Cwd     string `json:"cwd"`
	Name    string `json:"name"`
	Message struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// piSkipPrefix is how far into a line the cheap filter looks: far enough to
// reach the message role past the entry's id, parentId and timestamp.
const piSkipPrefix = 200

// foldPi applies the entries in raw to convo.
func foldPi(convo *Conversation, raw []byte) {
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' || !piWanted(line) {
			continue
		}
		var entry piEntry
		if json.Unmarshal(line, &entry) != nil {
			continue
		}
		applyPi(convo, entry)
	}
}

// piWanted keeps the decode off the lines that cannot matter: system
// messages carry the whole prompt and tool loadout and dwarf the rest of a
// session, and tool results are all output.
func piWanted(line []byte) bool {
	head := line
	if len(head) > piSkipPrefix {
		head = head[:piSkipPrefix]
	}
	switch {
	case bytes.Contains(head, []byte(`"type":"session_info"`)), bytes.Contains(head, []byte(`"type":"session"`)):
		return true
	case bytes.Contains(head, []byte(`"type":"message"`)):
		return bytes.Contains(head, []byte(`"role":"user"`)) || bytes.Contains(head, []byte(`"role":"assistant"`))
	}
	return false
}

func applyPi(convo *Conversation, entry piEntry) {
	switch entry.Type {
	case "session":
		convo.Tool = PiTool
		convo.ID = entry.ID
		convo.Cwd = entry.Cwd
	case "session_info":
		// Cleared with an empty name, which pi treats as no name.
		convo.Title = strings.TrimSpace(entry.Name)
	case "message":
		switch entry.Message.Role {
		case "user":
			prompt := typedPrompt(entry.Message.Content)
			if prompt == "" || band.Has(prompt) {
				return
			}
			if len(convo.FirstPrompts) < FirstPromptCount {
				convo.FirstPrompts = append(convo.FirstPrompts, prompt)
			}
			convo.Prompts = append(convo.Prompts, prompt)
			if len(convo.Prompts) > promptCount {
				convo.Prompts = convo.Prompts[1:]
			}
		case "assistant":
			for _, text := range assistantText(entry.Message.Content) {
				convo.Excerpts = append(convo.Excerpts, text)
				if len(convo.Excerpts) > excerptCount {
					convo.Excerpts = convo.Excerpts[1:]
				}
			}
		}
	}
}

// PiConversationInPane is the conversation a pi pane holds: the newest
// session file pi keeps for the pane's directory, which is the one a running
// pi last wrote to. The process cannot say which it holds: pi 1.5 sets its
// process title to "pi", which overwrites its argv, so a --session-id or
// --session it was started with is gone from /proc/<pid>/cmdline, and it keeps
// no session file open between writes. ok is false when the directory has no
// session file.
func PiConversationInPane(root, cwd string) (id, convCwd string, ok bool) {
	if _, header, ok := PiNewestFor(root, cwd); ok {
		return header.ID, firstNonEmpty(header.Cwd, cwd), true
	}
	return "", cwd, false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
