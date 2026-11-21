# Snooze: a mute with an end, behind the Timed snooze experiment.
#
# The board comes up draining its triage queue, focused on the first session
# that is waiting. In triage a snooze hands over the next session, as a mute
# does, so the run first steps back to the list (ctrl+\) and turns triage off.
# vhs sends ctrl+i as a tab, which is another key here, so triage is toggled
# from quick actions.
#
# Snooze ships off, so the run first shows quick actions with no snooze in
# it, then turns Timed snooze on from Settings → Experimental features (the
# last row, one up from the first; esc closes the card, clears the search,
# then saves), opened from quick actions since vhs does
# not send ctrl+s. vhs has no ctrl+alt chord either, so each snooze
# is run from quick actions, which is the same action the key runs. Each one
# moves the row under the cursor a step along the walk (an hour, four hours,
# a day, then unmuted); the run shows the first two to stay inside the
# capture budget. The row's state reads when it comes back and the error bar
# says the same, so every still is shot inside two polls.

Sleep 4s
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

Ctrl+k
Sleep 1500ms
Type "snooze"
Sleep 1500ms
Screenshot {{SHOTS}}/02-off-quick-actions-offer-no-snooze.png
Escape
Sleep 800ms

Ctrl+k
Sleep 1s
Type "settings"
Sleep 1s
Enter
Sleep 1500ms
Type "/"
Sleep 500ms
Type "experimental"
Sleep 1s
Enter
Sleep 500ms
Enter
Sleep 1s
Up
Sleep 1s
Screenshot {{SHOTS}}/03-experimental-card-timed-snooze-off.png
Enter
Sleep 1s
Screenshot {{SHOTS}}/04-experimental-card-timed-snooze-on.png
Escape
Sleep 500ms
Escape
Sleep 500ms
Escape
Sleep 800ms

Ctrl+k
Sleep 1500ms
Type "snooze"
Sleep 1500ms
Screenshot {{SHOTS}}/05-on-quick-actions-offer-snooze.png
Enter
Sleep 1500ms
Screenshot {{SHOTS}}/06-snoozed-for-an-hour.png
Sleep 800ms

Ctrl+k
Sleep 700ms
Type "snooze"
Sleep 700ms
Enter
Sleep 1200ms
Screenshot {{SHOTS}}/07-snoozed-for-four-hours.png
Sleep 1s
