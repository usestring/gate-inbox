<!-- Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE. -->

# Usage

```bash
gate-inbox
```

Sessions run inside tmux (`gi_*` namespace), so they survive the manager quitting. Inside a session, **Ctrl+Q** detaches back to the manager when your terminal and tmux leave it available; **Ctrl+\\** is an alternate under the same rule. **F3** opens its directory in your editor. In a full-screen attach, the session footer also shows an inner tmux prefix followed by `d` when configured. When nested inside another tmux, send the inner prefix shown in the footer, then press `d`. If both tmux servers use the same prefix, invoke the outer tmux's `send-prefix` binding; if the outer tmux otherwise captures the inner prefix, configure it to forward that key. `gate-inbox --version` prints the version.

Agent sessions live on tmux's own default server, so `tmux ls` lists them and `tmux attach -t gi_<id>` drives one by hand. Because that server is yours as much as the manager's, the manager never writes a server-global option there, never unbinds a key, and claims `Ctrl+Q`, `Ctrl+\`, `Ctrl+R` and `F3` only when nothing has bound them already — a key you have bound yourself keeps doing what you told it, and the footer's tmux prefix followed by `d` still detaches. Set `tmux_socket` in `config.toml` (or `GATE_INBOX_TMUX_SOCKET`) to put them back on a private server, reachable as `tmux -L <socket> attach -t gi_<id>`.

One more `gi_*` session shows up in `tmux ls` that holds no agent: `gi_poll-anchor`. The manager reads the whole board over a single tmux control-mode client per server, and a client has to be attached to some session — so it makes an idle one of its own rather than joining yours, which would put the manager in your session and let it move the window you are looking at. It runs nothing, and killing it costs nothing: the next poll puts it back. The manager removes it when it quits.

## Keys

Tell your agent to manage sessions and terminals in Gate Inbox; it can set them up and control them for you.

| Key | Action |
|-----|--------|
| `n` | New session: one question, which agent, in a box already holding the CLI you last spawned — then no form, no name, no prompt, in the group under the cursor. Type over the box to pick another, `←→` to cycle, `esc` to back out. **new session agent** in settings retires the question: on `last used` or `default tool`, `n` starts that CLI and never asks |
| `ctrl+n` | New session, asking first (tool, name, directory, optional starting prompt, group picker). The card opens on the tool: type to pick a CLI by name, or arrow through the matches |
| `T` | New terminal tab: a shell under the selected agent, or in the selected group |
| `o` | Open the selected row's directory in your editor |
| `f` | Fork the selected conversation into a named session in the same group and directory |
| `M` | Migrate the selected conversation to another CLI: a new session there reads the transcript and carries on |
| `g` | New group (name, parent, default path) |
| `enter` | Focus session in place (keys go to the agent, list stays) / fold group. An archived session has no live pane to focus, so the row offers attach and `u` instead |
| `A` | Attach session full screen (Settings can swap it with `enter`) |
| `l` | Focus the session you were on before this one; `l` again swaps back. The pair is held by session, so a poll, a fold or a filter reordering the board does not move it |
| `.` | Mark a finished session idle without entering it |
| `ctrl+q` / `ctrl+\` | Inside a session: back to the manager when the terminal and tmux leave the key available |
| tmux prefix, then `d` | Inside a full-screen attach: back to the manager when the prefix reaches the inner tmux |
| `F3` | Inside a session: open its directory in your editor |
| `→` | Step into the row: focus the session, or open the group |
| `←` | Step out: close the group, or — focused, with the caret at the start of the agent's prompt — back to the manager. This needs the tool's prompt marker (its `activity_cutoff`) on the caret's row, so a CLI without one keeps `←` entirely; anywhere else in the prompt it moves the caret as usual |
| `K` / `J` (or `shift+↑` / `shift+↓`) | Reorder session or group among its visible siblings |
| `m` | Move a session to a group, a terminal into a session, or a group under another group |
| `r` | Name a session: ask its agent to name itself (`/rename`, or the same request in prose). On an adopted pane, derive the name from its conversation. On a group, open the group card on it: name, parent and default path |
| `x` | End the selected session, or the whole subtree under a group: kills the pane, frees the RAM its agent held, and files the row in the archive under `t` |
| `X` | Archive every session in view. The confirmation carries a tick box (`space`) as well as the `y`, because one keystroke is the wrong price for every session on screen |
| `v` | Revive a dead session, or every dead session under a group. On a session that is still running it restarts the agent on the conversation it is already on |
| `V` | Revive every dead session in view |
| `O` | Take over the adopted panes: restart each idle one as a managed session on its own conversation now, and the busy ones as they go idle. Asks first |
| `R` | Restart the selected session on an empty context: same name, group, directory and tool |
| `u` | Restore a session or group out of the archive, resuming what it held. The act is `end`; the archive is where the row lands, the way a deleted file lands in a trash can |
| `U` | Undo the last archive: the same sessions out of the archive and running again. Offered after a confirmed end as well as a silent one |
| `space` | Quick prompt: answer the selected session, or spawn an agent in the selected group |
| `F` | Fold / unfold every group |
| `s` | Settings (default tool, new session agent, theme, terminal background, list density, layout, colour, status marks, ask before ending, sort, key hints, on leaving a session, after quick send, session keys, snippets, CLIs, report a bug, suggest a change, and the version row that updates in place) |
| `\|` | Resize the split: `←→` nudge the divider, `enter` commits, `esc` cancels |
| `\` | Hide / show the list beside the pane: the `board` layout under a key, and `alt+\` does it from inside a focused session. The layout you had comes back on the second press |
| `t` | Toggle archived view. A row there counts down its retention: seven days after it was archived the manager deletes it for good, with its hook files, and the countdown is on the row |
| `w` | Filter to sessions that need attention (`waiting`, stuck, `finished`, `errored`); press again to show all |
| `tab` | Enter the next session that needs you, wherever it is in the list; `shift+tab` walks back up |
| `alt+w` `alt+f` | Enter the next `waiting` / `finished` session |
| `alt+e` `alt+i` `alt+k` | Enter the next `errored` (or dead) / `idle` / `working` one |
| `G` | Gate: drain that queue one session at a time — the rail goes away, answering promotes the next, `alt+.` skips one, `ctrl+\` stops |
| `M` | Messages (updates, tips; `x` dismisses one for good). The welcome message points at Settings for a bug or an idea. |
| `e` | Hide / show empty groups |
| `/` | Search: session name, tool, group, status, what the pane is showing, and what the session has said or run |
| `H` | The key map for the current screen (`?` also works). It scrolls (`↑↓`/`jk`, `pgup`/`pgdn`, `g`/`G`) and `/` searches it down to one line. |
| `q` | Quit (sessions keep running) |

Navigation is keyboard-driven. The manager claims mouse reporting so the wheel stays inside the app and cannot scroll the TUI out of view: in a focused session it walks that pane's scrollback, where click-drag also selects pane text and copies it. In a focused agent that tracks the mouse, a click passes straight through to its own clickable UI while a drag still selects and copies; hold `alt` to pass a whole drag through instead, for the agent's own text selection or sliders. In the list the wheel does nothing, since moving the selection with it retargets every key that follows.

## Walking the queue

Answer a session, leave it, land in the next one that needs a person: `ctrl+q` does this, and until now it only did it inside triage, so someone who answered a session and pressed `ctrl+q` landed back on the board every time.

Settings (`s`) has an `on leaving a session` row for it — `list` (the default, today's behaviour) or `next`, which gives `ctrl+q` the same walk without a queue having to be armed with `i` first. Leaving a session mutes it, which is what makes the walk converge: answering a session does not clear its status until the poller sees the pane change, so without the mute the walk hands the same session straight back. `ctrl+\` is still the way out whatever the setting says, and the focused footer names whichever the key is about to do.

### The gate

Press `F2` to switch between **Typing** and **Menu**, shown at the top. The choice stays
on as the queue advances. Typing sends text to the agent. Menu uses plain keys:
`.` skip, `l` back, `q` exit, `x` end, `n` new, `y` copy ID, `o` editor.
`Home` jumps to the top of the pane's history; `End` returns to live output.
Numbers, arrows and `Enter` still answer the agent's menu. Switch to Typing for a written answer.
The gate starts in Typing; `alt+Home` and `alt+End` reach history there too.

`G` arms that whole drain in one key: the queue `i` builds, the hands-free handover, and the full
width, and it opens the session at the head of the queue rather than leaving you on a list. Inside
it, answering is the only gesture — a dialog answered with `1`-`9` or `enter` hands that session
over and promotes the next thing waiting on a person. `alt+.` skips the one in front of you, taking
it off the queue without the trip back to the list that `.` needs. `ctrl+\` stops.

The gate footer names the rest of the session's controls, the same set v1's gate view carried:
`ctrl+x` ends the session, `alt+n` starts a new one in its group and comes back to the queue,
`alt+y` copies the agent's session id, `alt+l` steps back to the one you just left, and `alt+o`
opens its directory in your editor. Your own snippets ride the same tier: each
`ctrl+alt+`*letter* answers the session in front of you, and its label sits beside the controls so
the chord is on screen while you drain. `alt+,` (or the chrome setting) hides the footer for the
rest of the drain.

However it ends — the key again, `ctrl+\`, or the queue running dry — the layout and the queue you
had before it come back, because a mode is not a preference. The `GATE` badge names the key back
out: with the rail away, nothing else is printing it.

## Quick prompt

Press `space` to dock a prompt bar at the bottom of the sidebar. The target follows the cursor while the bar is open (`↑↓` still navigate):

- On a **session** row, `enter` sends the typed text straight into the session's pane, so the agent gets it as a user message without you attaching. The bar clears and stays open, ready for the next answer; Settings (`s`) can make it close instead.
- On a **group** row, `enter` spawns a new agent in that group and submits the prompt at startup, using the group's default path. This is the shortest path to a fresh agent: `space`, type the task, `enter`, with no form and no name to invent. The spawn tool starts at the Settings default and `tab` (or `alt+m`) cycles it (claude ↔ opencode ↔ any configured tool); the footer shows the current pick. The agent starts working on the prompt immediately.

`ctrl+v` pastes an image from the system clipboard as an `[Image #1]` chip at the caret. The image is saved under `gate-inbox-pastes` in your temp directory, and on send each chip is swapped back for its path, so the paths reach the agent in the order and the places you pasted them. `backspace` next to a chip removes the whole chip, and an edit that swallows one releases its image. A clipboard holding text rather than an image pastes as text. Pasted images older than seven days are cleared at startup and once a day while the manager runs, so an agent can still open one from an earlier session while temp stays tidy.

`esc` closes the bar.

The new-session form's optional `prompt` field launches an agent the same way. It takes `ctrl+v` and its chips too, since a first task is often the screenshot that explains it: paste the design to match or the crash to read, and the agent opens the file on its first turn. Leaving the form without creating the session releases the images it was holding, the way closing the bar does. Tools whose CLI takes the prompt behind a flag declare it with `prompt_flag`, while a persistent CLI with no startup-prompt argument uses `prompt_mode = "send"` (see [Configuration](configuration.md)).

![answering a working Claude Code session from the prompt bar, without attaching](demo-space.gif)

## Which CLIs you get offered

Every configured tool is offered when you create a session, which is more than most people run. Settings (`s`) has a `CLIs` row: `enter` opens a checklist, `space` or `enter` unchecks the tool under the cursor, `esc` saves, and the ones left checked are what the `ctrl+n` form's `tool` picker and the quick prompt's `tab` cycle through. The last checked tool cannot be unchecked, since a picker with nothing in it could not create a session. It only narrows the pickers, so a session already on an unchecked tool keeps running and revives on that same tool. The last row, `request CLI support`, opens an issue for a CLI we do not ship rules for yet.

## Terminal tabs

`T` opens a shell tab: a session like any other (same list, same row keys, same `enter`, `x`, `v` and `R`) with your shell in the pane instead of an agent. On an agent, the new shell nests under that session, in that agent's group and directory. On a group, it lands in the group as an un-nested sibling, in the group's default path. On a nested shell it joins the same parent; on an un-nested shell it stays un-nested in that shell's group. Either way it opens in that shell's own directory, so a shell you have `cd`'d somewhere hands the next one the same place. A nested shell is named after the session it hangs under, `terminal-review-done` rather than `terminal-0ab5`, and the next one under that session counts up to `terminal-review-done-2`; a shell with no session over it keeps the generated name, and `r` renames any of them. Its status rests at idle throughout: turn tracking belongs to agents, and a shell has no turns.

The shell is the `[tools.terminal]` block in [config.toml](configuration.md). It ships with no command, which leaves the pane on `$SHELL`; set one to open a different shell. What marks it as a shell is `shell = true`, not its name, so a `[tools.terminal]` block you wrote yourself stays the agent CLI you meant it to be.

Shells live in the tree with the agents they belong to, marked with `❯` where an agent carries its status dot. `m` on a terminal moves it onto an agent (nests under that session) or onto a group (un-nests into that group). A group's dots and counts describe its agents, so only agent work shows as in progress.

**The keys that write into a pane refuse a shell.** `space` pastes its text and presses Enter, so on a shell a sentence meant for an agent would run as a command. It says the row is a shell and sends nothing; enter the session (`↵`) to type there, where what you type is plainly a command. `f` says the same, since a shell has no conversation to fork.

A shell left on its empty command carries no session id, so `gate-inbox rename` run inside one cannot find its session. Rename it from the list with `r`. Give the block a command and the pane gets an id like any other session.

## Opening the editor

`o` opens the row under the cursor in your editor: a session's live working directory (wherever its shell or agent has moved to, not only where it started), the directory it was created in when the live one cannot be read, or a group's default path. It works on a [terminal tab](#terminal-tabs) too — the shell you ran the build in is usually sitting in the directory you want open.

Gate Inbox takes the first of these it finds: `editor` in [config.toml](configuration.md), `$GATE_INBOX_EDITOR`, a GUI editor on `PATH` (`code`, `cursor`, `windsurf`, `zed`, `subl`, `idea`), then `$VISUAL` or `$EDITOR`, and last a terminal editor on `PATH` (`nvim`, `vim`, `nano`, `vi`). The environment comes before the terminal editors because it usually names the editor you set for git commit messages rather than the one a project should open in; the terminal editors come last because they are a fallback rather than a choice you made, and they are there so that nothing which opens a path can dead-end on a machine that has never been configured.

The line is run directly, never through a shell, so nothing in it is expanded and an `.envrc` that sets `EDITOR` cannot smuggle a command in behind it. Arguments are allowed, and quotes group one that carries a space: `editor = "code -n"`, `editor = "open -a 'Visual Studio Code'"`.

Inside a session, attached or focused, `F3` opens that session's directory the same way. It costs an attach its client, so the manager steps back into the session once a windowed editor is running, or once one that draws in the terminal exits. An editor that fails to start keeps the manager on screen, where you can read why.

Like `ctrl+q`, the manager keeps `F3` for itself inside a session, so a program running in there stops seeing it. Every `ctrl` combination reaches the program instead, `ctrl+o` included: Claude Code shows more lines with it, and in a [terminal tab](#terminal-tabs) `nano` writes the file out.

A known windowed editor (the six above, plus `open` and `xdg-open`) starts detached and the manager stays on screen, with the status line naming what opened. Everything else takes the terminal over the way an attach does and hands it back on exit — that way round because a terminal editor started detached would have nowhere to draw, while a windowed one launched this way only costs a repaint.

## Sessions in their own checkout

Gate Inbox does not create git worktrees. A session that should edit a checkout of its own is given one: make the worktree with the repository's own tooling first, then spawn the session with that path as its directory (`spawn --directory`, `directory` on the MCP `create_session` tool, or the path field of the New Session form). The board treats it as any other directory: nothing follows a rename, and deleting the session leaves the checkout where it is.

## Killing and reviving sessions

`x` ends a session that is holding RAM you want back, and on a group row it ends every live session under it; `X` ends every live session in view. Each asks to confirm first, and what it ends is the tmux session, not the record: the row stays in the tree, marked `dead`, with its name, group, and conversation id intact.

![ending every session under a group for the RAM, then reviving the whole subtree on its own conversations](demo-revive.gif)

`v` relaunches a dead session under its old id, keeping its name, group, and history. When the manager holds that session's own conversation id, revive resumes **that exact conversation** through the tool's `resume_by_id_command`: `claude --resume {id}`, `codex resume {id}`, `opencode --session {id}`.

The id arrives one of two ways: tools with a `session_id_flag` launch under an id the manager mints, and tools that mint their own are read back by a `session_store` capturer (`codex`, `opencode`). Without an id, revive falls back to `revive_command` (`claude --continue`), which resumes the working directory's most recent conversation, and the manager says so in the status line, since sessions sharing a directory would otherwise land on the wrong one. On a group row `v` revives every dead session under it, and `V` revives every dead session in view; both revive what they can and name the first failure rather than stopping.

A start that finds panes gone offers back the sessions that stopped without you ending them, before you have to notice the dead rows: `enter` restores every one of them, `c` opens a picker to take part of it, and `esc` leaves them alone. Whichever you answer, those rows are settled: the next start does not ask about them again, and `v` and `V` are still there for the ones you left. A session you revive by hand and lose again is a new loss, and that one is offered.

Only a session that died is offered, never one you ended. The board tells them apart from what it records as it happens:

- **Ended by you, never offered:** a kill from the board, the CLI (`gate-inbox kill`), the MCP tool or an extension; an archive; `gate-inbox park` (which `unpark` brings back); an agent you quit yourself with `/exit`, which leaves exit status 0 (ctrl+c's 130 counts too); and a pane you closed in tmux (`kill-pane`, `kill-window`) while its tmux server stayed up.
- **Died, offered:** the tmux server it ran on is gone or was restarted after it launched (a reboot, tmux itself ending), or the agent crashed, which leaves a non-zero exit status or a signal.
- **Unclear, offered and labelled:** nothing settles it either way, such as a stopped job's status. The row says what is known and when it was last seen.

The exit status comes from the pane's launch script, which records the agent's status in `hooks/<id>.exit` under the config directory before it drops to the shell.

## Archiving without being asked

Archiving ends the agent, so `x` asks first. It asks every time because until now nothing could take the answer back — a dialog is the price of an act with no undo. Settings (`s`) has an `ask before archiving` row that drops the dialog for `x` on a single session, and `U` is what pays for it: the last archive out of the archive and running again, offered whether the archive was silent or confirmed.

The wide gestures keep their dialog whatever the setting says. `x` on a group takes its whole subtree and `X` takes every row on screen, and neither is a keystroke aimed at something you picked out — which is what makes a silent answer safe. `X` keeps its tick for the same reason it has one.

## Restarting a session on the conversation it is on

`v` on a running session ends the agent and brings it straight back on the same conversation, so the history is there and the process is new. That is the difference that matters: a session reads its skills, its settings and its MCP servers when the CLI starts, so an agent that has been up since before any of those changed is answering with the old ones and has no way to reload them from inside. `R` below buys the same fresh process by throwing the conversation away, which was never the answer to "the tools are stale". It asks first, and `n` leaves the agent running.

A running session with a dead child under it keeps the older meaning: that press is about the child, and `v` revives what is dead beneath it.

## Restarting a session on an empty context

`R` keeps the row and drops the context: same name, group, tool, and working directory, launched on a conversation the agent has never seen. It is what you want when a session has piled up context you are done with, where reviving it would spend the budget re-reading history or land straight in a compact.

It asks to confirm first, and it works on a live session too: the running agent ends, then the fresh one launches. The conversation it was on is retired rather than resumed: the manager mints a new id for tools that take one (`session_id_flag`) and captures the new one for tools that mint their own (`session_store`). The retired conversation is left on disk untouched, and the row stops pointing at it, so a later `v` resumes the conversation the restart started rather than the context it dropped. The row changes hands only once the new agent is up, so a launch that cannot start (a tool gone from `PATH`, a directory that moved) leaves the session on the conversation it had, still there for `v`.

## Taking over adopted panes

An adopted pane is somebody else's window with an agent in it, and the board refuses to do to it what it does to its own sessions. `O` asks to take every adopted pane over: an idle one is ended in its own window and relaunched under the same row as a `gi_*` session on the conversation it was holding, read off the agent's process; a busy one is left alone and taken on the first poll pass that finds it idle. The dialog says how many go now and how many follow. The same offer is raised once when the manager starts on a board that holds adopted panes.

A pane whose conversation cannot be read is left where it is and the status line says so, because relaunching a tool that resumes by id on its continue command would pick the directory's most recent conversation instead.

## Forking sessions

1. Select a session and press `f`.
2. Enter a name.
3. Press `enter`.

The fork uses the source session's tool, group, working directory, and conversation history.

Claude Code offers to resume a large conversation from a summary instead, with the summary preselected. A fork answered that way keeps a summary rather than the responses it was made for, so Gate Inbox picks "Resume full session as-is" for you when that dialog appears in a fork's pane. Other tools, and a `claude` block that clears `fork_dialog_option`, leave the dialog for you.

Claude Code and Codex include default fork commands, and OpenCode forks without one (below). A custom tool needs a `fork_command` in its configuration. The source session must have a captured conversation ID.

OpenCode's TUI has no fork flag, so Gate Inbox forks through OpenCode's own API instead: it copies the whole conversation into a new session (`session.fork` through its newest message) before the pane exists, and launches that copy with `resume_by_id_command`. The fork starts on its own conversation id, so nothing has to be captured and a revive resumes the copy.

## Migrating a session to another CLI

1. Select a session and press `M`.
2. `tab` picks the CLI it moves to; the proposed name follows the pick until you type one.
3. Press `enter`.

No agent CLI can resume another's conversation, so a migration is a new session on the target CLI, in the source's group and working directory, whose first prompt points at the source's full transcript on disk (Claude Code's `~/.claude/projects/…/<id>.jsonl`, Codex's rollout file, or `opencode session export <id>` for OpenCode) and says how that file is laid out, to read it from the end back, and to carry on exactly where it left off without redoing finished steps or re-asking what the transcript already answers. When the source is still running, the prompt also names it so the new agent can ask it through `send_session` and `read_session` rather than guess.

The source and new session are linked, marked with `⇄`, and kept adjacent in the normal list. A shared accent-colored `╭─` / `├─` / `╰─` tree connector shows which panes belong together, without adding a group row to navigate through. Reordering either with `J` / `K` or moving it to another group moves both; the link survives a restart. Migrating again extends the same chain. The Initial Prompt panel shows the original task when recorded, while the new agent receives the transcript handoff instructions; its saved task prompt remains available after the source is removed. Search and triage still filter and rank individual sessions by their own state; a connector only joins adjacent visible partners.

The source keeps its current status. Archive it with `x` once the new session has taken over, or keep both. The source needs a captured conversation id and a transcript on disk, so a session that has not taken a turn cannot be moved yet, and only claude, codex and opencode conversations can be located.

Agents can do the same: `gate-inbox migrate <session-id> --tool <cli> [--name <name>]` from a shell, or the `migrate_session` MCP tool, including on their own session id to move themselves -- after a usage limit on the CLI they run, say.

## Self-naming sessions

Sessions spawned without a custom name (every quick spawn, and the form with the name left blank) get a placeholder like `claude-a1b2`, and their first prompt opens by asking the agent to run `gate-inbox rename "<name>"` once with a short name for the broad feature of the session (not a single subtask). The directive also tells the agent not to rename again unless you ask. When the first prompt cannot carry the directive (a `/slash` command, or no prompt at all), the manager sends it as its own message once the tool's input box appears in the pane. The subcommand drops the name into a per-session file; the manager picks it up on the next poll and updates the sidebar row and the tmux status bar. This works with any tool, since it only needs the agent to read its prompt and run one shell command.

OpenCode sessions skip that directive entirely (`skip_rename_directive`): asking an agent to name itself as its first act, when it has the least context, is what produced the vague names, and the directive prepended to `--prompt` is also the first message its own title model reads. An opencode launch goes out with the prompt exactly as typed, and typed is the word: opencode's `--prompt` fills the composer without submitting it, so the opening prompt is not passed on the command line at all (`prompt_mode = "send"`) but typed in once the composer is up, through the same claim-once pending-input path a message from another session takes, and submitted by the Enter that follows the manager's own paste. Nothing the operator types is ever submitted for them: the only Enters the manager presses are the ones right after its own paste. Naming still happens three ways: the manager reads the title opencode writes for the conversation itself (`session.title`) and puts it on the row, ignoring its `New session - …` placeholder until the real title lands; the generated `OPENCODE_CONFIG` every managed session launches with carries silent naming instructions in system context (name once, after the task is understood, through the `rename` MCP tool) plus the `/rename` command itself; and `r` types that command into the pane on demand. Every managed opencode session launches with `--standalone`: opencode otherwise runs every session against one background service per user, which reads its own environment rather than the launching client's, so the generated config would never reach the agent and the `rename` tool would carry the id of whichever session first started the service. A session launched on a chosen model gets a config of its own (`mcp-opencode-<id>.json`) carrying that model, since the opencode TUI has no model flag.

The directive spells the command as `"$GATE_INBOX_BIN" rename "<name>"`, not as the bare name. The manager runs from its own checkout — `go run .`, or a build sitting beside it — so it is on nobody's `PATH`, and every session is launched with `GATE_INBOX_BIN` holding the path to the manager that started it. Use the same variable for any of the subcommands below when you type one yourself; the bare name works only if you have also installed the binary.

A session started with `n` is named differently, because it is started with nothing to name it after: no prompt has been typed and the agent has not run a turn. It takes the working directory's own name, counting up (`sample-repo`, `sample-repo-2`, `sample-repo-3`) so a burst of them stays apart on the rail, and no rename directive is sent — that would cost the agent a turn before you had asked it for anything. Instead the manager reads the title the CLI writes for the conversation itself (Claude Code's `aiTitle`, opencode's `session.title`) and puts it on the row, within about half a minute of the conversation having one. Rename it yourself with `r` at any point and the row stops taking titles.

Sessions you name yourself keep that name: the first prompt only notes that `gate-inbox rename` is available later if you ask, and does not instruct the agent to rename now. You can still ask an agent to rename its session later, or run `"$GATE_INBOX_BIN" rename "<name>"` yourself from a shell inside the session.

## MCP: how agents discover these commands

Every session of an MCP-capable tool carries the Gate Inbox MCP server on spawn and revive, so its agent sees the whole workspace as native tools with descriptions telling it when to call each: its own session, the other agent sessions running beside it, the groups they are filed under, and the managed terminals. No per-project setup. The server lives in the same binary (`gate-inbox mcp`, stdio) and identifies the calling session through its environment.

That server is started once by its CLI and then lives as long as the conversation, while the board is rebuilt and restarted under it many times, so a weeks-old session keeps offering the tools of whatever build started it. Every tool says so in its reply once the board is running a newer binary. `create_session` does more than say so: a build that predates nesting would file the spawn with no parent, which cannot be repaired afterwards -- nothing records who asked for which session -- so a stale server runs the spawn through the installed manager under the config directory instead of in its own process, as the calling session, and takes the row back. If that copy is missing or fails, the spawn still happens in process rather than not at all.

| Tool | Action |
|------|--------|
| `rename` | Rename the calling session |
| `list_sessions` | List agent sessions with their ids, CLIs, groups, directories and statuses, narrowed by `parent` (`"me"` for the caller's own children), `status`, `include_archived` and `limit`; archived rows are left out unless asked for |
| `list_models` | List the model names a CLI accepts, so `create_session` can be given one instead of guessing |
| `create_session` | Start another agent CLI on a named task, as this session's child |
| `place_session` | File a session under this one, or release one of its own children back to the top level |
| `read_session` | Read what another agent's screen currently shows |
| `send_session` | Queue a message for another agent, delivered once it can read it |
| `send_children` | Queue one message for every session this one spawned, when the whole fan-out needs to hear it |
| `answer_session` | Answer a question one of your own spawned sessions has stopped on |
| `message_status` | Check whether a message you sent, or one sent to a session you spawned, is queued, held, delivered, dropped or answered |
| `wait_for_session` | Park until another session stops working, instead of polling it |
| `revive_session` | Bring a dead session back, resuming the conversation it held |
| `kill_session` | Stop a running agent, keeping its row and last screen |
| `archive_session` | File a finished session out of the active list, or restore it. An archived row is deleted for good seven days later |
| `task` | The shared work list in one tool: `action` is `list`, `create`, `claim`, `finish`, `release` or `delete` |
| `reserve_files` | Declare the files this session is editing, and see who else claims them |
| `release_files` | Give those claims back |
| `list_reservations` | See what every session is editing right now |
| `list_groups` | List groups with their default directories and session counts |
| `delete_group` | remove a group whose work is done; sessions still filed there move to the root rather than stopping |
| `create_group` | Add a group heading, nested with a slash path, for work the user asked to file separately; never for a fan-out |
| `list_terminals` | List active managed terminals and their current directories |
| `create_terminal` | Open a terminal under the calling session, or beside it when that session is itself a terminal, unless `nest` is false |
| `send_terminal` | Submit a command or send exact keys to a running terminal |
| `read_terminal` | Read the plain-text content currently visible in a terminal |
| `close_terminal` | Close a finished terminal nested under the caller: kill the pane and delete the row |

### Spawning and steering other agents

`list_models` answers what a CLI's `model` argument accepts before a spawn asks for one: the CLI's own listing command where the tool config has a `models_command` (opencode prints several hundred provider/model pairs, so the answer is capped and takes a `filter`), otherwise the names written into the tool's `models`, which are marked as possibly lagging the CLI. A tool with no `model_flag` (and no generated config to carry the model, as opencode has) says so here rather than at spawn time, where the same fact arrives as a refused launch. `migrate_session` moves a session's conversation to another CLI the way `M` does. `create_session` gives an agent the spawn the `ctrl+n` form gives a human: a name, a CLI, a group, a working directory and a first prompt. A session created this way is a normal row in the list, and the manager picks it up on its next poll, so it attaches, revives and forks like any other.

Each field falls back the way the form does. The CLI defaults to the one the calling agent runs, the group and directory default to the caller's, an explicit group uses that group's nearest inherited default path, and an explicit directory wins over both. A name is the agent's to choose and should describe the work; leaving it empty generates a placeholder and asks the new session to rename itself, exactly as a promptless spawn from the form does. Several agents working in one project each get a checkout of their own by being spawned into one: make it with the repository's tooling and pass it as the directory.

The text an agent hands another agent does not have to travel inside the tool call. A spawn brief, a message or a task body runs to pages, and an agent that has already written it to disk would otherwise paste the whole thing into the call a second time. Each text-bearing argument has a file twin the server reads instead: `prompt_file` on `create_session`, `message_file` on `send_session` and `send_children`, and `body_file` on `task` create. Exactly one of the pair is taken, the path must be absolute (a leading `~` is the home directory), an empty or missing file is refused, and trailing newlines are dropped so a file ending in one does not submit an empty turn after the text. The limits are unchanged: a `send_session` message read from a file is still capped at 8000 bytes, because it is typed into a prompt either way.

A spawn is the caller's child unless the caller says otherwise, and the parent is what everything downstream keys on: the list draws the fan-out as a tree, triage folds a child away while its parent is on it, a child's question is relayed to its parent's inbox and answered from there, its rest and finish are reported to the parent, and `send_children` reaches it. `nest: false` detaches the new session into a top-level row that reports to nobody, which is the right thing only for work that is not the caller's — a standalone session the user asked for in another group — and it is the only way into a group other than the caller's, since a nested session lives in its parent's group. The tool text says so, and it no longer tells an agent to group related spawns: a parent that read that as `create_group` plus `nest: false` spawned seven children nobody was routing to and then had to place each by hand.

Every session the tools hand back now carries `parent_id`, so an agent can see where its own spawn landed rather than infer it from the order of a list. That matters because a spawn can land flat: a session's MCP server is the build it started on, and one older than the nesting path files every child as a sibling of the session that asked for it — the call succeeds, the row comes back looking ordinary, and nothing says the fan-out is not a fan-out. `place_session` is the repair: it files a session under the caller, or with `release: true` takes one of the caller's own children back to the top level. A session may claim a row nobody owns and let go of one it owns; another session's child stays that session's, and rearranging somebody else's fan-out is the board's job. The same from a shell: `gate-inbox place <session-id> [--release]`.

`read_session` returns the target's current screen, and its last captured screen once the session has stopped. `kill_session` ends the process and leaves the row dead with its last screen, `revive_session` brings it back on the conversation it held, and `archive_session` files a finished row away or restores it, and a row left in the archive is deleted for good seven days after it was filed.

### A parent answers its own children

A session that spawns another owns what it spawned, and the moment that matters is the child stopping on a question: until somebody answers, the work the parent delegated is not happening. When a child goes waiting on an `AskUserQuestion` its parent is sent a message naming the child, the question and the options as the pane shows them, and told to answer with `answer_session`. It arrives through the ordinary inbox, so it is delivered when the parent is at rest like anything else.

`answer_session` is the reply path a message cannot be: `send_session` holds text until the recipient is at rest, and a session standing on a dialog never is, so the answer goes in as keystrokes — the arrows and Enter that land on the option named, or the words typed where the answer is nobody's option. Only the session that spawned it may answer it; a stranger's row is refused, naming who does own it. A permission prompt and a multi-select stay a person's to answer, and the question keeps standing on the board either way, so nothing here takes the operator out of the loop — it stops making them the first resort.

The same from a shell: `gate-inbox answer <session-id> "<answer>"`.

### Messages between agents

`send_session` queues a message rather than typing it immediately. Several agent CLIs keep their input line drawn underneath an approval dialog, so a message written at that moment would answer the dialog instead of being read. The manager holds it and types it in on the first poll where the target can read it: its input region is drawn, its status is not mid-turn (or it is, and its tool sets `type_ahead`), its own rules report no dialog on screen, nobody has anything written in its composer, and nobody has typed into it for three seconds. The draft check reads the pane and the caret together, fresh, and covers the whole composer, so a line that wrapped or took a newline holds as much as one on the prompt row; the quiet window covers what the screen cannot, a keystroke that has not been echoed yet, and is read from the manager's own forwarding for a pane in the focus view and from tmux's session activity for a terminal attached to it. `message_status` names either hold. Claude Code sets `type_ahead`: it queues what is typed into it mid-turn and reads it after the running tool call, so a correction to a busy session lands between its steps rather than after the turn it was meant to redirect. A send with `interrupt` (`--interrupt` on `gate-inbox send`) stops the recipient's running turn first with its tool's `interrupt_keys` (Escape for Claude Code), then types the message in once the turn has stopped, so it is the next turn rather than something read after the step in hand. The keys go once per message, since a second Escape opens Claude Code's rewind menu, and never while a dialog is showing: the pane is read again right before they are sent, and a dialog in either read holds the message like any other. A tool with no `interrupt_keys` refuses the send outright. `message_status` shows which mode a message was sent in. A send only ever queues, and its result says so; `message_status` says `delivered` once the text is in the recipient's prompt. Delivery is at most once, and a message the manager cannot prove reached the pane is retired as `dropped` rather than repeated, so its sender knows to send it again.

The gate is the recipient tool's own rules, because text typed onto a dialog picks an option: while one is on screen the queue waits, and `message_status` reports those messages as `held`, naming the session to go and answer. A question the agent left at a resting prompt trips no rule, and a message goes in there as an ordinary turn would, labelled as coming from another agent. `held` also covers a recipient the manager will never type into as things stand, a session archived or stopped since the message was queued, and says which it is.

`send_children` queues one message for every session the caller spawned, for a correction about the work rather than about one worker: the branch moved, the endpoint changed, stop using that field. Sending those by hand means the last child is told long after the first, and by then the earlier ones have acted on the instruction being corrected. It is opt-in and never the default — reaching for the whole subtree by accident interrupts eight agents to tell them something about the ninth, so `send_session` stays the way to reach one agent. Each child goes through the same gate as a single send, and one that cannot take the message (dead, archived, a terminal, holding a question for the operator, or a full queue) is reported with its reason rather than failing the call. From a shell: `gate-inbox send-children "<message>"`.

The message arrives labelled as coming from another session rather than from the user, with the sender's name and the id to answer on. The sender's own text sits inside a fence the manager mints at delivery, so a message written to imitate that label reads as what it is: the sender wrote it before the fence existed and cannot reproduce it. A receiving agent treats it as it would any untrusted input: it cannot approve a permission prompt, and it cannot change that session's configuration. Queue caps, a per-sender rate limit, a size cap on one message and a whitespace-insensitive fingerprint keep two agents from talking each other into a loop. `message_status` reports whether a message is queued, held, delivered, dropped or answered; answering a session acknowledges everything it sent.

Delivery needs the manager running, since its poller is what types the message in. A message queued while Gate Inbox is closed waits until it opens again, and `send_session` says so in its result rather than implying the message landed. A target that could never be reached is refused outright rather than queued: a session that is not running, an archived one, which the poller skips, and a tool declaring no `activity_cutoff`, which leaves nothing to read readiness from.

### Waiting and the shared task list

`wait_for_session` parks a single tool call until a session reaches one of the states that mean it stopped working, so an agent that spawned work does not read screens in a loop while it waits. A timeout returns the session's current state with `reached` false, because a timeout is an answer rather than a failure; `outcome` separates that from the session dying before it ever reached one of the awaited states. It is an ordinary tool call, which is what makes it work with every MCP client.

The task list is the manager's shared to-do list, visible to every session, all of it behind the one `task` tool. `create` puts work on it, `claim` takes a piece (by id, or the oldest one nothing is blocking), and `finish` marks it done, which unblocks every task that depended on it. A claim is a single atomic write, so two agents racing for the same task cannot both win: the loser is told who holds it. A session that is deleted hands its claims back to the list rather than parking them forever.

### File reservations

A checkout per session stops two agents overwriting one checkout, and that is the right answer whenever the work divides cleanly. It does not help when sessions deliberately share a checkout, and it defers the other kind of collision to merge time: two agents making incompatible decisions about the same interface find out only when the branches meet.

`reserve_files` declares the paths a session is about to edit. Overlap with a lease another session holds comes back as conflicts, naming the holder and what they said they were doing, so the two can settle it through `send_session` before either commits. The lease is advisory throughout: nothing is blocked, and an agent may edit anyway. It expires on its own, so a session that dies holding one never keeps the repo to itself, and `release_files` hands it back as soon as the edits land. An exclusive lease conflicts with any other lease on the same paths; two shared leases sit side by side. Matching compares a pattern against a literal path in both directions, so two patterns that each contain wildcards are only compared exactly, which is another reason these are a conversation starter rather than a lock.

### Terminals

`create_terminal` nests under the calling session unless `nest` is false, and a call from a terminal opens the new shell beside it: under the same agent, or un-nested in the same group when that terminal is itself un-nested. It defaults to the calling agent's group and live pane directory. A group other than the caller's needs `nest: false`, since a nested terminal lives in its parent's group; that group then supplies its nearest inherited default path, and an explicit directory wins over both. `close_terminal` kills the pane and removes the row once the job is finished, and it reaches only the terminals nested under the calling session: a shell someone else opened, or one deliberately left un-nested, is the user's to close. `send_terminal` accepts exactly one of a command, which is pasted and submitted with Enter, or a sequence of tmux key names such as `C-c`, `Up`, and `Enter`. `read_terminal` returns the current screen rather than unlimited scrollback.

The server's MCP initialization instructions teach agents to use these tools without waiting for an explicit request: list sessions and delegate a parallel workstream to a named `create_session` before running it in series, and open a terminal for human-visible work such as SSH, when the user should be able to watch it, attach, or take over. They list and reuse a relevant running terminal first; `create_terminal` nests under the caller unless `nest` is false; they send the command and read its screen while the job runs; and they call `close_terminal` when that job ends, unless the terminal is being left for the user. One-shot local commands stay in the agent's normal tools. The same guidance is repeated in the individual tool descriptions for clients that expose tools but not server instructions.

Every one of these tools acts on the user's machine. Agents should treat `send_terminal` with the same care as typing into an attached shell, and treat `create_session` and `kill_session` as what they are: starting a real agent process that spends tokens, and interrupting one that may be mid-task. Inspect the target returned by `list_sessions` or `list_terminals` first, and read the result before continuing.

Registration is per tool. Claude gets a generated `--mcp-config` file. Codex gets `-c mcp_servers...` overrides. OpenCode gets an `OPENCODE_CONFIG` merge file. A custom tool registers nothing unless its `mcp` names one of those three styles or a tool driver an extension in this build supplies; a style that is neither is refused rather than launched without the server. A spawn whose CLI is not on PATH is refused the same way, with the vendor's portable installer for a built-in agent, or the package manager on this machine for anything else.

A tool without an MCP client reaches the same workspace through the subcommands: `gate-inbox --help` lists them, from `sessions`, `spawn`, `send` and `wait` to the shared task list, file reservations and terminals.

A custom tool opts in with `mcp = "<style>"` in its config section. Set `mcp = "none"` to disable registration.

## Sorting the board

The board is in the order the move keys wrote it — `manual`, and the default, because a board somebody arranged stays arranged. Settings (`s`) has a `sort` row with two other orders: `activity` puts the most recently active session first, and `name` sorts alphabetically. Both sort **within each group**, so the structure you built stays.

Under `activity` the manager also opens on the freshest session rather than on whatever the manual order happened to put first.

Triage and search are unaffected — each already answers with its own order (who needs a person, and how well a row matched). While a non-manual sort is on, `K`/`J` say why they do nothing instead of writing an order you would not see until you set the sort back to `manual`.

## Groups

![folding the tree, creating a nested group, reordering, and archiving one](demo-groups.gif)

Groups are paths (`backend/api/auth`) forming a tree of unlimited depth. Sessions can live at any node, including the root. Create subgroups inline with `g`, reorder both groups and sessions with `K` / `J` (or `shift+↑↓`; the order persists), fold a subtree with `enter` on its row, fold or unfold the whole tree with `F`, hide or restore empty groups visually with `e`, and edit a group with `r`, which reopens the card that created it — name, parent and default path, so renaming and re-parenting are the same save. On a session, `r` renames it and `tab` cycles the tool (status rules and revive follow the new tool; useful when you quit one agent in the pane and start another).

## Status

Each session's tmux pane is polled (default every 2s) to derive a status:

| Mark | Status | Meaning |
|------|--------|---------|
| `◐` | `working` | The agent is busy on a turn |
| `◆` | `waiting` | Blocked on you: a dialog, a permission ask, or a plain-text question |
| `●` | `finished` | Turn ended — an alert that clears to `idle` once you enter the session, or on `.` |
| `✕` | `errored` | The tool reported an error |
| `○` | `idle` | Nothing running |
| `✕` | `dead` | The tmux session is gone |
| `◌` | `starting` | The pane is still launching |

Every row carries its mark, and each state has its own color from the active theme, so a glance down the rail tells you who needs you. The key map (`H`) lists the marks under "the mark on a session row".

A session stuck on the wrong mark is usually a rules question: the `[tools.<name>]` block in your own config is what the poller matches, and it keeps the rules it already has when a release ships better ones. [Configuration](configuration.md) has the two-line reset and how to read the pane the poller reads.

`w` narrows the list to sessions that need attention (`waiting`, stuck, `finished`, `errored`). Press again to show every status. An `ATTENTION` badge sits over the list with the key that clears it, and the session counts follow the filter; folds open so matches are not hidden. The archived view (`t`) and hidden empty groups (`e`) label themselves the same way. The badges take whatever room the rail has: padded away from the entries on a tall terminal, tight against them on a short one, and yielding to the entries once the list is down to its last rows.

`/` filters the list to the rows a query appears on. A query matches a row's own name, tool, group, and status; the text its pane is showing; and, from three characters up, the session's transcript — every prompt, reply, command, tool result and edited file since the session began, read from Claude Code's JSONL, Codex's rollout, or opencode's database, whichever the row's tool keeps — so the session that mentioned a hostname three hours ago is findable by that hostname. `*` stands for any run of characters (`db-*-07`), in the transcript and on the rows alike. The rows come best first: those named for the query, then those showing it on screen, then those that said it, ordered by how often, how recently, and who said it — a session still talking about it outranks one that mentioned it once, a mention in the newest turns outranks one hours back, and a term you typed outranks the same term the agent said, which outranks it in a command, which outranks it in a tool's output; text the harness injected into your turns counts least. A row the query is on only in the pane or only in the transcript says so with a `≡pane` or `≡hist` badge (`≡hist·12` when it said it twelve times), since the query is nowhere the row itself prints. Transcript hits arrive a beat after the keystroke and never make the list flicker: the metadata and pane matches show at once, the history answer joins them. The index is the sessions' text held in memory (plus a quarter as much again in per-block trigram filters, so a term that is not there costs a few bit tests rather than a scan), built in the background from the transcripts of the sessions on the board and nothing else; a fleet of long sessions catches up within a few seconds of the manager starting, a row that leaves the board leaves the index, and every byte of prose, command and tool output is kept up to 64MB per session and 512MB in all, past which the largest session gives up its oldest turns first. `GATE_INBOX_HISTORY_SEARCH=off` turns the transcript half off.

![the session tree, with a waiting agent's permission prompt in the preview](screenshot-sessions.png)

Each row carries its status and tool inline, and a folded group keeps a count per status so a collapsed subtree still tells you whether anything needs you. Selecting a session shows the tail of its pane on the right, which is how a `waiting` agent's actual question reaches you without attaching. A session with no window left, archived or dead, shows the snapshot taken when it still had one, and an archived one also shows how long it has before the retention sweep deletes it.

Detection matches per-tool regex rules against the visible pane, analyzes the newest turn to tell `finished` from `waiting`, and treats streaming output (content changing between polls) as `working`. A turn that ends without any turn-summary line still resolves: when a `working` pane goes quiet, the turn counts as `finished`, or `waiting` when it ends on a question. Background subagents that outlive the turn which started them are matched by `busy_line`, which keeps the session `working` until they finish. Claude Code names pending background agents in its own wait line ("Waiting for 2 background agents to finish"), and that line is what `busy_line` reads. Work the turn-end summary's tail names instead ("· 1 MCP task still running", "· 8 shells, 2 monitors still running") is not the agent working: the prompt takes input while a monitor sits armed, so such a turn reads as finished. A usage or rate-limit banner (`limit_line`) is `errored`. None of it runs while the tool says it has scrolled its own viewport off the live bottom (`scrolled_line`): the visible screen is history then, and a session keeps the status it already had until the viewport returns. Polling keeps running while you are inside a session, so statuses stay live. The selected session's pane tail renders in the preview panel, and moving the cursor fetches the preview immediately.

Codex usage limits with a recognized reset time resume automatically while the manager is running, including managed child sessions and adopted panes. The manager sends a continuation one minute after the reported reset, once the agent is alive and resting at an empty prompt. Codex's clock-only or dated “try again at” messages use the manager's local timezone. Unknown reset formats remain `errored` for manual attention.

The deadline and delivery claim survive manager restarts. A given limit banner is reprompted once; an unchanged banner cannot cause a retry loop, and an unconfirmed send is not repeated. New work clears the old recovery record, and a changed reset banner schedules a new deadline. Dialogs, typed input, a scrolled viewport, and recent operator input hold delivery. Pending launch inputs and messages from other agents wait behind recovery. The continuation preserves existing approval requirements and asks the parent to check subagent progress; each managed Codex child pane also gets its own recovery. At most ten automatic continuations are sent per polling pass, so a shared reset drains over successive passes. The log records scheduled reset times and continuation delivery.

Claude Code handles usage-limit continuation itself (since 2.1.234), controlled by `/config` → “Continue automatically at usage limit”. Gate Inbox never schedules or sends limit continuations to Claude sessions, including managed children.

For Claude Code, status comes first-hand from [hook events](https://docs.anthropic.com/en/docs/claude-code/hooks) instead of pane guessing: sessions launch with a generated `--settings` file whose hooks write the lifecycle state (`working`, `waiting`, `finished`, `idle`) to a per-session status file that the poller reads first. A `StopFailure` of `rate_limit` writes `errored`. A tool call is `working` except for `AskUserQuestion`, which is Claude Code asking a person and not work: nothing further fires until the answer, so its `PreToolUse` writes `waiting`. Matchers cannot exclude a tool and two matchers on one event race, so the single `PreToolUse` command reads the event on stdin to tell them apart. Pane rules still refine it — hooks cannot see a plain-text question, an Esc interrupt, or an error line, so a matching pane verdict upgrades the hook status — and they take over fully as fallback when the hook file is missing or stale. Enabled per tool with `status_source = "claude-hooks"`.

## The board layout

The manager normally shares the frame with a preview of the selected session. Settings (`s`) has a `layout` row, and its `board` mode gives the whole width to the list instead: the names, groups, statuses and work marks get the full terminal rather than thirty percent of it, which is the view to read when you want to see everything running at once.

The preview is not lost. Focusing a session still opens its pane, full width, and leaving it comes back to the board — the same one-panel frame a terminal too narrow for two columns already draws, asked for rather than measured. `board` is a choice about columns, not about a small screen, so unlike `mobile` it does not tighten the rail or cut the key hints.

`\` is that setting under a key, for the times you want the width for a moment rather than for good: it puts the rail away and brings back the layout you were on, and `alt+\` does the same from inside a focused session, where the rail is the only thing between the pane and the whole terminal. A `WIDE` badge on the board names the key back. The setting is persisted either way, so a rail put away with the key is still away after a restart.

## Key hints

The legend under the list is reference material, and once you know the keys it is room the list could have had. By default it follows the terminal: the legend drops to one row on a short window and off a very short one. Settings (`s`) has a `key hints` row to take that decision off the terminal — `always` keeps the full legend at any height, while `never` drops it at any height. `H` still opens the whole key map either way.

## Stats

The header shows a fleet summary: per-status session counts, plus `agents total usage: cpu N% · ram M% · X GB` for every live agent's full process tree (shell, agent, and children). CPU is that tree's CPU time over the last poll as a share of total machine capacity (same 0–100% unit as the computer gauge). RAM is resident set as a share of installed memory, with absolute size beside it. The selected session's detail line uses the same scale for that session alone.

The Computer block in the sessions panel shows machine gauges:

- **CPU**: whole-machine utilization (0-100%)
- **Memory**: used/total. On macOS this matches Activity Monitor's Memory Used (resident RAM minus free, speculative, and reclaimable file cache). On Linux it is `Total - MemAvailable`, so file cache is not counted as used.
- **Swap**: used against the most swap the machine can reach (`used/ceiling * 100`). On Linux the ceiling is the fixed swap size. On macOS the kernel adds and drops 1 GiB swapfiles as pressure moves, so the ceiling is the current allocation plus the free space on `/System/Volumes/VM`, capped at the kernel's 100-file limit.
- **Disk**: fill of the root filesystem (used / (used + available)), with free space from the kernel's available figure
- **Network**: up/down rates on real NICs only (loopback, utun, bridges, and similar virtual interfaces are excluded)
- **Temperature**: `cpu`, `gpu` and `soc` readings in °C, each the hottest sensor in its category, sampled every 5s. Apple Silicon draws no CPU/GPU line, so its dies report as one `soc` figure. A reading appears when the machine exposes that sensor.

## Quiet colour

A full board tints per state, and then tints the rail, the guides, the group names, the badges and the legend on top of that. Everything is coloured, so nothing is: the one row actually waiting on you is the same amount of loud as the twelve getting on with it.

Settings (`s`) has a `colour` row with a `quiet` mode that narrows the palette to what you are scanning for. `waiting`, `errored` and `finished` keep their colour — those are the three states that mean a person is owed something. `working` and `idle` fall back to the type ramp, and so do group names: an agent mid-turn and one resting both need nothing, and a colour saying "nothing is required here" is spent saying nothing. The accent stays, because it marks where the cursor and the keys are.

It is a palette transform rather than a second set of styles, so it reaches every surface at once and follows a theme change. Stepping the setting applies it, so you can see the board before saving.

## Status marks

Each session wears one mark for its state, and by default those are geometric shapes — `◆` waiting, `●` finished, `✕` errored, and so on. They are chosen for font coverage before legibility: a phone or an iPad resolves a glyph its font has no cell for as a `?`, or out of the colour emoji font at double width, which shunts the rest of the row sideways.

The cost is that a shape only carries state to somebody who has learned it, and colour does most of the work — which leaves nothing on a terminal drawing none. Settings (`s`) has a `status marks` row that switches to an emoji set that names each state instead of ranking it. It is opt-in rather than the default because turning it on is a claim about your terminal's font that only you can make; the picker shows the marks as you step it, so you can see whether your terminal draws them before saving. Every emoji mark is the same width as every other, so the column stays a column.

## Themes

`s` opens Settings, where `↑↓` move between fields and `←→` change the focused one.

![settings, with the theme picker and its palette swatches](screenshot-settings.png)

Fifteen palettes ship. Nine dark: `classic`, `solarized dark`, `catppuccin mocha`, `tokyo night`, `gruvbox dark`, `nord`, `dracula`, `rosé pine`, and `monochrome`. Six light: `solarized light`, `catppuccin latte`, `tokyo night day`, `gruvbox light`, `rosé pine dawn`, and `paper`. The swatch strip beside the name previews the palette, and the theme applies as you step through it, so the picker is a live preview of the whole UI. Your pick is saved with the rest of the state and restored on the next run.

**terminal background** decides whose backdrop the frame sits on, and it starts on `inherit`.

Inheriting, the manager leaves your terminal's colors alone. It asks the terminal for its background once at startup and mixes the tones it derives — the sessions rail, the section blocks, the hairline rules — from that, so the rail lands one step above the background you already chose. Except in OLED mode, the backdrop itself is never painted; agent panes keep the terminal's colors too, and nothing of your terminal's theme is written over. A terminal that does not answer the query falls back to the theme's own backdrop, as below.

`match theme` is the older behavior: the terminal's background is repainted to the palette's, so window padding outside the cell grid carries the same tone as the frame and the two meet without a seam, and agent panes are painted to match. The terminal's background is restored when the manager exits — but only on a clean exit, so a crash or a closed window leaves it on the palette's color until something else sets it back.

Switching back to `inherit` also clears the pane backgrounds a `match theme` run left on the tmux server, which otherwise outlive the process that wrote them.

Agent panes declare their background to the agent inside them: the terminal's while inheriting, the theme's while matching. An agent that detects its palette at launch keeps that palette until restarted.
