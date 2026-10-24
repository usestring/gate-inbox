# Drafting a review on the Files changed tab: the agent's drafts under
# their lines, keeping one and dropping the other, a comment of your own on
# the marked line, and the summary box that submits the kept drafts.
# Run with
#   --no-seed --transcripts finished
#
# Set up as review-page.tape, plus two agent drafts written to the store the
# way review_comment records them. The capture stops short of submitting:
# the stand-in gh answers reads only.

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
Set TypingSpeed 0ms
Type "mkdir -p {{HOME}}/fx && echo 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 | base64 -d | tar xz -C {{HOME}}/fx && export PATH={{HOME}}/fx/bin:$PATH"
Enter
Sleep 1s
Type `python3 -c "import sqlite3,time; db=sqlite3.connect('{{HOME}}/state.db'); now=time.time(); ms=int(now*1000); [db.execute('insert into review_requests (repo,number,trigger,trigger_id,title,author,url,head_sha,draft,additions,deletions,changed,checks,triggered_at,state,created_at,updated_at) values (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)', (r,n,t,t+str(n),ti,a,'https://github.com/'+r+'/pull/'+str(n),'abc123',d,ad,de,ch,ck,int((now-age)*1e9),'pending',ms,ms)) for r,n,t,ti,a,d,ad,de,ch,ck,age in [('example/api',412,'requested','Retry idempotent writes on a dropped connection','dana',0,184,37,3,'passing',26*3600),('example/web',88,'label','Move the settings page to the new form kit','sam',0,612,540,23,'pending',5*3600)]]; db.commit()"`
Enter
Sleep 2s
Type `python3 -c "import sqlite3,time; db=sqlite3.connect('{{HOME}}/state.db'); ms=int(time.time()*1000); [db.execute('insert into review_drafts (repo,number,head_sha,path,line,side,body,kind,context,author,session_id,state,created_at,updated_at) values (?,?,?,?,?,?,?,?,?,?,?,?,?,?)', ('example/api',412,'abc123','client/retry.go',l,'RIGHT',b,k,c,'agent','','proposed',ms,ms)) for l,b,k,c in [(5,'math/rand is shared and unseeded per process here; is identical jitter across clients intended?','question','The description says retries spread out so clients do not stampede the server.'),(9,'maxBackoff caps one delay, but nothing caps the total wait; a dropped write can retry for minutes.','finding','')]]; db.commit()"`
Enter
Sleep 2s
Set TypingSpeed 15ms
Type "{{LAUNCH}}"
Enter
Show
Sleep 35s

Up
Sleep 900ms
Enter
Sleep 2s
Enter
Sleep 4s
Type "2"
Sleep 1500ms
Type "]"
Sleep 1500ms
Screenshot {{SHOTS}}/01-agent-draft-selected.png
Sleep 1s

Type "y"
Sleep 1500ms
Type "]"
Sleep 1500ms
Type "d"
Sleep 1500ms
Screenshot {{SHOTS}}/02-one-kept-one-dropped.png
Sleep 1s

Type "c"
Sleep 1500ms
Type "Name the error this retries on in the doc comment."
Sleep 1500ms
Screenshot {{SHOTS}}/03-comment-on-the-marked-line.png
Enter
Sleep 1500ms
Screenshot {{SHOTS}}/04-your-comment-kept.png
Sleep 1s

Type "C"
Sleep 1500ms
Type "Two notes inline; the retry shape looks right."
Sleep 1500ms
Screenshot {{SHOTS}}/05-submit-with-kept-drafts.png
Sleep 1s
Escape
Sleep 1500ms
Screenshot {{SHOTS}}/06-cancelled.png
Sleep 2s
