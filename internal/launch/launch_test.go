// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package launch

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/opencode"
	"github.com/usestring/gate-inbox/internal/tmux"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// TestMain clears the TMUX/TMUX_PANE this process inherited from whatever real
// tmux the developer is running in -- several driver paths act on what they
// find there, and the pane they would find is the operator's own, on tmux's
// default server -- and sweeps control clients that earlier runs left behind
// on sockets whose servers are already gone. kill-server cannot collect those,
// which is why they accumulate.
func TestMain(m *testing.M) {
	tmuxtest.ClearInheritedTmuxEnv()
	tmuxtest.ReapStrays()
	os.Exit(m.Run())
}

func TestPromptInjectsDirectiveOnlyForAutoNamedWithPrompt(t *testing.T) {
	withDirective := Prompt("", "build the api", true, false)
	if !strings.HasPrefix(withDirective, RenameDirective+"\n\n") || !strings.HasSuffix(withDirective, "build the api") {
		t.Fatalf("auto-named prompt should carry the directive, got %q", withDirective)
	}
	named := Prompt("", "build the api", false, false)
	if !strings.HasPrefix(named, RenameAvailableNote+"\n\n") || !strings.HasSuffix(named, "build the api") {
		t.Fatalf("custom-named prompt should note rename is optional later, got %q", named)
	}
	if strings.Contains(named, "Run rename only this once") || strings.HasPrefix(named, RenameDirective) {
		t.Fatalf("custom-named prompt must not force a rename, got %q", named)
	}
	if got := Prompt("", "", true, false); got != "" {
		t.Fatalf("promptless session should stay clean, got %q", got)
	}
	if got := Prompt("", "/compact keep the api notes", true, false); got != "/compact keep the api notes" {
		t.Fatalf("slash-command prompt should stay clean, got %q", got)
	}
	if got := Prompt("", "/compact keep the api notes", false, false); got != "/compact keep the api notes" {
		t.Fatalf("named slash-command prompt should stay clean, got %q", got)
	}
}

func TestPromptLeavesAnOptedOutLaunchAlone(t *testing.T) {
	if got := Prompt("", "build the api", true, true); got != "build the api" {
		t.Fatalf("an opted-out auto-named prompt should stay clean, got %q", got)
	}
	if got := Prompt("", "build the api", false, true); got != "build the api" {
		t.Fatalf("an opted-out named prompt should stay clean, got %q", got)
	}
	if got := Prompt(CoordinationNote, "build the api", true, true); got != CoordinationNote+"\n\nbuild the api" {
		t.Fatalf("an opted-out launch keeps only the coordination note, got %q", got)
	}
	if got := Prompt("", "/compact keep the api notes", true, true); got != "/compact keep the api notes" {
		t.Fatalf("an opted-out slash prompt should stay clean, got %q", got)
	}
}

// opencode v2's --prompt fills the composer and never submits it, and the
// poll that pressed Enter for it could not tell the manager's prompt from a
// line the operator was writing. So the shipped config types the opening
// prompt in as a pending input, the path a message from another session
// takes, submitted by the Enter that follows the manager's own paste.
func TestOpencodeOpeningPromptIsTypedInNotPassedAsAFlag(t *testing.T) {
	cfg, err := config.LoadDir(t.TempDir())
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	tool := cfg.Tools["opencode"]
	plan, err := Assemble("opencode", tool, "build the api", "", false, "", "")
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if strings.Contains(plan.Command, "--prompt") || strings.Contains(plan.Command, "build the api") {
		t.Fatalf("the prompt rode the command line, where v2 leaves it unsubmitted: %q", plan.Command)
	}
	if plan.LaunchPrompt != "" {
		t.Fatalf("a launch prompt was recorded for the pending-input gate to wait on: %q", plan.LaunchPrompt)
	}
	if len(plan.PendingInputs) == 0 || plan.PendingInputs[0] != "build the api" {
		t.Fatalf("the prompt is not the first pending input: %q", plan.PendingInputs)
	}
}

func TestAssembleSkipsEveryDirectiveForOptedOutTools(t *testing.T) {
	tool := config.Tool{Command: "opencode", PromptFlag: "--prompt", SkipRenameDirective: true}
	plan, err := Assemble("opencode", tool, "build the api", "", true, "", "")
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if strings.Contains(plan.Command, RenameDirective) || strings.Contains(plan.Command, RenameAvailableNote) {
		t.Fatalf("an opted-out command carries a directive: %q", plan.Command)
	}
	if want := "opencode --prompt 'build the api'"; plan.Command != want {
		t.Fatalf("command = %q, want %q", plan.Command, want)
	}
	if len(plan.PendingInputs) != 0 {
		t.Fatalf("an embeddable directive needs no pending input, got %v", plan.PendingInputs)
	}

	deferred, err := Assemble("opencode", tool, "/compact the notes", "", true, "", "")
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(deferred.PendingInputs) != 0 {
		t.Fatalf("a slash-command launch on an opted-out tool should queue nothing, got %v", deferred.PendingInputs)
	}

	promptless, err := Assemble("opencode", tool, "", "", true, "", "")
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(promptless.PendingInputs) != 0 {
		t.Fatalf("a promptless launch on an opted-out tool should queue nothing, got %v", promptless.PendingInputs)
	}
}

