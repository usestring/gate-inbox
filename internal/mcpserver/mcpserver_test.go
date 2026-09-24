// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/extension/all"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/sessioncmd"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

type fakeTerminalCommands struct {
	listed      []sessioncmd.Terminal
	created     sessioncmd.Terminal
	screen      sessioncmd.TerminalScreen
	createdOpts sessioncmd.CreateTerminalOptions
	sentID      string
	sentCommand string
	sentKeys    []string
	readID      string
	closedID    string
	err         error
}

func (f *fakeTerminalCommands) List(string) ([]sessioncmd.Terminal, error) {
	return f.listed, f.err
}

func (f *fakeTerminalCommands) Create(_ string, opts sessioncmd.CreateTerminalOptions) (sessioncmd.Terminal, error) {
	f.createdOpts = opts
	return f.created, f.err
}

// The fake decides the input kind the way the real layer does, since the
// front is only allowed to report what it was handed.
func (f *fakeTerminalCommands) Send(_ string, id, command string, keys []string) (sessioncmd.TerminalInput, error) {
	f.sentID = id
	f.sentCommand = command
	f.sentKeys = append([]string(nil), keys...)
	sent := "keys"
	if strings.TrimSpace(command) != "" {
		sent = "command"
	}
	return sessioncmd.TerminalInput{TerminalID: id, Sent: sent}, f.err
}

func (f *fakeTerminalCommands) Read(_ string, id string) (sessioncmd.TerminalScreen, error) {
	f.readID = id
	return f.screen, f.err
}

func (f *fakeTerminalCommands) Close(_ string, id string) error {
	f.closedID = id
	return f.err
}

type fakeSessionCommands struct {
	listed         []sessioncmd.Session
	listOpts       sessioncmd.ListOptions
	created        sessioncmd.Session
	screen         sessioncmd.SessionScreen
	groups         []sessioncmd.Group
	createdOpts    sessioncmd.CreateSessionOptions
	sentID         string
	sentMessage    string
	sentSubject    string
	sentInterrupt  bool
	readID         string
	readSince      string
	placedID       string
	placedUnder    string
	statusID       int64
	waitedID       string
	waitedIDs      []string
	waitedChildren bool
	waitedUntil    []string
	waitedTimeout  time.Duration
	tasks          []sessioncmd.Task
	taskTitle      string
	taskBody       string
	taskDeps       []string
	claimedTaskID  string
	settledTaskID  string
	reservedPaths  []string
	reservedMode   string
	reservedTTL    time.Duration
	releasedPaths  []string
	reservations   []sessioncmd.Reservation
	revivedID      string
	migratedID     string
	migratedOpts   sessioncmd.MigrateOptions
	killedID       string
	archivedID     string
	archived       bool
	groupPath      string
	groupDir       string
	err            error
	answeredID     string
	answeredWith   string
	answerSelected string

	// What switch_account forwarded. Kept apart from the run above so the
	// longest name here does not re-space every field beside it.
	switchedID      string
	switchedAccount string
}

func (f *fakeSessionCommands) List(_ string, opts sessioncmd.ListOptions) (sessioncmd.SessionList, error) {
	f.listOpts = opts
	return sessioncmd.SessionList{Sessions: f.listed, Matched: len(f.listed), Returned: len(f.listed)}, f.err
}

func (f *fakeSessionCommands) Create(_ string, opts sessioncmd.CreateSessionOptions) (sessioncmd.Session, error) {
	f.createdOpts = opts
	return f.created, f.err
}

func (f *fakeSessionCommands) Send(_ string, id, message, subject string, interrupt bool) (sessioncmd.SendResult, error) {
	f.sentID = id
	f.sentMessage = message
	f.sentSubject = subject
	f.sentInterrupt = interrupt
	return sessioncmd.SendResult{MessageID: 7, QueuePosition: 1, ManagerAwake: true}, f.err
}

func (f *fakeSessionCommands) Wait(_ context.Context, _ string, opts sessioncmd.WaitOptions) (sessioncmd.WaitResult, error) {
	f.waitedIDs = opts.SessionIDs
	f.waitedChildren = opts.Children
	f.waitedUntil = opts.Until
	f.waitedTimeout = opts.Timeout
	f.waitedID = ""
	if len(opts.SessionIDs) > 0 {
		f.waitedID = opts.SessionIDs[0]
	}
	arrived := sessioncmd.Session{ID: f.waitedID, Name: "payments-retry", Tool: "claude", Status: "finished"}
	return sessioncmd.WaitResult{
		Session:  arrived,
		Reached:  true,
		Outcome:  sessioncmd.WaitReached,
		Waited:   "3s",
		Standing: []sessioncmd.WaitStanding{{Session: arrived, Outcome: sessioncmd.WaitReached}},
	}, f.err
}

func (f *fakeSessionCommands) MessageStatus(_ string, messageID int64) (sessioncmd.MessageState, error) {
	f.statusID = messageID
	return sessioncmd.MessageState{MessageID: messageID, SessionID: "a1b2c3d4", State: "delivered"}, f.err
}

func (f *fakeSessionCommands) Read(_ string, id string, since string) (sessioncmd.SessionScreen, error) {
	f.readID, f.readSince = id, since
	return f.screen, f.err
}

func (f *fakeSessionCommands) SendChildren(_ string, message string) (sessioncmd.ChildSend, error) {
	f.sentMessage = message
	return sessioncmd.ChildSend{Queued: 1, Deliveries: []sessioncmd.ChildDelivery{{SessionID: "kid-a", Name: "kid-a", MessageID: 1}}}, f.err
}

func (f *fakeSessionCommands) AdoptSession(_ string, id string) (sessioncmd.Session, error) {
	f.placedID, f.placedUnder = id, "caller"
	return sessioncmd.Session{ID: id, Name: "child", ParentID: "caller"}, f.err
}

