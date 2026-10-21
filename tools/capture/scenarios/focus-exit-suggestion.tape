# Right leaves an empty claude prompt that is showing claude's suggested next
# prompt. The suggestion is drawn faint after the caret, so the stripped pane
# reads it as typed text; the board now reads the styling and lets Right go
# back to the list.
#
# Run with the claude transcript that ends on that composer:
#   tools/capture/capture.sh --scenario tools/capture/scenarios/focus-exit-suggestion.tape \
#     --transcripts resting-suggestion,waiting,working
#
# The transcript's last line prints the faint hint, then a carriage return and
# a two-cell move put the caret back at the head of the empty composer, where
# claude parks it. The row is found by the transcript's text, as in
# focus-exit-arrow.tape.

Type "/"
Sleep 1s
Type "suggested prompt "
Sleep 1500ms
Enter
Sleep 1500ms
Enter
Sleep 2s
Screenshot {{SHOTS}}/01-focused-on-the-suggestion.png
Sleep 1s

Right
Sleep 1200ms
Screenshot {{SHOTS}}/02-right-returned-to-the-list.png
Sleep 1s
