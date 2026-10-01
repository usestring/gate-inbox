# Run with --no-seed --foreign resting --foreign resting-next. Each adopted
# Claude pane shows a finished turn and ends at the bare prompt, where both
# arrows are spare; the two transcripts are the same text under different
# names so the panes carry different conversation ids and a takeover of one
# does not collide with the other. In triage Left goes back to the list with
# the queue still armed, and Right hands the finished session over to the
# next one.

Sleep 5s
Escape
Sleep 2s
Type "i"
Sleep 3s
Screenshot {{SHOTS}}/01-first-finished.png
Sleep 1s

Left
Sleep 2s
Screenshot {{SHOTS}}/02-left-back-to-the-list-triage-on.png
Sleep 1s

Enter
Sleep 3s
Screenshot {{SHOTS}}/03-refocused.png
Sleep 1s

Right
Sleep 3s
Screenshot {{SHOTS}}/04-right-next-finished.png
Sleep 1s

Ctrl+H
Sleep 2s
Type "/"
Sleep 600ms
Type "prompt"
Sleep 1s
Enter
Sleep 1500ms
Screenshot {{SHOTS}}/05-key-map-prompt-edge.png
Sleep 2s
