// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package sessioncmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/usestring/gate-inbox/internal/git"
	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

type sessionHarness struct {
	driver    *tmux.Driver
	store     *store.Store
	sessions  *Sessions
	terminals *Terminals
	caller    store.Session
}

// sessionConfig gives the harness one agent CLI whose command echoes the
// prompt it launched with, so a spawn's own pane proves the prompt reached
// it, plus a shell block the agent tools must refuse.
const sessionConfig = `[tools.stoppable]
command = "cat"
default_status = "idle"
activity_cutoff = "(?m)^\u276f"
interrupt_keys = ["Escape"]

[tools.echoer]
command = "echo"
model_flag = "--model"
revive_command = "echo resumed"
default_status = "idle"
activity_cutoff = "(?m)^\u276f"

[tools.no-model-tool]
command = "echo"
default_status = "idle"
activity_cutoff = "(?m)^\u276f"

[tools.flagged]
command = "echo"
prompt_flag = "-n"
default_status = "idle"
activity_cutoff = "(?m)^\u276f"

[tools.blind]
command = "echo"
default_status = "idle"

# Stands in for a CLI sitting on an approval dialog: the input line is drawn
# under it, and only the rule tells that apart from a resting prompt.
[tools.dialog]
command = "printf 'Do you want to proceed?\\n  1. Yes\\n  2. No\\nEnter to confirm\\n❯ ' && cat"
default_status = "idle"
activity_cutoff = "(?m)^❯"
rules = [{ state = "waiting", pattern = "Enter to confirm" }]

[tools.resting]
command = "printf '❯ ' && cat"
default_status = "idle"
activity_cutoff = "(?m)^❯"

[tools.terminal]
command = ""
shell = true
default_status = "idle"

# Stands in for Claude Code: park reads its conversation off a per-process
# session file and unpark resumes it by id.
[tools.claude]
command = "echo"
resume_by_id_command = "echo resumed {id}"
revive_command = "echo continued"
default_status = "idle"
activity_cutoff = "(?m)^\u276f"
# The account recipe answers from the shell, so no secret store is reached.
# All four keys are spelled out: the built-in claude block names no accounts.
account_env = "CLAUDE_CODE_OAUTH_TOKEN"
account_secret = "CLAUDE_OAUTH_TOKEN_{account}"
account_command = "echo test-token-{secret}"
accounts_command = "printf 'CLAUDE_OAUTH_TOKEN_ALICE1\\n'"

# Stands in for an agent mid-turn: the screen matches a working rule, and
# a relaunch echoes whatever prompt rides its command line.
[tools.busy]
command = "printf 'Thinking about it\\n' && sleep 30"
revive_command = "echo resumed"
default_status = "idle"
activity_cutoff = "(?m)^\u276f"
rules = [{ state = "working", pattern = "Thinking about it" }]

# A tool park can recognise in a process tree that keeps no session file.
[tools.sleeper]
command = "sleep 30"
default_status = "idle"
activity_cutoff = "(?m)^\u276f"
`

func newSessionHarness(t *testing.T) *sessionHarness {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	configDir := tmuxtest.ScratchDir(t)
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(sessionConfig), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	driver, err := tmux.NewWithSocket(tmuxtest.NewSocket("sess"))
	if err != nil {
		t.Fatalf("tmux driver: %v", err)
	}
	st, err := store.Open(filepath.Join(configDir, "state.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	callerDir := t.TempDir()
	caller := store.Session{
		ID:     uuid.NewString()[:8],
		Name:   "calling-agent",
		Tool:   "echoer",
		Cwd:    callerDir,
		Group:  "backend",
		Status: status.Idle,
	}
	if err := st.CreateGroup("backend", callerDir); err != nil {
		t.Fatalf("create group: %v", err)
	}
	if err := driver.Create(caller.ID, caller.Cwd, "", nil, 80, 24); err != nil {
		t.Fatalf("create caller pane: %v", err)
	}
	if err := st.CreateSession(caller); err != nil {
		_ = driver.Kill(caller.ID)
		t.Fatalf("create caller row: %v", err)
	}
	newDriver := func(string) (*tmux.Driver, error) { return driver, nil }
	h := &sessionHarness{
		driver:    driver,
		store:     st,
		caller:    caller,
		sessions:  newSessions(configDir, MCPVocabulary(), newDriver, git.New),
		terminals: newTerminals(configDir, MCPVocabulary(), newDriver),
	}
	t.Cleanup(func() {
		sessions, _ := st.ListSessions(true)
		for _, sess := range sessions {
			_ = driver.Kill(sess.ID)
		}
		// Every capture opened a pooled control-mode client -- a process --
		// and those outlive the server, so kill-server alone left one
		// running per harness. Close first, then kill the server, then
		// collect anything that still holds the socket.
		driver.CloseCaptureClients()
		// kill-server ends every session on the socket, so a harness that
		// somehow resolved the default one would take the operator's own
		// tmux down with it.
		if !tmux.OwnsSocket(driver.SocketName()) {
			t.Fatalf("refusing to kill tmux server %q: that is tmux's default server", driver.SocketName())
		}
		if out, err := exec.Command("tmux", "-L", driver.SocketName(), "kill-server").CombinedOutput(); err != nil && !serverAlreadyGone(string(out)) {
			t.Errorf("kill test tmux server: %v: %s", err, strings.TrimSpace(string(out)))
		}
		tmuxtest.ReapSocket(driver.SocketName())
		_ = st.Close()
	})
	return h
}

func waitForSessionOutput(t *testing.T, sessions *Sessions, callerID, targetID, marker string) SessionScreen {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		screen, err := sessions.Read(callerID, targetID, "")
		if err == nil && strings.Contains(screen.Output, marker) {
			return screen
		}
		time.Sleep(25 * time.Millisecond)
	}
	screen, err := sessions.Read(callerID, targetID, "")
	t.Fatalf("session never showed %q: output=%q err=%v", marker, screen.Output, err)
	return SessionScreen{}
}

func TestSessionsCreateCarriesNamePromptAndTargetWithRealTmux(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{
		Name:   "payments-retry-fix",
		Prompt: "fix the retry backoff",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Name != "payments-retry-fix" || created.Tool != "echoer" || !created.Running {
		t.Fatalf("created identity = %+v", created)
	}
	if created.Group != h.caller.Group || !sameTerminalPath(created.Directory, h.caller.Cwd) {
		t.Fatalf("created target = %+v, caller = %+v", created, h.caller)
	}
	stored, err := h.store.Get(created.ID)
	if err != nil {
		t.Fatalf("stored session: %v", err)
	}
	if stored.Name != created.Name || stored.Tool != "echoer" || stored.Status != status.Starting {
		t.Fatalf("stored row = %+v", stored)
	}
	// echo prints what the launch command handed it, so the pane proves the
	// prompt rode the command line rather than being dropped.
	waitForSessionOutput(t, h.sessions, h.caller.ID, created.ID, "fix the retry backoff")
}

func TestSessionsCreateAutoNamesAndAsksForARename(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Prompt: "build the api"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Name != "echoer-"+created.ID[:4] {
		t.Fatalf("auto-named session = %q", created.Name)
	}
	waitForSessionOutput(t, h.sessions, h.caller.ID, created.ID, "build the api")
}

func TestSessionsCreateRejectsShellsUnknownToolsAndFlagPrompts(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	if _, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Tool: "terminal"}); err == nil ||
		!strings.Contains(err.Error(), "create_terminal") {
		t.Fatalf("shell tool error = %v", err)
	}
	if _, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Tool: "nope"}); err == nil ||
		!strings.Contains(err.Error(), "not configured") {
		t.Fatalf("unknown tool error = %v", err)
	}
	if _, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Prompt: "--help"}); err == nil ||
		!strings.Contains(err.Error(), "read it as a flag") {
		t.Fatalf("flag-like prompt error = %v", err)
	}
	// A tool that takes its prompt behind a flag can carry one safely, and a
	// bullet list is an ordinary way to write a task.
	if _, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Tool: "flagged", Prompt: "- do the thing"}); err != nil {
		t.Fatalf("a flagged tool should accept a prompt starting with a dash: %v", err)
	}
	missing := "missing-group"
	if _, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Group: &missing}); err == nil ||
		!strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("unknown group error = %v", err)
	}
}

