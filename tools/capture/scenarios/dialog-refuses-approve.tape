# ± on a session showing a dialog is refused, and the error bar says why.
# One pane playing the waiting transcript -- a permission dialog, which is what
# the guard reads -- and nothing else on the board:
#
#   tools/capture/capture.sh --no-seed --foreign waiting \
#     --scenario tools/capture/scenarios/dialog-refuses-approve.tape
#
# The worked example of both rules in the README: dismiss the take-over offer a
# --foreign board opens with, then reach the row by name rather than by walking
# to it.

Sleep 5s
Escape
Sleep 1s
Screenshot {{SHOTS}}/01-board.png
Sleep 1s

Type "/"
Sleep 1s
Type "gate-inbox "
Sleep 1500ms
Enter
Sleep 1500ms
Screenshot {{SHOTS}}/02-dialog-row-selected.png
Sleep 1s

# Shoot the refusal well inside one poll: the error bar clears after two
# passes, so a longer wait here catches an empty bar about half the time.
Type "±"
Sleep 800ms
Screenshot {{SHOTS}}/03-approve-refused.png
Sleep 2500ms
