package search

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func claudeLine(t *testing.T, kind string, content any) []byte {
	t.Helper()
	line, err := json.Marshal(map[string]any{
		"parentUuid": "p", "type": kind, "uuid": "u",
		"message": map[string]any{"role": kind, "content": content},
	})
	if err != nil {
		t.Fatal(err)
	}
	return line
}

func TestClaudeProseAndToolsAreIndexed(t *testing.T) {
	lim := DefaultLimits
	got := LineText(ToolClaude, claudeLine(t, "user", "fix the flaky mitm test"), lim)
	if got != "fix the flaky mitm test" {
		t.Fatalf("user prose: %q", got)
	}

	got = LineText(ToolClaude, claudeLine(t, "assistant", []any{
		map[string]any{"type": "thinking", "thinking": "secret plan"},
		map[string]any{"type": "text", "text": "Running the suite."},
		map[string]any{"type": "tool_use", "name": "Bash", "input": map[string]any{
			"command": "go test ./internal/mitm", "timeout": 120000, "description": "Run mitm tests",
		}},
	}), lim)
	for _, want := range []string{"Running the suite.", "Bash", "go test ./internal/mitm", "Run mitm tests"} {
		if !strings.Contains(got, want) {
			t.Errorf("assistant line lacks %q: %q", want, got)
		}
	}
	if strings.Contains(got, "secret plan") || strings.Contains(got, "120000") {
		t.Errorf("assistant line kept thinking or a number: %q", got)
	}

	got = LineText(ToolClaude, claudeLine(t, "user", []any{
		map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": "ok host=db-stage-3.example.test"},
	}), lim)
	if got != "ok host=db-stage-3.example.test" {
		t.Fatalf("string result: %q", got)
	}
	got = LineText(ToolClaude, claudeLine(t, "user", []any{
		map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": []any{
			map[string]any{"type": "text", "text": "first"}, map[string]any{"type": "image"}, map[string]any{"type": "text", "text": "second"},
		}},
	}), lim)
	if got != "first\nsecond" {
		t.Fatalf("block result: %q", got)
	}
}

// Every reader tags what it reads with who wrote it, and a user turn the
// harness wrote on the operator's behalf is not the operator's.
func TestSegmentsCarryTheirKind(t *testing.T) {
	lim := DefaultLimits
	kinds := func(tool string, line string) []Kind {
		var out []Kind
		for _, seg := range LineSegments(tool, []byte(line), lim) {
			out = append(out, seg.Kind)
		}
		return out
	}
	for line, want := range map[string][]Kind{
		string(claudeLine(t, "user", "typed by hand")):                                                                                          {KindUser},
		string(claudeLine(t, "user", "<system-reminder>injected</system-reminder>")):                                                            {KindSystem},
		string(claudeLine(t, "user", "<task-notification>done</task-notification>")):                                                            {KindSystem},
		string(claudeLine(t, "assistant", []any{map[string]any{"type": "text", "text": "said"}})):                                               {KindAssistant},
		string(claudeLine(t, "assistant", []any{map[string]any{"type": "tool_use", "name": "Bash", "input": map[string]any{"command": "ls"}}})): {KindInput},
		string(claudeLine(t, "user", []any{map[string]any{"type": "tool_result", "tool_use_id": "t", "content": "out"}})):                       {KindResult},
	} {
		if got := kinds(ToolClaude, line); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("claude %s -> %v want %v", line, got, want)
		}
	}
	for line, want := range map[string][]Kind{
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"typed"}]}}`:      {KindUser},
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"said"}]}}`: {KindAssistant},
		`{"type":"response_item","payload":{"type":"function_call","name":"shell","arguments":"{}"}}`:                               {KindInput},
		`{"type":"response_item","payload":{"type":"function_call_output","output":"printed"}}`:                                     {KindResult},
	} {
		if got := kinds(ToolCodex, line); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("codex %s -> %v want %v", line, got, want)
		}
	}
	oc := func(data string) []Kind {
		var out []Kind
		for _, seg := range opencodeMessageSegments(data, lim) {
			out = append(out, seg.Kind)
		}
		return out
	}
	if got := oc(`{"type":"user","text":"typed"}`); fmt.Sprint(got) != fmt.Sprint([]Kind{KindUser}) {
		t.Errorf("opencode user text -> %v", got)
	}
	if got := oc(`{"type":"assistant","content":[{"type":"text","text":"said"}]}`); fmt.Sprint(got) != fmt.Sprint([]Kind{KindAssistant}) {
		t.Errorf("opencode assistant text -> %v", got)
	}
	if got := oc(`{"type":"assistant","content":[{"type":"tool","name":"bash","state":{"input":{"command":"ls"},"content":[{"type":"text","text":"out"}]}}]}`); fmt.Sprint(got) != fmt.Sprint([]Kind{KindInput, KindResult}) {
		t.Errorf("opencode tool -> %v", got)
	}
	if KindUser.Weight() <= KindAssistant.Weight() || KindAssistant.Weight() <= KindInput.Weight() ||
		KindInput.Weight() <= KindResult.Weight() || KindResult.Weight() <= KindSystem.Weight() {
		t.Fatal("weights must fall from typed to said to ran to printed to injected")
	}
}

