# Mute a session out of triage until unmuted.
#
# The driver seeds three sessions (claude reads waiting, codex and opencode
# rest). Search for the claude row so the cursor lands on the waiting session
# without counting rows; M mutes it and the row's state reads "muted", M again
# puts it back, and a final mute shows the triage queue without it.

Type "/"
Sleep 1s
Type "claude"
Sleep 1500ms
Enter
Sleep 1500ms
Screenshot {{SHOTS}}/01-waiting-session-selected.png
Sleep 1s

Type "M"
Sleep 800ms
Screenshot {{SHOTS}}/02-row-reads-muted.png
Sleep 1s

Type "M"
Sleep 800ms
Screenshot {{SHOTS}}/03-unmuted-back-on-the-list.png
Sleep 1s

# Mute it again and show triage leaving it out entirely.
Type "M"
Sleep 800ms

Type "/"
Sleep 800ms
Ctrl+u
Sleep 800ms
Enter
Sleep 1500ms

Type "i"
Sleep 2500ms
Screenshot {{SHOTS}}/04-muted-session-out-of-triage.png
Sleep 1s
