# Rebinding onto a key another action already holds. The key map names the
# holder from the live map and waits: n leaves both alone, y (the confirm
# binding) moves the key and says which action gave it up. Here
# show_all_work, which ships unbound, is offered x, the archive (kill) key.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/rebind-clash-warning.tape

Type "h"
Sleep 1500ms
Type "show all of a session"
Sleep 1s
Enter
Sleep 1s
Enter
Sleep 1s
Type "x"
Sleep 1500ms
Screenshot {{SHOTS}}/01-clash-warning.png
Sleep 1s
Type "n"
Sleep 1500ms
Screenshot {{SHOTS}}/02-declined.png
Sleep 1s
Enter
Sleep 1s
Type "x"
Sleep 1500ms
Type "y"
Sleep 1500ms
Screenshot {{SHOTS}}/03-moved.png
Sleep 2500ms
