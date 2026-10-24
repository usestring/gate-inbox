# A turn resting on a shell it left running reads background, not working:
# the Claude row plays a summary ending "2 shells still running". The legend
# peek shows the ◒ row beside the working and delegating ones. Record with:
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/background-tier.tape \
#     --transcripts shells-wrapped,codex-working,finished

Sleep 5s
Screenshot {{SHOTS}}/01-board.png
Sleep 1500ms
Type "?"
Sleep 1500ms
Screenshot {{SHOTS}}/02-legend.png
Sleep 1500ms
Escape
Sleep 1s
