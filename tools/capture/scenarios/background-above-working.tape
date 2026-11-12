# A background row sorts above a working one in triage whatever their tiers:
# the Codex row is working and raised to urgent, the Claude row rests on a
# shell it left running, and the triage rail still puts the background row
# first. The opencode row is muted so triage focuses no pane. Record with:
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/background-above-working.tape \
#     --transcripts shells-wrapped,codex-working --cols 110 --rows 24

Sleep 5s
Screenshot {{SHOTS}}/01-board.png
Sleep 1s
Type "M"
Sleep 800ms
Up
Sleep 600ms
Ctrl+P
Sleep 1500ms
Screenshot {{SHOTS}}/02-working-urgent.png
Sleep 1s
Type "i"
Sleep 2s
Screenshot {{SHOTS}}/03-triage.png
Sleep 1500ms
