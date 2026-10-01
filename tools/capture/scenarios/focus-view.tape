# The focused view. `enter` opens the session that owns this transcript, the
# adopted claude pane, and it opens on the same shortened conversation the list
# shows -- the focused-view default. Settings' "focused view" row is then set
# to `terminal`, and focusing the session again hands the pane to the agent.
# F3 -- the same setting under a key -- is what switches a session from one to
# the other from inside it.
#
# The transcript's sidecar is what gives the adopted pane a conversation to
# draw, so this runs with --no-seed --foreign conversation.
#
# {{SHOTS}} is replaced with this run's still directory. Every Screenshot needs
# a Sleep after it, or vhs ends the recording before the frame is written.

Sleep 8s
Type "/"
Sleep 1s
Type "conversation "
Sleep 2s
Enter
Sleep 2s
Screenshot {{SHOTS}}/01-conversation-in-the-list.png
Sleep 1s

Enter
Sleep 3s
Screenshot {{SHOTS}}/02-focused-on-the-conversation.png
Sleep 1s

Ctrl+q
Sleep 1s

# Settings' focused-view row: 16 fields down from the tool row, then one step
# right from the conversation default to the terminal.
Type "s"
Sleep 1s
Down
Down
Down
Down
Down
Down
Down
Down
Down
Down
Down
Down
Down
Down
Down
Down
Sleep 1s
Right
Sleep 1s
Escape
Sleep 1s

Type "/"
Sleep 1s
Type "conversation "
Sleep 2s
Enter
Sleep 2s
Enter
Sleep 3s
Screenshot {{SHOTS}}/03-focused-on-the-terminal.png
Sleep 1s
