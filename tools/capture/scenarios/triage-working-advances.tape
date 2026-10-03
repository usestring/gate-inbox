# Run with --transcripts handover-late,waiting,finished. Enter answers the
# dialog, a late Notification hook refuses the answer, and the agent only goes
# back to work after the answer check has given up.
Sleep 6s
Type "i"
Sleep 3s
Screenshot {{SHOTS}}/01-unanswered-dialog.png
Sleep 1s
Enter
Sleep 2s
Screenshot {{SHOTS}}/02-refused-answer-keeps-focus.png
Sleep 7s
Alt+\
Sleep 1s
Alt+\
Sleep 1500ms
Screenshot {{SHOTS}}/03-working-pass-opens-next-session.png
Sleep 2s
