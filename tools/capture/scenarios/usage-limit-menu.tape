# A Claude session sitting on its limit menu ("What do you want to do?"),
# which leaves no input line on the screen, with a reply, lists and a table
# above the banner: the pane the limit card reads its banner from.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/usage-limit-menu.tape \
#     --transcripts usage-limit-menu
#
# The row is reached with the arrows rather than a search, since the card is
# not offered while a search is open, and focused, which is where the pane
# the banner is read from is captured.

# The cursor opens on the last seeded row; the first is the claude session.
Up
Sleep 600ms
Up
Sleep 900ms
Enter
Sleep 4s
Screenshot {{SHOTS}}/01-limit-menu-pane.png
Sleep 2s
