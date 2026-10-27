# The recent sort ranks sessions by the operator's last hand on them, not by
# what the agents did. The last row is opened and typed into, so it carries
# the newest interaction; toggling recent then lifts it to the top of its
# group, and toggling again puts the board back in its previous order.
#
# The toggle ships with no key, so it is reached through quick actions (:).
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/recent-sort.tape

Sleep 2s
Screenshot {{SHOTS}}/01-board.png
Sleep 1s

Enter
Sleep 2500ms
Type "x"
Sleep 1500ms
Ctrl+q
Sleep 1500ms

Type ":"
Sleep 1s
Type "recent sort"
Sleep 1s
Enter
Sleep 2500ms
Screenshot {{SHOTS}}/02-recent-first.png
Sleep 1s

Type ":"
Sleep 1s
Type "recent sort"
Sleep 1s
Enter
Sleep 2500ms
Screenshot {{SHOTS}}/03-previous-order.png
Sleep 1s
