// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

// Package launch assembles the shell command and environment a managed
// session starts with. The TUI and the MCP session tools both spawn
// sessions, from different processes, and a session spawned either way
// must carry the same prompt handling, session-id flags, hook wiring and
// MCP registration.
package launch

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/band"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/mcpreg"
	"github.com/usestring/gate-inbox/internal/tmux"
	"github.com/usestring/gate-inbox/internal/tooldrivers"
)

// RenameDirective asks the agent, as the first line of its first prompt,
// to name its own session via the rename tool. Carried only for auto-named
// sessions that launch with a prompt on a tool that wants it
// (skip_rename_directive opts out), so it fires exactly once.
const RenameDirective = `First, run this exact shell command once, replacing <name> with a short 2-4 word kebab-case name for the broad feature or theme of this whole session (not one subtask of a larger feature): "$GATE_INBOX_BIN" rename "<name>". Run rename only this once. Do not rename again later in the conversation unless the user explicitly asks you to rename; if they do, pick a broad name from context, not a narrow step. Then do the task:`

// ManagerBand marks a message the manager typed into a pane on its own
// account. It leads every standalone note below because those arrive
// unasked where the user's own typing goes, and an agent that mistakes one
// for the user acts on it as one. A note carried by the launch command line
// is part of the prompt the spawn asked for, so it goes unbanded.
const ManagerBand = band.Tag + ` This request is from Gate Inbox itself, not from the user. `

// DeferredRenameDirective is the standalone message sent into sessions
// whose first prompt could not carry the directive: slash-command
// prompts (the command must open the message) and promptless launches.
//
// The manager's own MCP tool leads and the shell command is the fallback.
// Both do the same thing, but a shell command is the one an agent has to stop
// and ask about: codex gates exec behind an approval dialog, so a rename asked
// for as a command line sat waiting on the operator to approve a housekeeping
// step they never asked for. The tool is registered in every session the
// manager starts (internal/mcpreg), and calling it raises no dialog.
const DeferredRenameDirective = ManagerBand + `Name this session with a short 2-4 word kebab-case name for the broad feature or theme of this whole session (not one subtask of a larger feature). Call the gate-inbox "rename" MCP tool with that name. Only if that tool is not available in this session, run this exact shell command once instead: "$GATE_INBOX_BIN" rename "<name>". Rename only this once. Do not rename again later in the conversation unless the user explicitly asks you to rename; if they do, pick a broad name from context, not a narrow step. Then continue.`

// AdoptedRenameDirective is the standalone message sent into a pane the
// manager only adopted. An adopted pane was started by somebody else, so it
// carries neither the session id nor the config directory in its environment
// and its shell may not have the manager on PATH: everything the rename
// subcommand reads from its environment has to be spelled out here instead.
// The provenance band leads because this arrives unasked where the user's own
// typing goes, and an agent that mistakes it for the user acts on it as one.
func AdoptedRenameDirective(command string) string {
	return ManagerBand +
		`Run this exact shell command once, replacing <name> with a short 2-4 word kebab-case name for the broad feature or theme of this whole session (not one subtask of a larger feature): ` +
		"`" + command + "`" +
		`. Run rename only this once, then carry on with what you were doing. Do not rename again later in the conversation unless the user explicitly asks you to rename; if they do, pick a broad name from context, not a narrow step.`
}

// AdoptedRenameCommand is the command line the directive above carries: the
// manager's own executable, given by absolute path, with the two environment
// values a launched session would have inherited.
func AdoptedRenameCommand(executable, configDir, sessionID string) string {
	// The names are bare and the values quoted: bash only reads a leading
	// NAME=value as an assignment when the name itself is unquoted.
	return config.HomeEnv + "=" + tmux.ShellQuote(configDir) + " " +
		hooks.EnvSessionID + "=" + tmux.ShellQuote(sessionID) + " " +
		tmux.ShellQuote(executable) + ` rename "<name>"`
}

