package sessioncmd

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

func TestStopProofRequiresTheFreshConversationOutput(t *testing.T) {
	proof := uuid.NewString()
	for _, raw := range []string{"", stopProofPrefix + uuid.NewString(), proof} {
		ctx, cancel := context.WithCancel(context.Background())
		err := awaitStopProof(ctx, proof, func() (string, error) {
			cancel()
			return raw, nil
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("unproven caller accepted: %q, %v", raw, err)
		}
	}
	wrapped := stopProofPrefix + proof[:12] + "\n" + proof[12:]
	styled := stopProofPrefix + "\x1b[36m" + proof[:12] + "\x1b[0m\n\x1b[36m" + proof[12:] + "\x1b[0m"
	for _, raw := range []string{wrapped, styled} {
		if err := awaitStopProof(context.Background(), proof, func() (string, error) { return raw, nil }); err != nil {
			t.Fatalf("visible proof rejected: %q, %v", raw, err)
		}
	}
}

func TestStopProofPinsTheConversationAndPaneBeforeActing(t *testing.T) {
	h := newSessionHarness(t)
	row, err := h.store.Get(h.caller.ID)
	if err != nil {
		t.Fatal(err)
	}
	row.Tool = "codex"
	row.AgentSessionID = uuid.NewString()
	if err := h.store.UpdateTool(row.ID, row.Tool); err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetAgentSessionID(row.ID, row.AgentSessionID); err != nil {
		t.Fatal(err)
	}
	runtime, err := h.sessions.open()
	if err != nil {
		t.Fatal(err)
	}
	pin, err := runtime.paneProcess(row)
	runtime.store.Close()
	if err != nil {
		t.Fatal(err)
	}
	pane, err := h.driver.PaneID(row.ID)
	if err != nil {
		t.Fatal(err)
	}
	proof := uuid.NewString()
	cmd := exec.Command("tmux", "-L", h.driver.SocketName(), "send-keys", "-t", pane, "-l", stopProofPrefix+proof)
	cmd.Env = tmuxtest.Environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture output: %v: %s", err, out)
	}
	launch := row.LaunchTime().Format(time.RFC3339Nano)
	for _, wrong := range [][3]string{{"old", pane, pin}, {launch, "%999999", pin}, {launch, pane, "1:old"}} {
		if _, err := h.sessions.FinishStopWithProof(row.ID, wrong[0], wrong[1], wrong[2], row.AgentSessionID, proof, false, true); err == nil {
			t.Fatalf("accepted changed launch or pane: %v", wrong)
		}
	}
	if _, err := h.sessions.FinishStopWithProof(row.ID, launch, pane, pin, uuid.NewString(), proof, false, true); err == nil {
		t.Fatal("accepted a changed conversation binding")
	}
	result, err := h.sessions.FinishStopWithProof(row.ID, launch, pane, pin, row.AgentSessionID, proof, true, true)
	if err != nil || !result.DryRun || !h.driver.Exists(row.ID) {
		t.Fatalf("proof dry run: %+v, %v", result, err)
	}
	kept, err := h.store.Get(row.ID)
	if err != nil || kept.Archived {
		t.Fatalf("dry run changed lifecycle: %+v, %v", kept, err)
	}
	if _, err := h.sessions.FinishStopWithProof(row.ID, launch, pane, pin, row.AgentSessionID, proof, false, true); err != nil {
		t.Fatal(err)
	}
	ended, err := h.store.Get(row.ID)
	if err != nil || !ended.Archived || h.driver.Exists(row.ID) || !strings.EqualFold(ended.Status, "dead") {
		t.Fatalf("proof did not finish lifecycle: %+v, %v", ended, err)
	}
}
