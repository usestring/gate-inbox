# Exercise Ctrl+S for Settings and Ctrl+Z for Snippets on the list and in a
# focused session. A bare "t" sends the fixture's continue snippet.
#
#   tools/capture/capture.sh --no-seed --transcripts resting --scenario tools/capture/scenarios/hotkey-menu.tape

Ctrl+n
Sleep 1500ms
Type "claude"
Sleep 800ms
Enter
Sleep 6s
Ctrl+\
Sleep 2s

Ctrl+S
Sleep 1500ms
Screenshot {{SHOTS}}/01-settings-from-list.png
Sleep 1s
Escape
Sleep 1s

Ctrl+Z
Sleep 1500ms
Screenshot {{SHOTS}}/02-snippets-from-list.png
Sleep 1s

Type "t"
Sleep 800ms
Screenshot {{SHOTS}}/03-sent-from-the-list.png
Sleep 1s

Escape
Sleep 1s

Enter
Sleep 2s
Ctrl+Z
Sleep 1500ms
Screenshot {{SHOTS}}/04-snippets-from-focus.png
Sleep 1s

Type "t"
Sleep 800ms
Screenshot {{SHOTS}}/05-sent-from-focus.png
Sleep 1s

Escape
Sleep 500ms
Ctrl+S
Sleep 1500ms
Screenshot {{SHOTS}}/06-settings-from-focus.png
Sleep 1s
