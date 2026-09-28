# The sessions rail on the right of the frame: the list with its preview to
# the left, the split dragged with the resize key (right now narrows the rail),
# then a focused session with its ring on the left and the rail beside it.

Screenshot {{SHOTS}}/01-list-rail-on-right.png
Sleep 1s

Type "|"
Sleep 800ms
Right
Sleep 300ms
Right
Sleep 300ms
Right
Sleep 800ms
Screenshot {{SHOTS}}/02-resizing-right-narrows-rail.png
Sleep 1s
Enter
Sleep 1s

Type "/"
Sleep 1s
Type "181930-2 "
Sleep 1500ms
Enter
Sleep 1500ms
Enter
Sleep 2500ms
Screenshot {{SHOTS}}/03-focused-rail-on-right.png
Sleep 1s

Ctrl+\
Sleep 1s
