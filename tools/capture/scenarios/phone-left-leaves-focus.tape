# By default, Left at the head of a focused session's prompt returns to the
# list -- here on the mobile layout, where a phone has no easy Ctrl+Q.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/phone-left-leaves-focus.tape --cols 54 --rows 27 \
#     --transcripts resting,waiting,working
Sleep 2s
Type "s"
Sleep 1500ms
Down 6
Sleep 500ms
Right 2
Sleep 1s
Enter
Sleep 2s
Screenshot {{SHOTS}}/01-mobile-list.png
Sleep 1s
Type "/"
Sleep 1s
Type "focus exit arrow "
Sleep 1500ms
Enter
Sleep 1500ms
Enter
Sleep 2s
Screenshot {{SHOTS}}/02-focused.png
Sleep 1s
Left
Sleep 1500ms
Screenshot {{SHOTS}}/03-left-returns-to-list.png
Sleep 1s
