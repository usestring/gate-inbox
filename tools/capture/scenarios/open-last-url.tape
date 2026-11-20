# ctrl+alt+o opens the newest link the selected session's pane printed. vhs
# cannot type a ctrl+alt chord, so this finds the action in quick actions,
# which also names its key, and runs it from there. The demo transcripts
# print no link, so the run ends on the board saying so rather than on a
# browser opening on the recording machine.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/open-last-url.tape

Sleep 3s
Ctrl+k
Sleep 1500ms
Type "recent link"
Sleep 2s
Screenshot {{SHOTS}}/01-quick-actions-open-last-link.png
Sleep 1s
# The filter ranks recent_sort above it, one row up.
Down
Sleep 800ms
Enter
Sleep 2s
Screenshot {{SHOTS}}/02-no-link-shown.png
Sleep 2s