func TestSessionsListCoversAgentsOnlyAndMarksTheCaller(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	if _, err := h.terminals.Create(h.caller.ID, CreateTerminalOptions{}); err != nil {
		t.Fatalf("create terminal: %v", err)
	}
	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	listed, err := h.sessions.List(h.caller.ID, ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed.Sessions) != 2 || listed.Matched != 2 || listed.Truncated {
		t.Fatalf("list should hold the caller and the new agent only, got %+v", listed)
	}
	seen := map[string]Session{}
	for _, sess := range listed.Sessions {
		seen[sess.ID] = sess
	}
	if !seen[h.caller.ID].Self || seen[created.ID].Self {
		t.Fatalf("self marking = %+v", listed)
	}
	if !seen[created.ID].Running || seen[created.ID].Name != "worker" {
		t.Fatalf("listed spawn = %+v", seen[created.ID])
	}
}

func TestSessionsSendAndReadReachTheTargetPane(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sent, err := h.sessions.Send(h.caller.ID, created.ID, "rebase on main", "", false)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if sent.MessageID == 0 || sent.QueuePosition != 1 {
		t.Fatalf("send result = %+v", sent)
	}
	// The message is queued, not typed: nothing reaches the pane until a
	// running manager decides the target is at rest.
	screen, err := h.sessions.Read(h.caller.ID, created.ID, "")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if strings.Contains(screen.Output, "rebase on main") {
		t.Fatalf("send must not type into the pane itself, got %q", screen.Output)
	}
	state, err := h.sessions.MessageStatus(h.caller.ID, sent.MessageID)
	if err != nil {
		t.Fatalf("MessageStatus: %v", err)
	}
	if state.State != "queued" {
		t.Fatalf("message state = %+v", state)
	}
	if _, err := h.sessions.Send(h.caller.ID, created.ID, "rebase on main", "", false); err == nil {
		t.Fatal("an identical message should be refused as a duplicate")
	}
	if _, err := h.sessions.Send(h.caller.ID, h.caller.ID, "talking to myself", "", false); err == nil {
		t.Fatal("a session should not message itself")
	}

	if _, err := h.sessions.Send(h.caller.ID, created.ID, "   ", "", false); err == nil {
		t.Fatal("an empty message should be refused")
	}
	terminal, err := h.terminals.Create(h.caller.ID, CreateTerminalOptions{})
	if err != nil {
		t.Fatalf("create terminal: %v", err)
	}
	if _, err := h.sessions.Send(h.caller.ID, terminal.ID, "ls", "", false); err == nil ||
		!strings.Contains(err.Error(), "terminal, not an agent") {
		t.Fatalf("sending to a terminal error = %v", err)
	}
}

func TestSessionsKillKeepsTheScreenAndReviveBringsItBack(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "worker", Prompt: "hold the line"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	waitForSessionOutput(t, h.sessions, h.caller.ID, created.ID, "hold the line")

	killed, err := h.sessions.Kill(h.caller.ID, created.ID)
	if err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if killed.Running || killed.Status != status.Dead {
		t.Fatalf("killed session = %+v", killed)
	}
	if h.driver.Exists(created.ID) {
		t.Fatal("killed session still has a pane")
	}
	screen, err := h.sessions.Read(h.caller.ID, created.ID, "")
	if err != nil {
		t.Fatalf("Read after kill: %v", err)
	}
	if !strings.Contains(screen.Output, "hold the line") {
		t.Fatalf("a killed session should keep its last screen, got %q", screen.Output)
	}

	revived, err := h.sessions.Revive(h.caller.ID, created.ID)
	if err != nil {
		t.Fatalf("Revive: %v", err)
	}
	if !revived.Running || !h.driver.Exists(created.ID) {
		t.Fatalf("revived session = %+v", revived)
	}
	if row, err := h.store.Get(created.ID); err != nil {
		t.Fatalf("Get after revive: %v", err)
	} else if !row.AgentLaunchedAt.After(row.CreatedAt) {
		t.Fatalf("revive should stamp the launch, got launched_at %v for a row created %v", row.AgentLaunchedAt, row.CreatedAt)
	}
	if _, err := h.sessions.Revive(h.caller.ID, created.ID); err == nil ||
		!strings.Contains(err.Error(), "still running") {
		t.Fatalf("reviving a live session error = %v", err)
	}
	if _, err := h.sessions.Kill(h.caller.ID, h.caller.ID); err == nil {
		t.Fatal("a session must not kill itself")
	}
}