func TestClaudeNonTurnsAreSkipped(t *testing.T) {
	for _, line := range []string{
		`{"type":"progress","data":{"text":"hello"}}`,
		`{"type":"summary","summary":"a title","leafUuid":"x"}`,
		`{"type":"file-history-snapshot","messageId":"m","snapshot":{}}`,
		`{"type":"user","message":{"role":"system","content":"injected"}}`,
		`not json at all`,
	} {
		if got := LineText(ToolClaude, []byte(line), DefaultLimits); got != "" {
			t.Errorf("%s -> %q, want nothing", line, got)
		}
	}
}

func TestResultCapAndHugeLineFastPath(t *testing.T) {
	lim := Limits{Text: 64, Input: 32, Result: 100}
	body := strings.Repeat("x", 90) + `quote " backslash \ tab ` + "\t" + strings.Repeat("y", 400000)
	line := claudeLine(t, "user", []any{map[string]any{"type": "tool_result", "tool_use_id": "t", "content": body}})
	if len(line) <= hugeLine {
		t.Fatalf("fixture is not huge: %d bytes", len(line))
	}
	fast, ok := hugeStringHead(line, toolResultMark, contentMark, lim.Result)
	if !ok {
		t.Fatal("fast path refused a plain tool_result line")
	}
	if len(fast) > lim.Result || !strings.HasPrefix(body, fast) || fast == "" {
		t.Fatalf("fast prefix %q is not a capped prefix of the result", fast)
	}
	full := joinSegments(contentSegments(json.RawMessage(mustField(t, line, "message", "content")), KindUser, lim))
	if !strings.HasPrefix(full, fast) {
		t.Fatalf("fast %q diverges from full parse %q", fast, full)
	}
	// Block-shaped results take the same path.
	line = claudeLine(t, "user", []any{map[string]any{"type": "tool_result", "tool_use_id": "t",
		"content": []any{map[string]any{"type": "text", "text": body}}}})
	if fast, ok = hugeStringHead(line, toolResultMark, contentMark, lim.Result); !ok || !strings.HasPrefix(body, fast) || fast == "" {
		t.Fatalf("block fast path: ok=%v %q", ok, fast)
	}
	// Whatever the fast path cannot read falls back rather than answering.
	if _, ok = hugeStringHead([]byte(`{"type":"user","message":{"content":"plain"}}`), toolResultMark, contentMark, lim.Result); ok {
		t.Fatal("fast path answered a line with no tool_result")
	}
}

func joinSegments(segments []Segment) string {
	var sb strings.Builder
	for _, seg := range segments {
		appendPart(&sb, seg.Text)
	}
	return sb.String()
}

func mustField(t *testing.T, line []byte, path ...string) []byte {
	t.Helper()
	var node map[string]json.RawMessage
	raw := json.RawMessage(line)
	for _, key := range path {
		if err := json.Unmarshal(raw, &node); err != nil {
			t.Fatal(err)
		}
		raw = node[key]
	}
	return raw
}

func TestCapBytesKeepsRunesWhole(t *testing.T) {
	s := "héllo wörld"
	for n := 0; n <= len(s); n++ {
		got := capBytes(s, n)
		if len(got) > n || !strings.HasPrefix(s, got) {
			t.Fatalf("cap %d: %q", n, got)
		}
		for _, r := range got {
			if r == '�' {
				t.Fatalf("cap %d split a rune: %q", n, got)
			}
		}
	}
}

