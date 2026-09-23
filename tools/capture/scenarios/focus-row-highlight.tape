# The selected rail row's highlight: the cursor starts on the last session,
# wraps onto the group, steps down to the first session, then focuses it, so
# any shift in the rows around the selection shows between stills.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/focus-row-highlight.tape

Sleep 1500ms
Screenshot {{SHOTS}}/01-last-session-selected.png
Sleep 1s

Down
Sleep 900ms
Screenshot {{SHOTS}}/02-group-selected.png
Sleep 1s

Down
Sleep 900ms
Screenshot {{SHOTS}}/03-first-session-selected.png
Sleep 1s

Enter
Sleep 2s
Screenshot {{SHOTS}}/04-focused.png
Sleep 1s

Ctrl+q
Sleep 1s
