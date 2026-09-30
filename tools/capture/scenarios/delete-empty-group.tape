# x on a group with nothing filed under it deletes the group rather than
# archiving it, so the name is free at once. The scenario makes an empty
# group, kills it, shows the archived view does not hold it, and makes a
# group under the same name again.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/delete-empty-group.tape

Sleep 3s
Type "g"
Sleep 1500ms
Type "aeo"
Sleep 1s
Screenshot {{SHOTS}}/01-new-group-card.png
Sleep 1s
Enter
Sleep 2s
Screenshot {{SHOTS}}/02-empty-group-listed.png
Sleep 1s
Type "x"
Sleep 1500ms
Screenshot {{SHOTS}}/03-delete-dialog.png
Sleep 1s
Type "y"
Sleep 800ms
Screenshot {{SHOTS}}/04-group-deleted.png
Sleep 2s
Type "t"
Sleep 2s
Screenshot {{SHOTS}}/05-not-in-the-archive.png
Sleep 1s
Type "t"
Sleep 1500ms
Type "g"
Sleep 1500ms
Type "aeo"
Sleep 1s
Enter
Sleep 2s
Screenshot {{SHOTS}}/06-name-free-again.png
Sleep 1s
