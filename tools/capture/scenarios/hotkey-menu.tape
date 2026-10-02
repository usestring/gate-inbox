# Space opens the hotkey menu: the snippets under the keys that send them, and
# no text box. The first still is the menu on a session; the second is the
# acknowledgement after a bare "c" sends "continue" to it.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/hotkey-menu.tape

Sleep 8s

Type "/"
Sleep 1s
Type "claude "
Sleep 1500ms
Enter
Sleep 1500ms

Type " "
Sleep 1500ms
Screenshot {{SHOTS}}/01-hotkey-menu.png
Sleep 1s

Type "c"
Sleep 800ms
Screenshot {{SHOTS}}/02-sent-from-the-menu.png
Sleep 1s

Escape
Sleep 1s

# The same menu opens inside a focused pane. Enter focuses, ctrl+] opens
# the menu, and the bare key answers the pane in front of you.
Enter
Sleep 2s
Ctrl+]
Sleep 1500ms
Screenshot {{SHOTS}}/03-hotkey-menu-in-focus.png
Sleep 1s

Type "c"
Sleep 800ms
Screenshot {{SHOTS}}/04-sent-in-focus.png
Sleep 1s

Escape
Sleep 500ms
Ctrl+q
Sleep 1s
