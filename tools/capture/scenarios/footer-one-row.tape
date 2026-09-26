# The decluttered footers: the list carries a single Session row, and the
# focused tier no longer states that typing reaches the agent. Seeding leaves
# the cursor on the last session it made, so Enter focuses a session row.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/footer-one-row.tape
Sleep 2s
Screenshot {{SHOTS}}/01-list-one-session-row.png
Sleep 1s
Enter
Sleep 3s
Screenshot {{SHOTS}}/02-focused-without-the-typing-hint.png
Sleep 1s
