# A finished session ages into the waiting tier in triage.
# Run with
#   --foreign finished
#
# The adopted pane finishes before the board seeds anything, so it has sat
# finished longest; the seeded claude session waits after it, and the seeded
# opencode session finishes last. The threshold is a stored setting read at
# startup, so the scenario leaves the board, sets it to two minutes and comes
# back, hidden, since the launch line carries the scratch paths; recording
# resumes once triage is on. Triage first reads waiting before finished; once
# the adopted pane has sat finished past the threshold it moves above the
# younger question, while the newer finish stays below it.

Hide
Type "q"
Sleep 5s
Type `python3 -c "import sqlite3; db=sqlite3.connect('{{HOME}}/state.db'); db.execute('insert or replace into settings (key,value) values (?,?)', ('triage_finished_age','120s')); db.commit()"`
Enter
Sleep 2s
Type "{{LAUNCH}}"
Enter
Sleep 12s

Type "i"
Sleep 4s
Show
Sleep 1s
Screenshot {{SHOTS}}/01-question-ahead-of-a-fresh-finish.png
Sleep 1s

Sleep 3s
Hide
Sleep 44s
Show
Sleep 3s
Screenshot {{SHOTS}}/02-aged-finish-ahead-of-the-question.png
Sleep 1s