func TestSessionsArchiveHidesAndRestores(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	archived, err := h.sessions.Archive(h.caller.ID, created.ID, true)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	// Archiving ends the agent: the row is what is kept, not the process.
	// A non-destructive archive leaked one process per filed-away spawn,
	// because filing a finished spawn away is what an agent is told to do
	// with it.
	if !archived.Archived || archived.Running {
		t.Fatalf("archiving must stop the pane: %+v", archived)
	}
	if archived.Status != status.Dead {
		t.Fatalf("archived session status = %q, want %q", archived.Status, status.Dead)
	}
	if h.driver.Exists(created.ID) {
		t.Fatal("archiving left the pane running")
	}
	stored, err := h.store.Get(created.ID)
	if err != nil || !stored.Archived {
		t.Fatalf("stored archived = %+v err=%v", stored, err)
	}
	if stored.Status != status.Dead {
		t.Fatalf("stored status = %q, want %q", stored.Status, status.Dead)
	}
	restored, err := h.sessions.Archive(h.caller.ID, created.ID, false)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if restored.Archived {
		t.Fatalf("restored session = %+v", restored)
	}
	if _, err := h.sessions.Archive(h.caller.ID, h.caller.ID, true); err == nil {
		t.Fatal("a session must not archive itself")
	}
}

func TestSessionGroupsListAndCreate(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	created, err := h.sessions.CreateGroup(h.caller.ID, "backend/payments", h.caller.Cwd)
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if created.Path != "backend/payments" || !sameTerminalPath(created.Directory, h.caller.Cwd) {
		t.Fatalf("created group = %+v", created)
	}
	if _, err := h.sessions.CreateGroup(h.caller.ID, "backend/payments", ""); err == nil ||
		!strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate group error = %v", err)
	}
	if _, err := h.sessions.CreateGroup(h.caller.ID, "unknown/child", ""); err == nil ||
		!strings.Contains(err.Error(), "parent group") {
		t.Fatalf("orphan group error = %v", err)
	}
	if _, err := h.sessions.CreateGroup(h.caller.ID, "  ", ""); err == nil {
		t.Fatal("an empty group path should be refused")
	}

	unnested := false
	spawned, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "worker", Group: &created.Path, Nest: &unnested})
	if err != nil {
		t.Fatalf("Create in new group: %v", err)
	}
	if spawned.Group != "backend/payments" {
		t.Fatalf("spawn group = %q", spawned.Group)
	}
	groups, err := h.sessions.Groups(h.caller.ID)
	if err != nil {
		t.Fatalf("Groups: %v", err)
	}
	counts := map[string]int{}
	for _, group := range groups {
		counts[group.Path] = group.Sessions
	}
	if counts["backend"] != 1 || counts["backend/payments"] != 1 {
		t.Fatalf("group counts = %+v", counts)
	}
}

func TestSendAndWaitRefuseATargetTheManagerNoLongerPolls(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := h.sessions.Archive(h.caller.ID, created.ID, true); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	// The refusal has to name the archive rather than the dead pane: an
	// archived row is skipped by the poller, so reviving it would put a
	// process back and still leave the message queueing forever.
	if _, err := h.sessions.Send(h.caller.ID, created.ID, "rebase on main", "", false); err == nil ||
		!strings.Contains(err.Error(), "archived") {
		t.Fatalf("send to an archived session = %v", err)
	}
	if _, err := h.sessions.Wait(context.Background(), h.caller.ID, WaitOptions{SessionIDs: []string{created.ID}, Timeout: time.Second}); err == nil ||
		!strings.Contains(err.Error(), "archived") {
		t.Fatalf("wait on an archived session = %v", err)
	}
}

func TestSendRefusesAToolTheManagerCannotReadReadinessFrom(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Tool: "blind", Name: "worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := h.sessions.Send(h.caller.ID, created.ID, "rebase on main", "", false); err == nil ||
		!strings.Contains(err.Error(), "activity_cutoff") {
		t.Fatalf("send to a tool with no readiness marker = %v", err)
	}
}

// Whether a manager is awake is read off a heartbeat only the poller
// writes. A value that is not a timestamp means something else wrote that
// row, and reporting it as "no manager" would send the caller after the
// wrong problem.
func TestAnUnreadableHeartbeatIsReportedRatherThanReadAsAClosedManager(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	worker, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := h.store.SetSetting(store.PollerHeartbeatKey, "just now"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if _, err := h.sessions.Send(h.caller.ID, worker.ID, "rebase on main", "", false); err == nil ||
		!strings.Contains(err.Error(), "poller heartbeat") {
		t.Fatalf("Send with a corrupt heartbeat = %v", err)
	}
}

// A sender follows its own message instead of reading the recipient's
// screen, and the recipient answering is the acknowledgement. Both
// transitions belong to this front; the store tests cover the rows they
// write, and nothing follows one message across the two.
func TestASenderSeesItsMessageDeliveredThenAnswered(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	worker, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sent, err := h.sessions.Send(h.caller.ID, worker.ID, "rebase on main", "", false)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	// The queued row is the whole contract with the manager: it names the
	// sender in the envelope it types and claims the row before typing.
	head, queued, err := h.store.HeadMessage(worker.ID)
	if err != nil || !queued {
		t.Fatalf("HeadMessage: %v, queued=%v", err, queued)
	}
	if head.ID != sent.MessageID || head.SenderID != h.caller.ID || head.SenderName != h.caller.Name ||
		head.Body != "rebase on main" || !head.ClaimedAt.IsZero() {
		t.Fatalf("queued row = %+v, caller = %+v", head, h.caller)
	}

	// The manager's poller owns delivery; these are the two writes it makes
	// once it finds the target at rest.
	claimed, err := h.store.ClaimMessage(sent.MessageID, time.Now())
	if err != nil || !claimed {
		t.Fatalf("ClaimMessage: %v, claimed=%v", err, claimed)
	}
	if err := h.store.MarkDelivered(sent.MessageID, time.Now()); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}

	state, err := h.sessions.MessageStatus(h.caller.ID, sent.MessageID)
	if err != nil {
		t.Fatalf("MessageStatus: %v", err)
	}
	if state.State != "delivered" || state.DeliveredAt == "" || state.SessionID != worker.ID {
		t.Fatalf("delivered state = %+v", state)
	}

	if _, err := h.sessions.Send(worker.ID, h.caller.ID, "rebased, tests pass", "", false); err != nil {
		t.Fatalf("reply: %v", err)
	}
	state, err = h.sessions.MessageStatus(h.caller.ID, sent.MessageID)
	if err != nil {
		t.Fatalf("MessageStatus after the reply: %v", err)
	}
	if state.State != "answered" {
		t.Fatalf("a reply did not acknowledge the message it answers: %+v", state)
	}
	// Only the sender may follow it; another session asking is told so
	// rather than shown someone else's traffic.
	if _, err := h.sessions.MessageStatus(worker.ID, sent.MessageID); err == nil ||
		!strings.Contains(err.Error(), "was not sent by this session") {
		t.Fatalf("reading another session's message = %v", err)
	}
}

