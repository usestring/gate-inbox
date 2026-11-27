package extension_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/extension"
)

// gate answers every turn with verdict, or fails, panics or hangs, and logs
// that it was asked.
type gate struct {
	id      string
	verdict extension.TurnVerdict
	err     error
	panics  bool
	hang    bool
	off     bool
	asked   *[]string
}

func (g *gate) Descriptor() extension.Descriptor { return extension.Descriptor{ID: g.id} }
func (g *gate) Configure(extension.Config) error { return nil }
func (g *gate) Enabled() bool                    { return !g.off }

func (g *gate) GateTurn(ctx context.Context, turn extension.Turn) (extension.TurnVerdict, error) {
	*g.asked = append(*g.asked, g.id+" "+string(turn.Source))
	switch {
	case g.panics:
		panic("broken gate")
	case g.hang:
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond)
		return extension.TurnVerdict{Hold: true}, nil
	}
	return g.verdict, g.err
}

func TestTurnGatesAskInOrderUntilOneHolds(t *testing.T) {
	var asked []string
	hooks := sessionHooks(t,
		&gate{id: "open", asked: &asked},
		&gate{id: "off", off: true, verdict: extension.TurnVerdict{Hold: true}, asked: &asked},
		&gate{id: "mover", verdict: extension.TurnVerdict{Hold: true, Requeue: true, Reason: "  moving\n to another   account "}, asked: &asked},
		&gate{id: "never", verdict: extension.TurnVerdict{Hold: true}, asked: &asked},
	)
	got, err := hooks.GateTurn(context.Background(), extension.Turn{Source: extension.TurnFromPrompt}, time.Second)
	if err != nil || !got.Hold || !got.Requeue || got.Reason != "moving to another account" {
		t.Fatalf("verdict %+v %v", got, err)
	}
	if strings.Join(asked, ",") != "open prompt,mover prompt" {
		t.Fatalf("asked %v", asked)
	}
	// Requeue means nothing from the board: the message is still queued.
	asked = nil
	got, _ = hooks.GateTurn(context.Background(), extension.Turn{Source: extension.TurnFromBoard, MessageID: 7}, time.Second)
	if !got.Hold || got.Requeue {
		t.Fatalf("board verdict %+v", got)
	}
}

// A gate that errors, panics or runs past its bound is skipped and the turn
// goes ahead; its failure comes back for the host to log.
func TestTurnGatesFailOpen(t *testing.T) {
	var asked []string
	hooks := sessionHooks(t,
		&gate{id: "errs", err: errors.New("cannot reach it"), asked: &asked},
		&gate{id: "panics", panics: true, asked: &asked},
		&gate{id: "hangs", hang: true, asked: &asked},
	)
	start := time.Now()
	got, err := hooks.GateTurn(context.Background(), extension.Turn{Source: extension.TurnFromBoard}, 100*time.Millisecond)
	if got.Hold {
		t.Fatalf("a failing gate held the turn: %+v", got)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("a hung gate held the host for %s", took)
	}
	for _, want := range []string{`"errs": cannot reach it`, `"panics": panicked`, `"hangs": did not answer in time`} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error %v lacks %q", err, want)
		}
	}
}

func TestTurnGateDefaults(t *testing.T) {
	var none *extension.SessionHooks
	if got, err := none.GateTurn(context.Background(), extension.Turn{}, time.Second); got.Hold || err != nil {
		t.Fatalf("nil hooks: %+v %v", got, err)
	}
	var asked []string
	hooks := sessionHooks(t, &gate{id: "quiet", verdict: extension.TurnVerdict{Hold: true}, asked: &asked})
	got, _ := hooks.GateTurn(context.Background(), extension.Turn{Source: extension.TurnFromPrompt}, time.Second)
	if got.Reason != `extension quiet is holding this turn` {
		t.Fatalf("default reason %q", got.Reason)
	}
}
