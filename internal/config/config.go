// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/usestring/gate-inbox/internal/logging"
)

// defaultNameSweepPace paces the bulk rename sweep. Slow enough that a
// board of ninety agents wakes up over minutes rather than all at once.
const defaultNameSweepPace = 3 * time.Second

// defaultChildAutoArchive is how long a finished child that never reported
// back stays on the list.
const defaultChildAutoArchive = 30 * time.Minute

type Rule struct {
	State   string `toml:"state"`
	Pattern string `toml:"pattern"`
}

type Tool struct {
	Command string `toml:"command"`
	// Shell marks a block that opens a plain shell rather than an agent
	// CLI: it is what T spawns, it stays out of the CLI pickers, and the
	// keys that write into a pane refuse it, since a sentence typed at a
	// shell is a command. Never inferred, so a tool block only means this
	// when its author said so.
	Shell         bool     `toml:"shell"`
	ReviveCommand string   `toml:"revive_command"`
	PromptFlag    string   `toml:"prompt_flag"`
	PromptMode    string   `toml:"prompt_mode"`
	InterruptKeys []string `toml:"interrupt_keys"`
	// EchoBudget bounds the after-keystroke chase on this tool's panes: how
	// long the focused view keeps looking for the repaint a key caused before
	// leaving it to the tick. It is per tool because agent TUIs differ by
	// more than one number covers -- codex repaints a typed character in
	// 27-45ms where Claude Code takes 10-25ms (measured on a development host), so a
	// budget that fits one gives up on the other mid-repaint and the
	// character waits for the next tick. Unset takes the default.
	EchoBudget Duration `toml:"echo_budget"`
	// SessionIDFlag makes a new session launch with an id we choose (e.g.
	// claude "--session-id <uuid>"), so revive can later resume that
	// exact conversation deterministically.
	SessionIDFlag string `toml:"session_id_flag"`
	// ModelFlag launches a session on a chosen model rather than the CLI's
	// own default (claude/codex/opencode "--model <name>"). Left unset, this
	// tool has no way to be told, and a session asking for a model on it is
	// refused rather than launched on whatever the default happens to be:
	// silently ignoring it is how somebody ends up paying opus rates for a
	// run they asked to be cheap, or reading a haiku answer as an opus one.
	ModelFlag string `toml:"model_flag"`
	// ModelsCommand prints the names ModelFlag accepts, one per line or in
	// whatever shape the CLI prints them ("opencode models"). It exists so a
	// caller can find a model rather than guess one: the names are the CLI's
	// own and change under us, and a guess that misses launches nothing.
	ModelsCommand string `toml:"models_command"`
	// Models is the same answer for a CLI that has no command to ask. It is
	// written here rather than discovered, so keep it to names the CLI
	// documents and expect it to lag; ModelsCommand is preferred wherever
	// the CLI offers one.
	Models []string `toml:"models"`
	// AccountEnv is the variable this CLI reads a subscription token from
	// (claude: CLAUDE_CODE_OAUTH_TOKEN), which is what lets a session be
	// launched on a named account rather than on whichever login the CLI
	// finds for itself. Left unset, a session asking for an account on this
	// tool is refused rather than launched on the default login: the
	// difference is whose usage window the run spends, and it only shows up
	// when that person hits their limit.
	AccountEnv string `toml:"account_env"`
	// AccountSecret is the name of the secret holding one account's token,
	// with {account} standing for the account name in upper case
	// ("CLAUDE_OAUTH_TOKEN_{account}").
	AccountSecret string `toml:"account_secret"`
	// AccountCommand prints one secret's value, with {secret} standing for
	// the name AccountSecret produced. It runs at launch; the token goes into
	// the session's environment and is never written to a row.
	AccountCommand string `toml:"account_command"`
	// AccountsCommand prints the secret names that exist, one per line, so a
	// caller can find an account rather than guess one.
	AccountsCommand string `toml:"accounts_command"`
	// ResumeByIDCommand resumes a specific conversation; "{id}" is replaced
	// with the session's agent id. Preferred over ReviveCommand, which only
	// resumes the working directory's most recent conversation.
	ResumeByIDCommand string `toml:"resume_by_id_command"`
	// ForkCommand creates a new conversation from an existing one. Templates
	// can use {id}, {new_id}, and {name}; Gate Inbox quotes each value.
	ForkCommand string `toml:"fork_command"`
	// ForkDialogOption is the literal label of the option a fork wants from
	// a dialog its own resume raises before the conversation loads, and
	// ForkDialogKeys are the tmux key names that select that option from
	// where the dialog opens. Both are needed for either to do anything, and
	// the label is matched against the pane as plain text: no dialog on
	// screen, no keys. Left unset, a fork that raises a dialog waits for the
	// operator to answer it, which is what every tool did before this.
	ForkDialogOption string   `toml:"fork_dialog_option"`
	ForkDialogKeys   []string `toml:"fork_dialog_keys"`
	// SessionStore names the built-in capturer that reads back the id a tool
	// minted itself when it has no SessionIDFlag ("codex" or "opencode").
	SessionStore string `toml:"session_store"`
	// RenameCommand is the slash command r types into a live pane to ask the
	// agent to name its own session. The agent reads its own conversation and
	// runs `gate-inbox rename`, which is a better name than anything derived
	// from the outside and is the tool's own job.
	//
	// Left unset, r asks in prose instead: every agent CLI can read a sentence
	// and run one shell command, and only some of them have anywhere to install
	// a command in. This names the shortcut where one exists, not whether the
	// tool can be asked at all.
	RenameCommand string `toml:"rename_command"`
	// SkipRenameDirective leaves a launch's first prompt untouched: no rename
	// directive is prepended to it and none is queued behind it. The session
	// is named from the outside instead -- the title its own CLI writes, read
	// by the naming pass -- on demand with r, and silently through the
	// per-session instructions its launch registers. Unset keeps the old
	// behaviour, which asks the agent to rename itself as its first act.
	SkipRenameDirective bool `toml:"skip_rename_directive"`
	// TypeAhead says this tool keeps what is typed into its composer mid-turn
	// and reads it at the next point it can (Claude Code queues it and hands
	// it over after the running tool call). A message to such a session is
	// typed in while it works, as long as its input line is drawn and no
	// dialog is on screen. Without it, a message to an agent that stays busy
	// waits for a rest that may not come for an hour, and a correction meant
	// to stop the work in hand lands only once that work is done.
	TypeAhead bool `toml:"type_ahead"`
	// MCP picks how the Gate Inbox MCP server is registered into this
	// tool's sessions: "claude", "codex", "opencode" or "none".
	// Empty uses the tool's config key when it names a known style.
	MCP            string `toml:"mcp"`
	StatusSource   string `toml:"status_source"`
	DefaultStatus  string `toml:"default_status"`
	ActivityCutoff string `toml:"activity_cutoff"`
	// InputLine is the marker a tool draws at the start of the row its
	// composer is typed on, when that is not the same thing as
	// ActivityCutoff. The two coincide for a tool whose prompt marker is
	// also the line dividing its transcript from its input box (claude's
	// "\u276f"), and they do not for a tool that boxes the composer: opencode
	// draws "\u2503" down the left of every composer row and closes the box
	// underneath with "\u2579", so the cutoff sits BELOW the row the caret is
	// on and no composer row is ever recognised as an input line. Left then
	// has no head of a prompt to find and is forwarded to the pane forever,
	// which is what kept Left from leaving an opencode session. Left unset,
	// the cutoff is used, so tools that need no distinction say nothing.
	InputLine    string `toml:"input_line"`
	TurnEnd      string `toml:"turn_end"`
	ChromeLine   string `toml:"chrome_line"`
	BlockedLine  string `toml:"blocked_line"`
	TrailingNote string `toml:"trailing_note"`
	// BusyLine marks work that outlives the turn which started it, such as
	// background agents and shells. Matching it in the newest turn keeps a
	// turn-end summary from resolving to finished while that work runs.
	BusyLine string `toml:"busy_line"`
	// LimitLine is a usage or rate-limit banner. Matching it in the newest
	// turn is errored even when a turn-end summary or a limit dialog would
	// otherwise settle the turn.
	LimitLine string `toml:"limit_line"`
	// ArrowDialogLine is the keybinding legend of a dialog that navigates
	// with the horizontal arrows, which is what decides whether Left still
	// belongs to the pane once a dialog has parked the caret on its own
	// selection marker. A dialog that only advertises "↑/↓ to navigate" does
	// nothing with Left, so Left is free to mean "back to the list" there --
	// and being able to step out of a session while it is asking something is
	// the case that matters, since that is when the operator most wants to
	// leave it and come back. Left unset, no dialog claims the arrows.
	//
	// Write it against the legend line itself, never against a bare glyph:
	// it is matched over the whole pane, and an arrow there is ordinary
	// prose more often than not ("85% → 60%", "repo#1430 → 95844f0").
	// A pattern that reads a loose ← or → pins the operator in every
	// session whose scrollback happens to carry one.
	ArrowDialogLine string `toml:"arrow_dialog_line"`
	// DialogStepRow is the question stepper a dialog draws above its options
	// ("←  ☐ Shape  ☐ Scope  ✔ Submit  →"), and DialogStepEntry one entry on
	// it. Together they answer the question the legend cannot: which step is
	// live. Left is the previous question everywhere but the first entry, and
	// on the first entry it does nothing at all, so that is the one place it
	// is free to mean "back to the list". Matched against the raw capture,
	// since the active entry is marked by the background colour it is drawn
	// on. Left unset, no dialog has a stepper and the legend decides alone.
	DialogStepRow   string `toml:"dialog_step_row"`
	DialogStepEntry string `toml:"dialog_step_entry"`
	// ScrolledLine is the affordance a tool draws while its own viewport is
	// parked above the live bottom ("Jump to bottom (ctrl+End)"). The pane
	// then shows history, so nothing read off it describes the session now
	// and status holds until the viewport comes back. Left unset, only a
	// forwarded mouse report suggests the operator is scrolling.
	ScrolledLine string `toml:"scrolled_line"`
	// JumpToBottomKey is the key, in tmux's own key syntax, that the tool's
	// jump-back affordance names -- ctrl+End for Claude Code. It is what
	// brings a parked viewport back to the live bottom, and it is here so
	// that a parked pane is something the board can fix rather than a
	// question for a person: a pane showing history cannot be read, and
	// nobody should have to go and scroll it by hand.
	// Only meaningful with scrolled_line, which is what says a pane is parked
	// in the first place.
	JumpToBottomKey string `toml:"jump_to_bottom_key"`
	Rules           []Rule `toml:"rules"`
}

