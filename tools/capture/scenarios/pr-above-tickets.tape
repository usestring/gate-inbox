# Pull requests above tickets. The parked demo agent echoes the two tickets
# before the pull request, so the order the rail draws is the sort's and not
# the order the references were mentioned in.

Enter
Sleep 1s
Type "ABC-135518"
Enter
Type "ABC-141768"
Enter
Type "https://github.com/example-org/sample-repo/pull/1978"
Enter
Sleep 1s
Ctrl+q
Sleep 1s

# W lifts the rail's cap and re-reads github and linear, so the rows are on
# screen and current without waiting for the ticker.
Type "W"
Sleep 7s
Screenshot {{SHOTS}}/01-pr-above-tickets.png
Sleep 1s
