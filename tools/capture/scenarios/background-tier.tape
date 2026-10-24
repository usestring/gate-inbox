# A turn resting on a shell it left running reads background, not working:
# the Claude row plays a summary whose "2 shells still running" tail wraps
# onto a row of its own, and the header counts it apart from the working
# Codex row. Record with:
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/background-tier.tape \
#     --transcripts shells-wrapped,codex-working,finished

Sleep 5s
Screenshot {{SHOTS}}/01-board.png
Sleep 1500ms
