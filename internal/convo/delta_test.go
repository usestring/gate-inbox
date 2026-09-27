package convo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// turn is one assistant record as Claude Code writes it, which is what the
// delta reader has to pick prose out of.
func turn(text string) string {
	return `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"` + text + `"}]}}` + "\n"
}

func prompt(text string) string {
	return `{"type":"user","message":{"role":"user","content":"` + text + `"}}` + "\n"
}

// toolTraffic is the bulk of a real transcript and none of what a parent
// came to read, so it must not appear in a delta.
const toolTraffic = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}` + "\n" +
	`{"type":"user","isMeta":false,"message":{"role":"user","content":[{"type":"tool_result","content":"ok  0.5s"}]}}` + "\n"

func writeDeltaTranscript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "conv.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func appendDeltaTranscript(t *testing.T, path, body string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(body); err != nil {
		t.Fatal(err)
	}
}

// The whole point: a second read carries what was added and nothing else.
// Re-reading the tail would have returned the first turn again, which is the
// cost this is here to remove.
func TestSinceReturnsOnlyWhatWasAppendedAfterTheCursor(t *testing.T) {
	path := writeDeltaTranscript(t, prompt("build the parser")+turn("parser built")+toolTraffic)

	first, err := Since(path, 0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(first.Turns) != 1 || first.Turns[0] != "parser built" {
		t.Fatalf("first read turns = %q", first.Turns)
	}
	if len(first.Prompts) != 1 || first.Prompts[0] != "build the parser" {
		t.Fatalf("first read prompts = %q", first.Prompts)
	}
	if first.Rewound {
		t.Error("a first read has no cursor to be unfaithful to, so it is not rewound")
	}

	appendDeltaTranscript(t, path, toolTraffic+turn("tests pass"))
	second, err := Since(path, first.Next)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(second.Turns) != 1 || second.Turns[0] != "tests pass" {
		t.Fatalf("second read turns = %q, want only the appended one", second.Turns)
	}
	if len(second.Prompts) != 0 {
		t.Errorf("second read replayed prompts: %q", second.Prompts)
	}
	if second.Next <= first.Next {
		t.Errorf("cursor did not advance: %d then %d", first.Next, second.Next)
	}
	if second.LastTurn() != "tests pass" {
		t.Errorf("LastTurn() = %q", second.LastTurn())
	}
}

// A child that has done nothing since the last look is the common case that
// a whole-pane read charges full price for.
func TestSinceIsEmptyWhenNothingWasAppended(t *testing.T) {
	path := writeDeltaTranscript(t, turn("still working"))
	first, err := Since(path, 0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	second, err := Since(path, first.Next)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if !second.Empty() {
		t.Fatalf("an unchanged transcript produced %+v", second)
	}
	if second.Next != first.Next {
		t.Errorf("cursor moved on an unchanged file: %d then %d", first.Next, second.Next)
	}
}

// A cursor past the end of the file names a transcript this is not. Saying
// so beats returning the wrong bytes as though they were the right ones.
func TestSinceRewindsRatherThanTrustAnOffsetPastTheEnd(t *testing.T) {
	path := writeDeltaTranscript(t, turn("only turn"))
	delta, err := Since(path, 1<<20)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if !delta.Rewound {
		t.Error("an offset past the end should report that the window was moved")
	}
	if delta.LastTurn() != "only turn" {
		t.Errorf("turns = %q, want the file read from the start", delta.Turns)
	}
}

// A caller away for a long time pays for a bounded window, not for the file.
func TestSinceBoundsOneReadAndSaysItMovedTheWindow(t *testing.T) {
	var body strings.Builder
	body.WriteString(turn("the opening turn nobody will see"))
	for body.Len() < 2*deltaCap {
		body.WriteString(toolTraffic)
	}
	body.WriteString(turn("the closing turn"))
	path := writeDeltaTranscript(t, body.String())

	delta, err := Since(path, 0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if !delta.Rewound {
		t.Error("a gap larger than one read should say the window begins later")
	}
	if delta.LastTurn() != "the closing turn" {
		t.Errorf("LastTurn() = %q, want the end kept", delta.LastTurn())
	}
	for _, got := range delta.Turns {
		if got == "the opening turn nobody will see" {
			t.Fatal("the window kept the start rather than the end")
		}
	}
}

// A window that did not begin at byte zero cuts its first line in half, and
// half a JSON record must not be read as a turn.
func TestSinceDropsTheRecordItsWindowCutInHalf(t *testing.T) {
	body := turn("first") + turn("second")
	path := writeDeltaTranscript(t, body)
	delta, err := Since(path, int64(len(turn("first"))/2))
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(delta.Turns) != 1 || delta.Turns[0] != "second" {
		t.Fatalf("turns = %q, want only the whole record", delta.Turns)
	}
}

func TestTranscriptForFindsAConversationWhoseDirectoryMovedUnderIt(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "projects", "-some-other-checkout", "conv-9.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(turn("hello")), 0o644); err != nil {
		t.Fatal(err)
	}
	// The cwd the caller knows about is not the one Claude Code mangled the
	// directory from, which is the ordinary case for a resumed session.
	if got := TranscriptFor(home, "conv-9", "/home/dev/repos/elsewhere"); got != path {
		t.Fatalf("TranscriptFor = %q, want %q", got, path)
	}
	if got := TranscriptFor(home, "conv-absent", ""); got != "" {
		t.Errorf("TranscriptFor for a conversation with no file = %q", got)
	}
}

// A dialog answered in the pane shows up in the transcript only as a tool
// result, and a declined call as one carrying the rejection, so a reader
// waiting on an answer has to be able to tell the two apart.
func TestSinceReportsToolResultsAndWhichWereRejected(t *testing.T) {
	path := writeDeltaTranscript(t, prompt("start"))
	start, err := Since(path, 0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	appendDeltaTranscript(t, path,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"ran","content":"ok"}]}}`+"\n"+
			`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"declined","is_error":true,"content":"The user doesn't want to proceed with this tool use. The tool use was rejected."}]}}`+"\n"+
			`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"failed","is_error":true,"content":[{"type":"text","text":"exit status 1"}]}]}}`+"\n")
	delta, err := Since(path, start.Next)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	want := []Result{{ToolUseID: "ran"}, {ToolUseID: "declined", Rejected: true}, {ToolUseID: "failed"}}
	if len(delta.Results) != len(want) {
		t.Fatalf("results = %+v, want %+v", delta.Results, want)
	}
	for i := range want {
		if delta.Results[i] != want[i] {
			t.Fatalf("result %d = %+v, want %+v", i, delta.Results[i], want[i])
		}
	}
	if len(delta.Prompts) != 0 || !delta.Empty() {
		t.Fatalf("tool results read as prose: %+v", delta)
	}
}

