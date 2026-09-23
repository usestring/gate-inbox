# A managed claude session whose agent stops being the one the manager
# launched, so nothing is writing its status file any more. Run with
#   --no-seed --transcripts finished
#
# The board used to go on reading that file as its tier-1 status source
# forever: the env var naming it survives in the pane's shell, so the file
# stays exactly as the last live hook left it and the row is pinned to a
# status nothing will ever move. It now falls through to the pane and says so
# with a subtle ⊙ beside the name -- beside the mute and deaf marks, in the
# subtle tint rather than the errored one, because a hookless session is
# working correctly and only being read with worse instruments.
#
# The incident is reproduced by suspending the agent, which is what happened
# on the real board: a whole process group stopped mid-spawn, leaving the
# wired agent in state T while a hand-run `claude --resume` with no --settings
# flag did the work. A stopped process writes no hook file, which is why the
# check does not count the flag on one.
#
# It seeds itself rather than using the driver's trio so there is exactly one
# demo agent to suspend. The opening sleep is long because the board builds an
# gate-inbox binary at startup, and every key before it is up is spent on a
# shell.

Sleep 30s
Enter
Sleep 4s

Type "n"
Sleep 2s
Type "claude"
Sleep 1500ms
Enter
Sleep 14s

# Back to the manager first. Spawning leaves the board focused on the new
# pane, where "typing goes to the agent" -- so every key below would be typed
# into the demo agent instead of the board without this.
Ctrl+q
Sleep 6s

# The control, and the case that must never gain the mark: the agent is the
# one the board launched, the flag is on its command line, and the row is
# clean.
Screenshot {{SHOTS}}/01-wired-row-is-unmarked.png
Sleep 2s

# Out to the shell to suspend the agent, then back in on the same scratch
# home. The pane and its session survive both, because they live on the
# board's tmux server rather than in the board.
Type "q"
Sleep 5s
Type "pkill -STOP -f demo-agent.sh"
Enter
Sleep 2s
Type "{{LAUNCH}}"
Enter
Sleep 35s

# The same row, now marked. Its status is still whatever the pane says --
# the mark adds that the pane is all the board has for this row.
Screenshot {{SHOTS}}/02-hookless-row-is-marked.png
Sleep 2s

# The pane behind it, with the suspended agent still in it.
Enter
Sleep 3s
Screenshot {{SHOTS}}/03-the-suspended-agent.png
Sleep 2s
Ctrl+q
Sleep 2s
