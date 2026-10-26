# The review queue is off until its Experimental row is on: the ingest key
# refuses with a pointer to the card, and the card toggles it on.
Sleep 8s
Type "P"
Sleep 1500ms
Screenshot {{SHOTS}}/01-ingest-refused.png
Sleep 1s
Type "s"
Sleep 2s
Up
Up
Up
Up
Up
Up
Up
Sleep 1s
Enter
Sleep 1s
Up
Sleep 1s
Screenshot {{SHOTS}}/02-review-queue-off.png
Right
Sleep 1s
Screenshot {{SHOTS}}/03-review-queue-on.png
Sleep 1s
Escape
Sleep 1s
Escape
Sleep 2s