// RenameAvailableNote tells a custom-named session that rename exists for
// later use without asking it to rename now.
const RenameAvailableNote = `This session is already named. You can rename it later with "$GATE_INBOX_BIN" rename "<name>" only if the user asks. Do not rename it now. Then do the task:`

// WorkdirDirectivePrefix opens the directive below, and is what TypedPrompt
// recognises it by when it takes the manager's own notes back off a stored
// launch prompt.
const WorkdirDirectivePrefix = `Your working directory for this task is `

// WorkdirDirective tells an agent where to work when its pane could not be
// opened there. It leads the first prompt, so the move happens before anything
// the spawn asked for, and it asks for a failure to be reported rather than
// worked around: a directory that has gone between the spawn resolving it and
// the agent starting is the one case where carrying on would put the work
// somewhere nobody asked for.
func WorkdirDirective(dir string) string {
	return WorkdirDirectivePrefix + dir +
		`. This session was started in a different directory, so change into it first with "cd ` + tmux.ShellQuote(dir) +
		`". If it is not there, stop and say so rather than working anywhere else.`
}

// CoordinationNote points a session at the subcommands. Only tools whose
// agent has no MCP client get it; the rest are told the same thing by the
// tool descriptions the MCP server registers.
const CoordinationNote = `Other agent sessions may be running beside you in Gate Inbox: run "$GATE_INBOX_BIN" help in your shell for the subcommands that list them, message them, share a task list and reserve the files you are about to edit.`

func coordinationNote(toolName string, tool config.Tool) string {
	if mcpreg.Style(toolName, tool.MCP) != mcpreg.StyleNone {
		return ""
	}
	return CoordinationNote
}

// DirectiveEmbeddable reports whether a launch note can ride the
// session's first prompt; otherwise auto-named sessions get the rename
// directive later as its own message.
func DirectiveEmbeddable(prompt string) bool {
	return prompt != "" && !strings.HasPrefix(prompt, "/")
}

// Prompt prepends the short agent notes a first prompt can carry: auto-named
// sessions must rename once, custom-named sessions only learn that rename is
// available later, and a session without MCP tools learns where the rest of
// the workspace is. The coordination note leads because both rename notes
// end by handing over to the task.
//
// skipDirective leaves the prompt alone apart from the coordination note: the
// tool names its sessions from the outside (its own title, an on-demand
// command, silent instructions), so anything prepended here would only pollute
// the first message the title is eventually written from.
func Prompt(note, prompt string, autoNamed, skipDirective bool) string {
	if !DirectiveEmbeddable(prompt) {
		return prompt
	}
	if skipDirective {
		if note != "" {
			return note + "\n\n" + prompt
		}
		return prompt
	}
	directive := RenameAvailableNote
	if autoNamed {
		directive = RenameDirective
	}
	if note != "" {
		return note + "\n\n" + directive + "\n\n" + prompt
	}
	return directive + "\n\n" + prompt
}

// TypedPrompt is the prompt a person typed, with the notes Prompt put in
// front of it taken back off. A transcript's first user record and the
// stored LaunchPrompt both carry the decorated form. A message the manager
// sent on its own account was typed by nobody, and comes back empty.
func TypedPrompt(prompt string) string {
	if strings.HasPrefix(strings.TrimSpace(prompt), ManagerBand) {
		return ""
	}
	for {
		trimmed := strings.TrimLeft(prompt, "\n")
		// The workdir directive carries a path, so it is recognised by its
		// opening and cut at the blank line that ends it rather than matched
		// whole like the fixed notes below.
		if strings.HasPrefix(trimmed, WorkdirDirectivePrefix) {
			if _, rest, found := strings.Cut(trimmed, "\n\n"); found {
				prompt = rest
				continue
			}
		}
		cut := false
		for _, note := range []string{CoordinationNote, RenameDirective, RenameAvailableNote} {
			if strings.HasPrefix(trimmed, note) {
				prompt, cut = trimmed[len(note):], true
				break
			}
		}
		if !cut {
			return strings.TrimSpace(prompt)
		}
	}
}

