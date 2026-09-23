# Focus: open a session, give its pane the whole screen, type a multi-line
# instruction into the agent, watch it answer, scroll back through the reply,
# and leave with ctrl+q.
#
#   tools/demo/record.sh tools/demo/focus.tape
#
# record.sh has launched the board, hidden, on a scratch home. Every session
# runs fake-agent.sh, so nothing here reaches a model. ctrl+b, ctrl+u and
# ctrl+e are the demo's aliases for alt+\, alt+pgup and alt+end, which vhs
# cannot send (see record.sh).

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

# Seed two agents off camera.
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
Sleep 8s
Show
Sleep 1s

# enter focuses the session: keys now go to the agent.
Enter
Sleep 1500ms

# alt+\ puts the list away and gives the pane the full width.
Ctrl+b
Sleep 1500ms

# A trailing backslash continues the message onto the next line.
Type `add a regression test for two sign-ins at once \`
Enter
Type `then run it 20 times in a loop \`
Enter
Type `and tell me if it ever flakes`
Sleep 600ms
Enter
Sleep 14s

# alt+pgup scrolls the pane back through the reply; alt+end is the live bottom.
Ctrl+u
Sleep 2500ms
Ctrl+e
Sleep 1500ms

# alt+\ brings the list back, and ctrl+q returns to it.
Ctrl+b
Sleep 1s
Ctrl+q
Sleep 3s
