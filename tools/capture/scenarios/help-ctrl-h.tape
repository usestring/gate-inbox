# ctrl+h opens the key map from the list and from inside a focused session,
# and closes it again from either screen.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/help-ctrl-h.tape

Ctrl+h
Sleep 2s
Screenshot {{SHOTS}}/01-key-map-from-list.png
Sleep 1s

Ctrl+h
Sleep 2s
Screenshot {{SHOTS}}/02-back-on-list.png
Sleep 1s

Enter
Sleep 2s
Screenshot {{SHOTS}}/03-focused-session.png
Sleep 1s

Ctrl+h
Sleep 2s
Screenshot {{SHOTS}}/04-key-map-from-focus.png
Sleep 1s

Ctrl+h
Sleep 2s
Screenshot {{SHOTS}}/05-back-in-focus.png
Sleep 1s

Ctrl+q
Sleep 1s