func (f *fakeSessionCommands) ReleaseSession(_ string, id string) (sessioncmd.Session, error) {
	f.placedID, f.placedUnder = id, ""
	return sessioncmd.Session{ID: id, Name: "child"}, f.err
}

func (f *fakeSessionCommands) Answer(_ string, id, reply string) (sessioncmd.AnsweredQuestion, error) {
	f.answeredID, f.answeredWith = id, reply
	return sessioncmd.AnsweredQuestion{SessionID: id, Name: "child", Answer: reply, Selected: f.answerSelected}, f.err
}

func (f *fakeSessionCommands) Revive(_ string, id string) (sessioncmd.Session, error) {
	f.revivedID = id
	return f.created, f.err
}

func (f *fakeSessionCommands) SwitchAccount(_ string, id string, account string) (sessioncmd.Session, error) {
	f.switchedID, f.switchedAccount = id, account
	return f.created, f.err
}

func (f *fakeSessionCommands) Migrate(_ string, id string, opts sessioncmd.MigrateOptions) (sessioncmd.Session, error) {
	f.migratedID = id
	f.migratedOpts = opts
	return f.created, f.err
}

func (f *fakeSessionCommands) Kill(_ string, id string, _ extension.KillSource) (sessioncmd.Session, error) {
	f.killedID = id
	return f.created, f.err
}

// The row an archive returns carries the state it landed in, which is what
// the front reads its verb off.
func (f *fakeSessionCommands) Archive(_ string, id string, archived bool) (sessioncmd.Session, error) {
	f.archivedID = id
	f.archived = archived
	updated := f.created
	updated.Archived = archived
	return updated, f.err
}

func (f *fakeSessionCommands) Tasks(string) ([]sessioncmd.Task, error) {
	return f.tasks, f.err
}

func (f *fakeSessionCommands) CreateTask(_ string, title, body string, dependsOn []string) (sessioncmd.Task, error) {
	f.taskTitle = title
	f.taskBody = body
	f.taskDeps = dependsOn
	return sessioncmd.Task{ID: "t1", Title: title, State: "pending"}, f.err
}

func (f *fakeSessionCommands) ClaimTask(_ string, taskID string) (sessioncmd.Task, error) {
	f.claimedTaskID = taskID
	return sessioncmd.Task{ID: "t1", Title: "fix retries", State: "in_progress", Mine: true}, f.err
}

func (f *fakeSessionCommands) FinishTask(_ string, taskID string) (sessioncmd.Task, error) {
	f.settledTaskID = taskID
	return sessioncmd.Task{ID: taskID, Title: "fix retries", State: "done"}, f.err
}

func (f *fakeSessionCommands) ReleaseTask(_ string, taskID string) (sessioncmd.Task, error) {
	f.settledTaskID = taskID
	return sessioncmd.Task{ID: taskID, Title: "fix retries", State: "pending"}, f.err
}

func (f *fakeSessionCommands) DeleteTask(_ string, taskID string) error {
	f.settledTaskID = taskID
	return f.err
}

func (f *fakeSessionCommands) Reserve(_ string, patterns []string, mode, note string, ttl time.Duration) (sessioncmd.ReserveResult, error) {
	f.reservedPaths = patterns
	f.reservedMode = mode
	f.reservedTTL = ttl
	return sessioncmd.ReserveResult{
		Reserved:  []sessioncmd.Reservation{{Pattern: patterns[0], Mode: "exclusive", Holder: "lead", ExpiresIn: "30m0s", Mine: true}},
		Conflicts: []sessioncmd.Reservation{{Pattern: "internal/store/store.go", Mode: "exclusive", Holder: "worker", ExpiresIn: "12m0s", Note: "adding a table"}},
	}, f.err
}

func (f *fakeSessionCommands) ReleaseFiles(_ string, patterns []string) (int, error) {
	f.releasedPaths = patterns
	return len(patterns), f.err
}

func (f *fakeSessionCommands) Reservations(string) ([]sessioncmd.Reservation, error) {
	return f.reservations, f.err
}

func (f *fakeSessionCommands) Groups(string) ([]sessioncmd.Group, error) {
	return f.groups, f.err
}

func (f *fakeSessionCommands) CreateGroup(_ string, path, directory string) (sessioncmd.Group, error) {
	f.groupPath = path
	f.groupDir = directory
	return sessioncmd.Group{Path: path, Directory: directory}, f.err
}

func (f *fakeSessionCommands) DeleteGroup(_ string, path string) (sessioncmd.GroupRemoval, error) {
	f.groupPath = path
	return sessioncmd.GroupRemoval{Removed: []string{path}, Moved: []string{"a1b2c3d4"}}, f.err
}

func connect(t *testing.T, configDir, sessionID string) *mcp.ClientSession {
	t.Helper()
	return connectServer(t, NewServer(configDir, sessionID, "test", all.Extensions()))
}

func connectServer(t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func callTool(t *testing.T, session *mcp.ClientSession, tool string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", tool, err)
	}
	return result
}

