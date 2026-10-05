<!-- Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE. -->

# Configuration

Config lives in your OS user config dir (`~/Library/Application Support/gate-inbox/config.toml` on macOS, `~/.config/gate-inbox/config.toml` on Linux, with `XDG_CONFIG_HOME` honored when set) and is created on first run with defaults for the three supported CLIs: Claude Code, Codex, and OpenCode v2. Any other CLI can be added as its own `[tools.<name>]` block (below); nothing about it is built in, so its block has to say everything the manager needs to know.

**Launch accounts.** A session that names no account runs on its CLI's own login. A build can carry an extension that chooses the account instead (`extension.AccountChooserProvider`); Settings then offers **launch accounts**, which switches between the CLI's own login and the extension's choice. A migration never takes an account from its caller: it runs on the extension's choice, or the CLI's own login when the build has none. With **new session agent** set to `auto`, `n` asks the same extension which CLI to start; Settings offers `auto` only when the extension can answer, and `n` opens the usual CLI picker whenever it does not.

Top-level: `poll_interval` (default `"2s"`) sets how often panes are polled for status, preview, and stats. `name_sweep_pace` (default `"3s"`) sets how long the name sweep waits between panes; every message it sends starts a turn in a live agent, so a larger board wants a longer gap. `stale_status_after` (default `"2h"`) is how long a session may read `working` or `starting` with its screen unchanged before its row is flagged `stale` and offered up by triage (see [Status](usage.md#status)). `editor` is the command Settings opens the snippets and restart-flags files in, arguments included (`editor = "code -n"`, `editor = "open -a 'Visual Studio Code'"`); it is run directly rather than through a shell, and quotes group an argument carrying a space. Left unset, Gate Inbox falls back to `$GATE_INBOX_EDITOR`, then a GUI editor on `PATH`, then `$VISUAL` / `$EDITOR`, and last a terminal editor on `PATH` (see [Opening the snippets file](usage.md#opening-the-snippets-file)). Nothing here has to be set for the file to open.

`[children]` controls what the board does with sessions an agent spawned. `auto_archive_after` (default `"30m"`) is how long a child whose pane has exited stays on the list when it never reported back. `finished_grace` (default `"10m"`) is how long a *finished* child stays once its spawner has taken the finish in -- the rest notice reached the spawner's prompt, or the spawner ran `read_session` or `wait_for_session` on it after it finished -- before the board archives it through the same teardown as `x`, so the archived view (quick actions) finds it and `u` restores it for the 7-day retention window. A child that is waiting, working, errored, dead without having finished, adopted, spawned with `keep`, or still has live children of its own is never archived by this rule, and neither is a Claude Code child with background work still running (see the usage guide), and archiving a parent takes its descendants with it. `keep_finished = true` turns the finished-child cleanup off board-wide.

<a id="claude-code-setup"></a>`[claude_code]` covers your own Claude Code config. `setup` (default on) has the board keep its hooks in `~/.claude/settings.json` and its MCP relay, a server named `gate-inbox`, in `~/.claude.json` (the files under `CLAUDE_CONFIG_DIR` when that is set), which is how a `claude` you started outside the board reports status and gets the board's tools once it is adopted (see [Status](usage.md#status)). There is no command to run: the board registers them when it starts and checks them every few minutes while it runs. `setup = false` makes the board remove them instead; the "claude code setup" setting does the same on one machine without editing this file. Named accounts need nothing more: they swap the token a session runs on, not the Claude Code config directory, so one registration covers them all.

```toml
[claude_code]
setup = false
```

<a id="codex-setup"></a>`[codex]` covers your own Codex config. `setup` (default on) has the board keep two hook entries, `UserPromptSubmit` and `Stop`, in Codex's `config.toml` (under `CODEX_HOME`, or `~/.codex`), on a machine with `codex` installed. That is how a `codex` you started outside the board reports status and hears the board's steering once it is adopted (see [Adopted panes](usage.md#adopted-codex)). The board registers them when it starts and checks them every few minutes, and touches nothing else in the file: comments, your own hooks and Codex's trust records stay byte for byte. Codex asks once to trust new hooks ("Hooks need review"; pick "Trust all and continue"), and the entries' text stays the same across Gate Inbox upgrades, so it does not ask again. `setup = false` makes the board remove them, and so does `gate-inbox codex-hooks uninstall`, which also keeps them out until `gate-inbox codex-hooks install`.

```toml
[codex]
setup = false
```

<a id="resource-hogs"></a>`[hogs]` sets when the board tells a session its processes are holding the machine (see [Resource hogs](usage.md#status)). It reads `/proc`, so it is Linux only (WSL2 included); elsewhere it is off. `enabled` (default on) switches it off with `false`. `sample_every` (default `"10s"`) is how often the trees are read, `reset_after` (default `"2m"`) is how long every rule of a kind must stay below before its episode ends -- a shorter dip keeps each rule's window open -- and `cooldown` (default `"30m"`) is the least time between two notices of the same tier to one session. Each tier of `[hogs.cpu]` and `[hogs.memory]` is a list of alternative rules; any one held for its `for` puts the session at that tier (`for` left out means the first sample that finds it). A CPU rule sets `percent`, summed over the tree, 100 being one full core. A memory rule sets `gib` (the tree's PSS), `growth_gib_per_min` (its growth over the last minute) and `available_below` (the host's `MemAvailable` as a percentage of RAM), and every one it sets must hold. A tier left out takes the built-in rules below; `stop = []` switches that tier off.

| Kind | Tier | Built-in rules (any one) |
| --- | --- | --- |
| cpu | `notice` | `{ percent = 100, for = "10m" }` |
| cpu | `warn` | `{ percent = 200, for = "5m" }`, `{ percent = 100, for = "30m" }` |
| cpu | `stop` | `{ percent = 400, for = "5m" }`, `{ percent = 200, for = "20m" }` |
| memory | `notice` | `{ gib = 8, for = "10m" }`, `{ gib = 4, available_below = 25 }` |
| memory | `warn` | `{ gib = 16, for = "5m" }`, `{ growth_gib_per_min = 1, for = "5m" }`, `{ gib = 4, available_below = 15 }` |
| memory | `stop` | `{ gib = 32 }`, `{ gib = 4, available_below = 10, for = "2m" }` |

```toml
[hogs]
cooldown = "1h"

[hogs.cpu]
notice = [{ percent = 150, for = "15m" }]
```

`[board]` holds the frame's layout. `sidebar` (default `"right"`) is the side the sessions list sits on, `"right"` or `"left"`; the session's pane takes the other side. Any other value stops the board at startup with an error naming the key. Settings (`s`) → **sidebar** can pick the other side on one machine, which outranks this file there; picking the file's side in Settings again goes back to following it (see [The board layout](usage.md#the-board-layout)).

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

**Status detection.** Optional per-tool fields refine it: `activity_cutoff` (regex locating the tool's input box, everything above it is turn content), `turn_end` (a turn-summary line marking the turn as over), `busy_line` (background work still pending after the turn: Claude Code's own wait line for background agents and dynamic workflows, plus the "still running" tail on the turn-end summary naming shells, monitors, MCP tasks and background tasks), `limit_line` (a usage or rate-limit banner; the session is `errored`), `chrome_line`, `blocked_line`, `trailing_note`, and `scrolled_line` (the affordance a tool draws while its own viewport is parked above the live bottom, such as Claude Code's "Jump to bottom (ctrl+End)"; the visible screen is history then, so status holds until the viewport comes back). `rename_command` is the slash command `r` types into the pane to have the agent name its own session (`/rename` for Claude Code, whose command directory this repo installs into, and for OpenCode, where the manager registers it per session through the generated `OPENCODE_CONFIG`); left unset, `r` asks for the same thing in prose, so a tool with nowhere to install a command is still asked rather than guessed at. `skip_rename_directive` leaves a launch's first prompt untouched — no directive prepended, none queued behind it — for a tool whose sessions are named from the outside instead (OpenCode sets it: naming comes from its own `session.title`, on demand with `/rename`, and silently through the steering the same generated config has its MCP server carry). `interrupt_keys` are what stop a running turn, for the operator's undo and for a message sent with `interrupt` (Claude Code: `Escape`); a tool without them refuses interrupting sends. `type_ahead` lets a queued message be typed into the session mid-turn, for a tool that keeps what it is handed and reads it once the running step ends (Claude Code sets it); a dialog or an undrawn input line still holds it. `status_source = "claude-hooks"` switches status to Claude Code hook events (see [Status](usage.md#status)). The generated config's `claude` and `opencode` blocks show all of them in use.

**Revive.** `resume_by_id_command` resumes one exact conversation, with `{id}` replaced by the session's captured agent id. That id comes either from launching under an id the manager mints (`session_id_flag`, e.g. `--session-id`) or from reading back an id the tool minted itself (`session_store = "codex" | "opencode"`, or the style of a tool driver an extension in your build supplies). `resume_picker_command` is what `v` runs when no id is available: the tool's own session picker, so you choose the conversation rather than resuming the directory's newest one blind (Claude Code: `claude --resume`; Codex: `codex resume`). `revive_command` is the last fallback, for a tool with neither, e.g. `opencode --continue`. Gate Inbox shell-quotes `{id}`, as it does for a fork, so write the placeholder bare: `codex resume {id}`.

**Forks.** `fork_command` creates a conversation from an existing session. Gate Inbox replaces and shell-quotes these placeholders:

- `{id}`: The source conversation ID.
- `{new_id}`: A new UUID that Gate Inbox records for exact revival.
- `{name}`: The new Gate Inbox session name.
- `{session_file}`: The file on disk holding the source conversation, for a CLI that forks by loading a file. Only a tool driver can find one, so it needs `session_store` to name a driver that supports it.

A `fork_command` references its source through `{id}` or `{session_file}`, so one of the two is required. Claude Code and Codex include default fork commands. OpenCode needs none: it forks through its own API and launches the copy with `resume_by_id_command`. A custom tool can omit `{new_id}` when its `session_store` captures the generated ID.

`fork_dialog_option` and `fork_dialog_keys` answer a dialog the fork's own resume raises before the conversation loads. Claude Code offers to resume a large conversation from a summary and preselects that option, so the quickest answer forks a summary rather than the responses the fork was made for; the `claude` block therefore ships `fork_dialog_option = "Resume full session as-is"` with `fork_dialog_keys = ["Down", "Enter"]`, and the manager picks that option itself. The option is matched against the pane as plain text, so nothing is ever typed at a pane that is not showing it, and both fields are needed for either to act. Left unset — every other tool — a fork that raises a dialog waits for you to answer it. The answer is armed for five minutes after the fork and dropped once sent.

**Prompts.** `prompt_flag` controls how the new-session form's optional prompt is embedded into the launch command. Tools that take the prompt as a positional argument (Claude Code: `claude 'the prompt'`) leave it empty; tools whose positional argument means something else declare the flag (OpenCode: `prompt_flag = "--prompt"`, since its positional argument is the project path). `prompt_mode = "send"` handles a persistent CLI that accepts no startup prompt: Gate Inbox waits until `activity_cutoff` finds its input box, then submits the prompt there (OpenCode uses this). `typed_prompt_prefixes` does the same for one prompt at a time: a CLI that reads a leading `@` as a file to attach or a leading `-` as an option lists those openings (`typed_prompt_prefixes = ["@", "-"]`), and a prompt that starts with one is typed in rather than passed as an argument. The prompt setting only affects a new launch; revive (`v`) uses the revive commands untouched.

**Extra directory.** An agent spawned into a directory outside its caller's tree has its pane opened in the caller's directory instead, to get past the CLI's first-run trust dialog, and is told to change into the requested one as its first step. A CLI that scopes its permissions to the directory it started in would still deny, or ask about, the first write it makes there. `add_dir_flag` names the CLI's flag for granting one more directory, and such a launch then carries it with the requested directory, last on the command line (Claude Code and Codex: `add_dir_flag = "--add-dir"`). A tool without it — OpenCode, which has no such flag — gets only the change-directory step, and no launch that opens where it was asked carries the flag.

**Undoing.** `interrupt_keys` is the sequence of exact tmux key names `ctrl+z` sends to stop the latest submission, such as `["C-c"]`. An omitted sequence sends `Escape`.

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
