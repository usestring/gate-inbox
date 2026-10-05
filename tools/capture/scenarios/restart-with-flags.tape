# Restart with flags: the picker lists the restart_flags.json presets over the
# session under the cursor, a bare key picks the Chrome preset, and the
# confirm names the session and the flags before y restarts it.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/restart-with-flags.tape

Sleep 3s

# The board opens on the group row, so the first Down reaches a session.
Down
Sleep 600ms
Down
Sleep 900ms
Screenshot {{SHOTS}}/01-session-selected.png
Sleep 1s

Type ":"
Sleep 1500ms
Type "restart it with extra flags"
Sleep 1500ms
Screenshot {{SHOTS}}/02-quick-actions-filtered.png
Sleep 1s
Enter
Sleep 1500ms
Screenshot {{SHOTS}}/03-restart-flags-picker.png
Sleep 1s

Type "c"
Sleep 1500ms
Screenshot {{SHOTS}}/04-restart-confirm.png
Sleep 1s

Type "y"
Sleep 3s
Screenshot {{SHOTS}}/05-restarted.png
Sleep 2s
