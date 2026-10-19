# ctrl+x kills from the list and from inside search, not only from a pane;
# D in the archived view then brings every killed session back.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/restore-all-and-ctrl-x.tape

Screenshot {{SHOTS}}/01-board.png
Sleep 1s

Down
Sleep 600ms
Down
Sleep 800ms
Ctrl+X
Sleep 1500ms
Screenshot {{SHOTS}}/02-ctrl-x-on-the-list.png
Sleep 1s
Type "y"
Sleep 3s

Type "/"
Sleep 1s
Type "project-3"
Sleep 1500ms
Ctrl+X
Sleep 1500ms
Screenshot {{SHOTS}}/03-ctrl-x-from-search.png
Sleep 1s
Type "y"
Sleep 3s
Screenshot {{SHOTS}}/04-two-killed.png
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
Screenshot {{SHOTS}}/05-archive.png
Sleep 1s
Type "D"
Sleep 4s
Screenshot {{SHOTS}}/06-restored-all.png
Sleep 1s
Type ":"
Sleep 1s
Type "archived view"
Sleep 1s
Enter
Sleep 3s
Screenshot {{SHOTS}}/07-back-on-the-board.png
Sleep 1s
