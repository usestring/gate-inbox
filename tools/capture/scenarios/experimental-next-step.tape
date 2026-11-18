# Next step is off until its Experimental row is on. On, the hotkey menu on a
# session at rest carries one suggested instruction on its suggestion line,
# and ctrl+y inserts it into the prompt unsent.
#
# The capture has no GitHub login, so the card here is the one that needs no
# pull request: the fixture project holds a file nobody committed and the
# session opened none.
#
# Run with the claude transcript that ends at rest:
#   tools/capture/capture.sh --scenario tools/capture/scenarios/experimental-next-step.tape \
#     --transcripts resting,waiting,working

Sleep 8s

# Two sessions at rest leave the seeded board draining triage, focused on the
# first; the menu and Settings below are the list's.
Ctrl+\
Sleep 2s

Ctrl+f
Sleep 1s
Type "focus exit arrow "
Sleep 1500ms
Enter
Sleep 1500ms

Ctrl+S
Sleep 2s
Screenshot {{SHOTS}}/01-off-by-default-no-card.png
Sleep 1s
Escape
Sleep 1s

# ctrl+alt+s, spelled as the escape byte and the chord. Settings is walked
# by search throughout: the list's last row is the build's version string,
# which the evidence scan can take for a login name, and a search that never
# matches that row keeps it off screen, on the way out as well as in.
Escape
Ctrl+S
Sleep 2s
Type "/"
Type "mental"
Sleep 1s
Enter
Sleep 1s
Enter
Sleep 1s
Up
Sleep 1s
Right
Sleep 1s
Screenshot {{SHOTS}}/02-next-step-on.png
Sleep 1s
Escape
Sleep 1s
Type "/"
Backspace 5
Type "arks"
Sleep 500ms
Enter
Sleep 500ms
Escape
Sleep 1s
Escape
Sleep 2s

Ctrl+S
Sleep 3s
Screenshot {{SHOTS}}/03-card-on-a-finished-session.png
Sleep 1s

Ctrl+y
Sleep 800ms
Screenshot {{SHOTS}}/04-inserted-unsent.png
Sleep 2s
Escape
Sleep 1s