// Log configures the diagnostic file log. The TUI owns the terminal, so
// this file is the only account of what the program did; every field left
// unset takes the built-in default.
type Log struct {
	// Level is off, error, warn, info, debug or trace. Trace is the only
	// level at which captured pane text is written at all, and it is
	// scrubbed even there.
	Level string `toml:"level"`
	// File overrides where the log is written. A directory is accepted as
	// well as a file.
	File       string `toml:"file"`
	MaxSizeMB  int    `toml:"max_size_mb"`
	MaxBackups int    `toml:"max_backups"`
	MaxTotalMB int    `toml:"max_total_mb"`
	// NoCompress keeps rotated files as plain text.
	NoCompress bool `toml:"no_compress"`
}

// Children configures what the list does with sessions another session
// spawned. They arrive in bursts and go quiet in bursts, so the list would
// rather stop drawing the ones that are over than make a person sweep them.
type Children struct {
	// AutoArchiveAfter is how long a child whose pane has exited is kept on
	// the list when it never reported back to its parent. An exited child
	// that did report is archived as soon as the report lands, whatever this
	// says. A child with a live pane is never swept, whatever its status.
	AutoArchiveAfter Duration `toml:"auto_archive_after"`
}

// Work configures what the board does with the pull requests and tickets its
// sessions are on once they are over.
type Work struct {
	// SettleAfter is how long a merged or closed pull request, or a completed
	// or cancelled ticket, stays on the board after the operator first had it
	// on screen in that state. Unset, a day.
	SettleAfter Duration `toml:"settle_after"`
}

// Integrations switches the built-in work providers, the sources the board
// reads pull request and ticket state from.
type Integrations struct {
	GitHub Integration `toml:"github"`
	Linear Integration `toml:"linear"`
}

// Integration is one provider's switch. Linear also needs LINEAR_API_KEY in
// the environment; without it Linear is off whatever this says.
type Integration struct {
	// Enabled is a pointer so that absent means on: a config written before
	// this section existed keeps the providers it had.
	Enabled *bool `toml:"enabled"`
}

