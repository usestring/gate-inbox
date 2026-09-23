# The board's default account: the settings row that names whose
# subscription every new session runs on, and the chip the row wears
# afterwards. The capture's own config answers the account commands, so no
# secret store is reached.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/session-account.tape

Screenshot {{SHOTS}}/01-board.png
Sleep 1s

# Settings opens on "default tool"; the account row is the one under it.
Type "s"
Sleep 2s
Down
Sleep 1s
Screenshot {{SHOTS}}/02-default-account-own-login.png
Sleep 1s

# Cycled to the first pooled account.
Right
Sleep 1500ms
Screenshot {{SHOTS}}/03-default-account-alice1.png
Sleep 1s

# Escape saves the card and closes it.
Escape
Sleep 2s

# A plain new session, nothing named: it runs on the default.
Ctrl+n
Sleep 2s
Enter
Sleep 4s

# Back on the list (the spawn lands in the pane, and ctrl+q is the way out
# of it): the row's chip says which CLI, and as whom.
Ctrl+q
Sleep 2s
Screenshot {{SHOTS}}/04-row-says-as-alice1.png
Sleep 2s
