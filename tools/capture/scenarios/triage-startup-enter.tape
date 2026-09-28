# Restarting the board with triage on drops straight back into the head of
# the queue, the way turning triage on does, instead of waiting on the list
# for an enter press.

# Triage on: the head of the queue focused, like every drain starts.
Type "i"
Sleep 4s
Screenshot {{SHOTS}}/01-triage-head-focused.png
Sleep 1s

# Back to the list, then quit: the mode persists across the restart.
Ctrl+\
Sleep 2s
Type "q"
Sleep 2s

# The same scratch home comes back up with triage still on, and the first
# poll enters the head again.
Type "{{LAUNCH}}"
Enter
Sleep 15s
Screenshot {{SHOTS}}/02-restart-enters-head.png
Sleep 1s
