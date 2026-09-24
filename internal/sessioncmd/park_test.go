package sessioncmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/shirou/gopsutil/v4/process"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

func TestParkStopsEveryAgentAndUnparkBringsExactlyThoseBack(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	worker, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "worker", Prompt: "hold the line"})
	if err != nil {
		t.Fatalf("Create worker: %v", err)
	}
	waitForSessionOutput(t, h.sessions, h.caller.ID, worker.ID, "hold the line")
	terminal, err := h.terminals.Create(h.caller.ID, CreateTerminalOptions{})
	if err != nil {
		t.Fatalf("Create terminal: %v", err)
	}
	// A row killed on purpose before the park stays dead and outside the
	// set: unpark owes only what park stopped.
	earlier, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "earlier"})
	if err != nil {
		t.Fatalf("Create earlier: %v", err)
	}
	if _, err := h.sessions.Kill(h.caller.ID, earlier.ID, extension.KillByCLI); err != nil {
		t.Fatalf("Kill earlier: %v", err)
	}

	// An adopted pane: a session on the same server the manager did not
	// create, whose shell stands in for a Claude Code process by having a
	// session file written for its pid.
	adoptedDir := t.TempDir()
	paneID, panePID := newForeignPane(t, h.driver.SocketName())
	adopted := store.Session{ID: uuid.NewString()[:8], Name: "borrowed", Tool: "claude", Cwd: h.caller.Cwd, Status: status.Idle,
		TmuxSocket: h.driver.SocketName(), TmuxPaneID: paneID}
	if err := h.store.CreateSession(adopted); err != nil {
		t.Fatalf("create adopted row: %v", err)
	}
	h.sessions.claudeHome = writeClaudeSessionFile(t, panePID, "conv-1", adoptedDir)

	// A session mid-turn: its screen matches the tool's working rule, so
	// unpark has to tell it to carry on.
	busy, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "mid-turn", Tool: "busy"})
	if err != nil {
		t.Fatalf("Create busy: %v", err)
	}
	waitForSessionOutput(t, h.sessions, h.caller.ID, busy.ID, "Thinking about it")

	// Two gi_ sessions with no row: one running a configured tool, which
	// park can give a row to, and one running only a shell, which it cannot.
	sleeper := uuid.NewString()[:8]
	if err := h.driver.Create(sleeper, h.caller.Cwd, "sleep 30", nil, 80, 24); err != nil {
		t.Fatalf("create sleeper pane: %v", err)
	}
	t.Cleanup(func() { _ = h.driver.Kill(sleeper) })
	unknown := uuid.NewString()[:8]
	if err := h.driver.Create(unknown, h.caller.Cwd, "", nil, 80, 24); err != nil {
		t.Fatalf("create unknown pane: %v", err)
	}
	t.Cleanup(func() { _ = h.driver.Kill(unknown) })

	// The rehearsal names the same sessions and changes nothing.
	plan, err := h.sessions.Park(h.caller.ID, true)
	if err != nil {
		t.Fatalf("Park dry run: %v", err)
	}
	if !plan.DryRun || !plan.Self || plan.Terminals != 1 || plan.Promoted != 1 || plan.Recovered != 1 || plan.Owed != 4 {
		t.Fatalf("dry run = %+v", plan)
	}
	if ids := parkedIDs(plan); !reflect.DeepEqual(ids, sorted(worker.ID, adopted.ID, sleeper, busy.ID)) {
		t.Fatalf("dry run planned %v", ids)
	}
	if !reflect.DeepEqual(plan.Working, []string{busy.ID}) {
		t.Fatalf("dry run working = %v, want only the mid-turn session", plan.Working)
	}
	if ids, _ := h.store.Interrupted(); len(ids) != 0 {
		t.Fatalf("a dry run recorded interrupted %v", ids)
	}
	if !reflect.DeepEqual(plan.Orphans, []string{unknown}) || len(plan.Warnings) != 0 || len(plan.Errors) != 0 {
		t.Fatalf("dry run orphans=%v warnings=%v errors=%v", plan.Orphans, plan.Warnings, plan.Errors)
	}
	if !h.driver.Exists(worker.ID) || !foreignPaneExists(t, h.driver.SocketName(), paneID) {
		t.Fatal("a dry run stopped a session")
	}
	if row, _ := h.store.Get(adopted.ID); row.TmuxPaneID != paneID || row.AgentSessionID != "" {
		t.Fatalf("a dry run changed the adopted row: %+v", row)
	}
	if ids, _ := h.store.Parked(); len(ids) != 0 {
		t.Fatalf("a dry run recorded %v", ids)
	}

	parked, err := h.sessions.Park(h.caller.ID, false)
	if err != nil {
		t.Fatalf("Park: %v", err)
	}
	if ids := parkedIDs(parked); !reflect.DeepEqual(ids, sorted(worker.ID, adopted.ID, sleeper, busy.ID)) {
		t.Fatalf("parked %v", ids)
	}
	if parked.Promoted != 1 || parked.Recovered != 1 || parked.Owed != 4 || len(parked.Errors) != 0 {
		t.Fatalf("park result = %+v", parked)
	}
	if !reflect.DeepEqual(parked.Working, []string{busy.ID}) {
		t.Fatalf("working = %v", parked.Working)
	}
	if ids, _ := h.store.Interrupted(); !reflect.DeepEqual(ids, []string{busy.ID}) {
		t.Fatalf("interrupted set = %v", ids)
	}
	for _, entry := range parked.Parked {
		if entry.Running || entry.Status != status.Dead {
			t.Fatalf("parked entry still reads live: %+v", entry)
		}
	}
	if h.driver.Exists(worker.ID) || h.driver.Exists(sleeper) || h.driver.Exists(busy.ID) || foreignPaneExists(t, h.driver.SocketName(), paneID) {
		t.Fatal("park left a planned session running")
	}
	if !h.driver.Exists(h.caller.ID) || !h.driver.Exists(terminal.ID) || !h.driver.Exists(unknown) {
		t.Fatal("park stopped a pane it said it would leave")
	}
	screen, err := h.sessions.Read(h.caller.ID, worker.ID, "")
	if err != nil || !strings.Contains(screen.Output, "hold the line") {
		t.Fatalf("a parked session should keep its last screen: %q %v", screen.Output, err)
	}
	// The adopted row is now an owned one, on the conversation and in the
	// directory its process named.
	promoted, err := h.store.Get(adopted.ID)
	if err != nil {
		t.Fatalf("Get promoted: %v", err)
	}
	if promoted.TmuxPaneID != "" || promoted.TmuxSocket != "" || promoted.AgentSessionID != "conv-1" ||
		promoted.Cwd != adoptedDir || promoted.Status != status.Dead {
		t.Fatalf("promoted row = %+v", promoted)
	}
	recovered, err := h.store.Get(sleeper)
	if err != nil {
		t.Fatalf("the orphan running a known tool should have a row: %v", err)
	}
	if recovered.Tool != "sleeper" || recovered.Status != status.Dead || recovered.NameSource != store.SourceDerived {
		t.Fatalf("recovered row = %+v", recovered)
	}
	if _, err := h.store.Get(unknown); err == nil {
		t.Fatal("an orphan whose agent cannot be told must not get a row")
	}
	// A second park finds nothing left and does not forget the first.
	again, err := h.sessions.Park(h.caller.ID, false)
	if err != nil || len(again.Parked) != 0 || again.Owed != 4 {
		t.Fatalf("second park = %+v, %v", again, err)
	}

	unparked, err := h.sessions.Unpark(h.caller.ID)
	if err != nil {
		t.Fatalf("Unpark: %v", err)
	}
	if len(unparked.Revived) != 4 || unparked.Owed != 0 || unparked.Continued != 0 || len(unparked.Errors) != 0 {
		t.Fatalf("unpark result = %+v", unparked)
	}
	if !reflect.DeepEqual(unparked.Nudged, []string{busy.ID}) {
		t.Fatalf("nudged = %v, want only the mid-turn session", unparked.Nudged)
	}
	// The mid-turn session came back with "continue" on its command line;
	// the others came back with nothing said to them.
	waitForSessionOutput(t, h.sessions, h.caller.ID, busy.ID, "resumed continue")
	if ids, _ := h.store.Interrupted(); len(ids) != 0 {
		t.Fatalf("interrupted set after unpark = %v", ids)
	}
	if !h.driver.Exists(worker.ID) || !h.driver.Exists(adopted.ID) || !h.driver.Exists(sleeper) || h.driver.Exists(earlier.ID) {
		t.Fatal("unpark revived the wrong rows")
	}
	// The promoted row came back as a managed session on its conversation.
	screen = waitForSessionOutput(t, h.sessions, h.caller.ID, adopted.ID, "resumed conv-1")
	if strings.Contains(screen.Output, "continue") {
		t.Fatalf("an idle session was told to continue: %q", screen.Output)
	}
	if ids, _ := h.store.Parked(); len(ids) != 0 {
		t.Fatalf("parked set after unpark = %v", ids)
	}
	empty, err := h.sessions.Unpark(h.caller.ID)
	if err != nil || len(empty.Revived) != 0 {
		t.Fatalf("unpark with nothing owed = %+v, %v", empty, err)
	}
}

