# Each review ask gets a briefing session before anyone opens it, and the
# Reviews tab says how far along that briefing is.
# Run with
#   --reviews
#
# A fetch is what launches the briefings, and a capture's scratch HOME has no
# GitHub login, so the scenario leaves the board and ties two of the seeded
# asks to the demo sessions the way a prepare records them: #412 to the
# working codex row, #418 to the waiting claude row. #421 keeps no session.
# The setup stays out of frame.

Hide
Type "q"
Sleep 5s
Type `python3 -c "import sqlite3; db=sqlite3.connect('{{HOME}}/state.db'); sid=lambda t: db.execute('select id from sessions where tool=? order by created_at limit 1',(t,)).fetchone()[0]; db.execute('update review_requests set session_id=? where number=412',(sid('codex'),)); db.execute('update review_requests set session_id=? where number=418',(sid('claude'),)); db.commit()"`
Enter
Sleep 2s
Type "{{LAUNCH}}"
Enter
Sleep 20s
Show
Sleep 1s

Type "]"
Sleep 1500ms
Screenshot {{SHOTS}}/01-reviews-tab-briefing-labels.png
Sleep 1s

Down
Sleep 1s
Screenshot {{SHOTS}}/02-briefing-ready-ask.png
Sleep 1s

Down
Sleep 1s
Screenshot {{SHOTS}}/03-ask-without-a-briefing.png
Sleep 1s
