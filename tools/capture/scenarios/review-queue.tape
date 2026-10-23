# The review queue: asks for the operator's review, on the board.
# Run with
#   --no-seed --transcripts finished
#
# P fetches the asks from GitHub, which a capture's scratch HOME has no login
# for, so the scenario leaves the board, writes four asks into the scratch
# store the way an ingest records them, and comes back. The Reviews row sits
# under the root; enter opens its page, j moves, x dismisses an ask.

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

Type "q"
Sleep 5s
Type `python3 -c "import sqlite3,time; db=sqlite3.connect('{{HOME}}/state.db'); now=time.time(); ms=int(now*1000); [db.execute('insert into review_requests (repo,number,trigger,trigger_id,title,author,url,head_sha,draft,additions,deletions,changed,checks,triggered_at,state,created_at,updated_at) values (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)', (r,n,t,t+str(n),ti,a,'https://github.com/'+r+'/pull/'+str(n),'abc123',d,ad,de,ch,ck,int((now-age)*1e9),'pending',ms,ms)) for r,n,t,ti,a,d,ad,de,ch,ck,age in [('example/api',412,'requested','Retry idempotent writes on a dropped connection','dana',0,184,37,6,'passing',26*3600),('example/web',88,'label','Move the settings page to the new form kit','sam',0,612,540,23,'pending',5*3600),('example/api',415,'comment','Cap the export job at one per account','ravi',0,48,9,2,'failing',40*60),('example/cli',31,'requested','Draft: config file discovery','lee',1,95,12,4,'none',8*60)]]; db.commit()"`
Enter
Sleep 2s
Type "{{LAUNCH}}"
Enter
Sleep 35s

Screenshot {{SHOTS}}/01-reviews-row.png
Sleep 1s

Up
Sleep 900ms
Enter
Sleep 2s
Screenshot {{SHOTS}}/02-reviews-page.png
Sleep 1s

Type "j"
Sleep 700ms
Type "j"
Sleep 900ms
Screenshot {{SHOTS}}/03-cursor-on-a-comment-ask.png
Sleep 1s

Type "x"
Sleep 1500ms
Screenshot {{SHOTS}}/04-after-dismiss.png
Sleep 1s

Escape
Sleep 1500ms
Type "P"
Sleep 1500ms
Screenshot {{SHOTS}}/05-ingest-without-a-github-login.png
Sleep 2s
