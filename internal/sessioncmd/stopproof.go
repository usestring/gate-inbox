package sessioncmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"github.com/google/uuid"
	"github.com/usestring/gate-inbox/internal/store"
)

const stopProofPrefix = "Gate Inbox stop proof: "

func (r *runtime) codexStopCandidate(target store.Session) error {
	shared, err := sharedCodexAncestor()
	if err != nil {
		return err
	}
	if !shared || target.Tool != "codex" || target.Status == "dead" || !validCodexStopThread(target.AgentSessionID) || target.AgentSessionID != os.Getenv("CODEX_THREAD_ID") {
		return errors.New("stop must run inside its own managed session pane")
	}
	rows, err := r.store.ListSessions(true)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.ID != target.ID && !row.Archived && row.Status != "dead" && row.Tool == "codex" && row.AgentSessionID == target.AgentSessionID {
			return errors.New("calling Codex thread belongs to multiple Gate Inbox rows")
		}
	}
	return nil
}

func validCodexStopThread(thread string) bool {
	id, err := uuid.Parse(thread)
	return err == nil && id.String() == thread
}

// A shared host's stdout is routed to the calling conversation, unlike its
// inherited pane environment. The worker only reads: it must never put its
// own challenge into the target pane and thereby manufacture ownership.
func (s *Sessions) FinishStopWithProof(sessionID, launched, pane, processPin, thread, proof string, dryRun, archive bool) (StopResult, error) {
	parsed, err := uuid.Parse(proof)
	if err != nil || parsed.String() != proof {
		return StopResult{}, errors.New("invalid stop caller proof")
	}
	runtime, err := s.open()
	if err != nil {
		return StopResult{}, err
	}
	defer runtime.store.Close()
	target, err := runtime.agent(sessionID)
	if err != nil {
		return StopResult{}, err
	}
	if target.Tool != "codex" || thread == "" || target.AgentSessionID != thread {
		return StopResult{}, errors.New("stop proof requires a bound Codex conversation")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	err = awaitStopProof(ctx, proof, func() (string, error) {
		row, err := runtime.store.Get(target.ID)
		if err != nil {
			return "", err
		}
		if launched != row.LaunchTime().Format(time.RFC3339Nano) || row.AgentSessionID != target.AgentSessionID || row.TmuxPaneID != target.TmuxPaneID || row.TmuxSocket != target.TmuxSocket {
			return "", errors.New("stop proof no longer matches this conversation launch")
		}
		if err := runtime.reach(row); err != nil {
			return "", err
		}
		currentPane, err := runtime.driver.PaneID(target.ID)
		if err != nil || currentPane != pane {
			return "", errors.New("stop proof no longer matches this session's pane")
		}
		pin, err := runtime.paneProcess(row)
		if err != nil || pin != processPin {
			return "", errors.New("stop proof no longer matches this pane process")
		}
		return runtime.driver.CapturePane(target.ID)
	})
	if err != nil {
		return StopResult{}, err
	}
	result := StopResult{Managed: true, Target: runtime.sessionInfo(target, true, true), DryRun: dryRun, Archive: archive}
	if dryRun {
		return result, nil
	}
	_, err = s.finishStop(sessionID, launched, pane, processPin, thread, archive)
	return result, err
}

func awaitStopProof(ctx context.Context, proof string, read func() (string, error)) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	wanted := compactProof(stopProofPrefix + proof)
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("stop caller proof did not reach its owning pane: %w", err)
		}
		raw, err := read()
		if err != nil {
			return err
		}
		if strings.Contains(compactProof(raw), wanted) {
			return nil
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
}

func compactProof(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, ansi.Strip(s))
}
