package sessioncmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/sessionhooks"
	"github.com/usestring/gate-inbox/internal/store"
)

// holder is a turn gate that holds every turn and records what it was asked.
type holder struct {
	verdict extension.TurnVerdict
	asked   []extension.Turn
}

func (h *holder) Descriptor() extension.Descriptor { return extension.Descriptor{ID: "holder"} }
func (h *holder) Configure(extension.Config) error { return nil }
func (h *holder) GateTurn(_ context.Context, turn extension.Turn) (extension.TurnVerdict, error) {
	h.asked = append(h.asked, turn)
	return h.verdict, nil
}

func useGate(t *testing.T, g extension.Extension) {
	t.Helper()
	reg, err := extension.NewRegistry([]extension.Extension{g})
	if err != nil {
		t.Fatal(err)
	}
	hooks, err := reg.SessionHooks(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sessionhooks.Use(func() (*extension.SessionHooks, error) { return hooks, nil }))
}

func (f *channelFixture) gateTurn(t *testing.T, recipient, prompt string) (decision, reason string) {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"prompt": prompt, "hook_event_name": "UserPromptSubmit"})
	out := TurnGateHook(f.configDir, recipient, payload, f.now)
	if out == "" {
		return "", ""
	}
	var parsed struct{ Decision, Reason string }
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("hook output is not JSON: %q", out)
	}
	return parsed.Decision, parsed.Reason
}

func queuedFor(t *testing.T, st *store.Store, id string) []store.InboxMessage {
	t.Helper()
	pending := true
	msgs, err := st.Inbox(id, store.InboxFilter{SenderID: store.RelayedHumanSenderID, Pending: &pending, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	return msgs
}

// A gate that holds a prompt blocks it with its reason; asked to requeue,
// the operator's prompt is queued in their voice for the board to type in
// later. The gate is asked about the session, from the prompt hook, and
// never told the prompt.
func TestPromptTurnGateBlocksAndRequeues(t *testing.T) {
	f := newChannelFixture(t)
	g := &holder{verdict: extension.TurnVerdict{Hold: true, Requeue: true, Reason: "moving this session first"}}
	useGate(t, g)
	decision, reason := f.gateTurn(t, "child001", "run the tests again")
	if decision != "block" || !strings.HasPrefix(reason, "moving this session first") || !strings.Contains(reason, "queued") {
		t.Fatalf("decision %q reason %q", decision, reason)
	}
	if len(g.asked) != 1 || g.asked[0].Session.ID != "child001" || g.asked[0].Source != extension.TurnFromPrompt {
		t.Fatalf("asked %+v", g.asked)
	}
	q := queuedFor(t, f.store, "child001")
	if len(q) != 1 || q[0].Body != "run the tests again" || q[0].SenderID != store.RelayedHumanSenderID {
		t.Fatalf("queued %+v", q)
	}
}

// A message another session sent is blocked but never requeued: it must not
// come back in the operator's voice.
func TestPromptTurnGateNeverRequeuesAnotherSessionsMessage(t *testing.T) {
	f := newChannelFixture(t)
	useGate(t, &holder{verdict: extension.TurnVerdict{Hold: true, Requeue: true, Reason: "held"}})
	_, prompt := f.deliver(t, "parent01", "child001", "Rebase onto main.")
	decision, reason := f.gateTurn(t, "child001", prompt)
	if decision != "block" || strings.Contains(reason, "queued") {
		t.Fatalf("decision %q reason %q", decision, reason)
	}
	if q := queuedFor(t, f.store, "child001"); len(q) != 0 {
		t.Fatalf("requeued another session's message: %+v", q)
	}
}

// With no gate, or a gate that lets the turn through, the hook says nothing.
func TestPromptTurnGateSilentWhenOpen(t *testing.T) {
	f := newChannelFixture(t)
	if d, _ := f.gateTurn(t, "child001", "hello"); d != "" {
		t.Fatalf("no gate: %q", d)
	}
	useGate(t, &holder{})
	if d, _ := f.gateTurn(t, "child001", "hello"); d != "" {
		t.Fatalf("open gate: %q", d)
	}
}
