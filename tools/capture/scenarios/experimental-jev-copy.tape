# Run with --no-seed --foreign jev-copy. The copy key asks JEV for real, so it
# needs TYPESAFE_API_KEY in the environment, and it writes this machine's
# clipboard with the fixture's text. JEV can answer after the first press's
# status has cleared, so the still is a second press, copying from the picks
# the first one kept.
Sleep 8s
Type "s"
Sleep 2s
Up
Up
Up
Up
Up
Up
Up
Sleep 1s
Enter
Sleep 1s
Up
Sleep 1s
Right
Sleep 1s
Screenshot {{SHOTS}}/01-jev-copy-enabled.png
Sleep 1s
Escape
Sleep 1s
Escape
Sleep 2s
Type "/"
Sleep 1s
Type "demo-api "
Sleep 2s
Enter
Sleep 1500ms
Type "'"
Sleep 8s
Type "'"
Sleep 800ms
Screenshot {{SHOTS}}/02-pick-copied.png
Sleep 2s