// A coordinator audits what reached its children, including a message from
// another session it did not send. A session that neither sent the message
// nor spawned its recipient is still refused.
func TestASpawnerSeesMessagesToItsChildButAStrangerDoesNot(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	child, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "child"})
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}
	notNested := false
	sender, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "sender", Nest: &notNested})
	if err != nil {
		t.Fatalf("Create sender: %v", err)
	}
	stranger, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "stranger", Nest: &notNested})
	if err != nil {
		t.Fatalf("Create stranger: %v", err)
	}
	sent, err := h.sessions.Send(sender.ID, child.ID, "stop querying GCP, use Axiom", "", false)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	state, err := h.sessions.MessageStatus(h.caller.ID, sent.MessageID)
	if err != nil {
		t.Fatalf("the child's spawner could not read a message to it: %v", err)
	}
	if state.SessionID != child.ID || state.State != "queued" || state.Body != "stop querying GCP, use Axiom" {
		t.Fatalf("spawner's view = %+v", state)
	}
	if _, err := h.sessions.MessageStatus(stranger.ID, sent.MessageID); err == nil ||
		!strings.Contains(err.Error(), "was not sent by this session") {
		t.Fatalf("a stranger reading a message to someone else's child = %v", err)
	}
}

// A message the manager could not type is retired so nothing retypes it,
// which leaves it looking exactly like a delivered one in the queue. The
// sender has no other way to find out it never landed.
func TestASenderIsToldWhenItsMessageWasDropped(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	worker, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sent, err := h.sessions.Send(h.caller.ID, worker.ID, "rebase on main", "", false)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := h.store.MarkDropped(sent.MessageID, time.Now()); err != nil {
		t.Fatalf("MarkDropped: %v", err)
	}

	state, err := h.sessions.MessageStatus(h.caller.ID, sent.MessageID)
	if err != nil {
		t.Fatalf("MessageStatus: %v", err)
	}
	if state.State != "dropped" || state.DeliveredAt != "" {
		t.Fatalf("dropped message reported as %+v", state)
	}
	if !strings.Contains(state.Reason, "send it again") {
		t.Fatalf("a dropped message does not say what to do about it: %+v", state)
	}
	// A reply must not turn a message that never arrived into an answered one.
	if _, err := h.sessions.Send(worker.ID, h.caller.ID, "rebased, tests pass", "", false); err != nil {
		t.Fatalf("reply: %v", err)
	}
	state, err = h.sessions.MessageStatus(h.caller.ID, sent.MessageID)
	if err != nil {
		t.Fatalf("MessageStatus after the reply: %v", err)
	}
	if state.State != "dropped" {
		t.Fatalf("a dropped message was acknowledged by an unrelated reply: %+v", state)
	}
}

// A draft at the recipient's prompt holds the queue for as long as it sits
// there, so the sender is told that is what it is waiting on, and told with
// the same read the poller makes.
func TestASenderSeesAMessageHeldByADraftAtThePrompt(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	worker, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Tool: "resting", Name: "worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	waitForSessionOutput(t, h.sessions, h.caller.ID, worker.ID, "❯")
	if err := h.driver.Paste(worker.ID, "USERTEXT-in-progress"); err != nil {
		t.Fatalf("Paste: %v", err)
	}
	waitForSessionOutput(t, h.sessions, h.caller.ID, worker.ID, "USERTEXT-in-progress")
	sent, err := h.sessions.Send(h.caller.ID, worker.ID, "rebase on main", "", false)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	state, err := h.sessions.MessageStatus(h.caller.ID, sent.MessageID)
	if err != nil {
		t.Fatalf("MessageStatus: %v", err)
	}
	if state.State != "held" || !strings.Contains(state.Reason, "text written") || !strings.Contains(state.Reason, worker.ID) {
		t.Fatalf("a message behind a draft reads as %+v", state)
	}
}

