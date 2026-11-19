# "confirm launches" is a row in Settings, on by default. Off, a top-level
# agent launches nested sessions without putting a launch Approval to its
# user first. Detached launches stay gated.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/confirm-launches.tape

Sleep 3s
Type "s"
Sleep 2s
Down 22
Sleep 1500ms
Screenshot {{SHOTS}}/01-confirm-launches-on.png
Sleep 1s
Right
Sleep 1500ms
Screenshot {{SHOTS}}/02-confirm-launches-off.png
Sleep 1s
Escape
Sleep 1500ms
Type "s"
Sleep 2s
Down 22
Sleep 1500ms
Screenshot {{SHOTS}}/03-confirm-launches-off-saved.png
Sleep 1s
Escape
Sleep 1s
