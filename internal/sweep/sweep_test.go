package sweep

import (
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

var now = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)

func row(id, state string, idle time.Duration) store.Session {
	return store.Session{ID: id, Name: id + "-name", Status: state, AgentSessionID: "conv-" + id,
		CreatedAt: now.Add(-idle - time.Hour), LastStatusAt: now.Add(-idle)}
}

func byID(report Report) map[string]Proposal {
	out := map[string]Proposal{}
	for _, p := range report.Proposals {
		out[p.SessionID] = p
	}
	return out
}

func TestEachStaleStateGetsItsAction(t *testing.T) {
	neverLaunched := row("nolaunch", status.Dead, 3*time.Hour)
	neverLaunched.AgentSessionID = ""
	errNoConvo := row("errnone", status.Errored, 30*time.Hour)
	errNoConvo.AgentSessionID = ""
	child := row("child", status.Idle, 100*time.Hour)
	child.ParentID = "parent01"
	sessions := []store.Session{
		row("idle-old", status.Idle, 263*time.Hour),
		row("finished", status.Finished, 80*time.Hour),
		row("idle-new", status.Idle, 10*time.Hour),
		row("working", status.Working, 300*time.Hour),
		row("waiting", status.Waiting, 101*time.Hour),
		row("waitnew", status.Waiting, 2*time.Hour),
		row("errored", status.Errored, 77*time.Hour),
		errNoConvo,
		row("dead", status.Dead, 2*time.Hour),
		row("deadnew", status.Dead, 10*time.Minute),
		neverLaunched,
		child,
	}
	report := Sweep(Input{Sessions: sessions, Now: now,
		Question: func(id string) (Question, bool) {
			return Question{Header: "Approval", Text: "May I apply?"}, id == "waiting"
		}}, DefaultThresholds)
	want := map[string]Action{
		"idle-old": Archive, "finished": Archive, "waiting": Kill, "errored": Revive,
		"errnone": Archive, "dead": Archive, "nolaunch": Archive, "child": Archive,
	}
	got := byID(report)
	if len(got) != len(want) {
		t.Fatalf("proposals for %v, want exactly %v", keys(got), want)
	}
	for id, action := range want {
		if got[id].Action != action {
			t.Errorf("%s = %q, want %q", id, got[id].Action, action)
		}
	}
	if q := got["waiting"].Question; q == nil || q.Text != "May I apply?" {
		t.Errorf("waiting row question = %+v, want the pending dialog", q)
	}
	if !strings.Contains(got["nolaunch"].Reason, "never launched") {
		t.Errorf("never-launched reason = %q", got["nolaunch"].Reason)
	}
	if got["child"].Parent != "parent01" || got["idle-old"].Age != "10d" {
		t.Errorf("child parent %q, idle age %q", got["child"].Parent, got["idle-old"].Age)
	}
	if c := got["waiting"].Calls; len(c) != 1 || c[0] != `mcp__gate-inbox__kill_session {"session_id":"waiting"}` {
		t.Errorf("kill calls = %v", c)
	}
	if c := got["errored"].Calls; len(c) != 1 || c[0] != `mcp__gate-inbox__revive_session {"session_id":"errored"}` {
		t.Errorf("revive calls = %v", c)
	}
	if report.Counts[Archive] != 6 || report.Counts[Kill] != 1 || report.Counts[Revive] != 1 {
		t.Errorf("counts = %v", report.Counts)
	}
	if report.Proposals[0].Action != Archive || report.Proposals[len(report.Proposals)-1].Action != Revive &&
		report.Proposals[len(report.Proposals)-1].Action != Kill {
		t.Errorf("order = %v, want archive first", report.Proposals)
	}
}

func TestMailFindsTheRowsItIsHeldFor(t *testing.T) {
	archived := row("filed", status.Dead, 400*time.Hour)
	archived.Archived = true
	quiet := row("quiet", status.Dead, 400*time.Hour)
	quiet.Archived = true
	sessions := []store.Session{
		archived, quiet,
		row("ended", status.Dead, 30*time.Minute),
		row("busy", status.Working, time.Minute),
		row("asking", status.Waiting, 2*time.Hour),
	}
	sent := func(id int64, to string, age time.Duration) store.InboxMessage {
		return store.InboxMessage{ID: id, SessionID: to, SenderName: "sender", SentAt: now.Add(-age)}
	}
	report := Sweep(Input{Sessions: sessions, Now: now, Messages: []store.InboxMessage{
		sent(1, "filed", 68*time.Hour), sent(2, "ended", 20*time.Hour), sent(3, "busy", 3*time.Hour),
		sent(4, "busy", 5*time.Minute), sent(5, "gone", 30*time.Hour), sent(6, "asking", 2*time.Hour),
	}}, DefaultThresholds)
	got := byID(report)
	if _, ok := got["quiet"]; ok {
		t.Error("an archived row with no mail was proposed")
	}
	filed := got["filed"]
	if filed.Action != Revive || len(filed.Calls) != 2 || !strings.Contains(filed.Calls[0], `"archived":false`) {
		t.Errorf("archived recipient = %+v, want restore then revive", filed)
	}
	if got["ended"].Action != Revive {
		t.Errorf("ended recipient = %q, want revive", got["ended"].Action)
	}
	busy := got["busy"]
	if busy.Action != Check || len(busy.Mail) != 1 || busy.Calls[0] != `mcp__gate-inbox__message_status {"message_id":3}` {
		t.Errorf("running recipient = %+v, want a check of the one old message", busy)
	}
	if !strings.Contains(got["asking"].Reason, "dialog") {
		t.Errorf("waiting recipient reason = %q", got["asking"].Reason)
	}
	if report.Orphaned != 1 {
		t.Errorf("orphaned = %d, want 1", report.Orphaned)
	}
}

func TestFormatSaysNothingWasChanged(t *testing.T) {
	report := Sweep(Input{Sessions: []store.Session{row("idle-old", status.Idle, 263*time.Hour)}, Now: now}, DefaultThresholds)
	text := Format(report)
	for _, want := range []string{"1 to archive, of 1 rows", "Nothing was changed", `archive_session {"session_id":"idle-old"}`, "top-level"} {
		if !strings.Contains(text, want) {
			t.Errorf("report lacks %q:\n%s", want, text)
		}
	}
	if text := Format(Sweep(Input{Now: now}, DefaultThresholds)); !strings.HasPrefix(text, "Nothing stale in 0 rows") {
		t.Errorf("empty report = %q", text)
	}
}

func TestAge(t *testing.T) {
	for d, want := range map[time.Duration]string{
		5 * time.Minute: "5m", 70 * time.Hour: "70h", 263 * time.Hour: "10d",
	} {
		if got := Age(d); got != want {
			t.Errorf("Age(%v) = %q, want %q", d, got, want)
		}
	}
}

func keys(m map[string]Proposal) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
