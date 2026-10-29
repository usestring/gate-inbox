# Horizontal arrows stay in the focused session; Ctrl+Q returns to the list.
Type "/"
Sleep 1s
Type "focus exit arrow "
Sleep 1500ms
Enter
Sleep 1500ms
Enter
Sleep 2s
Screenshot {{SHOTS}}/01-focused.png
Sleep 1s
Right
Left
Sleep 1200ms
Screenshot {{SHOTS}}/02-arrows-stay-focused.png
Sleep 1s
Ctrl+q
Sleep 1200ms
Screenshot {{SHOTS}}/03-hotkey-returns-to-list.png
Sleep 1s
