# A first run on a machine where an agent is already running in tmux. Run
#   --no-seed --foreign idle
# The driver dismisses the welcome card on the way in, so the reopen card's
# panes section is up first; the welcome card is then reached again from the
# key map, the way an operator gets back to it later.
Sleep 4s
Screenshot {{SHOTS}}/01-first-run-outside-pane.png
Sleep 1s
Escape
Sleep 1500ms

# The key map names the way back to the welcome card.
Type "H"
Sleep 1500ms
Screenshot {{SHOTS}}/02-keymap-w-welcome.png
Sleep 1s
Type "w"
Sleep 1500ms
Screenshot {{SHOTS}}/03-welcome-top.png
Sleep 1s
Type "jjjjjjjjjjjj"
Sleep 1s
Screenshot {{SHOTS}}/04-welcome-five-keys.png
Sleep 1s
Type "G"
Sleep 1s
Screenshot {{SHOTS}}/05-welcome-choices.png
Sleep 1s

# n on the card starts the first session.
Type "n"
Sleep 2s
Screenshot {{SHOTS}}/06-first-session.png
Sleep 1s
Escape
Sleep 1s
