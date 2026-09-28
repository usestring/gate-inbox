// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/usestring/gate-inbox/internal/extension/all"
	"github.com/usestring/gate-inbox/internal/mcpserver"
	"github.com/usestring/gate-inbox/internal/sessioncmd"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// The rest of this suite drives the subcommands against a fake layer, and
// the MCP suite does the same on its side, so a front that forwarded the
// wrong caller, dropped an argument or grew a sentence of its own would
// pass both. These tests run the two fronts over one real sessioncmd
// layer and one store. They stay on the shared task list, which is the
// part of that layer no tmux command touches.
type parityWorkspace struct {
	configDir string
	tasks     taskCommands
	files     fileCommands
	lead      store.Session
	worker    store.Session
}

func newParityWorkspace(t *testing.T) *parityWorkspace {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	configDir := tmuxtest.ScratchDir(t)
	st, err := store.Open(filepath.Join(configDir, "state.db"))
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	defer st.Close()
	commands := sessioncmd.NewSessions(configDir, sessioncmd.CLIVocabulary())
	workspace := &parityWorkspace{
		configDir: configDir,
		tasks:     commands,
		files:     commands,
		lead:      store.Session{ID: uuid.NewString()[:8], Name: "lead-agent", Tool: "claude", Cwd: configDir, Status: status.Idle},
		worker:    store.Session{ID: uuid.NewString()[:8], Name: "worker-agent", Tool: "claude", Cwd: configDir, Status: status.Idle},
	}
	for _, sess := range []store.Session{workspace.lead, workspace.worker} {
		if err := st.CreateSession(sess); err != nil {
			t.Fatalf("create session row: %v", err)
		}
	}
	return workspace
}

func (w *parityWorkspace) mcpText(t *testing.T, sessionID, tool string, args map[string]any) (string, bool) {
	t.Helper()
	result := w.mcpCall(t, sessionID, tool, args)
	var text strings.Builder
	for _, content := range result.Content {
		if written, ok := content.(*mcp.TextContent); ok {
			text.WriteString(written.Text)
		}
	}
	return text.String(), result.IsError
}

func (w *parityWorkspace) mcpSession(t *testing.T, sessionID string) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := mcpserver.NewServer(w.configDir, sessionID, "test", all.Extensions()).Connect(context.Background(), serverTransport, nil); err != nil {
		t.Fatalf("serve: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "parity-client", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func (w *parityWorkspace) mcpCall(t *testing.T, sessionID, tool string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	session := w.mcpSession(t, sessionID)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", tool, err)
	}
	return result
}

func cliText(t *testing.T, run func(*bytes.Buffer) error) string {
	t.Helper()
	out := &bytes.Buffer{}
	if err := run(out); err != nil {
		t.Fatalf("subcommand: %v", err)
	}
	return out.String()
}

// A task created from a shell reaches an agent holding only MCP tools, and
// what that agent does with it comes back to the shell.
func TestTheTwoFrontsShareOneTaskList(t *testing.T) {
	w := newParityWorkspace(t)

	created := cliText(t, func(out *bytes.Buffer) error {
		return runTaskCreate(out, w.tasks, []string{"wire the cli", "--body", "five files"}, w.lead.ID)
	})
	id := taskID(t, created)

	if text, isError := w.mcpText(t, w.worker.ID, "task", map[string]any{"action": "claim", "task_id": id}); isError ||
		!strings.Contains(text, "claimed wire the cli ("+id+") [in_progress]") {
		t.Fatalf("claim = %q, isError=%v", text, isError)
	}
	listed := cliText(t, func(out *bytes.Buffer) error {
		return runTaskList(out, w.tasks, nil, w.lead.ID)
	})
	if !strings.Contains(listed, "held by worker-agent") || strings.Contains(listed, "; yours") {
		t.Fatalf("the shell front does not see the MCP claim: %q", listed)
	}
	// The claim holds against the other front too, and says who has it.
	err := runTaskClaim(&bytes.Buffer{}, w.tasks, []string{id}, w.lead.ID)
	if err == nil || !strings.Contains(err.Error(), "already claimed by worker-agent") {
		t.Fatalf("claiming across fronts = %v", err)
	}

	finished := cliText(t, func(out *bytes.Buffer) error {
		return runTaskFinish(out, w.tasks, []string{id}, w.worker.ID)
	})
	if !strings.Contains(finished, "[done]") {
		t.Fatalf("finish = %q", finished)
	}
	if text, isError := w.mcpText(t, w.worker.ID, "task", map[string]any{"action": "list", "include_done": true}); isError ||
		!strings.Contains(text, "wire the cli ("+id+") [done]") {
		t.Fatalf("the MCP front does not see the shell's finish: %q, isError=%v", text, isError)
	}
}

