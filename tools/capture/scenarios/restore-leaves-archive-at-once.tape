# Restoring a row takes it out of the archived view on the frame the answer
# lands, not on the next poll. demo-project-2 is archived, the archive is opened,
# and the restore is answered; the still is taken 300ms later, well inside a
# poll interval, so it shows the archive already empty of the row.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/restore-leaves-archive-at-once.tape

Screenshot {{SHOTS}}/01-board.png
Sleep 1s

Type "/"
Sleep 1s
Ctrl+u
Sleep 500ms
Type "demo-project-2 "
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
Screenshot {{SHOTS}}/02-archived.png
Sleep 1s

Type "u"
Sleep 1s
Screenshot {{SHOTS}}/03-restore-confirm.png
Type "y"
Sleep 300ms
Screenshot {{SHOTS}}/04-restored-at-once.png
Sleep 2500ms

Type ":"
Sleep 1s
Type "archived view"
Sleep 1s
Enter
Sleep 2000ms
Screenshot {{SHOTS}}/05-back-on-board.png
Sleep 1500ms
