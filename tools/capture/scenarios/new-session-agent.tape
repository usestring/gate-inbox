# The n key before and after the setting that retires its question.
#
# Counting rows is stable here where it is not on the list: the settings card
# is a fixed field order that no poll rebuilds, so one Down is always the row
# under "default tool".

Screenshot {{SHOTS}}/01-board.png
Sleep 1s

# What n does today: a box asking which agent, opened on the last CLI used.
Type "n"
Sleep 2s
Screenshot {{SHOTS}}/02-n-asks-which-agent.png
Sleep 1s
Escape
Sleep 1s

# The new settings row, on its default -- ask, which is the box above.
Type "s"
Sleep 2s
Down
Sleep 1500ms
Screenshot {{SHOTS}}/03-new-session-agent-ask.png
Sleep 1s

# Cycled to last used: n stops asking and starts the CLI the last spawn ran.
Right
Sleep 1500ms
Screenshot {{SHOTS}}/04-new-session-agent-last-used.png
Sleep 1s

# Escape saves the card and closes it.
Escape
Sleep 2s

# And now n is one key: no box, straight into a new session.
Type "n"
Sleep 3s
Screenshot {{SHOTS}}/05-n-starts-it-outright.png
Sleep 2s
