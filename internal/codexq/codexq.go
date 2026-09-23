// Package codexq reads the state of Codex's interactive questions out of a
// session's rollout, because the pane cannot be trusted to hold them.
//
// Codex asks through two tools and loses the answer to both:
//
//   - request_user_input draws a dialog and blocks. Codex 0.153.4 arms an
//     auto-resolution timer on it, and when the timer fires the dialog leaves
//     the pane and the call returns {"answers":{}} -- an empty map, not an
//     answer. Measured at about two minutes, and the pane draws
//     "auto-resolves in Ns" only for the final 60s, so for the first half of
//     that life a doomed dialog is indistinguishable from one that waits.
//
//   - request_user_input_async draws no dialog at all. The question renders as
//     an ordinary transcript bullet with the input box still below it, the
//     call returns {"accepted":true} immediately, and the turn carries on.
//     There is no chrome to match and no way to answer it from the pane.
//
// Either way the board's pane rules stop matching and the row reverts with no
// record that anything was asked. The rollout keeps the whole exchange, so it
// is the honest source: a question is outstanding until an output carries a
// non-empty answers map, and nothing else counts as an answer.
package codexq

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"time"
)

// State is what became of one question.
type State int

const (
	// Outstanding is asked and still unresolved: no output yet, or an async
	// acknowledgement that only says the question was queued.
	Outstanding State = iota
	// Expired is resolved with an empty answers map. Codex's timer fired and
	// it proceeded as if it had asked and been told nothing.
	Expired
	// Answered is resolved with at least one answer.
	Answered
	// Superseded is unanswered, but the operator has spoken in the session
	// since it was asked. An expired question can never be answered -- its
	// dialog is gone -- so without this a row would read waiting for the rest
	// of the session's life and a silent loss would become permanent noise.
	// The retiring message lives in the rollout, so unlike an acked alert it
	// survives a board restart.
	Superseded
)

func (s State) String() string {
	switch s {
	case Expired:
		return "expired"
	case Answered:
		return "answered"
	case Superseded:
		return "superseded"
	}
	return "outstanding"
}

// Question is one ask and what became of it.
type Question struct {
	CallID string
	// Async is true for request_user_input_async, the variant that never
	// draws a dialog. It changes what the operator can do about the
	// question, so it is worth telling apart when reporting one.
	Async   bool
	Header  string
	Prompt  string
	AskedAt time.Time
	State   State
}

// Unresolved reports whether the question still needs the operator: nothing
// has come back for it, or the timer answered it for them, and they have not
// been back to the session since.
func (q Question) Unresolved() bool {
	return q.State == Outstanding || q.State == Expired
}

const (
	askTool      = "request_user_input"
	askToolAsync = "request_user_input_async"
	// maxLine caps one rollout record. A pasted file or a large tool result
	// can make a record enormous, and none of those are questions; the scan
	// must not buffer one into memory to find that out.
	maxLine = 1 << 20
)

var (
	askToolMark = []byte(askTool)
	outputMark  = []byte(`"function_call_output"`)
	userMark    = []byte(`"user"`)
)

type record struct {
	Payload *struct {
		Type      string          `json:"type"`
		Role      string          `json:"role"`
		Name      string          `json:"name"`
		CallID    string          `json:"call_id"`
		Arguments string          `json:"arguments"`
		Output    json.RawMessage `json:"output"`
		Meta      *struct {
			CreateTime float64 `json:"create_time"`
		} `json:"internal_chat_message_metadata_passthrough"`
	} `json:"payload"`
}

type askArgs struct {
	Questions []struct {
		Header   string `json:"header"`
		Question string `json:"question"`
	} `json:"questions"`
}

// Tracker follows one rollout as it is appended to.
//
// A rollout only grows, and the board polls every session about once a second
// against files that reach megabytes, so re-reading the whole thing per pass
// is work that scales with the length of the conversation rather than with
// what changed. Update reads from where the last one stopped and keeps the
// questions it has already paired.
type Tracker struct {
	offset    int64
	questions []Question
	index     map[string]int
}

// Update reads whatever has been appended to path since the last call and
// returns every question the rollout has shown so far, in ask order.
//
// A file shorter than the offset is a different conversation under the same
// name (or a truncation), so it is read from the start.
func (t *Tracker) Update(path string) ([]Question, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() < t.offset {
		t.reset()
	}
	if t.index == nil {
		t.index = map[string]int{}
	}
	if t.offset > 0 {
		if _, err := file.Seek(t.offset, io.SeekStart); err != nil {
			return nil, err
		}
	}
	read, err := t.consume(file)
	t.offset += read
	if err != nil {
		return t.questions, err
	}
	return t.questions, nil
}

