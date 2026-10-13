package sessioncmd

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/grant"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/relayask"
	"github.com/usestring/gate-inbox/internal/store"
)

// relayInput is the AskUserQuestion input a refusal ends on.
func relayInput(t *testing.T, err error) relayask.Input {
	t.Helper()
	text := err.Error()
	at := strings.LastIndex(text, `{"questions":`)
	if at < 0 {
		t.Fatalf("refusal carries no AskUserQuestion input:\n%s", text)
	}
	var in relayask.Input
	if jsonErr := json.Unmarshal([]byte(strings.TrimSpace(text[at:])), &in); jsonErr != nil {
		t.Fatalf("refusal's input is not JSON (%v):\n%s", jsonErr, text[at:])
	}
	return in
}

// asksAndAnswers is a parent transcript asking in, its first question
// answered with choice.
func asksAndAnswers(base *transcript, id string, at time.Time, in relayask.Input, choice string) *transcript {
	return base.ask(id, at, in.Questions...).
		answer(id, at.Add(time.Second), map[string]string{in.Questions[0].Question: choice})
}

// The four refusal classes answer_session and grant_permission returned most
// (gate-inbox.md §3), each replayed: the refusal carries an input, and that
// input passed as is to the parent's own question tool is what the relay
// check then accepts.

// Class "this is the child asking for your user's approval": an unrelayed
// answer to an Approval question.
func TestTheApprovalRefusalCarriesTheInputThatRelaysIt(t *testing.T) {
	h := newSessionHarness(t)
	asked := time.Now().Add(-2 * time.Minute).UTC()
	child := scriptedChild(t, h, storefront, &transcript{}, asked)

	_, err := h.sessions.Answer(h.caller.ID, child.ID, "Every storefront", false)
	if !errors.Is(err, errApprovalNeedsRelay) || !strings.Contains(err.Error(), "Next: call your own AskUserQuestion") {
		t.Fatalf("err = %v, want the Approval refusal naming the next call", err)
	}
	in := relayInput(t, err)
	if len(in.Questions) != 1 || !sameAsk(in.Questions[0], storefront) || in.Questions[0].Header != "Approval" {
		t.Fatalf("input %+v is not the child's question", in)
	}
	for _, option := range in.Questions[0].Options {
		if option.Description == "" {
			t.Fatalf("option %q has no description; AskUserQuestion requires one", option.Label)
		}
	}

	asksAndAnswers(&transcript{}, "toolu_parent", time.Now().UTC(), in, "Every storefront").
		write(t, h.sessions.claudeHome, "conv-parent")
	answered, err := h.sessions.Answer(h.caller.ID, child.ID, "Every storefront", true)
	if err != nil || !answered.Verified {
		t.Fatalf("relay with the refusal's input: %+v, %v", answered, err)
	}
}

// Class "not on a dialog this can read": the refusal says what the child is
// doing and what to wait for.
func TestTheNoDialogRefusalSaysWhatToWaitFor(t *testing.T) {
	h := newSessionHarness(t)
	child := childShowing(t, h, h.caller.ID, "child104", "resting", "all done, nothing to ask\n")
	_, err := h.sessions.Answer(h.caller.ID, child.ID, "Every storefront", true)
	if !errors.Is(err, dialog.ErrNoDialog) {
		t.Fatalf("answer to a session on no dialog err = %v, want ErrNoDialog", err)
	}
	for _, want := range []string{"no dialog up: session " + child.ID + "'s status is ", "Next: wait_for_session", "relay_ask"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("no-dialog refusal %q does not say %q", err, want)
		}
	}
}

// Class "relay refused: no dialog of yours asks the child's question with its
// options word for word": the parent paraphrased.
func TestAParaphraseRefusalCarriesTheInputThatRelaysIt(t *testing.T) {
	h := newSessionHarness(t)
	asked := time.Now().Add(-2 * time.Minute).UTC()
	paraphrased := storefront
	paraphrased.Question = "Which storefronts should the census include?"
	parent := (&transcript{}).ask("toolu_para", asked.Add(time.Minute), paraphrased).
		answer("toolu_para", asked.Add(70*time.Second), map[string]string{paraphrased.Question: "Germany only"})
	child := scriptedChild(t, h, storefront, parent, asked)

	_, err := h.sessions.Answer(h.caller.ID, child.ID, "Germany only", true)
	if !errors.Is(err, errRelayRefused) || !strings.Contains(err.Error(), "word for word") {
		t.Fatalf("err = %v, want the word-for-word refusal", err)
	}
	in := relayInput(t, err)
	asksAndAnswers(parent, "toolu_parent", time.Now().UTC(), in, "Germany only").
		write(t, h.sessions.claudeHome, "conv-parent")
	answered, err := h.sessions.Answer(h.caller.ID, child.ID, "Germany only", true)
	if err != nil || !answered.Verified || answered.Selected != "Germany only" {
		t.Fatalf("relay with the refusal's input: %+v, %v", answered, err)
	}
	rows, err := h.store.AnswersFor(child.ID, childCallID)
	if err != nil || len(rows) != 1 || rows[0].State != store.AnswerKeyed || rows[0].EvidenceToolUseID != "toolu_parent" {
		t.Fatalf("ledger = %+v, %v; want one keyed row citing the payload's dialog", rows, err)
	}
}

