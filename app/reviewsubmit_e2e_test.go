package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/usestring/gate-inbox/internal/store"
)

// reviewQuestion is one question of the tall dialog, with the option the
// test answers it with.
type reviewQuestion struct {
	Header   string         `json:"header"`
	Question string         `json:"question"`
	Options  []reviewOption `json:"options"`
	Multi    bool           `json:"multiSelect"`
	pick     string
}

type reviewOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// tallQuestions are four questions long enough that Claude Code's "Review
// your answers" page, which lists each one whole with its answer, is taller
// than reviewPaneRows: the tab row and the heading scroll off the top.
func tallQuestions(round string) []reviewQuestion {
	long := func(subject string) string {
		return "Round " + round + ": " + subject + " Before you answer, keep in mind that this choice is recorded " +
			"in the rollout log, is read back by the next session that picks up this work, and cannot be changed " +
			"later without opening a new review, so please pick the option you would still stand behind next week."
	}
	return []reviewQuestion{
		{Header: "Region", Question: long("which region should the survey cover first?"), pick: "North",
			Options: []reviewOption{{"North", "Start in the north"}, {"South", "Start in the south"}}},
		{Header: "Cadence", Question: long("how often should the survey run once it starts?"), pick: "Weekly",
			Options: []reviewOption{{"Daily", "Every day"}, {"Weekly", "Once a week"}}},
		{Header: "Owner", Question: long("which team should own the survey results?"), pick: "Research",
			Options: []reviewOption{{"Research", "The research team"}, {"Platform", "The platform team"}}},
		{Header: "Format", Question: long("which format should the survey report use?"), pick: "Table",
			Options: []reviewOption{{"Table", "A plain table"}, {"Chart", "A chart per region"}}},
	}
}

// reviewPaneRows is the child's pane height: short enough that the review
// page of tallQuestions overflows it.
const reviewPaneRows = 22