func TestWithPromptComposesPerToolStyle(t *testing.T) {
	flagged := config.Tool{Command: "opencode", PromptFlag: "--prompt"}
	if got := WithPrompt(flagged, flagged.Command, "do it"); got != "opencode --prompt 'do it'" {
		t.Fatalf("flagged compose = %q", got)
	}
	bare := config.Tool{Command: "cat"}
	if got := WithPrompt(bare, bare.Command, "do it"); got != "cat 'do it'" {
		t.Fatalf("bare compose = %q", got)
	}
	if got := WithPrompt(bare, bare.Command, ""); got != "cat" {
		t.Fatalf("empty prompt should leave the command untouched, got %q", got)
	}
	sent := config.Tool{Command: "hermes --cli", PromptMode: "send"}
	if got := WithPrompt(sent, sent.Command, "do it"); got != sent.Command {
		t.Fatalf("send-mode prompt changed launch command to %q", got)
	}
}

func TestAssembleRoutesPromptAndDirective(t *testing.T) {
	flagged := config.Tool{Command: "claude", PromptFlag: "-p", SessionIDFlag: "--session-id"}
	plan, _ := Assemble("claude", flagged, "build the api", "", true, "", "")
	if !strings.HasPrefix(plan.Command, "claude -p '"+RenameDirective) {
		t.Fatalf("auto-named flagged command = %q", plan.Command)
	}
	if plan.AgentSessionID == "" || !strings.Contains(plan.Command, "--session-id "+plan.AgentSessionID) {
		t.Fatalf("command should carry the chosen session id, got %q", plan.Command)
	}
	if len(plan.PendingInputs) != 0 {
		t.Fatalf("an embeddable directive needs no pending input, got %v", plan.PendingInputs)
	}

	deferred, _ := Assemble("claude", flagged, "/compact the notes", "", true, "", "")
	if len(deferred.PendingInputs) != 1 || deferred.PendingInputs[0] != DeferredRenameDirective {
		t.Fatalf("slash-command launch should defer the directive, got %v", deferred.PendingInputs)
	}
	// It lands where the user's own typing goes, so it says whose words it is.
	if !strings.HasPrefix(DeferredRenameDirective, ManagerBand) ||
		!strings.HasPrefix(AdoptedRenameDirective("am rename"), ManagerBand) {
		t.Fatal("a standalone directive must lead with the manager band")
	}

	sent := config.Tool{Command: "hermes", PromptMode: "send"}
	typed, _ := Assemble("hermes", sent, "build the api", "", false, "", "")
	if typed.Command != "hermes" {
		t.Fatalf("send-mode command = %q", typed.Command)
	}
	if len(typed.PendingInputs) != 1 || !strings.HasSuffix(typed.PendingInputs[0], "build the api") {
		t.Fatalf("send-mode prompt should be typed in, got %v", typed.PendingInputs)
	}
	if typed.AgentSessionID != "" {
		t.Fatalf("a tool without a session-id flag must not get one, got %q", typed.AgentSessionID)
	}
}

func TestAssembleNotesCoordinationOnlyForToolsWithoutMCP(t *testing.T) {
	noClient := config.Tool{Command: "pi", PromptFlag: "-p"}
	carried, _ := Assemble("pi", noClient, "build the api", "", false, "", "")
	if !strings.Contains(carried.Command, CoordinationNote) {
		t.Fatalf("a tool with no MCP client should be pointed at the subcommands, got %q", carried.Command)
	}
	if len(carried.PendingInputs) != 0 {
		t.Fatalf("a note the prompt carried needs no pending input, got %v", carried.PendingInputs)
	}
	// The command line is the prompt the spawn asked for, so nothing on it
	// announces itself as the manager's own words.
	if strings.Contains(carried.Command, ManagerBand) {
		t.Fatalf("a carried note must not be banded, got %q", carried.Command)
	}

	withClient := config.Tool{Command: "claude", PromptFlag: "-p"}
	if plan, _ := Assemble("claude", withClient, "build the api", "", false, "", ""); strings.Contains(plan.Command, CoordinationNote) {
		t.Fatalf("a tool whose MCP tool descriptions say this already must not repeat it, got %q", plan.Command)
	}

	// A slash command carries neither, so both queue, and the order is what
	// the agent reads: the directive ends on "Then continue.", and the note
	// is what it continues into.
	deferred, _ := Assemble("pi", noClient, "/compact the notes", "", true, "", "")
	if len(deferred.PendingInputs) != 2 ||
		deferred.PendingInputs[0] != DeferredRenameDirective || deferred.PendingInputs[1] != ManagerBand+CoordinationNote {
		t.Fatalf("a launch needing both should queue them in reading order, got %v", deferred.PendingInputs)
	}

	promptless, _ := Assemble("pi", noClient, "", "", false, "", "")
	if promptless.Command != noClient.Command {
		t.Fatalf("a promptless launch command should stay clean, got %q", promptless.Command)
	}
	if len(promptless.PendingInputs) != 1 || promptless.PendingInputs[0] != ManagerBand+CoordinationNote {
		t.Fatalf("a prompt that cannot carry the note should queue it, got %v", promptless.PendingInputs)
	}
}

func TestReviveCommandResumesTheConversationItHeld(t *testing.T) {
	full := config.Tool{
		Command:           "claude",
		ReviveCommand:     "claude --continue",
		ResumeByIDCommand: "claude --resume {id}",
	}
	for _, tc := range []struct {
		name           string
		tool           config.Tool
		agentSessionID string
		want           string
	}{
		{"a captured id resumes that conversation", full, "abc-123", "claude --resume 'abc-123'"},
		{"no captured id falls back to the newest one", full, "", "claude --continue"},
		{"a tool with no revive of its own starts fresh", config.Tool{Command: "pi"}, "abc-123", "pi"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := mustRevive(t, tc.tool, tc.agentSessionID, ""); got != tc.want {
				t.Fatalf("ReviveCommand = %q, want %q", got, tc.want)
			}
		})
	}
}

