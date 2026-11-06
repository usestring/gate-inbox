// verifyboard: local-only behaviour harness for gate-inbox #461/#462. Never committed.
//
//	verifyboard dump      -> every row through Board.List and Board.Get (live + archived)
//	verifyboard scenario  -> parent/child overlay transitions, archive, state.db replacement
//	verifyboard hammer    -> concurrent Gets compared with serial Gets
//
// GATE_INBOX_HOME names the (copied) home.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/usestring/gate-inbox/app"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/store"

	_ "modernc.org/sqlite"
)

type row struct {
	ID, Status, ParentID, SpawnedBy, Role, Dir string
	Running, Archived, Terminal              bool
	Err                                      string `json:",omitempty"`
}

func conv(s extension.SessionInfo) row {
	return row{ID: s.ID, Status: s.Status, ParentID: s.ParentID, SpawnedBy: s.SpawnedBy, Role: s.Role,
		Dir: s.Directory, Running: s.Running, Archived: s.Archived, Terminal: s.Terminal}
}

var ctx = context.Background()

func home() string { return os.Getenv("GATE_INBOX_HOME") }

func listAll(b extension.Board, archived bool) []extension.SessionInfo {
	var out []extension.SessionInfo
	f := extension.SessionFilter{Limit: 500, IncludeArchived: archived, IncludeTerminals: true}
	for {
		p, err := b.List(ctx, f)
		must(err)
		out = append(out, p.Sessions...)
		if !p.Truncated || p.Cursor == "" {
			return out
		}
		f.After = p.Cursor
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func get(b extension.Board, id string) row {
	s, err := b.Get(ctx, id)
	if err != nil {
		return row{ID: id, Err: err.Error()}
	}
	return conv(s)
}

func dump(b extension.Board) {
	out := map[string]any{}
	live := listAll(b, false)
	all := listAll(b, true)
	var lrows, grows, arows []row
	for _, s := range live {
		lrows = append(lrows, conv(s))
	}
	for _, s := range all {
		arows = append(arows, conv(s))
		grows = append(grows, get(b, s.ID))
	}
	for _, rs := range [][]row{lrows, grows, arows} {
		sort.Slice(rs, func(i, j int) bool { return rs[i].ID < rs[j].ID })
	}
	out["list_live"] = lrows
	out["list_all"] = arows
	out["get_each"] = grows
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", " ")
	must(enc.Encode(out))
}

func hammer(b extension.Board) {
	ids := []string{}
	for _, s := range listAll(b, true) {
		ids = append(ids, s.ID)
	}
	serial := map[string]row{}
	for _, id := range ids {
		serial[id] = get(b, id)
	}
	var mu sync.Mutex
	diffs := 0
	listErrs := 0
	var wg sync.WaitGroup
	for g := 0; g < 12; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < len(ids); i++ {
				id := ids[(i*7+g*13)%len(ids)]
				var r row
				if i%5 == 0 {
					if _, err := b.List(ctx, extension.SessionFilter{Limit: 500}); err != nil {
						mu.Lock()
						listErrs++
						mu.Unlock()
					}
					r = get(b, id)
				} else {
					r = get(b, id)
				}
				if r != serial[id] {
					mu.Lock()
					diffs++
					if diffs < 5 {
						fmt.Printf("DIFF %s serial=%+v concurrent=%+v\n", id, serial[id], r)
					}
					mu.Unlock()
				}
			}
		}(g)
	}
	wg.Wait()
	fmt.Printf("hammer: ids=%d goroutines=12 diffs=%d listErrs=%d\n", len(ids), diffs, listErrs)
}

type step struct {
	Step       string
	GetParent  string
	ListParent string
	Want       string
	OK         bool
}