func TestSinceRetainsIncompleteRecordsAcrossPolls(t *testing.T) {
	for _, prefix := range []string{"", turn("earlier")} {
		for _, record := range []string{
			prompt("continue"),
			`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"allowed","content":"ok"}]}}` + "\n",
		} {
			for split := 1; split < len(record); split++ {
				path := writeDeltaTranscript(t, prefix+record[:split])
				cursor := int64(len(prefix))
				for poll := 0; poll < 2; poll++ {
					delta, err := Since(path, cursor)
					if err != nil || delta.Next != cursor || !delta.Empty() || len(delta.Results) != 0 {
						t.Fatalf("split %d poll %d: delta=%+v err=%v", split, poll, delta, err)
					}
				}
				appendDeltaTranscript(t, path, record[split:])
				delta, err := Since(path, cursor)
				if err != nil || len(delta.Prompts)+len(delta.Results) != 1 || delta.Next != int64(len(prefix)+len(record)) {
					t.Fatalf("completed split %d: delta=%+v err=%v", split, delta, err)
				}
				again, err := Since(path, delta.Next)
				if err != nil || !again.Empty() || len(again.Results) != 0 {
					t.Fatalf("replayed split %d: delta=%+v err=%v", split, again, err)
				}
			}
		}
	}
}

func TestSinceStopsAfterCompleteRecordsBeforeAnIncompleteRecord(t *testing.T) {
	complete, pending := prompt("first"), turn("second")
	path := writeDeltaTranscript(t, complete+pending[:len(pending)/2])
	delta, err := Since(path, 0)
	if err != nil || delta.Next != int64(len(complete)) || len(delta.Prompts) != 1 || len(delta.Turns) != 0 {
		t.Fatalf("delta=%+v err=%v", delta, err)
	}
	appendDeltaTranscript(t, path, pending[len(pending)/2:])
	delta, err = Since(path, delta.Next)
	if err != nil || delta.LastTurn() != "second" || len(delta.Prompts) != 0 {
		t.Fatalf("completed delta=%+v err=%v", delta, err)
	}
}
