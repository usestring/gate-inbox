# The core loop, from an empty board: spawn two agents, watch their status,
# open the one that is waiting, answer it, and let the queue hand over the
# next one.
#
#   tools/demo/record.sh tools/demo/core-loop.tape
#
# record.sh has launched the board, hidden, on a scratch home. Every session
# runs fake-agent.sh, so nothing here reaches a model.

# Let the board negotiate the keyboard protocol before the first key, then put
# the cursor on the project group so new sessions land there. Searching for it
# lands on the row whatever the list looks like; esc drops the filter and
# leaves the cursor where it is.
Sleep 5s
Type "/"
Sleep 800ms
Type "demo"
Sleep 1s
Enter
Sleep 1s
Escape
Sleep 1s
Show
Sleep 1s

# n starts an agent in the group under the cursor and focuses it; the task is
# typed straight into its prompt. ctrl+q comes back to the list.
Type "n"
Sleep 2500ms
Type "fix the flaky login test"
Sleep 600ms
Enter
Sleep 3s
Ctrl+q
Sleep 1500ms

Type "n"
Sleep 2500ms
Type "update the install steps in the README"
Sleep 600ms
Enter
Sleep 2500ms
Ctrl+q

# Both run side by side; the list shows each one's status as it moves:
# working, then finished for one and waiting on a question for the other.
Sleep 7s

# i turns the list into a queue, longest-blocked first, and opens its head.
Type "i"
Sleep 3s

# Answering the question hands the session back to its agent and opens the
# next one that needs a look.
Type "1"
Sleep 4s

# ctrl+\ stops triage and i goes back to the groups, both sessions settled.
Ctrl+\
Sleep 1500ms
Type "i"
Sleep 4s
