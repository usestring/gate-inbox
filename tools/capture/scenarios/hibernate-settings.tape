# "hibernate" and "keep awake at least" are rows in Settings. Hibernate
# steps the rest period from off through 30m to 3d; keep awake at least is
# the live-session floor below which the sweep stops nothing.
#
# vhs cannot type the ctrl+alt+s chord, so Settings opens from quick actions.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/hibernate-settings.tape

# The board opens focused on a pane: leave it first.
Sleep 6s
Ctrl+q
Sleep 1500ms
Ctrl+k
Sleep 1500ms
Type "settings"
Sleep 1500ms
Enter
Sleep 2s
Down 26
Sleep 1500ms
Screenshot {{SHOTS}}/01-hibernate-off.png
Sleep 1s
Right
Sleep 700ms
Right
Sleep 700ms
Right
Sleep 1500ms
Screenshot {{SHOTS}}/02-hibernate-2h.png
Down
Sleep 700ms
Right
Sleep 700ms
Right
Sleep 1500ms
Screenshot {{SHOTS}}/03-keep-awake-10.png
Sleep 1s
Escape
Sleep 1500ms
Ctrl+k
Sleep 1500ms
Type "settings"
Sleep 1500ms
Enter
Sleep 2s
Down 26
Sleep 1500ms
Screenshot {{SHOTS}}/04-hibernate-saved.png
Sleep 1s
Escape
Sleep 1s
