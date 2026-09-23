# Rebinding a key from the board: the whole loop the key map now owns.
#
# The keys typed here are the defaults, so this tape re-records against any
# later change to them -- except the one it makes itself, which is why it
# rebinds fork rather than a key the rest of the scenario presses.

Screenshot {{SHOTS}}/01-footer-default.png
Sleep 1s

# The key map, which prints what is bound rather than what was compiled in.
Type "?"
Sleep 2s
Screenshot {{SHOTS}}/02-key-map.png
Sleep 1s

# Search narrows it to the one binding this tape is about.
Type "/"
Sleep 600ms
Type "fork it into"
Sleep 1s
Enter
Sleep 1500ms
Screenshot {{SHOTS}}/03-binding-selected.png
Sleep 1s

# Enter arms the capture: the next key pressed becomes this action's key.
Enter
Sleep 1500ms
Screenshot {{SHOTS}}/04-press-a-key.png
Sleep 1s

# And it is bound, and written to keys.toml.
Type "z"
Sleep 2s
Screenshot {{SHOTS}}/05-rebound.png
Sleep 1s

# Back on the list: the footer names the new key, without a restart.
Type "q"
Sleep 2s
Screenshot {{SHOTS}}/06-footer-rebound.png
Sleep 1s

# And the key acts. z forks the selected session; f no longer does anything.
Type "f"
Sleep 1500ms
Type "z"
Sleep 2s
Screenshot {{SHOTS}}/07-z-forks.png
Sleep 1s
Escape
Sleep 1s