// A conversation id is read from the agent CLI's own store, so it is not
// ours to trust. Spliced raw it would need no quote to break out of: a
// semicolon ends the resume command and starts whatever follows.
func TestReviveCommandQuotesAnIDThatSpellsAnotherCommand(t *testing.T) {
	tool := config.Tool{ResumeByIDCommand: "codex resume {id}"}
	for _, tc := range []struct{ name, id, want string }{
		{"a command after a semicolon", `abc; touch pwned`, `codex resume 'abc; touch pwned'`},
		{"an id closing the quote itself", `abc'; touch pwned; echo '`, `codex resume 'abc'\''; touch pwned; echo '\'''`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := mustRevive(t, tool, tc.id, ""); got != tc.want {
				t.Fatalf("ReviveCommand = %q, want %q", got, tc.want)
			}
		})
	}
}

// The quoting has to hold against a real shell, not just against a string
// comparison: the revive command reaches one through tmux.
func TestReviveCommandKeepsACapturedIDOutOfTheShell(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "pwned")
	id := `abc; touch ` + marker

	command := mustRevive(t, config.Tool{ResumeByIDCommand: "echo resume {id}"}, id, "")
	socket := tmuxtest.NewSocket("launch")
	driver, err := tmux.NewWithSocket(socket)
	if err != nil {
		t.Fatalf("driver: %v", err)
	}
	// This test used to name one fixed socket, never close the driver and
	// never take the server down: it left a tmux server and the control
	// client its capture opened running after every run.
	t.Cleanup(func() {
		driver.CloseCaptureClients()
		tmuxtest.KillServer(socket)
		tmuxtest.ReapSocket(socket)
	})
	sessID := "revive-quote-probe"
	// Wide enough that the echoed id lands on one row, since a wrapped
	// capture would split the text the assertion looks for.
	if err := driver.Create(sessID, dir, command, map[string]string{}, 400, 24); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = driver.Kill(sessID) })

	echoed := "resume " + id
	deadline := time.Now().Add(10 * time.Second)
	var pane string
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			pane, _ = driver.CapturePane(sessID)
			t.Fatalf("the id ran as a shell command: %s was created; pane:\n%s", marker, strings.TrimSpace(pane))
		}
		pane, _ = driver.CapturePane(sessID)
		if strings.Contains(pane, echoed) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Echoing the whole id as one argument is what proves the command ran
	// and the shell read the id as text, rather than nothing having run.
	if !strings.Contains(pane, echoed) {
		t.Fatalf("the resume command never echoed the id as one argument; pane:\n%s", strings.TrimSpace(pane))
	}
}

func TestEnvironmentCarriesSessionIDAndHooks(t *testing.T) {
	manager := hooks.NewManager(t.TempDir())
	// A board spawned from inside a managed session carries that session's
	// status file; neither launch below may inherit it.
	t.Setenv(hooks.EnvStatusFile, "/parent/session.status")

	plain := config.Tool{Command: "cat"}
	command, env, err := Environment(manager, "plain", plain, plain.Command, "abcd1234", "", "", nil)
	if err != nil {
		t.Fatalf("Environment: %v", err)
	}
	if env[hooks.EnvSessionID] != "abcd1234" {
		t.Fatalf("plain tool env = %v, want session id", env)
	}
	if env[hooks.EnvStatusFile] != "" {
		t.Fatalf("a tool without claude hooks must not get a status file, got %q", env[hooks.EnvStatusFile])
	}
	if command != plain.Command {
		t.Fatalf("a tool with no MCP style should launch untouched, got %q", command)
	}

	hooked := config.Tool{Command: "cat", StatusSource: hooks.StatusSourceClaude, MCP: "claude"}
	command, env, err = Environment(manager, "hooked", hooked, hooked.Command, "abcd1234", "", "", nil)
	if err != nil {
		t.Fatalf("Environment hooked: %v", err)
	}
	if env[hooks.EnvSessionID] != "abcd1234" || env[hooks.EnvStatusFile] == "" {
		t.Fatalf("hooked tool env = %v, want session id and status file", env)
	}
	if !strings.Contains(command, "--mcp-config '") || !strings.Contains(command, "--settings '") {
		t.Fatalf("hooked command = %q", command)
	}
}

// An adopted pane inherited none of the manager's environment, so the command
// has to carry every value the rename subcommand would otherwise read from it.
func TestAdoptedRenameCommandCarriesWhatThePaneNeverInherited(t *testing.T) {
	command := AdoptedRenameCommand("/opt/bin/gate inbox", "/home/dev/.config/gate-inbox", "1b364bb3")
	want := `GATE_INBOX_HOME='/home/dev/.config/gate-inbox' GATE_INBOX_SESSION_ID='1b364bb3' ` +
		`'/opt/bin/gate inbox' rename "<name>"`
	if command != want {
		t.Fatalf("got  %s\nwant %s", command, want)
	}
	// A bare NAME=value is only an assignment when the name is unquoted.
	for _, name := range []string{"GATE_INBOX_HOME", "GATE_INBOX_SESSION_ID"} {
		if strings.Contains(command, "'"+name+"'") {
			t.Fatalf("%s must not be quoted or the shell reads it as a command", name)
		}
	}
}

func TestAdoptedRenameDirectiveIsOneLineAndSaysWhoIsAsking(t *testing.T) {
	directive := AdoptedRenameDirective(`gate-inbox rename "<name>"`)
	if strings.Contains(directive, "\n") {
		t.Fatal("the directive is pasted into a live composer, so it stays on one line")
	}
	if !strings.HasPrefix(directive, "[gate-inbox]") {
		t.Fatalf("an unasked-for message must say it is not the user, got %q", directive)
	}
	if !strings.Contains(directive, "not from the user") {
		t.Fatalf("got %q", directive)
	}
	if !strings.Contains(directive, `gate-inbox rename "<name>"`) {
		t.Fatalf("the directive must carry the command it is asking for, got %q", directive)
	}
}