func callText(t *testing.T, session *mcp.ClientSession, tool string, args map[string]any) (string, bool) {
	t.Helper()
	result := callTool(t, session, tool, args)
	var text strings.Builder
	for _, content := range result.Content {
		if tc, ok := content.(*mcp.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	return text.String(), result.IsError
}

func TestListsAllTools(t *testing.T) {
	session := connect(t, t.TempDir(), "abc123")
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{
		"rename",
		"list_terminals", "create_terminal", "send_terminal", "read_terminal", "close_terminal",
	} {
		if !names[want] {
			t.Fatalf("missing tool %q in %v", want, names)
		}
	}
	// The research-run tools are not part of this build.
	for _, retired := range []string{
		"adopt_charter", "arm_charter", "charter_status", "edit_run_charter", "end_run",
		"list_runs", "run_status", "set_run", "steer_run", "decide",
	} {
		if names[retired] {
			t.Fatalf("the server still offers %q", retired)
		}
	}
	// The review screen is gone, so an agent must no longer be offered
	// tools that declare what it would have shown.
	for name := range names {
		if strings.Contains(name, "review") {
			t.Fatalf("the server still offers %q", name)
		}
	}
}

// The errors this server returns tell an agent what to call next, and it
// can only call the tools this server registered.
func TestTheMCPVocabularyNamesRealTools(t *testing.T) {
	session := connect(t, t.TempDir(), "abc123")
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	registered := map[string]bool{}
	for _, tool := range tools.Tools {
		registered[tool.Name] = true
	}
	words := reflect.ValueOf(sessioncmd.MCPVocabulary())
	for index := range words.NumField() {
		phrase := words.Field(index).String()
		field := words.Type().Field(index).Name
		// A phrase may carry an argument, as archive_session does; the tool
		// is the first word of it.
		tool, _, _ := strings.Cut(phrase, " ")
		if !registered[tool] {
			t.Fatalf("%s names %q, which this server does not serve: %v", field, tool, registered)
		}
	}
}

// A schema tag is a literal, so raising a constant would leave the tools
// advertising the old ceiling while the shell front rewrote its own help.
func TestSchemaBoundsMatchTheirConstants(t *testing.T) {
	bounds := []struct {
		args  any
		field string
		want  string
	}{
		{reserveFilesArgs{}, "TTLM", fmt.Sprintf("default %d, maximum %d",
			int(sessioncmd.DefaultReservationTTL.Minutes()), int(sessioncmd.MaxReservationTTL.Minutes()))},
		{waitSessionArgs{}, "TimeoutS", fmt.Sprintf("default %d, maximum %d",
			int(sessioncmd.DefaultWaitTimeout.Seconds()), int(sessioncmd.MaxWaitTimeout.Seconds()))},
		{listSessionsArgs{}, "Limit", fmt.Sprintf("default %d, maximum %d",
			sessioncmd.DefaultSessionLimit, sessioncmd.MaxSessionLimit)},
	}
	for _, bound := range bounds {
		field, ok := reflect.TypeOf(bound.args).FieldByName(bound.field)
		if !ok {
			t.Fatalf("%T has no field %s", bound.args, bound.field)
		}
		if schema := field.Tag.Get("jsonschema"); !strings.Contains(schema, bound.want) {
			t.Errorf("%T.%s describes itself as %q, which does not state %q", bound.args, bound.field, schema, bound.want)
		}
	}
}

func TestServerTeachesProactiveTerminalWorkflow(t *testing.T) {
	session := connect(t, t.TempDir(), "abc123")
	instructions := session.InitializeResult().Instructions
	for _, want := range []string{
		"without waiting to be asked",
		"SSH",
		"one-shot",
		"list_terminals",
		"create_terminal",
		"send_terminal",
		"read_terminal",
		"close_terminal",
		"reuse a running terminal",
		"nests under this session",
	} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("server instructions do not teach %q:\n%s", want, instructions)
		}
	}
}

func TestTerminalDescriptionsTeachWhenAndHowToChainTools(t *testing.T) {
	session := connect(t, t.TempDir(), "abc123")
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	descriptions := map[string]string{}
	for _, tool := range listed.Tools {
		descriptions[tool.Name] = tool.Description
	}
	for tool, wants := range map[string][]string{
		"list_terminals":  {"human-visible", "Reuse", "create_terminal"},
		"create_terminal": {"SSH", "one-shot", "nests", "close_terminal"},
		"close_terminal":  {"finished", "kills", "SSH"},
		"send_terminal":   {"create_terminal", "read_terminal", "executes on the user's machine"},
		"read_terminal":   {"after send_terminal", "monitor ongoing work"},
	} {
		for _, want := range wants {
			if !strings.Contains(descriptions[tool], want) {
				t.Errorf("%s description does not contain %q: %s", tool, want, descriptions[tool])
			}
		}
	}
}

func TestTerminalToolsExposeStructuredResultsAndForwardArguments(t *testing.T) {
	group := "backend"
	fake := &fakeTerminalCommands{
		listed: []sessioncmd.Terminal{{
			ID: "a1b2c3d4", Name: "terminal-a1b2", Group: group,
			Directory: "/work", Status: "idle", Running: true,
		}},
		created: sessioncmd.Terminal{
			ID: "e5f6a7b8", Name: "terminal-e5f6", Group: group,
			Directory: "/tmp", Status: "starting", Running: true,
		},
		screen: sessioncmd.TerminalScreen{
			Terminal: sessioncmd.Terminal{ID: "a1b2c3d4", Name: "terminal-a1b2", Running: true},
			Output:   "build complete",
		},
	}
	session := connectServer(t, newServer(t.TempDir(), "abc123", "test", fake, &fakeSessionCommands{}))

	listed := callTool(t, session, "list_terminals", map[string]any{})
	if listed.IsError || listed.StructuredContent == nil {
		t.Fatalf("list_terminals = %+v", listed)
	}
	if text, _ := callText(t, session, "list_terminals", map[string]any{}); !strings.Contains(text, "terminal-a1b2") {
		t.Fatalf("list text = %q", text)
	}

	created := callTool(t, session, "create_terminal", map[string]any{"group": group, "directory": "/tmp"})
	if created.IsError || created.StructuredContent == nil {
		t.Fatalf("create_terminal = %+v", created)
	}
	if fake.createdOpts.Group == nil || *fake.createdOpts.Group != group || fake.createdOpts.Directory != "/tmp" {
		t.Fatalf("create args = %+v", fake.createdOpts)
	}

	if text, isError := callText(t, session, "send_terminal", map[string]any{
		"terminal_id": "a1b2c3d4", "command": "go test ./...",
	}); isError || !strings.Contains(text, "sent command") {
		t.Fatalf("send command = %q, isError=%v", text, isError)
	}
	if fake.sentID != "a1b2c3d4" || fake.sentCommand != "go test ./..." || len(fake.sentKeys) != 0 {
		t.Fatalf("send command args = id %q command %q keys %v", fake.sentID, fake.sentCommand, fake.sentKeys)
	}

	if _, isError := callText(t, session, "send_terminal", map[string]any{
		"terminal_id": "a1b2c3d4", "keys": []string{"C-c", "Enter"},
	}); isError {
		t.Fatal("send keys returned an error")
	}
	if fake.sentCommand != "" || strings.Join(fake.sentKeys, ",") != "C-c,Enter" {
		t.Fatalf("send key args = command %q keys %v", fake.sentCommand, fake.sentKeys)
	}

	if text, isError := callText(t, session, "read_terminal", map[string]any{"terminal_id": "a1b2c3d4"}); isError || text != "build complete" {
		t.Fatalf("read = %q, isError=%v", text, isError)
	}
	if fake.readID != "a1b2c3d4" {
		t.Fatalf("read id = %q", fake.readID)
	}
}

