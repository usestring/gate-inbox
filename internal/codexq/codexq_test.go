package codexq

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scanFile is the whole-file read the tests want; production reads the same
// records incrementally through the same Tracker.
func scanFile(t *testing.T, path string) []Question {
	t.Helper()
	var tr Tracker
	got, err := tr.Update(path)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	return got
}

// scanLines writes the records to a file so a test drives the same path
// production does. A trailing newline is what marks a record complete.
func scanLines(t *testing.T, lines ...string) []Question {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return scanFile(t, path)
}

// TestExpiredBlockingFixture reads the rollout records captured from the live
// probe on 2026-09-09 that proved the auto-resolution timer: the question was
// asked at 04:30:53.108 and resolved at 04:32:56.273 with {"answers":{}},
// 123.2s later, with nobody touching the session.
//
// The fixture is the whole point of the package. An earlier draft prefiltered
// every line on the tool name, which the output record does not carry -- it
// names only its call_id -- so the resolution was skipped and this question
// read as outstanding rather than expired.
//
// Its turn_id is a placeholder because a live one is a value no rerun
// reproduces, which the module's fixture check rejects; nothing here reads it
// -- the call_id is what pairs the ask with its output -- so a fresh capture
// gets scrubbed the same way before it is committed.
func TestExpiredBlockingFixture(t *testing.T) {
	got := scanFile(t, filepath.Join("testdata", "expired-blocking.jsonl"))
	if len(got) != 1 {
		t.Fatalf("got %d questions, want 1: %+v", len(got), got)
	}
	q := got[0]
	if q.State != Expired {
		t.Errorf("State = %v, want expired", q.State)
	}
	if q.Async {
		t.Error("Async = true, want false for request_user_input")
	}
	if q.Prompt != "Which colour should the probe use?" {
		t.Errorf("Prompt = %q", q.Prompt)
	}
	if q.Header != "Colour" {
		t.Errorf("Header = %q, want Colour", q.Header)
	}
	if !q.Unresolved() {
		t.Error("Unresolved() = false; an expired question still needs the operator")
	}
	if got, want := q.AskedAt.Format("15:04:05"), "04:30:53"; got != want {
		t.Errorf("AskedAt = %s, want %s", got, want)
	}
}

const askLine = `{"payload":{"type":"function_call","name":"request_user_input","call_id":"c1",` +
	`"arguments":"{\"questions\":[{\"header\":\"H\",\"question\":\"Q?\"}]}"}}`

func outLine(body string) string {
	return `{"payload":{"type":"function_call_output","call_id":"c1","output":` + body + `}}`
}

func TestOutputStates(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lines []string
		want  State
	}{
		{"no output yet", []string{askLine}, Outstanding},
		{"empty answers is the timer, not an answer", []string{askLine, outLine(`"{\"answers\":{}}"`)}, Expired},
		{"async ack only says queued", []string{askLine, outLine(`"{\"accepted\":true}"`)}, Outstanding},
		{"an answer resolves it", []string{askLine, outLine(`"{\"answers\":{\"probe\":\"red\"}}"`)}, Answered},
		{"output as an object, not a string", []string{askLine, outLine(`{"answers":{"probe":"red"}}`)}, Answered},
		{"unparseable output leaves it open", []string{askLine, outLine(`"not json"`)}, Outstanding},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := scanLines(t, tc.lines...)
			if len(got) != 1 {
				t.Fatalf("got %d questions, want 1", len(got))
			}
			if got[0].State != tc.want {
				t.Errorf("State = %v, want %v", got[0].State, tc.want)
			}
		})
	}
}

// TestAsyncQuestionIsTracked covers the variant that draws no dialog at all.
// Its immediate {"accepted":true} is an acknowledgement that the question was
// queued; the turn then ends with nothing more said about it, and the pane
// never had chrome for the board to match. The rollout is the only evidence
// it was ever asked.
func TestAsyncQuestionIsTracked(t *testing.T) {
	got := scanLines(t,
		`{"payload":{"type":"function_call","name":"request_user_input_async","call_id":"a1",`+
			`"arguments":"{\"questions\":[{\"header\":\"Colour\",\"question\":\"Which colour?\"}]}"}}`,
		`{"payload":{"type":"function_call_output","call_id":"a1","output":"{\"accepted\":true}"}}`,
	)
	if len(got) != 1 {
		t.Fatalf("got %d questions, want 1", len(got))
	}
	if !got[0].Async {
		t.Error("Async = false, want true")
	}
	if got[0].State != Outstanding {
		t.Errorf("State = %v, want outstanding: an ack is not an answer", got[0].State)
	}
}

// TestOperatorReturnSupersedes: an expired question can never be answered --
// its dialog is gone -- so without this the row would read waiting forever and
// the silent loss would become permanent noise. The operator's next message in
// the session is what retires it.
func TestOperatorReturnSupersedes(t *testing.T) {
	userLine := `{"payload":{"type":"message","role":"user","content":[{"type":"text","text":"never mind"}]}}`
	got := scanLines(t, askLine, outLine(`"{\"answers\":{}}"`), userLine)
	if len(got) != 1 {
		t.Fatalf("got %d questions, want 1", len(got))
	}
	if got[0].State != Superseded {
		t.Errorf("State = %v, want superseded", got[0].State)
	}
	if got[0].Unresolved() {
		t.Error("Unresolved() = true; the operator has been back since")
	}
}

