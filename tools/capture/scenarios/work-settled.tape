# A settled artifact leaving the board. Run with
#   --config tools/capture/scenarios/work-settled.toml
# which shortens [work] settle_after to eight seconds so the recording can wait
# it out. The parked demo agent echoes what is typed at it, which is how the
# merged pull request and the ticket reach the pane the tracker reads.

Enter
Sleep 1s
Type "https://github.com/example-org/sample-repo/pull/1579"
Enter
Type "ABC-135518"
Enter
Sleep 1s
Ctrl+q
Sleep 1s

# W lifts the rail's cap and re-reads github and linear, so the rows are on
# screen and current without waiting for the ticker.
Type "W"
Sleep 7s
Screenshot {{SHOTS}}/01-work-merged-just-seen.png

# Past the settle window the merged pull request is gone and the started
# ticket is not: on screen while it was over is what retires a row.
Sleep 9s
Type "W"
Sleep 1s
Type "W"
Sleep 2s
Screenshot {{SHOTS}}/02-rail-settled-gone.png
Sleep 1s
