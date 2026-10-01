package sessioncmd

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/asks"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/store"
)

var pushApproval = convo.AskQuestion{Header: "Approval",
	Question: "May I push branch feat/s-1-x to usestring/gate-inbox and open PR 'feat: x'?",
	Options:  []convo.AskOption{{Label: "Yes, push and open it"}, {Label: "No"}}}

// attestFixture is a parent with a transcript, its child, a sibling it also
// spawned, and a stranger it did not.
type attestFixture struct {
	h                       *sessionHarness
	home                    string
	parent, child, stranger store.Session
	now                     time.Time
}

func newAttestFixture(t *testing.T, childAge time.Duration) *attestFixture {
	t.Helper()
	h := newSessionHarness(t)
	home := t.TempDir()
	h.sessions.claudeHome = home
	now := time.Now()
	f := &attestFixture{h: h, home: home, now: now}
	f.parent = childRow(t, h, store.Session{ID: "a0a0a0a1", Name: "coordinator", AgentSessionID: "conv-parent",
		CreatedAt: now.Add(-time.Hour)}, true)
	f.child = childRow(t, h, store.Session{ID: "c0c0c0c1", Name: "worker", AgentSessionID: "conv-child",
		ParentID: f.parent.ID, SpawnedBy: f.parent.ID, CreatedAt: now.Add(-childAge)}, true)
	f.stranger = childRow(t, h, store.Session{ID: "e0e0e0e1", Name: "someone-elses", AgentSessionID: "conv-strange",
		CreatedAt: now.Add(-20 * time.Minute)}, true)
	(&transcript{}).write(t, home, "conv-child")
	return f
}

func (f *attestFixture) userAnswered(t *testing.T, id string, at time.Time, answer string) {
	t.Helper()
	(&transcript{}).ask(id, at.Add(-time.Minute), pushApproval).
		answer(id, at, map[string]string{pushApproval.Question: answer}).write(t, f.home, "conv-parent")
}

func (f *attestFixture) send(target string) (SendResult, error) {
	return f.h.sessions.SendAttested(f.parent.ID, target, "Your user approved it: push and open the PR.", "", false,
		pushApproval.Question)
}

func TestAttestedSendCarriesTheUsersAnswer(t *testing.T) {
	f := newAttestFixture(t, 20*time.Minute)
	f.userAnswered(t, "toolu_parent", f.now.Add(-2*time.Minute), "Yes, push and open it")
	result, err := f.send(f.child.ID)
	if err != nil {
		t.Fatalf("SendAttested: %v", err)
	}
	if result.Attestation == "" || result.MessageID == 0 {
		t.Fatalf("result = %+v; want a queued message with an attestation", result)
	}
	a, ok, err := f.h.store.AttestationFor(result.MessageID)
	if err != nil || !ok {
		t.Fatalf("AttestationFor: %v, %v", ok, err)
	}
	if a.Nonce != result.Attestation || a.TargetSession != f.child.ID || a.BySession != f.parent.ID ||
		a.Answer != "Yes, push and open it" || a.Question != pushApproval.Question || a.EvidenceToolUseID != "toolu_parent" {
		t.Fatalf("attestation = %+v", a)
	}
	if !strings.Contains(FormatSendResult(result, f.child.ID), "relay attestation "+a.Nonce) {
		t.Fatal("the send's result does not name the attestation")
	}
	// The same answer of the user's approves one thing, so a second send
	// citing it is refused, whichever path it would take.
	if _, err := f.send(f.child.ID); !errors.Is(err, errRelayRefused) || !strings.Contains(err.Error(), "already been relayed") {
		t.Fatalf("second relay: %v; want it refused as already relayed", err)
	}
}

