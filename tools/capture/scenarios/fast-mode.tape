# The fast toggle on the new-session, migrate and fork cards, and the ► mark a
# row wears once it runs in fast mode. Migrate and fork need a session with a
# conversation, which an adopted foreign pane has:
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/fast-mode.tape \
#     --foreign conversation

Sleep 8s
Ctrl+n
Sleep 2s
Type "codex"
Sleep 1s

# Model, perms, children, then fast.
Down
Sleep 300ms
Down
Sleep 300ms
Down
Sleep 300ms
Down
Sleep 500ms
Space
Sleep 1s
Screenshot {{SHOTS}}/01-form-fast-on.png
Sleep 1s
Down
Sleep 500ms
Type "codex-fast"
Sleep 1s
Enter
Sleep 4s

# Back on the list: the new row carries the fast-mode mark.
Ctrl+q
Sleep 2s
Screenshot {{SHOTS}}/02-row-marked.png
Sleep 1s

# The migrate card opens on the source's setting (off); the last row turns
# fast mode on for the moved session.
Type "/"
Sleep 1s
Type "conversation "
Sleep 2s
Enter
Sleep 2s
Type ":"
Sleep 1500ms
Type "migrate it to another CLI"
Sleep 1500ms
Enter
Sleep 2s
Tab
Sleep 1s
Up
Sleep 500ms
Space
Sleep 1s
Screenshot {{SHOTS}}/03-migrate-fast-on.png
Sleep 1s
Escape
Sleep 1s

# The fork card carries the same toggle.
Type ":"
Sleep 1500ms
Type "fork"
Sleep 1500ms
Enter
Sleep 2s
Up
Sleep 500ms
Right
Sleep 1s
Screenshot {{SHOTS}}/04-fork-fast-on.png
Sleep 1s
Escape
Sleep 1s
