# The drain that G used to arm, built from triage and the two hide keys. The
# board opens in triage, enter focuses the head of the queue, alt+\ puts the list
# away and alt+, the key hints. G itself now does nothing on the board.

Sleep 12s
Type "G"
Sleep 1500ms
Screenshot {{SHOTS}}/01-G-does-nothing.png
Sleep 1s

Up
Sleep 300ms
Up
Sleep 300ms
Enter
Sleep 3s
Screenshot {{SHOTS}}/02-triage-head-of-the-queue.png
Sleep 1s

Alt+\
Sleep 1500ms
Screenshot {{SHOTS}}/03-list-away.png
Sleep 1s

Alt+,
Sleep 1500ms
Screenshot {{SHOTS}}/04-key-hints-away.png
Sleep 1s

Ctrl+\
Sleep 2s
Screenshot {{SHOTS}}/05-back-on-the-board.png
Sleep 1s
