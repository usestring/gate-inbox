# Run with --no-seed --foreign slash-opened --cols 160 --rows 44. The pane's
# transcript opens with a skill's slash command, which used to leave the
# rail's opening-prompt block empty. The naming sweep runs every 30s, so by
# 40s the rail, with the cursor moved off the group onto the row, shows the
# command line and the prompt typed after it.
Sleep 40s
Down
Sleep 2s
Screenshot {{SHOTS}}/01-rail-opening-prompts.png
Sleep 2s
