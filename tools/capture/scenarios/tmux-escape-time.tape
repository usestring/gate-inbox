# The startup tmux check on a server that holds Esc for 500ms, tmux 3.4's
# default escape-time: the card names it with the line that shortens it.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/tmux-escape-time.tape
#
# The driver's launch line runs the board outside tmux, where the check has
# no client to read, so the tape leaves the board and starts it again in a
# session on the scratch server with TMUX left set, the way an operator runs
# it. The escape-time is set on that server first, so the run does not depend
# on which tmux the machine has, and its status line is off, since it prints
# the machine's hostname.

Type "q"
Sleep 3s

Hide
Type `launch='{{LAUNCH}}'`
Enter
Sleep 500ms
Type `nested=${launch/env -u TMUX -u TMUX_PANE /env }`
Enter
Sleep 500ms
Type `{{TMUX}} set -s escape-time 500 \; set -g status off \; new-session -s tmux-check "$nested"`
Enter
Sleep 10s
Show
Screenshot {{SHOTS}}/01-tmux-check.png
Sleep 2s
