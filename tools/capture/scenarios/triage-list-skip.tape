# Skip from the list: "." in triage mutes the row under the cursor and enters
# the next session needing input -- the drain's one skip key, on the list and
# in the pane alike.

# Triage on: the head of the queue focused, the footer naming skip.
Type "i"
Sleep 4s
Screenshot {{SHOTS}}/01-triage-head-focused.png
Sleep 1s

# Back to the list: the head stays muted and the legend offers skip.
Ctrl+\
Sleep 2s
Screenshot {{SHOTS}}/02-list-with-skip.png
Sleep 1s

# Unmute the head, then skip it: the drain moves into the next session.
Type "."
Sleep 1500ms
Type "."
Sleep 3s
Screenshot {{SHOTS}}/03-list-skip-enters-next.png
Sleep 1s
