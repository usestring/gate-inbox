package namesweep

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/promptcache"
	"github.com/usestring/gate-inbox/internal/status"
)

// fakeGate answers the door however a test wants, including differently on
// the second look than on the first.
type fakeGate struct {
	states map[string][]string
	caches map[string]promptcache.State
	err    map[string]error
	looks  map[string]int
}

func (g *fakeGate) Status(target Verdict) (string, error) {
	if err := g.err[target.ID]; err != nil {
		return "", err
	}
	if g.looks == nil {
		g.looks = map[string]int{}
	}
	answers := g.states[target.ID]
	if len(answers) == 0 {
		return status.Idle, nil
	}
	i := min(g.looks[target.ID], len(answers)-1)
	g.looks[target.ID]++
	return answers[i], nil
}

func (g *fakeGate) Cache(target Verdict) promptcache.State {
	if state, ok := g.caches[target.ID]; ok {
		return state
	}
	return warm(target.ID)
}

type recorder struct{ sent []string }

func (r *recorder) send(id, text string) error {
	r.sent = append(r.sent, id+": "+text)
	return nil
}

func target(name string) Verdict {
	return Verdict{Candidate: adopted(name, status.Idle), Directive: "rename " + name, Send: true}
}

func run(targets []Verdict, gate Gate, send func(id, text string) error, sleeps *[]time.Duration) Result {
	sleep := func(d time.Duration) {
		if sleeps != nil {
			*sleeps = append(*sleeps, d)
		}
	}
	return Send(targets, gate, send, func() time.Time { return now }, 3*time.Second, sleep, nil)
}

func TestSendTypesTheDirectiveIntoEveryApprovedPane(t *testing.T) {
	var rec recorder
	result := run([]Verdict{target("one"), target("two")}, &fakeGate{}, rec.send, nil)
	if len(result.Sent) != 2 || len(result.Held) != 0 {
		t.Fatalf("sent %d, held %d, want 2 and 0", len(result.Sent), len(result.Held))
	}
	if got := strings.Join(rec.sent, " | "); got != "id-one: rename one | id-two: rename two" {
		t.Fatalf("got %q", got)
	}
}

// The race the re-check exists for: the plan was built and approved while
// this pane was idle, and a permission dialog came up in between. The message
// would be its answer, so the send is refused at the door.
func TestAPaneThatWentToWaitingBetweenTheListingAndTheSendIsRefused(t *testing.T) {
	var rec recorder
	gate := &fakeGate{states: map[string][]string{"id-flipped": {status.Waiting}}}
	result := run([]Verdict{target("flipped"), target("steady")}, gate, rec.send, nil)

	if len(result.Sent) != 1 || result.Sent[0].Name != "steady" {
		t.Fatalf("only the still-idle pane may be typed into, got %+v", result.Sent)
	}
	if len(rec.sent) != 1 || strings.Contains(strings.Join(rec.sent, ""), "flipped") {
		t.Fatalf("nothing may reach the flipped pane, got %v", rec.sent)
	}
	if len(result.Held) != 1 || !strings.Contains(result.Held[0].Reason, "changed since the plan") {
		t.Fatalf("the hold must say the pane changed, got %+v", result.Held)
	}
	if !strings.Contains(result.Held[0].Reason, "answer whatever dialog") {
		t.Fatalf("the hold must say what the message would have done, got %q", result.Held[0].Reason)
	}
}

// The same race on the other gate: a session that fell out of its cache
// window while the sweep was staggering is not paid for.
func TestASessionThatWentColdBetweenTheListingAndTheSendIsRefused(t *testing.T) {
	var rec recorder
	gate := &fakeGate{caches: map[string]promptcache.State{"id-chilled": cold("id-chilled")}}
	result := run([]Verdict{target("chilled")}, gate, rec.send, nil)
	if len(rec.sent) != 0 {
		t.Fatalf("nothing may be sent to a session that went cold, got %v", rec.sent)
	}
	if len(result.Held) != 1 || !strings.Contains(result.Held[0].Reason, "changed since the plan — cold") {
		t.Fatalf("got %+v", result.Held)
	}
}

// Both gates are asked again per pane, not once for the batch: a pane that
// answers idle on the first look and waiting on the second must not ride in
// on the earlier answer.
func TestTheGatesAreAskedOncePerPaneNotOncePerSweep(t *testing.T) {
	var rec recorder
	gate := &fakeGate{states: map[string][]string{
		"id-first":  {status.Idle},
		"id-second": {status.Working},
	}}
	result := run([]Verdict{target("first"), target("second")}, gate, rec.send, nil)
	if len(result.Sent) != 1 || result.Sent[0].Name != "first" {
		t.Fatalf("got %+v", result.Sent)
	}
	if gate.looks["id-second"] != 1 {
		t.Fatalf("the second pane was looked at %d times, want 1", gate.looks["id-second"])
	}
}

func TestAPaneThatCannotBeReadIsRefusedRatherThanTypedInto(t *testing.T) {
	var rec recorder
	gate := &fakeGate{err: map[string]error{"id-gone": errors.New("no such pane")}}
	result := run([]Verdict{target("gone")}, gate, rec.send, nil)
	if len(rec.sent) != 0 {
		t.Fatalf("an unreadable pane must not be typed into, got %v", rec.sent)
	}
	if len(result.Held) != 1 || !strings.Contains(result.Held[0].Reason, "unreadable") {
		t.Fatalf("got %+v", result.Held)
	}
}

func TestSendStaggersBetweenPanesAndNotBeforeTheFirst(t *testing.T) {
	var rec recorder
	var sleeps []time.Duration
	run([]Verdict{target("a"), target("b"), target("c")}, &fakeGate{}, rec.send, &sleeps)
	if len(sleeps) != 2 {
		t.Fatalf("three panes must cost two pauses, got %v", sleeps)
	}
	for _, slept := range sleeps {
		if slept != 3*time.Second {
			t.Fatalf("pace not honoured: %v", slept)
		}
	}
}

func TestSendStopsWhenTheOperatorEndsTheSweep(t *testing.T) {
	var rec recorder
	stop := make(chan struct{})
	close(stop)
	result := Send([]Verdict{target("a"), target("b")}, &fakeGate{}, rec.send,
		func() time.Time { return now }, 0, func(time.Duration) {}, stop)
	if len(result.Sent) != 0 || !result.Stopped {
		t.Fatalf("a closed stop must end the sweep before the first send, got %+v", result)
	}
}

func TestASendFailureEndsTheSweepInsteadOfMarchingOn(t *testing.T) {
	boom := errors.New("tmux is gone")
	sent := 0
	send := func(id, text string) error {
		sent++
		return boom
	}
	result := run([]Verdict{target("a"), target("b"), target("c")}, &fakeGate{}, send, nil)
	if sent != 1 || !errors.Is(result.Err, boom) {
		t.Fatalf("sent %d times, err %v", sent, result.Err)
	}
}
