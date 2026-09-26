# The focused footer's wording: skip on the walk key and undo on ctrl+z, with
# the mouse gestures moved off the one-row footer into the key map.
#
# The driver seeds one session per installed CLI, so only the first,
# gate-inbox, is certain to exist. It is reached by name rather than by
# counting rows: the live poll reorders and unfolds the tree under a blind
# Down. The search's Enter leaves the cursor on the row; the next focuses it.
#
# {{SHOTS}} is replaced with this run's still directory. Every Screenshot needs
# a Sleep after it, or vhs ends the recording before the frame is written.

Type "/"
Sleep 1s
Type "gate-inbox "
Sleep 1500ms
Enter
Sleep 1500ms
Enter
Sleep 2s
Screenshot {{SHOTS}}/01-focused-footer-offers-skip.png
Sleep 1s

# A submission makes ctrl+z live, so the footer grows the undo pair.
Type "Run the final verification"
Sleep 500ms
Enter
Sleep 900ms
Screenshot {{SHOTS}}/02-focused-footer-offers-undo.png
Sleep 1s

Ctrl+z
Sleep 900ms
Screenshot {{SHOTS}}/03-undo-issued.png
Sleep 1s
