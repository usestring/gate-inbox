# The key map under H, searched down to the two rows whose descriptions used
# to overflow the default card: the n row and the gate row. Each still shows
# the row on one line, with no ellipsis.

Type "H"
Sleep 2s
Screenshot {{SHOTS}}/01-key-map-under-H.png
Sleep 1s

Type "/"
Sleep 600ms
Type "cursor's group"
Sleep 1500ms
Screenshot {{SHOTS}}/02-new-session-row.png
Sleep 1s

Escape
Sleep 600ms
Type "/"
Sleep 600ms
Type "drain that queue"
Sleep 1500ms
Screenshot {{SHOTS}}/03-gate-row.png
Sleep 1s

Escape
Sleep 600ms
Type "H"
Sleep 1s
