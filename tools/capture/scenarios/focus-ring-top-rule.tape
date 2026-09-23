# The focused pane's ring, which now opens on the frame rule the body already
# spends: the list first, so the content column's own top edge is in frame, then
# focus, where the ring's title rides that same rule instead of a row below it.
#
# {{SHOTS}} is replaced with this run's still directory. Every Screenshot needs
# a Sleep after it, or vhs ends the recording before the frame is written.

Screenshot {{SHOTS}}/01-list-top-rule.png
Sleep 1s

Down
Sleep 600ms
Down
Sleep 900ms

Enter
Sleep 2s
Screenshot {{SHOTS}}/02-focused-ring-opens-on-the-rule.png
Sleep 1s

Ctrl+q
Sleep 1s
