package app

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

const opencodeStopTool = `
[tools.opencode]
command = "echo"
session_store = "opencode"
default_status = "idle"
`

func TestStopFromInsidePaneCompletesLifecycleOutsideIt(t *testing.T) {
	testSelfStop(t, true, false, false)
}

// The last session's death empties the server, and tmux SIGTERMs a
// run-shell -b job on the way out, so the worker must outlive that.
func TestStopOfTheServersLastSessionCompletesLifecycle(t *testing.T) {
	testSelfStop(t, false, false, false)
}

func TestStopDiscoversAdoptedCallerWithoutLaunchEnvironment(t *testing.T) {
	testSelfStop(t, true, true, false)
}

func TestStopIgnoresAStaleCodexThreadBoundToAnotherPane(t *testing.T) {
	testSelfStop(t, true, false, true)
}

func testSelfStop(t *testing.T, withSentinel, adopted, staleThread bool) {
	bin := buildFixture(t)
	socket := tmuxtest.Socket(t, "selfstop")
	env := fixtureHome(t, "tmux_socket = \""+socket+"\"\n"+promptTool)
	home := envValue(env, "GATE_INBOX_HOME")
	t.Cleanup(func() { killTestServer(t, envValue(env, "TMUX_TMPDIR"), socket) })
	seedSessions(t, filepath.Join(home, "state.db"))
	env = append(withoutKey(env, "GATE_INBOX_SESSION_ID"), "GATE_INBOX_SESSION_ID=ca11e400")
	spawn := func(name string) string {
		t.Helper()
		cmd := exec.Command(bin, "spawn", "--tool", "prompter", "--name", name, "--directory", t.TempDir(), "--json")
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("spawn: %v\n%s", err, out)
		}
		return idOf(t, string(out))
	}
	target := spawn("self-stop")
	var sentinel string
	if withSentinel {
		sentinel = spawn("sentinel")
	}
	tmuxCommand := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("tmux", append([]string{"-L", socket}, args...)...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("tmux: %v\n%s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	st, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	row, err := st.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	history := filepath.Join(home, "history.jsonl")
	if err := os.WriteFile(history, []byte("preserved conversation"), 0o600); err != nil {
		t.Fatal(err)
	}
	row.AgentSessionID = "preserved-conversation"
	if err := st.SetAgentSessionID(target, row.AgentSessionID); err != nil {
		t.Fatal(err)
	}
	const staleCodexThread = "0190a000-0000-7000-8000-00000000000b"
	if staleThread {
		if err := st.SetAgentSessionID(sentinel, staleCodexThread); err != nil {
			t.Fatal(err)
		}
		if err := hooks.NewManager(home).SyncCodex([]hooks.CodexRow{{ID: sentinel, Thread: staleCodexThread}}); err != nil {
			t.Fatal(err)
		}
	}
	dry := filepath.Join(home, "dry.json")
	pane := tmuxCommand("display-message", "-p", "-t", "gi_"+target, "#{pane_id}")
	command := tmux.ShellQuote(bin) + " stop --dry-run --json > " + tmux.ShellQuote(dry) + "; " + tmux.ShellQuote(bin) + " stop --json"
	if adopted {
		db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := db.Exec("UPDATE sessions SET tmux_socket = ?, tmux_pane_id = ? WHERE id = ?", socket, pane, target); err != nil {
			t.Fatal(err)
		}
	}
	// respawn-pane puts the command inside the actual managed surface, so
	// this catches lifecycle writes accidentally left in the killed process.
	launchEnv := "env GATE_INBOX_HOME=" + tmux.ShellQuote(home) + " GATE_INBOX_SESSION_ID=" + tmux.ShellQuote(target)
	if adopted {
		launchEnv = "env -u GATE_INBOX_SESSION_ID GATE_INBOX_HOME=" + tmux.ShellQuote(home)
	}
	if staleThread {
		launchEnv += " CODEX_THREAD_ID=" + staleCodexThread
	}
	tmuxCommand("respawn-pane", "-k", "-t", pane, launchEnv+" sh -c "+tmux.ShellQuote(command))
	deadline := time.Now().Add(15 * time.Second)
	var ends map[string]store.SessionEnd
	for time.Now().Before(deadline) {
		ends, err = st.SessionEnds()
		if err != nil {
			t.Fatal(err)
		}
		if ends[target].Reason == store.EndKilled {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	kept, err := st.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	if !ends[target].Matches(kept) || kept.Status != "dead" || kept.Archived || kept.AgentSessionID != row.AgentSessionID {
		dryData, _ := os.ReadFile(dry)
		logData, _ := os.ReadFile(filepath.Join(home, "logs", "gate-inbox.log"))
		t.Logf("dry: %s; log: %s", dryData, logData)
		capture := exec.Command("tmux", "-L", socket, "capture-pane", "-p", "-t", pane)
		capture.Env = env
		captured, _ := capture.CombinedOutput()
		t.Logf("pane: %s", captured)
		t.Fatalf("end: %+v, row: %+v", ends[target], kept)
	}
	if withSentinel {
		if got := tmuxCommand("list-sessions", "-F", "#{session_name}"); strings.Contains(got, "gi_"+target) || !strings.Contains(got, "gi_"+sentinel) {
			t.Fatalf("remaining sessions: %s", got)
		}
	}
	data, err := os.ReadFile(dry)
	if err != nil || !json.Valid(data) {
		t.Fatalf("dry run: %s, %v", data, err)
	}
	if data, err := os.ReadFile(history); err != nil || string(data) != "preserved conversation" {
		t.Fatal("conversation was removed")
	}
	want := fmt.Sprintf("%s cli %s dead\n", target, target)
	if got := readEventually(t, filepath.Join(home, "extensions", "noop", "kills.txt")); got != want {
		t.Fatalf("kill observer: %q, want %q", got, want)
	}
}

func TestStopRefusesSharedDaemonEvenWithoutThreadOrManagedEnvironment(t *testing.T) {
	bin := buildFixture(t)
	socket := tmuxtest.Socket(t, "sharedstop")
	env := fixtureHome(t, "tmux_socket = \""+socket+"\"\n"+promptTool)
	home := envValue(env, "GATE_INBOX_HOME")
	seedSessions(t, filepath.Join(home, "state.db"))
	env = withoutKey(withoutKey(env, "GATE_INBOX_SESSION_ID"), "CODEX_THREAD_ID")
	tmuxCmd := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("tmux", append([]string{"-L", socket}, args...)...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("tmux: %v: %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	tmuxCmd("new-session", "-d", "-s", "sentinel", "sleep 60")
	pane := tmuxCmd("display-message", "-p", "-t", "sentinel", "#{pane_id}")
	state, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	row := store.Session{ID: "daem0001", Tool: "codex", Name: "adopted", Status: "working", CreatedAt: time.Now(), TmuxSocket: socket, TmuxPaneID: pane, AgentSessionID: "unverified-thread"}
	if err := state.CreateSession(row); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(t.TempDir(), "codex")
	build := exec.Command("go", "build", "-o", fake, "./testdata/stopcaller")
	build.Env = tmuxtest.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}
	for _, thread := range []string{"", "unverified-thread", "another-thread"} {
		output := filepath.Join(t.TempDir(), "result.json")
		cmd := exec.Command(fake, "app-server", bin, output)
		cmd.Env = append(env, "CODEX_THREAD_ID="+thread, "TMUX_PANE="+pane)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
		data := readEventually(t, output)
		if !strings.Contains(data, "cannot verify a Gate Inbox caller pane") || !strings.Contains(data, "\"Failed\":true") {
			t.Fatalf("unsafe daemon classification: %s", data)
		}
		if got := tmuxCmd("display-message", "-p", "-t", pane, "#{pane_id}"); got != pane {
			t.Fatal("sentinel pane ended")
		}
	}
	ends, err := state.SessionEnds()
	if err != nil || len(ends) != 0 {
		t.Fatalf("refusal wrote lifecycle: %v, %v", ends, err)
	}
}

func TestSharedCodexStopResolvesStaleManagedEnvironment(t *testing.T) {
	bin := buildFixture(t)
	socket := tmuxtest.Socket(t, "stalestop")
	env := fixtureHome(t, "tmux_socket = \""+socket+"\"\n"+promptTool)
	home := envValue(env, "GATE_INBOX_HOME")
	seedSessions(t, filepath.Join(home, "state.db"))
	t.Cleanup(func() { killTestServer(t, envValue(env, "TMUX_TMPDIR"), socket) })
	tmuxCmd := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("tmux", append([]string{"-L", socket}, args...)...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("tmux: %v: %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	for _, name := range []string{"own", "foreign"} {
		tmuxCmd("new-session", "-d", "-s", name, "cat")
	}
	ownPane := tmuxCmd("display-message", "-p", "-t", "own", "#{pane_id}")
	foreignPane := tmuxCmd("display-message", "-p", "-t", "foreign", "#{pane_id}")
	state, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	thread := "11111111-1111-4111-8111-111111111111"
	foreignThread := "22222222-2222-4222-8222-222222222222"
	for _, row := range []store.Session{
		{ID: "own00001", Tool: "codex", Name: "own", Status: "working", CreatedAt: time.Now(), TmuxSocket: socket, TmuxPaneID: ownPane, AgentSessionID: thread},
		{ID: "foreign1", Tool: "codex", Name: "foreign", Status: "working", CreatedAt: time.Now(), TmuxSocket: socket, TmuxPaneID: foreignPane, AgentSessionID: foreignThread},
	} {
		if err := state.CreateSession(row); err != nil {
			t.Fatal(err)
		}
	}
	fake := filepath.Join(t.TempDir(), "codex")
	build := exec.Command("go", "build", "-o", fake, "./testdata/stopcaller")
	build.Env = tmuxtest.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}
	run := func(thread string, real bool) struct {
		Output string
		Failed bool
	} {
		t.Helper()
		output := filepath.Join(t.TempDir(), "result.json")
		cmd := exec.Command(fake, "app-server", bin, output)
		cmd.Env = append(withoutKey(withoutKey(env, "GATE_INBOX_SESSION_ID"), "CODEX_THREAD_ID"), "GATE_INBOX_SESSION_ID=foreign1", "CODEX_THREAD_ID="+thread)
		if real {
			cmd.Env = append(cmd.Env, "FIXTURE_STOP_REAL=1")
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
		var result struct {
			Output string
			Failed bool
		}
		if err := json.Unmarshal([]byte(readEventually(t, output)), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	if result := run("33333333-3333-4333-8333-333333333333", false); !result.Failed {
		t.Fatalf("unbound thread accepted: %+v", result)
	}
	duplicate := store.Session{ID: "dupe0001", Tool: "codex", Name: "duplicate", Status: "working", CreatedAt: time.Now(), AgentSessionID: thread}
	if err := state.CreateSession(duplicate); err != nil {
		t.Fatal(err)
	}
	if result := run(thread, false); !result.Failed {
		t.Fatalf("ambiguous thread accepted: %+v", result)
	}
	if err := state.SetAgentSessionID(duplicate.ID, ""); err != nil {
		t.Fatal(err)
	}
	unavailable := store.Session{ID: "unreach1", Tool: "codex", Name: "unavailable", Status: "working",
		CreatedAt: time.Now(), TmuxSocket: tmuxtest.Socket(t, "missingstop"), TmuxPaneID: "%0"}
	if err := state.CreateSession(unavailable); err != nil {
		t.Fatal(err)
	}
	result := run(thread, true)
	if result.Failed {
		t.Fatalf("own stop: %s", result.Output)
	}
	var stop struct {
		CallerProofLine string `json:"caller_proof_line"`
		Session         struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if err := json.Unmarshal([]byte(result.Output), &stop); err != nil || stop.Session.ID != "own00001" || stop.CallerProofLine == "" {
		t.Fatalf("stop result: %s, %v", result.Output, err)
	}
	if got := tmuxCmd("display-message", "-p", "-t", foreignPane, "#{pane_id}"); got != foreignPane {
		t.Fatal("foreign pane ended before proof")
	}
	tmuxCmd("send-keys", "-t", ownPane, "-l", stop.CallerProofLine)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		row, err := state.Get("own00001")
		if err != nil {
			t.Fatal(err)
		}
		if row.Archived && row.Status == "dead" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	row, err := state.Get("own00001")
	if err != nil || !row.Archived || row.Status != "dead" {
		t.Fatalf("own lifecycle: %+v, %v", row, err)
	}
	if got := tmuxCmd("display-message", "-p", "-t", foreignPane, "#{pane_id}"); got != foreignPane {
		t.Fatal("foreign pane ended")
	}
}

type sharedOpencodeStop struct {
	t            *testing.T
	bin          string
	fake         string
	home         string
	socket       string
	ownPane      string
	foreignPane  string
	server       int
	conversation string
	state        *store.Store
	env          []string
	tmux         func(args ...string) string
}

type stopFixtureResult struct {
	Output string
	Failed bool
}

func newSharedOpencodeStop(t *testing.T, family string) *sharedOpencodeStop {
	t.Helper()
	bin := buildFixture(t)
	socket := tmuxtest.Socket(t, family)
	env := fixtureHome(t, "tmux_socket = \""+socket+"\"\n"+opencodeStopTool)
	home := envValue(env, "GATE_INBOX_HOME")
	t.Cleanup(func() { killTestServer(t, envValue(env, "TMUX_TMPDIR"), socket) })
	f := &sharedOpencodeStop{
		t: t, bin: bin, home: home, socket: socket, conversation: "ses_stopboundfixture",
		env: env,
	}
	state, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { state.Close() })
	f.state = state
	now := time.Now()
	for _, row := range []store.Session{
		{ID: "ownoc001", Name: "own", Tool: "opencode", Status: "working", CreatedAt: now, AgentSessionID: f.conversation},
		{ID: "dupeoc01", Name: "duplicate", Tool: "opencode", Status: "working", CreatedAt: now, AgentSessionID: f.conversation},
		{ID: "foreig01", Name: "foreign", Tool: "opencode", Status: "working", CreatedAt: now, AgentSessionID: f.conversation},
		{ID: "archoc01", Name: "archived marker", Tool: "opencode", Status: "dead", CreatedAt: now, AgentSessionID: f.conversation, Archived: true, ArchivedAt: now},
		{ID: "deadoc01", Name: "dead marker", Tool: "opencode", Status: "dead", CreatedAt: now, AgentSessionID: f.conversation},
	} {
		if err := state.CreateSession(row); err != nil {
			t.Fatal(err)
		}
	}
	f.tmux = func(args ...string) string {
		t.Helper()
		cmd := exec.Command("tmux", append([]string{"-L", socket}, args...)...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("tmux: %v: %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	for _, name := range []string{"own", "foreign"} {
		f.tmux("new-session", "-d", "-s", name, "cat")
	}
	f.ownPane = f.tmux("display-message", "-p", "-t", "own", "#{pane_id}")
	f.foreignPane = f.tmux("display-message", "-p", "-t", "foreign", "#{pane_id}")
	server, err := strconv.Atoi(f.tmux("display-message", "-p", "#{pid}"))
	if err != nil {
		t.Fatal(err)
	}
	f.server = server
	if err := state.SetTmuxTarget("ownoc001", socket, f.ownPane); err != nil {
		t.Fatal(err)
	}
	if err := state.SetTmuxTarget("foreig01", socket, f.foreignPane); err != nil {
		t.Fatal(err)
	}
	f.fake = filepath.Join(t.TempDir(), "opencode")
	build := exec.Command("go", "build", "-o", f.fake, "./testdata/stopcaller")
	build.Env = tmuxtest.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}
	f.env = append(env, "TMUX="+tmuxtest.SocketPath(socket)+","+strconv.Itoa(server)+",0")
	return f
}

func (f *sharedOpencodeStop) mark(id, tool, conversation string) {
	f.t.Helper()
	if err := hooks.NewManager(f.home).SyncAdopted([]hooks.AdoptedPane{{
		ID: id, ServerPID: f.server, PaneID: f.ownPane, AgentPID: 1, Tool: tool, Conversation: conversation,
	}}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *sharedOpencodeStop) clearMarker() {
	f.t.Helper()
	if err := hooks.NewManager(f.home).SyncAdopted(nil); err != nil {
		f.t.Fatal(err)
	}
}

func (f *sharedOpencodeStop) run(binary, sessionID string, real bool) stopFixtureResult {
	f.t.Helper()
	output := filepath.Join(f.t.TempDir(), "result.json")
	cmd := exec.Command(binary, "serve", "--service")
	env := append(withoutKey(withoutKey(f.env, "GATE_INBOX_SESSION_ID"), "CODEX_THREAD_ID"),
		"TMUX_PANE="+f.ownPane, "FIXTURE_STOP_BIN="+f.bin, "FIXTURE_STOP_OUTPUT="+output)
	if sessionID != "" {
		env = append(env, "GATE_INBOX_SESSION_ID="+sessionID)
	}
	if real {
		env = append(env, "FIXTURE_STOP_REAL=1")
	}
	cmd.Env = env
	if err := cmd.Start(); err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	var result stopFixtureResult
	if err := json.Unmarshal([]byte(readEventually(f.t, output)), &result); err != nil {
		f.t.Fatal(err)
	}
	return result
}

func (f *sharedOpencodeStop) untouched(id string) {
	f.t.Helper()
	row, err := f.state.Get(id)
	if err != nil || row.Archived || row.Status == "dead" {
		f.t.Fatalf("refusal changed %s: %+v, %v", id, row, err)
	}
	if got := f.tmux("display-message", "-p", "-t", f.ownPane, "#{pane_id}"); got != f.ownPane {
		f.t.Fatal("own pane ended")
	}
	if ends, err := f.state.SessionEnds(); err != nil || len(ends) != 0 {
		f.t.Fatalf("refusal wrote lifecycle: %v, %v", ends, err)
	}
}

func TestSharedOpencodeStopUsesThePaneMarker(t *testing.T) {
	t.Run("missing marker stays unmanaged", func(t *testing.T) {
		f := newSharedOpencodeStop(t, "ocmiss")
		f.clearMarker()
		result := f.run(f.fake, "", false)
		if result.Failed || !strings.Contains(result.Output, `"managed": false`) {
			t.Fatalf("missing marker: %+v", result)
		}
		f.untouched("ownoc001")
	})
	t.Run("stale marker refuses", func(t *testing.T) {
		f := newSharedOpencodeStop(t, "ocstale")
		f.mark("ownoc001", "opencode", "ses_neverbound")
		result := f.run(f.fake, "", false)
		if !result.Failed || !strings.Contains(result.Output, "no longer matches") {
			t.Fatalf("stale marker: %+v", result)
		}
		f.untouched("ownoc001")
	})
	t.Run("marker row bound elsewhere refuses", func(t *testing.T) {
		f := newSharedOpencodeStop(t, "ocelsewhere")
		f.mark("foreig01", "opencode", f.conversation)
		result := f.run(f.fake, "", false)
		if !result.Failed || !strings.Contains(result.Output, "bound to another pane") {
			t.Fatalf("foreign marker row: %+v", result)
		}
		f.untouched("ownoc001")
	})
	t.Run("marker on the unbound duplicate refuses", func(t *testing.T) {
		f := newSharedOpencodeStop(t, "ocduplicate")
		f.mark("dupeoc01", "opencode", f.conversation)
		result := f.run(f.fake, "", false)
		if !result.Failed || !strings.Contains(result.Output, "bound to another pane") {
			t.Fatalf("unbound duplicate marker: %+v", result)
		}
		f.untouched("ownoc001")
		f.untouched("dupeoc01")
	})
	t.Run("stopped marker row refuses", func(t *testing.T) {
		for _, id := range []string{"archoc01", "deadoc01"} {
			t.Run(id, func(t *testing.T) {
				f := newSharedOpencodeStop(t, "ocstopped")
				f.mark(id, "opencode", f.conversation)
				result := f.run(f.fake, "", false)
				if !result.Failed || !strings.Contains(result.Output, "has stopped") {
					t.Fatalf("stopped marker row: %+v", result)
				}
				f.untouched("ownoc001")
			})
		}
	})
	t.Run("ambiguous bound rows refuse", func(t *testing.T) {
		f := newSharedOpencodeStop(t, "ocambiguous")
		extra := store.Session{ID: "amboc001", Name: "ambiguous", Tool: "opencode", Status: "working",
			CreatedAt: time.Now(), TmuxSocket: f.socket, TmuxPaneID: f.ownPane, AgentSessionID: f.conversation}
		if err := f.state.CreateSession(extra); err != nil {
			t.Fatal(err)
		}
		f.mark("ownoc001", "opencode", f.conversation)
		result := f.run(f.fake, "", false)
		if !result.Failed || !strings.Contains(result.Output, "multiple Gate Inbox rows") {
			t.Fatalf("ambiguous rows: %+v", result)
		}
		f.untouched("ownoc001")
	})
	t.Run("without the shared service refuses", func(t *testing.T) {
		f := newSharedOpencodeStop(t, "ocstandalone")
		f.mark("ownoc001", "opencode", f.conversation)
		agent := filepath.Join(t.TempDir(), "agent")
		copyFile(t, f.fake, agent, 0o700)
		result := f.run(agent, "ownoc001", false)
		if !result.Failed || !strings.Contains(result.Output, "own managed session pane") {
			t.Fatalf("standalone caller: %+v", result)
		}
		f.untouched("ownoc001")
	})
	t.Run("foreign environment resolves the bound row", func(t *testing.T) {
		f := newSharedOpencodeStop(t, "ocforeignenv")
		f.mark("ownoc001", "opencode", f.conversation)
		result := f.run(f.fake, "foreig01", true)
		if result.Failed {
			t.Fatalf("own stop: %s", result.Output)
		}
		f.finish(result)
	})
	t.Run("unavailable unrelated pane does not block the bound row", func(t *testing.T) {
		f := newSharedOpencodeStop(t, "ocunavailable")
		unavailable := store.Session{ID: "unreach1", Tool: "opencode", Name: "unavailable", Status: "working",
			CreatedAt: time.Now(), TmuxSocket: tmuxtest.Socket(t, "missingocstop"), TmuxPaneID: "%0"}
		if err := f.state.CreateSession(unavailable); err != nil {
			t.Fatal(err)
		}
		f.mark("ownoc001", "opencode", f.conversation)
		result := f.run(f.fake, "ownoc001", true)
		if result.Failed {
			t.Fatalf("own stop: %s", result.Output)
		}
		f.finish(result)
	})
	t.Run("no managed environment uses the pane marker", func(t *testing.T) {
		f := newSharedOpencodeStop(t, "ocbare")
		f.mark("ownoc001", "opencode", f.conversation)
		result := f.run(f.fake, "", true)
		if result.Failed {
			t.Fatalf("own stop: %s", result.Output)
		}
		f.finish(result)
	})
}

func (f *sharedOpencodeStop) finish(result stopFixtureResult) {
	f.t.Helper()
	var stop struct {
		CallerProofLine string `json:"caller_proof_line"`
		Session         struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if err := json.Unmarshal([]byte(result.Output), &stop); err != nil || stop.Session.ID != "ownoc001" || stop.CallerProofLine == "" {
		f.t.Fatalf("stop result: %s, %v", result.Output, err)
	}
	if got := f.tmux("display-message", "-p", "-t", f.foreignPane, "#{pane_id}"); got != f.foreignPane {
		f.t.Fatal("foreign pane ended before proof")
	}
	f.tmux("send-keys", "-t", f.ownPane, "-l", stop.CallerProofLine)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		row, err := f.state.Get("ownoc001")
		if err != nil {
			f.t.Fatal(err)
		}
		if row.Archived && row.Status == "dead" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	row, err := f.state.Get("ownoc001")
	if err != nil || !row.Archived || row.Status != "dead" {
		f.t.Fatalf("own lifecycle: %+v, %v", row, err)
	}
	duplicate, err := f.state.Get("dupeoc01")
	if err != nil || duplicate.Archived || duplicate.Status == "dead" {
		f.t.Fatalf("duplicate row changed: %+v, %v", duplicate, err)
	}
	if got := f.tmux("display-message", "-p", "-t", f.foreignPane, "#{pane_id}"); got != f.foreignPane {
		f.t.Fatal("foreign pane ended")
	}
}
