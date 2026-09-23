# Open the snippets file from Settings. Recorded with nothing configured --
# no editor in config.toml, no $EDITOR -- so Enter proves the terminal-editor
# fallback hands over the screen and comes back to the same Settings card.

Type "s"
Sleep 2s

# Snippets is four rows above the first field through the wrapping cursor.
Up
Sleep 300ms
Up
Sleep 300ms
Up
Sleep 300ms
Up
Sleep 1s
Screenshot {{SHOTS}}/01-snippets-setting.png
Sleep 1s

Enter
Sleep 2s
Screenshot {{SHOTS}}/02-snippets-editor.png
Sleep 1s

Escape
Type ":q"
Enter
Sleep 2s
