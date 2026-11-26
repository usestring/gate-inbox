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

// A child moved to another account is archived and replaced by a new
// session. The pass skips archived rows, so the old row never reached
// watchChildDialog again and its queued relay was delivered hours later.
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
