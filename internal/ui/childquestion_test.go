package ui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// askPane is a pane holding an AskUserQuestion, in the shape dialog.Parse
// reads: the options above the legend, and the legend that identifies it.
const askPane = `Which storefront should the census cover?

❯ 1. Germany only
  2. Every storefront

Enter to select · ↑/↓ to navigate · Esc to cancel
`

// permissionPane is a pane holding a permission prompt: numbered choices like
// a dialog's, and the "Enter to confirm" legend that says it is not one.
const permissionPane = "Do you want to proceed?\n\n  1. Yes\n  2. No\n\nEnter to confirm · Esc to reject\n"

// pollerWithStore is a poller with nothing but a store: nothing here needs a
// pane, a tmux socket or a pass.
func pollerWithStore(t *testing.T) (*poller, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(tmuxtest.ScratchDir(t), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return &poller{store: st}, st
}

func seedSession(t *testing.T, st *store.Store, sess store.Session) store.Session {
	t.Helper()
	if sess.Tool == "" {
		sess.Tool = "claude"
	}
	if err := st.CreateSession(sess); err != nil {
		t.Fatalf("create session %s: %v", sess.ID, err)
	}
	return sess
}

func TestChildQuestionMessageNamesTheChildAndHowToAnswer(t *testing.T) {
	held, ok := dialog.Parse(askPane)
	if !ok {
		t.Fatal("the fixture pane does not parse as a dialog")
	}
	body := childQuestionMessage(store.Session{ID: "abc12345", Name: "sampleapp-reach-census"}, held)
	for _, want := range []string{
		"sampleapp-reach-census",
		"abc12345",
		"Which storefront should the census cover?",
		"Germany only",
		"answer_session",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("relayed message does not mention %q:\n%s", want, body)
		}
	}
}

func TestRelayChildQuestionQueuesForTheParent(t *testing.T) {
	p, st := pollerWithStore(t)
	parent := seedSession(t, st, store.Session{ID: "parent01", Name: "site-graph-endpoint", Status: status.Working})
	child := seedSession(t, st, store.Session{ID: "child001", Name: "sampleapp-reach-census", ParentID: parent.ID, Status: status.Working})

	if err := p.relayChildQuestion(child, status.Waiting, askPane); err != nil {
		t.Fatalf("relayChildQuestion: %v", err)
	}
	head, found, err := st.HeadMessage(parent.ID)
	if err != nil {
		t.Fatalf("HeadMessage: %v", err)
	}
	if !found {
		t.Fatal("the parent was told nothing about its child's question")
	}
	if head.SenderID != child.ID {
		t.Errorf("message came from %q, want the child %q", head.SenderID, child.ID)
	}
	if !strings.Contains(head.Body, "Which storefront should the census cover?") {
		t.Errorf("message does not carry the question:\n%s", head.Body)
	}
}

func TestRelayChildQuestionIgnoresWhatIsNotItsToRelay(t *testing.T) {
	parentID := "parent01"
	cases := []struct {
		name   string
		sess   store.Session
		status string
		pane   string
	}{
		{
			name:   "a session with no parent",
			sess:   store.Session{ID: "orphan01", Name: "unparented", Status: status.Working},
			status: status.Waiting,
			pane:   askPane,
		},
		{
			name:   "a child that has not stopped",
			sess:   store.Session{ID: "child002", ParentID: parentID, Status: status.Working},
			status: status.Working,
			pane:   askPane,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, st := pollerWithStore(t)
			seedSession(t, st, store.Session{ID: parentID, Name: "site-graph-endpoint", Status: status.Working})
			child := seedSession(t, st, tc.sess)
			if err := p.relayChildQuestion(child, tc.status, tc.pane); err != nil {
				t.Fatalf("relayChildQuestion: %v", err)
			}
			if _, found, err := st.HeadMessage(parentID); err != nil {
				t.Fatalf("HeadMessage: %v", err)
			} else if found {
				t.Error("something was relayed that should not have been")
			}
		})
	}
}

func TestChildWaitMessageNamesTheChildAndNotThePane(t *testing.T) {
	body := childWaitMessage(store.Session{ID: "child003", Name: "retailer-shell-probe"})
	for _, want := range []string{"retailer-shell-probe", "child003", "answer_session cannot answer it"} {
		if !strings.Contains(body, want) {
			t.Errorf("relayed message does not mention %q:\n%s", want, body)
		}
	}
	// The pane is the one thing this must not carry: these screens hold live
	// API keys, and a permission prompt quotes the command it is asking about.
	if strings.Contains(body, "Do you want to proceed?") {
		t.Errorf("the message carries the pane:\n%s", body)
	}
}

