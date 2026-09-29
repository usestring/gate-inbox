# Killing a parent with x takes the terminal nested under its child. The
# seeded claude row, found by its transcript, gets a shell; the shell spawns
# a child agent named worker, T on worker opens a terminal under it, and x on
# the parent ends the parent, worker and worker's terminal together.

Sleep 3s
Type "/"
Sleep 1s
Type "release checklist "
Sleep 1500ms
Enter
Sleep 1500ms
Escape
Sleep 1500ms

Type "T"
Sleep 5s
Type `export GATE_INBOX_SESSION_ID=$(tmux display -p '#S' | sed 's/^gi_//')`
Enter
Sleep 1s
Type `"$GATE_INBOX_BIN" spawn --tool claude --name worker --prompt hello`
Enter
Sleep 8s
# Off the screen, so the search below finds the worker row, not this pane.
Type "clear"
Enter
Sleep 1s
Ctrl+q
Sleep 2s

Type "/"
Sleep 1s
Type "worker "
Sleep 1500ms
Enter
Sleep 1500ms
# The search carries worker's parent above it, so the match is one row down.
Down
Sleep 1500ms
Escape
Sleep 1500ms

Type "T"
Sleep 5s
Type "echo opened by worker"
Enter
Sleep 1s
Ctrl+q
Sleep 3s
Screenshot {{SHOTS}}/01-before-x.png
Sleep 1s

Type "/"
Sleep 1s
Type "release checklist "
Sleep 1500ms
Enter
Sleep 1500ms
Escape
Sleep 1500ms
Type "x"
Sleep 1500ms
Screenshot {{SHOTS}}/02-x-dialog.png
Sleep 1s
Type "y"
Sleep 3s
Screenshot {{SHOTS}}/03-after-x.png
Sleep 1s
