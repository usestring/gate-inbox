package ui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

type fakePanes struct {
	screens map[string]string
	keys    map[string][][]string
}

func (f *fakePanes) CapturePane(id string) (string, error) { return f.screens[id], nil }
func (f *fakePanes) SendKeys(id string, keys ...string) error {
	if f.keys == nil {
		f.keys = map[string][][]string{}
	}
	f.keys[id] = append(f.keys[id], keys)
	return nil
}

func clearedFixture(t *testing.T, parentStatus string) (*poller, *store.Store, *fakePanes, store.Session, store.Session) {
	t.Helper()
	p, st := pollerWithStore(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	panes := &fakePanes{screens: map[string]string{}}
	p.panes = panes
	parent := seedSession(t, st, store.Session{ID: "parent01", Name: "prune-memory-run", Status: parentStatus})
	child := seedSession(t, st, store.Session{ID: "child001", Name: "docker-headless-start", ParentID: parent.ID, Status: status.Waiting})
	return p, st, panes, parent, child
}

// The reported bug: the user answers the child at its pane while the
// parent's own copy of the question is open, and then answers it again.
func TestAChildAnsweredAtItsPaneWithdrawsTheParentsOpenCopy(t *testing.T) {
	p, st, panes, parent, child := clearedFixture(t, status.Working)
	start := time.Now()
	if err := p.watchChildDialog(child, status.Waiting, askPane, start); err != nil {
		t.Fatal(err)
	}
	head, found, err := st.HeadMessage(parent.ID)
	if err != nil || !found {
		t.Fatalf("relay not queued: %v %v", found, err)
	}
	if !strings.Contains(head.Body, "never ask it twice") {
		t.Errorf("the relay does not say a pane answer withdraws it:\n%s", head.Body)
	}
	if err := st.MarkDelivered(head.ID, start.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	// The parent put it to the user; the user answered at the child's pane.
	parent.Status = status.Waiting
	if err := st.UpdateStatus(parent.ID, status.Waiting); err != nil {
		t.Fatal(err)
	}
	panes.screens[parent.ID] = askPane
	if err := p.watchChildDialog(child, status.Working, "", start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got, want := panes.keys[parent.ID], [][]string{{"Escape"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("parent keyed %v, want its duplicate dialog dismissed with %v", got, want)
	}
	next, found, err := st.HeadMessage(parent.ID)
	if err != nil || !found {
		t.Fatalf("the parent was not told the child moved on: %v %v", found, err)
	}
	for _, want := range []string{"no longer on the dialog", "Do not put it to your user", "dismissed it"} {
		if !strings.Contains(next.Body, want) {
			t.Errorf("notice lacks %q:\n%s", want, next.Body)
		}
	}
}

func TestAnUnreadRelayIsDroppedQuietlyWhenTheChildMovesOn(t *testing.T) {
	p, st, panes, parent, child := clearedFixture(t, status.Working)
	start := time.Now()
	if err := p.watchChildDialog(child, status.Waiting, askPane, start); err != nil {
		t.Fatal(err)
	}
	if err := p.watchChildDialog(child, status.Working, "", start.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := st.HeadMessage(parent.ID); found {
		t.Error("the parent will still be handed a question that is already answered")
	}
	if len(panes.keys) != 0 {
		t.Errorf("keyed %v into a parent that was not asking", panes.keys)
	}
}

func TestAParentAskingSomethingElseIsNeverKeyed(t *testing.T) {
	p, st, panes, parent, child := clearedFixture(t, status.Working)
	start := time.Now()
	if err := p.watchChildDialog(child, status.Waiting, askPane, start); err != nil {
		t.Fatal(err)
	}
	head, _, _ := st.HeadMessage(parent.ID)
	if err := st.MarkDelivered(head.ID, start); err != nil {
		t.Fatal(err)
	}
	parent.Status = status.Waiting
	if err := st.UpdateStatus(parent.ID, status.Waiting); err != nil {
		t.Fatal(err)
	}
	panes.screens[parent.ID] = "Which region should the deploy target?\n\n❯ 1. us-east\n  2. eu-west\n\nEnter to select · ↑/↓ to navigate · Esc to cancel\n"
	if err := p.watchChildDialog(child, status.Working, "", start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(panes.keys) != 0 {
		t.Errorf("keyed %v into a parent asking an unrelated question", panes.keys)
	}
}

// Approval then Merge: the child moved straight to a new question, which
// still clears the first one the parent is holding open.
func TestAChildOnANewQuestionWithdrawsTheParentsCopyOfTheLastOne(t *testing.T) {
	p, st, panes, parent, child := clearedFixture(t, status.Working)
	start := time.Now()
	if err := p.watchChildDialog(child, status.Waiting, askPane, start); err != nil {
		t.Fatal(err)
	}
	head, _, _ := st.HeadMessage(parent.ID)
	if err := st.MarkDelivered(head.ID, start); err != nil {
		t.Fatal(err)
	}
	parent.Status = status.Waiting
	if err := st.UpdateStatus(parent.ID, status.Waiting); err != nil {
		t.Fatal(err)
	}
	panes.screens[parent.ID] = askPane
	next := "Merge the pull request now?\n\n❯ 1. Merge\n  2. Hold\n\nEnter to select · ↑/↓ to navigate · Esc to cancel\n"
	if err := p.watchChildDialog(child, status.Waiting, next, start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := panes.keys[parent.ID]; !reflect.DeepEqual(got, [][]string{{"Escape"}}) {
		t.Fatalf("parent keyed %v, want its stale dialog dismissed", got)
	}
	// The notice that the first question was dismissed comes first, and the
	// relay of the new one stays queued behind it.
	notice, found, _ := st.HeadMessage(parent.ID)
	if !found || !strings.Contains(notice.Body, "dismissed it") {
		t.Fatalf("the parent was not told its open question was dismissed: %+v", notice)
	}
	if err := st.MarkDelivered(notice.ID, start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	latest, found, _ := st.HeadMessage(parent.ID)
	if !found || !strings.Contains(latest.Body, "Merge the pull request now?") {
		t.Errorf("the new question was not relayed after withdrawing the old one: %+v", latest)
	}
}

// relayAndAsk relays the child's askPane dialog, delivers it and leaves the
// parent asking it at its own pane, as a parent does that put it to its user.
func relayAndAsk(t *testing.T, p *poller, st *store.Store, panes *fakePanes, parent *store.Session, child store.Session, at time.Time) {
	t.Helper()
	if err := p.watchChildDialog(child, status.Waiting, askPane, at); err != nil {
		t.Fatal(err)
	}
	head, found, err := st.HeadMessage(parent.ID)
	if err != nil || !found || !strings.Contains(head.Body, "has stopped on a question") {
		t.Fatalf("relay not at the head of the parent's queue: %+v %v %v", head, found, err)
	}
	if err := st.MarkDelivered(head.ID, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	parent.Status = status.Waiting
	if err := st.UpdateStatus(parent.ID, status.Waiting); err != nil {
		t.Fatal(err)
	}
	panes.screens[parent.ID] = askPane
}

// waitingFor is every message still due to reach sessionID, by subject.
func waitingFor(t *testing.T, st *store.Store, sessionID string) []store.InboxMessage {
	t.Helper()
	all, err := st.WaitingMessages()
	if err != nil {
		t.Fatal(err)
	}
	var out []store.InboxMessage
	for _, msg := range all {
		if msg.SessionID == sessionID {
			out = append(out, msg)
		}
	}
	return out
}

func clearedNoticeIn(msgs []store.InboxMessage) (store.InboxMessage, bool) {
	for _, msg := range msgs {
		if strings.HasPrefix(msg.Subject, childDialogClearedSubject+":") {
			return msg, true
		}
	}
	return store.InboxMessage{}, false
}

// The next relay about the child used to withdraw the notice, which shared
// its subject, before the parent read it.
func TestTheDismissalNoticeSurvivesTheChildsNextRelay(t *testing.T) {
	p, st, panes, parent, child := clearedFixture(t, status.Working)
	start := time.Now()
	relayAndAsk(t, p, st, panes, &parent, child, start)
	next := "Merge the pull request now?\n\n❯ 1. Merge\n  2. Hold\n\nEnter to select · ↑/↓ to navigate · Esc to cancel\n"
	if err := p.watchChildDialog(child, status.Waiting, next, start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	panes.screens[parent.ID] = ""
	if err := p.watchChildDialog(child, status.Working, "", start.Add(time.Minute+2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, found := clearedNoticeIn(waitingFor(t, st, parent.ID)); !found {
		t.Fatalf("the dismissal notice was withdrawn by the relays after it: %+v", waitingFor(t, st, parent.ID))
	}
}

// A burst of relays used to fill the per-recipient rate cap and drop the
// notice at exactly the moment a dismissal needed explaining.
func TestTheDismissalNoticeIsNotDroppedByTheRateCap(t *testing.T) {
	p, st, panes, parent, child := clearedFixture(t, status.Working)
	start := time.Now()
	relayAndAsk(t, p, st, panes, &parent, child, start)
	// The relay itself counts against the cap; these fill the rest of it.
	for i := range store.DefaultInboxLimits.RateCap - 1 {
		body := "status update " + string(rune('a'+i))
		if _, _, err := st.Enqueue(store.InboxMessage{
			SessionID: parent.ID, SenderID: child.ID, SenderName: child.Name,
			Body: body, Fingerprint: body, SentAt: start.Add(30 * time.Second),
		}, store.DefaultInboxLimits); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.watchChildDialog(child, status.Working, "", start.Add(40*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := panes.keys[parent.ID]; !reflect.DeepEqual(got, [][]string{{"Escape"}}) {
		t.Fatalf("parent keyed %v, want its duplicate dialog dismissed", got)
	}
	if _, found := clearedNoticeIn(waitingFor(t, st, parent.ID)); !found {
		t.Error("the rate cap dropped the notice explaining a dismissal")
	}
}

// The same question dismissed twice within the dedupe window is two
// dismissals, and the parent must hear of both.
func TestASecondDismissalOfTheSameQuestionStillReachesTheParent(t *testing.T) {
	p, st, panes, parent, child := clearedFixture(t, status.Working)
	start := time.Now()
	for round := range 2 {
		at := start.Add(time.Duration(round) * 3 * time.Minute)
		if round == 0 {
			relayAndAsk(t, p, st, panes, &parent, child, at)
		} else {
			// The child asks again and the parent, taking the first
			// dismissal for a decline, puts the same question to its user
			// again; the identical relay is deduped, the dialog is not.
			if err := p.watchChildDialog(child, status.Waiting, askPane, at); err != nil {
				t.Fatal(err)
			}
			parent.Status = status.Waiting
			if err := st.UpdateStatus(parent.ID, status.Waiting); err != nil {
				t.Fatal(err)
			}
			panes.screens[parent.ID] = askPane
		}
		if err := p.watchChildDialog(child, status.Working, "", at.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		notice, found := clearedNoticeIn(waitingFor(t, st, parent.ID))
		if !found {
			t.Fatalf("round %d: the dismissal notice was not queued", round+1)
		}
		if err := st.MarkDelivered(notice.ID, at.Add(time.Minute+time.Second)); err != nil {
			t.Fatal(err)
		}
		parent.Status = status.Working
		if err := st.UpdateStatus(parent.ID, status.Working); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(panes.keys[parent.ID]); got != 2 {
		t.Errorf("parent dismissed %d times, want 2", got)
	}
}

// The notice says what the user answered at the child, from the child's
// own record, and that the Escape the parent saw was not a decline.
func TestTheDismissalNoticeSaysWhatTheUserAnswered(t *testing.T) {
	p, st, panes, parent, child := clearedFixture(t, status.Working)
	child.AgentSessionID = "c0ffee00-0000-4000-8000-000000000001"
	child.Cwd = "/work"
	start := time.Now()
	relayAndAsk(t, p, st, panes, &parent, child, start)
	answeredAt := start.Add(30 * time.Second).UTC().Format(time.RFC3339)
	writeChildTranscript(t, child,
		`{"type":"assistant","timestamp":"`+start.UTC().Format(time.RFC3339)+`","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_c1","name":"AskUserQuestion","input":{"questions":[{"header":"Scope","question":"Which storefront should the census cover?","multiSelect":false,"options":[{"label":"Germany only","description":"DE"},{"label":"Every storefront","description":"All"}]}]}}]}}`+"\n"+
			`{"type":"user","timestamp":"`+answeredAt+`","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_c1","content":"answered"}]},"toolUseResult":{"answers":{"Which storefront should the census cover?":"Every storefront"}}}`)
	if err := p.watchChildDialog(child, status.Working, "", start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	notice, found, err := st.HeadMessage(parent.ID)
	if err != nil || !found {
		t.Fatalf("no notice: %v %v", found, err)
	}
	for _, want := range []string{
		"Which storefront should the census cover? → Every storefront",
		"not your user declining",
		"Tell your user their answer was received",
	} {
		if !strings.Contains(notice.Body, want) {
			t.Errorf("notice lacks %q:\n%s", want, notice.Body)
		}
	}
}
