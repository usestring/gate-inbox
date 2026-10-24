package gatechannel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/convo"
)

var questions = []convo.AskQuestion{{
	Header:   "Deploy",
	Question: "May I push?",
	Options:  []convo.AskOption{{Label: "Yes"}, {Label: "No"}},
}}

// fakeClock advances only when a hold sleeps, and runs step at each sleep, so
// a test acts at a known point in the hold rather than racing a real one.
type fakeClock struct {
	mu   sync.Mutex
	now  time.Time
	step func(now time.Time)
}

func (c *fakeClock) clock() Clock {
	return Clock{
		Now: func() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now },
		Sleep: func(d time.Duration) {
			c.mu.Lock()
			c.now = c.now.Add(d)
			now := c.now
			c.mu.Unlock()
			if c.step != nil {
				c.step(now)
			}
		},
	}
}

func lease(t *testing.T, dir string, now time.Time) Lease {
	t.Helper()
	l, err := Renew(dir, "voice", "", true, 45*time.Second, 10*time.Second, now)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// The fix the live run asked for: with no consumer able to deliver, a call
// is never held, so the dialog draws at once.
func TestACallIsNotHeldWithoutADeliverableLease(t *testing.T) {
	dir := t.TempDir()
	start := time.Now()
	slept := false
	clock := Clock{Now: func() time.Time { return start }, Sleep: func(time.Duration) { slept = true }}

	if out := HoldCall(dir, "first", "toolu_1", questions, clock); out != "" || slept {
		t.Fatalf("no lease: output %q, slept %v", out, slept)
	}
	l, err := Renew(dir, "voice", "", false, 45*time.Second, 10*time.Second, start)
	if err != nil {
		t.Fatal(err)
	}
	if out := HoldCall(dir, "first", "toolu_1", questions, clock); out != "" || slept {
		t.Fatalf("paused lease: output %q, slept %v", out, slept)
	}
	if _, err := Renew(dir, "voice", l.Token, true, 45*time.Second, 10*time.Second, start.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if out := HoldCall(dir, "first", "toolu_1", questions, clock); out != "" || slept {
		t.Fatalf("expired lease: output %q, slept %v", out, slept)
	}
	if entries, _ := os.ReadDir(holdDir(dir)); len(entries) != 0 {
		t.Fatalf("holds left behind: %v", entries)
	}
}

func TestADeliveredAnswerAllowsTheCallWithItsAnswers(t *testing.T) {
	dir := t.TempDir()
	c := &fakeClock{now: time.Now()}
	l := lease(t, dir, c.now)
	delivered := make(chan error, 1)
	once := sync.Once{}
	c.step = func(time.Time) {
		once.Do(func() {
			holds, err := Pending(dir, l.Token, c.clock().Now())
			if err != nil || len(holds) != 1 {
				t.Errorf("pending %v %v", holds, err)
				return
			}
			h := holds[0]
			answer := Answer{Version: Version, Token: l.Token, Session: h.Session, ToolUseID: h.ToolUseID,
				Revision: h.Revision, Answers: map[string]string{"May I push?": "Yes"}}
			go func() { delivered <- Deliver(dir, answer, RealClock) }()
			// Let Deliver claim the hold and write the answer before the
			// hold looks again.
			for {
				if _, err := os.Stat(filepath.Join(holdDir(dir), "first.toolu_1.answer")); err == nil {
					return
				}
				time.Sleep(time.Millisecond)
			}
		})
	}

	out := HoldCall(dir, "first", "toolu_1", questions, c.clock())
	var decoded struct {
		HookSpecificOutput struct {
			PermissionDecision string `json:"permissionDecision"`
			UpdatedInput       struct {
				Answers map[string]string `json:"answers"`
			} `json:"updatedInput"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("output %q: %v", out, err)
	}
	if decoded.HookSpecificOutput.PermissionDecision != "allow" || decoded.HookSpecificOutput.UpdatedInput.Answers["May I push?"] != "Yes" {
		t.Fatalf("output %s", out)
	}
	if err := <-delivered; err != nil {
		t.Fatalf("deliver: %v", err)
	}
}

// The other half of the fix: a consumer that pauses mid-hold, because its
// broker was suppressed, voice went off or the mic is busy, frees the dialog
// on the hold's next look, and a late answer is refused.
func TestAHoldEndsAtOnceWhenTheLeaseStopsDelivering(t *testing.T) {
	for name, stop := range map[string]func(dir string, l Lease, now time.Time){
		"paused": func(dir string, l Lease, now time.Time) {
			_, _ = Renew(dir, "voice", l.Token, false, 45*time.Second, 10*time.Second, now)
		},
		"released": func(dir string, l Lease, _ time.Time) { _ = Release(dir, l.Token) },
		"expired":  nil,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			c := &fakeClock{now: time.Now()}
			l := lease(t, dir, c.now)
			start := c.now
			var h Hold
			c.step = func(now time.Time) {
				if h.Session == "" {
					holds, _ := Pending(dir, l.Token, now)
					if len(holds) == 1 {
						h = holds[0]
					}
					if stop != nil {
						stop(dir, l, now)
					}
				}
			}
			if out := HoldCall(dir, "first", "toolu_1", questions, c.clock()); out != "" {
				t.Fatalf("output %q", out)
			}
			held := c.now.Sub(start)
			if stop != nil && held > time.Second {
				t.Errorf("held %s after the lease stopped delivering", held)
			}
			if stop == nil && held > 11*time.Second {
				t.Errorf("held %s past a 10s lease", held)
			}
			_ = Release(dir, l.Token)
			fresh := lease(t, dir, c.now)
			late := Answer{Version: Version, Token: fresh.Token, Session: h.Session, ToolUseID: h.ToolUseID,
				Revision: h.Revision, Answers: map[string]string{"May I push?": "Yes"}}
			if err := Deliver(dir, late, c.clock()); err == nil {
				t.Fatal("an answer to an ended hold was delivered")
			}
		})
	}
}

func TestAnAnswerMustMatchTheHeldCall(t *testing.T) {
	hold := Hold{Version: Version, Session: "first", ToolUseID: "toolu_1", Revision: Revision(questions), Questions: questions}
	multi := []convo.AskQuestion{{Question: "Which?", MultiSelect: true, Options: []convo.AskOption{{Label: "A"}, {Label: "B"}}}}
	multiHold := Hold{Session: "first", ToolUseID: "toolu_2", Revision: Revision(multi), Questions: multi}
	ok := func(h Hold, answers map[string]string) Answer {
		return Answer{Session: h.Session, ToolUseID: h.ToolUseID, Revision: h.Revision, Answers: answers}
	}
	for name, tc := range map[string]struct {
		hold   Hold
		answer Answer
		want   string
	}{
		"an option":         {hold, ok(hold, map[string]string{"May I push?": "No"}), ""},
		"a decline":         {hold, Answer{Session: "first", ToolUseID: "toolu_1", Revision: hold.Revision, Decline: true}, ""},
		"free text":         {hold, ok(hold, map[string]string{"May I push?": "maybe later"}), "not one of the options"},
		"no answer":         {hold, ok(hold, map[string]string{}), "were answered"},
		"another call":      {hold, Answer{Session: "first", ToolUseID: "toolu_9", Revision: hold.Revision}, "another call"},
		"changed questions": {hold, Answer{Session: "first", ToolUseID: "toolu_1", Revision: "stale"}, "changed"},
		"several labels":    {multiHold, ok(multiHold, map[string]string{"Which?": "A, B"}), ""},
		"a repeated label":  {multiHold, ok(multiHold, map[string]string{"Which?": "A, A"}), "not one of the options"},
	} {
		t.Run(name, func(t *testing.T) {
			err := validate(tc.hold, tc.answer)
			if tc.want == "" && err != nil {
				t.Fatalf("refused: %v", err)
			}
			if tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestOneConsumerHoldsTheLeaseUntilItLapses(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	l := lease(t, dir, now)
	if _, err := Renew(dir, "other", "", true, 45*time.Second, 10*time.Second, now); err == nil {
		t.Fatal("a second consumer took a live lease")
	}
	if _, err := Renew(dir, "voice", "wrong", true, 45*time.Second, 10*time.Second, now); err == nil {
		t.Fatal("a renewal with the wrong token was accepted")
	}
	if _, err := Pending(dir, "wrong", now); err == nil {
		t.Fatal("pending was read with the wrong token")
	}
	if _, err := Renew(dir, "voice", "", true, time.Hour, 10*time.Second, now); err == nil {
		t.Fatal("a hold past the hook timeout was accepted")
	}
	if _, err := Renew(dir, "other", "", true, 45*time.Second, 10*time.Second, now.Add(11*time.Second)); err != nil {
		t.Fatalf("a lapsed lease was not given up: %v", err)
	}
	if err := Release(dir, l.Token); err == nil {
		t.Fatal("the old token released the new lease")
	}
}

// A consumer answers one call at a time: a call asked while another is held
// draws at once instead of waiting behind it. A hold its hook left behind
// stops counting at its deadline.
func TestACallIsNotHeldBehindAnother(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	lease(t, dir, now)
	other := Hold{Version: Version, Session: "first", ToolUseID: "toolu_1", Revision: Revision(questions), Questions: questions, Deadline: now.Add(30 * time.Second)}
	if err := writeJSON(filepath.Join(holdDir(dir), "first.toolu_1.hold"), other); err != nil {
		t.Fatal(err)
	}
	slept := false
	clock := Clock{Now: func() time.Time { return now }, Sleep: func(time.Duration) { slept = true }}
	if out := HoldCall(dir, "second", "toolu_2", questions, clock); out != "" || slept {
		t.Fatalf("behind a live hold: output %q, slept %v", out, slept)
	}
	if _, err := os.Stat(filepath.Join(holdDir(dir), "second.toolu_2.hold")); !os.IsNotExist(err) {
		t.Fatalf("the second call's hold was left behind: %v", err)
	}

	c := &fakeClock{now: now.Add(31 * time.Second)}
	c.step = func(time.Time) { slept = true }
	_, _ = Renew(dir, "voice", "", true, 45*time.Second, 10*time.Second, c.now)
	HoldCall(dir, "second", "toolu_2", questions, c.clock())
	if !slept {
		t.Fatal("a hold past its deadline still kept another call from being held")
	}
}
