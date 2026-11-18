# Board keys are chords: the key map and quick actions name them, a bare
# letter on the list does nothing, and from inside a focused session the
# board's own chords leave the pane and run there (search, quick actions).
# ctrl+i is not recorded: the recorder's terminal sends it as tab. Nor are the
# ctrl+alt chords: vhs has no way to type one.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/ctrl-chord-keys.tape

Sleep 3s
Type "x"
Sleep 1500ms
Screenshot {{SHOTS}}/01-bare-letter-does-nothing.png
Sleep 1s

Ctrl+h
Sleep 2s
Screenshot {{SHOTS}}/02-key-map-chords.png
Sleep 1s
Escape
Sleep 1500ms

Ctrl+k
Sleep 2s
Screenshot {{SHOTS}}/03-quick-actions-chords.png
Sleep 1s
Escape
Sleep 1500ms

# Up to the claude row: the opencode one opens on a banner naming its log.
Type "k"
Sleep 500ms
Type "k"
Sleep 500ms
Enter
Sleep 2s
Screenshot {{SHOTS}}/04-focused-session.png
Sleep 1s

Ctrl+f
Sleep 2s
Screenshot {{SHOTS}}/05-search-from-focus.png
Sleep 1s
Escape
Sleep 1500ms

Enter
Sleep 2s
Ctrl+k
Sleep 2s
Screenshot {{SHOTS}}/06-quick-actions-from-focus.png
Sleep 1s
Escape
Sleep 1s