func scenario(b extension.Board, tool string) {
	path := filepath.Join(home(), "state.db")
	w, err := store.Open(path)
	must(err)
	now := time.Now()
	p := store.Session{ID: "vbparent", Name: "vb-parent", Tool: tool, Cwd: "/tmp", Group: "verify", Status: "finished", CreatedAt: now}
	c1 := store.Session{ID: "vbchild1", Name: "vb-child1", Tool: tool, Cwd: "/tmp", Group: "verify", Status: "finished", ParentID: "vbparent", SpawnedBy: "vbparent", CreatedAt: now.Add(time.Second)}
	c2 := store.Session{ID: "vbchild2", Name: "vb-child2", Tool: tool, Cwd: "/tmp", Group: "verify", Status: "finished", ParentID: "vbparent", SpawnedBy: "vbparent", CreatedAt: now.Add(2 * time.Second)}
	gc := store.Session{ID: "vbgrand", Name: "vb-grand", Tool: tool, Cwd: "/tmp", Group: "verify", Status: "finished", ParentID: "vbchild2", SpawnedBy: "vbchild2", CreatedAt: now.Add(3 * time.Second)}
	_ = gc
	for _, s := range []store.Session{p, c1, c2} {
		must(w.CreateSession(s))
	}
	var steps []step
	check := func(name, id, want string) {
		g := get(b, id)
		lr := ""
		for _, s := range listAll(b, false) {
			if s.ID == id {
				lr = s.Status
			}
		}
		if g.Err != "" {
			lr = "ERR " + g.Err
		}
		steps = append(steps, step{name, g.Status, lr, want, g.Status == want && lr == want})
	}
	check("all finished", "vbparent", "finished")
	must(w.UpdateStatus("vbchild1", "working"))
	check("child1 starts working", "vbparent", "delegating")
	must(w.UpdateStatus("vbchild1", "finished"))
	check("child1 ends", "vbparent", "finished")
	must(w.UpdateStatus("vbchild2", "background"))
	check("child2 background -> parent", "vbparent", "delegating")
	must(w.UpdateStatus("vbchild2", "waiting"))
	check("child2 waiting -> parent", "vbparent", "delegating")
	must(w.UpdateStatus("vbchild2", "errored"))
	check("child2 errored -> parent", "vbparent", "finished")
	must(w.UpdateStatus("vbchild2", "finished"))
	msgID, _, err := w.Enqueue(store.InboxMessage{SessionID: "vbparent", SenderID: "vbchild1", SenderName: "vb-child1",
		Body: "child rest", Subject: store.ChildRestSubject("vbchild1"), SentAt: time.Now()}, store.InboxLimits{QueueCap: 1000, RateCap: 1000, RateWindow: time.Minute, PairCap: 1000, PairWindow: time.Minute})
	must(err)
	check("pending child report -> parent", "vbparent", "delegating")
	must(w.MarkDelivered(msgID, time.Now()))
	check("report delivered -> parent", "vbparent", "finished")
	must(w.UpdateStatus("vbparent", "idle"))
	must(w.UpdateStatus("vbchild2", "working"))
	check("idle parent, child2 working", "vbparent", "delegating")
	must(w.SetArchived("vbchild2", true))
	check("archive working child2 -> parent", "vbparent", "idle")
	must(w.SetArchived("vbchild2", false))
	check("unarchive child2 -> parent", "vbparent", "delegating")
	must(w.UpdateStatus("vbparent", "waiting"))
	check("waiting parent stays waiting", "vbparent", "waiting")
	must(w.UpdateStatus("vbparent", "finished"))
	check("back to finished, child2 working", "vbparent", "delegating")
	must(w.Close())

	// Fill the board's idle handles with concurrent reads before the replacement.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); get(b, "vbparent") }()
	}
	wg.Wait()

	// Replace state.db: a consistent copy with child2 finished and the parent waiting.
	tmp := path + ".replacement"
	os.Remove(tmp)
	db, err := sql.Open("sqlite", path)
	must(err)
	_, err = db.Exec(`VACUUM INTO ?`, tmp)
	must(err)
	must(db.Close())
	r, err := store.Open(tmp)
	must(err)
	must(r.UpdateStatus("vbchild2", "finished"))
	must(r.UpdateStatus("vbparent", "waiting"))
	must(r.Close())
	before, _ := os.Stat(path)
	os.Remove(path + "-wal")
	os.Remove(path + "-shm")
	must(os.Rename(tmp, path))
	after, _ := os.Stat(path)
	fmt.Fprintf(os.Stderr, "replaced state.db: same file=%v\n", os.SameFile(before, after))
	check("after replacing state.db (new file: parent waiting)", "vbparent", "waiting")
	check("after replacing state.db (child2 finished)", "vbchild2", "finished")

	// Write to the new file through another connection; reads must follow it.
	w2, err := store.Open(path)
	must(err)
	must(w2.UpdateStatus("vbparent", "finished"))
	must(w2.UpdateStatus("vbchild1", "working"))
	check("new file: child1 working -> parent", "vbparent", "delegating")
	must(w2.Close())
	// Churn handles, then verify the new file kept every write and is intact.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); get(b, "vbchild1") }()
	}
	wg.Wait()
	db2, err := sql.Open("sqlite", path)
	must(err)
	var integrity, pst, c1st string
	must(db2.QueryRow(`PRAGMA integrity_check`).Scan(&integrity))
	must(db2.QueryRow(`SELECT status FROM sessions WHERE id='vbparent'`).Scan(&pst))
	must(db2.QueryRow(`SELECT status FROM sessions WHERE id='vbchild1'`).Scan(&c1st))
	db2.Close()
	steps = append(steps, step{"new file integrity/persistence", pst + "/" + c1st, integrity, "finished/working ok", pst == "finished" && c1st == "working" && integrity == "ok"})
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", " ")
	must(enc.Encode(steps))
	bad := 0
	for _, s := range steps {
		if !s.OK {
			bad++
		}
	}
	fmt.Fprintf(os.Stderr, "scenario: %d steps, %d failed\n", len(steps), bad)
	_ = strings.TrimSpace
}

func main() {
	b, err := app.NewBoard()
	must(err)
	switch os.Args[1] {
	case "dump":
		dump(b)
	case "hammer":
		hammer(b)
	case "scenario":
		scenario(b, os.Args[2])
	}
}
