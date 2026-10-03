# The last-pane walk: l rides the default row, one press steps back to the
# pane just left, and a second press keeps walking.
#
# Seeding leaves the cursor on the last session it made, so the first Enter
# focuses a session row with no search involved, and two steps up the rail
# reaches a second pane the same way.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/last-pane-walk.tape
#
# {{SHOTS}} is replaced with this run's still directory. Every Screenshot needs
# a Sleep after it, or vhs ends the recording before the frame is written.

Enter
Sleep 2s
Ctrl+q
Sleep 1s
Type "k"
Sleep 500ms
Type "k"
Sleep 500ms
Enter
Sleep 2s
Ctrl+q
Sleep 1s
Screenshot {{SHOTS}}/01-list-names-last-pane.png
Sleep 1s
Type "l"
Sleep 2s
Screenshot {{SHOTS}}/02-back-one-pane.png
Sleep 1s
Ctrl+q
Sleep 1s
Type "l"
Sleep 2s
Screenshot {{SHOTS}}/03-back-another-pane.png
Sleep 1s
