package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/codexinput"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

func TestCodexInboxSteersActiveTurnWithoutTerminalInput(t *testing.T) {
	p, sess, msg := codexInbox(t)
	id := msg.ID
	called := false
	p.steerCodex = func(ctx context.Context, thread, text string, claim codexinput.Claim) (codexinput.Outcome, error) {
		called = true
		if thread != sess.AgentSessionID || !strings.Contains(text, "focus on the regression") || !strings.Contains(text, "CROSS-SESSION-MESSAGE") {
			t.Errorf("wrong destination or lost provenance: %s %s", thread, text)
		}
		ok, err := claim()
		if err != nil || !ok {
			return codexinput.NotSent, err
		}
		state, err := p.store.Message(id, "sender01")
		if err != nil {
			return codexinput.NotSent, err
		}
		if !state.DeliveredAt.IsZero() {
			t.Error("reported delivered before Codex acknowledged it")
		}
		return codexinput.Accepted, nil
	}
	// A nil tmux driver makes any paste, key, interrupt or draft edit fail.
	capture := tmux.Capture{Text: "• Working (5s • esc to interrupt)\n\n› operator draft", At: time.Now()}
	if err := p.maybeDeliverInbox(sess, map[string]store.InboxMessage{sess.ID: msg}, capture, status.Working, true); err != nil {
		t.Fatal(err)
	}
	if !p.awaitSends(10 * time.Second) {
		t.Fatal("delivery did not settle")
	}
	if !called {
		t.Fatal("active Codex message waited for idle instead of steering")
	}
	state, err := p.store.Message(id, "sender01")
	if err != nil {
		t.Fatal(err)
	}
	if state.DeliveredAt.IsZero() || !state.DroppedAt.IsZero() {
		t.Fatalf("accepted message not delivered: %+v", state)
	}
}

func codexInbox(t *testing.T) (*poller, store.Session, store.InboxMessage) {
	t.Helper()
	p, sess := newTestPollerWithSession(t)
	cfg, err := config.LoadDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p.engine, err = status.NewEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sess.AgentSessionID = "thread-1"
	sess.Status = status.Working
	if err := p.store.SetAgentSessionID(sess.ID, sess.AgentSessionID); err != nil {
		t.Fatal(err)
	}
	id, _, err := p.store.Enqueue(store.InboxMessage{SessionID: sess.ID, SenderID: "sender01", SenderName: "reviewer", Body: "focus on the regression", SentAt: time.Now()}, store.DefaultInboxLimits)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := p.store.Message(id, "sender01")
	if err != nil {
		t.Fatal(err)
	}
	return p, sess, msg
}

func TestCodexInboxKeepsUnsafeTargetsQueued(t *testing.T) {
	for _, name := range []string{"trust", "exec-approval", "patch-approval", "ask1", "missing-thread", "dead", "interrupt"} {
		t.Run(name, func(t *testing.T) {
			p, sess, msg := codexInbox(t)
			pane := "• Working (5s • esc to interrupt)\n\n› "
			alive := true
			switch name {
			case "missing-thread":
				sess.AgentSessionID = ""
			case "dead":
				alive = false
			case "interrupt":
				msg.Interrupt = true
			default:
				b, err := os.ReadFile(filepath.Join("..", "dialog", "testdata", "codex", "codex-0.157.0-"+name+"-w40.txt"))
				if err != nil {
					t.Fatal(err)
				}
				pane = string(b)
			}
			p.steerCodex = func(context.Context, string, string, codexinput.Claim) (codexinput.Outcome, error) {
				t.Error("attempted steering into an unsafe target")
				return codexinput.NotSent, nil
			}
			if err := p.maybeDeliverInbox(sess, map[string]store.InboxMessage{sess.ID: msg}, tmux.Capture{Text: pane, At: time.Now()}, status.Working, alive); err != nil {
				t.Fatal(err)
			}
			if !p.awaitSends(10 * time.Second) {
				t.Fatal("delivery did not settle")
			}
			state, err := p.store.Message(msg.ID, "sender01")
			if err != nil {
				t.Fatal(err)
			}
			if !state.ClaimedAt.IsZero() || !state.DeliveredAt.IsZero() {
				t.Fatalf("unsafe message left queue: %+v", state)
			}
		})
	}
}

func TestCodexInboxDoesNotRetryUnconfirmedInput(t *testing.T) {
	for _, outcome := range []codexinput.Outcome{codexinput.NotSent, codexinput.Unconfirmed} {
		t.Run(map[codexinput.Outcome]string{codexinput.NotSent: "rejected", codexinput.Unconfirmed: "lost-ack"}[outcome], func(t *testing.T) {
			p, sess, msg := codexInbox(t)
			calls := 0
			p.steerCodex = func(ctx context.Context, thread, text string, claim codexinput.Claim) (codexinput.Outcome, error) {
				calls++
				if ok, err := claim(); err != nil || !ok {
					t.Errorf("claim=%v error=%v", ok, err)
				}
				return outcome, errors.New("lost or rejected request")
			}
			for range 2 {
				heads, err := p.store.HeadMessages()
				if err != nil {
					t.Fatal(err)
				}
				if err := p.maybeDeliverInbox(sess, heads, tmux.Capture{Text: "• Working (5s • esc to interrupt)\n\n› ", At: time.Now()}, status.Working, true); err != nil {
					t.Fatal(err)
				}
				if !p.awaitSends(10 * time.Second) {
					t.Fatal("delivery did not settle")
				}
			}
			state, err := p.store.Message(msg.ID, "sender01")
			if err != nil {
				t.Fatal(err)
			}
			if outcome == codexinput.NotSent {
				if calls != 2 || !state.ClaimedAt.IsZero() || !state.DeliveredAt.IsZero() {
					t.Fatalf("rejected input not retryable: calls=%d state=%+v", calls, state)
				}
			} else if calls != 1 || state.DroppedAt.IsZero() {
				t.Fatalf("ambiguous input retried or reported delivered: calls=%d state=%+v", calls, state)
			}
		})
	}
}
