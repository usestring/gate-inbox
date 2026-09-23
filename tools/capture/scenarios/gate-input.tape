# The gate's input toggle: F2 switches between Typing and Menu, the mode
# rides along the top rule, and Menu's plain keys reach the pane's history.
# Recorded against a long first transcript, so the pane has scrollback for
# Home to climb and End to come back from:
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/gate-input.tape \
#     --transcripts waiting-long,working,finished

# Let the seeded transcripts play out first, so the queue orders on the
# statuses that are on screen when it is armed.
Sleep 12s

# G arms the drain and opens the session at the head of the queue. The gate
# starts in Typing, which is the mode that sends what is typed to the agent.
Type "G"
Sleep 3s
Screenshot {{SHOTS}}/01-typing.png
Sleep 1s

# F2 is escape-then-OQ: vhs 0.11 has no F2 of its own, and SS3 Q is what a
# terminal sends for it. The same trick every alt chord in these scenarios
# uses -- the two commands go down the pty with no sleep between them, which
# is the escape prefix the key is.
Escape
Type "OQ"
Sleep 2s
Screenshot {{SHOTS}}/02-menu.png
Sleep 1s

# Home in Menu mode is the jump to the oldest of the pane's history, spelled
# as escape-then-[H for the same reason.
Escape
Type "[H"
Sleep 3s
Screenshot {{SHOTS}}/03-top.png
Sleep 1s

# End comes back to the live bottom, and a bare "." skips this one and
# promotes the next -- the mode survives the advance.
Escape
Type "[F"
Sleep 1s
Type "."
Sleep 3s
Screenshot {{SHOTS}}/04-menu-next.png
Sleep 1s

# "l" goes back a gate, and F2 returns to Typing, where the same letters
# reach the agent instead of the board.
Type "l"
Sleep 2s
Escape
Type "OQ"
Type "hello"
Sleep 2s
Screenshot {{SHOTS}}/05-typing-again.png
Sleep 1s

# ctrl+\ stops the gate and puts the rail and the queue back.
Ctrl+\
Sleep 2s