func TestCloseTerminalForwardsID(t *testing.T) {
	fake := &fakeTerminalCommands{}
	session := connectServer(t, newServer(t.TempDir(), "abc123", "test", fake, &fakeSessionCommands{}))
	text, isError := callText(t, session, "close_terminal", map[string]any{"terminal_id": "a1b2c3d4"})
	if isError || !strings.Contains(text, "closed terminal a1b2c3d4") {
		t.Fatalf("close_terminal = %q, isError=%v", text, isError)
	}
	if fake.closedID != "a1b2c3d4" {
		t.Fatalf("closed id = %q", fake.closedID)
	}
}

func TestCreateTerminalForwardsNest(t *testing.T) {
	fake := &fakeTerminalCommands{
		created: sessioncmd.Terminal{ID: "e5f6a7b8", Name: "terminal-e5f6"},
	}
	session := connectServer(t, newServer(t.TempDir(), "abc123", "test", fake, &fakeSessionCommands{}))

	if created := callTool(t, session, "create_terminal", map[string]any{}); created.IsError {
		t.Fatalf("create_terminal no args = %+v", created)
	}
	if fake.createdOpts.Nest != nil {
		t.Fatalf("omitted nest = %+v, want nil", fake.createdOpts.Nest)
	}

	if created := callTool(t, session, "create_terminal", map[string]any{"nest": false}); created.IsError {
		t.Fatalf("create_terminal nest false = %+v", created)
	}
	if fake.createdOpts.Nest == nil || *fake.createdOpts.Nest {
		t.Fatalf("nest false = %+v", fake.createdOpts.Nest)
	}
}

func TestTerminalToolAnnotationsDescribeLocalRisk(t *testing.T) {
	session := connect(t, t.TempDir(), "abc123")
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]*mcp.Tool{}
	for _, tool := range listed.Tools {
		tools[tool.Name] = tool
	}
	for _, name := range []string{"list_terminals", "read_terminal"} {
		if annotations := tools[name].Annotations; annotations == nil || !annotations.ReadOnlyHint || annotations.OpenWorldHint == nil || *annotations.OpenWorldHint {
			t.Fatalf("%s annotations = %+v", name, annotations)
		}
		if tools[name].OutputSchema == nil {
			t.Fatalf("%s has no structured output schema", name)
		}
	}
	if annotations := tools["create_terminal"].Annotations; annotations == nil || annotations.DestructiveHint == nil || *annotations.DestructiveHint {
		t.Fatalf("create annotations = %+v", annotations)
	}
	if annotations := tools["send_terminal"].Annotations; annotations == nil || annotations.DestructiveHint == nil || !*annotations.DestructiveHint || annotations.OpenWorldHint == nil || !*annotations.OpenWorldHint {
		t.Fatalf("send annotations = %+v", annotations)
	}
	if tool := tools["close_terminal"]; tool == nil {
		t.Fatal("missing close_terminal")
	} else if annotations := tool.Annotations; annotations == nil || annotations.DestructiveHint == nil || !*annotations.DestructiveHint {
		t.Fatalf("close annotations = %+v", annotations)
	}
}

func TestTerminalToolErrorsAreToolErrors(t *testing.T) {
	fake := &fakeTerminalCommands{err: errors.New("terminal is not running")}
	session := connectServer(t, newServer(t.TempDir(), "abc123", "test", fake, &fakeSessionCommands{}))
	for _, call := range []struct {
		name string
		args map[string]any
	}{
		{"list_terminals", map[string]any{}},
		{"create_terminal", map[string]any{}},
		{"send_terminal", map[string]any{"terminal_id": "a1", "command": "pwd"}},
		{"read_terminal", map[string]any{"terminal_id": "a1"}},
		{"close_terminal", map[string]any{"terminal_id": "a1"}},
	} {
		text, isError := callText(t, session, call.name, call.args)
		if !isError || !strings.Contains(text, "not running") {
			t.Fatalf("%s = %q, isError=%v", call.name, text, isError)
		}
	}
}

func TestRenameWritesMailbox(t *testing.T) {
	configDir := tmuxtest.ScratchDir(t)
	session := connect(t, configDir, "abc123")
	text, isError := callText(t, session, "rename", map[string]any{"name": "fix-auth-bug"})
	if isError || !strings.Contains(text, "fix-auth-bug") {
		t.Fatalf("rename = %q, isError=%v", text, isError)
	}
	content, err := os.ReadFile(hooks.NewManager(configDir).NameFile("abc123"))
	if err != nil || string(content) != "fix-auth-bug" {
		t.Fatalf("mailbox = %q, %v", content, err)
	}
}

func TestBadInputsReturnToolErrors(t *testing.T) {
	configDir := tmuxtest.ScratchDir(t)

	session := connect(t, configDir, "abc123")
	if text, isError := callText(t, session, "rename", map[string]any{"name": "  "}); !isError {
		t.Fatalf("empty name should error, got %q", text)
	}

	noSession := connect(t, configDir, "")
	if text, isError := callText(t, noSession, "rename", map[string]any{"name": "x"}); !isError || !strings.Contains(text, "GATE_INBOX_SESSION_ID") {
		t.Fatalf("missing session id should error, got %q", text)
	}
}