// The manager runs from its own checkout and is on nobody's PATH, so a
// session told to type the bare name gets "command not found" -- which is
// what every rename directive said, and why no session on a live board had
// ever carried a name its own agent chose. Its path has to travel with the
// session, and the directives have to name that path rather than the binary.
func TestEnvironmentCarriesTheManagersOwnPath(t *testing.T) {
	manager := hooks.NewManager(t.TempDir())
	tool := config.Tool{Command: "cat"}
	_, env, err := Environment(manager, "plain", tool, tool.Command, "abcd1234", "", "", nil)
	if err != nil {
		t.Fatalf("Environment: %v", err)
	}
	binary := env[hooks.EnvExecutable]
	if binary == "" {
		t.Fatalf("no %s in the session env: %v", hooks.EnvExecutable, env)
	}
	if binary != Executable() {
		t.Fatalf("%s = %q, want the running manager at %q", hooks.EnvExecutable, binary, Executable())
	}
	for _, directive := range []string{RenameDirective, DeferredRenameDirective, RenameAvailableNote, CoordinationNote} {
		if !strings.Contains(directive, "$"+hooks.EnvExecutable) {
			t.Fatalf("a directive still names the binary instead of its path: %q", directive)
		}
	}
}

// A relocated board reaches the worker, because anything the session runs
// resolves the config dir against the default when the variable is unset. The
// negative half is the load-bearing one: composing a default that was never set would put a stale absolute path
// in every worker's environment on a board that never relocated.
func TestEnvironmentCarriesARelocatedHomeAndInventsNoDefault(t *testing.T) {
	manager := hooks.NewManager(t.TempDir())
	tool := config.Tool{Command: "cat"}
	home := t.TempDir()
	t.Setenv(config.HomeEnv, home)
	_, env, err := Environment(manager, "plain", tool, tool.Command, "abcd1234", "", "", nil)
	if err != nil {
		t.Fatalf("Environment: %v", err)
	}
	if env[config.HomeEnv] != home {
		t.Errorf("%s = %q, want %q", config.HomeEnv, env[config.HomeEnv], home)
	}
	t.Setenv(config.HomeEnv, "")
	_, def, err := Environment(manager, "plain", tool, tool.Command, "abcd1234", "", "", nil)
	if err != nil {
		t.Fatalf("Environment: %v", err)
	}
	if got, set := def[config.HomeEnv]; set {
		t.Errorf("a default-home board composed %s = %q", config.HomeEnv, got)
	}
}

// TypedPrompt takes back off what Prompt put in front, whichever notes a
// launch chose, and leaves a bare prompt alone.
func TestTypedPromptStripsTheNotes(t *testing.T) {
	for _, decorated := range []string{
		Prompt("", "ship it", true, false),
		Prompt("", "ship it", false, false),
		Prompt(CoordinationNote, "ship it", true, false),
		"ship it",
	} {
		if got := TypedPrompt(decorated); got != "ship it" {
			t.Errorf("TypedPrompt(%q) = %q", decorated, got)
		}
	}
	if got := TypedPrompt(Prompt("", "/review", true, false)); got != "/review" {
		t.Errorf("a slash prompt came back as %q", got)
	}
	if got := TypedPrompt(DeferredRenameDirective); got != "" {
		t.Errorf("a manager note came back as a prompt: %q", got)
	}
}

// mustRevive is ReviveCommand for the cases that are not about the model: it
// fails the test rather than returning an error nobody was asking about.
func mustRevive(t *testing.T, tool config.Tool, agentSessionID, model string) string {
	t.Helper()
	command, err := ReviveCommand("claude", tool, agentSessionID, model)
	if err != nil {
		t.Fatalf("ReviveCommand: %v", err)
	}
	return command
}

// Each flag below was read off the CLI installed on the box this was written
// on, not from anybody's memory of it: claude 2.1.263 and codex-cli 0.153.4
// both spell it --model.
func TestModelRidesTheToolsOwnFlag(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tool  config.Tool
		model string
		want  string
	}{
		// Quoted, like every other value this package puts on a command line.
		{"claude", config.Tool{Command: "claude", ModelFlag: "--model"}, "opus", "claude --model 'opus'"},
		{"codex", config.Tool{Command: "codex", ModelFlag: "--model"}, "gpt-5", "codex --model 'gpt-5'"},
		// The default is the empty answer, and it must not put a bare flag on
		// the command line.
		{"no model asked for", config.Tool{Command: "claude", ModelFlag: "--model"}, "", "claude"},
		{"blank model asked for", config.Tool{Command: "claude", ModelFlag: "--model"}, "   ", "claude"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := WithModel(tc.name, tc.tool, tc.tool.Command, tc.model)
			if err != nil {
				t.Fatalf("WithModel: %v", err)
			}
			if got != tc.want {
				t.Errorf("command = %q, want %q", got, tc.want)
			}
		})
	}
}

