# Run with --no-seed --foreign conversation: the migrate card needs a session
# with a transcript to move, which the seeded demo panes do not write. The
# card opens on the source's permission settings; turning them on moves the
# conversation to codex with its prompts off, and the new row is marked.
Sleep 8s
Type "/"
Sleep 1s
Type "conversation "
Sleep 2s
Enter
Sleep 2s

# Migrate has no key of its own; quick actions reach it.
Type ":"
Sleep 1500ms
Type "migrate it to another CLI"
Sleep 1500ms
Enter
Sleep 2s
Tab
Sleep 1s
Screenshot {{SHOTS}}/01-migrate-opens-on-source.png
Sleep 1s

Down
Sleep 500ms
Space
Sleep 500ms
Down
Sleep 500ms
Space
Sleep 1s
Screenshot {{SHOTS}}/02-migrate-turns-them-on.png
Sleep 1s

Enter
Sleep 4s
Ctrl+q
Sleep 2s
Screenshot {{SHOTS}}/03-moved-row-marked.png
Sleep 2s
