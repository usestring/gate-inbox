package ui

import (
	"os"
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
	questions := dialog.Questions(askPane, nil)
	if len(questions) != 1 {
		t.Fatalf("the fixture pane reads as %d questions, want 1", len(questions))
	}
	body := childQuestionsMessage(store.Session{ID: "abc12345", Name: "sampleapp-reach-census"}, questions)
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
	for _, want := range []string{"retailer-shell-probe", "child003", "read_session", "send_session", "keys", "relay: true"} {
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

func TestRelayChildQuestionRelaysAPermissionPromptWhole(t *testing.T) {
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
	for _, want := range []string{"permission prompt", "Do you want to proceed?", `question: "Do you want to proceed?"`, `options: "Yes", "No"`, "relay: true"} {
		if !strings.Contains(head.Body, want) {
			t.Errorf("the message does not carry %q:\n%s", want, head.Body)
		}
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

func readDialogFixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "dialog", "testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(raw)
}

// The stop that prompted the rewrite: an ordinary four-question
// AskUserQuestion on a narrow pane, its tab labels cut short and its legend
// wrapped, relayed as a permission prompt only a person could answer. It is a
// question, so it is relayed as one, with the call that answers it. The pane
// is the raw capture, which is what the poller now hands the relay.
func TestRelayOfATabbedDialogIsAQuestion(t *testing.T) {
	p, st := pollerWithStore(t)
	parent := seedSession(t, st, store.Session{ID: "parent01", Name: "release-lead", Status: status.Working})
	child := seedSession(t, st, store.Session{ID: "9b7f9d5c", Name: "release-audit", ParentID: parent.ID, Status: status.Working})
	if err := p.relayChildQuestion(child, status.Waiting, readDialogFixture(t, "claude-2.1.283-tabs-w50-second.ansi")); err != nil {
		t.Fatalf("relayChildQuestion: %v", err)
	}
	head, found, err := st.HeadMessage(parent.ID)
	if err != nil || !found {
		t.Fatalf("nothing relayed: %v", err)
	}
	for _, want := range []string{"a dialog asking 4 questions", "Question 2 of 4 [Compliance]", "Question 3 of 4 [np…]",
		"Should the release pipeline block on the licence compliance scan?", "Block", "answer_session",
		"word for word"} {
		if !strings.Contains(head.Body, want) {
			t.Errorf("relayed message does not mention %q:\n%s", want, head.Body)
		}
	}
	if strings.Contains(head.Body, "only a person") {
		t.Errorf("a question was relayed as a person's to answer:\n%s", head.Body)
	}
}

func TestRelayOfAMultiSelectSaysToAnswerItWithTicks(t *testing.T) {
	body := childQuestionsMessage(store.Session{ID: "child004", Name: "ci-checks"},
		dialog.Questions(readDialogFixture(t, "claude-2.1.283-tabs-w44-multiselect.ansi"), nil))
	for _, want := range []string{"Which checks must pass before merge?", "Question 2 is a multi-select", "ticks"} {
		if !strings.Contains(body, want) {
			t.Errorf("relayed message does not mention %q:\n%s", want, body)
		}
	}
}

func TestRelayOfAPermissionPromptCarriesItScrubbed(t *testing.T) {
	p, st := pollerWithStore(t)
	parent := seedSession(t, st, store.Session{ID: "parent01", Name: "site-graph-endpoint", Status: status.Working})
	child := seedSession(t, st, store.Session{ID: "child005", Name: "cleaner", ParentID: parent.ID, Status: status.Working})
	pane := "● Bash(rm -rf build/)\n\n  Bash command\n    API_TOKEN=XXXXXXXXXXXXXXXXXXXXXXXX ./deploy.sh\n\n" +
		"  Do you want to proceed?\n  ❯ 1. Yes\n    2. No\n\n  Enter to confirm · Esc to cancel\n"
	if err := p.relayChildQuestion(child, status.Waiting, pane); err != nil {
		t.Fatalf("relayChildQuestion: %v", err)
	}
	head, found, err := st.HeadMessage(parent.ID)
	if err != nil || !found {
		t.Fatalf("nothing relayed: %v", err)
	}
	for _, want := range []string{"permission prompt", "./deploy.sh", "Do you want to proceed?", "1. Yes", "2. No"} {
		if !strings.Contains(head.Body, want) {
			t.Errorf("relayed message does not mention %q:\n%s", want, head.Body)
		}
	}
	if strings.Contains(head.Body, "XXXXXXXXXXXXXXXXXXXXXXXX") {
		t.Errorf("the message carries a credential:\n%s", head.Body)
	}
}

func TestChildQuestionMessageFlagsAnApprovalForRelay(t *testing.T) {
	questions := []dialog.Question{
		{Index: 1, Header: "Approval", Question: "May I append the allowlist entry?"},
		{Index: 2, Header: "Scope", Question: "Which files?"},
	}
	body := childQuestionsMessage(store.Session{ID: "child005", Name: "gitleaks"}, questions)
	if !strings.Contains(body, "Question 1 is headed Approval") || !strings.Contains(body, "relay: true") {
		t.Fatalf("an Approval question was not flagged for relay:\n%s", body)
	}
	questions[0].Header = "Tooling"
	if body := childQuestionsMessage(store.Session{ID: "child005", Name: "gitleaks"}, questions); strings.Contains(body, "relay: true") {
		t.Fatalf("a message with no Approval question names relay:\n%s", body)
	}
}

// No relay hands a dialog to whoever is at the child's pane or to the
// operator: every kind names the call the parent makes itself.
func TestNoRelayHandsTheDialogOff(t *testing.T) {
	child := store.Session{ID: "child005", Name: "probe", Tool: "claude"}
	bodies := map[string]string{"no dialog": childWaitMessage(child)}
	entries, err := os.ReadDir(filepath.Join("..", "dialog", "testdata"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		body, _ := childDialogBody(child, readDialogFixture(t, entry.Name()), "")
		bodies[entry.Name()] = body
	}
	for name, body := range bodies {
		lower := strings.ToLower(body)
		for _, banned := range []string{"at its pane", "at the pane", "operator", "keystroke", "person at", "a person's"} {
			if strings.Contains(lower, banned) {
				t.Errorf("%s: the relay says %q:\n%s", name, banned, body)
			}
		}
		if !strings.Contains(body, "answer_session") && !strings.Contains(body, "send_session") {
			t.Errorf("%s: the relay names no call to make:\n%s", name, body)
		}
	}
}
