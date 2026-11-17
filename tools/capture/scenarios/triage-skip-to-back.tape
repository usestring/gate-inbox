# A skipped question returns after the other eligible work, without a reset.
# Run with --no-seed --foreign waiting --foreign finished.
Sleep 12s
Ctrl+\
Sleep 1s
Type "i"
Sleep 1s
Type "i"
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
