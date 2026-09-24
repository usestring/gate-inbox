package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

const modulePath = "github.com/usestring/gate-inbox"

// fixtureSource is a main in a module of its own that registers one no-op
// extension and the artifacts one through the public packages.
var fixtureSource = filepath.Join("testdata", "external", "main.go")

// badSnippetSource is a build whose snippet defaults cannot bind.
var badSnippetSource = filepath.Join("testdata", "badsnippet", "main.go")

// TestFixtureImportsOnlyThePublicPackages states the boundary in the
// fixtures' own source, so a failure names the import rather than leaving
// it to a compiler error about internal packages.
func TestFixtureImportsOnlyThePublicPackages(t *testing.T) {
	for _, source := range []string{fixtureSource, badSnippetSource} {
		file, err := parser.ParseFile(token.NewFileSet(), source, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse fixture: %v", err)
		}
		public := map[string]bool{modulePath + "/app": true, modulePath + "/extension": true, modulePath + "/extension/artifacts": true}
		for _, spec := range file.Imports {
			path, _ := strconv.Unquote(spec.Path.Value)
			if strings.Contains(path, "/internal/") || strings.HasSuffix(path, "/internal") {
				t.Errorf("%s imports %s", source, path)
			}
			if strings.HasPrefix(path, modulePath) && !public[path] {
				t.Errorf("%s imports %s, which is not a public package", source, path)
			}
		}
	}
}

// buildFixture builds the fixture as its own module, requiring this one
// through a replace. The go tool refuses an internal import across modules,
// so this succeeding is the proof that a build outside the module needs
// nothing under internal/.
func buildFixture(t *testing.T) string {
	t.Helper()
	return buildModule(t, fixtureSource)
}

// buildModule builds the main at sourcePath as a module of its own.
func buildModule(t *testing.T, sourcePath string) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	source, err := os.ReadFile(sourcePath)
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
	build.Env = append(tmuxtest.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("the fixture module does not build against the public packages: %v\n%s", err, out)
	}
	return bin
}

// fixtureHome is a scratch GATE_INBOX_HOME holding config, in an environment
// with no way back to the tmux server the test itself may be running in: no
// TMUX or TMUX_PANE, and a TMUX_TMPDIR of its own.
func fixtureHome(t *testing.T, config string) []string {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("poll_interval = \"2s\"\n\n"+config), 0o644); err != nil {
		t.Fatal(err)
	}
	return append(tmuxtest.Environ(), "GATE_INBOX_HOME="+home, "GATE_INBOX_SESSION_ID=fixture-session")
}

