# Run with --no-seed --foreign conversation. The default live terminal keeps
# input visible; compressed focus must be explicitly enabled in experiments.
Sleep 8s
Type "/"
Sleep 1s
Type "conversation "
Sleep 2s
Enter
Sleep 2s
Screenshot {{SHOTS}}/01-conversation-in-the-list.png
Sleep 1s
Enter
Sleep 3s
Screenshot {{SHOTS}}/02-default-live-terminal.png
Sleep 1s
Ctrl+q
Sleep 1s
Type "s"
Sleep 1s
Down
Down
Down
Down
Down
Down
Down
Down
Down
Down
Down
Down
Down
Down
Down
Down
Down
Down
Down
Enter
Sleep 1s
Up
Sleep 1s
Screenshot {{SHOTS}}/03-experiment-default-off.png
Sleep 1s
Right
Sleep 1s
Screenshot {{SHOTS}}/04-experiment-enabled.png
Sleep 1s
Escape
Escape
Sleep 1s
Enter
Sleep 3s
Screenshot {{SHOTS}}/05-opt-in-conversation.png
Sleep 1s
Ctrl+q
Sleep 1s
Type "s"
Sleep 1s
Down
Down
Down
Down
Down
Down
Down
Down
Down
Down
Down
Down
Down
Down
Down
Down
Down
Down
Down
Enter
Sleep 1s
Up
Right
Escape
Escape
Sleep 1s
Enter
Sleep 3s
Screenshot {{SHOTS}}/07-experiment-disabled-terminal.png
Sleep 1s