// A sender is told a hold only where the manager keeps one. The gate is the
// recipient tool's own rules, read off its current screen: a dialog holds
// the queue, because text typed onto one picks an option, and a prompt at
// rest does not, whatever the stored status says about it.
func TestASenderSeesAMessageHeldByARecipientOnADialog(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	worker, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Tool: "dialog", Name: "worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sent, err := h.sessions.Send(h.caller.ID, worker.ID, "rebase on main", "", false)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitForSessionOutput(t, h.sessions, h.caller.ID, worker.ID, "Enter to confirm")

	state, err := h.sessions.MessageStatus(h.caller.ID, sent.MessageID)
	if err != nil {
		t.Fatalf("MessageStatus: %v", err)
	}
	if state.State != "held" {
		t.Fatalf("a message behind a dialog reads as %+v", state)
	}
	if !strings.Contains(state.Reason, worker.ID) || !strings.Contains(state.Reason, "dialog") {
		t.Fatalf("the hold does not say why: %+v", state)
	}

	// A session whose screen shows no dialog is delivered to, so its sender
	// hears the truth: ordinary queued, whatever waiting the row carries.
	resting, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Tool: "resting", Name: "resting-worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	restingSend, err := h.sessions.Send(h.caller.ID, resting.ID, "rebase on main", "", false)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitForSessionOutput(t, h.sessions, h.caller.ID, resting.ID, "❯")
	if err := h.store.UpdateStatus(resting.ID, status.Waiting); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	state, err = h.sessions.MessageStatus(h.caller.ID, restingSend.MessageID)
	if err != nil {
		t.Fatalf("MessageStatus: %v", err)
	}
	if state.State != "queued" || state.Reason != "" {
		t.Fatalf("a recipient the manager will type into was reported as holding its queue: %+v", state)
	}
}

// A recipient can leave the manager's reach after the send: archived rows
// are skipped by the poll, and a dead session has no pane to type into. The
// queue then never moves, and the sender is the one who has to be told.
func TestASenderIsToldWhenItsRecipientLeftTheManagersReach(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	worker, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Tool: "resting", Name: "worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sent, err := h.sessions.Send(h.caller.ID, worker.ID, "rebase on main", "", false)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := h.sessions.Archive(h.caller.ID, worker.ID, true); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	state, err := h.sessions.MessageStatus(h.caller.ID, sent.MessageID)
	if err != nil {
		t.Fatalf("MessageStatus: %v", err)
	}
	if state.State != "held" || !strings.Contains(state.Reason, "archived") ||
		!strings.Contains(state.Reason, h.sessions.words.Restore) {
		t.Fatalf("a message to an archived session reads as %+v", state)
	}

	if _, err := h.sessions.Archive(h.caller.ID, worker.ID, false); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := h.sessions.Kill(h.caller.ID, worker.ID); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	state, err = h.sessions.MessageStatus(h.caller.ID, sent.MessageID)
	if err != nil {
		t.Fatalf("MessageStatus: %v", err)
	}
	if state.State != "held" || !strings.Contains(state.Reason, "not running") ||
		!strings.Contains(state.Reason, h.sessions.words.Revive) {
		t.Fatalf("a message to a dead session reads as %+v", state)
	}
}

// An errored session is running, unarchived and configured, so every other
// held case passes it by, while the poller types into resting sessions only.
// A coordinator polling a handoff has to be able to tell that apart from a
// recipient that is merely slow.
func TestASenderIsToldWhenItsRecipientErrored(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	worker, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Tool: "resting", Name: "worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sent, err := h.sessions.Send(h.caller.ID, worker.ID, "rebase on main", "", false)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := h.store.UpdateStatus(worker.ID, status.Errored); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	state, err := h.sessions.MessageStatus(h.caller.ID, sent.MessageID)
	if err != nil {
		t.Fatalf("MessageStatus: %v", err)
	}
	if state.State != "held" || !strings.Contains(state.Reason, "errored") ||
		!strings.Contains(state.Reason, h.sessions.words.Read) {
		t.Fatalf("a message to an errored session reads as %+v", state)
	}
}

// A tool block can be deleted after a message was queued for a session
// running that tool, which leaves the poller unable to read readiness.
// The message stays queued: the hold lifts if the tool block returns.
func TestASenderIsToldWhenTheRecipientsToolLeftTheConfig(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	worker, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Tool: "resting", Name: "worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sent, err := h.sessions.Send(h.caller.ID, worker.ID, "rebase on main", "", false)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	runtime, err := h.sessions.open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer runtime.store.Close()
	delete(runtime.cfg.Tools, "resting")

	reason, err := runtime.heldReason(worker.ID)
	if err != nil {
		t.Fatalf("heldReason: %v", err)
	}
	if !strings.Contains(reason, "activity_cutoff") || !strings.Contains(reason, worker.ID) {
		t.Fatalf("a message whose tool left the config reads as %q", reason)
	}
	if sent.MessageID == 0 {
		t.Fatalf("Send returned no message id")
	}
}

// The queue and rate caps count messages, so without a size cap one message
// is an unbounded paste into another agent's prompt.
func TestSendRefusesAMessageTooLargeToPasteIntoAPrompt(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	worker, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := h.sessions.Send(h.caller.ID, worker.ID, strings.Repeat("x", maxMessageBytes+1), "", false); err == nil ||
		!strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("an oversized message was answered with %v", err)
	}
	if queued, err := h.store.QueuedCount(worker.ID); err != nil || queued != 0 {
		t.Fatalf("queued = %d, %v: the refusal still cost the recipient a slot", queued, err)
	}
	if _, err := h.sessions.Send(h.caller.ID, worker.ID, strings.Repeat("x", maxMessageBytes), "", false); err != nil {
		t.Fatalf("a message at the limit was refused: %v", err)
	}
}

// A fleet that opened a group for its work has to be able to close it, and
// the sessions still filed there are not what it asked to remove.
func TestDeleteGroupMovesItsSessionsToTheRoot(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	if _, err := h.sessions.CreateGroup(h.caller.ID, "fleet", ""); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	group := "fleet"
	unnested := false
	worker, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Tool: "resting", Name: "worker", Group: &group, Nest: &unnested})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if worker.Group != "fleet" {
		t.Fatalf("session landed in %q, not the group it was given", worker.Group)
	}

	removal, err := h.sessions.DeleteGroup(h.caller.ID, "fleet")
	if err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}
	if len(removal.Removed) != 1 || removal.Removed[0] != "fleet" {
		t.Fatalf("removed = %v", removal.Removed)
	}
	if len(removal.Moved) != 1 || removal.Moved[0] != worker.ID {
		t.Fatalf("moved = %v, want the session that was filed there", removal.Moved)
	}

	// The session is the point: it keeps running, at the root.
	after, err := h.store.Get(worker.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if after.Group != "" {
		t.Fatalf("session sits in %q rather than the root", after.Group)
	}
	if !h.driver.Exists(worker.ID) {
		t.Fatal("deleting a group stopped the agent running in it")
	}
	groups, err := h.sessions.Groups(h.caller.ID)
	if err != nil {
		t.Fatalf("Groups: %v", err)
	}
	for _, g := range groups {
		if g.Path == "fleet" {
			t.Fatal("the group survived its deletion")
		}
	}
	if _, err := h.sessions.DeleteGroup(h.caller.ID, "fleet"); err == nil {
		t.Fatal("deleting a group that does not exist was accepted")
	}
}

