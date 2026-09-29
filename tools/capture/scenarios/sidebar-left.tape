# The sidebar setting. The board opens with the sessions rail on the right,
# Settings' "sidebar" row moves it to the left as it is stepped -- the board
# behind the panel is the preview -- and closing Settings keeps it there. A
# focused session then draws its ring on the right with the rail to its left,
# and Left at the empty prompt is the arrow that returns to the list.
#
# The sidebar row is the eighth in Settings, so seven Downs reach it. The
# claude row is found by searching for its tool rather than by counting rows,
# since the live poll reorders and unfolds the tree under a blind Down. Left
# only leaves at the head of the prompt, so record with the claude session
# resting on its bare prompt. Linear is switched off so the branch's ticket
# does not draw a work row under the session:
#
#   printf '[integrations.linear]\nenabled = false\n' > /tmp/claude/no-linear.toml
#   tools/capture/capture.sh --scenario tools/capture/scenarios/sidebar-left.tape \
#     --transcripts resting,working,finished --config /tmp/claude/no-linear.toml
#
# {{SHOTS}} is replaced with this run's still directory. Every Screenshot needs
# a Sleep after it, or vhs ends the recording before the frame is written.

Screenshot {{SHOTS}}/01-list-rail-on-right.png
Sleep 1s

Type "s"
Sleep 1200ms
Down
Sleep 150ms
Down
Sleep 150ms
Down
Sleep 150ms
Down
Sleep 150ms
Down
Sleep 150ms
Down
Sleep 150ms
Down
Sleep 800ms
Screenshot {{SHOTS}}/02-settings-sidebar-right.png
Sleep 1s

Right
Sleep 1200ms
Screenshot {{SHOTS}}/03-settings-sidebar-left-live.png
Sleep 1s

Enter
Sleep 1500ms
Screenshot {{SHOTS}}/04-list-rail-on-left.png
Sleep 1s

Type "/"
Sleep 1s
Type "claude"
Sleep 1500ms
Enter
Sleep 1500ms
Enter
Sleep 2s
Screenshot {{SHOTS}}/05-focused-rail-on-left.png
Sleep 1s

Left
Sleep 1200ms
Screenshot {{SHOTS}}/06-left-returned-to-the-list.png
Sleep 1s
