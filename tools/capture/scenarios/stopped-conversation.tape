# Run with --no-seed --foreign conversation. The saved fixture conversation
# remains readable after the private tmux server, and every agent pane, is gone.
Sleep 6s
Type "/"
Sleep 1s
Type "inbox-conversation "
Sleep 1500ms
Enter
Sleep 1s
Enter
Sleep 2s
Ctrl+q
Sleep 1s
Type "a"
Sleep 5s
Type "q"
Sleep 2s
Type "{{TMUX}} kill-server"
Enter
Sleep 2s
Type "{{LAUNCH}}"
Enter
Sleep 14s
Type "/"
Sleep 1s
Type "inbox-conversation "
Sleep 1500ms
Enter
Sleep 2s
Screenshot {{SHOTS}}/01-stopped-saved-conversation.png
Sleep 2s
Type ":"
Sleep 1s
Type "conversation"
Sleep 1s
Enter
Sleep 1s
Screenshot {{SHOTS}}/02-expanded-saved-conversation.png
Sleep 2s
