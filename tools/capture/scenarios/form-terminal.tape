# The New Session form offers the terminal the way n's box does: the tool
# picker lists it, and typing narrows to it. (Submitting it opens a shell
# tab, which the public evidence scan cannot carry: a shell frame shows the
# machine's prompt. The submit path is covered by unit test instead.)

Sleep 3s
Ctrl+n
Sleep 2s
Screenshot {{SHOTS}}/01-form-offers-terminal.png
Type "term"
Sleep 1s
Screenshot {{SHOTS}}/02-terminal-narrowed.png
Sleep 1s
Escape
Sleep 1s
