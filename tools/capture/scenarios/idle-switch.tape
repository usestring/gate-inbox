# "switch when idle" is a row in Settings, on by default. Off, an operator
# left three minutes in a working session stays there instead of being moved
# to the next session that needs a person.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/idle-switch.tape

Sleep 3s
Type "s"
Sleep 2s
Down 21
Sleep 1500ms
Screenshot {{SHOTS}}/01-idle-switch-on.png
Sleep 1s
Right
Sleep 1500ms
Screenshot {{SHOTS}}/02-idle-switch-off.png
Sleep 1s
Escape
Sleep 1500ms
Type "s"
Sleep 2s
Down 21
Sleep 1500ms
Screenshot {{SHOTS}}/03-idle-switch-off-saved.png
Sleep 1s
Escape
Sleep 1s
