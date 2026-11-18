# Snooze: a mute with an end.
#
# The board comes up draining its triage queue, focused on the first session
# that is waiting. In triage a snooze hands over the next session, as a mute
# does, so the run first steps back to the list (ctrl+\) and turns triage off.
# vhs sends ctrl+i as a tab, which is another key here, so triage is toggled
# from quick actions. Each alt+z then moves the row under the cursor one step
# along the walk: an hour, four hours, a day, then unmuted. The row's state
# reads when it comes back and the error bar says the same, so every still is
# shot inside two polls. The last press snoozes it again.

Sleep 5s
Ctrl+\
Sleep 2s
Ctrl+k
Sleep 1500ms
Type "triage"
Sleep 1s
Enter
Sleep 2s
Screenshot {{SHOTS}}/01-session-selected.png
Sleep 1s

Alt+z
Sleep 1500ms
Screenshot {{SHOTS}}/02-snoozed-for-an-hour.png
Sleep 1500ms

Alt+z
Sleep 1500ms
Screenshot {{SHOTS}}/03-snoozed-for-four-hours.png
Sleep 1500ms

Alt+z
Sleep 1500ms
Screenshot {{SHOTS}}/04-snoozed-for-a-day.png
Sleep 1500ms

Alt+z
Sleep 1500ms
Screenshot {{SHOTS}}/05-unmuted-back-on-the-list.png
Sleep 1500ms

Alt+z
Sleep 1500ms
Screenshot {{SHOTS}}/06-snoozed-again.png
Sleep 1s
