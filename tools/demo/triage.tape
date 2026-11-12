# Triage, the README's opening recording: four agents at different priorities,
# two of them stopped on a question. ctrl+i turns the group into one queue and
# opens its head; every answer hands that session back to its agent and the
# queue advances to the next on its own. ctrl+v is the demo's alias for
# ctrl+i, which vhs sends as tab (see record.sh).
#
#   tools/demo/record.sh tools/demo/triage.tape
#
# record.sh has launched the board, hidden, on a scratch home. Every session
# runs fake-agent.sh, so nothing here reaches a model.

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

# Seed four agents off camera. ctrl+p in a focused session steps its priority
# from none: one press is urgent, two high, three medium.
Ctrl+n
Sleep 2500ms
Type "upgrade dependencies"
Enter
Sleep 1500ms
Ctrl+p
Sleep 800ms
Ctrl+q
Sleep 1500ms
Ctrl+n
Sleep 2500ms
Type "update the install steps in the README"
Enter
Sleep 1500ms
Ctrl+p
Sleep 500ms
Ctrl+p
Sleep 800ms
Ctrl+q
Sleep 1500ms
Ctrl+n
Sleep 2500ms
Type "ABC-123 add CSV export to billing"
Enter
Sleep 1500ms
Ctrl+p
Sleep 500ms
Ctrl+p
Sleep 500ms
Ctrl+p
Sleep 800ms
Ctrl+q
Sleep 1500ms
Ctrl+n
Sleep 2500ms
Type "fix the flaky login test"
Enter
Sleep 2s
Ctrl+q
Sleep 10s
Show
Sleep 1200ms

# ctrl+i turns the group into one queue: questions first, then by priority.
# It opens the head of the queue, the urgent question.
Ctrl+v
Sleep 3500ms

# A number answers the dialog, and the queue advances to the next question.
Type "1"
Sleep 3500ms

# Arrows and Enter answer it the same way; next is the high-priority finish.
Down
Sleep 800ms
Enter
Sleep 3500ms

# A typed reply is an answer too, and the queue moves on again.
Type "thanks, open a draft PR"
Sleep 600ms
Enter
Sleep 4s

# ctrl+\ stops triage and returns to the list.
Ctrl+\
Sleep 3s
