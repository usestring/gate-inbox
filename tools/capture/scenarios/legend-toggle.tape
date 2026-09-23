# The footer under a key: hidden and brought back on the list, then the same
# from inside a focused session, where the rows go to the agent's pane.

Screenshot {{SHOTS}}/01-list-with-the-footer.png
Sleep 1s

Type ","
Sleep 1500ms
Screenshot {{SHOTS}}/02-list-footer-hidden.png
Sleep 1s

Type ","
Sleep 1500ms
Screenshot {{SHOTS}}/03-list-footer-back.png
Sleep 1s

Enter
Sleep 2s
Screenshot {{SHOTS}}/04-focused-with-the-footer.png
Sleep 1s

Alt+,
Sleep 1500ms
Screenshot {{SHOTS}}/05-focused-footer-hidden.png
Sleep 1s

Alt+,
Sleep 1500ms
Screenshot {{SHOTS}}/06-focused-footer-back.png
Sleep 1s

Ctrl+\
Sleep 1s
