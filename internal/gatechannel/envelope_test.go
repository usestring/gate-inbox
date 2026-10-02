package gatechannel

import (
	"encoding/json"
	"github.com/usestring/gate-inbox/internal/convo"
	"strings"
	"testing"
	"time"
)

func exampleCall() convo.AskCall {
	return convo.AskCall{ToolUseID: "call-one", AskedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Questions: []convo.AskQuestion{{Header: "Next", Question: "Which sample?", Options: []convo.AskOption{
		{Label: "Alpha", Description: "Read the first sample", Preview: "alpha preview"},
		{Label: "Beta", Description: "Read the second sample"},
	}}}}
}

func envelope(t *testing.T, call convo.AskCall) Envelope {
	t.Helper()
	got, err := NewEnvelope("epoch-one", "session-one", "agent-one", call)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestEnvelopeKeepsFullQuestionSnapshot(t *testing.T) {
	call := exampleCall()
	got := envelope(t, call)
	call.Questions[0].Question = "Changed"
	call.Questions[0].Options[0].Preview = "changed preview"
	if got.Questions[0].Question != "Which sample?" || got.Questions[0].Options[0].Preview != "alpha preview" {
		t.Fatal("source mutation changed the delivered snapshot")
	}
	got.Questions[0].Options[1].Label = "Changed"
	if call.Questions[0].Options[1].Label != "Beta" {
		t.Fatal("consumer mutation changed the saved call")
	}
}

func TestGateRevisionChangesWithCompleteOrderedContent(t *testing.T) {
	original := envelope(t, exampleCall())
	for name, change := range map[string]func(*convo.AskCall){
		"question":     func(c *convo.AskCall) { c.Questions[0].Question += " now" },
		"header":       func(c *convo.AskCall) { c.Questions[0].Header = "Other" },
		"multi select": func(c *convo.AskCall) { c.Questions[0].MultiSelect = true },
		"label":        func(c *convo.AskCall) { c.Questions[0].Options[0].Label = "Other" },
		"description":  func(c *convo.AskCall) { c.Questions[0].Options[0].Description += " only" },
		"preview":      func(c *convo.AskCall) { c.Questions[0].Options[0].Preview += " changed" },
		"option order": func(c *convo.AskCall) {
			c.Questions[0].Options[0], c.Questions[0].Options[1] = c.Questions[0].Options[1], c.Questions[0].Options[0]
		},
		"question added": func(c *convo.AskCall) {
			c.Questions = append(c.Questions, convo.AskQuestion{Question: "Another sample?"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			call := exampleCall()
			change(&call)
			got := envelope(t, call)
			if got.Revision == original.Revision || got.ID == original.ID {
				t.Fatal("changed gate content retained its revision or identity")
			}
		})
	}
}

func TestQuestionOrderChangesRevision(t *testing.T) {
	call := exampleCall()
	call.Questions = append(call.Questions, convo.AskQuestion{Header: "Later", Question: "Another sample?"})
	original := envelope(t, call)
	call.Questions[0], call.Questions[1] = call.Questions[1], call.Questions[0]
	if got := envelope(t, call); got.Revision == original.Revision {
		t.Fatal("question order did not change revision")
	}
}

func TestIdenticalReasksAndRestartsHaveDistinctIdentity(t *testing.T) {
	original := envelope(t, exampleCall())
	for _, v := range [][4]string{
		{"epoch-two", "session-one", "agent-one", "call-one"},
		{"epoch-one", "session-two", "agent-one", "call-one"},
		{"epoch-one", "session-one", "agent-two", "call-one"},
		{"epoch-one", "session-one", "agent-one", "call-two"},
	} {
		call := exampleCall()
		call.ToolUseID = v[3]
		got, err := NewEnvelope(v[0], v[1], v[2], call)
		if err != nil || got.ID == original.ID || got.Revision != original.Revision {
			t.Fatalf("identity change was lost: %v", err)
		}
	}
	call := exampleCall()
	call.AskedAt = call.AskedAt.Add(time.Second)
	got := envelope(t, call)
	if got.ID != original.ID || got.AskedAt == original.AskedAt {
		t.Fatal("observation time changed identity or was discarded")
	}
}

func TestEnvelopeRejectsMissingIdentityOrQuestions(t *testing.T) {
	for index := range 4 {
		v := [4]string{"epoch", "session", "agent", "call"}
		v[index] = " "
		call := exampleCall()
		call.ToolUseID = v[3]
		if _, err := NewEnvelope(v[0], v[1], v[2], call); err == nil {
			t.Fatal("accepted incomplete identity")
		}
	}
	for _, call := range []convo.AskCall{{ToolUseID: "call"}, {ToolUseID: "call", Questions: []convo.AskQuestion{{Question: " "}}}} {
		if _, err := NewEnvelope("epoch", "session", "agent", call); err == nil {
			t.Fatal("accepted an empty question")
		}
	}
}

func TestFreeTextQuestionNormalizesEmptyOptions(t *testing.T) {
	call := convo.AskCall{ToolUseID: "call", Questions: []convo.AskQuestion{{Question: "Your reply?"}}}
	first := envelope(t, call)
	call.Questions[0].Options = []convo.AskOption{}
	second := envelope(t, call)
	if first.Revision != second.Revision {
		t.Fatal("nil and empty options represent different questions")
	}
	raw, err := json.Marshal(first)
	if err != nil || !strings.Contains(string(raw), `"options":[]`) {
		t.Fatalf("free-text envelope did not retain empty options: %s %v", raw, err)
	}
}

func TestVersionOneIdentityVector(t *testing.T) {
	got := envelope(t, exampleCall())
	if got.Version != 1 || got.Revision != "e4f91106df4bf9bdc43ac6fa1d877574284d457d68e9c1cc62812b2f141cc418" || got.ID != "2599b09a38c2f8eafdee282c3b89a7378eeb0c95b1d05b82cdc75ec6dfb32a6a" {
		t.Fatalf("version one identity changed: %d %s %s", got.Version, got.Revision, got.ID)
	}
}
