# A turn that ended with work still running in the background stays working,
# not finished, however the pane draws it: the Claude row's summary is wrapped
# the way a 52-column pane wraps it ("· 2 shells still" over "running"), and
# the Codex row names its background terminal under the turn's time label.
# Record with:
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/background-work-not-finished.tape \
#     --transcripts shells-wrapped,codex-background,finished
#
# The driver leaves the cursor on the last session it created; the Claude row
# is the first, so two steps up select it.

Sleep 4s
Screenshot {{SHOTS}}/01-board.png
Sleep 1500ms

Up
Sleep 700ms
Screenshot {{SHOTS}}/02-codex-background-terminal.png
Sleep 1500ms
Up
Sleep 1500ms
Screenshot {{SHOTS}}/03-claude-wrapped-shells-tail.png
Sleep 1500ms
