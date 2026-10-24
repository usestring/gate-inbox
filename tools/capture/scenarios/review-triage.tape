# The Reviews row in triage: it queues by its oldest ask, and the drain hands
# over the review page the way it hands over a session.
# Run with
#   --no-seed --transcripts finished
#
# The scenario writes four asks out of frame into the scratch store, the way
# an ingest records them (a capture's scratch HOME has no GitHub login), and
# relaunches. The oldest ask is a day old, so the row heads the queue: i opens
# the review page on it, ctrl+q moves on to the finished session, and the row
# stays on the list once its asks were seen.

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
Hide
Type `python3 -c "import sqlite3,time; db=sqlite3.connect('{{HOME}}/state.db'); now=time.time(); ms=int(now*1000); [db.execute('insert into review_requests (repo,number,trigger,trigger_id,title,author,url,head_sha,draft,additions,deletions,changed,checks,triggered_at,state,created_at,updated_at) values (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)', (r,n,t,t+str(n),ti,a,'https://github.com/'+r+'/pull/'+str(n),'abc123',d,ad,de,ch,ck,int((now-age)*1e9),'pending',ms,ms)) for r,n,t,ti,a,d,ad,de,ch,ck,age in [('example/api',412,'requested','Retry idempotent writes on a dropped connection','dana',0,184,37,6,'passing',26*3600),('example/web',88,'label','Move the settings page to the new form kit','sam',0,612,540,23,'pending',5*3600),('example/api',415,'comment','Cap the export job at one per account','ravi',0,48,9,2,'failing',40*60),('example/cli',31,'requested','Draft: config file discovery','lee',1,95,12,4,'none',8*60)]]; db.commit()"`
Enter
Sleep 2s
Type "{{LAUNCH}}"
Enter
Show
Sleep 35s

Type "i"
Sleep 3s
Screenshot {{SHOTS}}/01-triage-hands-over-the-review-page.png
Sleep 1s

Ctrl+q
Sleep 3s
Screenshot {{SHOTS}}/02-leaving-it-moves-on-to-the-session.png
Sleep 1s

Ctrl+\
Sleep 2s
Screenshot {{SHOTS}}/03-reviews-row-stays-on-the-list.png
Sleep 2s