func TestDeleteGroupTakesItsSubtreeAndKeepsNesting(t *testing.T) {
	h := newSessionHarness(t)
	if _, err := h.sessions.CreateGroup(h.caller.ID, "fleet", ""); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if _, err := h.sessions.CreateGroup(h.caller.ID, "fleet/backend", ""); err != nil {
		t.Fatalf("CreateGroup nested: %v", err)
	}
	group := "fleet/backend"
	unnested := false
	worker, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Tool: "resting", Name: "worker", Group: &group, Nest: &unnested})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	shell := store.Session{
		ID: "aaaa1111", Name: "sh-worker", Tool: "resting",
		Group: "fleet/backend", Status: status.Idle, ParentID: worker.ID,
	}
	if err := h.store.CreateSession(shell); err != nil {
		t.Fatalf("nest shell: %v", err)
	}

	removal, err := h.sessions.DeleteGroup(h.caller.ID, "fleet")
	if err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}
	if len(removal.Removed) != 2 {
		t.Fatalf("removed = %v, want the group and its child", removal.Removed)
	}
	if len(removal.Moved) != 2 {
		t.Fatalf("moved = %v, want both sessions from the nested group", removal.Moved)
	}
	after, err := h.store.Get(shell.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if after.Group != "" {
		t.Fatalf("nested session sits in %q rather than the root", after.Group)
	}
	if after.ParentID != worker.ID {
		t.Fatalf("moving the subtree unhooked the terminal from its agent: parent %q", after.ParentID)
	}
}

// A spawn nests under the session that asked for it.
func TestASpawnNestsUnderItsCaller(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	row, err := h.store.Get(created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.ParentID != h.caller.ID {
		t.Fatalf("parent = %q, want the caller %q", row.ParentID, h.caller.ID)
	}
}

// The store carries one level of parenthood, so a grandchild is filed against
// the caller's own parent, while spawned_by still names the caller.
func TestAGrandchildIsFiledUnderTheRoot(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	child, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "child"})
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}
	grandchild, err := h.sessions.Create(child.ID, CreateSessionOptions{Name: "grandchild"})
	if err != nil {
		t.Fatalf("Create grandchild: %v", err)
	}
	row, err := h.store.Get(grandchild.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.ParentID != h.caller.ID {
		t.Fatalf("parent = %q, want the root %q rather than the child %q",
			row.ParentID, h.caller.ID, child.ID)
	}
	if row.SpawnedBy != child.ID {
		t.Fatalf("spawned_by = %q, want the child %q that asked", row.SpawnedBy, child.ID)
	}
}

// A session launched on a chosen model records it, comes back on it, and
// reports it — the three places the answer has to survive for the choice to
// mean anything after the launch.
func TestCreateOnAChosenModelKeepsItThroughRevive(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{
		Name:  "model-worker",
		Model: "opus",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Model != "opus" {
		t.Errorf("Model = %q, want the model asked for", created.Model)
	}
	row, err := h.store.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Model != "opus" {
		t.Errorf("row Model = %q, want it recorded", row.Model)
	}
	if text := FormatSession(created); !strings.Contains(text, "opus") {
		t.Errorf("the formatted session hides the model: %s", text)
	}

	// Kill and revive: the relaunch has to ask for the same model, or the run
	// silently becomes a different one while the row still says otherwise.
	if _, err := h.sessions.Kill(h.caller.ID, created.ID); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	revived, err := h.sessions.Revive(h.caller.ID, created.ID)
	if err != nil {
		t.Fatalf("Revive: %v", err)
	}
	if revived.Model != "opus" {
		t.Errorf("revived Model = %q, want it preserved", revived.Model)
	}
}

// The default is still the default: no model asked for, nothing recorded, and
// nothing added to the command line.
func TestCreateWithoutAModelStaysOnTheToolsDefault(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "default-worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Model != "" {
		t.Errorf("Model = %q, want empty", created.Model)
	}
	// The chip rides the tool, so that is what is checked: every formatted
	// session carries an "(id ...)" and matching on a bare paren finds it.
	if text := FormatSession(created); strings.Contains(text, "running echoer (") {
		t.Errorf("a default session wears a model chip: %s", text)
	}
}

// The echoer test tool has no model flag, which is what most of the configured
// CLIs look like until somebody reads their --help. Asking one for a model is
// refused rather than launched on its default.
func TestCreateRefusesAModelATToolCannotBeTold(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	_, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{
		Tool: "no-model-tool", Name: "refused", Model: "opus",
	})
	if err == nil {
		t.Fatal("a tool with no model flag accepted a model")
	}
	if !strings.Contains(err.Error(), "no model flag") {
		t.Fatalf("err = %v, want it to say the CLI cannot be told", err)
	}
	sessions, err := h.store.ListSessions(true)
	if err != nil {
		t.Fatal(err)
	}
	for _, sess := range sessions {
		if sess.Name == "refused" {
			t.Fatal("the refused session was created anyway")
		}
	}
}