// WithPrompt appends the first prompt to a tool's command, using the
// tool's prompt flag when it has one. Tools that take their prompt as
// typed input instead keep the bare command.
func WithPrompt(tool config.Tool, command, prompt string) string {
	if prompt == "" || tool.PromptMode == "send" {
		return command
	}
	if tool.PromptFlag != "" {
		return command + " " + tool.PromptFlag + " " + tmux.ShellQuote(prompt)
	}
	return command + " " + tmux.ShellQuote(prompt)
}

// Plan is everything a spawn needs beyond the store row: the command to
// run, the inputs the poller types in once the agent is up, and the
// conversation id a later revive resumes.
type Plan struct {
	Command        string
	PendingInputs  []string
	AgentSessionID string
	// LaunchPrompt is the prompt the command line carries, which the agent
	// clears its composer to pick up. Input delivered before then goes with
	// it, so the poller waits for this text to reach the pane. A tool typed
	// into takes its prompt as pending input instead, leaving nothing to
	// wait behind.
	LaunchPrompt string
	// Model is the model the command line asks for, empty for the CLI's own
	// default. Recorded on the session so a revive comes back on the same one.
	Model string
	// Account is the named subscription the session runs on, empty for the
	// CLI's own login. Recorded on the session so a revive spends the same
	// person's window; the token itself is read again at each launch.
	Account string
}

// ModelRidesConfig reports whether a tool takes its model from the config
// the manager generates for it rather than from a command-line flag. That is
// opencode: its TUI has no model flag, and the generated config's model key
// is the only place a chosen model can be named.
func ModelRidesConfig(toolName string, tool config.Tool) bool {
	return mcpreg.Style(toolName, tool.MCP) == "opencode"
}

// TakesModel reports whether a session on the tool can be launched on a
// chosen model: through its model flag, or through its generated config.
func TakesModel(toolName string, tool config.Tool) bool {
	return tool.ModelFlag != "" || ModelRidesConfig(toolName, tool)
}

// Assemble resolves a session's first prompt into a launch plan. A prompt
// rides the command line when the tool takes one, and is typed into the
// pane when the tool's prompt mode is "send". Tools that accept a chosen
// session id launch with one, so a later revive resumes this exact
// conversation rather than the directory's most recent one; tools without
// the flag mint their own id, captured after launch by the poller.
// WithModel puts a chosen model on a command line, and refuses when the tool
// has no flag to put it on. A tool whose model rides its generated config
// keeps a bare command line; Environment writes the model into that config.
//
// Refusing is the point. A tool whose CLI cannot be told which model to use
// would otherwise launch on its default and look like it obeyed, and the
// difference only shows up in the bill or in the quality of the answers --
// long after the person who asked is in a position to notice.
func WithModel(toolName string, tool config.Tool, command, model string) (string, error) {
	model = strings.TrimSpace(model)
	if model == "" || ModelRidesConfig(toolName, tool) {
		return command, nil
	}
	if tool.ModelFlag == "" {
		return "", fmt.Errorf("%s cannot be launched on a chosen model: its CLI has no model flag, "+
			"so it would run on its own default instead; leave the model empty to use that default, "+
			"and call list_models for the CLIs that do accept one",
			tool.Command)
	}
	return command + " " + tool.ModelFlag + " " + tmux.ShellQuote(model), nil
}

// WithAccount checks that a chosen account can be handed to the tool at
// all, and refuses when it cannot. The refusal is for the same reason
// WithModel's is: a tool with nowhere to take a token would launch on its own
// stored login and look like it obeyed, and the difference is whose usage
// window the run spends.
func WithAccount(tool config.Tool, account string) (string, error) {
	account = accounts.Normalize(account)
	if account == "" {
		return "", nil
	}
	if tool.AccountEnv == "" || tool.AccountSecret == "" || tool.AccountCommand == "" {
		return "", fmt.Errorf("%s cannot be launched on a chosen account: its config names no account_env, "+
			"so it would run on its own stored login instead; leave the account empty to use that login, "+
			"and call list_accounts for the CLIs that do accept one",
			tool.Command)
	}
	if _, err := accounts.Secret(tool, account); err != nil {
		return "", err
	}
	return account, nil
}