// On reports whether the provider is switched on.
func (i Integration) On() bool { return i.Enabled == nil || *i.Enabled }

type Config struct {
	PollInterval Duration `toml:"poll_interval"`
	// AdoptSockets names extra tmux servers to scan for agent panes the
	// manager did not start. The default server is always scanned; this is
	// for sessions kept on a named socket.
	AdoptSockets []string `toml:"adopt_sockets"`
	// TmuxSocket is the tmux server the manager runs its own sessions on.
	// Empty takes GATE_INBOX_TMUX_SOCKET, then tmux's own default server,
	// where a managed session can be attached and driven by hand like any
	// other. Naming a socket here puts them back on a server of their own,
	// which nothing else on the machine shares.
	TmuxSocket string `toml:"tmux_socket"`
	// Editor is the command the o key opens a directory in, arguments
	// included. Empty falls back to $GATE_INBOX_EDITOR, then a GUI
	// editor found on PATH, then $VISUAL / $EDITOR, and last to a
	// terminal editor on PATH -- so this is an override rather than
	// something that has to be set before a key which opens a path works.
	Editor string `toml:"editor"`
	// NameSweepPace is how long the bulk rename sweep waits between panes.
	// Every message it sends starts a turn in somebody's live agent, so the
	// sweep is paced rather than fired at once; a slower machine or a larger
	// board wants a longer gap.
	NameSweepPace Duration     `toml:"name_sweep_pace"`
	Log           Log          `toml:"log"`
	Children      Children     `toml:"children"`
	Work          Work         `toml:"work"`
	Integrations  Integrations `toml:"integrations"`
	// Extensions holds each extension's section, keyed by extension ID
	// ([extensions.<id>]). The config package does not know what is in
	// one: the extension that owns a section decodes and validates it (see
	// the public extension package), so adding an extension never means
	// editing this file.
	Extensions map[string]map[string]any `toml:"extensions"`
	Tools      map[string]Tool           `toml:"tools"`
}

type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalText(text []byte) error {
	parsed, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	d.Duration = parsed
	return nil
}

// HomeEnv moves everything this program keeps -- its config and its sqlite --
// somewhere else.
//
// It exists because the obvious way to do that is XDG_CONFIG_HOME, and that
// moves every other program's config too. Pointing it at a scratch directory
// to keep a trial off real state also hides gh's credentials, so the manager
// reports "gh is not authenticated" while the same shell is logged in.
const HomeEnv = "GATE_INBOX_HOME"

func Dir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv(HomeEnv)); dir != "" {
		return dir, nil
	}
	return DefaultDir()
}

// DirName is the config directory's name under the user config dir.
const DirName = "gate-inbox"

// DefaultDir is the config directory when HomeEnv is unset.
func DefaultDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, DirName), nil
}

func Load() (Config, error) {
	dir, err := Dir()
	if err != nil {
		return Config{}, err
	}
	return LoadDir(dir)
}

// LoadDir loads the configuration kept in dir. Session-scoped commands
// already receive the manager's config directory, so they must not resolve
// it again from a possibly different process environment.
func LoadDir(dir string) (Config, error) {
	path := filepath.Join(dir, "config.toml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := writeDefault(path); err != nil {
			return Config{}, err
		}
	}
	var cfg Config
	if err := decodeInto(path, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	if err := cfg.backfillToolDefaults(); err != nil {
		return Config{}, err
	}
	cfg.applyDefaults()
	return cfg, nil
}

// backfillToolDefaults fills fields the built-in tools gained after a
// user's config.toml was written: existing tools keep their values, but
// any field left at its zero value inherits the built-in default, and
// tools absent from the file are added. This lets older configs pick up
// new capabilities (a new prompt_flag, extra rules) without a rewrite.
func (c *Config) backfillToolDefaults() error {
	if c.Tools == nil {
		c.Tools = map[string]Tool{}
	}
	builtin, err := Default()
	if err != nil {
		return err
	}
	for name, def := range builtin.Tools {
		user, ok := c.Tools[name]
		if !ok {
			c.Tools[name] = def
			continue
		}
		c.Tools[name] = mergeTool(name, user, def)
	}
	return nil
}

// busyLineAgentsOnly, busyLineShellsOnly and busyLineEveryKind are the
// busy_line patterns claude shipped with before this one. The first is the
// agent wait line without dynamic workflows; the second added the bare "N
// shells still running" tail to it; the third read every kind of work named
// in that tail -- shells, monitors, MCP tasks, background tasks -- so a turn
// that ended with a shell or a monitor parked pinned its row at working,
// sometimes for good. A config carrying any of them verbatim was written by
// an older release and takes the current pattern; one edited by hand keeps
// what its author wrote.
const busyLineAgentsOnly = `^[✻✳✶✽✢·✦✧+*] Waiting for \d+ background agents? to finish`

const busyLineShellsOnly = `^[✻✳✶✽✢·✦✧+*] (?:Waiting for \d+ background agents? to finish|.*· \d+ shells? still running)`

const busyLineEveryKind = `^[✻✳✶✽✢·✦✧+*] (?:Waiting for \d+ background agents? to finish|\S+ for \d.*· \d+ [^·]*still running)`

// waitingEnterToConfirmBare is the waiting pattern claude shipped with before
// the anchored one: the bare phrase, matching it anywhere in the turn. A
// config carrying it verbatim was written by an older release and takes the
// current pattern; one edited by hand keeps what its author wrote.
const waitingEnterToConfirmBare = `Enter to confirm`