// opencode v2's TUI has no model flag: the model rides the generated config
// instead, so the command line stays bare and the revive does too.
func TestOpencodeV2ModelRidesTheGeneratedConfig(t *testing.T) {
	tool := config.Tool{Command: "opencode", ResumeByIDCommand: "opencode --session {id}"}
	if !ModelRidesConfig("opencode", tool) || !TakesModel("opencode", tool) {
		t.Fatal("opencode v2 must take its model from the generated config")
	}
	got, err := WithModel("opencode", tool, tool.Command, "anthropic/claude-sonnet-5")
	if err != nil || got != "opencode" {
		t.Fatalf("v2 command = %q, %v; want no model flag", got, err)
	}
	revive, err := ReviveCommand("opencode", tool, "ses_1", "anthropic/claude-sonnet-5")
	if err != nil || revive != "opencode --session 'ses_1'" {
		t.Fatalf("v2 revive = %q, %v; want no model flag", revive, err)
	}
	plan, err := Assemble("opencode", tool, "fix the bug", "", false, "anthropic/claude-sonnet-5", "")
	if err != nil || plan.Model != "anthropic/claude-sonnet-5" || strings.Contains(plan.Command, "--model") {
		t.Fatalf("v2 plan = %+v, %v; want the model recorded but off the command line", plan, err)
	}
	// Another tool on the same box is untouched by opencode's release.
	if ModelRidesConfig("claude", config.Tool{Command: "claude", ModelFlag: "--model"}) {
		t.Fatal("claude's model does not ride opencode's config")
	}
	// Without its generated config there is nowhere to put the model.
	if bare := (config.Tool{Command: "opencode", MCP: "none"}); TakesModel("opencode", bare) {
		t.Fatal("opencode with no generated config cannot be given a model")
	}
}

// The model Assemble left off a v2 opencode command line reaches the session
// through Environment, which writes it into the config the launch points at
// and runs the session on a private server so that config is read at all.
func TestEnvironmentWritesOpencodeV2ModelIntoItsConfig(t *testing.T) {
	// Environment refuses a CLI that is not installed, so only an installed
	// v2 exercises the path.
	if major := opencode.Cached().Major; major < 2 {
		t.Skipf("needs opencode v2 installed, found major version %d (0 is none)", major)
	}
	manager := hooks.NewManager(t.TempDir())
	tool := config.Tool{Command: "opencode", MCP: "opencode"}
	command, env, err := Environment(manager, "opencode", tool, "opencode", "abcd1234", "anthropic/claude-sonnet-5", "", nil)
	if err != nil {
		t.Fatalf("Environment: %v", err)
	}
	if command != "opencode --standalone" {
		t.Fatalf("command = %q, want a private server and no model flag", command)
	}
	content, err := os.ReadFile(env["OPENCODE_CONFIG"])
	if err != nil {
		t.Fatalf("OPENCODE_CONFIG %q: %v", env["OPENCODE_CONFIG"], err)
	}
	if !strings.Contains(string(content), `"model": "anthropic/claude-sonnet-5"`) {
		t.Fatalf("config at %q names no model:\n%s", env["OPENCODE_CONFIG"], content)
	}
}

// A model a CLI cannot be told about is refused, never dropped. Launching on
// the default and reporting success is how somebody pays opus rates for a run
// they asked to be cheap, and reads a haiku answer as an opus one.
func TestAToolWithNoModelFlagRefusesAModel(t *testing.T) {
	tool := config.Tool{Command: "somecli"}
	if _, err := WithModel("somecli", tool, tool.Command, "opus"); err == nil {
		t.Fatal("a tool with no model flag accepted a model")
	} else if !strings.Contains(err.Error(), "no model flag") {
		t.Fatalf("err = %v, want it to name the missing flag", err)
	}
	// The same tool is perfectly usable without one.
	if got, err := WithModel("somecli", tool, tool.Command, ""); err != nil || got != "somecli" {
		t.Fatalf("got %q, %v; want the bare command", got, err)
	}
	if _, err := Assemble("somecli", tool, "do the thing", "", false, "opus", ""); err == nil {
		t.Error("Assemble launched a tool that cannot be told which model to use")
	}
}

// A quoted model, because it reaches a shell.
func TestModelIsQuotedForTheShell(t *testing.T) {
	tool := config.Tool{Command: "claude", ModelFlag: "--model"}
	got, err := WithModel("claude", tool, tool.Command, "a model; rm -rf /")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "; rm") && !strings.Contains(got, "'a model; rm -rf /'") {
		t.Fatalf("the model reached the command line unquoted: %s", got)
	}
}

// A session comes back on the model it launched with: a revive onto the CLI's
// default would quietly change what the run is while the row still said
// otherwise.
func TestReviveComesBackOnTheSameModel(t *testing.T) {
	tool := config.Tool{Command: "claude", ModelFlag: "--model", ResumeByIDCommand: "claude --resume {id}"}
	got, err := ReviveCommand("claude", tool, "conv-1", "opus")
	if err != nil {
		t.Fatalf("ReviveCommand: %v", err)
	}
	if !strings.Contains(got, "--resume") || !strings.Contains(got, "--model 'opus'") {
		t.Fatalf("revive = %q, want the conversation and the model", got)
	}
	// And a session that never asked for one still comes back bare.
	if got, err := ReviveCommand("claude", tool, "conv-1", ""); err != nil || strings.Contains(got, "--model") {
		t.Fatalf("revive = %q, %v; want no model flag", got, err)
	}
}

// Assemble records what it asked for, since the row is what a later revive
// reads the model back out of.
func TestAssembleRecordsTheModelItAskedFor(t *testing.T) {
	tool := config.Tool{Command: "claude", ModelFlag: "--model"}
	plan, err := Assemble("claude", tool, "fix the bug", "", false, " opus ", "")
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if plan.Model != "opus" {
		t.Errorf("Plan.Model = %q, want the trimmed model", plan.Model)
	}
	if !strings.Contains(plan.Command, "--model 'opus'") {
		t.Errorf("command = %q", plan.Command)
	}
	plain, err := Assemble("claude", tool, "fix the bug", "", false, "", "")
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if plain.Model != "" || strings.Contains(plain.Command, "--model") {
		t.Errorf("a default launch carried a model: %+v", plain)
	}
}

