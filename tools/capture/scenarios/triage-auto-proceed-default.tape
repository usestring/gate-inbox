# The help card after the default flip: the queue row now says the handover
# happens and where to turn it off, which is what an operator looking for
# "why did my queue not advance" had no way to find.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/triage-auto-proceed-default.tape
#
# The handover itself is not recordable here. Both gestures that count as an
# answer are read off the caret -- selectionDialogUp wants it on the dialog's
# own marker, textTypedAtPrompt wants it after text on a prompt line -- and
# demo-agent.sh replays its transcript with printf '%s\n' per line, so the
# caret always ends on a fresh blank row below whatever was drawn. The board
# is right to see no answer there. internal/ui's live-pane tests drive the
# real gestures instead.

# Let the seeded transcripts play out first, so the queue orders on the
# statuses that are on screen when it is built.
Sleep 12s
Screenshot {{SHOTS}}/01-board.png
Sleep 1s

# i flattens the groups into one queue and opens the session at the head of
# it, which is the state an operator is in when the handover would fire.
Type "i"
Sleep 3s
Screenshot {{SHOTS}}/02-head-of-the-queue.png
Sleep 1s

# Back out to the list, so the card's keys reach the board rather than the
# pane the queue just put us inside.
Ctrl+\
Sleep 3s

# The card, filtered to the queue's own rows. Each note stands on its own:
# the search drops the rows it does not match, so a note that finished a
# neighbouring sentence would lose its subject here.
Type "H"
Sleep 2s
Type "/"
Sleep 600ms
Type "triage"
Sleep 1s
Enter
Sleep 2s
Screenshot {{SHOTS}}/03-card-names-the-handover.png
Sleep 2s
