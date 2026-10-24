# The recent sort keeps the rows that need you first: waiting and finished
# sessions lead by recency, and everything else follows by recency. The
# working row is typed into last, so it carries the newest interaction, and
# still sorts below the waiting and finished rows once recent is on.
#
# The toggle ships with no key, so the run binds one:
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/recent-sort-action-first.tape \
#     --keys tools/capture/scenarios/recent-sort-action-first.keys.toml

Sleep 2s
Screenshot {{SHOTS}}/01-board.png
Sleep 1s

Up
Sleep 1500ms
Enter
Sleep 2500ms
Type "x"
Sleep 1500ms
Ctrl+q
Sleep 1500ms

Ctrl+t
Sleep 2500ms
Screenshot {{SHOTS}}/02-recent-action-first.png
Sleep 1s
