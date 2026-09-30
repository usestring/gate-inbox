# Run with --transcripts handover,waiting,finished. The first Enter simulates
# a background tool finishing while the dialog stays open; the second
# answers it and paints a working pane. Both events stay on the private server.
Sleep 6s
Type "i"
Sleep 3s
Screenshot {{SHOTS}}/01-unanswered-dialog.png
Sleep 1s
Enter
Sleep 300ms
Alt+\
Sleep 1s
Alt+\
Sleep 1500ms
Screenshot {{SHOTS}}/02-background-call-keeps-focus.png
Sleep 1s
Enter
Sleep 3s
Alt+\
Sleep 1s
Alt+\
Sleep 1500ms
Screenshot {{SHOTS}}/03-answer-opens-next-session.png
Sleep 2s
