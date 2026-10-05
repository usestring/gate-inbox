# The archived view rebound to y, and every hint that names the way to it
# following. The rebind is made on the key map screen, the way an operator
# makes it: h (which opens on its search field), the action's name, Enter to
# arm, then the new key. Then a kill dialog says y finds the row, and the
# archived view's own badge says y goes back -- where it used to print t
# whatever the archived view was on.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/rebound-archived-hint.tape

Screenshot {{SHOTS}}/01-board.png
Sleep 1s

Type "h"
Sleep 1500ms
Type "archived view"
Sleep 1s
Enter
Sleep 1s
Screenshot {{SHOTS}}/02-key-map-archived-view.png
Enter
Sleep 1s
Type "y"
Sleep 1500ms
Screenshot {{SHOTS}}/03-rebound-to-y.png
Escape
Sleep 800ms
Escape
Sleep 1500ms

Type "x"
Sleep 1500ms
Screenshot {{SHOTS}}/04-kill-dialog-names-y.png
Type "y"
Sleep 3s

Type "y"
Sleep 2500ms
Screenshot {{SHOTS}}/05-archived-badge-names-y.png
Sleep 2500ms
