# The default scenario: the board as it opens, then the two views a reader is
# most likely to need context from. Copy this file and drive the surface your
# PR changes instead — the driver has already seeded three sessions and left
# the cursor on the list.
#
# {{SHOTS}} is replaced with this run's still directory. Every Screenshot needs
# a Sleep after it, or vhs ends the recording before the frame is written.

Screenshot {{SHOTS}}/01-board.png
Sleep 1s

Down
Sleep 600ms
Down
Sleep 900ms
Screenshot {{SHOTS}}/02-session-selected.png
Sleep 1s

Type "W"
Sleep 2s
Screenshot {{SHOTS}}/03-all-work-on-the-rail.png
Sleep 1s
Type "W"
Sleep 1s

# Triage focuses a session, so leaving it is ctrl+\ (stop triage), not escape.
Type "i"
Sleep 2s
Screenshot {{SHOTS}}/04-triage-queue.png
Sleep 1s
Ctrl+\
Sleep 1s