// mergeTool returns user with any zero-value field filled from def.
//
// Shell is deliberately not among them. "terminal" is a plausible name for
// a hand-rolled agent block, and backfilling the flag onto one would take
// the user's own tool out of the pickers and refuse to prompt it, without
// saying so. A block is a shell only where its author wrote that.
func mergeTool(name string, user, def Tool) Tool {
	fill := func(dst *string, src string) {
		if *dst == "" {
			*dst = src
		}
	}
	fill(&user.Command, def.Command)
	fill(&user.ReviveCommand, def.ReviveCommand)
	fill(&user.ModelFlag, def.ModelFlag)
	fill(&user.ModelsCommand, def.ModelsCommand)
	if len(user.Models) == 0 {
		user.Models = def.Models
	}
	fill(&user.AccountEnv, def.AccountEnv)
	fill(&user.AccountSecret, def.AccountSecret)
	fill(&user.AccountCommand, def.AccountCommand)
	fill(&user.AccountsCommand, def.AccountsCommand)
	fill(&user.PromptFlag, def.PromptFlag)
	fill(&user.PromptMode, def.PromptMode)
	if len(user.InterruptKeys) == 0 {
		user.InterruptKeys = def.InterruptKeys
	}
	if user.EchoBudget.Duration <= 0 {
		user.EchoBudget = def.EchoBudget
	}
	fill(&user.SessionIDFlag, def.SessionIDFlag)
	fill(&user.ResumeByIDCommand, def.ResumeByIDCommand)
	fill(&user.ForkCommand, def.ForkCommand)
	fill(&user.ForkDialogOption, def.ForkDialogOption)
	if len(user.ForkDialogKeys) == 0 {
		user.ForkDialogKeys = def.ForkDialogKeys
	}
	fill(&user.SessionStore, def.SessionStore)
	fill(&user.RenameCommand, def.RenameCommand)
	user.SkipRenameDirective = user.SkipRenameDirective || def.SkipRenameDirective
	user.TypeAhead = user.TypeAhead || def.TypeAhead
	fill(&user.MCP, def.MCP)
	fill(&user.StatusSource, def.StatusSource)
	fill(&user.DefaultStatus, def.DefaultStatus)
	fill(&user.ActivityCutoff, def.ActivityCutoff)
	fill(&user.InputLine, def.InputLine)
	fill(&user.TurnEnd, def.TurnEnd)
	fill(&user.ChromeLine, def.ChromeLine)
	fill(&user.BlockedLine, def.BlockedLine)
	fill(&user.TrailingNote, def.TrailingNote)
	fill(&user.BusyLine, def.BusyLine)
	fill(&user.LimitLine, def.LimitLine)
	fill(&user.ScrolledLine, def.ScrolledLine)
	fill(&user.JumpToBottomKey, def.JumpToBottomKey)
	fill(&user.ArrowDialogLine, def.ArrowDialogLine)
	fill(&user.DialogStepRow, def.DialogStepRow)
	fill(&user.DialogStepEntry, def.DialogStepEntry)
	if name == "codex" {
		if user.ActivityCutoff == `(?m)^›` {
			user.ActivityCutoff = def.ActivityCutoff
		}
		if user.ChromeLine == `^\s*─*\s*$` {
			user.ChromeLine = def.ChromeLine
		}
	}
	if name == "claude" && (user.BusyLine == busyLineAgentsOnly || user.BusyLine == busyLineShellsOnly || user.BusyLine == busyLineEveryKind) {
		user.BusyLine = def.BusyLine
	}
	if len(user.Rules) == 0 {
		user.Rules = def.Rules
	} else if name == "claude" {
		upgradeBareEnterToConfirm(user.Rules, def.Rules)
		user.Rules = withAskUserQuestionRules(user.Rules, def.Rules)
	} else if name == "opencode" {
		user.Rules = withDialogRules(user.Rules, def.Rules, opencodeDialogSamples)
	} else if name == "codex" {
		for i, rule := range user.Rules {
			if rule.State != "working" || rule.Pattern != `(?m)esc to interrupt\b` {
				continue
			}
			for _, current := range def.Rules {
				if current.State == "working" {
					user.Rules[i] = current
					break
				}
			}
		}
	}
	return user
}

// askUserQuestionSamples are lines Claude Code draws on an AskUserQuestion
// dialog: the keybinding legend under a question, and the question the
// review page a stepper dialog ends on asks in place of a legend. They are
// the samples, not the rules: a config written before the dialog was
// recognized keeps its own rules forever, and one of them reading a line is
// what says that page is already covered. A user who wrote a different
// pattern that matches it keeps theirs.
const askUserQuestionLegend = "Enter to select \u00b7 \u2191/\u2193 to navigate \u00b7 Esc to cancel"
const askUserQuestionReview = "Ready to submit your answers?"

var askUserQuestionSamples = []string{askUserQuestionLegend, askUserQuestionReview}

// upgradeBareEnterToConfirm swaps the superseded bare phrase for whichever
// default rule reads a real legend, in place, so the rule keeps the position
// its author gave it and the rest of the block is left as written.
func upgradeBareEnterToConfirm(user, def []Rule) {
	for i, r := range user {
		if r.State != "waiting" || r.Pattern != waitingEnterToConfirmBare {
			continue
		}
		for _, d := range def {
			if d.State != "waiting" || !strings.Contains(d.Pattern, waitingEnterToConfirmBare) {
				continue
			}
			user[i] = d
			break
		}
	}
}

// opencodeDialogSamples are the rows of opencode's two blocking overlays: the
// banner and legend of a permission ask, and the legend of the select prompt an
// agent raises to ask its own question. Both dialogs were recognized after the
// tool block shipped, so a config written before either one carries a rules
// array with no waiting rule in it at all and reads a blocked session as
// working off the spinner row above the overlay.
var opencodeDialogSamples = []string{
	"\u25b3 Permission required",
	"ctrl+f fullscreen  \u21c6 select  enter confirm",
	"\u2191\u2193 select  enter submit  esc dismiss",
}

// withAskUserQuestionRules adds the built-in rule for each page of that
// dialog to a user's claude rules when none of them recognize it, keeping it
// among the waiting rules that lead the list.
func withAskUserQuestionRules(user, def []Rule) []Rule {
	return withDialogRules(user, def, askUserQuestionSamples)
}

// withDialogRules adds the built-in waiting rule for each sample line a user's
// rules do not already read, keeping the additions among the waiting rules that
// lead the list.
func withDialogRules(user, def []Rule, samples []string) []Rule {
	for _, sample := range samples {
		user = withRuleFor(sample, user, def)
	}
	return user
}

func withRuleFor(sample string, user, def []Rule) []Rule {
	for _, r := range user {
		if r.State != "waiting" {
			continue
		}
		re, err := regexp.Compile(r.Pattern)
		if err == nil && re.MatchString(sample) {
			return user
		}
	}
	for _, d := range def {
		if d.State != "waiting" {
			continue
		}
		re, err := regexp.Compile(d.Pattern)
		if err != nil || !re.MatchString(sample) {
			continue
		}
		at := 0
		for at < len(user) && user[at].State == "waiting" {
			at++
		}
		out := make([]Rule, 0, len(user)+1)
		out = append(out, user[:at]...)
		out = append(out, d)
		return append(out, user[at:]...)
	}
	return user
}

