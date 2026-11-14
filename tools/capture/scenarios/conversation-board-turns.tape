# Run with --no-seed --foreign board-turns. The pane's transcript holds the
# user's two prompts, a message from another session, and two cache-warden
# keep-alives answered "ok". The compressed conversation shows only the
# exchange between the session and its user: the keep-alives, their replies
# and the other session's message are gone, and the session's own reply to
# that message stays.
Sleep 35s
Down
Sleep 4s
Screenshot {{SHOTS}}/01-list-conversation-user-only.png
Enter
Sleep 3s
Escape
Type "OR"
Sleep 4s
Screenshot {{SHOTS}}/02-focus-conversation-user-only.png
Sleep 2s
