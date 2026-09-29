package ui

import (
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// A detached spawn belongs to the person at its pane. On 2026-09-28 a session
// that made one at the user's request was told of every finish and question
// it had, relayed them to the user, and answered its dialog itself. Its nested
// sibling, made by the same session, is still reported as before.
func TestADetachedSpawnRelaysNothingToItsCreator(t *testing.T) {
	restings := []string{status.Finished, status.Errored, status.Dead}
	for _, resting := range restings {
		t.Run(resting, func(t *testing.T) {
			p, st := pollerWithStore(t)
			creator := seedSession(t, st, store.Session{ID: "parent01", Name: "high-cpu-usage", Status: status.Working})
			detached := seedSession(t, st, store.Session{
				ID: "child001", Name: "gitest-tmux-no-user-config", SpawnedBy: creator.ID, Status: status.Working,
			})
			nested := seedSession(t, st, store.Session{
				ID: "child002", Name: "nested-probe", ParentID: creator.ID, SpawnedBy: creator.ID, Status: status.Working,
			})

			if err := p.relayChildRest(detached, resting); err != nil {
				t.Fatalf("relayChildRest detached: %v", err)
			}
			if err := p.relayChildQuestion(detached, status.Waiting, askPane); err != nil {
				t.Fatalf("relayChildQuestion detached: %v", err)
			}
			if err := p.relayChildQuestion(detached, status.Waiting, permissionPane); err != nil {
				t.Fatalf("relayChildQuestion detached: %v", err)
			}
			if _, found, err := st.HeadMessage(creator.ID); err != nil {
				t.Fatalf("HeadMessage: %v", err)
			} else if found {
				t.Fatal("the creator of a detached session was sent a relay about it")
			}

			if err := p.relayChildRest(nested, resting); err != nil {
				t.Fatalf("relayChildRest nested: %v", err)
			}
			head, found, err := st.HeadMessage(creator.ID)
			if err != nil {
				t.Fatalf("HeadMessage: %v", err)
			}
			if !found || head.SenderID != nested.ID {
				t.Fatalf("nested child's rest was not relayed to its creator: found=%v head=%+v", found, head)
			}
		})
	}
}

// A detached session's question stays in front of the operator: nobody is
// on it to fold it away for.
func TestADetachedSpawnsQuestionIsNotFoldedForItsCreator(t *testing.T) {
	live := map[string]bool{"parent01": true}
	detached := stoppedChild(time.Minute)
	detached.ParentID, detached.SpawnedBy = "", "parent01"
	m := ownedBoard(t, liveParent(), detached)
	if m.parentOwns(m.sessions[1], time.Now(), live) {
		t.Error("a detached session's question was folded away as its creator's to answer")
	}

	nested := stoppedChild(time.Minute)
	nested.SpawnedBy = "parent01"
	m = ownedBoard(t, liveParent(), nested)
	if !m.parentOwns(m.sessions[1], time.Now(), live) {
		t.Error("a nested child's question is no longer its creator's to answer")
	}
}