// Switching a session's account re-points the row and brings the session
// back on the conversation it holds with the new token; a dead one is only
// re-pointed. A session cannot switch itself, and a tool with nowhere to
// take a token refuses.
func TestSwitchAccountRestartsALiveSessionOnTheNewAccount(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "worker", Tool: "claude"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	switched, err := h.sessions.SwitchAccount(h.caller.ID, created.ID, "alice1")
	if err != nil {
		t.Fatalf("SwitchAccount: %v", err)
	}
	if switched.Account != "ALICE1" || !switched.Running || !h.driver.Exists(created.ID) {
		t.Fatalf("switched = %+v", switched)
	}
	if row, _ := h.store.Get(created.ID); row.Account != "ALICE1" {
		t.Errorf("row Account = %q", row.Account)
	}
	if text := FormatSession(switched); !strings.Contains(text, "as ALICE1") {
		t.Errorf("the formatted session hides the account: %s", text)
	}
	if _, err := h.sessions.Kill(h.caller.ID, created.ID); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	back, err := h.sessions.SwitchAccount(h.caller.ID, created.ID, "")
	if err != nil {
		t.Fatalf("SwitchAccount back: %v", err)
	}
	if back.Account != "" || back.Running {
		t.Errorf("a dead session should only be re-pointed, got %+v", back)
	}
	if _, err := h.sessions.SwitchAccount(h.caller.ID, h.caller.ID, "ALICE1"); err == nil {
		t.Error("a session switched its own account")
	}
	plain, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "plain"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := h.sessions.SwitchAccount(h.caller.ID, plain.ID, "ALICE1"); err == nil {
		t.Error("a tool with no account_env took an account")
	}

	// A pane the manager adopted rather than started: applying an account
	// means ending the session and launching it again, and that process is
	// not the manager's to end. Re-pointing it would write an account no
	// launch of ours ever reads.
	adopted := store.Session{ID: uuid.NewString()[:8], Name: "borrowed", Tool: "claude",
		Cwd: h.caller.Cwd, Status: status.Idle, TmuxSocket: h.driver.SocketName(), TmuxPaneID: "%99"}
	if err := h.store.CreateSession(adopted); err != nil {
		t.Fatalf("create adopted row: %v", err)
	}
	if _, err := h.sessions.SwitchAccount(h.caller.ID, adopted.ID, "ALICE1"); err == nil {
		t.Error("a pane the manager did not start took an account")
	}
	if row, _ := h.store.Get(adopted.ID); row.Account != "" {
		t.Errorf("the refused row was re-pointed anyway: %q", row.Account)
	}
}

