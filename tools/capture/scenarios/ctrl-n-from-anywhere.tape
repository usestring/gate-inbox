# ctrl+n starts a new session from inside a box: settings closes and the
# new-session form opens, then from a focused session the CLI box opens.

Type "s"
Sleep 2s
Screenshot {{SHOTS}}/01-settings-open.png
Ctrl+N
Sleep 2s
Screenshot {{SHOTS}}/02-form-from-settings.png
Escape
Sleep 1s
Enter
Sleep 2s
Ctrl+N
Sleep 2s
Screenshot {{SHOTS}}/03-cli-box-from-focus.png
Sleep 1s