// Same caller, same operation, same words: the sentences come from
// sessioncmd, and a front writing its own would drift from the other.
func TestTheTwoFrontsAnswerOneOperationIdentically(t *testing.T) {
	w := newParityWorkspace(t)
	created := cliText(t, func(out *bytes.Buffer) error {
		return runTaskCreate(out, w.tasks, []string{"wire the cli"}, w.lead.ID)
	})
	id := taskID(t, created)
	if _, err := w.tasks.ClaimTask(w.worker.ID, id); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}

	fromCLI := cliText(t, func(out *bytes.Buffer) error {
		return runTaskList(out, w.tasks, nil, w.lead.ID)
	})
	fromMCP, isError := w.mcpText(t, w.lead.ID, "task", map[string]any{"action": "list"})
	if isError {
		t.Fatalf("task list errored: %q", fromMCP)
	}
	if strings.TrimSpace(fromCLI) != strings.TrimSpace(fromMCP) {
		t.Fatalf("the fronts describe one list differently:\ncli: %q\nmcp: %q", fromCLI, fromMCP)
	}

	cliRecord := cliText(t, func(out *bytes.Buffer) error {
		return runTaskList(out, w.tasks, []string{"--json"}, w.lead.ID)
	})
	cliTasks := decodeTaskList(t, []byte(cliRecord))
	mcpTasks := w.mcpTaskList(t, w.lead.ID, map[string]any{"action": "list"})
	if !reflect.DeepEqual(cliTasks, mcpTasks) {
		t.Fatalf("the fronts return different records:\ncli: %+v\nmcp: %+v", cliTasks, mcpTasks)
	}

	// A refusal is what an agent reads most often, so it has to match too.
	cliRefusal := runTaskClaim(&bytes.Buffer{}, w.tasks, []string{id}, w.lead.ID)
	if cliRefusal == nil {
		t.Fatal("claiming a held task should be refused")
	}
	mcpRefusal, isError := w.mcpText(t, w.lead.ID, "task", map[string]any{"action": "claim", "task_id": id})
	if !isError {
		t.Fatalf("claiming a held task = %q, want a tool error", mcpRefusal)
	}
	if cliRefusal.Error() != mcpRefusal {
		t.Fatalf("the fronts refuse differently:\ncli: %q\nmcp: %q", cliRefusal, mcpRefusal)
	}
}

// A bound enforced on one front is a bound the other front does not have.
// Both take the lease duration in their own units, and an out-of-range one
// has to come back refused in the same words whichever front asked.
func TestTheTwoFrontsRefuseTheSameOutOfRangeTTL(t *testing.T) {
	w := newParityWorkspace(t)
	for _, out := range []struct {
		name    string
		ttl     string
		minutes int
	}{
		{"beyond the maximum", "9h", 540},
		{"negative", "-5m", -5},
	} {
		t.Run(out.name, func(t *testing.T) {
			fromCLI := runReserve(&bytes.Buffer{}, w.files, []string{"--ttl", out.ttl, "internal/store/store.go"}, w.lead.ID)
			if fromCLI == nil {
				t.Fatal("the shell front granted a lease outside the bound")
			}
			fromMCP, isError := w.mcpText(t, w.lead.ID, "reserve_files", map[string]any{
				"paths": []string{"internal/store/store.go"}, "ttl_minutes": out.minutes,
			})
			if !isError {
				t.Fatalf("the MCP front granted a lease outside the bound: %q", fromMCP)
			}
			if fromCLI.Error() != fromMCP {
				t.Fatalf("the fronts refuse differently:\ncli: %q\nmcp: %q", fromCLI, fromMCP)
			}
			if !strings.Contains(fromMCP, "outside 0 to "+sessioncmd.MaxReservationTTL.String()) {
				t.Fatalf("the refusal does not name the bound: %q", fromMCP)
			}
		})
	}
	// The bound refuses what is outside it and nothing else, on both fronts:
	// an extra check grown on one of them is the drift this file is here for.
	if err := runReserve(&bytes.Buffer{}, w.files, []string{"--ttl", "45m", "internal/store/store.go"}, w.lead.ID); err != nil {
		t.Fatalf("a ttl inside the bound was refused: %v", err)
	}
	granted, isError := w.mcpText(t, w.lead.ID, "reserve_files", map[string]any{
		"paths": []string{"internal/store/tasks.go"}, "ttl_minutes": 45,
	})
	if isError {
		t.Fatalf("the MCP front refused a ttl inside the bound: %q", granted)
	}
}