func TestListsFleetTools(t *testing.T) {
	session := connect(t, t.TempDir(), "abc123")
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{
		"list_sessions", "create_session", "read_session", "send_session",
		"revive_session", "kill_session", "archive_session",
		"list_groups", "create_group", "delete_group", "message_status", "wait_for_session",
		"list_accounts", "switch_account",
		"task",
		"reserve_files", "release_files", "list_reservations",
	} {
		if !names[want] {
			t.Fatalf("missing tool %q in %v", want, names)
		}
	}
}

// A model already has a way to run work in parallel, and reads a list of
// sessions as that list unless the block says otherwise. What the tools
// reach is the user's machine, so the block names it: other CLIs, running
// whatever the user picked, outliving this conversation.
func TestServerTeachesThatSessionsAreOtherCLIsNotSubagents(t *testing.T) {
	session := connect(t, t.TempDir(), "abc123")
	instructions := session.InitializeResult().Instructions
	for _, want := range []string{
		"separate CLI processes",
		"never subagents of this conversation",
		"Codex",
	} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("server instructions do not teach %q:\n%s", want, instructions)
		}
	}
}

func TestServerTeachesDelegationWorkflow(t *testing.T) {
	session := connect(t, t.TempDir(), "abc123")
	instructions := session.InitializeResult().Instructions
	for _, want := range []string{
		"list_sessions",
		"create_session",
		"read_session",
		"send_session",
		"wait_for_session",
		"the task tool",
		"reserve_files",
		"own checkout",
		"without waiting to be asked",
		// A parent once read "group related spawns" as create_group plus
		// nest false, and its whole fan-out landed flat with nothing relayed
		// to it; the block has to say the opposite.
		"never make a group for one",
	} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("server instructions do not teach %q:\n%s", want, instructions)
		}
	}
}

// The instruction block is the whole discovery mechanism: with it emptied,
// a model offered these same tools reaches for its own subagents instead.
// Claude Code truncates it at 2048 characters, and a block that overruns
// loses its tail there silently, so the length is part of the contract.
func TestServerInstructionsSurviveTheClientLimit(t *testing.T) {
	const claudeCodeLimit = 2048
	session := connect(t, t.TempDir(), "abc123")
	instructions := session.InitializeResult().Instructions
	if len(instructions) >= claudeCodeLimit {
		t.Fatalf("server instructions are %d characters; Claude Code truncates at %d, dropping the tail", len(instructions), claudeCodeLimit)
	}
	// The safety paragraph is the tail, and the one thing no tool
	// description repeats.
	if !strings.Contains(instructions, "acts on the user's machine") {
		t.Fatalf("the instructions no longer say these tools act on the user's machine:\n%s", instructions)
	}
}

func TestSessionDescriptionsTeachWhenAndHowToChainTools(t *testing.T) {
	session := connect(t, t.TempDir(), "abc123")
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	descriptions := map[string]string{}
	for _, tool := range listed.Tools {
		descriptions[tool.Name] = tool.Description
	}
	for tool, wants := range map[string][]string{
		"list_sessions":    {"Call first", "create_session"},
		"create_session":   {"without waiting for the user", "own checkout", "cannot see this conversation", "read_session", "never create a group for one", "nest false is a detach"},
		"read_session":     {"after create_session", "current screen"},
		"send_session":     {"self-contained instruction", "read_session", "at rest", "another agent rather than from the user"},
		"message_status":   {"delivered", "queued"},
		"wait_for_session": {"instead of calling read_session in a loop", "timeout is a normal answer", "reached false", "children true", "never wait on children one at a time", "standing"},
		"revive_session":   {"dead session"},
		"kill_session":     {"revive_session", "ask first"},
		"archive_session":  {"archived false"},
		"create_group":     {"list_groups", "parent", "Not for a fan-out"},
	} {
		for _, want := range wants {
			if !strings.Contains(descriptions[tool], want) {
				t.Errorf("%s description does not contain %q: %s", tool, want, descriptions[tool])
			}
		}
	}
}

