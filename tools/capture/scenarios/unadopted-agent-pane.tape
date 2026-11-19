# A pane that draws an agent's prompt but runs no process the board can tie
# to a tool: the board leaves it off the list, and now says so on the status
# line instead of leaving it to be found by hand. Run with the default seed.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/unadopted-agent-pane.tape

# The board opens focused on a pane: leave it, then quit from quick actions so
# the mystery pane exists before the board's first scan.
Sleep 6s
Ctrl+\
Sleep 1500ms
Ctrl+k
Sleep 1500ms
Type "quit (sessions"
Sleep 1500ms
Enter
Sleep 2s
Type `{{TMUX}} new-session -d -s mystery "printf '❯ '; exec sleep 9999"`
Enter
Sleep 1s

Type "{{LAUNCH}}"
Enter
Sleep 2s
# The relaunched board reopens on the pane it last focused; the shot is of
# the list, where the pane is missing.
Ctrl+\
Sleep 700ms
Screenshot {{SHOTS}}/01-unadopted-pane-flagged.png
Sleep 10s
# Still up well past a status message's couple of polls.
Screenshot {{SHOTS}}/02-still-flagged-10s-later.png
Sleep 1s