func decodeInto(path string, cfg *Config) error {
	meta, err := toml.DecodeFile(path, cfg)
	if err != nil {
		return err
	}
	for _, section := range retiredSections {
		if meta.IsDefined(section) {
			logging.Warn("config section is no longer read; it is ignored", "path", path, "section", section)
		}
	}
	return nil
}

// retiredSections are sections an older build read and this one does not. A
// config that still has one loads as if it did not, with a warning, so an
// upgrade never costs anybody their board.
var retiredSections = []string{"supervisor"}

// Default returns the built-in configuration without touching the filesystem.
func Default() (Config, error) {
	var cfg Config
	if _, err := toml.Decode(defaultConfig, &cfg); err != nil {
		return Config{}, err
	}
	cfg.applyDefaults()
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.PollInterval.Duration <= 0 {
		c.PollInterval.Duration = 2 * time.Second
	}
	if c.NameSweepPace.Duration <= 0 {
		c.NameSweepPace.Duration = defaultNameSweepPace
	}
	if c.Children.AutoArchiveAfter.Duration <= 0 {
		c.Children.AutoArchiveAfter.Duration = defaultChildAutoArchive
	}
	if c.Tools == nil {
		c.Tools = map[string]Tool{}
	}
	for name, tool := range c.Tools {
		if tool.DefaultStatus == "" {
			tool.DefaultStatus = "idle"
			c.Tools[name] = tool
		}
	}
}

func (c Config) ToolNames() []string {
	names := make([]string, 0, len(c.Tools))
	for name := range c.Tools {
		names = append(names, name)
	}
	return names
}

// ShellTool returns the first shell block by name, making the choice stable
// when a user configures more than one.
func (c Config) ShellTool() (string, Tool, bool) {
	chosen := ""
	for name, tool := range c.Tools {
		if tool.Shell && (chosen == "" || name < chosen) {
			chosen = name
		}
	}
	if chosen == "" {
		return "", Tool{}, false
	}
	return chosen, c.Tools[chosen], true
}

func writeDefault(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(defaultConfig), 0o644)
}

