package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/classifier"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/store"
)

func TestAutoGroupNameIsOneSegment(t *testing.T) {
	for raw, want := range map[string]string{
		"  feeds  ":       "feeds",
		"fix/login\tflow": "fix-loginflow",
		"":                "",
		"a-very-long-session-name-that-keeps-going-on": "a-very-long-session-name-that-keeps-goin",
	} {
		if got := autoGroupName(raw); got != want {
			t.Errorf("autoGroupName(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestAskAutoGroupSendsEveryOptionAndReadsTheChoice(t *testing.T) {
	requests := make(chan map[string]any, 2)
	choice := "g2"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		requests <- request
		_ = json.NewEncoder(w).Encode(map[string]any{"model": jevModel, "answers": map[string]any{
			"next": map[string]any{"type": "choice", "choice": choice},
		}})
	}))
	defer server.Close()
	options := []string{"Existing group \"feeds\"", "New group \"infra\"", "A new one-off project"}
	if got := askAutoGroup(context.Background(), classifier.JEV, server.Client(), server.URL, "k", map[string]any{"session_name": "x"}, options); got != 1 {
		t.Fatalf("choice = %d", got)
	}
	criteria := (<-requests)["questions"].(map[string]any)["next"].(map[string]any)["criteria"].(map[string]any)
	if len(criteria) != len(options)+1 || criteria["none"] == nil || criteria["g3"] != options[2] {
		t.Fatalf("criteria = %#v", criteria)
	}
	choice = "none"
	if got := askAutoGroup(context.Background(), classifier.JEV, server.Client(), server.URL, "k", nil, options); got != -1 {
		t.Fatalf("none = %d", got)
	}
}

func TestAutoGroupFilesIntoAnExistingGroup(t *testing.T) {
	m := buildModel(t)
	if err := m.store.AddGroup("feeds", ""); err != nil {
		t.Fatal(err)
	}
	m.materializeGroupsLocal([]string{"feeds"})
	createSession(t, m, "fix-ticker-feed", t.TempDir(), "")
	m.autoGroupOn = true
	sess := m.sessions[0]
	options := m.autoGroupOptions(sess, time.Now())
	if len(options) != 1 || options[0].kind != autoGroupExisting || options[0].group != "feeds" {
		t.Fatalf("options = %#v", options)
	}
	m.applyAutoGroup(autoGroupResultMsg{id: sess.ID, name: sess.Name, option: options[0], ok: true})
	got, err := m.store.Get(sess.ID)
	if err != nil || got.Group != "feeds" {
		t.Fatalf("group = %q, err %v (bar %q)", got.Group, err, m.errBar.text)
	}
}

func TestAutoGroupPairsTwoLooseSessionsIntoANewGroup(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	createSession(t, m, "billing-export", dir, "")
	createSession(t, m, "billing-export-tests", dir, "")
	m.autoGroupOn = true
	m.cfg.AutoGroup.Presets = []string{"infra"}
	var sess, peer store.Session
	for _, s := range m.sessions {
		if s.Name == "billing-export" {
			peer = s
		} else {
			sess = s
		}
	}
	options := m.autoGroupOptions(sess, time.Now())
	var pick autoGroupOption
	kinds := map[autoGroupKind]int{}
	for _, option := range options {
		kinds[option.kind]++
		if option.kind == autoGroupPeer {
			pick = option
		}
	}
	if kinds[autoGroupPreset] != 1 || kinds[autoGroupPeer] != 1 || pick.peer.ID != peer.ID {
		t.Fatalf("options = %#v", options)
	}
	m.applyAutoGroup(autoGroupResultMsg{id: sess.ID, name: sess.Name, option: pick, ok: true})
	for _, id := range []string{sess.ID, peer.ID} {
		got, err := m.store.Get(id)
		if err != nil || got.Group != "billing-export" {
			t.Fatalf("%s group = %q, err %v (bar %q)", id, got.Group, err, m.errBar.text)
		}
	}
	if m.groupPaths["billing-export"] != dir {
		t.Fatalf("group dir = %q", m.groupPaths["billing-export"])
	}
}

