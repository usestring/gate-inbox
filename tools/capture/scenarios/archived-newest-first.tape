# The archived view lists what was filed last at the top. Two of the seeded
# sessions are ended in a known order -- gate-inbox-2 first, then
# gate-inbox-3 -- so the archive has to read -3 above -2, whatever order
# the active list had them in. Then a reorder key is pressed there, to show
# the move keys refusing and saying why.
#
# The search filter outlives the Enter that leaves it, so each lookup is
# cleared with Escape before the next one starts.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/archived-newest-first.tape

Screenshot {{SHOTS}}/01-board.png
Sleep 1s

Type "/"
Sleep 1s
Type "gate-inbox-2 "
Sleep 1500ms
Enter
Sleep 1500ms
Type "x"
Sleep 1500ms
Type "y"
Sleep 3s
Escape
Sleep 1s

Type "/"
Sleep 1s
Type "gate-inbox-3 "
Sleep 1500ms
Enter
Sleep 1500ms
Type "x"
Sleep 1500ms
Type "y"
Sleep 3s
Escape
Sleep 1s

Type "t"
Sleep 2500ms
Screenshot {{SHOTS}}/02-archive-newest-first.png
Sleep 1s

Type "J"
Sleep 800ms
Screenshot {{SHOTS}}/03-reorder-refused-in-archive.png
Sleep 2500ms

Type "t"
Sleep 1500ms