const defaultConfig = `poll_interval = "2s"

# The shared artifact store. Agents publish HTML, markdown or JSON to a URL
# and hand the link to you or to each other; the link carries its own key, so
# whoever has it can open that one artifact and nothing else. An artifact
# published by a session on one pooled account is readable by a session on
# another -- and by a CLI that has no artifacts of its own.
#
# Off until you turn it on. An extension that is off registers no tools, so
# an agent here neither sees them nor pays context for them. Turning it off
# again does not break links already handed out, because the worker serving
# them does not consult this file.
#
# It has no store of its own: base_url and key_command name the worker you
# deployed and how to read its signing key, and until both are set the tools
# stay off whatever enabled says.
#
# [extensions.artifacts]
# enabled = true
# base_url = "https://artifacts.example.com"
# key_secret = "ARTIFACT_SIGNING_KEY"
# key_command = "pass show {secret}"
# identity_command = "git config user.email"   # who is recorded as publishing
# link_ttl = "720h"         # how long a minted link stays valid; Go durations have
#                           # no day unit, so this is hours (720h = 30 days)
# max_bytes = 16777216      # largest artifact this client will send

# The tmux server sessions run on. Unset, they run on tmux's own default
# server, so "tmux ls" lists them and "tmux attach -t gi_<id>" reaches one.
# Naming a socket here gives them a server nothing else shares, reachable
# only as "tmux -L <socket>".
# tmux_socket = "gate-inbox"

# Sessions another session spawned. A child whose pane has exited is archived
# as soon as its report to the parent lands; one that never reported is kept
# this long after it exits, then archived too. A child with a live pane is
# never swept, whatever it reads as. The parent's badge goes on
# counting them as "(+N done)".
# [children]
# auto_archive_after = "30m"

# The pull requests and tickets sessions are on. One that is over -- merged,
# closed, completed, cancelled -- stays on the board this long after you first
# see it that way, then leaves the rail and the counts for good.
# [work]
# settle_after = "24h"

# The diagnostic log. It is a file log only -- this program owns the
# terminal, so nothing is ever printed to the screen. "gate-inbox
# --log-path" prints where it is written.
# [log]
# level = "info"          # off, error, warn, info, debug, trace
# file = ""               # default: <config dir>/logs/gate-inbox.log
# max_size_mb = 8         # rotate once the active file reaches this
# max_backups = 5         # rotated files kept, oldest deleted first
# max_total_mb = 48       # hard cap on everything this log holds
# no_compress = false     # rotated files are gzipped by default
#
# level = "trace" is the only setting under which captured pane text
# reaches the log, and secrets are scrubbed even then.

# The editor "o" opens a directory in, arguments allowed: "code -n", or
# "open -a 'Visual Studio Code'". Quotes group an argument that carries a
# space; the line is run directly, never through a shell. Left unset,
# Gate Inbox takes $GATE_INBOX_EDITOR, then the first GUI editor on
# PATH (code, cursor, windsurf, zed, subl, idea), then $VISUAL or $EDITOR,
# and last a terminal editor on PATH (nvim, vim, nano, vi). Setting this is
# how you override that order, not how you switch the editor on.
# editor = "code"

# Rules are matched top-down against the visible pane text (ANSI stripped);
# first match wins, except a matching waiting rule outranks a working match.
# A limit_line match is errored even when a turn-end summary or a limit
# dialog would otherwise settle the turn.
# When no rule matches, the newest turn decides:
# the content region is the text above the last activity_cutoff match
# (the tool's input box). If the region's last content line — skipping
# chrome_line matches (blanks, separators, input-box borders) — is a
# turn_end marker, the turn just ended: finished, or waiting when the
# line above it carries a question mark. A blocked_line there (e.g. an
# interrupt banner) also derives waiting. Otherwise default_status
# applies, and a region whose newest content lines changed since the
# previous poll counts as working (streaming output often renders without
# any spinner). Only the newest lines count, so a prompt someone is writing
# -- which grows the input box and scrolls the oldest rows off the top of
# the pane -- is not mistaken for output. A turn
# that closes without any turn_end marker still resolves: when a working
# region stops changing and nothing matches, its last content line
# decides finished versus waiting (question mark waits).
#
# None of that runs while a scrolled_line match says the tool has parked
# its own viewport above the live bottom: the visible screen is history
# then, and a session holds the status it already had until the viewport
# comes back.

[tools.claude]
command = "claude"
# text typed mid-turn is queued and read after the running tool call, so a
# message need not wait for the turn to end
type_ahead = true
# Escape stops a running turn; a second one opens the rewind menu, so
# Gate Inbox never sends it twice for one message
interrupt_keys = ["Escape"]
# claude 2.1.263: --model <model>
model_flag = "--model"
# Named subscriptions are off: a session runs on claude's own stored login.
# To launch on a named account instead, say where its token comes from.
# claude reads a token from account_env ahead of its own login; {account} is
# the account name in upper case, and {secret} the name account_secret made.
#
# account_env = "CLAUDE_CODE_OAUTH_TOKEN"
# account_secret = "CLAUDE_OAUTH_TOKEN_{account}"
# account_command = "pass show claude/{secret}"
# accounts_command = "pass ls claude"
# claude has no command that prints these; they are the documented aliases.
# The 1M-context variants sit beside the bare ones because an explicit --model
# overrides the operator's settings.json default: picking "opus" for someone
# who defaults to "opus[1m]" drops that session to the standard window, and
# nothing says so until it compacts early.
models = ["sonnet", "sonnet[1m]", "opus", "opus[1m]", "haiku"]
# revive (v) launches a new session with this id, so it can later resume
# that exact conversation regardless of what else ran in the directory
session_id_flag = "--session-id"
resume_by_id_command = "claude --resume {id}"
fork_command = "claude --resume {id} --fork-session --session-id {new_id} --name {name}"
# Resuming a large conversation opens a dialog offering a summary instead,
# with the summary preselected -- and a fork answered that way keeps a
# summary rather than the responses it was made for. The manager picks the
# full-session option itself; the keys move one row down from where the
# dialog opens and confirm.
fork_dialog_option = "Resume full session as-is"
fork_dialog_keys = ["Down", "Enter"]
# fallback when a session predates id tracking: resumes the last conversation there
revive_command = "claude --continue"
# r asks the session to name itself with this instead of guessing from the
# transcript: /rename is a project command that runs gate-inbox rename
rename_command = "/rename"
# hooks report status events directly; the pane rules below stay as fallback
status_source = "claude-hooks"
default_status = "idle"
activity_cutoff = "(?m)^❯"
turn_end = "^[✻✳✶✽✢·✦✧+*] \\S+ for \\d.*$"
# Box borders, plus the right-aligned "✔ Update installed · Restart to
# update" banner claude draws above the composer once a CLI update has been
# staged. Unmatched it becomes the region's last content line and is not a
# turn_end marker, so every verdict collapses to default_status with
# matched=false -- and matched is what a claude-hooks session depends on:
# only a matched pane verdict talks a stale "working" hook file back down,
# and inbox delivery never fires while a session reads working.
#
# Chrome and not trailing_note on purpose. trailing_note is sticky -- the
# first match makes settledBelow skip everything after it, so a newer turn
# streaming below the banner would still read finished -- and only
# chrome_line is consulted by lastContentIndex, so only chrome_line keeps the
# banner from masking a blocked_line interrupt above it.
chrome_line = "^\\s*[─q]{4,}.*$|^[\\s─q]*$|^\\s*✔ Update installed\\b.*$"
blocked_line = "Interrupted ·"
# recap blocks ("※ recap: …") render below the turn-end summary
trailing_note = "^※"
# Only background subagents keep a turn busy. While any are pending, Claude
# replaces the turn-end summary with its own wait line ("✻ Waiting for 2
# background agents to finish", "✻ Waiting for 1 background agent and 1
# dynamic workflow to finish") and drops the summary's "still running" tail
# altogether, so agents never appear in that tail. What the tail does name --
# shells, monitors, MCP tasks, background tasks ("✻ Worked for 0s · done 4:28
# AM · 8 shells, 2 monitors still running") -- runs on while the prompt takes
# input: a monitor can sit armed for hours, and a row pinned at working for it
# hides a prompt that is ready. Such a turn is finished.
busy_line = "^[✻✳✶✽✢·✦✧+*] Waiting for \\d+ (?:background agents?|dynamic workflows?)"
# a usage/rate-limit banner sits above the turn-end summary
limit_line = "(?m)You've hit your .+limit"
# claude draws this over the last content row while its own viewport is
# parked above the live bottom
scrolled_line = "Jump to bottom \\(ctrl\\+End\\)"
# The key that affordance names, for the board to press itself rather than ask.
jump_to_bottom_key = "C-End"
# A dialog claims Left only where it says so: in the "… to navigate" segment of
# its own keybinding legend. Reading a loose ← or → anywhere in the pane is what
# pinned the operator, because a multi-question dialog draws a decorative
# "←  ☐ Shape  ☐ Scope  ✔ Submit  →" stepper above its options while its legend
# says "↑/↓ to navigate · n to add notes · Tab to switch questions" -- Tab is what
# steps between questions, not Left -- and because ordinary prose carries arrows
# too. So require the arrow wording inside that one segment: "Tab/Arrow keys to
# navigate" claims Left, "↑/↓ to navigate" leaves it alone, and a permission
# prompt ("Enter to confirm · Esc to cancel") has no such segment at all.
arrow_dialog_line = "(?i)\\x{B7} [^\\x{B7}\\n]*(?:Arrow keys|\\x{2190}|\\x{2192})[^\\x{B7}\\n]* to navigate\\b"
# The question stepper, drawn above the options whenever the dialog has more
# than one step: "←  ☐ Shape  ☐ Scope  ✔ Submit  →" for three questions, and
# "←  ☐ Checks  ✔ Submit  →" for a single multi-select one, which has the
# question and Submit. Where it sits decides Left, because Left IS the stepper:
# one entry in, Left steps back a question; on the first entry it does nothing
# (measured live -- the pane comes back byte for byte identical), so only there
# is it free to leave. A dialog with no stepper falls back to the legend above.
#
# The entry glyph carries whether the step has been ANSWERED, so the class
# needs every state a step is drawn in: an answered question comes back ☒, and
# a class missing it leaves nothing visible to the left of the live entry, so a
# later question reads as the first and Left leaves instead of stepping back.
dialog_step_row = "^[ \\x{A0}]*\\x{2190}[ \\x{A0}].*[ \\x{A0}]\\x{2192}[ \\x{A0}]*$"
dialog_step_entry = "[\\x{2610}-\\x{2612}\\x{2714}]"
rules = [
  # Selection dialogs (trust prompt, permission asks, questions) block on the
  # user and close with "Enter to confirm" on their keybinding legend. The
  # phrase alone is not that legend, and this rule leads the list, so a turn
  # merely PRINTING those words outranks the working spinner below -- a line
  # like  138    waitForPaneText(t, m, sessID, "Enter to confirm")
  # scrolling past in a diff read as a dialog. So require the phrase to BE a legend segment:
  # starting its line (past any indent or box rule) or following a separator,
  # then ending the line or running into the next segment. That reads
  # "Enter to confirm \x{B7} Esc to cancel" and "\x{2191}/\x{2193} to select,
  # Enter to confirm", while quoted source and prose keep their own brackets,
  # quotes and words around it.
  { state = "waiting", pattern = "(?m)^[ \\x{A0}\\x{2502}|]*(?:[^\\n]*[,\\x{B7}][ \\x{A0}]*)?Enter to confirm(?:[ \\x{A0}]*[,\\x{B7}][^\\n]*)?[ \\x{A0}\\x{2502}|]*$" },
  { state = "waiting", pattern = "(?m)^[ \\x{A0}]*❯[ \\x{A0}]+\\d+\\." },
  # AskUserQuestion marks only the selected option with ❯, and that marker is
  # itself an activity_cutoff match, so the two rules above sit above the cutoff
  # and the dialog reads as idle. Its keybinding legend is the signal instead: a
  # whole line running from "Enter to select" to "Esc to cancel", middot
  # separated. How many segments sit between them is the dialog's own business
  # -- a multi-question one adds "n to add notes" and "Tab to switch questions"
  # -- so anchor both ends and let the middle be any number of segments.
  { state = "waiting", pattern = "(?m)^[ \\x{A0}]*Enter to select \\x{B7} (?:[^\\x{B7}\\n]+\\x{B7} )*Esc to cancel[ \\x{A0}]*$" },
  # The review page a stepper dialog ends on: the answers listed, "Ready to
  # submit your answers?", then "❯ 1. Submit answers / 2. Cancel" -- and no
  # legend at all, so the rule above cannot see it, and the marker row is the
  # activity cutoff so the numbered rule cannot either. The question line sits
  # above the cutoff and inside the newest turn, which is where rules look.
  { state = "waiting", pattern = "(?m)^[ \\x{A0}]*Ready to submit your answers\\?[ \\x{A0}]*$" },
  # spinner row of an active turn, any duration format:
  # "✳ Drizzling… (6s · thinking)" / "✽ Zigzagging… (3m 18s · ↓ 1.4k tokens)"
  { state = "working", pattern = "(?m)^[✻✳✶✽✢·✦✧+*] \\S+… \\(" },
  { state = "working", pattern = "esc to interrupt" },
  { state = "errored", pattern = "(?im)^\\s*error:" },
]

[tools.opencode]
command = "opencode"
# No model_flag: opencode's TUI has none, so the manager writes the chosen
# "provider/model" into the generated OPENCODE_CONFIG instead.
# every provider/model pair this login can reach, one per line
models_command = "opencode models"
# opencode mints its own session id; capture it after launch and resume it
session_store = "opencode"
resume_by_id_command = "opencode --session {id}"
# No fork_command: opencode's TUI has no fork flag, so the manager copies the
# conversation through opencode's API and launches the copy with
# resume_by_id_command.
revive_command = "opencode --continue"
# The opening prompt is typed in once the composer is up, the way a message
# from another session is, and submitted by the same Enter that follows the
# manager's own paste. v2's --prompt fills the composer without submitting
# it, and the poll that pressed Enter for it could not tell the manager's
# prompt from a line the operator was writing.
prompt_mode = "send"
# r asks the session to name itself with this: /rename is a command the
# manager registers into every opencode session it starts, through the
# generated OPENCODE_CONFIG (internal/mcpreg), so there is nowhere to install
# it and nothing that can shadow it.
rename_command = "/rename"
# A launch carries no rename directive: the agent is not asked to name itself
# as its first act, when it has the least context and the name comes out vague.
# Naming comes from the session.title opencode writes itself (the naming pass
# ignores its "New session - …" placeholder until the real title lands), on
# demand with the command above, and silently through the instructions the same
# generated config carries.
skip_rename_directive = true
default_status = "idle"
activity_cutoff = "(?m)^\\s*╹"
# opencode draws its composer as a box: "┃" down the left of every row it is
# typed on, closed underneath by the "╹" the cutoff finds. The cutoff is
# therefore a row BELOW the caret and cannot recognise the input line, so the
# composer names itself here. The bar alone is the marker: tmux trims a row's
# trailing blanks, so an empty composer row is the bar and nothing else, and a
# marker that demanded the box's padding would miss exactly the empty prompt
# Left is meant to leave from.
input_line = "^[ \\x{A0}]*┃"
# The finished-turn row: "Build · DeepSeek V4 Pro (New) · 4.8s · 66.7 tok/s".
# The anchor is the duration itself: a digit-led "· 4.8s" / "· 1m 22s", then
# an optional "· N tok/s". The composer's own model row ("Build auto ·
# DeepSeek V4 Pro (New) OpenCode Go") has no duration and opens with the ┃
# bar, so it never reads as a finished turn.
# Wide panes put v2's sidebar on the same row ("… tok/s      $0.01 spent"), so
# a tail after two or more spaces is allowed.
turn_end = "^ *[^ ┃].*· \\d+(?:\\.\\d+)?[hms](?: \\d+[hms])*(?: · [\\d.]+ tok/s)?(?: {2,}.*)?$"
# Composer rows, and (second branch) v2's sidebar: on a wide pane it lays
# "Context", "MCP" and the connection list in a column past the transcript,
# which capture-pane renders as rows of 40+ leading spaces. Those must not
# count as content below the finished-turn row or the turn never settles.
chrome_line = "^\\s*(┃.*)?$|^ {40,}\\S.*$"
limit_line = "(?i)requires more credits|(?:Usage|Free|Go) limit reached"
# The permission overlay steps its options with the horizontal arrows
# ("ctrl+f fullscreen  ⇆ select  enter confirm"), so Left belongs to the pane
# there. A question dialog instead names "↑↓ select" -- "⇆ tab" only where
# there is something to tab between -- so Left is spare and leaves focus.
# Keyed on "⇆ select" rather than a bare ⇆ for that reason: the tab segment
# must not pin the operator in a dialog that is asking them something.
arrow_dialog_line = "\\x{21C6} select"
rules = [
  # a permission ask is an overlay: it replaces the input box, so nothing
  # below it settles the turn and the still-open turn's spinner row above
  # it keeps matching working. Both lines are verbatim from captured
  # overlays (../status/testdata/opencode-permission-*.txt), and both leave
  # the pane when the ask is answered -- neither lands in the transcript,
  # so a past ask cannot match again.
  { state = "waiting", pattern = "\\x{25B3} Permission required" },
  # its keybinding legend: "ctrl+f fullscreen  ⇆ select  enter confirm"
  { state = "waiting", pattern = "\\x{21C6} select\\s+enter confirm" },
  # opencode's other blocking overlay is the prompt an agent raises to ask its
  # own question: a numbered list of options under the question text, closed by
  # a legend of its own. It shares no wording with the permission ask above --
  # nothing says "permission", and its keys are the vertical arrows -- so the
  # only rule it otherwise trips is the working spinner of the still-open turn
  # that sits above the overlay.
  #
  # The legend is assembled from up to four segments, and the leading two drop
  # out on their own conditions ("⇆ tab" only with something to tab between,
  # "↑↓ select" only with something to step through), while the enter verb
  # is the dialog's own ("submit" for a select, "toggle" for a multi-select,
  # "confirm" otherwise). Anchor on the two that always render together, the
  # enter verb and the trailing "esc dismiss", and let the row's head be
  # whatever the dialog and its box border put there. The gap between them is
  # drawn spaces however wide the row gets, so it is matched as spaces rather
  # than \s, which would also join an "enter submit" ending one transcript
  # line to an "esc dismiss" opening the next.
  { state = "waiting", pattern = "(?m)^.*\\benter (?:submit|toggle|confirm)[ \\x{A0}]+esc dismiss[ \\x{A0}]*$" },
  { state = "errored", pattern = "(?i)requires more credits" },
  { state = "errored", pattern = "(?im)^\\s*error\\b" },
  # opencode draws no agent row mid-turn; its only spinner is the footer
  # "⬝⬝⬝⬝⬝⬝⬝⬝ esc interrupt" under the composer, which this rule reads because
  # the match scope takes in a footer that trips a working rule
  # (status.matchScope).
  { state = "working", pattern = "esc interrupt" },
]

[tools.codex]
command = "codex"
# codex-cli 0.153.4: -m, --model <MODEL>
model_flag = "--model"
# codex repaints a typed character in 27-45ms against Claude Code's 10-25ms,
# so the default chase gives up mid-repaint and the key waits for a tick
echo_budget = "90ms"
# No rename_command on purpose. Codex ships a BUILT-IN /rename ("rename the
# current thread") which shadows any prompt file of that name, so typing it
# renamed codex's own thread and never reached the manager: a rename that
# reported success and left the board's row exactly as it was. Codex 0.153
# does not offer ~/.codex/prompts/*.md as slash commands at all, so there is
# no project command to point at instead. Falling through to the prose
# directive is what actually renames the session.
# codex mints its own session id; capture it after launch and resume it
session_store = "codex"
resume_by_id_command = "codex resume {id}"
fork_command = "codex fork {id}"
# fallback: resumes the most recent session in the working directory
revive_command = "codex resume --last"
default_status = "idle"
# The animated composer paints Braille dots above its input row; those redraws
# are not agent output and must stay outside the activity fingerprint.
activity_cutoff = "(?m)^(?:[ \\t\\x{2800}-\\x{28ff}]*\\n)*›"
input_line = "^›"
# Codex 0.153 separates turns with a full-width horizontal rule, drawn as the
# next turn opens, and no longer draws the "─ Worked for 12s ─" summary older
# builds closed a command-running turn with (it drew nothing at all for a
# conversational one). Both are matched, because a board outlives a codex
# upgrade and a pane already on screen still carries whichever its build drew.
#
# This marker is also what scopes rule matching to the current turn, so a
# pattern that matches nothing is not a missing nicety: with the summary gone,
# nothing was ever scoped away and one "■ … error …" line went on deciding the
# session's status for every later turn. A session resting at its prompt read
# errored until the pane scrolled clean.
#
# A bare rule cannot be told from one codex draws mid-turn between tool output
# and the final response, so scope can start one message late and miss an error
# printed above it. That way round is the cheap one: the turn is still live, the
# \z-anchored working rule below still matches it, and the next poll of a
# settled turn reads the whole thing.
turn_end = "(?m)^(?:─+ Worked for [\\dhms. ]+─.*|─{20,}\\s*)$"
chrome_line = '^\s*─*\s*$|^⚠ (?:The \S+ MCP server is not logged in\. Run \x60codex mcp login \S+\x60\.|MCP startup incomplete \(failed: [^)]+\))$'
limit_line = "(?m)You've hit your usage limit"
rules = [
  # bottom-pane dialogs (command approval, choice prompts, first-run trust)
  # select a numbered option and block on the user's answer
  { state = "waiting", pattern = "(?m)^\\s*›\\s+\\d+\\." },
  { state = "waiting", pattern = "(?m)Press enter to (confirm|continue)\\b" },
  { state = "waiting", pattern = "(?m)enter to submit answer\\b" },
  # active status row is the final row above the input box; anchoring its full
  # shape keeps an answer that quotes "esc to interrupt" from looking active
  { state = "working", pattern = "(?m)^[ \\t]*(?:• )?[^\\n]*\\([\\dhms. ]+ [•·] esc to interrupt\\)(?: · [^\\n]*)?[ \\t]*\\n(?:[ \\t]+└[^\\n]*\\n(?:[ \\t]{4}[^\\n]*\\n)*)?[ \\t\\n]*\\z" },
  { state = "errored", pattern = "(?im)^\\s*■.*\\berror\\b" },
]

# The terminal tab "T" spawns: a shell in the group's directory, listed
# beside the agents but with nothing running in it. An empty command leaves
# the pane on $SHELL; set one to open a different shell instead. shell = true
# is what marks it: the CLI pickers skip it, and the keys that write into a
# pane refuse it, because a sentence typed at a shell is a command.
[tools.terminal]
command = ""
shell = true
default_status = "idle"
`