func TestAutoGroupLeavesASessionAPersonPlaced(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "moved-by-hand", t.TempDir(), "")
	m.autoGroupOn = true
	sess := m.sessions[0]
	m.noteAutoGroupPlaced(sess.ID)
	if _, ok := m.autoGroupCandidate(time.Now()); ok {
		t.Fatal("a session a person placed was offered")
	}
	m.applyAutoGroup(autoGroupResultMsg{id: sess.ID, name: sess.Name, option: autoGroupOption{kind: autoGroupPreset, group: "infra"}, ok: true})
	if got, _ := m.store.Get(sess.ID); got.Group != "" {
		t.Fatalf("filed into %q", got.Group)
	}
}

func TestAutoGroupPeerNameCollisionLeavesBothSessionsLoose(t *testing.T) {
	m := buildModel(t)
	m.autoGroupOn = true
	if err := m.store.AddGroup("fix-login", ""); err != nil {
		t.Fatal(err)
	}
	createSession(t, m, "fix/login", t.TempDir(), "")
	peer := m.sessions[0]
	createSession(t, m, "login-tests", t.TempDir(), "")
	sess := m.sessions[1]
	if sess.ID == peer.ID {
		sess = m.sessions[0]
	}
	m.applyAutoGroup(autoGroupResultMsg{id: sess.ID, name: sess.Name, option: autoGroupOption{kind: autoGroupPeer, peer: peer}, ok: true})
	for _, id := range []string{sess.ID, peer.ID} {
		got, err := m.store.Get(id)
		if err != nil || got.Group != "" {
			t.Fatalf("session %s group = %q, err %v", id, got.Group, err)
		}
	}
}

func TestAutoGroupDoesNotRecreateAChangedExistingGroup(t *testing.T) {
	for _, change := range []string{"rename", "move", "delete"} {
		t.Run(change, func(t *testing.T) {
			m := buildModel(t)
			m.autoGroupOn = true
			if err := m.store.AddGroup("project", ""); err != nil {
				t.Fatal(err)
			}
			m.materializeGroupsLocal([]string{"project"})
			createSession(t, m, "project-work", t.TempDir(), "")
			sess := m.sessions[0]
			option := m.autoGroupOptions(sess, time.Now())[0]
			var err error
			switch change {
			case "rename":
				err = m.store.RenameGroup("project", "renamed")
			case "move":
				err = m.store.MoveGroup("project", "parent")
			case "delete":
				_, err = m.store.DeleteEmptyGroup("project")
			}
			if err != nil {
				t.Fatal(err)
			}
			m.applyAutoGroup(autoGroupResultMsg{id: sess.ID, name: sess.Name, option: option, ok: true})
			got, err := m.store.Get(sess.ID)
			if err != nil || got.Group != "" {
				t.Fatalf("group = %q, err %v", got.Group, err)
			}
			groups, err := m.store.Groups()
			if err != nil {
				t.Fatal(err)
			}
			for _, group := range groups {
				if group.Name == "project" {
					t.Fatal("recreated stale group")
				}
			}
		})
	}
}