func TestSessionToolsExposeStructuredResultsAndForwardArguments(t *testing.T) {
	group := "backend"
	fake := &fakeSessionCommands{
		listed: []sessioncmd.Session{{
			ID: "a1b2c3d4", Name: "payments-retry", Tool: "claude", Group: group,
			Directory: "/work", Status: "working", Running: true, Self: true,
		}},
		created: sessioncmd.Session{
			ID: "e5f6a7b8", Name: "payments-retry-fix", Tool: "codex", Group: group,
			Directory: "/work/tree", Status: "starting", Running: true,
		},
		screen: sessioncmd.SessionScreen{
			Session: sessioncmd.Session{ID: "a1b2c3d4", Name: "payments-retry", Running: true},
			Output:  "tests passing",
		},
		groups: []sessioncmd.Group{{Path: group, Directory: "/work", Sessions: 2}},
	}
	session := connectServer(t, newServer(t.TempDir(), "abc123", "test", &fakeTerminalCommands{}, fake))

	listed := callTool(t, session, "list_sessions", map[string]any{})
	if listed.IsError || listed.StructuredContent == nil {
		t.Fatalf("list_sessions = %+v", listed)
	}
	if text, _ := callText(t, session, "list_sessions", map[string]any{}); !strings.Contains(text, "payments-retry") || !strings.Contains(text, "this session") {
		t.Fatalf("list text = %q", text)
	}
	if opts := fake.listOpts; opts.Parent != "" || len(opts.Status) != 0 || opts.IncludeArchived || opts.Limit != 0 {
		t.Fatalf("an unfiltered call invented filters: %+v", opts)
	}
	narrowed := callTool(t, session, "list_sessions", map[string]any{
		"parent": "me", "status": []string{"working", "waiting"},
		"include_archived": true, "limit": 5,
	})
	if narrowed.IsError {
		t.Fatalf("filtered list_sessions = %+v", narrowed)
	}
	if opts := fake.listOpts; opts.Parent != "me" || strings.Join(opts.Status, ",") != "working,waiting" ||
		!opts.IncludeArchived || opts.Limit != 5 {
		t.Fatalf("list filters reached the layer as %+v", fake.listOpts)
	}
	// The rendering and the structured rows carry the same sessions and the
	// SDK sends both, so a caller reading only the rows can drop the prose.
	structuredOnly, isError := callText(t, session, "list_sessions", map[string]any{"include_text": false})
	if isError || !strings.Contains(structuredOnly, "1 of 1 matching sessions") {
		t.Fatalf("structured-only list_sessions = %q, isError=%v", structuredOnly, isError)
	}
	// Not an empty rendering: the SDK backfills an empty Content with the
	// structured payload re-encoded, which costs more than the prose it
	// would have replaced.
	if strings.Contains(structuredOnly, "payments-retry") {
		t.Fatalf("include_text false still spelled the rows out: %q", structuredOnly)
	}

	created := callTool(t, session, "create_session", map[string]any{
		"name": "payments-retry-fix", "prompt": "fix the retry backoff",
		"tool": "codex", "group": group, "directory": "/work",
	})
	if created.IsError || created.StructuredContent == nil {
		t.Fatalf("create_session = %+v", created)
	}
	opts := fake.createdOpts
	if opts.Name != "payments-retry-fix" || opts.Prompt != "fix the retry backoff" || opts.Tool != "codex" {
		t.Fatalf("create args = %+v", opts)
	}
	if opts.Group == nil || *opts.Group != group || opts.Directory != "/work" {
		t.Fatalf("create target = %+v", opts)
	}

	if text, isError := callText(t, session, "send_session", map[string]any{
		"session_id": "a1b2c3d4", "message": "rebase on main",
	}); isError || !strings.Contains(text, "queued message 7") {
		t.Fatalf("send_session = %q, isError=%v", text, isError)
	}
	if fake.sentID != "a1b2c3d4" || fake.sentMessage != "rebase on main" {
		t.Fatalf("send args = id %q message %q", fake.sentID, fake.sentMessage)
	}

	if text, isError := callText(t, session, "message_status", map[string]any{"message_id": 7}); isError || !strings.Contains(text, "delivered") {
		t.Fatalf("message_status = %q, isError=%v", text, isError)
	}
	if fake.statusID != 7 {
		t.Fatalf("message_status reached the layer with id %d", fake.statusID)
	}

	if text, isError := callText(t, session, "read_session", map[string]any{"session_id": "a1b2c3d4"}); isError || !strings.Contains(text, "tests passing") {
		t.Fatalf("read_session = %q, isError=%v", text, isError)
	}
	// The cursor is the whole point of the second read, so a front that
	// dropped it would leave every caller silently re-reading whole panes.
	if _, isError := callText(t, session, "read_session", map[string]any{"session_id": "a1b2c3d4", "since": "conv-1@4096"}); isError {
		t.Fatal("read_session with a cursor returned an error")
	}
	if fake.readSince != "conv-1@4096" {
		t.Fatalf("read_session reached the layer with since %q", fake.readSince)
	}

	if text, isError := callText(t, session, "wait_for_session", map[string]any{
		"session_id": "a1b2c3d4", "until": []string{"finished"}, "timeout_s": 30,
	}); isError || !strings.Contains(text, "finished") {
		t.Fatalf("wait_for_session = %q, isError=%v", text, isError)
	}
	if fake.waitedID != "a1b2c3d4" || strings.Join(fake.waitedUntil, ",") != "finished" || fake.waitedTimeout != 30*time.Second {
		t.Fatalf("wait args = id %q until %v timeout %v", fake.waitedID, fake.waitedUntil, fake.waitedTimeout)
	}
	// The fan-out arguments reach the layer as the set the tool advertises:
	// the singular id folded in beside the list, and children on its own.
	if _, isError := callText(t, session, "wait_for_session", map[string]any{
		"session_id": "a1b2c3d4", "session_ids": []string{"e5f6a7b8"},
	}); isError {
		t.Fatal("wait_for_session over a named set returned an error")
	}
	if strings.Join(fake.waitedIDs, ",") != "a1b2c3d4,e5f6a7b8" || fake.waitedChildren {
		t.Fatalf("wait set = %v children %v", fake.waitedIDs, fake.waitedChildren)
	}
	if _, isError := callText(t, session, "wait_for_session", map[string]any{"children": true}); isError {
		t.Fatal("wait_for_session over children returned an error")
	}
	if !fake.waitedChildren || len(fake.waitedIDs) != 0 {
		t.Fatalf("children wait = %v ids %v", fake.waitedChildren, fake.waitedIDs)
	}

	if _, isError := callText(t, session, "revive_session", map[string]any{"session_id": "a1b2c3d4"}); isError {
		t.Fatal("revive_session returned an error")
	}
	if fake.revivedID != "a1b2c3d4" {
		t.Fatalf("revive id = %q", fake.revivedID)
	}

	if _, isError := callText(t, session, "kill_session", map[string]any{"session_id": "a1b2c3d4"}); isError {
		t.Fatal("kill_session returned an error")
	}
	if fake.killedID != "a1b2c3d4" {
		t.Fatalf("kill id = %q", fake.killedID)
	}

	if text, _ := callText(t, session, "archive_session", map[string]any{"session_id": "a1b2c3d4"}); !strings.Contains(text, "archived") {
		t.Fatalf("archive text = %q", text)
	}
	if !fake.archived {
		t.Fatal("archive_session should default to archiving")
	}
	if text, _ := callText(t, session, "archive_session", map[string]any{"session_id": "a1b2c3d4", "archived": false}); !strings.Contains(text, "restored") {
		t.Fatalf("restore text = %q", text)
	}
	if fake.archived {
		t.Fatal("archived false should restore")
	}

	if text, _ := callText(t, session, "list_groups", map[string]any{}); !strings.Contains(text, "backend") {
		t.Fatalf("list_groups text = %q", text)
	}
	if _, isError := callText(t, session, "create_group", map[string]any{"path": "work/payments", "directory": "/work"}); isError {
		t.Fatal("create_group returned an error")
	}
	if fake.groupPath != "work/payments" || fake.groupDir != "/work" {
		t.Fatalf("create_group args = %q %q", fake.groupPath, fake.groupDir)
	}

	text, isError := callText(t, session, "delete_group", map[string]any{"path": "work/payments"})
	if isError {
		t.Fatalf("delete_group errored: %q", text)
	}
	if fake.groupPath != "work/payments" {
		t.Fatalf("delete_group path = %q", fake.groupPath)
	}
	if !strings.Contains(text, "deleted work/payments") || !strings.Contains(text, "a1b2c3d4") {
		t.Fatalf("delete_group text = %q", text)
	}
}

