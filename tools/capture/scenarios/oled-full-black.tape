# OLED blacks out the whole terminal: the rail, the preview's unstyled runs of
# agent output and a focused pane. Record it on a terminal that starts grey,
# so any cell the board leaves alone shows:
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/oled-full-black.tape \
#     --terminal-bg "#3a3f4b"

Sleep 8s
Screenshot {{SHOTS}}/01-board.png
Sleep 1s

Type "/"
Sleep 1s
Type "gate-inbox-2"
Sleep 1500ms
Enter
Sleep 2s
Screenshot {{SHOTS}}/02-preview.png
Sleep 1s

Enter
Sleep 2500ms
Screenshot {{SHOTS}}/03-focused-pane.png
Sleep 1s
Ctrl+\
Sleep 1500ms

Type "s"
Sleep 1500ms
Screenshot {{SHOTS}}/04-settings.png
Sleep 1s
Escape
Sleep 1s