// AccountForSwitch is the account an existing session may be moved onto:
// the name to record, or the refusal to show. The board's card and the MCP
// switch_account tool both go through it, so an operator and an agent are
// told the same thing about the same row.
//
// An adopted pane is refused because applying an account means ending the
// session and launching it again. The manager never started that process, so
// it has nothing to bring back.
func AccountForSwitch(tool config.Tool, adopted bool, account string) (string, error) {
	if adopted {
		return "", errors.New("that session is a pane the manager did not start, so it cannot be " +
			"ended and relaunched on another account; take it over first")
	}
	return WithAccount(tool, account)
}

// The workdir argument, when set, is the directory the spawn asked for but the
// pane could not be opened in; the agent is told to change into it before doing
// anything else. Empty for every session launched where it was asked to be,
// which is almost all of them.
func Assemble(toolName string, tool config.Tool, rawPrompt, workdir string, autoNamed bool, model, account string) (Plan, error) {
	account, err := WithAccount(tool, account)
	if err != nil {
		return Plan{}, err
	}
	note := coordinationNote(toolName, tool)
	carried := DirectiveEmbeddable(rawPrompt)
	prompt := Prompt(note, rawPrompt, autoNamed, tool.SkipRenameDirective)
	workdir = strings.TrimSpace(workdir)
	if workdir != "" && carried {
		prompt = WorkdirDirective(workdir) + "\n\n" + prompt
	}
	plan := Plan{Command: WithPrompt(tool, tool.Command, prompt)}
	// Ahead of the prompt a typed-into tool takes as pending input, so the
	// agent moves before it reads the task rather than after.
	if workdir != "" && !carried {
		plan.PendingInputs = append(plan.PendingInputs, ManagerBand+WorkdirDirective(workdir))
	}
	if tool.PromptMode != "send" {
		plan.LaunchPrompt = prompt
	}
	if tool.PromptMode == "send" && prompt != "" {
		plan.PendingInputs = append(plan.PendingInputs, prompt)
	}
	if autoNamed && !tool.SkipRenameDirective && !carried {
		plan.PendingInputs = append(plan.PendingInputs, DeferredRenameDirective)
	}
	if note != "" && !carried {
		plan.PendingInputs = append(plan.PendingInputs, ManagerBand+note)
	}
	if tool.SessionIDFlag != "" {
		plan.AgentSessionID = uuid.NewString()
		plan.Command += " " + tool.SessionIDFlag + " " + plan.AgentSessionID
	}
	command, err := WithModel(toolName, tool, plan.Command, model)
	if err != nil {
		return Plan{}, err
	}
	plan.Command = command
	plan.Model = strings.TrimSpace(model)
	plan.Account = account
	return plan, nil
}

// ReviveCommand is the base command a dead session comes back on. When
// its own conversation id was captured, it resumes that exact
// conversation instead of the working directory's most recent one, which
// would be the wrong conversation whenever sessions share a cwd. The id is
// read from the agent CLI's own store rather than minted here, so it is
// quoted for the shell the way a fork's {id} already is.
func ReviveCommand(toolName string, tool config.Tool, agentSessionID, model string) (string, error) {
	base := tool.Command
	switch {
	case agentSessionID != "" && tool.ResumeByIDCommand != "":
		base = strings.ReplaceAll(tool.ResumeByIDCommand, "{id}", tmux.ShellQuote(agentSessionID))
	case tool.ReviveCommand != "":
		base = tool.ReviveCommand
	}
	// A session comes back on the model it was launched with. Reviving a run
	// onto the CLI's default would quietly change what it is, and the row
	// would still say otherwise.
	return WithModel(toolName, tool, base, model)
}

