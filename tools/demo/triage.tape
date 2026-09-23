# The gate: several agents at once, two of them stopped on a question. G
# drains the triage queue full-width, and every answer hands that session back
# to its agent and brings up the next one on its own.
#
#   tools/demo/record.sh tools/demo/triage.tape
#
# record.sh has launched the board, hidden, on a scratch home. Every session
# runs fake-agent.sh, so nothing here reaches a model. ctrl+t is the demo's
# alias for f2, which vhs cannot send (see record.sh).

# Put the cursor on the project group, by search: esc drops the filter and
# leaves the cursor on the row.
Sleep 5s
Type "/"
Sleep 800ms
Type "demo"
Sleep 1s
Enter
Sleep 1s
Escape
Sleep 1s

# Seed four agents off camera.
Type "n"
Sleep 2500ms
Type "ABC-123 add CSV export to billing"
Enter
Sleep 2s
Ctrl+q
Sleep 1500ms
Type "n"
Sleep 2500ms
Type "update the install steps in the README"
Enter
Sleep 2s
Ctrl+q
Sleep 1500ms
Type "n"
Sleep 2500ms
Type "fix the flaky login test"
Enter
Sleep 2s
Ctrl+q
Sleep 1500ms
Type "n"
Sleep 2500ms
Type "upgrade dependencies"
Enter
Sleep 2s
Ctrl+q
Sleep 10s
Show
Sleep 1500ms

# G arms the gate on the session that has waited longest, full width.
Type "G"
Sleep 2500ms

# f2 switches from the conversation to the terminal, where the dialog is, and
# stays there as the queue advances.
Ctrl+t
Sleep 2000ms

# A number answers the dialog; the next waiting session comes up on its own.
Type "1"
Sleep 3500ms

# Arrows and Enter answer it the same way.
Down
Sleep 800ms
Enter
Sleep 3500ms

# A finished session is next; a typed reply is an answer too.
Type "thanks, open a draft PR"
Sleep 600ms
Enter
Sleep 3500ms

# ctrl+\ stops the gate and the list comes back as it was.
Ctrl+\
Sleep 3s
