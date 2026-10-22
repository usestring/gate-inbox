# The group card's start field pins the group's path: every session spawned
# into the group then starts there, and a deeper directory it asked for is
# handed to it to change into. The scenario makes a group, turns the pin on,
# and shows the rail marking the group's dir as pinned.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/group-pin-path.tape

Sleep 3s
Type "g"
Sleep 1500ms
Type "repo"
Sleep 800ms
Tab
Sleep 500ms
Tab
Sleep 800ms
Screenshot {{SHOTS}}/01-start-field-default.png
Sleep 500ms
Space
Sleep 1s
Screenshot {{SHOTS}}/02-start-field-pinned.png
Sleep 1s
Enter
Sleep 2s
Screenshot {{SHOTS}}/03-rail-shows-pinned.png
Sleep 1s
