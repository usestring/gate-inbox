package codexq

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
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
	CallID     string
	Async      bool
	AskedAt    time.Time
	Questions  []AskedQuestion
	State      State
	Answers    map[string]Answer
	ResolvedAt time.Time
	Reply      string
	ReplyAt    time.Time
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
		Content   []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Meta *struct {
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
		var text []string
		for _, part := range p.Content {
			if part.Type == "input_text" && part.Text != "" {
				text = append(text, part.Text)
			}
		}
		for i := range *calls {
			call := &(*calls)[i]
			if call.State == Outstanding || call.State == Expired {
				call.State = Superseded
				if call.Async && call.Reply == "" {
					call.Reply, call.ReplyAt = strings.Join(text, "\n"), at
				}
			}
		}
	case "function_call":
		if p.Name != askTool && p.Name != askToolAsync {
			return
		}
		call := Call{CallID: p.CallID, Async: p.Name == askToolAsync, AskedAt: at, State: Outstanding,
			Questions: parseQuestions(p.Arguments)}
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

var (
	pathsMu sync.Mutex
	paths   = map[string]string{}
)

func RolloutPath(root, id string) string {
	if root == "" || id == "" {
		return ""
	}
	key := root + "\x00" + id
	pathsMu.Lock()
	cached := paths[key]
	pathsMu.Unlock()
	if cached != "" {
		if _, err := os.Stat(cached); err == nil {
			return cached
		}
	}
	var found []string
	suffix := id + ".jsonl"
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), suffix) {
			found = append(found, path)
		}
		return nil
	})
	if len(found) == 0 {
		return ""
	}
	sort.Strings(found)
	path := found[len(found)-1]
	pathsMu.Lock()
	paths[key] = path
	pathsMu.Unlock()
	return path
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
