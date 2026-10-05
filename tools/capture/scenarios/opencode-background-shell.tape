# An OpenCode turn that ended while a shell it moved to the background still
# ran stays working, not finished: the composer footer names the work as a
# count ("↓ 1 shell"), and the rule this branch adds is what reads it. The
# driver leaves the cursor on the last session it created, the OpenCode row.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/opencode-background-shell.tape \
#     --transcripts finished,codex-resting,opencode-background-shell

Sleep 4s
Screenshot {{SHOTS}}/01-opencode-background-shell-working.png
Sleep 1500ms
