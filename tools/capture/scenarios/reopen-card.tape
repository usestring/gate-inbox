# The reopen card: the board comes back after a reboot to find its own
# sessions gone and an agent somebody started by hand in the meantime, and
# asks about both on one screen. Run with the default seed.

# What a reboot amounts to here: the panes go, the rows stay.
Type "q"
Sleep 2s
Type "{{TMUX}} kill-server"
Enter
Sleep 2s

# An agent started by hand in tmux while the board was closed.
Type "{{TMUX}} new-session -d -s byhand -x 120 -y 40 tools/capture/demo-agent.sh idle"
Enter
Sleep 3s

Type "{{LAUNCH}}"
Enter
Sleep 14s
Screenshot {{SHOTS}}/01-reopen-card.png
Sleep 1s

# The summary scrolls to what relaunching costs.
Type "jjjjjj"
Sleep 1s
Screenshot {{SHOTS}}/01b-reopen-card-scrolled.png
Sleep 1s

# Per session and per pane.
Type "c"
Sleep 1500ms
Screenshot {{SHOTS}}/02-picker.png
Sleep 1s
Escape
Sleep 1s

# The panes' answer moves with the arrows.
Right
Sleep 1s
Screenshot {{SHOTS}}/03-relaunch-chosen.png
Sleep 1s
Left
Sleep 1s

# Never ask again: apply the answer and store it, with the notice that says
# where to turn it back on.
Type "N"
Sleep 800ms
Screenshot {{SHOTS}}/04-never-ask-notice.png
Sleep 1s

# The two settings the card writes.
Type "s"
Sleep 1500ms
Type "jjjjjjjjjjjjjjjj"
Sleep 1500ms
Screenshot {{SHOTS}}/05-settings.png
Sleep 1s
Escape
Sleep 1s
