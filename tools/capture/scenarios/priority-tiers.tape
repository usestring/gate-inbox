# Priority tiers: p cycling one session through the scale, and the triage
# queue those tiers produce. The driver has already seeded the sessions and
# left the cursor on the list.

Screenshot {{SHOTS}}/01-board.png
Sleep 1s

Down
Sleep 600ms
Down
Sleep 900ms

# One press per tier, so the glyph beside the name is caught at each one:
# urgent, high, medium, low, then back to none.
Type "p"
Sleep 1500ms
Screenshot {{SHOTS}}/02-urgent.png
Sleep 1s

Type "p"
Sleep 1500ms
Screenshot {{SHOTS}}/03-high.png
Sleep 1s

Type "p"
Sleep 1500ms
Screenshot {{SHOTS}}/04-medium.png
Sleep 1s

Type "p"
Sleep 1500ms
Screenshot {{SHOTS}}/05-low.png
Sleep 1s

# Back to urgent, and give a second session a tier of its own, so the queue
# below has something to order.
Type "p"
Sleep 1200ms
Type "p"
Sleep 1500ms
Up
Sleep 800ms
Type "p"
Sleep 1200ms
Type "p"
Sleep 1500ms
Screenshot {{SHOTS}}/06-two-tiers.png
Sleep 1s

# The queue the tiers produce: urgent heads the sessions that need a person,
# high follows, and the untiered ones keep their usual order behind them.
Type "i"
Sleep 2500ms
Screenshot {{SHOTS}}/07-triage-queue.png
Sleep 1500ms
Ctrl+\
Sleep 1s