// An agent holds one front or the other: a session whose CLI carries no
// MCP client has the subcommands and nothing else. An error naming the
// words of the front the reader does not have sends it after a command it
// cannot run.
func TestEachFrontNamesTheCommandsItsCallerHas(t *testing.T) {
	w := newParityWorkspace(t)

	fromCLI := runTaskFinish(&bytes.Buffer{}, w.tasks, []string{"nosuch12"}, w.lead.ID)
	if fromCLI == nil {
		t.Fatal("finishing a task that does not exist should be refused")
	}
	// Taken from the vocabulary rather than spelled out, so the assertion
	// follows how the shell front reaches the manager instead of pinning
	// one spelling of it.
	if !strings.Contains(fromCLI.Error(), sessioncmd.CLIVocabulary().ListTasks) {
		t.Fatalf("the shell front does not name a subcommand: %q", fromCLI)
	}
	if strings.Contains(fromCLI.Error(), `task with action "list"`) {
		t.Fatalf("the shell front names an MCP tool its caller cannot call: %q", fromCLI)
	}

	fromMCP, isError := w.mcpText(t, w.lead.ID, "task", map[string]any{"action": "finish", "task_id": "nosuch12"})
	if !isError {
		t.Fatalf("finishing a missing task = %q, want a tool error", fromMCP)
	}
	if !strings.Contains(fromMCP, `task with action "list"`) || strings.Contains(fromMCP, "gate-inbox") {
		t.Fatalf("the MCP front does not speak in tools: %q", fromMCP)
	}
}

// Every spelling the shell front hands out has to be a command that
// actually dispatches, since the whole point is an agent running it.
func TestTheCLIVocabularyNamesRealSubcommands(t *testing.T) {
	table := Commands()
	verbs := map[string][]command{}
	for _, section := range sections() {
		for _, registered := range section.commands {
			verbs[registered.name] = registered.verbs
		}
	}
	words := reflect.ValueOf(sessioncmd.CLIVocabulary())
	for index := range words.NumField() {
		phrase := words.Field(index).String()
		field := words.Type().Field(index).Name
		parts := strings.Fields(phrase)
		// The first word invokes the manager -- through the path its launch
		// exported, with the bare name as the fallback -- so it is checked
		// for naming the manager rather than for being exactly the name.
		if len(parts) < 2 || !strings.Contains(parts[0], "gate-inbox") {
			t.Fatalf("%s is %q, which is not a command an agent can run", field, phrase)
		}
		if table[parts[1]] == nil {
			t.Fatalf("%s names %q, which is not a subcommand", field, parts[1])
		}
		if len(parts) > 2 && !strings.HasPrefix(parts[2], "-") && !slices.ContainsFunc(verbs[parts[1]], func(verb command) bool {
			return verb.name == parts[2]
		}) {
			t.Fatalf("%s names %q, which %s has no verb for", field, parts[2], parts[1])
		}
	}
}

// taskID reads the id back out of the sentence a create printed, which is
// the only place either front hands one to its caller.
func taskID(t *testing.T, created string) string {
	t.Helper()
	opened := strings.Index(created, "(")
	closed := strings.Index(created, ")")
	if opened < 0 || closed < opened {
		t.Fatalf("create printed no task id: %q", created)
	}
	return created[opened+1 : closed]
}

