package ui

import (
	"reflect"
	"slices"
	"testing"
	"time"
)

// A reply to a session park left at rest brings it back and holds the
// reply for the poller, instead of the dead-row hint.
func TestSendToAParkedSessionUnparksItAndQueuesTheMessage(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "asker", t.TempDir(), "")
	sess := m.sessionRows()[0]
	if err := m.tmux.Kill(sess.ID); err != nil {
		t.Fatalf("kill: %v", err)
	}
	ids := []string{sess.ID}
	if err := m.store.AddParked(ids); err != nil {
		t.Fatalf("AddParked: %v", err)
	}
	if err := m.store.AddDormant(ids); err != nil {
		t.Fatalf("AddDormant: %v", err)
	}
	row, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	m.sendSentence(row, "yes, go ahead", `"yes, go ahead"`, true, false)

	deadline := time.Now().Add(5 * time.Second)
	for !m.tmux.Exists(sess.ID) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !m.tmux.Exists(sess.ID) {
		t.Fatalf("the parked session was not brought back; err bar %q", m.errBar.text)
	}
	got, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatalf("Get after send: %v", err)
	}
	if !slices.Contains(got.PendingInputs, "yes, go ahead") {
		t.Fatalf("pending inputs = %v, want the reply queued", got.PendingInputs)
	}
	for name, read := range map[string]func() ([]string, error){"parked": m.store.Parked, "dormant": m.store.Dormant} {
		if left, _ := read(); len(left) != 0 {
			t.Fatalf("%s set after the send = %v", name, left)
		}
	}
	if m.errBar.text == m.deadSessionHint() {
		t.Fatal("a parked session got the dead-row hint")
	}
}

// A dead row park never stopped still gets the hint and nothing else.
func TestSendToADeadUnparkedSessionStillGetsTheHint(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "gone", t.TempDir(), "")
	sess := m.sessionRows()[0]
	if err := m.tmux.Kill(sess.ID); err != nil {
		t.Fatalf("kill: %v", err)
	}
	row, _ := m.store.Get(sess.ID)
	m.sendSentence(row, "hello", `"hello"`, true, false)
	if m.errBar.text != m.deadSessionHint() {
		t.Fatalf("err bar = %q, want the dead-row hint", m.errBar.text)
	}
	if m.tmux.Exists(sess.ID) {
		t.Fatal("a dead row park never stopped was revived by a send")
	}
	if got, _ := m.store.Get(sess.ID); !reflect.DeepEqual(got.PendingInputs, row.PendingInputs) {
		t.Fatalf("pending inputs changed: %v", got.PendingInputs)
	}
}
