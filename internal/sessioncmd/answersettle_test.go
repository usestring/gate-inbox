package sessioncmd

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/store"
)

// flakyPane is a dialog whose child drops the first dropKeys keystroke calls
// a busy pane can lose, and runs onEnter once Enter has been taken.
type flakyPane struct {
	*tabbedDialog
	dropKeys int
	onEnter  func()
}

func (p *flakyPane) Keys(keys ...string) error {
	if p.dropKeys > 0 {
		p.dropKeys--
		return nil
	}
	err := p.tabbedDialog.Keys(keys...)
	if p.onEnter != nil && slices.Contains(keys, "Enter") {
		p.onEnter()
		p.onEnter = nil
	}
	return err
}

func fastLateRecord(t *testing.T) {
	t.Helper()
	wait := lateRecordWait
	lateRecordWait = 200 * time.Millisecond
	t.Cleanup(func() { lateRecordWait = wait })
}

func settleAnswer(t *testing.T, f *relayFixture, pane dialogPane, reply string) (AnsweredQuestion, error) {
	t.Helper()
	raw, _ := pane.Capture()
	held, ok := dialog.Inspect(ansi.Strip(raw))
	if !ok {
		t.Fatalf("no dialog on:\n%s", raw)
	}
	return answerGuarded(pane, raw, held, reply, f.guard(false))
}

// Keys the child dropped leave its dialog standing: the answer is keyed once
// more and lands, and its row reads keyed rather than failed.
func TestAnAnswerWhoseKeysWereDroppedIsKeyedOnceMore(t *testing.T) {
	fastSettle(t)
	fastLateRecord(t)
	f := newRelayFixture(t, rollout)
	pane := &flakyPane{tabbedDialog: newSingleDialog(rollout, 60, plan...), dropKeys: 1}
	answered, err := settleAnswer(t, f, pane, "Do both")
	if err != nil || !answered.Verified || pane.answers[0] != "Do both" {
		t.Fatalf("answered %+v, err %v, registered %q", answered, err, pane.answers[0])
	}
	if rows := f.rows(t); len(rows) != 1 || rows[0].State != store.AnswerKeyed {
		t.Fatalf("ledger = %+v, want the one row keyed", rows)
	}
}

// A child that took the answer but recorded it after the readback gave up
// is the answer landing: its row reads keyed.
func TestAnAnswerRecordedLateIsKeyed(t *testing.T) {
	fastSettle(t)
	fastLateRecord(t)
	f := newRelayFixture(t, rollout)
	sim := newSingleDialog(rollout, 60, plan...)
	sim.silent = true
	recorded := (&transcript{}).ask(childCallID, relayStart, rollout).
		answer(childCallID, relayStart.Add(time.Minute), map[string]string{rollout.Question: "Do both"})
	done := make(chan struct{})
	t.Cleanup(func() { <-done })
	pane := &flakyPane{tabbedDialog: sim, onEnter: func() {
		delay := 2 * readbackTimeout
		go func() {
			defer close(done)
			time.Sleep(delay)
			path := filepath.Join(f.home, "projects", "any-project", "conv-child.jsonl")
			_ = os.WriteFile(path, []byte(strings.Join(recorded.lines, "\n")+"\n"), 0o644)
		}()
	}}
	readback := readbackTimeout
	readbackTimeout = 50 * time.Millisecond
	t.Cleanup(func() { readbackTimeout = readback })
	answered, err := settleAnswer(t, f, pane, "Do both")
	if err != nil || !answered.Verified {
		t.Fatalf("answered %+v, err %v; want the late record to confirm it", answered, err)
	}
	if rows := f.rows(t); len(rows) != 1 || rows[0].State != store.AnswerKeyed {
		t.Fatalf("ledger = %+v, want the one row keyed", rows)
	}
}

// A dialog gone with no record of the answer is still a failure: nothing is
// keyed again into a child that may be at its prompt, and the row says failed.
func TestAnAnswerGoneWithoutARecordStaysFailed(t *testing.T) {
	fastSettle(t)
	fastLateRecord(t)
	f := newRelayFixture(t, rollout)
	sim := newSingleDialog(rollout, 60, plan...)
	sim.silent = true
	_, err := settleAnswer(t, f, sim, "Do both")
	if err == nil || !strings.Contains(err.Error(), "cannot be confirmed") {
		t.Fatalf("err = %v, want the readback failure", err)
	}
	if enters := strings.Count(strings.Join(sim.keys, " "), "Enter"); enters != 1 {
		t.Fatalf("keys %v: Enter pressed %d times, want once", sim.keys, enters)
	}
	if rows := f.rows(t); len(rows) != 1 || rows[0].State != store.AnswerFailed {
		t.Fatalf("ledger = %+v, want the one row failed", rows)
	}
}

// A child recording another answer than the one keyed is not keyed again.
func TestALateRecordOfAnotherAnswerStaysFailed(t *testing.T) {
	fastSettle(t)
	fastLateRecord(t)
	f := newRelayFixture(t, rollout)
	sim := newSingleDialog(rollout, 60, plan...)
	sim.silent = true
	pane := &flakyPane{tabbedDialog: sim, onEnter: func() {
		(&transcript{}).ask(childCallID, relayStart, rollout).
			answer(childCallID, relayStart.Add(time.Minute), map[string]string{rollout.Question: "Hold for staged UI"}).
			write(t, f.home, "conv-child")
	}}
	if _, err := settleAnswer(t, f, pane, "Do both"); err == nil {
		t.Fatal("an answer the child recorded differently settled as landed")
	}
	if rows := f.rows(t); len(rows) != 1 || rows[0].State != store.AnswerFailed {
		t.Fatalf("ledger = %+v, want the one row failed", rows)
	}
}