// The shipped defaults are part of the feature: a flag missing here means the
// CLI silently cannot be given a model.
func TestTheDefaultConfigNamesEachInstalledCLIsModelFlag(t *testing.T) {
	cfg, err := config.LoadDir(t.TempDir())
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	for _, name := range []string{"claude", "codex", "opencode"} {
		if !TakesModel(name, cfg.Tools[name]) {
			t.Errorf("%s takes no model, so it would refuse every model", name)
		}
	}
}

// A launched session starts with the operator's own shell environment, so an
// agent reaches the same API keys opening a terminal gives the operator,
// even where the background service or the manager predates a secrets file.
func TestEnvironmentInheritsTheParentShellEnv(t *testing.T) {
	manager := hooks.NewManager(t.TempDir())
	tool := config.Tool{Command: "cat"}
	t.Setenv("GATE_INBOX_TEST_PARENT_MARKER", "from-the-operator-shell")
	_, env, err := Environment(manager, "plain", tool, tool.Command, "abcd1234", "", "", nil)
	if err != nil {
		t.Fatalf("Environment: %v", err)
	}
	if env["GATE_INBOX_TEST_PARENT_MARKER"] != "from-the-operator-shell" {
		t.Fatalf("session env lacks the parent shell export: %v", env)
	}
}

// Per-process values must never cross into a session: the manager's own pane
// address (production code reads it to find the pane it is drawing in), the
// working directory the new session sets itself, the nesting level the new
// shell sets, the terminal type tmux declares per pane, and empty values,
// which is also what keeps an explicitly unset home from composing one.
func TestEnvironmentSkipsPerProcessParentVars(t *testing.T) {
	manager := hooks.NewManager(t.TempDir())
	tool := config.Tool{Command: "cat"}
	for key, value := range map[string]string{
		"TMUX": "/tmp/fake,,0", "TMUX_PANE": "%0",
		"PWD": "/nope", "OLDPWD": "/nope", "SHLVL": "9", "TERM": "xterm",
		"GATE_INBOX_TEST_EMPTY": "",
	} {
		t.Setenv(key, value)
	}
	_, env, err := Environment(manager, "plain", tool, tool.Command, "abcd1234", "", "", nil)
	if err != nil {
		t.Fatalf("Environment: %v", err)
	}
	for _, key := range []string{"TMUX", "TMUX_PANE", "PWD", "OLDPWD", "SHLVL", "TERM", "GATE_INBOX_TEST_EMPTY"} {
		if got, set := env[key]; set {
			t.Errorf("session env carries parent %s = %q", key, got)
		}
	}
}

// A spawn asked for by a Claude session arrives through that session's MCP
// server, which Claude Code stamps as its child. The stamp must stop at the
// manager: a claude launched with CLAUDE_CODE_CHILD_SESSION set turns its
// transcript off (no --resume, so no revive, unpark or fork), and the other
// names would tell it whose child it is.
func TestEnvironmentSkipsTheClaudeChildStamp(t *testing.T) {
	manager := hooks.NewManager(t.TempDir())
	tool := config.Tool{Command: "cat"}
	stamp := map[string]string{
		"CLAUDECODE": "1", "CLAUDE_CODE_SESSION_ID": "9df92cbc-outer",
		"CLAUDE_CODE_CHILD_SESSION": "1", "CLAUDE_CODE_SESSION_ATTENDED": "1",
		"CLAUDE_PID": "4242", "CLAUDE_EFFORT": "high", "AI_AGENT": "claude-code/agent",
	}
	for key, value := range stamp {
		t.Setenv(key, value)
	}
	_, env, err := Environment(manager, "plain", tool, tool.Command, "abcd1234", "", "", nil)
	if err != nil {
		t.Fatalf("Environment: %v", err)
	}
	for key := range stamp {
		if got, set := env[key]; set {
			t.Errorf("session env carries the Claude child stamp %s = %q", key, got)
		}
	}
}

// Keys compose sets explicitly win over the same name in the parent shell:
// a nested manager must not hand its outer session id to the inner one.
func TestEnvironmentExplicitKeysWinOverParentEnv(t *testing.T) {
	manager := hooks.NewManager(t.TempDir())
	tool := config.Tool{Command: "cat"}
	t.Setenv(hooks.EnvSessionID, "outer-session")
	_, env, err := Environment(manager, "plain", tool, tool.Command, "abcd1234", "", "", nil)
	if err != nil {
		t.Fatalf("Environment: %v", err)
	}
	if env[hooks.EnvSessionID] != "abcd1234" {
		t.Fatalf("%s = %q, want the inner session id", hooks.EnvSessionID, env[hooks.EnvSessionID])
	}
}

func TestValidEnvName(t *testing.T) {
	for _, key := range []string{"PATH", "_A1", "SAMPLE_SERVICE_API_KEY"} {
		if !validEnvName(key) {
			t.Errorf("validEnvName(%q) = false, want true", key)
		}
	}
	for _, key := range []string{"", "1A", "HAS SPACE", "HAS-DASH", "HAS.DOT"} {
		if validEnvName(key) {
			t.Errorf("validEnvName(%q) = true, want false", key)
		}
	}
}

