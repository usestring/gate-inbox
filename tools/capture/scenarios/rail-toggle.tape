# The rail under a key: the list beside the pane put away and brought back
# from the board, then the same from inside a focused session, where the
# columns go to the agent's pane.

Screenshot {{SHOTS}}/01-list-with-the-preview.png
Sleep 1s

Type "\"
Sleep 1500ms
Screenshot {{SHOTS}}/02-list-preview-hidden.png
Sleep 1s

Type "\"
Sleep 1500ms
Screenshot {{SHOTS}}/03-list-preview-back.png
Sleep 1s

Enter
Sleep 2s
Screenshot {{SHOTS}}/04-focused-with-the-rail.png
Sleep 1s

Alt+\
Sleep 1500ms
Screenshot {{SHOTS}}/05-focused-full-width.png
Sleep 1s

Alt+\
Sleep 1500ms
Screenshot {{SHOTS}}/06-focused-rail-back.png
Sleep 1s

Ctrl+\
Sleep 1s