// A user who was asked and chose another option is not asked again: that
// refusal names their answer and carries no new question.
func TestAMismatchedAnswerIsNotTurnedIntoANewQuestion(t *testing.T) {
	h := newSessionHarness(t)
	asked := time.Now().Add(-2 * time.Minute).UTC()
	parent := (&transcript{}).ask("toolu_parent", asked.Add(time.Minute), storefront).
		answer("toolu_parent", asked.Add(90*time.Second), map[string]string{storefront.Question: "Every storefront"})
	child := scriptedChild(t, h, storefront, parent, asked)
	_, err := h.sessions.Answer(h.caller.ID, child.ID, "Germany only", true)
	if !errors.Is(err, errRelayRefused) || strings.Contains(err.Error(), `{"questions":`) {
		t.Fatalf("err = %v, want the mismatch refusal without an input", err)
	}
}

// read_session's digest carries the same input.
func TestReadSessionCarriesTheRelayInput(t *testing.T) {
	h := newSessionHarness(t)
	child := scriptedChild(t, h, storefront, &transcript{}, time.Now().Add(-time.Minute).UTC())
	screen, err := h.sessions.Read(h.caller.ID, child.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if screen.Digest.RelayAsk == nil || len(screen.Digest.RelayAsk.Questions) != 1 ||
		!sameAsk(screen.Digest.RelayAsk.Questions[0], storefront) {
		t.Fatalf("digest.relay_ask = %+v, want the child's question", screen.Digest.RelayAsk)
	}
	if text := FormatSessionScreen(screen); !strings.Contains(text, "relay_ask") ||
		!strings.Contains(text, screen.Digest.RelayAsk.JSON()) {
		t.Fatalf("read_session text does not carry the input:\n%s", text)
	}
}

// Class "this grant needs your user's approval, and no dialog of yours asks it
// yet": the first call of every grant. Its input, passed as is, is approved.
func TestTheGrantRefusalsInputIsTheApprovalTheGrantAccepts(t *testing.T) {
	f := newRelayFixture(t, gitleaks)
	f.parentSays(t, &transcript{})
	gs := []grant.Grant{{Kind: grant.KindSoft, Value: "git push origin feat/x"}}
	_, _, err := f.sessions.grantApproval(f.store, f.parent, f.child, gs, 0)
	var need *ApprovalNeeded
	if !errors.As(err, &need) || !strings.Contains(err.Error(), "no dialog of yours asks it yet") {
		t.Fatalf("err = %v, want the grant's approval refusal", err)
	}
	in := relayInput(t, err)
	f.parentSays(t, asksAndAnswers(&transcript{}, "toolu_grant", relayStart, in, grantOption))
	evidence, _, err := f.sessions.grantApproval(f.store, f.parent, f.child, gs, 0)
	if err != nil || evidence != "toolu_grant" {
		t.Fatalf("grant with the refusal's input: evidence %q, %v", evidence, err)
	}
}

// relayask.For leaves out what the relay check does not read and fills what
// AskUserQuestion requires.
func TestRelayInputFromTheChildsCall(t *testing.T) {
	asked := []convo.AskQuestion{
		{ID: "q1", Header: "Rollout", Question: rollout.Question, Options: []convo.AskOption{
			{Label: "Do both", Preview: "## long preview"}, {Label: "Hold", Description: "Wait"}}},
		notify,
	}
	shown := []dialog.Question{
		{Index: 1, Header: "Rollout", Question: rollout.Question, Answered: true},
		{Index: 2, Header: "Notify", Question: notify.Question},
	}
	in := relayask.For(asked, shown)
	if in == nil || len(in.Questions) != 1 || in.Questions[0].Question != notify.Question {
		t.Fatalf("input %+v, want only the unanswered question", in)
	}
	in = relayask.For(asked, []dialog.Question{{Index: 1, Question: rollout.Question}, {Index: 2, Question: notify.Question}})
	first := in.Questions[0]
	if first.ID != "" || first.Options[0].Preview != "" || first.Options[0].Description != "Do both" ||
		first.Options[1].Description != "Wait" {
		t.Fatalf("first question %+v, want no id or preview and every option described", first)
	}
	if raw := in.JSON(); strings.Contains(raw, `"id"`) || !strings.Contains(raw, `"multiSelect":false`) {
		t.Fatalf("JSON %s is not AskUserQuestion's input shape", raw)
	}
}

// A permission prompt's input quotes its text with its choices as options,
// which admitScreen accepts.
func TestRelayInputForAScreenIsWhatAdmitScreenAccepts(t *testing.T) {
	screen := dialog.Screen{Kind: dialog.ScreenPermission,
		Lines:   []string{"Bash command", "", "  rm -rf build/", "", "Do you want to proceed?"},
		Choices: []dialog.ScreenChoice{{Label: "Yes", Number: 1}, {Label: "No, and tell Claude what to do differently", Number: 2}}}
	in := relayask.ForScreen(screen)
	if in == nil || len(in.Questions) != 1 || !asksScreen(in.Questions[0], screen.Prompt(), screen.Labels()) {
		t.Fatalf("input %+v does not match the screen as admitScreen reads it", in)
	}
}