// TestComposeReachesAPaneUnderTheOperatorShell is the spawn path end to
// end: compose the launch, open it on a private tmux server, and prove a
// parent shell export arrives in the pane under the operator's real login
// shell while a blocklisted per-process value does not.
func TestComposeReachesAPaneUnderTheOperatorShell(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	if _, err := os.Stat("/usr/bin/zsh"); err != nil {
		t.Skip("no zsh to launch under")
	}
	t.Setenv("SHELL", "/usr/bin/zsh")
	t.Setenv("GATE_INBOX_E2E_PARENT_MARKER", "reached-the-pane")
	t.Setenv("TMUX_PANE", "%9")
	manager := hooks.NewManager(t.TempDir())
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	blocked := filepath.Join(dir, "blocked")
	base := "printenv GATE_INBOX_E2E_PARENT_MARKER > " + tmux.ShellQuote(marker) +
		"; printenv TMUX_PANE > " + tmux.ShellQuote(blocked)
	tool := config.Tool{Command: base}
	command, env, err := Compose(manager, "plain", tool, base, "e2eshellenv", "", "", nil)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	driver, err := tmux.NewWithSocket(tmuxtest.NewSocket("e2e"))
	if err != nil {
		t.Fatalf("NewWithSocket: %v", err)
	}
	if err := driver.Create("e2eshellenv", dir, command, env, 80, 24); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { driver.Kill("e2eshellenv") })
	deadline := time.Now().Add(30 * time.Second)
	for {
		data, err := os.ReadFile(marker)
		if err == nil && len(data) > 0 {
			if got := strings.TrimSpace(string(data)); got != "reached-the-pane" {
				t.Fatalf("pane saw GATE_INBOX_E2E_PARENT_MARKER = %q, want %q", got, "reached-the-pane")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the pane to write its marker")
		}
		time.Sleep(100 * time.Millisecond)
	}
	// tmux assigns every pane an id of its own, so the check is not absence
	// but non-inheritance: the pane must not see the manager's pane address.
	if data, err := os.ReadFile(blocked); err == nil && strings.TrimSpace(string(data)) == "%9" {
		t.Fatal("pane inherited the manager's TMUX_PANE")
	}
}

// accountTool is a CLI that takes a subscription token from its environment,
// the way claude takes CLAUDE_CODE_OAUTH_TOKEN.
var accountTool = config.Tool{
	Command:        "cat",
	AccountEnv:     "GATE_INBOX_TEST_OAUTH_TOKEN",
	AccountSecret:  "GATE_INBOX_TEST_SECRET_{account}",
	AccountCommand: "read {secret}",
}

func stubAccount(t *testing.T, fn func(tool config.Tool, account string) (string, error)) {
	t.Helper()
	prev := resolveAccount
	resolveAccount = fn
	t.Cleanup(func() { resolveAccount = prev })
}

// The token is read at launch and exported through the tool's own variable,
// winning over one the operator's shell exports: a session asked for one
// person's account must not quietly run on another's. A session that asked
// for none reads no secret and leaves the CLI to its own login.
func TestEnvironmentExportsTheAccountsToken(t *testing.T) {
	manager := hooks.NewManager(t.TempDir())
	t.Setenv("GATE_INBOX_TEST_OAUTH_TOKEN", "the-operators-own")
	asked := ""
	stubAccount(t, func(tool config.Tool, account string) (string, error) {
		asked = account
		return "sk-ant-oat01-bob", nil
	})
	_, env, err := Environment(manager, "claude", accountTool, accountTool.Command, "abcd1234", "", "alice1", nil)
	if err != nil {
		t.Fatalf("Environment: %v", err)
	}
	if asked != "ALICE1" || env["GATE_INBOX_TEST_OAUTH_TOKEN"] != "sk-ant-oat01-bob" {
		t.Errorf("resolved %q, env token %q", asked, env["GATE_INBOX_TEST_OAUTH_TOKEN"])
	}
	asked = ""
	_, env, err = Environment(manager, "claude", accountTool, accountTool.Command, "abcd1234", "", "", nil)
	if err != nil {
		t.Fatalf("Environment: %v", err)
	}
	if asked != "" || env["GATE_INBOX_TEST_OAUTH_TOKEN"] != "" {
		t.Errorf("a launch with no account resolved %q and exported %q", asked, env["GATE_INBOX_TEST_OAUTH_TOKEN"])
	}
}

// An account refuses the launch rather than starting a session on whatever
// login the CLI finds for itself: when the tool has nowhere to take a token,
// when the name is not a name, and when the secret will not resolve.
func TestAccountRefusesRatherThanLaunchingOnTheDefaultLogin(t *testing.T) {
	if _, err := Assemble("somecli", config.Tool{Command: "somecli"}, "do the thing", "", false, "", "ALICE1"); err == nil {
		t.Error("Assemble launched a tool that cannot be handed a token")
	}
	if _, err := Assemble("claude", accountTool, "fix the bug", "", false, "", "a; rm -rf /"); err == nil {
		t.Error("Assemble accepted a shell-shaped account name")
	}
	manager := hooks.NewManager(t.TempDir())
	stubAccount(t, func(config.Tool, string) (string, error) { return "", errors.New("secret not found") })
	if _, _, err := Environment(manager, "claude", accountTool, accountTool.Command, "abcd1234", "", "NOBODY1", nil); err == nil || !strings.Contains(err.Error(), "secret not found") {
		t.Errorf("err = %v, want the resolver's refusal", err)
	}
}

