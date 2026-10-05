# A destructive confirm now offers a third answer: "don't ask again".
# x on an empty group shows it; A deletes the group and silences that
# dialog, so the next empty group goes on x alone. The settings row is
# where the choice can be turned back on.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/confirm-always.tape

Sleep 3s
Type "g"
Sleep 1500ms
Type "confirm"
Sleep 1s
Enter
Sleep 2s
Screenshot {{SHOTS}}/01-empty-group.png
Sleep 1s
Type "x"
Sleep 1500ms
Screenshot {{SHOTS}}/02-dont-ask-again-on-the-dialog.png
Sleep 1s
Type "A"
Sleep 1200ms
Screenshot {{SHOTS}}/03-deleted-and-silenced.png
Sleep 1s
Type "g"
Sleep 1500ms
Type "second"
Sleep 1s
Enter
Sleep 2s
Screenshot {{SHOTS}}/04-second-empty-group.png
Sleep 1s
Type "x"
Sleep 1500ms
Screenshot {{SHOTS}}/05-no-dialog-now.png
Sleep 1s
Type "s"
Sleep 2s
Down
Down
Down
Down
Down
Down
Down
Down
Down
Down
Down
Sleep 1500ms
Screenshot {{SHOTS}}/06-setting-reads-never.png
Sleep 1s
