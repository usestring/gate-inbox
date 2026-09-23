# A child's work on its parent's row. The parked demo agent echoes a ticket,
# then T opens a terminal under it that prints a pull request; the parent's
# rail carries that pull request too, tagged with the child it came from.

Enter
Sleep 1s
Type "ABC-135518"
Enter
Sleep 1s
Ctrl+q
Sleep 1s

# A new terminal takes the focus, so ctrl+q hands the keys back to the list.
Type "T"
Sleep 5s
Type "echo https://github.com/example-org/sample-repo/pull/1980"
Enter
Sleep 1s
Ctrl+q
Sleep 2s

# Up to the parent. W lifts the cap and re-reads github and linear, so the
# rows are current without waiting for the ticker.
Up
Sleep 1500ms
Type "W"
Sleep 7s
Screenshot {{SHOTS}}/01-parent-carries-child-work.png
Sleep 1s
