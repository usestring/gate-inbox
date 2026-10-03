# Permissions a parent granted its child with grant_permission, on the board.
# Run with
#   --no-seed --transcripts finished
#
# A grant widens what a session may do, so the board names each one, with the
# time it lapses, in the selected session's details rather than leaving it to
# be found in a settings file. A grant is made by a parent through the MCP
# tool, which no keystroke on the board can do, so the scenario leaves the
# board, writes two grants into the scratch store the way the tool records
# them, and comes back.

Sleep 30s
Enter
Sleep 4s

Type "n"
Sleep 2s
Type "claude"
Sleep 1500ms
Enter
Sleep 14s
Ctrl+q
Sleep 4s

Down
Sleep 700ms
Down
Sleep 900ms
Screenshot {{SHOTS}}/01-no-grants.png
Sleep 1s

Type "q"
Sleep 5s
Type `python3 -c "import sqlite3,time; db=sqlite3.connect('{{HOME}}/state.db'); sid=db.execute('select id from sessions limit 1').fetchone()[0]; now=int(time.time()*1000); [db.execute('insert into permission_grants (session_id,kind,value,by_session,evidence_tool_use_id,question_hash,created_at,expires_at) values (?,?,?,?,?,?,?,?)', (sid,k,v,'parent01','toolu_demo',k,now,now+m*60000)) for k,v,m in [('soft_command','tools/probe.sh',120),('domain','proxy.example.com',30)]]; db.commit()"`
Enter
Sleep 2s
Type "{{LAUNCH}}"
Enter
Sleep 35s

Down
Sleep 700ms
Down
Sleep 900ms
Screenshot {{SHOTS}}/02-grants-in-the-details.png
Sleep 1s