func TestIdentifyToolMatchesTheProgramNotTheShell(t *testing.T) {
	tools := map[string]config.Tool{
		"claude":   {Command: "claude"},
		"sleeper":  {Command: "sleep 30"},
		"terminal": {Command: "", Shell: true},
		"shelly":   {Command: "sh", Shell: true},
	}
	cases := []struct {
		lines []string
		want  string
		ok    bool
	}{
		{[]string{"-zsh", "/usr/local/bin/claude --resume abc"}, "claude", true},
		{[]string{"sh", "sleep 30"}, "sleeper", true},
		{[]string{"sh"}, "", false},
		{nil, "", false},
	}
	for _, tc := range cases {
		got, ok := identifyTool(tools, tc.lines)
		if got != tc.want || ok != tc.ok {
			t.Errorf("identifyTool(%v) = %q, %v; want %q, %v", tc.lines, got, ok, tc.want, tc.ok)
		}
	}
}

// newForeignPane opens a session the manager did not create on the test
// server and returns its pane id and pane pid, the way an adoption scan
// would have recorded them.
func newForeignPane(t *testing.T, socket string) (paneID string, pid int) {
	t.Helper()
	if out, err := exec.Command("tmux", "-L", socket, "new-session", "-d", "-s", "foreign", "-x", "80", "-y", "24", "sh").CombinedOutput(); err != nil {
		t.Fatalf("foreign new-session: %v: %s", err, out)
	}
	out, err := exec.Command("tmux", "-L", socket, "list-panes", "-t", "foreign", "-F", "#{pane_id} #{pane_pid}").Output()
	if err != nil {
		t.Fatalf("foreign list-panes: %v", err)
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		t.Fatalf("foreign list-panes = %q", out)
	}
	pid, err = strconv.Atoi(fields[1])
	if err != nil {
		t.Fatalf("foreign pane pid %q: %v", fields[1], err)
	}
	return fields[0], pid
}