// TestReviewSubmitE2E proves, against a real Claude Code child in a short
// pane, that a parent's answer_session submits a several-question dialog
// whose "Review your answers" page outgrows the pane:
//
//	a. answers for every question, relay false: Submit is pressed, the call
//	   returns without a timeout, and the child's transcript records exactly
//	   those answers;
//	b. the same with relay true, the parent's own transcript holding the same
//	   AskUserQuestion answered by its user with the same answers;
//	c. answers with submit false leave the child on the review page: read_session
//	   gives digest.next and no relay_ask, and answer_session "Submit answers"
//	   then submits it.
//
// The parent is this test speaking the board's MCP server as a seeded session
// row; the child is a board-launched claude. It costs a handful of haiku turns
// on ANTHROPIC_API_KEY, so it is opt-in, and it runs isolated the way
// TestAdoptedClaudeE2E does (scratch HOME, CLAUDE_CONFIG_DIR, GATE_INBOX_HOME
// and test-owned tmux servers):
//
//	GATE_INBOX_E2E_CLAUDE=1 [GATE_INBOX_E2E_CLAUDE_OUT=<dir>] \
//	  go test ./app -run TestReviewSubmitE2E -timeout 30m -v
func TestReviewSubmitE2E(t *testing.T) {
	apiKey := requireClaudeE2E(t)
	e := newAdoptedE2E(t, apiKey)
	e.title = "Tall review page submit e2e"
	e.startBoard()

	out, err := e.gi(operator, "spawn", "--tool", "claude", "--name", "asker", "--directory", e.workL, "--json")
	if err != nil {
		t.Fatalf("spawn: %v\n%s", err, out)
	}
	child := idOf(t, out)
	target := "gi_" + child
	e.waitComposer(e.agents, target)
	e.settle(child)
	if out, err := e.agents.run("resize-window", "-t", target, "-x", "110", "-y", strconv.Itoa(reviewPaneRows)); err != nil {
		t.Fatalf("resize the child's pane: %v\n%s", err, out)
	}
	size, _ := e.agents.run("display-message", "-p", "-t", target, "#{pane_width}x#{pane_height}")
	e.record("setup", "the child runs in a short pane", strings.TrimSpace(size) == "110x"+strconv.Itoa(reviewPaneRows),
		"pane size: "+size)

	parent := connectFixture(t, e.bin, append(e.env[:len(e.env):len(e.env)], "GATE_INBOX_SESSION_ID="+operator))

	// a. relay false, every answer, submitted.
	qs := e.askTall(target, child, "A")
	answered, isErr := callTool(t, parent, "answer_session", map[string]any{
		"session_id": child, "answers": answersFor(qs),
	})
	e.save("a-answer.txt", answered)
	got := e.awaitAnswers(qs)
	e.record("a", "relay false: answer_session submits the tall review page and the child gets exactly those answers",
		!isErr && !strings.Contains(answered, "never ticked") && got == "",
		fmt.Sprintf("answer_session error: %v\n%s\nchild transcript: %s", isErr, clip(answered, 900), orOK(got)))
	e.save("a-pane.txt", e.agents.history(target))
	e.settle(child)

	// b. relay true, the parent's transcript holding its user's answers.
	qs = e.askTall(target, child, "B")
	e.parentRelayed(qs)
	answered, isErr = callTool(t, parent, "answer_session", map[string]any{
		"session_id": child, "answers": answersFor(qs), "relay": true,
	})
	e.save("b-answer.txt", answered)
	got = e.awaitAnswers(qs)
	e.record("b", "relay true: answer_session submits the tall review page and the child gets exactly those answers",
		!isErr && !strings.Contains(answered, "never ticked") && got == "",
		fmt.Sprintf("answer_session error: %v\n%s\nchild transcript: %s", isErr, clip(answered, 900), orOK(got)))
	e.save("b-pane.txt", e.agents.history(target))
	e.settle(child)

	// c. submit false leaves the child on the review page.
	qs = e.askTall(target, child, "C")
	answered, isErr = callTool(t, parent, "answer_session", map[string]any{
		"session_id": child, "answers": answersFor(qs), "submit": false,
	})
	e.save("c-answer.txt", answered)
	screen := e.agents.screen(target)
	e.save("c-review-screen.txt", screen)
	read, readErr := callTool(t, parent, "read_session", map[string]any{"session_id": child})
	e.save("c-read.json", read)
	var digest struct {
		Digest struct {
			Next     string          `json:"next"`
			RelayAsk json.RawMessage `json:"relay_ask"`
		} `json:"digest"`
	}
	_ = json.Unmarshal([]byte(read), &digest)
	clipped := strings.Contains(screen, "Submit answers") && !strings.Contains(screen, "Review your answers")
	e.record("c", "the review page outgrows the pane: its heading has scrolled off",
		clipped, lastLines(screen, reviewPaneRows))
	e.record("c", "submit false: read_session gives digest.next and no relay_ask",
		!isErr && !readErr && digest.Digest.Next != "" && len(digest.Digest.RelayAsk) == 0,
		fmt.Sprintf("answer_session error: %v\n%s\nnext: %q\nrelay_ask: %s",
			isErr, clip(answered, 500), digest.Digest.Next, clip(string(digest.Digest.RelayAsk), 500)))
	answered, isErr = callTool(t, parent, "answer_session", map[string]any{
		"session_id": child, "answer": "Submit answers",
	})
	e.save("c-submit.txt", answered)
	got = e.awaitAnswers(qs)
	e.record("c", `answer_session "Submit answers" submits the complete review page`,
		!isErr && got == "", fmt.Sprintf("answer_session error: %v\n%s\nchild transcript: %s",
			isErr, clip(answered, 900), orOK(got)))
	e.save("c-pane.txt", e.agents.history(target))
}

// requireClaudeE2E skips unless a real Claude Code run was asked for and can
// happen, and returns the API key it runs on.
func requireClaudeE2E(t *testing.T) string {
	t.Helper()
	if os.Getenv("GATE_INBOX_E2E_CLAUDE") == "" {
		t.Skip("GATE_INBOX_E2E_CLAUDE unset")
	}
	for _, bin := range []string{"claude", "tmux"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip(bin + " not installed")
		}
	}
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if len(apiKey) < 20 {
		t.Skip("ANTHROPIC_API_KEY unset")
	}
	return apiKey
}

// askTall has the child put tallQuestions to its user in one
// AskUserQuestion call, and waits for the dialog to be up.
func (e *adoptedE2E) askTall(target, child, round string) []reviewQuestion {
	e.t.Helper()
	qs := tallQuestions(round)
	input, err := json.Marshal(map[string]any{"questions": qs})
	if err != nil {
		e.t.Fatal(err)
	}
	askFile := filepath.Join(e.giHome, "hooks", child+".ask.json")
	_ = os.Remove(askFile)
	e.agents.typeLine(e.t, target, "Call the AskUserQuestion tool exactly once, with exactly this input and no "+
		"change to any text: "+string(input)+" After I answer, reply with ANSWERED-"+round+" and nothing else.")
	ok := false
	waitUntilQuiet(150*time.Second, func() bool {
		ask, _ := os.ReadFile(askFile)
		ok = strings.Contains(string(ask), "Round "+round+":") && e.row(child).Status == "waiting"
		return ok
	})
	e.save("ask-"+round+"-screen.txt", e.agents.screen(target))
	if !ok {
		e.save("ask-"+round+"-history.txt", e.agents.history(target))
		e.t.Fatalf("round %s: the child never put the dialog up; status %q\n%s", round, e.row(child).Status,
			lastLines(e.agents.screen(target), reviewPaneRows))
	}
	return qs
}

