# Moving one live session onto another subscription: the a key's card, and
# the row coming back on the new account. The capture's own config answers
# the account commands, so no secret store is reached.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/switch-account.tape

Screenshot {{SHOTS}}/01-board.png
Sleep 1s

# The cursor opens on the last seeded row; the first is the live claude
# session on its own login.
Up
Sleep 600ms
Up
Sleep 900ms
Type "a"
Sleep 2500ms
Screenshot {{SHOTS}}/02-switch-account-card.png
Sleep 1s

# One step right: the first pooled account.
Right
Sleep 1500ms
Screenshot {{SHOTS}}/03-picked-alice1.png
Sleep 1s

# Enter restarts it on the conversation it holds, with the new token.
Enter
Sleep 5s
Screenshot {{SHOTS}}/04-row-back-as-alice1.png
Sleep 2s
