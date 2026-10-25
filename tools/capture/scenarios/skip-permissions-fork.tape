# The fork card's two permission toggles, opened on the source's settings.
# skip-permissions.tape has the new-session card, skip-permissions-migrate.tape
# the migrate card.
#
# A fork needs a captured conversation id, which the seeded rows lack and an
# adopted foreign pane has:
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/skip-permissions-fork.tape \
#     --no-seed --foreign idle

Sleep 3s
# The cursor opens on the group row.
Down
Sleep 500ms
Type ":"
Sleep 1s
Type "fork"
Sleep 1s
Enter
Sleep 2s
Screenshot {{SHOTS}}/01-fork-card.png
Sleep 1s

# The session's own toggle, then the default for the sessions it spawns.
Down
Sleep 500ms
Space
Sleep 1s
Down
Sleep 500ms
Right
Sleep 1s
Screenshot {{SHOTS}}/02-fork-toggles-on.png
Sleep 1s
Escape
Sleep 1s
