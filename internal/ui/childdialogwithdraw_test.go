package ui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// limitMenuPane is a choices dialog that is not a question: a usage-limit
// menu, which goes away without an answer when the child is moved to
// another account.
const limitMenuPane = "What do you want to do?\n\n" +
	"❯ 1. Stop and wait for limit to reset\n" +
	"  2. Wait here, then continue automatically\n\n" +
	"Enter to confirm · Esc to cancel\n"

// limitMenuAskPane is the parent's own AskUserQuestion putting that menu to
// its user, as the relay tells it to.
const limitMenuAskPane = "What do you want to do?\n\n" +
	"❯ 1. Stop and wait for limit to reset\n" +
	"  2. Wait here, then continue automatically\n\n" +
	"Enter to select · ↑/↓ to navigate · Esc to cancel\n"

// relayScreen relays the limit menu, delivers it, and leaves the parent
// asking it at its own pane.
func relayScreen(t *testing.T, p *poller, st *store.Store, panes *fakePanes, parent *store.Session, child store.Session, at time.Time) {
	t.Helper()
	if err := p.watchChildDialog(child, status.Waiting, limitMenuPane, at); err != nil {
		t.Fatal(err)
	}
	head, found, err := st.HeadMessage(parent.ID)
	if err != nil || !found || !strings.Contains(head.Body, "Stop and wait for limit to reset") {
		t.Fatalf("screen relay not at the head of the parent's queue: %+v %v %v", head, found, err)
	}
	if err := st.MarkDelivered(head.ID, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	parent.Status = status.Waiting
	if err := st.UpdateStatus(parent.ID, status.Waiting); err != nil {
		t.Fatal(err)
	}
	panes.screens[parent.ID] = limitMenuAskPane
}

// A relayed screen dialog (no question tool call behind it) recorded no
// questions, so its clearing withdrew nothing and the parent's copy stayed
// open. The child here leaves waiting for errored, as a limit reads once
// the pane's status classes the menu as a limit.
func TestAClearedScreenRelayWithdrawsTheParentsOpenCopy(t *testing.T) {
	for _, next := range []string{status.Working, status.Errored, status.Dead} {
		t.Run(next, func(t *testing.T) {
			p, st, panes, parent, child := clearedFixture(t, status.Working)
			start := time.Now()
			relayScreen(t, p, st, panes, &parent, child, start)
			if err := p.watchChildDialog(child, next, "", start.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			if got, want := panes.keys[parent.ID], [][]string{{"Escape"}}; !reflect.DeepEqual(got, want) {
				t.Fatalf("parent keyed %v, want its copy of the menu dismissed with %v", got, want)
			}
			notice, ok := clearedNoticeIn(waitingFor(t, st, parent.ID))
			if !ok {
				t.Fatal("parent not told the menu cleared")
			}
			full, err := st.Message(notice.ID, child.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(full.Body, "What do you want to do?") {
				t.Errorf("notice does not say which dialog cleared:\n%s", full.Body)
			}
		})
	}
}

func TestAnUnreadScreenRelayIsDroppedWhenItsDialogClears(t *testing.T) {
	p, st, panes, parent, child := clearedFixture(t, status.Working)
	start := time.Now()
	if err := p.watchChildDialog(child, status.Waiting, limitMenuPane, start); err != nil {
		t.Fatal(err)
	}
	if err := p.watchChildDialog(child, status.Errored, "", start.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if msgs := waitingFor(t, st, parent.ID); len(msgs) != 0 {
		t.Errorf("the parent will still be handed a menu that is gone: %+v", msgs)
	}
	if len(panes.keys) != 0 {
		t.Errorf("keyed %v into a parent that was not asking", panes.keys)
	}
}

// The sweep drops the queued relay of a child absent from the pass, even
// though the child's row is now archived.
func TestARelayFromAChildThatLeftThePassIsWithdrawn(t *testing.T) {
	p, st, panes, parent, child := clearedFixture(t, status.Working)
	start := time.Now()
	if err := p.watchChildDialog(child, status.Waiting, limitMenuPane, start); err != nil {
		t.Fatal(err)
	}
	if err := st.SetArchived(child.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := p.withdrawDepartedChildDialogs(map[string]bool{parent.ID: true}, start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if msgs := waitingFor(t, st, parent.ID); len(msgs) != 0 {
		t.Errorf("an archived child's relay is still queued for the parent: %+v", msgs)
	}
	if _, kept := p.childDialogs[child.ID]; kept {
		t.Error("the departed child's relay is still tracked")
	}
	if len(panes.keys) != 0 {
		t.Errorf("keyed %v into a parent that was not asking", panes.keys)
	}
}

func TestADepartedChildsDeliveredRelayDismissesTheParentsCopy(t *testing.T) {
	p, st, panes, parent, child := clearedFixture(t, status.Working)
	start := time.Now()
	relayScreen(t, p, st, panes, &parent, child, start)
	if err := st.SetArchived(child.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := p.withdrawDepartedChildDialogs(map[string]bool{parent.ID: true}, start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got, want := panes.keys[parent.ID], [][]string{{"Escape"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("parent keyed %v, want its copy dismissed with %v", got, want)
	}
	if _, ok := clearedNoticeIn(waitingFor(t, st, parent.ID)); !ok {
		t.Error("parent not told the departed child's dialog cleared")
	}
}

// A child still in the pass keeps its relay: the sweep is only for rows
// the pass no longer visits.
func TestAChildStillInThePassKeepsItsRelay(t *testing.T) {
	p, st, _, parent, child := clearedFixture(t, status.Working)
	start := time.Now()
	if err := p.watchChildDialog(child, status.Waiting, limitMenuPane, start); err != nil {
		t.Fatal(err)
	}
	if err := p.withdrawDepartedChildDialogs(map[string]bool{parent.ID: true, child.ID: true}, start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if msgs := waitingFor(t, st, parent.ID); len(msgs) != 1 {
		t.Errorf("a live child's relay was withdrawn: %+v", msgs)
	}
	if _, kept := p.childDialogs[child.ID]; !kept {
		t.Error("a live child's relay is no longer tracked")
	}
}

// A child moved to another account is archived and replaced by a new
// session. The pass skips archived rows, so the old row never reached
// watchChildDialog again and its queued relay was delivered hours later.
// This drives a whole pass, so it covers the pass handing the sweep the
// rows it visited.
func TestAPassWithdrawsTheRelayOfAChildArchivedSinceTheLastPass(t *testing.T) {
	m := buildModel(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	parent := seedSession(t, m.store, store.Session{ID: "parent01", Name: "prune-memory-run", Cwd: t.TempDir(), Status: status.Working})
	child := seedSession(t, m.store, store.Session{ID: "child001", Name: "docker-headless-start", Cwd: t.TempDir(), ParentID: parent.ID, Status: status.Waiting})
	if err := m.poller.watchChildDialog(child, status.Waiting, limitMenuPane, time.Now()); err != nil {
		t.Fatal(err)
	}
	if msgs := waitingFor(t, m.store, parent.ID); len(msgs) != 1 {
		t.Fatalf("relay not queued: %+v", msgs)
	}
	if err := m.store.SetArchived(child.ID, true); err != nil {
		t.Fatal(err)
	}
	if msg, failed := m.poller.refreshOnce().(errMsg); failed {
		t.Fatalf("pass failed: %v", msg.err)
	}
	if msgs := waitingFor(t, m.store, parent.ID); len(msgs) != 0 {
		t.Errorf("the archived child's relay is still queued after a pass: %+v", msgs)
	}
	if _, kept := m.poller.childDialogs[child.ID]; kept {
		t.Error("the archived child's relay is still tracked after a pass")
	}
}

// Two children on the same screen: the parent's copy of one reads the same
// as the other's, since the screen's prompt names neither. One clearing must
// not dismiss the parent's question while the other still waits on it.
func TestAClearedScreenRelayLeavesASiblingsCopyOpen(t *testing.T) {
	p, st, panes, parent, childA := clearedFixture(t, status.Working)
	childB := seedSession(t, st, store.Session{ID: "child002", Name: "fixture-refresh", ParentID: parent.ID, Status: status.Waiting})
	start := time.Now()
	if err := p.watchChildDialog(childA, status.Waiting, limitMenuPane, start); err != nil {
		t.Fatal(err)
	}
	if err := p.watchChildDialog(childB, status.Waiting, limitMenuPane, start); err != nil {
		t.Fatal(err)
	}
	for _, msg := range waitingFor(t, st, parent.ID) {
		if err := st.MarkDelivered(msg.ID, start.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	parent.Status = status.Waiting
	if err := st.UpdateStatus(parent.ID, status.Waiting); err != nil {
		t.Fatal(err)
	}
	// The parent is putting B's copy to its user.
	panes.screens[parent.ID] = limitMenuAskPane
	if err := p.watchChildDialog(childA, status.Errored, "", start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if keys := panes.keys[parent.ID]; len(keys) != 0 {
		t.Fatalf("keyed %v into the parent's question about a sibling still waiting on it", keys)
	}
	if _, kept := p.childDialogs[childB.ID]; !kept {
		t.Error("the sibling's relay is no longer tracked")
	}
	if _, ok := clearedNoticeIn(waitingFor(t, st, parent.ID)); !ok {
		t.Error("the parent was not told the cleared child's dialog is gone")
	}
}
