# Taking over adopted panes: two panes the manager did not start, one idle and
# one finished, taken over without a question. Run with
#   --no-seed --foreign idle --foreign finished
# so the board is those two panes and nothing else.

# The first refresh after the welcome screen finds the adopted rows. The idle
# pane is ended in its window and comes back as gi_ under the same row; the
# finished one is owed until it is idle.
Sleep 5s
Screenshot {{SHOTS}}/01-idle-one-taken.png
Sleep 3s

# Entering the finished session acknowledges it, and leaving it puts the row
# at idle -- which the next pass takes.
Alt+f
Sleep 2s
Ctrl+q
Sleep 2500ms
Screenshot {{SHOTS}}/02-finished-one-followed.png
Sleep 1s