// A question asked after the operator last spoke is still theirs to answer.
func TestQuestionAfterOperatorMessageStandsOpen(t *testing.T) {
	userLine := `{"payload":{"type":"message","role":"user","content":[{"type":"text","text":"go on"}]}}`
	got := scanLines(t, userLine, askLine, outLine(`"{\"answers\":{}}"`))
	if len(got) != 1 || got[0].State != Expired {
		t.Fatalf("got %+v, want one expired question", got)
	}
}

// An assistant message is the agent talking, not the operator returning.
func TestAssistantMessageDoesNotSupersede(t *testing.T) {
	line := `{"payload":{"type":"message","role":"assistant","content":[{"type":"text","text":"user"}]}}`
	got := scanLines(t, askLine, outLine(`"{\"answers\":{}}"`), line)
	if len(got) != 1 || got[0].State != Expired {
		t.Fatalf("got %+v, want one expired question", got)
	}
}

// TestUnrelatedToolsIgnored keeps the scan from claiming every function call
// is a question. A rollout is mostly shell and file calls.
func TestUnrelatedToolsIgnored(t *testing.T) {
	got := scanLines(t,
		`{"payload":{"type":"function_call","name":"shell","call_id":"s1","arguments":"{}"}}`,
		`{"payload":{"type":"function_call_output","call_id":"s1","output":"\"ok\""}}`,
		`{"payload":{"type":"message","role":"assistant","content":[{"text":"request_user_input"}]}}`,
	)
	if len(got) != 0 {
		t.Fatalf("got %d questions, want 0: %+v", len(got), got)
	}
}

// TestIncrementalUpdate is the reason Tracker exists: the board polls every
// session about once a second against rollouts that reach megabytes, so a
// pass must read what was appended rather than the whole conversation.
func TestIncrementalUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	write := func(lines ...string) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}

	var tr Tracker
	write(askLine)
	got, err := tr.Update(path)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(got) != 1 || got[0].State != Outstanding {
		t.Fatalf("first pass: %+v, want one outstanding", got)
	}
	first := tr.offset
	if first == 0 {
		t.Fatal("offset did not advance")
	}

	// The resolution lands; only it should be read.
	write(outLine(`"{\"answers\":{}}"`))
	got, err = tr.Update(path)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(got) != 1 || got[0].State != Expired {
		t.Fatalf("second pass: %+v, want one expired", got)
	}
	if tr.offset <= first {
		t.Errorf("offset %d did not advance past %d", tr.offset, first)
	}

	// Nothing appended: the same answer, no rescan.
	before := tr.offset
	if got, err = tr.Update(path); err != nil || len(got) != 1 || tr.offset != before {
		t.Errorf("idle pass changed something: %+v %v offset %d", got, err, tr.offset)
	}
}

// A half-written record must be left for the next pass rather than parsed as
// truncated JSON and skipped for good -- a rollout is appended to while it is
// read, so every question arrives this way at least once.
func TestPartialRecordIsReReadWhenComplete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	half := askLine[:40]
	if err := os.WriteFile(path, []byte(half), 0o644); err != nil {
		t.Fatal(err)
	}
	var tr Tracker
	got, err := tr.Update(path)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("a half-written record produced %+v", got)
	}
	if err := os.WriteFile(path, []byte(askLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err = tr.Update(path); err != nil || len(got) != 1 {
		t.Fatalf("completed record: %+v %v", got, err)
	}
}

// A shorter file is a different conversation under the same name, so the
// tracker must start over rather than seek past the whole of it.
func TestTruncationRestartsTheScan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte(askLine+"\n"+outLine(`"{\"answers\":{}}"`)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var tr Tracker
	if got, err := tr.Update(path); err != nil || len(got) != 1 {
		t.Fatalf("first: %+v %v", got, err)
	}
	if err := os.WriteFile(path, []byte(askLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := tr.Update(path)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(got) != 1 || got[0].State != Outstanding {
		t.Fatalf("after truncation: %+v, want one outstanding", got)
	}
}

func TestUnresolvedFiltersResolved(t *testing.T) {
	mk := func(id, name, output string) []string {
		call := `{"payload":{"type":"function_call","name":"` + name + `","call_id":"` + id + `",` +
			`"arguments":"{\"questions\":[{\"header\":\"H\",\"question\":\"Q?\"}]}"}}`
		if output == "" {
			return []string{call}
		}
		return []string{call, `{"payload":{"type":"function_call_output","call_id":"` + id + `","output":` + output + `}}`}
	}
	var lines []string
	lines = append(lines, mk("answered", "request_user_input", `"{\"answers\":{\"a\":\"red\"}}"`)...)
	lines = append(lines, mk("expired", "request_user_input", `"{\"answers\":{}}"`)...)
	lines = append(lines, mk("open", "request_user_input_async", `"{\"accepted\":true}"`)...)

	var ids []string
	for _, q := range Unresolved(scanLines(t, lines...)) {
		ids = append(ids, q.CallID)
	}
	if len(ids) != 2 || ids[0] != "expired" || ids[1] != "open" {
		t.Errorf("unresolved = %v, want [expired open]", ids)
	}
}
