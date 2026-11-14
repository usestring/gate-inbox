package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/classifier"
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
