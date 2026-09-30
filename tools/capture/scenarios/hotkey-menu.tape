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
