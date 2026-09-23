# Taking over adopted panes: the startup offer over two panes the manager did
# not start, one idle and one finished. Run with
#   --no-seed --foreign idle --foreign finished
# so the board is those two panes and nothing else.

# The first refresh after the welcome screen finds the adopted rows and
# raises the offer: one goes now, one follows.
Sleep 4s
Screenshot {{SHOTS}}/01-offer.png
Sleep 1s

# Yes: the idle pane is ended in its window and comes back as gi_ under the
# same row; the finished one is owed.
Type "y"
Sleep 1500ms
Screenshot {{SHOTS}}/02-idle-one-taken.png
Sleep 3s

# Entering the finished session acknowledges it, and leaving it puts the row
# at idle -- which the next pass takes.
Alt+f
Sleep 2s
Ctrl+q
Sleep 2500ms
Screenshot {{SHOTS}}/03-finished-one-followed.png
Sleep 1s
