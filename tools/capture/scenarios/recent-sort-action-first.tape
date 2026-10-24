# The recent sort keeps the rows that need you first: waiting and finished
# sessions lead by recency, and everything else follows by recency. The
# working row is typed into last, so it carries the newest interaction, and
# still sorts below the waiting and finished rows once recent is on.
#
# The toggle ships with no key, so the scenario binds one in keys.toml and
# restarts the board to read it.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/recent-sort-action-first.tape

Sleep 2s
Screenshot {{SHOTS}}/01-board.png
Sleep 1s

Type "/"
Sleep 1s
Type "demo-project-2"
Sleep 1500ms
Enter
Sleep 1500ms
Enter
Sleep 2500ms
Type "x"
Sleep 1500ms
Ctrl+q
Sleep 1500ms

Type "q"
Sleep 2s
Type `printf '[list]\nrecent_sort = ["ctrl+t"]\n' > {{HOME}}/keys.toml`
Enter
Sleep 1s
Type "{{LAUNCH}}"
Enter
Sleep 4s

Ctrl+t
Sleep 2500ms
Screenshot {{SHOTS}}/02-recent-action-first.png
Sleep 1s