// Environment resolves the shell command and environment a session
// launches with. Every session carries its id so the rename subcommand
// can find it; tools backed by hooks additionally get the generated
// settings file and their status-file path, plus a clean slate from any
// earlier files under the same id.
//
// model is the model the session runs on, for a tool whose model rides its
// generated config (ModelRidesConfig); a tool with a model flag already
// carries it in baseCommand and ignores this.
//
// account is the named subscription the session runs on, empty for the
// CLI's own login. Its token is read here, at launch, and exported into the
// session through the tool's AccountEnv.
//
// contributed is what the build's extensions add for this launch. It
// overrides what the session would inherit, and an empty value withholds an
// inherited one; a name this function sets itself is refused rather than
// overridden.
func Environment(manager *hooks.Manager, toolName string, tool config.Tool, baseCommand, id, model, account string, contributed map[string]string) (string, map[string]string, error) {
	if err := config.CheckInstalled(baseCommand); err != nil {
		return "", nil, err
	}
	// Refused before anything is removed or written: a style nothing
	// implements is a config mistake, not a launch half made.
	if err := tooldrivers.CheckTool(toolName, tool); err != nil {
		return "", nil, err
	}
	if err := manager.RemoveName(id); err != nil {
		return "", nil, err
	}
	if tool.StatusSource == hooks.StatusSourceClaude {
		if _, err := manager.EnsureSettings(); err != nil {
			return "", nil, err
		}
		if err := manager.Remove(id); err != nil {
			return "", nil, err
		}
	}
	return compose(manager, toolName, tool, baseCommand, id, model, account, contributed, true)
}

// parentEnvBlocklist are per-process values a launched session must never
// inherit from the manager's own environment: the manager's pane address
// (production code reads it to find the pane it is drawing in), the working
// directory the new session sets itself, the nesting level the new shell
// sets, the terminal type tmux already declares per pane, and the stamp a
// Claude Code process puts on every child it starts (its Bash tool, hooks
// and MCP servers -- so the manager's own MCP server, and the CLI it
// delegates a spawn to, carry it). A claude launched with that stamp takes
// itself for a nested child and turns transcript saving off, so the row can
// never be resumed, revived or forked. A managed session is a peer of the
// session that asked for it, not its child. The same goes for the status
// file a managed claude's hooks write: compose sets one only for a session
// with those hooks, so an inherited one would point a session at another
// session's status.
var parentEnvBlocklist = map[string]bool{
	"TMUX": true, "TMUX_PANE": true,
	"PWD": true, "OLDPWD": true, "SHLVL": true,
	"TERM":       true,
	"CLAUDECODE": true, "CLAUDE_CODE_SESSION_ID": true, "CLAUDE_CODE_CHILD_SESSION": true,
	"CLAUDE_CODE_SESSION_ATTENDED": true, "CLAUDE_PID": true, "CLAUDE_EFFORT": true,
	"AI_AGENT":          true,
	hooks.EnvStatusFile: true,
	hooks.EnvExitFile:   true,
}

// inheritParentEnv carries the operator's own shell environment into a
// launched session: API keys and other exports a login shell would otherwise
// only reach through startup files, so the agent process itself starts with
// the same environment opening a terminal gives the operator. Keys compose
// sets explicitly (session id, executable, home) win over inherited
// ones; empty values, names no POSIX shell can export, and blocklisted
// per-process values never cross.
func inheritParentEnv(env map[string]string) {
	for _, kv := range os.Environ() {
		key, value, _ := strings.Cut(kv, "=")
		if value == "" || parentEnvBlocklist[key] || !validEnvName(key) {
			continue
		}
		if _, set := env[key]; !set {
			env[key] = value
		}
	}
}

// checkContributed refuses an extension's variable that the launch sets
// itself or must never carry. Refused rather than overridden either way: an
// extension that thinks it sets the session's id or its account token is
// wrong about what it is doing, and a launch that quietly ignored it would
// leave it wrong.
func checkContributed(tool config.Tool, contributed map[string]string) error {
	for key := range contributed {
		reserved := key == hooks.EnvSessionID || key == hooks.EnvExecutable || key == config.HomeEnv ||
			parentEnvBlocklist[key] || tool.AccountEnv != "" && key == tool.AccountEnv
		if reserved {
			return fmt.Errorf("an extension set %s, which the launch sets itself", key)
		}
		if !validEnvName(key) {
			return fmt.Errorf("an extension set %q, which is not an environment variable name", key)
		}
	}
	return nil
}

