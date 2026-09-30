# : opens the quick actions palette over the list. Typing narrows it to the
# action, each row carrying its key and keys.toml name, and enter runs it and
# leaves a note with the key that does it in one press.

Sleep 3s
Type ":"
Sleep 1500ms
Screenshot {{SHOTS}}/01-palette-open.png
Sleep 1s
Type "fold"
Sleep 1500ms
Screenshot {{SHOTS}}/02-filtered.png
Sleep 1s
Enter
Sleep 1500ms
Screenshot {{SHOTS}}/03-ran-with-key-note.png
Sleep 2s
Ctrl+p
Sleep 1500ms
Screenshot {{SHOTS}}/04-recent-first.png
Sleep 1s
Escape
Sleep 1s
