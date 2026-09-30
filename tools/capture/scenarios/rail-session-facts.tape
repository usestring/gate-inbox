# The rail's session block under the list carries only what the cursor row
# does not: the row already draws the name, the state and the age, so the
# block keeps the CLI chip, group, started, dir and usage. Run with
#   --cols 160 --rows 44
# so the rail is tall enough for the full dock the block rides on.

Sleep 6s
Screenshot {{SHOTS}}/01-rail-session-block.png
Sleep 1s
Down
Sleep 2s
Screenshot {{SHOTS}}/02-next-row-session-block.png
Sleep 1s
