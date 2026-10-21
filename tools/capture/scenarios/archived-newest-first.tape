# The archived view lists what was filed last at the top. Two of the seeded
# sessions are ended in a known order -- gate-inbox-2 first, then
# gate-inbox-3 -- so the archive has to read -3 above -2, whatever order
# the active list had them in. Then a reorder key is pressed there, to show
# the move keys refusing and saying why.
#
# The search filter outlives both the Enter and the Escape that leave it, so
# each lookup wipes the field with Ctrl+u first, and the filter is wiped once
# more before the archive is opened.
#
# The archived view ships with no key, so it is opened and closed the way an
# unbound action is reached: quick actions (:), its name, Enter. A key typed
# here would only work on a keymap that happened to bind it.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/archived-newest-first.tape

Screenshot {{SHOTS}}/01-board.png
Sleep 1s

Type "/"
Sleep 1s
Ctrl+u
Sleep 500ms
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
Ctrl+u
Sleep 500ms
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

Type "/"
Sleep 1s
Ctrl+u
Sleep 500ms
Escape
Sleep 1s

Type ":"
Sleep 1s
Type "archived view"
Sleep 1s
Enter
Sleep 2500ms
Screenshot {{SHOTS}}/02-archive-newest-first.png
Sleep 1s

Type "J"
Sleep 800ms
Screenshot {{SHOTS}}/03-reorder-refused-in-archive.png
Sleep 2500ms

Type ":"
Sleep 1s
Type "archived view"
Sleep 1s
Enter
Sleep 1500ms
