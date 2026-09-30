# Pull requests in mixed states, typed merged and closed first so the order
# the rail draws is the rank's and not the order they were mentioned in:
# blocked (red CI, a conflict) above one awaiting review, above the merged
# one, above the closed one. Real pull requests on this repository, because
# the board asks gh; each was picked for the state it was in when recorded.

Enter
Sleep 1s
Type "https://github.com/usestring/gate-inbox/pull/152"
Enter
Type "https://github.com/usestring/gate-inbox/pull/133"
Enter
Type "https://github.com/usestring/gate-inbox/pull/159"
Enter
Type "https://github.com/usestring/gate-inbox/pull/155"
Enter
Type "https://github.com/usestring/gate-inbox/pull/151"
Enter
Sleep 1s
Ctrl+q
Sleep 1s

# W lifts the rail's cap and re-reads github, so every row is on screen and
# current without waiting for the ticker.
Type "W"
Sleep 8s
Screenshot {{SHOTS}}/01-open-above-merged-above-closed.png
Sleep 1s
