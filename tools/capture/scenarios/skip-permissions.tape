# The new-session card's two permission toggles, and the mark a row wears
# once it runs with its prompts off. skip-permissions-migrate.tape has the
# migrate card. Codex here, since both claude and codex
# carry a skip_permissions_flag.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/skip-permissions.tape

Sleep 3s
Ctrl+n
Sleep 2s
Type "codex"
Sleep 1s
Screenshot {{SHOTS}}/01-form-toggles-off.png
Sleep 1s

# Model, then the session's own toggle.
Down
Sleep 500ms
Down
Sleep 500ms
Space
Sleep 1s
# Then the default for the sessions it spawns.
Down
Sleep 500ms
Space
Sleep 1s
Screenshot {{SHOTS}}/02-form-toggles-on.png
Sleep 1s
Down
Sleep 500ms
Type "codex-no-prompts"
Sleep 1s

Enter
Sleep 4s

# Back on the list: the new row carries the skip-permissions mark.
Ctrl+q
Sleep 2s
Screenshot {{SHOTS}}/03-row-marked.png
Sleep 2s

