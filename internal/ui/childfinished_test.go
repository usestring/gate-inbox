package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

func TestChildRestMessageNamesTheChildAndWhereTheWorkIs(t *testing.T) {
	body := childRestMessage(store.Session{ID: "child001", Name: "sampleapp-reach-census"}, restRelay[status.Finished])
	for _, want := range []string{
		"sampleapp-reach-census",
		"child001",
		"has finished its turn",
		"read_session",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("relayed message does not mention %q:\n%s", want, body)
		}
	}
}

// A dead child has no turn that ended and no prompt to send to, so the tail
// that suits a finished one is false for it twice over. Found by driving the
// real board: the message told a parent to send_session a row whose pane had
// exited.
func TestChildRestMessageDoesNotOfferADeadChildAPrompt(t *testing.T) {
	body := childRestMessage(store.Session{ID: "child001", Name: "sampleapp-reach-census"}, restRelay[status.Dead])
	for _, want := range []string{"is gone", "read_session", "revive_session"} {
		if !strings.Contains(body, want) {
			t.Errorf("relayed message does not mention %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "turn ended") {
		t.Errorf("a killed child is told to have ended a turn:\n%s", body)
	}
	if !strings.Contains(body, "send_session cannot reach it") {
		t.Errorf("the message offers a dead row a prompt it does not have:\n%s", body)
	}
}

// Every ending has to name the child it is about, since the id is what the
// parent acts on and the shared prefix carries only the name.
func TestEveryRestEndingNamesTheSession(t *testing.T) {
	for state := range restRelay {
		body := childRestMessage(store.Session{ID: "child001", Name: "census"}, restRelay[state])
		if strings.Count(body, "child001") < 2 {
			t.Errorf("%s ending does not name the session to act on:\n%s", state, body)
		}
		if strings.Contains(body, "%!") {
			t.Errorf("%s ending has a broken verb:\n%s", state, body)
		}
	}
}

func TestRelayChildRestQueuesForTheParent(t *testing.T) {
	cases := []struct {
		status string
		says   string
	}{
		{status.Finished, "has finished its turn"},
		{status.Errored, "has stopped with an error"},
		{status.Dead, "is gone"},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			p, st := pollerWithStore(t)
			parent := seedSession(t, st, store.Session{ID: "parent01", Name: "site-graph-endpoint", Status: status.Working})
			child := seedSession(t, st, store.Session{ID: "child001", Name: "sampleapp-reach-census", ParentID: parent.ID, Status: status.Working})

			if err := p.relayChildRest(child, tc.status); err != nil {
				t.Fatalf("relayChildRest: %v", err)
			}
			head, found, err := st.HeadMessage(parent.ID)
			if err != nil {
				t.Fatalf("HeadMessage: %v", err)
			}
			if !found {
				t.Fatal("the parent was told nothing about its child coming to rest")
			}
			if head.SenderID != child.ID {
				t.Errorf("message came from %q, want the child %q", head.SenderID, child.ID)
			}
			if !strings.Contains(head.Body, tc.says) {
				t.Errorf("message does not say how the child came to rest:\n%s", head.Body)
			}
		})
	}
}

func TestRelayChildRestIgnoresWhatIsNotItsToRelay(t *testing.T) {
	parentID := "parent01"
	cases := []struct {
		name   string
		sess   store.Session
		status string
	}{
		{
			name:   "a session with no parent",
			sess:   store.Session{ID: "orphan01", Name: "unparented", Status: status.Working},
			status: status.Finished,
		},
		{
			name:   "a child that is still working",
			sess:   store.Session{ID: "child002", ParentID: parentID, Status: status.Working},
			status: status.Working,
		},
		{
			// The question relay's, not this one's: a child holding a dialog
			// has not come to rest, and telling the parent twice about one
			// stop is worse than telling it once.
			name:   "a child that has stopped on a question",
			sess:   store.Session{ID: "child003", ParentID: parentID, Status: status.Working},
			status: status.Waiting,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, st := pollerWithStore(t)
			seedSession(t, st, store.Session{ID: parentID, Name: "site-graph-endpoint", Status: status.Working})
			child := seedSession(t, st, tc.sess)
			if err := p.relayChildRest(child, tc.status); err != nil {
				t.Fatalf("relayChildRest: %v", err)
			}
			if _, found, err := st.HeadMessage(parentID); err != nil {
				t.Fatalf("HeadMessage: %v", err)
			} else if found {
				t.Error("something was relayed that should not have been")
			}
		})
	}
}

