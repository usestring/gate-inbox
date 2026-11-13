# The row detail setting: rows spell their state and CLI out by default, and
# on marks drop the state word and write each CLI as its coloured letters.
#
# The settings card is a fixed field order, so ten Downs from "default tool"
# is always the row detail row.

Sleep 3s
Screenshot {{SHOTS}}/01-words.png
Sleep 1s

Type "s"
Sleep 2s
Down
Down
Down
Down
Down
Down
Down
Down
Down
Down
Sleep 1500ms
Right
Sleep 1500ms
Screenshot {{SHOTS}}/02-row-detail-marks.png
Sleep 1s

# Escape saves the card and closes it.
Escape
Sleep 3s
Screenshot {{SHOTS}}/03-marks.png
Sleep 2s
