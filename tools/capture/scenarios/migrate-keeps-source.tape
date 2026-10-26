# Run with --no-seed --foreign conversation: the migrate card needs a session
# with a transcript to move. A migration from the card leaves the source
# running beside the new session; only an account switch retires it.
Sleep 8s
Type "/"
Sleep 1s
Type "conversation "
Sleep 2s
Enter
Sleep 2s

Type ":"
Sleep 1500ms
Type "migrate it to another CLI"
Sleep 1500ms
Enter
Sleep 2s
Tab
Sleep 1s
Screenshot {{SHOTS}}/01-migrate-card.png
Sleep 1s

Enter
Sleep 4s
Ctrl+q
Sleep 3s
Screenshot {{SHOTS}}/02-source-stays-beside-new-session.png
Sleep 2s