// A child can outlive its parent's row: the store lets a parent be deleted
// and the child keeps the id. Following that dangling link is a read of a row
// that is not there, and before ignoreDeletedSession knew about one it ended
// the whole poll pass -- the board drew nothing at all.
func TestRelayChildRestSurvivesADeletedParent(t *testing.T) {
	p, st := pollerWithStore(t)
	parent := seedSession(t, st, store.Session{ID: "parent01", Name: "site-graph-endpoint", Status: status.Working})
	child := seedSession(t, st, store.Session{ID: "child001", Name: "sampleapp-reach-census", ParentID: parent.ID, Status: status.Working})
	if err := st.Delete(parent.ID); err != nil {
		t.Fatalf("delete parent: %v", err)
	}

	if err := p.relayChildRest(child, status.Finished); err != nil {
		t.Fatalf("relayChildRest on an orphan: %v", err)
	}
}

// A shell opened under a session with T or create_terminal is a child row
// like any other, and its closing is not news: whoever opened it closed it.
func TestRelayChildRestSkipsASessionsOwnShell(t *testing.T) {
	p, st := pollerWithStore(t)
	p.shellTools = map[string]bool{"terminal": true}
	parent := seedSession(t, st, store.Session{ID: "parent01", Name: "site-graph-endpoint", Status: status.Working})
	shell := seedSession(t, st, store.Session{ID: "shell001", Name: "site-graph-endpoint sh", Tool: "terminal", ParentID: parent.ID, Status: status.Idle})

	if err := p.relayChildRest(shell, status.Dead); err != nil {
		t.Fatalf("relayChildRest: %v", err)
	}
	if _, found, err := st.HeadMessage(parent.ID); err != nil {
		t.Fatalf("HeadMessage: %v", err)
	} else if found {
		t.Error("a closed shell was relayed to the session that opened it")
	}
}

// A full queue is the one refusal that loses news: the parent is never told
// this child stopped, nothing retries, and the child has no turn left to
// read an error in. It has to reach the operator instead of the log.
func TestAChildRestLostToAFullQueueIsReported(t *testing.T) {
	p, st := pollerWithStore(t)
	parent := seedSession(t, st, store.Session{ID: "parent01", Name: "site-graph-endpoint", Status: status.Working})
	for i := range store.DefaultInboxLimits.QueueCap {
		body := fmt.Sprintf("filler %d", i)
		if _, _, err := st.Enqueue(store.InboxMessage{
			SessionID:   parent.ID,
			SenderID:    fmt.Sprintf("other%03d", i),
			SenderName:  "another-child",
			Body:        body,
			Fingerprint: store.Fingerprint(body),
			SentAt:      time.Now(),
		}, store.DefaultInboxLimits); err != nil {
			t.Fatalf("filling the parent's queue at %d: %v", i, err)
		}
	}
	child := seedSession(t, st, store.Session{ID: "child001", Name: "retry-path-fix", ParentID: parent.ID, Status: status.Working})

	err := p.relayChildRest(child, status.Finished)
	if err == nil {
		t.Fatal("a child's rest was lost to a full queue in silence")
	}
	if !strings.Contains(err.Error(), child.Name) || !strings.Contains(err.Error(), parent.Name) {
		t.Fatalf("the report names neither end of what was lost: %v", err)
	}
}

// A child that stops, is sent on and stops again leaves its parent the
// current notice rather than a queue of stale ones.
func TestARepeatedChildRestReplacesTheNoticeAlreadyQueued(t *testing.T) {
	p, st := pollerWithStore(t)
	parent := seedSession(t, st, store.Session{ID: "parent01", Name: "site-graph-endpoint", Status: status.Working})
	child := seedSession(t, st, store.Session{ID: "child001", Name: "retry-path-fix", ParentID: parent.ID, Status: status.Working})

	if err := p.relayChildRest(child, status.Finished); err != nil {
		t.Fatalf("relayChildRest: %v", err)
	}
	if err := p.relayChildRest(child, status.Errored); err != nil {
		t.Fatalf("relayChildRest: %v", err)
	}
	queued, err := st.QueuedCount(parent.ID)
	if err != nil {
		t.Fatalf("QueuedCount: %v", err)
	}
	if queued != 1 {
		t.Fatalf("the parent holds %d notices about one child, want the current one", queued)
	}
	head, found, err := st.HeadMessage(parent.ID)
	if err != nil || !found {
		t.Fatalf("HeadMessage: %v, found=%v", err, found)
	}
	if !strings.Contains(head.Body, "stopped with an error") {
		t.Fatalf("the parent reads the stale notice: %q", head.Body)
	}
}