func TestSessionToolAnnotationsDescribeLocalRisk(t *testing.T) {
	session := connect(t, t.TempDir(), "abc123")
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]*mcp.Tool{}
	for _, tool := range listed.Tools {
		tools[tool.Name] = tool
	}
	// A rename is the change this test exists to catch, and reading the
	// annotations off a tool that is no longer there panics the package.
	for _, name := range []string{"list_sessions", "read_session", "list_groups", "kill_session", "create_session", "send_session"} {
		if tools[name] == nil {
			t.Fatalf("%s is not registered", name)
		}
	}
	for _, name := range []string{"list_sessions", "read_session", "list_groups"} {
		if annotations := tools[name].Annotations; annotations == nil || !annotations.ReadOnlyHint {
			t.Fatalf("%s annotations = %+v", name, annotations)
		}
		if tools[name].OutputSchema == nil {
			t.Fatalf("%s has no structured output schema", name)
		}
	}
	if annotations := tools["kill_session"].Annotations; annotations == nil || annotations.DestructiveHint == nil || !*annotations.DestructiveHint {
		t.Fatalf("kill annotations = %+v", annotations)
	}
	for _, name := range []string{"create_session", "send_session"} {
		annotations := tools[name].Annotations
		if annotations == nil || annotations.ReadOnlyHint || annotations.OpenWorldHint == nil || !*annotations.OpenWorldHint {
			t.Fatalf("%s annotations = %+v", name, annotations)
		}
	}
}

func TestSessionToolErrorsAreToolErrors(t *testing.T) {
	fake := &fakeSessionCommands{err: errors.New("session is not running")}
	session := connectServer(t, serverWithFakes(t, fake))
	for _, call := range []struct {
		name string
		args map[string]any
	}{
		{"list_sessions", map[string]any{}},
		{"create_session", map[string]any{"name": "x"}},
		{"read_session", map[string]any{"session_id": "a1"}},
		{"send_session", map[string]any{"session_id": "a1", "message": "hi"}},
		{"message_status", map[string]any{"message_id": 7}},
		{"wait_for_session", map[string]any{"session_id": "a1"}},
		{"task", map[string]any{"action": "list"}},
		{"task", map[string]any{"action": "create", "title": "x"}},
		{"task", map[string]any{"action": "claim"}},
		{"task", map[string]any{"action": "finish", "task_id": "t1"}},
		{"task", map[string]any{"action": "release", "task_id": "t1"}},
		{"task", map[string]any{"action": "delete", "task_id": "t1"}},
		{"reserve_files", map[string]any{"paths": []string{"a.go"}}},
		{"release_files", map[string]any{}},
		{"list_reservations", map[string]any{}},
		{"revive_session", map[string]any{"session_id": "a1"}},
		{"kill_session", map[string]any{"session_id": "a1"}},
		{"archive_session", map[string]any{"session_id": "a1"}},
		{"list_groups", map[string]any{}},
		{"create_group", map[string]any{"path": "work"}},
		{"delete_group", map[string]any{"path": "work"}},
	} {
		text, isError := callText(t, session, call.name, call.args)
		if !isError || !strings.Contains(text, "not running") {
			t.Fatalf("%s = %q, isError=%v", call.name, text, isError)
		}
	}
}

func serverWithFakes(t *testing.T, sessions sessionCommands) *mcp.Server {
	t.Helper()
	return newServer(t.TempDir(), "abc123", "test", &fakeTerminalCommands{}, sessions)
}

// The account an agent names reaches the command layer as it typed it --
// the normalizing and the refusals belong to one place below this, not to
// the front -- and an omitted account is the CLI's own login rather than
// "leave it alone", which is how a session moves back off a pooled token.
func TestSwitchAccountForwardsTheTargetAndTheAccount(t *testing.T) {
	fake := &fakeSessionCommands{created: sessioncmd.Session{ID: "a1b2c3d4", Name: "worker", Account: "ALICE1"}}
	session := connectServer(t, serverWithFakes(t, fake))

	text, isError := callText(t, session, "switch_account", map[string]any{
		"session_id": "a1b2c3d4", "account": "alice1",
	})
	if isError || !strings.Contains(text, "switched") || !strings.Contains(text, "worker") {
		t.Fatalf("switch_account = %q, isError=%v", text, isError)
	}
	if fake.switchedID != "a1b2c3d4" || fake.switchedAccount != "alice1" {
		t.Fatalf("switch args = id %q account %q", fake.switchedID, fake.switchedAccount)
	}

	fake.switchedAccount = "not-called"
	if _, isError := callText(t, session, "switch_account", map[string]any{"session_id": "a1b2c3d4"}); isError {
		t.Fatal("switch_account with no account errored")
	}
	if fake.switchedAccount != "" {
		t.Fatalf("an omitted account reached the command layer as %q", fake.switchedAccount)
	}

	fake.err = errors.New("a session cannot switch its own account")
	if text, isError := callText(t, session, "switch_account", map[string]any{"session_id": "a1b2c3d4"}); !isError ||
		!strings.Contains(text, "cannot switch its own account") {
		t.Fatalf("a refused switch answered %q, isError=%v", text, isError)
	}
}

