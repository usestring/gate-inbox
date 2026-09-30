# Every question of a several-question dialog in the list's preview. Run with
#   --no-seed --foreign ask-four --foreign ask-review
# so the board is two Claude panes the manager did not start: one standing on
# the second tab of a four-question dialog, one on its Submit page with every
# question answered. Neither is focused; the preview is the conversation with
# the dialog's card under the newest message.
Sleep 6s
Type "/"
Sleep 1s
Type "Type something"
Sleep 1500ms
Enter
Sleep 4s
Screenshot {{SHOTS}}/01-four-questions-second-on-screen.png
Sleep 2s

# Escape drops the first filter, so the second search starts empty.
Escape
Sleep 1500ms
Type "/"
Sleep 1s
Type "Review your answers"
Sleep 1500ms
Enter
Sleep 4s
Screenshot {{SHOTS}}/02-submit-page-every-answer.png
Sleep 2s
