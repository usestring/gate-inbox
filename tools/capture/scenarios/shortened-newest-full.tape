# Shortened conversation keeps the newest turn full: the two older turns
# shorten to four lines each while the newest renders whole. Hovering a
# shortened turn expands it, but vhs sends no pointer, so the recording
# holds the shortened frame and the hover path is covered by unit tests.
# Run with --no-seed --foreign shortened
#
# {{SHOTS}} is replaced with this run's still directory. Every Screenshot needs
# a Sleep after it, or vhs ends the recording before the frame is written.

Sleep 8s
Type "/"
Sleep 1s
Type "shortened "
Sleep 2s
Enter
Sleep 2s
Screenshot {{SHOTS}}/01-shortened-newest-full.png
Sleep 1s
