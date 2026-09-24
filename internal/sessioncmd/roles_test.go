package sessioncmd

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/sessionhooks"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// roleBoard declares a reviewer that relays and pins, and a linter that
// asks for nothing. Its Relayer rewrites a "yes", keeps "for the board" to
// itself, and refuses "no question".
type roleBoard struct{}

func (roleBoard) Descriptor() extension.Descriptor { return extension.Descriptor{ID: "board"} }
func (roleBoard) Configure(extension.Config) error { return nil }
func (roleBoard) Roles() []extension.RoleSpec {
	return []extension.RoleSpec{
		{Name: "reviewer", RelayToParent: true, PinnedStatus: true, SkipSendChildren: true},
		{Name: "linter"},
		{Name: "voice", RelayToParent: true, PinnedStatus: true},
	}
}
func (roleBoard) Relay(_ context.Context, relay extension.Relay) (string, error) {
	switch relay.Text {
	case "for the board":
		return "", nil
	case "no question":
		return "", errors.New("nothing is waiting on an answer")
	case "yes":
		return "the operator chose yes", nil
	}
	return relay.Text, nil
}

// otherBoard is a second extension, with a reviewer of its own.
type otherBoard struct{}

func (otherBoard) Descriptor() extension.Descriptor { return extension.Descriptor{ID: "other"} }
func (otherBoard) Configure(extension.Config) error { return nil }
func (otherBoard) Roles() []extension.RoleSpec {
	return []extension.RoleSpec{{Name: "reviewer", PinnedStatus: true}}
}