// The trust dialog keys on the directory the CLI starts in, so a spawn outside
// the caller's own tree opens the pane where the caller started and hands the
// requested directory to the agent instead. The row still records the requested
// one: it is what work and git discovery read.
func TestCreateOutsideTheCallersTreeLaunchesWhereTheCallerDid(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	elsewhere := t.TempDir()
	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{
		Name:      "worktree-child",
		Prompt:    "fix the retry backoff",
		Directory: elsewhere,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !sameTerminalPath(created.Directory, elsewhere) {
		t.Fatalf("created directory = %q, want the requested %q", created.Directory, elsewhere)
	}
	stored, err := h.store.Get(created.ID)
	if err != nil {
		t.Fatalf("stored session: %v", err)
	}
	if !sameTerminalPath(stored.Cwd, elsewhere) {
		t.Fatalf("stored cwd = %q, want the requested %q", stored.Cwd, elsewhere)
	}
	pane, err := h.driver.PaneCurrentPath(created.ID)
	if err != nil {
		t.Fatalf("pane path: %v", err)
	}
	if !sameTerminalPath(pane, h.caller.Cwd) {
		t.Fatalf("pane opened in %q, want the caller's launch directory %q", pane, h.caller.Cwd)
	}
	// echo prints the command line, so the pane proves the agent was told to
	// change into the directory it was asked to work in. The pane wraps at its
	// own width, so the path is matched against the unwrapped text.
	screen := waitForSessionOutput(t, h.sessions, h.caller.ID, created.ID, launch.WorkdirDirectivePrefix)
	unwrapped := strings.ReplaceAll(screen.Output, "\n", "")
	if !strings.Contains(unwrapped, "cd '"+elsewhere+"'") {
		t.Fatalf("the agent was not told to change into %q: %q", elsewhere, screen.Output)
	}
}

// A spawn inside the caller's tree inherits its trust, so nothing is diverted
// and no directive is added.
func TestCreateInsideTheCallersTreeOpensThereUndirected(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	nested := filepath.Join(h.caller.Cwd, "packages", "api")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{
		Prompt:    "fix the retry backoff",
		Directory: nested,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	pane, err := h.driver.PaneCurrentPath(created.ID)
	if err != nil {
		t.Fatalf("pane path: %v", err)
	}
	if !sameTerminalPath(pane, nested) {
		t.Fatalf("pane opened in %q, want the requested %q", pane, nested)
	}
	screen := waitForSessionOutput(t, h.sessions, h.caller.ID, created.ID, "fix the retry backoff")
	if strings.Contains(screen.Output, launch.WorkdirDirectivePrefix) {
		t.Fatalf("an undiverted spawn was told to change directory: %q", screen.Output)
	}
}

func TestLaunchDirectoryFallsBackToTheRequestedDirectory(t *testing.T) {
	caller := t.TempDir()
	outside := t.TempDir()
	if got := launchDirectory(caller, outside); got != caller {
		t.Fatalf("a spawn outside the caller's tree = %q, want %q", got, caller)
	}
	if got := launchDirectory(caller, caller); got != caller {
		t.Fatalf("the caller's own directory = %q, want %q", got, caller)
	}
	if got := launchDirectory("", outside); got != outside {
		t.Fatalf("a caller with no directory = %q, want %q", got, outside)
	}
	// A caller whose own directory has gone vouches for nothing, and opening
	// no pane at all would be worse than the dialog this avoids.
	if got := launchDirectory(filepath.Join(caller, "gone"), outside); got != outside {
		t.Fatalf("a caller directory that does not exist = %q, want %q", got, outside)
	}
}

// The unfiltered list is the whole table, and on a board that has run for
// months that is mostly archived and dead rows nobody asked for. Every
// narrowing below is a filter an agent had no way to express before.
func TestSessionsListNarrowsToWhatTheCallerAskedFor(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	detached := false
	for _, name := range []string{"child-one", "child-two"} {
		if _, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: name}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	stranger, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "somebody-elses", Nest: &detached})
	if err != nil {
		t.Fatalf("create detached: %v", err)
	}
	filed, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "finished-work"})
	if err != nil {
		t.Fatalf("create archived: %v", err)
	}
	if _, err := h.sessions.Archive(h.caller.ID, filed.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}

	// Archived rows are the bulk of a long-lived board and are left out
	// unless asked for; the caller, its two live children and the detached
	// session are what is left.
	all, err := h.sessions.List(h.caller.ID, ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if all.Matched != 4 {
		t.Fatalf("unarchived list = %d rows, want 4: %+v", all.Matched, all.Sessions)
	}
	withFiled, err := h.sessions.List(h.caller.ID, ListOptions{IncludeArchived: true})
	if err != nil {
		t.Fatalf("List archived: %v", err)
	}
	if withFiled.Matched != 5 {
		t.Fatalf("archived list = %d rows, want 5: %+v", withFiled.Matched, withFiled.Sessions)
	}

	// "me" is the whole point: a parent knows its own id and should not have
	// to spell it out to read its own fan-out.
	mine, err := h.sessions.List(h.caller.ID, ListOptions{Parent: SelfParent})
	if err != nil {
		t.Fatalf("List children: %v", err)
	}
	names := map[string]bool{}
	for _, sess := range mine.Sessions {
		names[sess.Name] = true
		if sess.ParentID != h.caller.ID {
			t.Fatalf("parent filter returned %+v", sess)
		}
	}
	if len(mine.Sessions) != 2 || !names["child-one"] || !names["child-two"] {
		t.Fatalf("parent me = %+v, want the two children only", mine.Sessions)
	}
	if byID, err := h.sessions.List(h.caller.ID, ListOptions{Parent: h.caller.ID}); err != nil ||
		len(byID.Sessions) != len(mine.Sessions) {
		t.Fatalf("spelling the id out = %+v, err %v", byID, err)
	}
	if none, err := h.sessions.List(h.caller.ID, ListOptions{Parent: stranger.ID}); err != nil || none.Matched != 0 {
		t.Fatalf("a session with no children = %+v, err %v", none, err)
	}

	// A limit that hides rows says so, because a manager acting on 2 of its
	// 4 sessions without knowing it is worse off than one that paid for the
	// whole list.
	capped, err := h.sessions.List(h.caller.ID, ListOptions{Limit: 2})
	if err != nil {
		t.Fatalf("List limited: %v", err)
	}
	if len(capped.Sessions) != 2 || capped.Matched != 4 || !capped.Truncated {
		t.Fatalf("limited list = %+v", capped)
	}
	if text := FormatSessionList(capped); !strings.Contains(text, "2 of 4") {
		t.Fatalf("the rendering hides the truncation: %q", text)
	}

	idle, err := h.sessions.List(h.caller.ID, ListOptions{Status: []string{status.Idle}})
	if err != nil {
		t.Fatalf("List idle: %v", err)
	}
	for _, sess := range idle.Sessions {
		if sess.Status != status.Idle {
			t.Fatalf("status filter returned %+v", sess)
		}
	}
	rest, err := h.sessions.List(h.caller.ID, ListOptions{Status: []string{status.Starting}})
	if err != nil {
		t.Fatalf("List starting: %v", err)
	}
	if idle.Matched+rest.Matched != all.Matched {
		t.Fatalf("the two states do not partition the list: idle %d, starting %d, all %d", idle.Matched, rest.Matched, all.Matched)
	}
	// A set is a set: both states together are the whole unarchived list.
	if both, err := h.sessions.List(h.caller.ID, ListOptions{Status: []string{status.Idle, status.Starting}}); err != nil ||
		both.Matched != all.Matched {
		t.Fatalf("two states = %+v, err %v", both, err)
	}

	// A mistyped state or an unservable limit is refused rather than
	// silently answered with an empty board, which reads as a quiet fleet.
	if _, err := h.sessions.List(h.caller.ID, ListOptions{Status: []string{"busy"}}); err == nil ||
		!strings.Contains(err.Error(), "unknown state") {
		t.Fatalf("an unknown state = %v", err)
	}
	if _, err := h.sessions.List(h.caller.ID, ListOptions{Limit: MaxSessionLimit + 1}); err == nil ||
		!strings.Contains(err.Error(), "out of range") {
		t.Fatalf("an over-large limit = %v", err)
	}
}

// Interrupt is refused where the tool has no keys to stop a turn with, since
// the message would then arrive late, which is what the sender asked to
// avoid. Where it is accepted, the mode is recorded on the message.
func TestInterruptIsRefusedWithoutInterruptKeysAndRecordedWithThem(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	plain, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "plain", Tool: "echoer"})
	if err != nil {
		t.Fatalf("Create plain: %v", err)
	}
	if _, err := h.sessions.Send(h.caller.ID, plain.ID, "stop", "", true); err == nil ||
		!strings.Contains(err.Error(), "no interrupt_keys") {
		t.Fatalf("interrupting a tool with no interrupt_keys = %v", err)
	}
	stoppable, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "stoppable", Tool: "stoppable"})
	if err != nil {
		t.Fatalf("Create stoppable: %v", err)
	}
	sent, err := h.sessions.Send(h.caller.ID, stoppable.ID, "stop querying GCP, use Axiom", "", true)
	if err != nil {
		t.Fatalf("Send with interrupt: %v", err)
	}
	if !sent.Interrupt || !strings.Contains(FormatSendResult(sent, stoppable.ID), "interrupted first") {
		t.Fatalf("send result does not say it interrupts: %+v", sent)
	}
	state, err := h.sessions.MessageStatus(h.caller.ID, sent.MessageID)
	if err != nil {
		t.Fatalf("MessageStatus: %v", err)
	}
	if !state.Interrupt || !strings.Contains(FormatMessageState(state), "sent with interrupt") {
		t.Fatalf("message_status does not show the interrupt mode: %+v", state)
	}
	plainSend, err := h.sessions.Send(h.caller.ID, stoppable.ID, "and then push", "", false)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if plainState, err := h.sessions.MessageStatus(h.caller.ID, plainSend.MessageID); err != nil || plainState.Interrupt {
		t.Fatalf("a plain send was recorded as interrupting: %+v, %v", plainState, err)
	}
}
