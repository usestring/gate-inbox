package agentsession

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
)

// Background follows Claude Code transcripts for work that outlives the turn
// which started it: shells and monitors started in the background, agents
// launched async, and MCP calls moved to the background. Claude ends the turn
// and fires Stop while any of them runs, and wakes itself with a
// <task-notification> when one ends, so a session whose Stop has fired is not
// done while one is still pending. The pane says the same in its turn-end
// tail, but only as a line the terminal width can wrap; the transcript names
// every task by id.
//
// Each transcript is read once and then followed from where the last read
// stopped, so asking on every poll costs a stat and whatever was appended.
type Background struct {
	mu    sync.Mutex
	files map[string]*backgroundFile
}

type backgroundFile struct {
	// own is the conversation the transcript is named for. A launch counts
	// only on a line stamped with it, so history a fork copied in from
	// another conversation names nothing this process is running.
	own    string
	offset int64
	// partial is a trailing line the writer had not finished at the last
	// read.
	partial []byte
	// monitors is the tool_use ids of Monitor calls, whose result names a
	// task by a bare taskId that other tools could also carry.
	monitors map[string]bool
	started  map[string]bool
	ended    map[string]bool
}

// NewBackground returns an empty tracker.
func NewBackground() *Background {
	return &Background{files: map[string]*backgroundFile{}}
}

// Pending is the background tasks the transcript at path started and has not
// yet heard end, sorted. ok is false when the transcript cannot be read, which
// says nothing either way.
func (b *Background) Pending(path string) (ids []string, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	f, err := os.Open(path)
	if err != nil {
		delete(b.files, path)
		return nil, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, false
	}
	state := b.files[path]
	// A transcript only grows; a shorter one is a different file.
	if state == nil || info.Size() < state.offset {
		state = &backgroundFile{own: strings.TrimSuffix(filepath.Base(path), ".jsonl"), monitors: map[string]bool{}, started: map[string]bool{}, ended: map[string]bool{}}
		b.files[path] = state
	}
	if info.Size() > state.offset {
		if _, err := f.Seek(state.offset, io.SeekStart); err != nil {
			return nil, false
		}
		if err := state.read(f); err != nil {
			return nil, false
		}
	}
	for id := range state.started {
		if !state.ended[id] {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids, true
}

func (s *backgroundFile) read(r io.Reader) error {
	reader := bufio.NewReaderSize(r, 256*1024)
	for {
		line, err := reader.ReadBytes('\n')
		s.offset += int64(len(line))
		if err == io.EOF {
			s.partial = append(s.partial, line...)
			return nil
		}
		if err != nil {
			return err
		}
		if len(s.partial) > 0 {
			line = append(s.partial, line...)
			s.partial = nil
		}
		s.entry(line)
	}
}

// Markers that pick out the few lines worth decoding; a transcript runs to
// tens of megabytes and nearly all of it is neither.
var (
	markNotification = []byte("<task-notification>")
	markBackgroundID = []byte(`"backgroundTaskId"`)
	markAsync        = []byte(`"isAsync":true`)
	markTaskID       = []byte(`"taskId"`)
	markMonitor      = []byte(`"name":"Monitor"`)
	markMovedMCP     = []byte("moved to the background as task ")
	markStopped      = []byte(`"task_id"`)
	markKilledShell  = []byte(`"shell_id"`)
)

var (
	notifiedTaskID = regexp.MustCompile(`<task-id>([^<]+)</task-id>`)
	movedMCPTaskID = regexp.MustCompile(`^MCP tool ".*" is still running after .* moved to the background as task ([A-Za-z0-9_-]+)`)
)

type transcriptLine struct {
	Type      string          `json:"type"`
	SessionID string          `json:"sessionId"`
	Operation string          `json:"operation"`
	Content   json.RawMessage `json:"content"`
	Message   struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
	Attachment    struct {
		Type   string `json:"type"`
		Prompt string `json:"prompt"`
	} `json:"attachment"`
}

type contentBlock struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	ToolUseID string `json:"tool_use_id"`
}

type launchResult struct {
	BackgroundTaskID string `json:"backgroundTaskId"`
	IsAsync          bool   `json:"isAsync"`
	AgentID          string `json:"agentId"`
	TaskID           string `json:"taskId"`
	// StoppedTaskID and KilledShellID answer TaskStop and the older
	// KillShell, after which no notification arrives.
	Message       string `json:"message"`
	StoppedTaskID string `json:"task_id"`
	KilledShellID string `json:"shell_id"`
}

type resultText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (s *backgroundFile) entry(line []byte) {
	notification := bytes.Contains(line, markNotification)
	launch := bytes.Contains(line, markBackgroundID) || bytes.Contains(line, markAsync) ||
		bytes.Contains(line, markTaskID) || bytes.Contains(line, markMovedMCP) ||
		bytes.Contains(line, markStopped) || bytes.Contains(line, markKilledShell)
	monitor := bytes.Contains(line, markMonitor)
	if !notification && !launch && !monitor {
		return
	}
	var e transcriptLine
	if json.Unmarshal(line, &e) != nil {
		return
	}
	// A notification is Claude's own message to itself: queued the moment
	// the task ends, then delivered as the user turn's whole text, or attached
	// to the turn already running. One quoted
	// inside a tool result or a reply is somebody's output, not an ending.
	if notification {
		var text string
		switch {
		case e.Type == "queue-operation" && e.Operation == "enqueue":
			_ = json.Unmarshal(e.Content, &text)
		case e.Type == "user":
			_ = json.Unmarshal(e.Message.Content, &text)
		case e.Attachment.Type == "queued_command":
			text = e.Attachment.Prompt
		}
		if bytes.HasPrefix(bytes.TrimSpace([]byte(text)), markNotification) {
			for _, m := range notifiedTaskID.FindAllStringSubmatch(text, -1) {
				s.ended[m[1]] = true
			}
		}
	}
	var blocks []contentBlock
	_ = json.Unmarshal(e.Message.Content, &blocks)
	if e.Type == "assistant" && monitor {
		for _, b := range blocks {
			if b.Type == "tool_use" && b.Name == "Monitor" {
				s.monitors[b.ID] = true
			}
		}
	}
	if e.Type != "user" || len(e.ToolUseResult) == 0 {
		return
	}
	if e.SessionID != "" && e.SessionID != s.own {
		return
	}
	var result launchResult
	if json.Unmarshal(e.ToolUseResult, &result) == nil {
		switch {
		case result.BackgroundTaskID != "":
			s.started[result.BackgroundTaskID] = true
		case result.IsAsync && result.AgentID != "":
			s.started[result.AgentID] = true
		case result.TaskID != "" && s.resultOfMonitor(blocks):
			s.started[result.TaskID] = true
		case result.StoppedTaskID != "" && strings.HasPrefix(result.Message, "Successfully stopped task"):
			s.ended[result.StoppedTaskID] = true
		case result.KilledShellID != "" && strings.HasPrefix(result.Message, "Successfully killed shell"):
			s.ended[result.KilledShellID] = true
		}
		return
	}
	// An MCP call that outran its timeout returns Claude's own text, not
	// fields.
	var texts []resultText
	if json.Unmarshal(e.ToolUseResult, &texts) != nil {
		return
	}
	for _, t := range texts {
		if m := movedMCPTaskID.FindStringSubmatch(t.Text); t.Type == "text" && m != nil {
			s.started[m[1]] = true
		}
	}
}

func (s *backgroundFile) resultOfMonitor(blocks []contentBlock) bool {
	for _, b := range blocks {
		if b.Type == "tool_result" && s.monitors[b.ToolUseID] {
			return true
		}
	}
	return false
}
