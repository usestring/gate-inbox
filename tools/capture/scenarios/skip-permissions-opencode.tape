# The new-session card's permission toggle on opencode, which has no
# skip_permissions_flag: its skip rides the generated OPENCODE_CONFIG, so the
# card offers it with no "(no flag on opencode)" note and the row comes up
# marked. skip-permissions.tape has the same card on codex.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/skip-permissions-opencode.tape

Sleep 3s
Ctrl+n
Sleep 2s
Type "opencode"
Sleep 1s

# Model, then the session's own toggle.
Down
Sleep 500ms
Down
Sleep 500ms
Space
Sleep 1s
Screenshot {{SHOTS}}/01-opencode-perms-on.png
Sleep 1s
Down
Sleep 500ms
Down
Sleep 500ms
Type "opencode-no-prompts"
Sleep 1s

Enter
Sleep 4s

# Back on the list: the new row carries the skip-permissions mark.
Ctrl+q
Sleep 2s
Screenshot {{SHOTS}}/02-row-marked.png
Sleep 2s
