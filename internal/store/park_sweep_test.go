package store

import (
	"testing"
	"time"
)

// A parked child reads dead with no pane once the host reboots, the exited
// case the child sweep files away. Unpark drops an archived row as gone, so
// filing one loses it: on 2026-11-07 a board started after a park and a
// reboot filed about 56 parked children this way and none came back.
func TestAutoArchivableChildrenLeavesParkedAndHibernatedChildren(t *testing.T) {
	tests := []struct {
		name     string
		reported bool
		parked   bool
		hiber    bool
		want     string
	}{
		{name: "parked child dead past the window", parked: true, want: ""},
		{name: "parked child whose report was delivered", reported: true, parked: true, want: ""},
		{name: "hibernated child dead past the window", hiber: true, want: ""},
		{name: "unparked child dead past the window", want: "kid"},
		{name: "unparked child whose report was delivered", reported: true, want: "kid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newTestStore(t)
			if err := st.CreateSession(sample("p1", "")); err != nil {
				t.Fatalf("parent: %v", err)
			}
			age := time.Hour
			if tt.reported {
				// Inside the window, so only the delivered report qualifies it.
				age = time.Minute
			}
			childOf(t, st, "p1", "kid", "dead", age)
			if tt.reported {
				id, _, err := st.Enqueue(InboxMessage{
					SessionID: "p1", SenderID: "kid", SenderName: "probe",
					Body: "done: the answer is 42", SentAt: time.Now(),
				}, DefaultInboxLimits)
				if err != nil {
					t.Fatalf("Enqueue: %v", err)
				}
				if err := st.MarkDelivered(id, time.Now()); err != nil {
					t.Fatalf("MarkDelivered: %v", err)
				}
			}
			// A second, unrelated parked id keeps the set from being only
			// the row under test.
			if err := st.AddParked([]string{"elsewhere"}); err != nil {
				t.Fatalf("AddParked: %v", err)
			}
			if tt.parked {
				if err := st.AddParked([]string{"kid"}); err != nil {
					t.Fatalf("AddParked: %v", err)
				}
			}
			if tt.hiber {
				if err := st.AddHibernated([]string{"kid"}); err != nil {
					t.Fatalf("AddHibernated: %v", err)
				}
			}
			if got := archivable(t, st, 30*time.Minute); got != tt.want {
				t.Fatalf("archivable = %q, want %q", got, tt.want)
			}
		})
	}
}