func TestTaskToolsForwardArgumentsAndRenderTheList(t *testing.T) {
	fake := &fakeSessionCommands{
		tasks: []sessioncmd.Task{
			{ID: "t1", Title: "add the column", State: "done"},
			{ID: "t2", Title: "backfill it", State: "pending", DependsOn: []string{"t1"}, BlockedBy: []string{"t1"}, Blocked: true},
			{ID: "t3", Title: "verify", State: "in_progress", OwnerName: "worker", Mine: true},
		},
	}
	session := connectServer(t, serverWithFakes(t, fake))

	text, isError := callText(t, session, "task", map[string]any{"action": "list"})
	if isError {
		t.Fatalf("task list errored: %q", text)
	}
	for _, want := range []string{"add the column", "blocked on t1", "held by worker", "yours"} {
		if !strings.Contains(text, want) {
			t.Fatalf("task list text missing %q: %s", want, text)
		}
	}

	if _, isError := callText(t, session, "task", map[string]any{
		"action": "create", "title": "fix retries", "body": "see internal/retry", "depends_on": []string{"t1"},
	}); isError {
		t.Fatal("task create errored")
	}
	if fake.taskTitle != "fix retries" || fake.taskBody != "see internal/retry" || strings.Join(fake.taskDeps, ",") != "t1" {
		t.Fatalf("task create args = %q %q %v", fake.taskTitle, fake.taskBody, fake.taskDeps)
	}

	if _, isError := callText(t, session, "task", map[string]any{"action": "claim"}); isError {
		t.Fatal("claim with no id errored")
	}
	if fake.claimedTaskID != "" {
		t.Fatalf("claim-next should forward an empty id, got %q", fake.claimedTaskID)
	}
	if _, isError := callText(t, session, "task", map[string]any{"action": "claim", "task_id": "t2"}); isError {
		t.Fatal("claim errored")
	}
	if fake.claimedTaskID != "t2" {
		t.Fatalf("claim id = %q", fake.claimedTaskID)
	}
	for _, action := range []string{"finish", "release", "delete"} {
		fake.settledTaskID = ""
		if _, isError := callText(t, session, "task", map[string]any{"action": action, "task_id": "t3"}); isError {
			t.Fatalf("%s errored", action)
		}
		if fake.settledTaskID != "t3" {
			t.Fatalf("%s id = %q", action, fake.settledTaskID)
		}
	}

	if text, isError := callText(t, session, "task", map[string]any{"action": "bogus"}); !isError ||
		!strings.Contains(text, "unknown action") {
		t.Fatalf("an unknown action answered %q, isError=%v", text, isError)
	}
}

func TestReservationToolsReportConflictsWithoutRefusingTheLease(t *testing.T) {
	fake := &fakeSessionCommands{
		reservations: []sessioncmd.Reservation{
			{Pattern: "internal/ui/*.go", Mode: "exclusive", Holder: "worker", ExpiresIn: "20m0s", Note: "focus mode"},
		},
	}
	session := connectServer(t, serverWithFakes(t, fake))

	text, isError := callText(t, session, "reserve_files", map[string]any{
		"paths": []string{"internal/store/*.go"}, "mode": "exclusive", "note": "inbox table", "ttl_minutes": 45,
	})
	if isError {
		t.Fatalf("reserve_files errored: %q", text)
	}
	for _, want := range []string{"reserved internal/store/*.go", "conflicts with leases already held", "held by worker", "adding a table"} {
		if !strings.Contains(text, want) {
			t.Fatalf("reserve text missing %q: %s", want, text)
		}
	}
	if strings.Join(fake.reservedPaths, ",") != "internal/store/*.go" || fake.reservedMode != "exclusive" || fake.reservedTTL != 45*time.Minute {
		t.Fatalf("reserve args = %v %q %v", fake.reservedPaths, fake.reservedMode, fake.reservedTTL)
	}

	if text, _ := callText(t, session, "list_reservations", map[string]any{}); !strings.Contains(text, "focus mode") {
		t.Fatalf("list_reservations text = %q", text)
	}
	released := callTool(t, session, "release_files", map[string]any{"paths": []string{"internal/store/*.go", "internal/ui/*.go"}})
	if released.IsError {
		t.Fatalf("release_files = %+v", released)
	}
	// The count is what tells a caller how much of what it asked for it
	// actually held, and the shell front already hands it back as a number.
	if structured, ok := released.StructuredContent.(map[string]any); !ok || structured["released"] != float64(2) {
		t.Fatalf("release_files structured = %#v", released.StructuredContent)
	}
	if strings.Join(fake.releasedPaths, ",") != "internal/store/*.go,internal/ui/*.go" {
		t.Fatalf("release args = %v", fake.releasedPaths)
	}
}

func TestCreateSessionForwardsTheModel(t *testing.T) {
	fake := &fakeSessionCommands{}
	session := connectServer(t, serverWithFakes(t, fake))
	if text, isError := callText(t, session, "create_session",
		map[string]any{"name": "worker", "tool": "claude", "model": "opus"}); isError {
		t.Fatalf("create_session: %s", text)
	}
	if fake.createdOpts.Model != "opus" {
		t.Errorf("Model = %q, want it forwarded", fake.createdOpts.Model)
	}
	// Omitted stays omitted: that is the CLI's own default, and the tool must
	// not invent one.
	fake.createdOpts = sessioncmd.CreateSessionOptions{}
	if text, isError := callText(t, session, "create_session",
		map[string]any{"name": "worker", "tool": "claude"}); isError {
		t.Fatalf("create_session: %s", text)
	}
	if fake.createdOpts.Model != "" {
		t.Errorf("Model = %q, want empty", fake.createdOpts.Model)
	}
}