// A switch refuses everything a launch refuses, plus the one case only a
// session already running has: a pane the manager adopted rather than
// started, which it cannot end and bring back on another token. Clearing the
// account is refused there too -- that is a relaunch as well.
func TestAccountForSwitchRefusesWhatCannotBeRelaunched(t *testing.T) {
	if _, err := AccountForSwitch(accountTool, true, "ALICE1"); err == nil ||
		!strings.Contains(err.Error(), "did not start") {
		t.Errorf("an adopted pane took an account: %v", err)
	}
	if _, err := AccountForSwitch(accountTool, true, ""); err == nil {
		t.Error("an adopted pane was moved back to its own login")
	}
	if _, err := AccountForSwitch(config.Tool{Command: "somecli"}, false, "ALICE1"); err == nil {
		t.Error("a tool that cannot be handed a token took an account")
	}
	if _, err := AccountForSwitch(accountTool, false, "a; rm -rf /"); err == nil {
		t.Error("a shell-shaped account name was accepted")
	}
	// The name is recorded the way the secret spells it, since the row is
	// what the next launch reads it back out of.
	if got, err := AccountForSwitch(accountTool, false, " alice1 "); err != nil || got != "ALICE1" {
		t.Errorf("AccountForSwitch = %q, %v", got, err)
	}
	if got, err := AccountForSwitch(accountTool, false, ""); err != nil || got != "" {
		t.Errorf("clearing the account = %q, %v", got, err)
	}
}

// Assemble records the account in the secret's spelling, since the row is
// what a later revive reads it back out of; the shipped claude block already
// knows where the team's tokens live, and no other CLI claims to.
func TestAssembleRecordsTheAccountAndTheDefaultConfigNamesNoAccounts(t *testing.T) {
	plan, err := Assemble("claude", accountTool, "fix the bug", "", false, "", " alice1 ")
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if plan.Account != "ALICE1" || strings.Contains(plan.Command, "ALICE1") {
		t.Errorf("Plan.Account = %q, command = %q", plan.Account, plan.Command)
	}
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	// Where a token comes from is the operator's to say: the core names no
	// account store for any tool, claude included.
	for _, name := range []string{"claude", "codex", "opencode"} {
		if cfg.Tools[name].AccountEnv != "" {
			t.Errorf("%s claims to take a subscription token; nothing here was read off that CLI", name)
		}
	}
}

// A session whose pane could not be opened where the spawn asked for it is
// told to change into that directory before it does anything else, so the work
// still lands where it was meant to.
func TestAssembleLeadsWithTheWorkdirDirective(t *testing.T) {
	tool := config.Tool{Command: "claude", PromptFlag: "--prompt"}
	plan, err := Assemble("claude", tool, "build the api", "/home/dev/worktrees/wt", true, "", "")
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if !strings.Contains(plan.LaunchPrompt, "/home/dev/worktrees/wt") {
		t.Fatalf("the launch prompt does not name the working directory: %q", plan.LaunchPrompt)
	}
	if !strings.HasPrefix(plan.LaunchPrompt, WorkdirDirectivePrefix) {
		t.Fatalf("the directive does not lead the prompt: %q", plan.LaunchPrompt)
	}
	if !strings.Contains(plan.LaunchPrompt, RenameDirective) || !strings.Contains(plan.LaunchPrompt, "build the api") {
		t.Fatalf("the directive displaced the rest of the prompt: %q", plan.LaunchPrompt)
	}
	// The stored launch prompt carries the manager's own notes, and the prompt
	// a person typed has to come back out of it without them.
	if got := TypedPrompt(plan.LaunchPrompt); got != "build the api" {
		t.Fatalf("typed prompt = %q", got)
	}
	// The change of directory is worth nothing if a missing directory reads as
	// success and the work happens wherever the pane opened.
	if !strings.Contains(plan.LaunchPrompt, "stop and say so") {
		t.Fatalf("a failed cd is not reported: %q", plan.LaunchPrompt)
	}
}

// A promptless or slash-command launch cannot carry the directive on its
// command line, so it arrives as the first thing typed in -- ahead of the
// prompt a typed-into tool takes the same way.
func TestAssembleSendsTheWorkdirDirectiveFirstWhenItCannotRide(t *testing.T) {
	sent := config.Tool{Command: "hermes", PromptMode: "send"}
	plan, err := Assemble("hermes", sent, "/compact the notes", "/srv/work", false, "", "")
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(plan.PendingInputs) < 2 {
		t.Fatalf("pending inputs = %q", plan.PendingInputs)
	}
	if !strings.HasPrefix(plan.PendingInputs[0], ManagerBand) || !strings.Contains(plan.PendingInputs[0], "/srv/work") {
		t.Fatalf("the directive is not the banded first input: %q", plan.PendingInputs)
	}
	if plan.PendingInputs[1] != "/compact the notes" {
		t.Fatalf("the prompt no longer follows the directive: %q", plan.PendingInputs)
	}
}

// Every session launched where it was asked to be, which is almost all of
// them, must compose exactly what it composed before.
func TestAssembleWithoutAWorkdirIsUnchanged(t *testing.T) {
	tool := config.Tool{Command: "claude", PromptFlag: "--prompt"}
	plan, err := Assemble("claude", tool, "build the api", "  ", true, "", "")
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if strings.Contains(plan.LaunchPrompt, WorkdirDirectivePrefix) {
		t.Fatalf("an empty workdir still produced a directive: %q", plan.LaunchPrompt)
	}
}

// A stored prompt that is one of the manager's own banded notes is nothing
// the user typed.
func TestTypedPromptDropsABandedNote(t *testing.T) {
	if got := TypedPrompt(DeferredRenameDirective); got != "" {
		t.Errorf("TypedPrompt(%q) = %q, want empty", DeferredRenameDirective[:30], got)
	}
	if got := TypedPrompt("[gate-inbox] is the product name"); got != "[gate-inbox] is the product name" {
		t.Errorf("a prompt that only mentions the tag was dropped: %q", got)
	}
}
