# A session on a dozen pull requests: the rail hangs five rows and a count, in
# the list and in triage, and W hangs the rest of them off the session where
# the count was. The parked demo agent echoes what is typed at it, which is how
# the references reach the pane the tracker reads.

Enter
Sleep 1s
Type "https://github.com/example-org/sample-repo/pull/1613"
Enter
Type "https://github.com/example-org/sample-repo/pull/1624"
Enter
Type "https://github.com/example-org/sample-repo/pull/1579"
Enter
Type "https://github.com/example-org/sample-repo/pull/1574"
Enter
Type "https://github.com/example-org/sample-repo/pull/1610"
Enter
Type "https://github.com/example-org/sample-repo/pull/1600"
Enter
Type "https://github.com/example-org/sample-repo/pull/1590"
Enter
Type "https://github.com/example-org/sample-repo/pull/1580"
Enter
Type "https://github.com/example-org/sample-repo/pull/1570"
Enter
Type "https://github.com/example-org/sample-repo/pull/1560"
Enter
Type "https://github.com/example-org/sample-repo/pull/1550"
Enter
Type "https://github.com/example-org/sample-repo/pull/1540"
Enter
Sleep 1s
Ctrl+q
Sleep 1s

# W re-reads github and linear on its way up, so a second W leaves the rail
# capped and current rather than waiting out the ticker.
Type "W"
Sleep 7s
Type "W"
Sleep 1s
Screenshot {{SHOTS}}/01-rail-capped-at-five.png
Sleep 1s

# Triage opens focused on the session that needs a person; ctrl+\ stops the
# walk and leaves a cursor on the queue, two steps down is the wide session,
# and "i" is back to groups.
Type "i"
Sleep 2s
Ctrl+\
Sleep 1s
Down
Sleep 400ms
Down
Sleep 1500ms
Screenshot {{SHOTS}}/02-triage-capped.png
Sleep 1s
Type "i"
Sleep 1500ms

# Back from triage the cursor is on the wide session; W puts the rows the
# count stood for back under it, on the rail itself.
Screenshot {{SHOTS}}/03-rail-count-line.png
Sleep 1s
Type "W"
Sleep 2s
Screenshot {{SHOTS}}/04-rail-shows-all.png
Sleep 1s
