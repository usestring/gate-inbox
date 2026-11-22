# Run with --no-seed --foreign last-prompt --cols 140 --rows 40. The one row
# is a claude pane whose newest prompt runs past 500 characters; triage
# focuses it on its own, and the strip under the input box shows that prompt
# wrapped and cut with an ellipsis.
Sleep 12s
Screenshot {{SHOTS}}/01-last-prompt-under-pane.png
Sleep 2s
