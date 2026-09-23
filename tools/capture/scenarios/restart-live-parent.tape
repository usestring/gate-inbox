# v on a running session that has a dead child under it. The dialog is about
# the session the cursor is on -- the child is a count and a toggle, not the
# default -- which is the whole point after a press of v brought back 47
# archived children and skipped the one session it named.
#
# {{SHOTS}} is replaced with this run's still directory. Every Screenshot needs
# a Sleep after it, or vhs ends the recording before the frame is written.

# The board opens on the group row; the first Down reaches a session.
Down
Sleep 1200ms
Down
Sleep 1800ms

# Give it a child, then end the child so the parent has a dead one under it.
# A new terminal takes the focus, so ctrl+q is what hands the keys back to the
# list -- without it the x and the y below are typed into the shell.
Type "T"
Sleep 5s
Ctrl+q
Sleep 5s
Screenshot {{SHOTS}}/01-child-spawned.png
Sleep 1s
Type "x"
Sleep 5s
Type "y"
Sleep 4s

Up
Sleep 1800ms
Screenshot {{SHOTS}}/02-parent-selected.png
Sleep 1s

Type "v"
Sleep 4s
Screenshot {{SHOTS}}/03-restart-dialog-child-opt-in.png
Sleep 2s

Type "y"
Sleep 5s
Screenshot {{SHOTS}}/04-parent-restarted-child-left-dead.png
Sleep 2s
