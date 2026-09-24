package app

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

const modulePath = "github.com/usestring/gate-inbox"

// fixtureSource is a main in a module of its own that registers one no-op
// extension through the public packages.
var fixtureSource = filepath.Join("testdata", "external", "main.go")

// TestFixtureImportsOnlyThePublicPackages states the boundary in the
// fixture's own source, so a failure names the import rather than leaving
// it to a compiler error about internal packages.
func TestFixtureImportsOnlyThePublicPackages(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), fixtureSource, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	public := map[string]bool{modulePath + "/app": true, modulePath + "/extension": true}
	for _, spec := range file.Imports {
		path, _ := strconv.Unquote(spec.Path.Value)
		if strings.Contains(path, "/internal/") || strings.HasSuffix(path, "/internal") {
			t.Errorf("the fixture imports %s", path)
		}
		if strings.HasPrefix(path, modulePath) && !public[path] {
			t.Errorf("the fixture imports %s, which is not a public package", path)
		}
	}
}

// buildFixture builds the fixture as its own module, requiring this one
// through a replace. The go tool refuses an internal import across modules,
// so this succeeding is the proof that a build outside the module needs
// nothing under internal/.
func buildFixture(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	source, err := os.ReadFile(fixtureSource)
	if err != nil {
		t.Fatal(err)
	}
	goMod := "module example.test/fixture\n\ngo 1.26\n\n" +
		"require " + modulePath + " v0.0.0\n\n" +
		"replace " + modulePath + " => " + root + "\n"
	sum, err := os.ReadFile(filepath.Join(root, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string][]byte{"main.go": source, "go.mod": []byte(goMod), "go.sum": sum} {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(dir, "fixture")
	build := exec.Command("go", "build", "-buildvcs=false", "-o", bin, ".")
	build.Dir = dir
	build.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("the fixture module does not build against the public packages: %v\n%s", err, out)
	}
	return bin
}

// fixtureHome is a scratch GATE_INBOX_HOME holding config.
func fixtureHome(t *testing.T, config string) []string {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("poll_interval = \"2s\"\n\n"+config), 0o644); err != nil {
		t.Fatal(err)
	}
	return append(isolatedEnv(t), "GATE_INBOX_HOME="+home, "GATE_INBOX_SESSION_ID=fixture-session")
}

// isolatedEnv is this process's environment without the tmux server the test
// run was started from. $TMUX names the pane the test was run in, and tmux
// with no -L or -S follows it to that live server; TMUX_TMPDIR is a scratch
// directory of the test's own, so no socket name resolves outside it. The
// directory is a short one: a socket path under t.TempDir() runs past the
// length a unix socket may have.
func isolatedEnv(t *testing.T) []string {
	t.Helper()
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key == "TMUX" || key == "TMUX_PANE" || key == "TMUX_TMPDIR" {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "TMUX_TMPDIR="+tmuxtest.ScratchDir(t))
}

