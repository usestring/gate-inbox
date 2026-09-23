# A pane that reads its keys and never paints: the board types into it, gives
# up on it, says so, and takes it out of the queue. One foreign pane running
# the "deaf" transcript and nothing else on the board:
#
#   tools/capture/capture.sh --no-seed --foreign deaf \
#     --scenario tools/capture/scenarios/deaf-pane.tape

Sleep 12s
Escape
Sleep 2s
Screenshot {{SHOTS}}/01-board.png
Sleep 1s

# Reached by name rather than by walking to it: which row the cursor opens on
# depends on when the adoption scan took the pane, and a blind Down races it.
Type "/"
Sleep 1s
Type "gate-inbox "
Sleep 1500ms
Enter
Sleep 1500ms
Screenshot {{SHOTS}}/02-the-row-that-needs-a-person.png
Sleep 1s

# Into the pane. It is showing a dialog, so it reads as a session waiting on
# somebody -- exactly the row a drain hands over.
Enter
Sleep 3s
Screenshot {{SHOTS}}/03-focused-on-the-dialog.png
Sleep 1s

# Answer it. Every key is delivered and consumed, and the pane never moves.
# Spaced out on purpose: keys typed faster than a chase completes share one,
# and it is chases the board counts, not keystrokes.
Type "1"
Sleep 500ms
Enter
Sleep 500ms
Type "1"
Sleep 500ms
Enter
Sleep 500ms
Type "y"
Sleep 500ms
Enter

# The sixth key is the one that crosses the run, and the error bar clears
# after a couple of polls -- so this is shot inside one rather than after the
# typing stops.
Sleep 400ms
Screenshot {{SHOTS}}/04-the-board-says-so.png
Sleep 400ms

Type "y"
Sleep 500ms
Enter
Sleep 1200ms
Screenshot {{SHOTS}}/05-nothing-came-of-the-keys.png
Sleep 2s

Ctrl+\
Sleep 2500ms
Screenshot {{SHOTS}}/06-marked-on-the-row.png
Sleep 3s
