# Sessions that died while the board was closed: the board comes back after a
# reboot straight to the list, marks the lost rows, and V brings every dead
# one back. Run with the default seed.

# What a reboot amounts to here: the panes go, the rows stay.
Type "q"
Sleep 2s
Type "{{TMUX}} kill-server"
Enter
Sleep 2s

Type "{{LAUNCH}}"
Enter
Sleep 14s
Screenshot {{SHOTS}}/01-marked-in-the-list.png
Sleep 1s

Type "V"
Sleep 8s
Screenshot {{SHOTS}}/02-revived.png
Sleep 2s
