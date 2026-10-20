# How long a triage skip lasts is a row in Settings, under smart triage: 15
# minutes by default, stepping through 30, 60 and never expires.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/triage-skip-lasts.tape

Sleep 3s
Type "s"
Sleep 2s
Down 20
Sleep 1500ms
Screenshot {{SHOTS}}/01-triage-skip-default.png
Sleep 1s
Right
Sleep 800ms
Right
Sleep 800ms
Right
Sleep 1500ms
Screenshot {{SHOTS}}/02-triage-skip-never.png
Sleep 1s
Escape
Sleep 1500ms
