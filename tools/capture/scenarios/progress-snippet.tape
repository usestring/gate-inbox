# The progress-summary snippet on the § menu key. The first-run snippets file
# carries it, so the key map names it -- as a bare §, the key the hotkey
# menu reads.
#
# Only the surface is recorded. The ui tests cover delivery: the menu reads
# bare keys, so there is no chord encoding for vhs to miss.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/progress-snippet.tape

Sleep 8s

Type "H"
Sleep 2s
Type "/"
Sleep 1s
Type "progress"
Sleep 1s
Enter
Sleep 1500ms
Screenshot {{SHOTS}}/01-key-map-names-section-menu-key.png
Sleep 1s