func TestAutoGroupRejectsARenamedPeer(t *testing.T) {
	m := buildModel(t)
	m.autoGroupOn = true
	sess := store.Session{ID: "primary", Name: "login-tests", Tool: "claude"}
	peer := store.Session{ID: "peer", Name: "login", Tool: "claude"}
	for _, row := range []store.Session{sess, peer} {
		if err := m.store.CreateSession(row); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.store.RenameSession(peer.ID, "unrelated-task"); err != nil {
		t.Fatal(err)
	}
	m.applyAutoGroup(autoGroupResultMsg{id: sess.ID, name: sess.Name, option: autoGroupOption{kind: autoGroupPeer, peer: peer}, ok: true})
	for _, id := range []string{sess.ID, peer.ID} {
		got, err := m.store.Get(id)
		if err != nil || got.Group != "" {
			t.Fatalf("session %s group = %q, err %v", id, got.Group, err)
		}
	}
	groups, err := m.store.Groups()
	if err != nil || len(groups) != 0 {
		t.Fatalf("groups = %+v, err %v", groups, err)
	}
}

func TestAutoGroupAsksOncePerName(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "first-name", t.TempDir(), "")
	m.autoGroupOn = true
	sess, ok := m.autoGroupCandidate(time.Now())
	if !ok {
		t.Fatal("no candidate")
	}
	m.autoGroup.checked = map[string]string{sess.ID: sess.Name}
	if _, ok := m.autoGroupCandidate(time.Now()); ok {
		t.Fatal("asked twice under one name")
	}
	m.sessions[0].Name = "renamed-after-task"
	if _, ok := m.autoGroupCandidate(time.Now()); !ok {
		t.Fatal("a rename was not asked about")
	}
}

func TestAutoGroupSkipsArchivedGroups(t *testing.T) {
	m := buildModel(t)
	if err := m.store.AddGroup("old-project", ""); err != nil {
		t.Fatal(err)
	}
	m.materializeGroupsLocal([]string{"old-project"})
	createSession(t, m, "new-work", t.TempDir(), "")
	m.archivedGroups = map[string]bool{"old-project": true}
	if options := m.autoGroupOptions(m.sessions[0], time.Now()); len(options) != 0 {
		t.Fatalf("an archived group was offered: %#v", options)
	}
}

func TestAutoGroupPreservesDetachedSpawner(t *testing.T) {
	m := buildModel(t)
	m.autoGroupOn = true
	for _, sess := range []store.Session{
		{ID: "creator", Name: "creator", Tool: "claude"},
		{ID: "detached", Name: "work", Tool: "claude", SpawnedBy: "creator"},
	} {
		if err := m.store.CreateSession(sess); err != nil {
			t.Fatal(err)
		}
	}
	m.applyAutoGroup(autoGroupResultMsg{id: "detached", name: "work", option: autoGroupOption{kind: autoGroupPreset, group: "project"}, ok: true})
	got, err := m.store.Get("detached")
	if err != nil || got.Group != "project" || got.SpawnedBy != "creator" || got.ParentID != "" {
		t.Fatalf("grouped session = %+v, err %v", got, err)
	}
}

func TestAutoGroupReadsOpeningWorkBeforeTranscriptTail(t *testing.T) {
	for _, tool := range []string{search.ToolClaude, search.ToolCodex} {
		t.Run(tool, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "projects", "project")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "conversation.jsonl")
			file, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			message := func(text string) string {
				if tool == search.ToolClaude {
					return `{"type":"user","message":{"role":"user","content":"` + text + `"}}` + "\n"
				}
				return `{"type":"response_item","payload":{"type":"message","role":"user","content":"` + text + `"}}` + "\n"
			}
			if _, err := file.WriteString(message("# AGENTS.md instructions for /repo") + message("[gate-inbox] This request is from Gate Inbox itself: Name this session") + message("Fix the parser")); err != nil {
				t.Fatal(err)
			}
			if _, err := file.Seek(17<<20, 0); err != nil {
				t.Fatal(err)
			}
			if _, err := file.WriteString("\n" + message("An unrelated later task")); err != nil {
				t.Fatal(err)
			}
			locator := search.NewLocator(root, root)
			sess := store.Session{ID: "adopted", AgentSessionID: "conversation"}
			if tool == search.ToolCodex {
				if err := os.Rename(path, filepath.Join(root, "rollout-conversation.jsonl")); err != nil {
					t.Fatal(err)
				}
			}
			if got := autoGroupFirstPrompt(locator, sess, tool, ""); got != "Fix the parser" {
				t.Fatalf("opening work = %q", got)
			}
		})
	}
}