func TestAttestedSendRefusals(t *testing.T) {
	cases := []struct {
		name   string
		age    time.Duration
		setup  func(t *testing.T, f *attestFixture)
		target func(f *attestFixture) string
		want   string
	}{
		{"a session it did not spawn", 20 * time.Minute, func(t *testing.T, f *attestFixture) {
			f.userAnswered(t, "toolu_parent", f.now.Add(-2*time.Minute), "Yes, push and open it")
		}, func(f *attestFixture) string { return f.stranger.ID }, "not a child you spawned"},
		{"no such dialog", 20 * time.Minute, func(t *testing.T, f *attestFixture) {
			(&transcript{}).write(t, f.home, "conv-parent")
		}, nil, "word for word"},
		{"answered before the child existed", 20 * time.Minute, func(t *testing.T, f *attestFixture) {
			f.userAnswered(t, "toolu_parent", f.now.Add(-25*time.Minute), "Yes, push and open it")
		}, nil, "before the child was spawned"},
		{"a stale answer", 2 * time.Hour, func(t *testing.T, f *attestFixture) {
			f.userAnswered(t, "toolu_parent", f.now.Add(-45*time.Minute), "Yes, push and open it")
		}, nil, "more than 30m0s ago"},
		{"an answer Gate Inbox typed", 20 * time.Minute, func(t *testing.T, f *attestFixture) {
			f.userAnswered(t, "toolu_parent", f.now.Add(-2*time.Minute), "Yes, push and open it")
			if _, err := f.h.store.RecordAnswer(store.DialogAnswer{TargetSession: f.parent.ID,
				TargetToolUseID: "toolu_parent", QuestionHash: "x", Answer: "Yes, push and open it",
				BySession: "grand001", Mode: store.AnswerByAgent}); err != nil {
				t.Fatal(err)
			}
		}, nil, "typed by the session that spawned you"},
		{"a child holding its own Approval dialog", 20 * time.Minute, func(t *testing.T, f *attestFixture) {
			f.userAnswered(t, "toolu_parent", f.now.Add(-2*time.Minute), "Yes, push and open it")
			(&transcript{}).ask("toolu_child", f.now.Add(-time.Minute), pushApproval).write(t, f.home, "conv-child")
		}, nil, "answer_session relay: true"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newAttestFixture(t, c.age)
			c.setup(t, f)
			target := f.child.ID
			if c.target != nil {
				target = c.target(f)
			}
			result, err := f.send(target)
			if !errors.Is(err, errRelayRefused) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("SendAttested = %+v, %v; want a refusal saying %q", result, err, c.want)
			}
			if _, found, _ := f.h.store.HeadMessage(target); found {
				t.Fatal("a refused relay still queued its message")
			}
		})
	}
}

type attestedAskSource struct {
	answered convo.AnsweredAsk
	pending  bool
}

func (attestedAskSource) Traits() asks.Traits      { return asks.Traits{} }
func (attestedAskSource) Located(asks.Target) bool { return true }
func (s attestedAskSource) Pending(target asks.Target) (asks.Call, bool) {
	return asks.Call{ID: "child-approval", Questions: []convo.AskQuestion{pushApproval}}, s.pending && target.AgentSessionID == "conv-child"
}
func (attestedAskSource) Result(asks.Target, string) (asks.Result, bool) { return asks.Result{}, false }
func (s attestedAskSource) Answered(asks.Target, time.Time) ([]convo.AnsweredAsk, error) {
	return []convo.AnsweredAsk{s.answered}, nil
}
func (attestedAskSource) Unanswered(asks.Target) (asks.Result, bool) { return asks.Result{}, false }

func TestAttestationUsesSharedCliQuestionRecords(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(fmt.Sprint(pending), func(t *testing.T) {
			f := newAttestFixture(t, 20*time.Minute)
			tool := t.Name()
			asks.Register(tool, attestedAskSource{pending: pending, answered: convo.AnsweredAsk{ToolUseID: "parent-approval", Questions: []convo.AskQuestion{pushApproval}, Answers: map[string]string{pushApproval.Question: "Yes, push and open it"}, AnsweredAt: f.now.Add(-2 * time.Minute)}})
			f.parent.Tool, f.child.Tool = tool, tool
			answer, _, err := f.h.sessions.attestable(f.h.store, f.parent, f.child, pushApproval.Question, f.now)
			if pending {
				if !errors.Is(err, errRelayRefused) || !strings.Contains(err.Error(), "own Approval dialog") {
					t.Fatalf("error %v", err)
				}
				return
			}
			if err != nil || answer.ToolUseID != "parent-approval" {
				t.Fatalf("answer %+v, error %v", answer, err)
			}
		})
	}
}
