# The board running inside a pane of one of its own managed sessions, which is
# where an operator puts it every time they start it from an agent's window.
# That session used to be a row on this board: selecting it aimed the preview,
# the status poll and an attach at the manager's own screen, so the board drew
# itself inside itself. Run with --transcripts working,waiting,finished.
#
# No keystroke can produce this arrangement, so the tape leaves the board,
# respawns one managed pane on the manager binary, and attaches to it. The
# launch line the driver hands out unsets TMUX and TMUX_PANE -- which is the
# whole point of the arrangement -- so those two are put back, and nothing
# else about it changes.

# Three managed sessions, on a board running in a terminal of its own.
Screenshot {{SHOTS}}/00-three-managed-sessions.png
Sleep 2s

Type "q"
Sleep 3s

Hide
Type `host=$({{TMUX}} list-sessions -F '#{session_name}' | grep '^gi_' | head -1)`
Enter
Sleep 500ms
Type `launch='{{LAUNCH}}'`
Enter
Sleep 500ms
Type `nested=${launch/env -u TMUX -u TMUX_PANE /env }`
Enter
Sleep 500ms
# A driver that stopped spelling the unsets that way would leave the board
# outside tmux, and the stills would go on looking like an ordinary board
# rather than the arrangement this tape exists to record.
Type `[ "$nested" = "$launch" ] && nested='echo THE LAUNCH LINE NO LONGER UNSETS TMUX; sleep 300'`
Enter
Sleep 500ms
Type `{{TMUX}} respawn-pane -k -t "$host" "$nested"`
Enter
Sleep 10s
Show

Type `{{TMUX}} attach -t "$host"`
Enter
Sleep 14s

# tmux inside tmux: the outer server has no clipboard, so the board opens on
# the notice about it. Any key closes it.
Escape
Sleep 3s

# The same three sessions less the one this board is running in -- the status
# bar names it, and the other two are untouched. Before the change it was
# listed here too, and opening it drew this board inside its own preview.
Screenshot {{SHOTS}}/01-the-hosting-session-is-absent.png
Sleep 2s

# The row it would have drawn is still in the store, in the care of whichever
# board is not standing in its pane -- hidden, never deleted. Its own agent's
# window is what this pane now holds.
Down
Sleep 2s
Screenshot {{SHOTS}}/02-another-row-previews-normally.png
Sleep 2s
