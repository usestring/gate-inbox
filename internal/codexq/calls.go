package codexq

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/usestring/gate-inbox/internal/band"
)

const NoneOfTheAbove = "None of the above"

const NotePrefix = "user_note: "

type Option struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

type AskedQuestion struct {
	ID       string
	Header   string
	Question string
	Options  []Option
}

type Answer struct {
	Labels []string
	Note   string
}

type Call struct {
	CallID      string
	Async       bool
	AskedAt     time.Time
	Questions   []AskedQuestion
	State       State
	Answers     map[string]Answer
	AnswerTimes map[string]time.Time
	ResolvedAt  time.Time
	Reply       string
	ReplyAt     time.Time
}

type callRecord struct {
	Timestamp string `json:"timestamp"`
	Payload   *struct {
		Type      string          `json:"type"`
		Role      string          `json:"role"`
		Name      string          `json:"name"`
		CallID    string          `json:"call_id"`
		Arguments string          `json:"arguments"`
		Output    json.RawMessage `json:"output"`
		Content   []messagePart   `json:"content"`
		Meta      *struct {
			CreateTime float64 `json:"create_time"`
		} `json:"internal_chat_message_metadata_passthrough"`
	} `json:"payload"`
}

type callArgs struct {
	Questions []struct {
		ID       string          `json:"id"`
		Header   string          `json:"header"`
		Question string          `json:"question"`
		Title    string          `json:"title"`
		Options  json.RawMessage `json:"options"`
	} `json:"questions"`
}

func ReadCalls(path string) ([]Call, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readCalls(file)
}

func readCalls(r io.Reader) ([]Call, error) {
	var (
		calls []Call
		index = map[string]int{}
	)
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 && len(line) <= maxLine && err == nil {
			recordCall(line, &calls, index)
		}
		if err == io.EOF {
			return calls, nil
		}
		if err != nil {
			return calls, err
		}
	}
}

func recordCall(line []byte, calls *[]Call, index map[string]int) {
	if !bytes.Contains(line, askToolMark) && !bytes.Contains(line, outputMark) && !bytes.Contains(line, userMark) {
		return
	}
	var rec callRecord
	if json.Unmarshal(line, &rec) != nil || rec.Payload == nil {
		return
	}
	p := rec.Payload
	at := recordTime(rec.Timestamp)
	if p.Meta != nil && p.Meta.CreateTime > 0 {
		sec := int64(p.Meta.CreateTime)
		at = time.Unix(sec, int64((p.Meta.CreateTime-float64(sec))*float64(time.Second))).UTC()
	}
	switch p.Type {
	case "message":
		if p.Role != "user" {
			return
		}
		reply, operator := operatorMessage(p.Content)
		if !operator {
			return
		}
		if strings.Contains(reply, questionReplyOpen) || strings.Contains(reply, questionReplyClose) {
			applyQuestionReply(reply, at, *calls, index)
			return
		}
		pendingAsync := 0
		for _, call := range *calls {
			if call.Async && call.State == Outstanding {
				pendingAsync++
			}
		}
		for i := range *calls {
			call := &(*calls)[i]
			if call.State == Outstanding || call.State == Expired {
				if call.Async && (pendingAsync != 1 || approvalNeedsChoice(*call, reply)) {
					continue
				}
				if call.Async && call.State == Outstanding && pendingAsync == 1 && len(call.Questions) == 1 &&
					call.Reply == "" && reply != "" && at.After(call.AskedAt) {
					call.Reply, call.ReplyAt = reply, at
				}
				call.State = Superseded
			}
		}
	case "function_call":
		if p.Name != askTool && p.Name != askToolAsync {
			return
		}
		call := Call{CallID: p.CallID, Async: p.Name == askToolAsync, AskedAt: at, State: Outstanding,
			Questions: parseQuestions(p.Arguments)}
		if call.Async {
			for i := range call.Questions {
				call.Questions[i].ID = call.CallID + ":" + strconv.Itoa(i)
			}
		}
		if at, seen := index[call.CallID]; seen {
			(*calls)[at] = call
			return
		}
		index[call.CallID] = len(*calls)
		*calls = append(*calls, call)
	case "function_call_output":
		at2, seen := index[p.CallID]
		if !seen {
			return
		}
		call := &(*calls)[at2]
		state := outputState(p.Output)
		if state == Outstanding {
			return
		}
		call.State, call.ResolvedAt = state, at
		if state == Answered {
			call.Answers = parseAnswers(p.Output)
		}
	}
}

