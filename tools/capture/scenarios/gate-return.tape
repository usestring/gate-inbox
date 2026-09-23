# Leaving the gate has to put back a list the operator can read, and the case
# that broke was the one where the row the drain ended on is not a row the
# restored list has: a session inside a group they had closed. The cursor comes
# back on the group, and the pane the drain was showing used to stay drawn
# under it -- a preview belonging to a session nobody is on.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/gate-return.tape
#
# The seeded trio is ungrouped, so the scenario builds the group it needs: the
# waiting session is the first row under root, and it is the one the gate picks.

# Let the seeded transcripts play out, so the status the queue orders on is the
# one on screen when the gate is armed.
Sleep 12s

# A group to close over the session. Creating one leaves the cursor on it, at
# the bottom of the list.
Type "g"
Sleep 1500ms
Type "work"
Sleep 800ms
Enter
Sleep 2500ms

# Back up to the waiting session. One arrow per sleep: a burst of them arrives
# as one run of escape bytes and the board reads none of it.
Up
Sleep 700ms
Up
Sleep 700ms
Up
Sleep 700ms
Screenshot {{SHOTS}}/01-waiting-session.png
Sleep 1s

# Move it into the group -- root first in the picker, the new group under it.
Type "m"
Sleep 1500ms
Down
Sleep 800ms
Enter
Sleep 2500ms
Screenshot {{SHOTS}}/02-session-in-the-group.png
Sleep 1s

# Up onto the group row and close it over the session: enter on a group folds
# it. The cursor stays on the group, which is the default view the gate has to
# give back -- and the row the restored list will put it on.
Up
Sleep 800ms
Enter
Sleep 1500ms
Screenshot {{SHOTS}}/03-group-closed.png
Sleep 1s

# G arms the drain: the queue flattens the groups away, so the session inside
# the closed one is found and entered full width.
Type "G"
Sleep 4s
Screenshot {{SHOTS}}/04-gate-draining.png
Sleep 1s

# ctrl+\ stops the gate. The group comes back closed, the cursor lands on it,
# and the preview goes with the session rather than staying behind it.
Ctrl+\
Sleep 3s
Screenshot {{SHOTS}}/05-default-view-restored.png
Sleep 2s
