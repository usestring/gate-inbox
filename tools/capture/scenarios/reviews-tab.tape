# With the review queue on, startup stays on Sessions even with triage saved.
# Ctrl+] switches tabs and is the shortcut shown in the tab bar and key map.
Type "q"
Sleep 2s
Type `sqlite3 {{HOME}}/state.db "INSERT OR REPLACE INTO settings(key, value) VALUES ('triage_mode', 'on');"`
Enter
Type "{{LAUNCH}}"
Enter
Sleep 8s
Screenshot {{SHOTS}}/01-sessions-tab.png
Ctrl+]
Sleep 1500ms
Screenshot {{SHOTS}}/02-reviews-tab.png
Down
Sleep 1s
Screenshot {{SHOTS}}/03-second-ask.png
Down
Sleep 1s
Ctrl+]
Sleep 1500ms
Screenshot {{SHOTS}}/04-back-to-sessions.png
Sleep 1s
Type "h"
Sleep 1500ms
Type "Sessions and Reviews"
Sleep 1s
Screenshot {{SHOTS}}/05-tab-switch-help.png
Sleep 1s