func recordTime(stamp string) time.Time {
	at, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return time.Time{}
	}
	return at.UTC()
}

func parseQuestions(arguments string) []AskedQuestion {
	var args callArgs
	if json.Unmarshal([]byte(arguments), &args) != nil {
		return nil
	}
	out := make([]AskedQuestion, 0, len(args.Questions))
	for _, q := range args.Questions {
		asked := AskedQuestion{ID: q.ID, Header: q.Header, Question: q.Question}
		if asked.Question == "" {
			asked.Question = q.Title
		}
		var options []Option
		if json.Unmarshal(q.Options, &options) != nil {
			options = nil
			var labels []string
			if json.Unmarshal(q.Options, &labels) == nil {
				for _, label := range labels {
					options = append(options, Option{Label: label})
				}
			}
		}
		asked.Options = options
		out = append(out, asked)
	}
	return out
}

func parseAnswers(raw json.RawMessage) map[string]Answer {
	body := raw
	var inner string
	if json.Unmarshal(raw, &inner) == nil {
		body = json.RawMessage(inner)
	}
	var out struct {
		Answers map[string]struct {
			Answers []string `json:"answers"`
		} `json:"answers"`
	}
	if json.Unmarshal(body, &out) != nil {
		return nil
	}
	answers := make(map[string]Answer, len(out.Answers))
	for id, given := range out.Answers {
		var answer Answer
		for _, entry := range given.Answers {
			if note, ok := strings.CutPrefix(entry, NotePrefix); ok {
				answer.Note = note
				continue
			}
			answer.Labels = append(answer.Labels, entry)
		}
		answers[id] = answer
	}
	return answers
}

func Root() string {
	if home := os.Getenv("CODEX_HOME"); home != "" {
		return filepath.Join(home, "sessions")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex", "sessions")
}

func RolloutPath(root, id string) string {
	if root == "" || id == "" {
		return ""
	}
	var found string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
			return nil
		}
		if !strings.HasSuffix(name, "-"+id+".jsonl") {
			_, suffix, resumed := strings.Cut(name, "-"+id+"_")
			if !resumed || suffix == ".jsonl" || !rolloutSessionMatches(path, id) {
				return nil
			}
		}
		if path > found {
			found = path
		}
		return nil
	})
	return found
}

func rolloutSessionMatches(path, id string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), maxLine)
	if !scanner.Scan() {
		return false
	}
	var meta struct {
		Type    string `json:"type"`
		Payload struct {
			ID        string `json:"id"`
			SessionID string `json:"session_id"`
		} `json:"payload"`
	}
	if json.Unmarshal(scanner.Bytes(), &meta) != nil || meta.Type != "session_meta" {
		return false
	}
	return (meta.Payload.ID == id || meta.Payload.SessionID == id) &&
		(meta.Payload.ID == "" || meta.Payload.ID == id) &&
		(meta.Payload.SessionID == "" || meta.Payload.SessionID == id)
}

type messagePart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func operatorMessage(content []messagePart) (string, bool) {
	var parts []string
	for _, part := range content {
		if part.Type != "input_text" {
			continue
		}
		text := strings.TrimSpace(part.Text)
		// Hook prompts and board relays also have role=user, but are not operator answers.
		if band.Has(text) || strings.HasPrefix(text, "<hook_prompt ") || strings.HasPrefix(text, "<hook_prompt>") {
			return "", false
		}
		parts = append(parts, part.Text)
	}
	return strings.Join(parts, "\n"), true
}

type parsed struct {
	offset int64
	calls  []Call
	index  map[string]int
}

var (
	parsedMu sync.Mutex
	parsedBy = map[string]*parsed{}
)

func CallsAt(path string) ([]Call, error) {
	parsedMu.Lock()
	defer parsedMu.Unlock()
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	state := parsedBy[path]
	if state == nil || info.Size() < state.offset {
		state = &parsed{index: map[string]int{}}
		parsedBy[path] = state
	}
	if info.Size() == state.offset {
		return append([]Call(nil), state.calls...), nil
	}
	if _, err := file.Seek(state.offset, io.SeekStart); err != nil {
		return nil, err
	}
	br := bufio.NewReaderSize(file, 64*1024)
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			break
		}
		state.offset += int64(len(line))
		if len(line) <= maxLine {
			recordCall(line, &state.calls, state.index)
		}
	}
	return append([]Call(nil), state.calls...), nil
}