// useRoles makes the two boards the process's extensions for the rest of
// the test, which is therefore never parallel.
func useRoles(t *testing.T) {
	t.Helper()
	registry, err := extension.NewRegistry([]extension.Extension{roleBoard{}, otherBoard{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Configure("", nil); err != nil {
		t.Fatal(err)
	}
	collected, err := registry.SessionHooks("", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sessionhooks.Use(func() (*extension.SessionHooks, error) { return collected, nil }))
}

// helperOf files a row with role under parentID. It has no pane: a relay
// needs its parent to be deliverable, not the helper.
func helperOf(t *testing.T, h *sessionHarness, parentID, role string) store.Session {
	t.Helper()
	helper := store.Session{
		ID: uuid.NewString()[:8], Name: "helper", Tool: "echoer", Cwd: h.caller.Cwd,
		Group: h.caller.Group, Status: status.Working, ParentID: parentID, Role: role,
	}
	if err := h.store.CreateSession(helper); err != nil {
		t.Fatal(err)
	}
	return helper
}

func headOf(t *testing.T, h *sessionHarness, id string) (store.InboxMessage, bool) {
	t.Helper()
	msg, ok, err := h.store.HeadMessage(id)
	if err != nil {
		t.Fatal(err)
	}
	return msg, ok
}

func TestARoleRelaysToItsParentAsTheOperatorAndReleasesItsPin(t *testing.T) {
	h := newSessionHarness(t)
	useRoles(t)
	reviewer := helperOf(t, h, h.caller.ID, "board/reviewer")
	if err := h.sessions.BoardPinStatus("board", reviewer.ID, status.Waiting); err != nil {
		t.Fatal(err)
	}

	sent, err := h.sessions.Send(reviewer.ID, h.caller.ID, "yes", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !sent.Relayed || sent.MessageID == 0 {
		t.Fatalf("Send = %+v, want a queued relay", sent)
	}
	msg, ok := headOf(t, h, h.caller.ID)
	if !ok || msg.SenderID != store.RelayedHumanSenderID || msg.Body != "the operator chose yes" {
		t.Fatalf("queued %+v, want the Relayer's rewrite as the operator's relayed words", msg)
	}
	if _, pinned := hooks.NewManager(h.sessions.configDir).Read(reviewer.ID); pinned {
		t.Fatal("the relay left the pin's status file behind")
	}
	if row, err := h.store.Get(reviewer.ID); err != nil || row.Status != status.Working {
		t.Fatalf("row = %+v, %v; want it working once the pin is released", row, err)
	}
}

// TestARelayingRoleSendingAsTheOperatorIsStillRelayed holds the helper's
// claim to the operator's words to the same path as its plain send: through
// the extension's Relayer, queued as a relayed line, never as a shell send.
func TestARelayingRoleSendingAsTheOperatorIsStillRelayed(t *testing.T) {
	h := newSessionHarness(t)
	useRoles(t)
	helper := helperOf(t, h, h.caller.ID, "board/voice")
	if err := h.sessions.BoardPinStatus("board", helper.ID, status.Waiting); err != nil {
		t.Fatal(err)
	}

	sent, err := h.sessions.SendAsHuman(helper.ID, h.caller.ID, "yes", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !sent.Relayed || sent.MessageID == 0 {
		t.Fatalf("SendAsHuman = %+v, want a queued relay", sent)
	}
	msg, ok := headOf(t, h, h.caller.ID)
	if !ok || msg.SenderID != store.RelayedHumanSenderID || msg.Body != "the operator chose yes" {
		t.Fatalf("queued %+v, want the Relayer's rewrite as the operator's relayed words", msg)
	}
	if _, pinned := hooks.NewManager(h.sessions.configDir).Read(helper.ID); pinned {
		t.Fatal("the relay left the pin's status file behind")
	}

	// What the extension keeps for itself queues nothing, whichever way
	// it was sent.
	sent, err = h.sessions.SendAsHuman(helper.ID, h.caller.ID, "for the board", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !sent.Relayed || sent.MessageID != 0 {
		t.Fatalf("SendAsHuman = %+v, want a relay that queued nothing", sent)
	}
	if _, err := h.sessions.SendAsHuman(helper.ID, h.caller.ID, "no question", "", false); err == nil ||
		!strings.Contains(err.Error(), "nothing is waiting on an answer") {
		t.Fatalf("SendAsHuman = %v, want the Relayer's refusal", err)
	}
}

// TestASendAsTheOperatorFromNoRelayingRoleIsAShellSend keeps the operator's
// own send, and one from a session whose role does not relay, off the relay
// path: each is queued as the operator's shell words.
func TestASendAsTheOperatorFromNoRelayingRoleIsAShellSend(t *testing.T) {
	h := newSessionHarness(t)
	useRoles(t)
	linter := helperOf(t, h, h.caller.ID, "board/linter")
	for body, from := range map[string]string{"yes, from a shell": "", "yes, from a plain role": linter.ID} {
		sent, err := h.sessions.SendAsHuman(from, h.caller.ID, body, "", false)
		if err != nil {
			t.Fatal(err)
		}
		msg, ok := headOf(t, h, h.caller.ID)
		if sent.Relayed || !ok || msg.SenderID != store.HumanSenderID || msg.Body != body {
			t.Fatalf("from %q: sent %+v, queued %+v; want the operator's shell words, unrelayed", from, sent, msg)
		}
		if err := h.store.MarkDelivered(msg.ID, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestARelayTheExtensionKeepsQueuesNothing(t *testing.T) {
	h := newSessionHarness(t)
	useRoles(t)
	reviewer := helperOf(t, h, h.caller.ID, "board/reviewer")

	sent, err := h.sessions.Send(reviewer.ID, h.caller.ID, "for the board", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !sent.Relayed || sent.MessageID != 0 {
		t.Fatalf("Send = %+v, want a relay that queued nothing", sent)
	}
	if !strings.Contains(FormatSendResult(sent, h.caller.ID), "nothing was queued") {
		t.Fatalf("formatted as %q", FormatSendResult(sent, h.caller.ID))
	}
	if msg, ok := headOf(t, h, h.caller.ID); ok {
		t.Fatalf("queued %+v", msg)
	}

	_, err = h.sessions.Send(reviewer.ID, h.caller.ID, "no question", "", false)
	if err == nil || !strings.Contains(err.Error(), "nothing is waiting on an answer") {
		t.Fatalf("Send = %v, want the Relayer's refusal", err)
	}
}

func TestAnArchivedRoleCannotRelay(t *testing.T) {
	h := newSessionHarness(t)
	useRoles(t)
	reviewer := helperOf(t, h, h.caller.ID, "board/reviewer")
	if err := h.store.SetArchived(reviewer.ID, true); err != nil {
		t.Fatal(err)
	}
	_, err := h.sessions.Send(reviewer.ID, h.caller.ID, "yes", "", false)
	if err == nil || !strings.Contains(err.Error(), "archived") {
		t.Fatalf("Send = %v, want an archived relay refused", err)
	}
	if msg, ok := headOf(t, h, h.caller.ID); ok {
		t.Fatalf("queued %+v", msg)
	}
}

func TestOnlyARelayingRoleWritingToItsParentIsRelayed(t *testing.T) {
	h := newSessionHarness(t)
	useRoles(t)
	linter := helperOf(t, h, h.caller.ID, "board/linter")
	sent, err := h.sessions.Send(linter.ID, h.caller.ID, "yes", "", false)
	if err != nil {
		t.Fatal(err)
	}
	msg, ok := headOf(t, h, h.caller.ID)
	if sent.Relayed || !ok || msg.SenderID != linter.ID || msg.Body != "yes" {
		t.Fatalf("Send = %+v, queued %+v; want an ordinary send", sent, msg)
	}

	// The reviewer writing to a session it is not filed under is itself.
	sibling, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Tool: "stoppable", Name: "sibling"})
	if err != nil {
		t.Fatal(err)
	}
	reviewer := helperOf(t, h, h.caller.ID, "board/reviewer")
	sent, err = h.sessions.Send(reviewer.ID, sibling.ID, "yes", "", false)
	if err != nil {
		t.Fatal(err)
	}
	msg, ok = headOf(t, h, sibling.ID)
	if sent.Relayed || !ok || msg.SenderID != reviewer.ID || msg.Body != "yes" {
		t.Fatalf("Send = %+v, queued %+v; want an ordinary send", sent, msg)
	}
}

func TestSendChildrenSkipsARoleThatAsksToBeLeftOut(t *testing.T) {
	h := newSessionHarness(t)
	useRoles(t)
	worker, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Tool: "stoppable", Name: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	reviewer := helperOf(t, h, h.caller.ID, "board/reviewer")

	sent, err := h.sessions.SendChildren(h.caller.ID, "the branch moved")
	if err != nil {
		t.Fatal(err)
	}
	reasons := map[string]string{}
	for _, delivery := range sent.Deliveries {
		reasons[delivery.SessionID] = delivery.Skipped
	}
	if reasons[worker.ID] != "" {
		t.Fatalf("the worker was skipped: %q", reasons[worker.ID])
	}
	if !strings.Contains(reasons[reviewer.ID], "not part of the fan-out") {
		t.Fatalf("the reviewer was %q, want it skipped as a role helper", reasons[reviewer.ID])
	}
}

func TestPinStatusIsTheOwningExtensionsAlone(t *testing.T) {
	h := newSessionHarness(t)
	useRoles(t)
	manager := hooks.NewManager(h.sessions.configDir)
	reviewer := helperOf(t, h, h.caller.ID, "board/reviewer")

	if err := h.sessions.BoardPinStatus("board", reviewer.ID, status.Waiting); err != nil {
		t.Fatal(err)
	}
	if got, ok := manager.Read(reviewer.ID); !ok || got != status.Waiting {
		t.Fatalf("status file = %q, %v", got, ok)
	}
	if row, _ := h.store.Get(reviewer.ID); row.Status != status.Waiting {
		t.Fatalf("row status = %q", row.Status)
	}

	if err := h.sessions.BoardPinStatus("other", reviewer.ID, status.Finished); err == nil || !strings.Contains(err.Error(), `not launched by extension "other"`) {
		t.Fatalf("another extension's pin = %v", err)
	}
	linter := helperOf(t, h, h.caller.ID, "board/linter")
	if err := h.sessions.BoardPinStatus("board", linter.ID, status.Waiting); err == nil || !strings.Contains(err.Error(), "does not pin its status") {
		t.Fatalf("a role without PinnedStatus = %v", err)
	}
	if err := h.sessions.BoardPinStatus("board", h.caller.ID, status.Waiting); err == nil {
		t.Fatal("pinned a session with no role")
	}
	for _, bad := range []string{"starting", "dead", "asleep"} {
		if err := h.sessions.BoardPinStatus("board", reviewer.ID, bad); err == nil || !strings.Contains(err.Error(), "not a status to pin") {
			t.Fatalf("pinning %q = %v", bad, err)
		}
	}
	if got, _ := manager.Read(reviewer.ID); got != status.Waiting {
		t.Fatalf("a refused pin changed the file to %q", got)
	}

	if err := h.sessions.BoardPinStatus("board", reviewer.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.Read(reviewer.ID); ok {
		t.Fatal("releasing the pin left the file")
	}
}