func TestRelayChildQuestionTellsTheParentAboutAWaitItCannotRead(t *testing.T) {
	p, st := pollerWithStore(t)
	parent := seedSession(t, st, store.Session{ID: "parent01", Name: "site-graph-endpoint", Status: status.Working})
	child := seedSession(t, st, store.Session{ID: "child003", Name: "retailer-shell-probe", ParentID: parent.ID, Status: status.Working})

	if err := p.relayChildQuestion(child, status.Waiting, permissionPane); err != nil {
		t.Fatalf("relayChildQuestion: %v", err)
	}
	head, found, err := st.HeadMessage(parent.ID)
	if err != nil {
		t.Fatalf("HeadMessage: %v", err)
	}
	if !found {
		t.Fatal("the parent was told nothing about its child's stop")
	}
	if !strings.Contains(head.Body, "answer_session cannot answer it") {
		t.Errorf("the message does not say the parent cannot answer it:\n%s", head.Body)
	}
	if strings.Contains(head.Body, "Do you want to proceed?") {
		t.Errorf("the message carries the pane:\n%s", head.Body)
	}
}

// The relay goes to the session that spawned the row, not to the root it is
// drawn under. Reading it off the tree flooded roots with questions about work
// they had not assigned -- ci-cd-build-speed gave up out loud at 05:01:45,
// "Same stale wave -- fifth one... I'll stop reading them one by one" -- while
// the session that could actually answer heard nothing.
func TestRelayChildQuestionQueuesForTheSpawnerNotTheRoot(t *testing.T) {
	p, st := pollerWithStore(t)
	root := seedSession(t, st, store.Session{ID: "parent01", Name: "root-manager", Status: status.Working})
	spawner := seedSession(t, st, store.Session{
		ID: "child001", Name: "go-ci-batch-width-retune",
		ParentID: root.ID, SpawnedBy: root.ID, Status: status.Working,
	})
	grand := seedSession(t, st, store.Session{
		ID: "child002", Name: "sampleapp-reach-census",
		ParentID: root.ID, SpawnedBy: spawner.ID, Status: status.Working,
	})

	if err := p.relayChildQuestion(grand, status.Waiting, askPane); err != nil {
		t.Fatalf("relayChildQuestion: %v", err)
	}
	head, found, err := st.HeadMessage(spawner.ID)
	if err != nil {
		t.Fatalf("HeadMessage: %v", err)
	}
	if !found {
		t.Fatal("the session that spawned it was told nothing about its child's question")
	}
	if head.SenderID != grand.ID {
		t.Errorf("message came from %q, want %q", head.SenderID, grand.ID)
	}
	if _, atRoot, err := st.HeadMessage(root.ID); err != nil {
		t.Fatalf("HeadMessage: %v", err)
	} else if atRoot {
		t.Error("the root was told about a question it did not assign and cannot answer")
	}
}

// The same for a child coming to rest: its report belongs to whoever asked
// for the work.
func TestRelayChildRestQueuesForTheSpawnerNotTheRoot(t *testing.T) {
	p, st := pollerWithStore(t)
	root := seedSession(t, st, store.Session{ID: "parent01", Name: "root-manager", Status: status.Working})
	spawner := seedSession(t, st, store.Session{
		ID: "child001", Name: "spawner", ParentID: root.ID, SpawnedBy: root.ID, Status: status.Working,
	})
	grand := seedSession(t, st, store.Session{
		ID: "child002", Name: "grandchild", ParentID: root.ID, SpawnedBy: spawner.ID, Status: status.Working,
	})

	if err := p.relayChildRest(grand, status.Finished); err != nil {
		t.Fatalf("relayChildRest: %v", err)
	}
	if _, found, err := st.HeadMessage(spawner.ID); err != nil {
		t.Fatalf("HeadMessage: %v", err)
	} else if !found {
		t.Fatal("the session that spawned it was not told it had finished")
	}
	if _, atRoot, err := st.HeadMessage(root.ID); err != nil {
		t.Fatalf("HeadMessage: %v", err)
	} else if atRoot {
		t.Error("the root was told about a child it did not spawn")
	}
}