func TestCodexRolloutRows(t *testing.T) {
	lim := DefaultLimits
	cases := map[string]string{
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"deploy the collector"}]}}`:                               "deploy the collector",
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Done."}]}}`:                                        "Done.",
		`{"type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"<skills_instructions>"}]}}`:                         "",
		`{"type":"response_item","payload":{"type":"custom_tool_call","name":"exec","input":"tools.exec_command({\"cmd\":\"echo hi\"})"}}`:                                  "exec\ntools.exec_command({\"cmd\":\"echo hi\"})",
		`{"type":"response_item","payload":{"type":"function_call","name":"shell","arguments":"{\"command\":[\"ls\"]}"}}`:                                                   "shell\n{\"command\":[\"ls\"]}",
		`{"type":"response_item","payload":{"type":"custom_tool_call_output","output":[{"type":"input_text","text":"Script completed"},{"type":"input_text","text":"x"}]}}`: "Script completed\nx",
		`{"type":"response_item","payload":{"type":"function_call_output","output":"exit 0\nhello"}}`:                                                                       "exit 0\nhello",
		`{"type":"event_msg","payload":{"type":"user_message","message":"deploy the collector"}}`:                                                                           "",
		`{"type":"response_item","payload":{"type":"reasoning","summary":[]}}`:                                                                                              "",
	}
	for line, want := range cases {
		got := LineText(ToolCodex, []byte(line), lim)
		if got != want {
			t.Errorf("%s\n got %q\nwant %q", line, got, want)
		}
	}
}

// A huge Codex call output takes the same prefix fast path a huge Claude
// tool_result does, and agrees with the full parse.
func TestCodexHugeOutputFastPath(t *testing.T) {
	lim := Limits{Text: 64, Input: 32, Result: 100}
	body := strings.Repeat("o", 90) + `quote " then ` + strings.Repeat("p", 400000)
	line, _ := json.Marshal(map[string]any{"type": "response_item", "payload": map[string]any{
		"type": "function_call_output", "call_id": "c", "output": body,
	}})
	if len(line) <= hugeLine {
		t.Fatalf("fixture is not huge: %d bytes", len(line))
	}
	fast, ok := hugeStringHead(line, codexOutputMark, outputMark, lim.Result)
	if !ok || fast == "" || len(fast) > lim.Result || !strings.HasPrefix(body, fast) {
		t.Fatalf("codex fast path: ok=%v %q", ok, fast)
	}
	if got := LineText(ToolCodex, line, lim); got != fast {
		t.Fatalf("LineText took a different path: %q vs %q", got, fast)
	}
}

func TestOpenCodeMessageKinds(t *testing.T) {
	lim := DefaultLimits
	for data, want := range map[string]string{
		`{"type":"user","text":"deploy the portal"}`: "deploy the portal",
		`{"type":"assistant","content":[{"type":"tool","name":"bash","state":{"input":{"command":"kubectl rollout restart"},"content":[{"type":"text","text":"restarted"}]}}]}`: "bash\nkubectl rollout restart\nrestarted",
		`{"type":"assistant","content":[{"type":"reasoning","text":"private plan"}]}`:                                                                                           "",
		`{"type":"step-finish","reason":"stop"}`: "",
	} {
		var sb strings.Builder
		for _, seg := range opencodeMessageSegments(data, lim) {
			appendPart(&sb, seg.Text)
		}
		if got := sb.String(); got != want {
			t.Errorf("%s\n got %q\nwant %q", data, got, want)
		}
	}
}

func TestGenericReaderFindsConversationalStrings(t *testing.T) {
	line := `{"sessionId":"s","messages":[{"role":"user","content":"where is the config"},{"role":"model","parts":[{"text":"in etc"}]}],"meta":{"tokens":42}}`
	got := LineText("gemini", []byte(line), DefaultLimits)
	for _, want := range []string{"where is the config", "in etc"} {
		if !strings.Contains(got, want) {
			t.Errorf("generic lacks %q: %q", want, got)
		}
	}
	if strings.Contains(got, "42") || strings.Contains(got, "user") {
		t.Errorf("generic kept metadata: %q", got)
	}
}