// TestExternalBuildServesEveryEntryPoint is the acceptance proof for the
// public seam: a module outside this one, importing app and extension only,
// builds the board with extensions of its own, and the CLI and per-session
// MCP faces of the executable run through app.Run with them composed in.
// The TUI face no longer refuses an extension's config, so it has nothing
// to show here without taking over a terminal and a tmux server; the key
// map's report of a disabled extension is tested in the ui package.
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

	// The extension redacts text with the board's own scrubber.
	t.Run("mcp scrub", func(t *testing.T) {
		session := connectFixture(t, bin, fixtureHome(t, ""))
		key := "sk-" + strings.Repeat("x", 24)
		got := callText(t, session, "noop_scrub", map[string]any{"text": "export KEY=" + key + " and go"})
		if got != "export KEY=[redacted] and go" {
			t.Fatalf("noop_scrub answered %q", got)
		}
	})

	// The build's config defaults lie under the operator's file: with no
	// file of its own, the extension's greeting and claude's named-account
	// settings both come from the distribution. The "mcp" case above,
	// whose file sets the greeting, is the operator winning.
	t.Run("mcp config defaults", func(t *testing.T) {
		home := t.TempDir()
		env := append(tmuxtest.Environ(), "GATE_INBOX_HOME="+home, "GATE_INBOX_SESSION_ID=fixture-session")
		session := connectFixture(t, bin, env)
		if got := callText(t, session, "noop_ping", map[string]any{}); got != "distribution greeting from fixture-session" {
			t.Fatalf("noop_ping answered %q", got)
		}
		got := callText(t, session, "list_accounts", map[string]any{"tool": "claude"})
		if !strings.Contains(got, "alice1\nbob2") {
			t.Fatalf("list_accounts answered %q, want the accounts the defaults' accounts_command lists", got)
		}
		written, err := os.ReadFile(filepath.Join(home, "config.toml"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(written), "FIXTURE_") || strings.Contains(string(written), "distribution greeting") {
			t.Fatal("the first-run config.toml carries the distribution's defaults")
		}
	})

	// Defaults are part of the build, so a section in them that no
	// extension of the build owns is a broken build: every face refuses it
	// before it starts, the MCP face included, rather than serving a session
	// with the build's extensions silently dropped.
	t.Run("defaults naming an unowned section stop every face", func(t *testing.T) {
		const want = "config defaults: [extensions] has section(s) no extension in this build owns: stranger (this build has: noop, artifacts, items, tally, extra)"
		faces := map[string]*exec.Cmd{
			"cli": exec.Command(bin, "--version"),
			"mcp": exec.Command(bin, "mcp"),
		}
		if script, err := exec.LookPath("script"); err == nil {
			faces["tui"] = exec.Command(script, "-qec", bin, "/dev/null")
			if runtime.GOOS != "linux" {
				faces["tui"] = exec.Command(script, "-q", "/dev/null", bin)
			}
		}
		for name, face := range faces {
			t.Run(name, func(t *testing.T) {
				face.Env = append(fixtureHome(t, ""), "FIXTURE_EXTRA_DEFAULTS=\n[extensions.stranger]\nx = 1\n")
				out, err := face.CombinedOutput()
				if err == nil && name != "tui" {
					t.Fatalf("started despite the unowned section:\n%s", out)
				}
				if !strings.Contains(string(out), want) {
					t.Fatalf("want %q:\n%s", want, out)
				}
			})
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
		for _, want := range []string{"ca11e400,c41d0001 in 2 pages", "the child pane | the child's last screen"} {
			if !strings.Contains(got, want) {
				t.Fatalf("noop_peek answered %q, want it to contain %q", got, want)
			}
		}

		// When a session was created and archived, read through Host as the
		// store holds them: an unarchived row has no archive time.
		st, err := store.Open(filepath.Join(home, "state.db"))
		if err != nil {
			t.Fatal(err)
		}
		created := time.Now().Add(-time.Hour)
		if err := st.CreateSession(store.Session{ID: "a4c41fe0", Name: "filed", Tool: "claude",
			Status: "dead", CreatedAt: created}); err != nil {
			t.Fatal(err)
		}
		if err := st.SetArchived("a4c41fe0", true); err != nil {
			t.Fatal(err)
		}
		stored := map[string]store.Session{}
		for _, id := range []string{"c41d0001", "a4c41fe0"} {
			sess, err := st.Get(id)
			if err != nil {
				t.Fatal(err)
			}
			stored[id] = sess
		}
		st.Close()
		if stored["a4c41fe0"].ArchivedAt.IsZero() {
			t.Fatal("the store kept no archive time for the archived row")
		}
		for id, sess := range stored {
			result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
				Name: "noop_stamps", Arguments: map[string]any{"id": id},
			})
			if err != nil {
				t.Fatalf("call noop_stamps: %v", err)
			}
			got := result.Content[0].(*mcp.TextContent).Text
			archived := int64(0)
			if !sess.ArchivedAt.IsZero() {
				archived = sess.ArchivedAt.UnixNano()
			}
			if want := fmt.Sprintf("%d %d", sess.CreatedAt.UnixNano(), archived); got != want {
				t.Fatalf("noop_stamps %s answered %q, want %q", id, got, want)
			}
		}
		if got := stored["a4c41fe0"].CreatedAt.UnixNano(); got != created.UnixNano() {
			t.Fatalf("the archived row was created at %d, want %d", got, created.UnixNano())
		}
	})

	// The extension lists the terminal nested under its own session and
	// ends it through the Host. From a session outside that tree it ends a
	// terminal under a session it could kill, and is refused one whose
	// session is gone.
	t.Run("mcp host ends a nested terminal", func(t *testing.T) {
		if _, err := exec.LookPath("tmux"); err != nil {
			t.Skip("ending a terminal ends a tmux pane")
		}
		socket := tmuxtest.NewSocket("endterm")
		env := fixtureHome(t, "tmux_socket = \""+socket+"\"\n"+shellTool)
		home := envValue(env, "GATE_INBOX_HOME")
		tmuxDir := envValue(env, "TMUX_TMPDIR")
		env = append(env, "GATE_INBOX_SESSION_ID=ca11e400")
		state := filepath.Join(home, "state.db")
		seedSessions(t, state)
		t.Setenv("TMUX_TMPDIR", tmuxDir)
		t.Cleanup(func() { killTestServer(t, tmuxDir, socket) })
		mine := paintTerminal(t, state, socket, "7e770001", "ca11e400")
		theirs := paintTerminal(t, state, socket, "7e770002", "c41d0001")

		session := connectFixture(t, bin, env)
		ended := callText(t, session, "noop_end", map[string]any{"id": "7e770001"})
		if want := "c41d0001 terminal=false,7e770001 terminal=true | ended 7e770001"; ended != want {
			t.Fatalf("noop_end answered %q, want %q", ended, want)
		}
		if mine.Exists("7e770001") {
			t.Fatal("the nested terminal's pane outlived the kill")
		}

		// A session outside the tree, as the operator's own would be.
		st, err := store.Open(state)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"0a75de01", "0a75de02"} {
			if err := st.CreateSession(store.Session{ID: id, Name: "outsider " + id, Tool: "claude", Status: "idle", CreatedAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
		}
		st.Close()
		// A terminal whose session then goes, leaving it nested under none.
		gone := paintTerminal(t, state, socket, "7e770004", "0a75de02")
		st, err = store.Open(state)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Delete("0a75de02"); err != nil {
			t.Fatal(err)
		}
		st.Close()
		outsider := connectFixture(t, bin, append(env, "GATE_INBOX_SESSION_ID=0a75de01"))
		ended = callText(t, outsider, "noop_end", map[string]any{"id": "7e770002", "parent": "c41d0001"})
		if want := "7e770002 terminal=true | ended 7e770002"; ended != want {
			t.Fatalf("noop_end from outside answered %q, want %q", ended, want)
		}
		if theirs.Exists("7e770002") {
			t.Fatal("the terminal under a killable session outlived the kill")
		}
		refused := callText(t, outsider, "noop_end", map[string]any{"id": "7e770004", "parent": "0a75de02"})
		if !strings.Contains(refused, "| refused: terminal 7e770004 is nested under no agent session") {
			t.Fatalf("noop_end on an orphaned terminal answered %q", refused)
		}
		if !gone.Exists("7e770004") {
			t.Fatal("the orphaned terminal was ended")
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

	// The extension reads a session's inbox as the board, through
	// app.NewBoard alone.
	t.Run("board reads an inbox", func(t *testing.T) {
		if _, err := exec.LookPath("tmux"); err != nil {
			t.Skip("the board commands open a tmux driver")
		}
		env := fixtureHome(t, "")
		home := envValue(env, "GATE_INBOX_HOME")
		seedSessions(t, filepath.Join(home, "state.db"))
		st, err := store.Open(filepath.Join(home, "state.db"))
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		for i, msg := range []store.InboxMessage{
			{SenderID: "c41d0001", Body: "done: the survey", SentAt: now.Add(-time.Minute)},
			{SenderID: store.RelayedHumanSenderID, Body: "use the second draft", SentAt: now.Add(-30 * time.Second)},
			{SenderID: store.HumanSenderID, Body: "and the totals", SentAt: now},
		} {
			msg.SessionID, msg.SenderName, msg.Fingerprint = "ca11e400", msg.SenderID, textfmt.Fingerprint(msg.Body)
			id, _, err := st.Enqueue(msg, store.DefaultInboxLimits)
			if err == nil && i == 0 {
				err = st.MarkDelivered(id, now)
			}
			if err != nil {
				st.Close()
				t.Fatal(err)
			}
		}
		st.Close()

		session := connectFixture(t, bin, env)
		if got, want := callText(t, session, "noop_inbox", map[string]any{"id": "ca11e400"}),
			"operator:and the totals:true | operator/relayed:use the second draft:true | c41d0001:done: the survey:false"; got != want {
			t.Fatalf("noop_inbox answered %q, want %q", got, want)
		}
		if got, want := callText(t, session, "noop_inbox", map[string]any{"id": "ca11e400", "from": "operator", "pending": true}),
			"operator:and the totals:true"; got != want {
			t.Fatalf("noop_inbox filtered answered %q, want %q", got, want)
		}
		// A relayed line is the operator's words but not typed at a shell,
		// and is asked for and named as such.
		if got, want := callText(t, session, "noop_inbox", map[string]any{"id": "ca11e400", "from": "operator/relayed"}),
			"operator/relayed:use the second draft:true"; got != want {
			t.Fatalf("noop_inbox relayed answered %q, want %q", got, want)
		}
	})

	// The extension reads the configured CLIs, as the board and as the
	// session, without importing the config package.
	t.Run("tools", func(t *testing.T) {
		env := fixtureHome(t, "[tools.pinger]\ncommand = \"cat\"\nmodel_flag = \"--model\"\nmodels = [\"small\", \"large\"]\n\n[tools.term]\ncommand = \"sh\"\nshell = true\n")
		got := callText(t, connectFixture(t, bin, env), "noop_tools", map[string]any{})
		for _, want := range []string{
			"pinger shell:false model:true hooks:false [small,large]",
			"term shell:true model:false hooks:false []",
			"claude shell:false",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("noop_tools answered\n%s\nwant a line holding %q", got, want)
			}
		}
		if !strings.Contains(got, "hooks:true") {
			t.Fatalf("no shipped tool reports status through hooks:\n%s", got)
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

	// The artifacts section is owned once the build registers the public
	// extension, so a config naming it starts, and enabling it serves the
	// artifact tools beside the fixture's own.
	t.Run("mcp artifacts", func(t *testing.T) {
		session := connectFixture(t, bin, fixtureHome(t, "[extensions.artifacts]\nenabled = true\n"+
			"base_url = \"https://artifacts.example.test\"\nkey_command = \"sh\"\n"))
		names := listTools(t, session)
		if !names["publish_artifact"] || !names["noop_ping"] || !names["rename"] {
			t.Fatalf("want the artifact tools beside the fixture's and the host's; got %v", names)
		}
	})

	// The fixture's driver captures an id with the session-file helpers
	// extension exports, so a module outside this one needs no copy of the
	// core's own. Each conversation file below is one those helpers must
	// get right: an id that is not a plain token, a directory that only
	// matches through a symlink, a conversation another session claimed,
	// one from another directory, and a tie that goes to the first listed.
	t.Run("tool driver capture", func(t *testing.T) {
		root := t.TempDir()
		work := filepath.Join(root, "work")
		elsewhere := filepath.Join(root, "elsewhere")
		for _, dir := range []string{work, elsewhere} {
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		link := filepath.Join(root, "link")
		if err := os.Symlink(work, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		resolved, err := filepath.EvalSymlinks(work)
		if err != nil {
			t.Fatal(err)
		}
		launch := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
		for name, rec := range map[string]struct {
			id, cwd string
			at      time.Duration
		}{
			"a-planted": {"abc; touch pwned", resolved, 0},
			"b-claimed": {"conv-claimed", resolved, 1},
			"c-foreign": {"conv-foreign", elsewhere, 1},
			"d-ours":    {"conv-ours", resolved, 2},
			"e-tied":    {"conv-tied", resolved, 2},
			"f-later":   {"conv-later", resolved, 3},
		} {
			line, err := json.Marshal(map[string]any{"id": rec.id, "cwd": rec.cwd, "created": launch.Add(rec.at * time.Second)})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(work, name+".echo.jsonl"), append(line, '\n'), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		session := connectFixture(t, bin, fixtureHome(t, ""))
		if got := callText(t, session, "noop_capture", map[string]any{"directory": link, "claimed": "conv-claimed"}); got != "captured conv-ours" {
			t.Fatalf("the driver's capture through the exported helpers answered %q, want the earliest unclaimed conversation in the linked directory", got)
		}
		if got := callText(t, session, "noop_capture", map[string]any{"directory": elsewhere}); got != "captured " {
			t.Fatalf("a directory with no conversation files captured %q", got)
		}
	})

	// A CLI the core has no code for, taught by the fixture's driver: a
	// migration off an "echo" session finds its transcript through the
	// driver, and the new session's launch registers the MCP server
	// through it.
	t.Run("tool driver", func(t *testing.T) {
		if _, err := exec.LookPath("tmux"); err != nil {
			t.Skip("a migration launches a tmux session")
		}
		// The launch runs on a named server under the fixture's own
		// TMUX_TMPDIR, which the cleanup ends by its full socket path.
		const echoTool = "tmux_socket = \"gitest-driver\"\n\n[tools.echo]\ncommand = \"sleep 60\"\nprompt_mode = \"send\"\nsession_store = \"echo\"\n"
		env := append(fixtureHome(t, echoTool), "GATE_INBOX_SESSION_ID=ca11e400")
		home := envValue(env, "GATE_INBOX_HOME")
		socket := filepath.Join(envValue(env, "TMUX_TMPDIR"), "tmux-"+strconv.Itoa(os.Getuid()), "gitest-driver")
		t.Cleanup(func() {
			kill := exec.Command("tmux", "-S", socket, "kill-server")
			kill.Env = env
			_ = kill.Run()
		})
		work := t.TempDir()
		seedEchoSessions(t, filepath.Join(home, "state.db"), work)

		migrate := exec.Command(bin, "migrate", "--tool", "echo", "--json", "ec400001")
		migrate.Env = env
		out, err := migrate.CombinedOutput()
		if err != nil {
			t.Fatalf("migrate: %v\n%s", err, out)
		}
		var moved struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(out, &moved); err != nil || moved.ID == "" {
			t.Fatalf("migrate answered %q: %v", out, err)
		}
		note, err := os.ReadFile(filepath.Join(home, "hooks", "echo-mcp-"+moved.ID))
		if err != nil {
			t.Fatalf("the driver registered no MCP server for the new session: %v", err)
		}
		if want := "gate-inbox "; !strings.HasPrefix(string(note), want) || !strings.HasSuffix(string(note), " mcp GATE_INBOX_SESSION_ID="+moved.ID) {
			t.Fatalf("the driver was handed %q", note)
		}
		st, err := store.Open(filepath.Join(home, "state.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		row, err := st.Get(moved.ID)
		if err != nil {
			t.Fatal(err)
		}
		transcript := filepath.Join(work, "conv-1.echo.jsonl")
		if prompt := strings.Join(row.PendingInputs, "\n"); !strings.Contains(prompt, transcript) || !strings.Contains(prompt, "One echo record per line.") {
			t.Fatalf("the new session was not pointed at the driver's transcript:\n%s", prompt)
		}

		refused := exec.Command(bin, "migrate", "--tool", "echo", "ec400001")
		refused.Env = append(fixtureHome(t, echoTool+"mcp = \"nope\"\n"), "GATE_INBOX_SESSION_ID=ca11e400")
		seedEchoSessions(t, filepath.Join(envValue(refused.Env, "GATE_INBOX_HOME"), "state.db"), work)
		out, err = refused.CombinedOutput()
		if err == nil || !strings.Contains(string(out), `mcp = "nope" is not a style this build has (built in: claude, codex, opencode, none; from extensions: echo)`) {
			t.Fatalf("a style nothing implements was not refused: %v\n%s", err, out)
		}
	})

	// A refused section switches off its own extension and nothing else:
	// the other extension and the host keep their tools, and the session's
	// instructions carry the reason.
	t.Run("mcp disables only the extension whose config is refused", func(t *testing.T) {
		session := connectFixture(t, bin, fixtureHome(t, "[extensions.noop]\ngreeting = \"hi\"\nbogus = 1\n\n[extensions.items]\nlabel = \"ace\"\n"))
		names := listTools(t, session)
		if names["noop_ping"] || !names["items_draw"] || !names["rename"] {
			t.Fatalf("want the host's and items' tools without noop's; got %v", names)
		}
		if got := callText(t, session, "items_draw", map[string]any{}); got != "ace" {
			t.Fatalf("items_draw answered %q", got)
		}
		instructions := session.InitializeResult().Instructions
		if !strings.Contains(instructions, "noop disabled: [extensions.noop]: unknown key(s): bogus") {
			t.Fatalf("the instructions do not say why noop is off:\n%s", instructions)
		}
	})

	// A section no extension owns is reported and disables nothing.
	t.Run("mcp warns about an unowned section", func(t *testing.T) {
		session := connectFixture(t, bin, fixtureHome(t, "[extensions.stranger]\nx = 1\n"))
		names := listTools(t, session)
		if !names["noop_ping"] || !names["items_draw"] {
			t.Fatalf("an unowned section cost the build's extensions their tools; got %v", names)
		}
		instructions := session.InitializeResult().Instructions
		if !strings.Contains(instructions, "ignored [extensions.stranger]: no extension in this build owns it (this build has: noop, artifacts, items, tally, extra)") {
			t.Fatalf("the instructions do not warn about the unowned section:\n%s", instructions)
		}
	})

	// A tool block naming an mcp style the build does not have is still
	// refused by the board itself, under script(1): sessions launched from
	// it would come up without the board's tools. An extension's refused
	// section no longer stops the board; the subtests above cover it.
	t.Run("tui refuses an unknown tool style", func(t *testing.T) {
		script, err := exec.LookPath("script")
		if err != nil {
			t.Skip("script(1) is needed to give the board a terminal")
		}
		// util-linux and BSD script(1) take the command differently.
		board := exec.Command(script, "-qec", bin, "/dev/null")
		if runtime.GOOS != "linux" {
			board = exec.Command(script, "-q", "/dev/null", bin)
		}
		board.Env = fixtureHome(t, "[tools.claude]\nmcp = \"nope\"\n")
		out, _ := board.CombinedOutput()
		if want := `tool claude: mcp = "nope" is not a style this build has`; !strings.Contains(string(out), want) {
			t.Fatalf("the board did not refuse the config with %q:\n%s", want, out)
		}
	})
}

// The board lends a BoardProvider its poll passes: an extension compiled
// outside this module is started with the board, told of a status change
// the first pass stores even though another of its subscribers panics on
// it, and stopped when the board quits. It also launches a helper of its own
// from the board, with a role, which its own spawn policy and launch
// environment see like any other spawn.
func TestExternalBuildRunsOnTheBoard(t *testing.T) {
	script, err := exec.LookPath("script")
	if err != nil {
		t.Skip("script(1) is needed to give the board a terminal")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("the board polls a tmux server")
	}
	bin := buildFixture(t)
	socket := tmuxtest.NewSocket("lifecycle")
	env := fixtureHome(t, "tmux_socket = \""+socket+"\"\n"+envEchoTool+shellTool+envDumpTool)
	home := envValue(env, "GATE_INBOX_HOME")
	logFile := filepath.Join(home, "board.log")
	traceFile := filepath.Join(home, "spans.jsonl")
	env = append(env, "GATE_INBOX_LOG_FILE="+logFile, "GATE_INBOX_LOG_LEVEL=info",
		"GATE_INBOX_TRACES=file:"+traceFile, "GATE_INBOX_TRACE_SAMPLE=1")
	// The board starts its own server under the scratch TMUX_TMPDIR, and it
	// is taken down there by path.
	t.Cleanup(func() { killTestServer(t, envValue(env, "TMUX_TMPDIR"), socket) })
	seedSessions(t, filepath.Join(home, "state.db"))
	data := filepath.Join(home, "extensions", "noop")
	// The operator's own terminal, nested under nobody, which the extension
	// must not be able to end.
	t.Setenv("TMUX_TMPDIR", envValue(env, "TMUX_TMPDIR"))
	operators := paintTerminal(t, filepath.Join(home, "state.db"), socket, "0be7a001", "")

	board := exec.Command(script, "-qec", bin, "/dev/null")
	if runtime.GOOS != "linux" {
		board = exec.Command(script, "-q", "/dev/null", bin)
	}
	board.Env = env
	var out strings.Builder
	board.Stdout, board.Stderr = &out, &out
	if err := board.Start(); err != nil {
		t.Fatal(err)
	}
	// exited is closed once the board is gone, so every reader sees it.
	exited := make(chan struct{})
	go func() {
		board.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		board.Process.Kill()
		<-exited
	})

	started := waitForFile(t, filepath.Join(data, "started.txt"), "", exited, &out)
	// The caller seeded as working has no pane, so the first pass stores it
	// dead; the child was dead already and does not move. The subscriber
	// names the session by reading it back through the Board it was lent.
	waitForFile(t, filepath.Join(data, "events.txt"), "ca11e400 working>dead \"\" the caller\n", exited, &out)
	waitForFile(t, filepath.Join(data, "passes.txt"), "c41d0001 dead", exited, &out)
	launched := waitForFile(t, filepath.Join(data, "launched.txt"), " noop/helper c41d0001\n", exited, &out)
	helper, _, _ := strings.Cut(launched, " ")
	waitForFile(t, filepath.Join(data, "spawned.txt"), helper+" extension noop/helper\n", exited, &out)
	if got := waitForFile(t, filepath.Join(data, "env-"+helper+".txt"), "", exited, &out); got != "spawn" {
		t.Fatalf("the helper's pane saw NOOP_LAUNCH=%q, want the extension's spawn", got)
	}
	// Its helper's status is the extension's to pin, and the pin is the
	// status file the board reads; a session it did not launch is refused.
	if got := waitForFile(t, filepath.Join(data, "pinned.txt"), "", exited, &out); got != "pinned; refused the child\n" {
		t.Fatalf("pinned.txt = %q", got)
	}
	if got, ok := hooks.NewManager(home).Read(helper); !ok || got != "waiting" {
		t.Fatalf("the helper's status file = %q, %v; want the pin", got, ok)
	}
	// Ending the helper removes that file, so the extension waits for this
	// before it messages and kills the helper.
	if err := os.WriteFile(filepath.Join(data, "pin-read.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// A worker it launched with no role is refused a pin until the
	// extension supervises it; then the board's passes hold it waiting,
	// and the subscriber is told as of any transition.
	supervised := waitForFile(t, filepath.Join(data, "supervised.txt"), "", exited, &out)
	if _, rest, _ := strings.Cut(strings.TrimSpace(supervised), " "); rest != "refused unclaimed pinned" {
		t.Fatalf("supervised.txt = %q", supervised)
	}
	waitForFile(t, filepath.Join(data, "events.txt"), ">waiting \"ask\" supervised\n", exited, &out)
	// The extension messages its helper, takes back a message still queued
	// behind that, ends the terminal opened under it, types it a command,
	// unparks it, plans its replacement, tries a held replacement and takes
	// it back, replaces it in its own seat through a hold it commits, ends
	// the replacement and files it away, through the board.
	waitForFile(t, filepath.Join(data, "sent.txt"), helper+" queued 1\n", exited, &out)
	waitForFile(t, filepath.Join(data, "withdrew.txt"), helper+" withdrew 1\n", exited, &out)
	waitForFile(t, filepath.Join(data, "large.txt"), helper+" full queued, over too large true\n", exited, &out)
	// It speaks as the operator too, which the matching subject does not let
	// replace its fenced message.
	waitForFile(t, filepath.Join(data, "voiced.txt"), helper+" voiced true superseded 0\n", exited, &out)
	nested := paintTerminal(t, filepath.Join(home, "state.db"), socket, "7e770003", helper)
	waitForFile(t, filepath.Join(data, "terminals.txt"), "7e770003 listed true\n7e770003 ended dead false\n0be7a001 refused\n", exited, &out)
	if nested.Exists("7e770003") || !operators.Exists("0be7a001") {
		t.Fatal("want the nested terminal's pane ended and the operator's kept")
	}
	waitForFile(t, filepath.Join(data, "command.txt"), helper+" at-prompt false read false\n", exited, &out)
	waitForFile(t, filepath.Join(data, "unparked.txt"), helper+" parked false\n", exited, &out)
	aborted := waitForFile(t, filepath.Join(data, "aborted.txt"), " held-under "+helper+" | refused true | gone true | replaced-by \"\" | old ", exited, &out)
	if !strings.HasSuffix(aborted, " true\n") {
		t.Fatalf("aborted.txt = %q, want the helper still running after the abort", aborted)
	}
	if counts := queuedCounts(t, filepath.Join(home, "state.db")); counts[helper] != 3 {
		t.Fatalf("queued = %v, want the helper's message still its own after the abort", counts)
	}
	if err := os.WriteFile(filepath.Join(data, "go-commit"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	held := waitForFile(t, filepath.Join(data, "held.txt"), " held-under "+helper+" | old running true\n", exited, &out)
	waitForFile(t, filepath.Join(data, "settled.txt"), "commit again <nil> | abort after true\n", exited, &out)
	replaced := waitForFile(t, filepath.Join(data, "replaced.txt"), " helper noop/helper c41d0001 dead replaced-by ", exited, &out)
	fresh, _, _ := strings.Cut(replaced, " ")
	if fresh == helper || strings.HasPrefix(fresh, "error") || !strings.HasPrefix(held, fresh+" ") {
		t.Fatalf("replaced.txt = %q after held.txt = %q, want the held session in the helper's seat", replaced, held)
	}
	if !strings.HasSuffix(replaced, " replaced-by "+fresh+"\n") {
		t.Fatalf("replaced.txt = %q, want the retired helper to name %s as what replaced it", replaced, fresh)
	}
	if counts := queuedCounts(t, filepath.Join(home, "state.db")); counts[fresh] != 3 || counts[helper] != 0 {
		t.Fatalf("queued = %v, want the helper's message forwarded at the commit", counts)
	}
	if got := waitForFile(t, filepath.Join(data, "env-"+fresh+".txt"), "", exited, &out); got != "replace "+helper {
		t.Fatalf("the replacement's pane saw NOOP_LAUNCH=%q, want a replace from the helper", got)
	}
	checkPlanWasLaunched(t, home, data, helper, fresh)
	waitForFile(t, filepath.Join(data, "killed.txt"), fresh+" dead false\n", exited, &out)
	waitForFile(t, filepath.Join(data, "archived.txt"), fresh+" archived true\n", exited, &out)
	pendingLine := waitForFile(t, filepath.Join(data, "pending.txt"), " held-under "+helper+"\n", exited, &out)
	pending, _, _ := strings.Cut(pendingLine, " ")
	// Every kill the board made of an agent session is heard, as the
	// board's; the terminal it ended is not a session's kill.
	kills := waitForFile(t, filepath.Join(data, "kills.txt"), fresh+" board  dead\n", exited, &out)
	if strings.Contains(kills, "7e770003") {
		t.Fatalf("a terminal's end was reported as a kill:\n%s", kills)
	}

	var pid int
	if _, err := fmt.Sscan(started, &pid); err != nil {
		t.Fatalf("started.txt = %q: %v", started, err)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		t.Fatalf("signal the board: %v", err)
	}
	if got := waitForFile(t, filepath.Join(data, "stopped.txt"), "", exited, &out); got != "true\n" {
		t.Fatalf("stopped.txt = %q, want the stop called after the board's context was cancelled", got)
	}
	// The hold the extension left open was aborted as it stopped: the
	// pending session is gone, and the seat it waited on is as it was.
	<-exited
	st, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Get(pending); err == nil {
		t.Fatalf("the unsettled replacement %s outlived the extension that held it", pending)
	}
	if seat, err := st.Get(helper); err != nil || seat.ParentID != "c41d0001" || seat.Status != "dead" {
		t.Fatalf("the seat the abort left = %+v, %v; want it untouched", seat, err)
	}
	events, _ := os.ReadFile(filepath.Join(data, "events.txt"))
	if strings.Count(string(events), "working>dead") != 1 {
		t.Fatalf("the transition was reported more than once:\n%s", events)
	}
	// The extension's line is in the board's own log, tagged with its id
	// and scrubbed, once the board has exited and flushed it.
	select {
	case <-exited:
	case <-time.After(20 * time.Second):
		t.Fatalf("the board did not exit after SIGTERM:\n%s", out.String())
	}
	logged, _ := os.ReadFile(logFile)
	if !strings.Contains(string(logged), `msg="noop on the board" extension=noop note="key [redacted]"`) {
		t.Fatalf("the extension's line is not in the board log, tagged and scrubbed:\n%s", logged)
	}
	// Its span is in the board's trace, named under its id.
	traced, _ := os.ReadFile(traceFile)
	if !strings.Contains(string(traced), `"name":"noop.start"`) || !strings.Contains(string(traced), `"key":"extension","value":{"stringValue":"noop"}`) {
		t.Fatalf("the extension's span is not in the board's trace, named and tagged with its id:\n%s", traced)
	}
}

// envDumpTool writes out, NUL-separated, the environment and the arguments
// its pane was started with, before the launch reason envecho writes.
const envDumpTool = `
[tools.envdump]
command = "sh -c 'env -0 > \"$NOOP_OUT.env\"; printf \"%s\\0\" \"$0\" \"$@\" > \"$NOOP_OUT.argv\"; printf %s \"$NOOP_LAUNCH\" > \"$NOOP_OUT\"; exec sleep 30' --"
default_status = "idle"
activity_cutoff = "(?m)^\\$ "
`

// checkPlanWasLaunched holds the plan the extension read before replacing
// helper against the pane that replaced it as fresh: running the planned
// command gives the pane's arguments, every planned variable is in the
// pane's environment with the planned value, and the id minted for the
// plan is the only difference. Reading the plan moved nothing on the board
// and filed nothing of its own.
func checkPlanWasLaunched(t *testing.T, home, data, helper, fresh string) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(data, "plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	var planned struct {
		Plan struct {
			SessionID string
			Command   string
			Env       map[string]string
		}
		Before, After []string
	}
	if err := json.Unmarshal(body, &planned); err != nil {
		t.Fatalf("plan.json: %v\n%s", err, body)
	}
	plan := planned.Plan
	if plan.SessionID == "" || plan.SessionID == fresh || plan.SessionID == helper {
		t.Fatalf("plan id %q, want one of its own (helper %s, replacement %s)", plan.SessionID, helper, fresh)
	}
	if !slices.Equal(planned.Before, planned.After) || !slices.Contains(planned.After, helper+" true") {
		t.Fatalf("the board moved while the plan was read:\nbefore %v\nafter  %v", planned.Before, planned.After)
	}
	st, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Get(plan.SessionID); err == nil {
		t.Fatalf("the plan filed a row for %s", plan.SessionID)
	}
	if borrower, _ := st.Setting("account_borrower:" + plan.SessionID); borrower != "" {
		t.Fatalf("the plan recorded a borrower for %s", plan.SessionID)
	}
	asLaunched := func(s string) string { return strings.ReplaceAll(s, plan.SessionID, fresh) }
	fields := func(path string) []string {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimSuffix(string(body), "\x00"), "\x00")
	}

	launched := map[string]string{}
	for _, entry := range fields(filepath.Join(data, "env-"+fresh+".txt.env")) {
		key, value, _ := strings.Cut(entry, "=")
		launched[key] = value
	}
	for key, value := range plan.Env {
		// The pane's shell sets _ to the command it last ran, whatever it
		// was started with.
		if key == "_" {
			continue
		}
		got, set := launched[key]
		if want := asLaunched(value); got != want || !set && want != "" {
			t.Errorf("the pane's %s = %q (set %v), the plan's %q", key, got, set, want)
		}
	}

	// The planned command, run by hand with the planned environment, writes
	// its arguments where the plan's own id sends them.
	rehearsal := exec.Command("sh", "-c", plan.Command)
	for key, value := range plan.Env {
		rehearsal.Env = append(rehearsal.Env, key+"="+value)
	}
	if err := rehearsal.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		rehearsal.Process.Kill()
		rehearsal.Wait()
	})
	written := filepath.Join(data, "env-"+plan.SessionID+".txt")
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(25 * time.Millisecond) {
		if _, err := os.Stat(written); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the planned command never ran: %q", plan.Command)
		}
	}
	want := fields(filepath.Join(data, "env-"+fresh+".txt.argv"))
	got := fields(written + ".argv")
	for i := range got {
		got[i] = asLaunched(got[i])
	}
	if !slices.Equal(got, want) {
		t.Fatalf("the planned command's arguments are %q, the pane's %q", got, want)
	}
}

// waitForFile waits for path to exist and hold want, and returns what it
// holds. It fails early if the board exits without writing it, and reads
// once more after the exit: the board writes and exits between two polls.
func waitForFile(t *testing.T, path, want string, exited <-chan struct{}, out *strings.Builder) string {
	t.Helper()
	holds := func() (string, bool) {
		body, err := os.ReadFile(path)
		return string(body), err == nil && len(body) > 0 && strings.Contains(string(body), want)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if body, ok := holds(); ok {
			return body
		}
		select {
		case <-exited:
			if body, ok := holds(); ok {
				return body
			}
			t.Fatalf("the board exited before %s held %q:\n%s", filepath.Base(path), want, out.String())
		case <-time.After(50 * time.Millisecond):
		}
	}
	body, _ := holds()
	t.Fatalf("%s holds %q, want %q; board output:\n%s", filepath.Base(path), body, want, out.String())
	return ""
}

// An extension compiled outside this module adds a CLI command: it runs
// from the built binary after its config is read, is listed in help under
// its own heading, and a name the core already answers to stops every face.
func TestExternalBuildRunsExtensionCommands(t *testing.T) {
	bin := buildFixture(t)
	run := func(t *testing.T, env []string, args ...string) (string, int) {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return string(out), exit.ExitCode()
		}
		if err != nil {
			t.Fatalf("run %v: %v", args, err)
		}
		return string(out), 0
	}

	t.Run("runs configured", func(t *testing.T) {
		env := fixtureHome(t, "[extensions.noop]\ngreeting = \"hi\"\n")
		out, code := run(t, env, "noop-echo", "a", "b")
		want := "hi: a b (config in " + filepath.Base(envValue(env, "GATE_INBOX_HOME")) + ")\n"
		if code != 0 || out != want {
			t.Fatalf("noop-echo exited %d with %q, want %q", code, out, want)
		}
	})

	t.Run("its own usage and errors", func(t *testing.T) {
		env := fixtureHome(t, "")
		if out, code := run(t, env, "noop-echo", "-h"); code != 0 || !strings.Contains(out, "usage: gate-inbox noop-echo") {
			t.Fatalf("noop-echo -h exited %d with %q", code, out)
		}
		if out, code := run(t, env, "noop-echo"); code != 1 || !strings.Contains(out, "noop-echo needs words") {
			t.Fatalf("noop-echo with no words exited %d with %q", code, out)
		}
	})

	t.Run("refused config stops it", func(t *testing.T) {
		out, code := run(t, fixtureHome(t, "[extensions.noop]\nbogus = 1\n"), "noop-echo", "a")
		if code != 1 || !strings.Contains(out, "[extensions.noop]: unknown key(s): bogus") || strings.Contains(out, "a (config") {
			t.Fatalf("noop-echo under a refused config exited %d with %q", code, out)
		}
	})

	t.Run("help", func(t *testing.T) {
		out, code := run(t, fixtureHome(t, ""), "help")
		section := "\nNoop fixture\n  gate-inbox noop-echo <words...>\n      print the words after the configured greeting\n"
		if code != 0 || !strings.Contains(out, section) {
			t.Fatalf("help exited %d without the extension's section:\n%s", code, out)
		}
		if strings.Index(out, section) > strings.Index(out, "\nOptions:") {
			t.Fatalf("the extension's section is not listed before the options:\n%s", out)
		}
	})

	// A command typed in the operator's own shell, which is no session,
	// reads any session by id with the operator's reach, and is refused
	// the acts that need a calling session. From a session's shell the
	// Host acts as that session.
	t.Run("reach from an operator's shell", func(t *testing.T) {
		if _, err := exec.LookPath("tmux"); err != nil {
			t.Skip("the session services open a tmux driver")
		}
		env := fixtureHome(t, "tmux_socket = \""+tmuxtest.NewSocket("operator")+"\"\n")
		seedSessions(t, filepath.Join(envValue(env, "GATE_INBOX_HOME"), "state.db"))
		operator := slices.DeleteFunc(slices.Clone(env), func(entry string) bool {
			return strings.HasPrefix(entry, "GATE_INBOX_SESSION_ID=")
		})
		out, code := run(t, operator, "noop-who", "c41d0001", "hello")
		want := `caller "" | ca11e400,c41d0001 | the child` + "\nsend refused: "
		if code != 0 || !strings.HasPrefix(out, want) || !strings.Contains(out, "GATE_INBOX_SESSION_ID is unset") {
			t.Fatalf("noop-who from an operator's shell exited %d with %q, want it to start %q and refuse the send", code, out, want)
		}
		out, code = run(t, append(env, "GATE_INBOX_SESSION_ID=ca11e400"), "noop-who", "c41d0001")
		if want := `caller "ca11e400" | ca11e400,c41d0001 | the child` + "\n"; code != 0 || out != want {
			t.Fatalf("noop-who from a session's shell exited %d with %q, want %q", code, out, want)
		}
	})

	// The Host a command is given plans a replacement as the board would
	// launch it for this extension, and launches nothing.
	t.Run("plans a replacement", func(t *testing.T) {
		if _, err := exec.LookPath("tmux"); err != nil {
			t.Skip("the session services open a tmux driver")
		}
		env := fixtureHome(t, "tmux_socket = \""+tmuxtest.NewSocket("plan")+"\"\n"+envEchoTool)
		state := filepath.Join(envValue(env, "GATE_INBOX_HOME"), "state.db")
		seedSessions(t, state)
		out, code := run(t, append(env, "GATE_INBOX_SESSION_ID=ca11e400"), "noop-plan", "c41d0001")
		if want := "new id true | carried true | prompt true\n"; code != 0 || out != want {
			t.Fatalf("noop-plan exited %d with %q, want %q", code, out, want)
		}
		st, err := store.Open(state)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		if rows, _ := st.ListSessions(true); len(rows) != 2 {
			t.Fatalf("the board holds %d rows after a plan, want the 2 it was seeded with", len(rows))
		}
	})

	t.Run("a clash with a core command stops every face", func(t *testing.T) {
		env := append(fixtureHome(t, ""), "NOOP_FIXTURE_CLASH=1")
		for _, args := range [][]string{{"--version"}, {"noop-echo", "a"}, {"help"}} {
			out, code := run(t, env, args...)
			if code != 1 || !strings.Contains(out, `extension "noop": command "task" is already a command of the core`) {
				t.Fatalf("%v with a clashing command exited %d with %q", args, code, out)
			}
		}
	})
}

// TestExternalBuildSnippetDefaultsReachRun proves a module outside this one
// hands its snippets to the board through app.Options alone. The main
// fixture carries a valid entry and its faces run above; this build carries
// one that could never bind, and Run refuses it before any face does.
func TestExternalBuildSnippetDefaultsReachRun(t *testing.T) {
	bin := buildModule(t, badSnippetSource)
	env := fixtureHome(t, "")
	version := exec.Command(bin, "--version")
	version.Env = env
	out, err := version.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("--version with a snippet default that cannot bind: %v\n%s", err, out)
	}
	for _, want := range []string{"snippet defaults", "key “1” must be a single letter"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("the refusal does not say %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(envValue(env, "GATE_INBOX_HOME"), "snippets.json")); !os.IsNotExist(err) {
		t.Fatalf("a refused build touched snippets.json: %v", err)
	}
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

// shellTool is a terminal tool for config.toml.
const shellTool = `
[tools.shell]
command = ""
shell = true
default_status = "idle"
`

// paintTerminal files a terminal nested under parent, or under nobody when
// parent is empty, and runs a shell-less pane for it on socket.
func paintTerminal(t *testing.T, path, socket, id, parent string) *tmux.Driver {
	t.Helper()
	driver, err := tmux.NewWithSocket(socket)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := driver.Create(id, dir, "sleep 60", nil, 80, 24); err != nil {
		t.Fatalf("create %s: %v", id, err)
	}
	t.Cleanup(func() { _ = driver.Kill(id) })
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sess := store.Session{ID: id, Name: "sh " + id, Tool: "shell", Status: "idle", Cwd: dir, ParentID: parent, CreatedAt: time.Now()}
	// Filed as a leaf, as create_terminal files one, so it may hang under
	// a session that is itself a child.
	if err := st.LaunchSessionLeaf(sess, func() error { return nil }); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
	return driver
}

// seedEchoSessions writes the calling session and an echo session with a
// captured conversation in dir into the state database at path.
func seedEchoSessions(t *testing.T, path, dir string) {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now()
	for _, sess := range []store.Session{
		{ID: "ca11e400", Name: "the caller", Tool: "claude", Status: "working", CreatedAt: now},
		{ID: "ec400001", Name: "the echo", Tool: "echo", Cwd: dir, Status: "idle", AgentSessionID: "conv-1", CreatedAt: now},
	} {
		if err := st.CreateSession(sess); err != nil {
			t.Fatalf("seed %s: %v", sess.ID, err)
		}
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