// TestExternalBuildServesEveryEntryPoint is the acceptance proof for the
// public seam: a module outside this one, importing app and extension only,
// builds the board with an extension of its own, and each face of the
// executable -- CLI, per-session MCP server and TUI -- runs through app.Run
// with that extension composed in.
func TestExternalBuildServesEveryEntryPoint(t *testing.T) {
	bin := buildFixture(t)

	t.Run("cli", func(t *testing.T) {
		env := fixtureHome(t, "")
		version := exec.Command(bin, "--version")
		version.Env = env
		out, err := version.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "gate-inbox 0.0.0-fixture") {
			t.Fatalf("--version: %v\n%s", err, out)
		}
		unknown := exec.Command(bin, "definitely-not-a-verb")
		unknown.Env = env
		out, err = unknown.CombinedOutput()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 2 || !strings.Contains(string(out), "unknown command") {
			t.Fatalf("an unknown verb: %v\n%s", err, out)
		}
	})

	t.Run("mcp", func(t *testing.T) {
		session := connectFixture(t, bin, fixtureHome(t, "[extensions.noop]\ngreeting = \"hi\"\n"))
		names := listTools(t, session)
		if !names["noop_ping"] || !names["rename"] {
			t.Fatalf("want the extension's tool beside the host's; got %v", names)
		}
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "noop_ping", Arguments: map[string]any{}})
		if err != nil {
			t.Fatalf("call noop_ping: %v", err)
		}
		text := result.Content[0].(*mcp.TextContent).Text
		// Its own config section, and the session it was registered for.
		if text != "hi from fixture-session" {
			t.Fatalf("noop_ping answered %q", text)
		}
	})

	// The extension reads the board through Host alone: two rows seeded
	// into the store the MCP face opens, listed and then read back by a
	// tool compiled outside this module.
	t.Run("mcp host services", func(t *testing.T) {
		if _, err := exec.LookPath("tmux"); err != nil {
			t.Skip("the session services open a tmux driver")
		}
		env := fixtureHome(t, "")
		home := envValue(env, "GATE_INBOX_HOME")
		// Session ids are hex; a later entry overrides fixtureHome's.
		env = append(env, "TMUX_TMPDIR="+t.TempDir(), "GATE_INBOX_SESSION_ID=ca11e400")
		seedSessions(t, filepath.Join(home, "state.db"))

		session := connectFixture(t, bin, env)
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
			Name: "noop_peek", Arguments: map[string]any{"id": "c41d0001"},
		})
		if err != nil {
			t.Fatalf("call noop_peek: %v", err)
		}
		got := result.Content[0].(*mcp.TextContent).Text
		if result.IsError {
			t.Fatalf("noop_peek failed: %s", got)
		}
		for _, want := range []string{"ca11e400,c41d0001", "the child pane | the child's last screen"} {
			if !strings.Contains(got, want) {
				t.Fatalf("noop_peek answered %q, want it to contain %q", got, want)
			}
		}
	})

	// The extension reads a pane and answers its dialog as the board, on a
	// session it has no relationship to, through app.NewBoard and the
	// public dialog reading alone.
	t.Run("board reads and answers a dialog", func(t *testing.T) {
		if _, err := exec.LookPath("tmux"); err != nil {
			t.Skip("the board reads a tmux pane")
		}
		socket := tmuxtest.NewSocket("board")
		env := fixtureHome(t, "tmux_socket = \""+socket+"\"\n")
		home := envValue(env, "GATE_INBOX_HOME")
		tmuxDir := envValue(env, "TMUX_TMPDIR")
		env = append(env, "GATE_INBOX_SESSION_ID=ca11e400")
		seedSessions(t, filepath.Join(home, "state.db"))
		// This process's own tmux calls resolve the socket under the same
		// scratch directory as the fixture's.
		t.Setenv("TMUX_TMPDIR", tmuxDir)
		t.Cleanup(func() { killTestServer(t, tmuxDir, socket) })
		paintSession(t, filepath.Join(home, "state.db"), socket, "d1a10001", boardAskPane)
		paintSession(t, filepath.Join(home, "state.db"), socket, "d1a10002", boardPermissionPane)

		session := connectFixture(t, bin, env)
		read := callText(t, session, "noop_board", map[string]any{"id": "d1a10001"})
		if want := "AskUserQuestion | Which region should the survey cover? | North only,South only | parsed:true"; read != want {
			t.Fatalf("noop_board read %q, want %q", read, want)
		}
		answered := callText(t, session, "noop_board", map[string]any{"id": "d1a10001", "answer": "South only"})
		if !strings.HasSuffix(answered, "| selected: South only") {
			t.Fatalf("noop_board answered %q", answered)
		}
		refused := callText(t, session, "noop_board", map[string]any{"id": "d1a10002", "answer": "Yes"})
		if !strings.Contains(refused, "| refused: a permission prompt") {
			t.Fatalf("noop_board on a permission prompt answered %q", refused)
		}
	})

	// The extension finds a session's conversation and writes its handover
	// copy as the board, through app.NewBoard alone.
	t.Run("board reads a conversation", func(t *testing.T) {
		if _, err := exec.LookPath("tmux"); err != nil {
			t.Skip("the board commands open a tmux driver")
		}
		claudeHome := t.TempDir()
		env := append(fixtureHome(t, ""), "CLAUDE_CONFIG_DIR="+claudeHome, "GATE_INBOX_SESSION_ID=ca11e400")
		home := envValue(env, "GATE_INBOX_HOME")
		seedSessions(t, filepath.Join(home, "state.db"))
		st, err := store.Open(filepath.Join(home, "state.db"))
		if err != nil {
			t.Fatal(err)
		}
		err = st.CreateSession(store.Session{ID: "c0a70001", Name: "talker", Tool: "claude", Status: "idle",
			AgentSessionID: "conv-5678", Cwd: t.TempDir(), CreatedAt: time.Now()})
		st.Close()
		if err != nil {
			t.Fatal(err)
		}
		transcript := filepath.Join(claudeHome, "projects", "any-project", "conv-5678.jsonl")
		if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
			t.Fatal(err)
		}
		records := `{"type":"user","message":{"role":"user","content":"before the compaction"}}` + "\n" +
			`{"type":"system","subtype":"compact_boundary","content":"Conversation compacted"}` + "\n" +
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"still on course"}]}}` + "\n" +
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"drifted"}]}}` + "\n"
		if err := os.WriteFile(transcript, []byte(records), 0o644); err != nil {
			t.Fatal(err)
		}

		session := connectFixture(t, bin, env)
		if got, want := callText(t, session, "noop_convo", map[string]any{"id": "c0a70001"}),
			"conv-5678 | claude conv-5678.jsonl | conv-5678.jsonl.handover.jsonl cut:false filtered:true"; got != want {
			t.Fatalf("noop_convo answered %q, want %q", got, want)
		}
		if got := callText(t, session, "noop_convo", map[string]any{"id": "c0a70001", "until": "still on course"}); !strings.HasSuffix(got, "cut:true filtered:true") {
			t.Fatalf("noop_convo with a quote answered %q", got)
		}
		// The same reads through the calling session's Host, where a
		// session's MCP tools and CLI verbs have no Board.
		if got, want := callText(t, session, "noop_convo", map[string]any{"id": "c0a70001", "as_session": true}),
			"conv-5678 | claude conv-5678.jsonl | conv-5678.jsonl.handover.jsonl cut:false filtered:true"; got != want {
			t.Fatalf("noop_convo as the session answered %q, want %q", got, want)
		}
	})

	// Two sessions' MCP servers are two processes: what one writes to the
	// extension's data directory, the other reads back, and it lands under
	// the config directory rather than in the board's own store.
	t.Run("mcp data directory", func(t *testing.T) {
		env := fixtureHome(t, "")
		home := envValue(env, "GATE_INBOX_HOME")
		writer := connectFixture(t, bin, env)
		if got := callText(t, writer, "noop_note", map[string]any{"write": "kept across processes"}); got != "kept across processes" {
			t.Fatalf("noop_note wrote and answered %q", got)
		}
		reader := connectFixture(t, bin, append(env, "GATE_INBOX_SESSION_ID=another-session"))
		if got := callText(t, reader, "noop_note", map[string]any{}); got != "kept across processes" {
			t.Fatalf("a second MCP server read back %q", got)
		}
		if _, err := os.Stat(filepath.Join(home, "extensions", "noop", "note.txt")); err != nil {
			t.Fatalf("the note is not under the extension's data directory: %v", err)
		}
	})

	t.Run("mcp keeps the host's tools when an extension's config is refused", func(t *testing.T) {
		session := connectFixture(t, bin, fixtureHome(t, "[extensions.noop]\ngreeting = \"hi\"\nbogus = 1\n"))
		names := listTools(t, session)
		if names["noop_ping"] || !names["rename"] {
			t.Fatalf("want the host's tools and not the refused extension's; got %v", names)
		}
	})

	// The board takes over a terminal, so it is run under script(1) with a
	// config the extension refuses: the refusal comes from the TUI's own
	// startup, after it has found a terminal and before it touches tmux or
	// a running board.
	t.Run("tui", func(t *testing.T) {
		script, err := exec.LookPath("script")
		if err != nil {
			t.Skip("script(1) is needed to give the board a terminal")
		}
		for name, tc := range map[string]struct{ config, want string }{
			"unknown key":     {"[extensions.noop]\nbogus = 1\n", "[extensions.noop]: unknown key(s): bogus"},
			"unowned section": {"[extensions.stranger]\nx = 1\n", "no extension in this build owns: stranger (this build has: noop)"},
		} {
			t.Run(name, func(t *testing.T) {
				// util-linux and BSD script(1) take the command differently.
				board := exec.Command(script, "-qec", bin, "/dev/null")
				if runtime.GOOS != "linux" {
					board = exec.Command(script, "-q", "/dev/null", bin)
				}
				board.Env = fixtureHome(t, tc.config)
				out, _ := board.CombinedOutput()
				if !strings.Contains(string(out), tc.want) {
					t.Fatalf("the board did not refuse the config with %q:\n%s", tc.want, out)
				}
			})
		}
	})
}