// An argument added to one front is an argument the other front's callers
// cannot reach. send_session grew a subject, so `send` has to grow the flag
// in the same change, or a shell can queue only messages nothing can
// supersede.
func TestBothFrontsTakeEverySendArgument(t *testing.T) {
	w := newParityWorkspace(t)
	listed, err := w.mcpSession(t, w.lead.ID).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	found := false
	for _, tool := range listed.Tools {
		if tool.Name != "send_session" {
			continue
		}
		found = true
		encoded, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("marshal send_session schema: %v", err)
		}
		if err := json.Unmarshal(encoded, &schema); err != nil {
			t.Fatalf("send_session schema is not an object schema: %v (%s)", err, encoded)
		}
	}
	if !found {
		t.Fatal("send_session is not on the MCP front")
	}
	sessions := newSessions(w.configDir)
	for name := range schema.Properties {
		// session_id and message are the two operands; everything else an
		// agent can pass has to be a flag a shell can pass too. message_file
		// is the message operand again, read from disk so the text need not
		// travel inside the tool call; a shell reaches the same thing with
		// "$(cat file)", and no subcommand has a file twin.
		if name == "session_id" || name == "message" || name == "message_file" {
			continue
		}
		if !strings.Contains(usageSend, "--"+name) {
			t.Errorf("--%s is not in the send usage line: %q", name, usageSend)
		}
		// Run the real subcommand with the flag, at a session id nothing
		// holds: what comes back has to be about that id. A flag the set
		// does not define is read as a third operand -- cmdline.Interspersed
		// makes anything it has no flag for one, so the message would come
		// back as a usage error rather than as an unknown flag.
		flag := []string{"--" + name, "x"}
		if prop, _ := schema.Properties[name].(map[string]any); prop["type"] == "boolean" {
			flag = flag[:1]
		}
		err := runSend(&bytes.Buffer{}, sessions, append(flag, "nosuch12", "hello"), w.lead.ID)
		if err == nil {
			t.Fatal("sending to a session that does not exist should be refused")
		}
		if !strings.Contains(err.Error(), "nosuch12") {
			t.Errorf("send_session takes %q and `gate-inbox send` does not: %v", name, err)
		}
	}
}

// taskListRecord is the list both fronts hand a caller, decoded field by
// field so a front that dropped the count or the truncation flag differs.
type taskListRecord struct {
	Tasks     []sessioncmd.Task `json:"tasks"`
	Matched   int               `json:"matched"`
	Returned  int               `json:"returned"`
	Truncated bool              `json:"truncated"`
}

func decodeTaskList(t *testing.T, encoded []byte) taskListRecord {
	t.Helper()
	var list taskListRecord
	if err := json.Unmarshal(encoded, &list); err != nil {
		t.Fatalf("not a task list: %v (%s)", err, encoded)
	}
	return list
}

