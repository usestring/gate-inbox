# A skipped question returns after the other eligible work, without a reset.
# Run with --no-seed; both sessions replay the waiting Claude fixture.
Ctrl+n
Sleep 1500ms
Type "claude"
Sleep 800ms
Enter
Sleep 8s
Ctrl+\
Sleep 2s
Ctrl+n
Sleep 1500ms
Type "claude"
Sleep 800ms
Enter
Sleep 8s
Ctrl+\
Sleep 1s
Type "i"
Sleep 1s
Type "i"
Sleep 2s
Enter
Sleep 3s
Screenshot {{SHOTS}}/01-first-session.png
Sleep 1s
Ctrl+q
Sleep 3s
Screenshot {{SHOTS}}/02-other-work-first.png
Sleep 1s
Ctrl+q
Sleep 3s
Screenshot {{SHOTS}}/03-skipped-session-returns.png
Sleep 2s
