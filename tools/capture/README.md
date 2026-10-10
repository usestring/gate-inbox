# Capture — visual evidence for a gate-inbox PR

A PR that changes what the board draws ships a recording and stills of the real
TUI running the change, embedded at the top of its description. A reviewer
should see the change without building the branch, the same way a portal PR
carries browser video.

```bash
tools/capture/capture.sh                                   # the default board tour
tools/capture/capture.sh --scenario tools/capture/scenarios/my-change.tape
```

It builds the branch, runs the board under [vhs](https://github.com/charmbracelet/vhs),
drives the scenario's keystrokes and writes

```
<out>/media/<run>/board.gif        # plays inline in the PR description
<out>/media/<run>/*.png            # the scenario's stills
<out>/test-results/<run>/video.webm
```

which is the layout `skills/ship-ui-feature/scripts/post-to-pr.ts` uploads.

**This repository is public, so its evidence never lands here.** No recording or
still is attached to a PR in this repo, linked from one, or committed. The
evidence goes to the private companion PR, and only after the operator has seen
every frame and approved that exact upload; `post-to-pr.ts` refuses a public
target outright.

## What it runs against

Nothing of the operator's. The board gets a scratch `GATE_INBOX_HOME`, its
own `TMUX_TMPDIR`, a scratch `HOME` and `XDG_*` directories, and no
`LINEAR_API_KEY`, `GITHUB_TOKEN` or `GH_TOKEN`, so it never lists, moves or
kills a session on the live board and never reads a real transcript, ticket or
pull request. It runs in a fixture `demo-project` under that scratch home, never this
checkout's real path. The driver refuses to start
when a scratch path would be the live board's home, the real `HOME` or the
operator's tmux directory, and deletes a recording whose screen text shows the
operator's home path or login. The sessions it opens run `demo-agent.sh` — a transcript player —
rather than a real CLI, so a capture spawns no agent and spends no tokens.
The seeded set is whichever of `claude`, `codex`, `opencode` this machine has
installed, because the board's agent picker offers no others. Each plays a
transcript in its own pane shape, because the board reads a pane with that
tool's rules: `claude` plays `demo/waiting.txt`, `codex` plays
`demo/codex-working.txt` and `opencode` plays `demo/opencode-finished.txt`, so
with all three installed the board opens on one waiting, one working and one
finished row. `--transcripts a,b,c` replaces the set in that order; a
claude-shaped transcript in a codex or opencode pane reads as idle.

## Theme and size

A capture records in the **oled** theme, on a terminal the size of the one you
are running. Pure black is what reads cleanest in a PR body under either GitHub
theme, and the theme is a row in the board's store, so `themeseed` seeds the
scratch state with it and the terminal around the board is painted black to
match — the margin and the board are drawn by different things, and a mismatch
reads as a rendering bug.

- `--theme "<name>"` records another palette; `--theme current` uses whatever
  your own board is set to (that one settings row is read, never a session).
- `--terminal-bg "#rrggbb"` starts the terminal on another background. A change
  to how much of the screen the board paints needs it: on a terminal that
  already matches the theme, a cell the board left unpainted looks painted.
  vhs's padding around the terminal keeps that colour whatever the board
  sends, so it frames the stills as the reference.
- `--cols N` / `--rows M` override the terminal size. The default is your
  attached terminal, which on a wide one makes a wide capture: GitHub scales an
  image to the body width, so more columns means smaller text to a reviewer.
  Narrowing to what the change actually needs is usually worth it.
- `--font-size N` (default 20) is the resolution knob. vhs sizes its terminal in
  pixels and the board in cells, so the driver measures the cell once per font
  size — two probe runs, cached under `work/` — and computes the pixel size from
  the cell count. `--width` / `--height` still override it outright.

## Panes the manager did not start

`--foreign <transcript>` opens a window on the board's own tmux server before the
board comes up, running the demo agent on that transcript, under a session name
that is not `gi_*` — so the adoption scan takes it the way it takes an agent
you started by hand. Repeat it for more. `--no-seed` skips the spawned trio,
for a scenario whose board is those panes alone. The `idle` transcript is
short enough to have played out before the first poll, so its pane reads as
idle; `finished` and `waiting` read as those. Each foreign pane also gets the
session file Claude Code writes for a live process, in a scratch
`CLAUDE_CONFIG_DIR` the board is pointed at, naming its transcript as the
conversation id — so a takeover can read a conversation off it and the
relaunch replays the same transcript. The seeded tools' resume commands
point at the demo agent as well, so nothing a scenario does can launch a
real CLI.

```bash
tools/capture/capture.sh --scenario tools/capture/scenarios/take-over-adopted.tape \
  --no-seed --foreign idle --foreign finished
```

## Writing a scenario

A scenario is a [tape](https://github.com/charmbracelet/vhs#vhs-command-reference)
fragment. The driver has already launched the board, dismissed the welcome
screen and seeded three sessions, so a scenario starts on the list and only
needs the keys that reach the surface under test:

```
Type "W"
Sleep 2s
Screenshot {{SHOTS}}/01-all-work-on-the-rail.png
Sleep 1s
```

- `{{SHOTS}}` is replaced with this run's still directory.
- `{{LAUNCH}}` is the command that started the board and `{{TMUX}}` the tmux
  binary aimed at its private server, both for a scenario that has to leave the
  board and come back: `q` quits to the shell, `{{TMUX}} kill-server` takes the
  panes with it the way a reboot does, and `{{LAUNCH}}` starts the board again
  on the same scratch home.
- Spell an alt chord the way the keymap spells it — `Alt+,`, `Alt+\`, `Alt+f`.
  vhs sends its own `Alt+x` as a bare `x`, so the driver rewrites the line into
  the `Escape` + `Type` pair that puts the escape byte on the wire. Only a
  single-character chord works; `Alt+Up`, `Alt+Enter` and the other named keys
  are a vhs parse error with no equivalent to rewrite them into.
- Every `Screenshot` needs a `Sleep` after it; vhs writes the frame from the
  recording that follows, so a trailing screenshot is silently dropped.
- Be generous with `Sleep`. Keys are sent whether or not the board has caught
  up, and a key that arrives mid-redraw is simply handled late.
- Keep a copy in `scenarios/` when the flow is worth re-recording later.

### Land on the row you meant

**Address a row by searching for it, never by counting rows.** `/`, the session
name, `↵` leaves the cursor on that row:

```
Type "/"
Sleep 1s
Type "the-session "
Sleep 1500ms
Enter
Sleep 1500ms
```

Walking there with `j`/`k` records differently every run, and not because keys
go missing — see below, they all arrive. The list is live: the 2s poll rebuilds
the tree, a status settles from `working` to `idle` minutes after launch, and
the rail unfolds a ticket row under a session whose branch names one.
`buildTree` holds the cursor on its row's identity through all of that, which is
exactly why searching is stable and counting is not — the row you counted to is
not the row that is there two seconds later. A filtered view (`w`, `i`) moves
most of all, since a row leaves it the moment its status changes.

### Land the caret where the change is

Some of what the board decides is read off the pane's **caret**, not its text:
what Left and Right mean at a prompt is the pair that matters most (`leave_at_prompt`
in the key map — the spare arrow steps back to the list, the other one reaches
the agent). A still of the right pane proves nothing about those keys; the caret
has to be where a resting CLI parks it.

The demo agent leaves it there when the transcript it plays **ends without a
trailing newline**: the final line is printed and the pane's caret stays at its
end. With a trailing newline the caret falls to the blank row below, where no
prompt marker is, and every caret-read key reaches the agent instead.

So to record one of those keys, add a transcript under `demo/` whose last line
is the tool's bare prompt marker and whose last byte is not a newline, and give
it to that tool with `--transcripts`, in the order the driver seeds them
(`claude,codex,opencode`):

```bash
tools/capture/capture.sh --scenario tools/capture/scenarios/focus-exit-arrow.tape \
  --transcripts resting,waiting,working
```

The marker is the tool's own — claude's `❯`, codex's `›`, opencode's `┃` bar —
so a transcript that ends on claude's reads as a prompt only for the claude row.
Reach that row by the text of the transcript, since only one tool gets it.

### A `--foreign` pane is taken over once idle

The board takes adopted panes over by itself, so on a `--no-seed --foreign …`
board an idle pane comes back as a `gi_` session a few seconds in, and a
waiting or working one stays adopted until it goes idle. A scenario about an
adopted row should use a pane that is not idle, or shoot before the first pass.

### Shoot a message on the error bar within a second

Anything a key puts on the error bar is cleared two poll passes later — about
four seconds at the capture's interval, and as little as two when the key lands
just before a pass. A `Sleep 2500ms` before the `Screenshot` therefore catches
an empty bar about half the time, which is the last thing that made a scenario
record differently from one run to the next. Take the still ~800ms after the
key and sleep afterwards instead.

`dialog-refuses-approve.tape` is the worked example of all three rules.

### When a scenario lands somewhere unexpected

Re-run it with `--keep-home` and read the board's own log:

```bash
grep -ao 'msg=key key=[^ ]* branch=[^ ]* mode=[^ ]* row=[^ ]* name=[^ ]*' \
  <out>/work/home/logs/gate-inbox.log
```

Every key the board received is there, with the mode that handled it and the row
the cursor was on when it arrived. That separates a key that was never
delivered, one a dialog swallowed, and one that landed on a row that had moved —
three failures that look identical in a still, and the last two are the ones
that actually happen.

## Requirements

`vhs`, `ttyd`, `ffmpeg`, `tmux`, and a Chromium (vhs screenshots the terminal
through a headless one). Missing tooling exits 2 and changes nothing.

```bash
sudo apt-get install -y ttyd ffmpeg
go install github.com/charmbracelet/vhs@v0.11.0
```

Pin that version. vhs 0.12.0 — the current release — runs a tape to the end,
prints `Creating board.gif...` and exits 0 having written no GIF, no WebM and no
stills. A capture on it fails the artefact check at the end of the run rather
than reporting a success it did not have.

Run it unsandboxed. Chromium needs to open sockets and write its own profile,
which the agent sandbox denies — the failure reads as a browser crash, not as a
permission error.
