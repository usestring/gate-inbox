# Smart triage is a row in Settings, on by default. Triage first orders the
# queue newest turn first; turned off, the same board is back on the strict
# state-first order.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/smart-triage.tape

Sleep 3s
Type "s"
Sleep 2s
Down 19
Sleep 1500ms
Screenshot {{SHOTS}}/01-smart-triage-row-on.png
Sleep 1s
Escape
Sleep 1500ms
Type "i"
Sleep 3s
Ctrl+\
Sleep 2s
Screenshot {{SHOTS}}/02-triage-rail-smart.png
Sleep 1s
Type "i"
Sleep 1500ms
Type "s"
Sleep 2s
Down 19
Sleep 1s
Right
Sleep 1500ms
Screenshot {{SHOTS}}/03-smart-triage-row-off.png
Sleep 1s
Escape
Sleep 1500ms
Type "i"
Sleep 3s
Ctrl+\
Sleep 2s
Screenshot {{SHOTS}}/04-triage-rail-strict.png
Sleep 1s
