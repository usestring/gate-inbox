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
	latest, found, _ := st.HeadMessage(parent.ID)
	if !found || !strings.Contains(latest.Body, "Merge the pull request now?") {
		t.Errorf("the new question was not relayed after withdrawing the old one: %+v", latest)
	}
}