func validEnvName(key string) bool {
	if key == "" {
		return false
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		ok := c == '_' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || i > 0 && '0' <= c && c <= '9'
		if !ok {
			return false
		}
	}
	return true
}

// Compose is Environment without the side effects: the same command and the
// same environment, with no file written, nothing removed and no registration
// run.
//
// A rehearsal that wrote the shared settings file would be doing part of the
// thing it exists to avoid. It also has to work where those paths do not:
// a stale claude-settings.json behind a sandbox deny rule is precisely when
// somebody wants to read the plan rather than run it.
func Compose(manager *hooks.Manager, toolName string, tool config.Tool, baseCommand, id, model, account string, contributed map[string]string) (string, map[string]string, error) {
	return compose(manager, toolName, tool, baseCommand, id, model, account, contributed, false)
}

// resolveAccount reads an account's token, replaced in tests so a launch
// plan never reaches a real secret store.
var resolveAccount = accounts.Resolve

func compose(manager *hooks.Manager, toolName string, tool config.Tool, baseCommand, id, model, account string, contributed map[string]string, write bool) (string, map[string]string, error) {
	if err := config.CheckInstalled(baseCommand); err != nil {
		return "", nil, err
	}
	if err := checkContributed(tool, contributed); err != nil {
		return "", nil, err
	}
	style, err := mcpreg.Resolve(toolName, tool.MCP)
	if err != nil {
		return "", nil, err
	}
	account, err = WithAccount(tool, account)
	if err != nil {
		return "", nil, err
	}
	// The manager's own path, not its name: it is normally run out of its
	// checkout and is on nobody's PATH, so a session told to type the bare
	// name cannot reach the subcommands at all.
	env := map[string]string{hooks.EnvSessionID: id, hooks.EnvExecutable: Executable(), hooks.EnvExitFile: manager.ExitFile(id)}
	inheritParentEnv(env)
	for key, value := range contributed {
		env[key] = value
	}
	// A relocated board has to reach the worker too: anything the session
	// runs that resolves the config dir falls back to the default when the
	// variable is unset. Passed only when it is actually
	// set: a default-home board must keep composing exactly what it composed
	// before, and hardcoding the fallback here would put a stale absolute path
	// in every worker's environment.
	if home := strings.TrimSpace(os.Getenv(config.HomeEnv)); home != "" {
		env[config.HomeEnv] = home
	}
	// The token is read fresh at every launch and set after the inherited
	// environment, so it wins over a token the operator's own shell exports:
	// a session asked for one person's account must not quietly run on
	// another's.
	if account != "" {
		token, err := resolveAccount(tool, account)
		if err != nil {
			return "", nil, err
		}
		env[tool.AccountEnv] = token
	} else if tool.AccountEnv != "" {
		// A spawning agent may itself be borrowing a subscription.
		env[tool.AccountEnv] = ""
	}
	register := mcpreg.Preview
	if write {
		register = mcpreg.Apply
		// The launch script writes the agent's exit status here and never
		// creates the directory itself.
		if err := os.MkdirAll(manager.Dir(), 0o755); err != nil {
			return "", nil, err
		}
	}
	command, err := register(style, Executable(), manager.Dir(), baseCommand, env, strings.TrimSpace(model))
	if err != nil {
		return "", nil, err
	}
	if tool.StatusSource != hooks.StatusSourceClaude {
		return command, env, nil
	}
	env[hooks.EnvStatusFile] = manager.StatusFile(id)
	// Composed through hooks.SettingsArgv, which is also what the poller
	// looks for in a running process's argv to tell a wired session from one
	// somebody restarted by hand inside its pane. One spelling, so the check
	// cannot go quietly false by this line changing.
	return command + " " + hooks.SettingsArgv(tmux.ShellQuote(manager.SettingsPath())), env, nil
}