func connectFixture(t *testing.T, bin string, env []string) *mcp.ClientSession {
	t.Helper()
	server := exec.Command(bin, "mcp")
	server.Env = env
	client := mcp.NewClient(&mcp.Implementation{Name: "fixture-client", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), &mcp.CommandTransport{Command: server}, nil)
	if err != nil {
		t.Fatalf("connect to the fixture's MCP server: %v", err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func listTools(t *testing.T, session *mcp.ClientSession) map[string]bool {
	t.Helper()
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	names := map[string]bool{}
	for _, tool := range listed.Tools {
		names[tool.Name] = true
	}
	return names
}

// seedSessions writes the calling session and one dead child, with the
// screen it died on, into the state database at path.
func seedSessions(t *testing.T, path string) {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now()
	for _, sess := range []store.Session{
		{ID: "ca11e400", Name: "the caller", Tool: "claude", Status: "working", CreatedAt: now},
		{ID: "c41d0001", Name: "the child", Tool: "claude", Status: "dead", ParentID: "ca11e400", CreatedAt: now},
	} {
		if err := st.CreateSession(sess); err != nil {
			t.Fatalf("seed %s: %v", sess.ID, err)
		}
	}
	if err := st.SetSnapshot("c41d0001", "the child's last screen"); err != nil {
		t.Fatal(err)
	}
}

const boardAskPane = `Which region should the survey cover?

❯ 1. North only
  2. South only

Enter to select · ↑/↓ to navigate · Esc to cancel
`

const boardPermissionPane = `  Do you want to proceed?
  ❯ 1. Yes
    2. No

  Enter to confirm · Esc to cancel
`

// killTestServer ends the server on a test-owned socket, addressed by its
// full path under dir so it can never resolve to another server.
func killTestServer(t *testing.T, dir, socket string) {
	t.Helper()
	if !tmuxtest.Owns(socket) {
		t.Fatalf("refusing to kill the tmux server on %q: not a test socket", socket)
	}
	path := filepath.Join(dir, "tmux-"+strconv.Itoa(os.Getuid()), socket)
	_ = exec.Command("tmux", "-S", path, "kill-server").Run()
}

// paintSession files an agent session nobody spawned and runs a pane for it
// on socket, showing pane, and waits for the paint to land.
func paintSession(t *testing.T, path, socket, id, pane string) {
	t.Helper()
	dir := t.TempDir()
	fixture := filepath.Join(dir, "pane.txt")
	if err := os.WriteFile(fixture, []byte(pane), 0o644); err != nil {
		t.Fatal(err)
	}
	driver, err := tmux.NewWithSocket(socket)
	if err != nil {
		t.Fatal(err)
	}
	if err := driver.Create(id, dir, "cat "+fixture+"; sleep 60", nil, 80, 24); err != nil {
		t.Fatalf("create %s: %v", id, err)
	}
	t.Cleanup(func() { _ = driver.Kill(id) })
	lines := strings.Split(strings.TrimSpace(pane), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	deadline := time.Now().Add(10 * time.Second)
	for {
		painted, err := driver.CapturePane(id)
		if err == nil && strings.Contains(ansi.Strip(painted), last) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never painted:\n%s", id, painted)
		}
		time.Sleep(25 * time.Millisecond)
	}
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.CreateSession(store.Session{ID: id, Name: "painted " + id, Tool: "claude", Status: "waiting", Cwd: dir, CreatedAt: time.Now()}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

// envValue is the last value env gives key, which is the one a child
// process sees.
func envValue(env []string, key string) string {
	value := ""
	for _, entry := range env {
		if v, ok := strings.CutPrefix(entry, key+"="); ok {
			value = v
		}
	}
	return value
}

// callText calls one tool and returns its text, failing on a tool error.
func callText(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if result.IsError {
		t.Fatalf("%s failed: %s", name, text)
	}
	return text
}