func foreignPaneExists(t *testing.T, socket, paneID string) bool {
	t.Helper()
	out, err := exec.Command("tmux", "-L", socket, "list-panes", "-a", "-F", "#{pane_id}").Output()
	if err != nil {
		return false
	}
	return slices.Contains(strings.Fields(string(out)), paneID)
}

// writeClaudeSessionFile stands a pane's process in for a Claude Code one
// by writing the per-process session file Claude Code would have, stamped
// with the process's real start time so the liveness check accepts it.
func writeClaudeSessionFile(t *testing.T, pid int, conversation, cwd string) (claudeHome string) {
	t.Helper()
	proc, err := process.NewProcess(int32(pid))
	if err != nil {
		t.Fatalf("process %d: %v", pid, err)
	}
	started, err := proc.CreateTime()
	if err != nil {
		t.Fatalf("process %d start time: %v", pid, err)
	}
	claudeHome = t.TempDir()
	if err := os.MkdirAll(filepath.Join(claudeHome, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"pid": pid, "sessionId": conversation, "cwd": cwd, "startedAt": started, "name": "borrowed"})
	if err := os.WriteFile(filepath.Join(claudeHome, "sessions", strconv.Itoa(pid)+".json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return claudeHome
}

func parkedIDs(result ParkResult) []string {
	ids := make([]string, 0, len(result.Parked))
	for _, sess := range result.Parked {
		ids = append(ids, sess.ID)
	}
	sort.Strings(ids)
	return ids
}

func sorted(ids ...string) []string {
	sort.Strings(ids)
	return ids
}

func TestUnparkKeepsAFailedReviveForARetryAndDropsWhatIsGone(t *testing.T) {
	h := newSessionHarness(t)
	orphan := store.Session{ID: uuid.NewString()[:8], Name: "moved", Tool: "echoer", Cwd: t.TempDir() + "/missing", Status: status.Dead}
	if err := h.store.CreateSession(orphan); err != nil {
		t.Fatalf("create orphan: %v", err)
	}
	if err := h.store.SetParked([]string{orphan.ID, "deleted1", h.caller.ID}); err != nil {
		t.Fatalf("SetParked: %v", err)
	}
	result, err := h.sessions.Unpark(h.caller.ID)
	if err != nil {
		t.Fatalf("Unpark: %v", err)
	}
	if len(result.Revived) != 0 || result.Gone != 1 || result.AlreadyRunning != 1 || result.Owed != 1 {
		t.Fatalf("unpark result = %+v", result)
	}
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "working directory no longer exists") {
		t.Fatalf("errors = %v", result.Errors)
	}
	if ids, _ := h.store.Parked(); len(ids) != 1 || ids[0] != orphan.ID {
		t.Fatalf("parked set = %v", ids)
	}
}

func TestReviveWarningNamesWhatUnparkCannotResumeExactly(t *testing.T) {
	rt := &runtime{cfg: config.Config{Tools: map[string]config.Tool{
		"exact":    {Command: "x", ResumeByIDCommand: "x --resume {id}"},
		"continue": {Command: "y", ReviveCommand: "y --continue"},
	}}}
	dir := t.TempDir()
	cases := []struct {
		name string
		sess store.Session
		want string
	}{
		{"unknown tool", store.Session{Tool: "gone", Cwd: dir}, "no longer configured"},
		{"missing directory", store.Session{Tool: "exact", Cwd: dir + "/missing"}, "no longer exists"},
		{"no id for an id-resuming tool", store.Session{Tool: "exact", Cwd: dir}, "continue command"},
		{"id captured", store.Session{Tool: "exact", Cwd: dir, AgentSessionID: "abc"}, ""},
		{"tool that only continues", store.Session{Tool: "continue", Cwd: dir}, ""},
	}
	for _, tc := range cases {
		got := reviveWarning(rt, tc.sess)
		if (tc.want == "" && got != "") || (tc.want != "" && !strings.Contains(got, tc.want)) {
			t.Errorf("%s: warning = %q, want %q", tc.name, got, tc.want)
		}
	}
}