func answersFor(qs []reviewQuestion) []map[string]any {
	out := make([]map[string]any, len(qs))
	for i, q := range qs {
		out[i] = map[string]any{"question": q.Question, "answer": q.pick}
	}
	return out
}

// awaitAnswers waits for the child's transcript to record an answer to qs and
// returns "" when it records exactly their picks, or what differs.
func (e *adoptedE2E) awaitAnswers(qs []reviewQuestion) string {
	want := map[string]string{}
	for _, q := range qs {
		want[q.Question] = q.pick
	}
	diff := "no answer recorded"
	waitUntilQuiet(90*time.Second, func() bool {
		recorded, found := lastAnswers(e.transcript(e.workL), qs[0].Question)
		if !found {
			return false
		}
		diff = ""
		for question, pick := range want {
			if recorded[question] != pick {
				diff += fmt.Sprintf("%q answered %q, want %q; ", clip(question, 40), recorded[question], pick)
			}
		}
		if len(recorded) != len(want) {
			diff += fmt.Sprintf("%d answers recorded, want %d", len(recorded), len(want))
		}
		return true
	})
	return diff
}

// lastAnswers is the answers map of the last AskUserQuestion result in text
// that answers question.
func lastAnswers(text, question string) (map[string]string, bool) {
	var found map[string]string
	for _, line := range transcriptLines(text, `"toolUseResult"`, `"answers"`) {
		var rec struct {
			ToolUseResult struct {
				Answers map[string]string `json:"answers"`
			} `json:"toolUseResult"`
		}
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		if _, ok := rec.ToolUseResult.Answers[question]; ok {
			found = rec.ToolUseResult.Answers
		}
	}
	return found, found != nil
}

// parentRelayed gives the operator row a conversation whose transcript holds
// qs put to its user in its own AskUserQuestion, answered with the picks:
// what a parent that relayed the child's dialog word for word has on record.
func (e *adoptedE2E) parentRelayed(qs []reviewQuestion) {
	e.t.Helper()
	conversation := fmt.Sprintf("e2e-parent-%d", time.Now().UnixNano())
	answers := map[string]string{}
	for _, q := range qs {
		answers[q.Question] = q.pick
	}
	now := time.Now().UTC()
	id := "toolu_e2e_parent_relay"
	lines := []map[string]any{
		{"type": "assistant", "timestamp": now.Format(time.RFC3339Nano),
			"message": map[string]any{"role": "assistant", "content": []any{map[string]any{
				"type": "tool_use", "id": id, "name": "AskUserQuestion", "input": map[string]any{"questions": qs}}}}},
		{"type": "user", "timestamp": now.Add(2 * time.Second).Format(time.RFC3339Nano),
			"message": map[string]any{"role": "user", "content": []any{map[string]any{
				"type": "tool_result", "tool_use_id": id, "content": "The user answered."}}},
			"toolUseResult": map[string]any{"answers": answers}},
	}
	var body strings.Builder
	for _, line := range lines {
		raw, err := json.Marshal(line)
		if err != nil {
			e.t.Fatal(err)
		}
		body.Write(raw)
		body.WriteByte('\n')
	}
	path := filepath.Join(e.claudeD, "projects", "e2e-parent", conversation+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body.String()), 0o644); err != nil {
		e.t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(e.giHome, "state.db"))
	if err != nil {
		e.t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetAgentSessionID(operator, conversation); err != nil {
		e.t.Fatal(err)
	}
	time.Sleep(3 * time.Second)
}

// callTool calls one of the board's MCP tools and returns its result as
// JSON (the structured content when there is one) and whether it is an
// error.
func callTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return "call failed: " + err.Error(), true
	}
	if res.StructuredContent != nil {
		if raw, err := json.Marshal(res.StructuredContent); err == nil && !res.IsError {
			return string(raw), false
		}
	}
	var text []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text = append(text, tc.Text)
		}
	}
	return strings.Join(text, "\n"), res.IsError
}

func orOK(diff string) string {
	if diff == "" {
		return "exactly the answers sent"
	}
	return diff
}
