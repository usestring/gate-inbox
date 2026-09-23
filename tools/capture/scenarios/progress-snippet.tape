# The progress-summary snippet on alt+§, the one snippet key off the ctrl+alt
# chord. The first-run snippets file carries it, so the key map names it -- as
# alt+§, with no ^, because it has no ctrl.
#
# Only the surface is recorded. vhs cannot deliver alt+§: it sends the ESC and
# the § as two separate keys even inside one Type, so the board reads a bare §
# instead. TestSectionKeyIsReachable and the ui tests cover delivery.
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
Screenshot {{SHOTS}}/01-key-map-names-alt-section.png
Sleep 1s
