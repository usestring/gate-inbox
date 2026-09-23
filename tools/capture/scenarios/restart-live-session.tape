# v on a session that is still running: the confirm names what it costs and
# what it keeps, then the agent comes back on the same conversation.
#
# {{SHOTS}} is replaced with this run's still directory. Every Screenshot needs
# a Sleep after it, or vhs ends the recording before the frame is written.

Screenshot {{SHOTS}}/01-board.png
Sleep 1s

# The board opens on the group row, so the first Down is what reaches a
# session at all; the second picks the running claude one.
Down
Sleep 600ms
Down
Sleep 900ms
Screenshot {{SHOTS}}/02-live-session-selected.png
Sleep 1s

Type "v"
Sleep 2s
Screenshot {{SHOTS}}/03-restart-confirm.png
Sleep 2s

Type "y"
Sleep 3s
Screenshot {{SHOTS}}/04-restarted.png
Sleep 2s
