# The direct-chord change is visible in two surfaces: the toolbar, which now
# lists each lettered snippet under its option+shift chord, and the hotkey
# menu, which stays the full namespace.
#
# vhs cannot drive the chord itself: its key syntax accepts one modifier, and
# `Alt+Shift+c` is rejected ("Expected alt character"). The send is covered by
# the ui tests (TestDirectChordSendsFromTheList, TestDirectChordSendsFromAFocusedSession).
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/direct-snippet.tape

Sleep 8s

Type "/"
Sleep 1s
Type "claude "
Sleep 1500ms
Enter
Sleep 1500ms

Screenshot {{SHOTS}}/01-toolbar-lists-the-chords.png
Sleep 1s

Type " "
Sleep 1500ms
Screenshot {{SHOTS}}/02-hotkey-menu-stays-the-namespace.png
Sleep 1s

Escape
Sleep 500ms
Ctrl+q
Sleep 1s