func (t *Tracker) reset() {
	t.offset = 0
	t.questions = nil
	t.index = map[string]int{}
}

// consume reads whole records and reports how many bytes of them it took, so
// a half-written final record is left for the next call to read once the rest
// of it has landed rather than being parsed as truncated JSON and skipped for
// good.
func (t *Tracker) consume(r io.Reader) (int64, error) {
	var read int64
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			// No newline yet: this record is still being written.
			return read, nil
		}
		read += int64(len(line))
		if len(line) > maxLine {
			continue
		}
		t.record(line)
	}
}

func (t *Tracker) record(line []byte) {
	isAsk := bytes.Contains(line, askToolMark)
	// An output record names only its call_id -- the tool name appears on the
	// call alone -- so filtering outputs on askToolMark would drop every
	// resolution and leave answered and expired questions reading as
	// outstanding. A user message is read for the same reason a settled turn
	// is: it says the operator has been here since the question.
	if !isAsk && !bytes.Contains(line, outputMark) && !bytes.Contains(line, userMark) {
		return
	}
	var rec record
	if json.Unmarshal(line, &rec) != nil || rec.Payload == nil {
		return
	}
	p := rec.Payload
	switch p.Type {
	case "message":
		if p.Role != "user" {
			return
		}
		t.supersede()
	case "function_call":
		if p.Name != askTool && p.Name != askToolAsync {
			return
		}
		q := Question{CallID: p.CallID, Async: p.Name == askToolAsync, State: Outstanding}
		var args askArgs
		if json.Unmarshal([]byte(p.Arguments), &args) == nil && len(args.Questions) > 0 {
			q.Header = args.Questions[0].Header
			q.Prompt = args.Questions[0].Question
		}
		if p.Meta != nil && p.Meta.CreateTime > 0 {
			sec := int64(p.Meta.CreateTime)
			frac := p.Meta.CreateTime - float64(sec)
			q.AskedAt = time.Unix(sec, int64(frac*float64(time.Second))).UTC()
		}
		// A repeated call_id is the same question, not a second one.
		if at, seen := t.index[q.CallID]; seen {
			t.questions[at] = q
			return
		}
		t.index[q.CallID] = len(t.questions)
		t.questions = append(t.questions, q)
	case "function_call_output":
		at, seen := t.index[p.CallID]
		if !seen {
			return
		}
		t.questions[at].State = outputState(p.Output)
	}
}

// supersede retires the questions standing when the operator spoke. Only
// those: a question asked after this message is still theirs to answer.
func (t *Tracker) supersede() {
	for i, q := range t.questions {
		if q.State == Outstanding || q.State == Expired {
			t.questions[i].State = Superseded
		}
	}
}

// outputState reads what an output says about its question.
//
// The output is a JSON string holding JSON. Two shapes are not answers and
// must never be read as one: {"answers":{}}, which is the auto-resolution
// timer reporting that it gave up, and {"accepted":true}, which is the async
// tool acknowledging that the question was queued. Only a non-empty answers
// map is an answer.
func outputState(raw json.RawMessage) State {
	if len(raw) == 0 {
		return Outstanding
	}
	body := raw
	var inner string
	if json.Unmarshal(raw, &inner) == nil {
		body = json.RawMessage(inner)
	}
	var out struct {
		Answers  map[string]json.RawMessage `json:"answers"`
		Accepted *bool                      `json:"accepted"`
	}
	if json.Unmarshal(body, &out) != nil {
		return Outstanding
	}
	if len(out.Answers) > 0 {
		return Answered
	}
	if out.Answers != nil {
		// Present and empty: the timer fired.
		return Expired
	}
	// {"accepted":true} and anything else say nothing about an answer.
	return Outstanding
}

// Unresolved returns the questions from a rollout that still need the
// operator, newest last. It is the whole of what the board needs: a session
// with any of these has been asked something it never got told, and has not
// heard from the operator since.
func Unresolved(questions []Question) []Question {
	var out []Question
	for _, q := range questions {
		if q.Unresolved() {
			out = append(out, q)
		}
	}
	return out
}