func (w *parityWorkspace) mcpTaskList(t *testing.T, sessionID string, args map[string]any) taskListRecord {
	t.Helper()
	result := w.mcpCall(t, sessionID, "task", args)
	if result.IsError {
		t.Fatalf("task list %v errored: %+v", args, result.Content)
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	return decodeTaskList(t, encoded)
}

// A list that has run for a while is mostly finished work, each row with
// its full instruction. Both fronts have to leave that out by default, take
// the same arguments to ask for it back, and say in the same words when a
// limit cut the answer.
func TestTheTwoFrontsNarrowTheTaskListAlike(t *testing.T) {
	w := newParityWorkspace(t)
	var ids []string
	for _, title := range []string{"add the column", "backfill it", "write the docs", "verify"} {
		created := cliText(t, func(out *bytes.Buffer) error {
			return runTaskCreate(out, w.tasks, []string{title, "--body", "the full instruction for " + title}, w.lead.ID)
		})
		ids = append(ids, taskID(t, created))
	}
	for _, id := range ids[:2] {
		if _, err := w.tasks.ClaimTask(w.lead.ID, id); err != nil {
			t.Fatalf("ClaimTask: %v", err)
		}
		if _, err := w.tasks.FinishTask(w.lead.ID, id); err != nil {
			t.Fatalf("FinishTask: %v", err)
		}
	}
	if _, err := w.tasks.ClaimTask(w.worker.ID, ids[2]); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}

	for _, ask := range []struct {
		name     string
		mcp      map[string]any
		cli      []string
		caller   string
		matched  int
		returned int
	}{
		{"the default read", map[string]any{}, nil, w.lead.ID, 2, 2},
		{"finished work too", map[string]any{"include_done": true}, []string{"--include-done"}, w.lead.ID, 4, 4},
		{"one state", map[string]any{"state": []string{"done"}}, []string{"--state", "done"}, w.lead.ID, 2, 2},
		{"its own claims", map[string]any{"mine": true}, []string{"--mine"}, w.worker.ID, 1, 1},
		{"a cut list", map[string]any{"include_done": true, "limit": 3}, []string{"--include-done", "--limit", "3"}, w.lead.ID, 4, 3},
		{"with bodies", map[string]any{"include_body": true}, []string{"--include-body"}, w.lead.ID, 2, 2},
	} {
		t.Run(ask.name, func(t *testing.T) {
			args := map[string]any{"action": "list"}
			for name, value := range ask.mcp {
				args[name] = value
			}
			fromMCP, isError := w.mcpText(t, ask.caller, "task", args)
			if isError {
				t.Fatalf("task list %v errored: %q", args, fromMCP)
			}
			fromCLI := cliText(t, func(out *bytes.Buffer) error {
				return runTaskList(out, w.tasks, ask.cli, ask.caller)
			})
			if strings.TrimSpace(fromCLI) != strings.TrimSpace(fromMCP) {
				t.Fatalf("the fronts describe one list differently:\ncli: %q\nmcp: %q", fromCLI, fromMCP)
			}
			record := decodeTaskList(t, []byte(cliText(t, func(out *bytes.Buffer) error {
				return runTaskList(out, w.tasks, append(ask.cli, "--json"), ask.caller)
			})))
			if structured := w.mcpTaskList(t, ask.caller, args); !reflect.DeepEqual(record, structured) {
				t.Fatalf("the fronts return different records:\ncli: %+v\nmcp: %+v", record, structured)
			}
			if record.Matched != ask.matched || record.Returned != ask.returned || record.Truncated != (ask.returned < ask.matched) {
				t.Fatalf("list = %d of %d, truncated %t; want %d of %d", record.Returned, record.Matched, record.Truncated, ask.returned, ask.matched)
			}
			_, askedForDone := ask.mcp["include_done"]
			_, askedForState := ask.mcp["state"]
			_, askedForBody := ask.mcp["include_body"]
			for _, task := range record.Tasks {
				if task.State == store.TaskDone && !askedForDone && !askedForState {
					t.Fatalf("finished work was listed without being asked for: %+v", task)
				}
				if (task.Body != "") != askedForBody {
					t.Fatalf("%s came back with body %q when include_body was %t", task.ID, task.Body, askedForBody)
				}
			}
			if ask.returned < ask.matched && !strings.Contains(fromMCP, fmt.Sprintf("(%d of %d matching tasks", ask.returned, ask.matched)) {
				t.Fatalf("a cut list does not say so: %q", fromMCP)
			}
		})
	}
}

// An argument the task tool takes for list is one a shell has to be able
// to pass as well; the list arguments are the ones whose description opens
// with "list:".
func TestBothFrontsTakeEveryTaskListArgument(t *testing.T) {
	w := newParityWorkspace(t)
	listed, err := w.mcpSession(t, w.lead.ID).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	var schema struct {
		Properties map[string]struct {
			Type        any    `json:"type"`
			Description string `json:"description"`
		} `json:"properties"`
	}
	for _, tool := range listed.Tools {
		if tool.Name != "task" {
			continue
		}
		encoded, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("marshal task schema: %v", err)
		}
		if err := json.Unmarshal(encoded, &schema); err != nil {
			t.Fatalf("task schema is not an object schema: %v (%s)", err, encoded)
		}
	}
	checked := 0
	for name, prop := range schema.Properties {
		if !strings.HasPrefix(prop.Description, "list:") {
			continue
		}
		checked++
		flag := "--" + strings.ReplaceAll(name, "_", "-")
		if !strings.Contains(usageTaskList, flag) {
			t.Errorf("%s is not in the task list usage line: %q", flag, usageTaskList)
		}
		args := []string{flag}
		switch name {
		case "state":
			args = append(args, "pending")
		case "limit":
			args = append(args, "5")
		}
		if prop.Type != "boolean" && len(args) == 1 {
			t.Errorf("the test does not know a value for %s", name)
			continue
		}
		if err := runTaskList(&bytes.Buffer{}, w.tasks, args, w.lead.ID); err != nil {
			t.Errorf("the task tool takes %q for list and `task list` does not: %v", name, err)
		}
	}
	if checked == 0 {
		t.Fatal("the task tool offers no list arguments at all")
	}
}
