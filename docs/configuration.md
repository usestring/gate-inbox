<!-- Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE. -->

# Configuration

Config lives in your OS user config dir (`~/Library/Application Support/gate-inbox/config.toml` on macOS, `~/.config/gate-inbox/config.toml` on Linux, with `XDG_CONFIG_HOME` honored when set) and is created on first run with defaults for the three supported CLIs: Claude Code, Codex, and OpenCode v2. Any other CLI can be added as its own `[tools.<name>]` block (below); nothing about it is built in, so its block has to say everything the manager needs to know.

Top-level: `poll_interval` (default `"2s"`) sets how often panes are polled for status, preview, and stats. `name_sweep_pace` (default `"3s"`) sets how long the `N` name sweep waits between panes; every message it sends starts a turn in a live agent, so a larger board wants a longer gap. `editor` is the command `o` opens a directory in, arguments included (`editor = "code -n"`, `editor = "open -a 'Visual Studio Code'"`); it is run directly rather than through a shell, and quotes group an argument carrying a space. Left unset, Gate Inbox falls back to `$GATE_INBOX_EDITOR`, then a GUI editor on `PATH`, then `$VISUAL` / `$EDITOR`, and last a terminal editor on `PATH` (see [Opening the editor](usage.md#opening-the-editor)). Nothing here has to be set for a key that opens a path to work.

The generated file also carries a `[tools.terminal]` block. That one is the shell `T` opens, not an agent CLI: an empty `command` leaves the pane on `$SHELL`, and setting one opens a different shell. `shell = true` is what marks it — never the name — so the tool pickers skip it and the keys that write into a pane refuse it (see [Terminal tabs](usage.md#terminal-tabs)). Any block can carry the flag, and a `[tools.terminal]` block already in your own config keeps whatever it already means.

Add any CLI tool as a `[tools.<name>]` block:

```toml
[tools.mytool]
command = "mytool"
default_status = "idle"
rules = [
  { state = "working", pattern = "esc to interrupt" },
  { state = "errored", pattern = "(?im)^\\s*error:" },
]
```

Rules match top-down against the visible pane text; first match wins, and `default_status` applies when nothing matches.

**Status detection.** Optional per-tool fields refine it: `activity_cutoff` (regex locating the tool's input box, everything above it is turn content), `turn_end` (a turn-summary line marking the turn as over), `busy_line` (background subagents still pending after the turn, named in Claude Code's own wait line), `limit_line` (a usage or rate-limit banner; the session is `errored`), `chrome_line`, `blocked_line`, `trailing_note`, and `scrolled_line` (the affordance a tool draws while its own viewport is parked above the live bottom, such as Claude Code's "Jump to bottom (ctrl+End)"; the visible screen is history then, so status holds until the viewport comes back). `rename_command` is the slash command `r` types into the pane to have the agent name its own session (`/rename` for Claude Code, whose command directory this repo installs into, and for OpenCode, where the manager registers it per session through the generated `OPENCODE_CONFIG`); left unset, `r` asks for the same thing in prose, so a tool with nowhere to install a command is still asked rather than guessed at. `skip_rename_directive` leaves a launch's first prompt untouched — no directive prepended, none queued behind it — for a tool whose sessions are named from the outside instead (OpenCode sets it: naming comes from its own `session.title`, on demand with `/rename`, and silently through the instructions the same generated config carries). `interrupt_keys` are what stop a running turn, for the operator's rescind and for a message sent with `interrupt` (Claude Code: `Escape`); a tool without them refuses interrupting sends. `type_ahead` lets a queued message be typed into the session mid-turn, for a tool that keeps what it is handed and reads it once the running step ends (Claude Code sets it); a dialog or an undrawn input line still holds it. `status_source = "claude-hooks"` switches status to Claude Code hook events (see [Status](usage.md#status)). The generated config's `claude` and `opencode` blocks show all of them in use.

**Revive.** `resume_by_id_command` resumes one exact conversation, with `{id}` replaced by the session's captured agent id. That id comes either from launching under an id the manager mints (`session_id_flag`, e.g. `--session-id`) or from reading back an id the tool minted itself (`session_store = "codex" | "opencode"`, or the style of a tool driver an extension in your build supplies). `revive_command` is what `v` falls back to when no id is available, e.g. `claude --continue`. Gate Inbox shell-quotes `{id}`, as it does for a fork, so write the placeholder bare: `codex resume {id}`.

**Forks.** `fork_command` creates a conversation from an existing session. Gate Inbox replaces and shell-quotes these placeholders:

- `{id}`: The source conversation ID.
- `{new_id}`: A new UUID that Gate Inbox records for exact revival.
- `{name}`: The new Gate Inbox session name.
- `{session_file}`: The file on disk holding the source conversation, for a CLI that forks by loading a file. Only a tool driver can find one, so it needs `session_store` to name a driver that supports it.

A `fork_command` references its source through `{id}` or `{session_file}`, so one of the two is required. Claude Code and Codex include default fork commands. OpenCode needs none: it forks through its own API and launches the copy with `resume_by_id_command`. A custom tool can omit `{new_id}` when its `session_store` captures the generated ID.

`fork_dialog_option` and `fork_dialog_keys` answer a dialog the fork's own resume raises before the conversation loads. Claude Code offers to resume a large conversation from a summary and preselects that option, so the quickest answer forks a summary rather than the responses the fork was made for; the `claude` block therefore ships `fork_dialog_option = "Resume full session as-is"` with `fork_dialog_keys = ["Down", "Enter"]`, and the manager picks that option itself. The option is matched against the pane as plain text, so nothing is ever typed at a pane that is not showing it, and both fields are needed for either to act. Left unset — every other tool — a fork that raises a dialog waits for you to answer it. The answer is armed for five minutes after the fork and dropped once sent.

**Prompts.** `prompt_flag` controls how the new-session form's optional prompt is embedded into the launch command. Tools that take the prompt as a positional argument (Claude Code: `claude 'the prompt'`) leave it empty; tools whose positional argument means something else declare the flag (OpenCode: `prompt_flag = "--prompt"`, since its positional argument is the project path). `prompt_mode = "send"` handles a persistent CLI that accepts no startup prompt: Gate Inbox waits until `activity_cutoff` finds its input box, then submits the prompt there (OpenCode uses this). The prompt setting only affects a new launch; revive (`v`) uses the revive commands untouched.

**Rescinding.** `interrupt_keys` is the sequence of exact tmux key names `ctrl+z` sends to stop the latest submission, such as `["C-c"]`. An omitted sequence sends `Escape`.

**MCP.** `mcp = "claude" | "codex" | "opencode" | "none"` picks how the Gate Inbox MCP server is registered into the tool's sessions (see [MCP](usage.md#mcp-how-agents-discover-these-commands)). An empty value uses the tool's config key when it names a known style.

**Tool drivers.** A build can carry extensions that teach it CLIs the core has no code for. Such a driver registers the MCP server, reads back the conversation id, finds a conversation's file and hands one over for a migration. A driver has a style name, and a tool block uses it the way it uses a built-in: `mcp = "<style>"`, `session_store = "<style>"`, or a `[tools.<style>]` key. An `mcp` or `session_store` value that neither the core nor a driver in this build implements stops the board at startup and refuses a launch, naming the styles this build does have. It is not treated as `none`. When the CLI itself is missing, the spawn stops and a dialog shows the same install hint as a missing tmux or git.

**Your config wins.** A field you left out is filled from the built-in defaults on every launch, and a tool missing from the file is added whole, so an older config picks up new capabilities. A field you do have is yours and stays: a `[tools.opencode]` block that already carries a `rules = [...]` array keeps that array even after a release ships better rules for OpenCode. That is what you want for a block you tuned, and it is the first thing to check when a tool the manager supports reads its status wrong.

**When a status looks wrong.** Delete the `rules` array from that tool's block (or the whole `[tools.<name>]` block) and relaunch: the block comes back on the current built-in rules. To see what the rules are being matched against, read the pane the way the poller does — `tmux capture-pane -p -t gi_<id>` — and compare it with the patterns in your block. A CLI that changed its output in a new version is worth [an issue](https://github.com/usestring/gate-inbox/issues/new/choose) with that pane text and the CLI's version, since the built-in rules then need updating for everyone.

State is stored next to the config in `state.db` (SQLite).

## The diagnostic log

Gate Inbox owns the terminal while it runs, so it never prints anything to the screen: what it
did is written to a rotating file instead. `gate-inbox --log-path` prints where that file is and
lists what is currently on disk.

The default is `<config dir>/logs/gate-inbox.log`, beside `state.db`, at `info`. Rotation is by
size: 8 MB per file, 5 rotated files kept, older ones gzipped, and a 48 MB cap on the directory as a
whole so it can never fill a disk. Writing is asynchronous — a slow or full disk costs log lines,
never a frame.

At `info` the log carries startup and resolved configuration, every key press with the branch it
dispatched to and the row the cursor was on, mode transitions, every tmux command with its socket,
its duration and its error, adoption decisions and every adopted row pruned. `debug` adds the
per-candidate adoption reasoning and the routine poll passes.

**Captured pane text is never written at `info`.** The panes this program reads are running coding
agents and routinely have live API keys and OAuth tokens on screen. Pane content reaches the log
only at `trace`, and credential shapes are scrubbed even there — as they are in every other record,
message and attribute alike.

Configure it under `[log]`, or override for a single run with `GATE_INBOX_LOG_LEVEL` and
`GATE_INBOX_LOG_FILE`, which both beat the config file:

```toml
[log]
level = "info"          # off, error, warn, info, debug, trace
file = ""               # default: <config dir>/logs/gate-inbox.log
max_size_mb = 8         # rotate once the active file reaches this
max_backups = 5         # rotated files kept, oldest deleted first
max_total_mb = 48       # hard cap on everything this log holds
no_compress = false     # rotated files are gzipped by default
```

`max_backups` left unset is derived from `max_total_mb / max_size_mb`, so the two cannot be
configured into contradicting each other.

## Right-to-left text

Hebrew and Arabic rows are painted as the cells they occupy, the same on every host. A terminal that runs its own bidirectional layout, iTerm2's right-to-left support or WezTerm's `bidi_enabled`, reorders those rows itself; turn that support off to read the frame in the columns Gate Inbox paints.
