# The exit arrow follows the rail. The rail sits on the right of the frame, so
# the spare arrow at a prompt is Right: the focused session's pane is left, the
# rail beside it, and pressing Right at the end of the empty prompt returns to
# the list. Left reaches the agent instead, which is the half no still shows.
#
# The caret has to be at the prompt's end for the key to be spare, and that is
# a position rather than a state: the transcript seeded for the claude session
# ends on its bare "❯" with no trailing newline, which is what leaves the demo
# pane's caret there (see demo-agent.sh). The claude row is found by the text
# of that transcript rather than by counting rows, since the live poll reorders
# and unfolds the tree under a blind Down.
#
# {{SHOTS}} is replaced with this run's still directory. Every Screenshot needs
# a Sleep after it, or vhs ends the recording before the frame is written.

Type "/"
Sleep 1s
Type "focus exit arrow "
Sleep 1500ms
Enter
Sleep 1500ms
Enter
Sleep 2s
Screenshot {{SHOTS}}/01-focused-at-the-prompt-end.png
Sleep 1s

Right
Sleep 1200ms
Screenshot {{SHOTS}}/02-right-returned-to-the-list.png
Sleep 1s
