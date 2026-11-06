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
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
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

func (r *runtime) sharedCodexThreadTarget() (store.Session, error) {
	shared, err := sharedCodexAncestor()
	if err != nil {
		return store.Session{}, err
	}
	thread := os.Getenv("CODEX_THREAD_ID")
	if !shared || !validCodexStopThread(thread) {
		return store.Session{}, errors.New("stop must run inside its own managed session pane")
	}
	rows, err := r.store.ListSessions(true)
	if err != nil {
		return store.Session{}, err
	}
	var target store.Session
	for _, row := range rows {
		if !row.Archived && row.Status != "dead" && row.Tool == "codex" && row.AgentSessionID == thread {
			if target.ID != "" {
				return store.Session{}, errors.New("calling Codex thread belongs to multiple Gate Inbox rows")
			}
			target = row
		}
	}
	if target.ID == "" {
		return store.Session{}, errors.New("stop must run inside its own managed session pane")
	}
	if err := r.reach(target); err != nil {
		return store.Session{}, err
	}
	return target, nil
}

func validCodexStopThread(thread string) bool {
	id, err := uuid.Parse(thread)
	return err == nil && id.String() == thread
}

func validOpencodeStopConversation(conversation string) bool {
	return len(conversation) > len("ses_") && strings.HasPrefix(conversation, "ses_") && extension.ValidSessionID(conversation)
}

func (r *runtime) isOpencode(tool string) bool {
	return r.cfg.Tools[tool].SessionStore == "opencode"
}

func (r *runtime) stopCandidate(target store.Session) error {
	if r.isOpencode(target.Tool) {
		return r.opencodeStopCandidate(target)
	}
	return r.codexStopCandidate(target)
}

// opencodeStopCandidate rechecks, right before a stop is queued, that the
// pane's own marker still names the row the caller resolved to.
func (r *runtime) opencodeStopCandidate(target store.Session) error {
	shared, err := sharedOpencodeAncestor()
	if err != nil {
		return err
	}
	if !shared || !r.isOpencode(target.Tool) || target.Archived || target.Status == "dead" || !validOpencodeStopConversation(target.AgentSessionID) {
		return errors.New("stop must run inside its own managed session pane")
	}
	pane, socket := tmux.OwnPane()
	if pane == "" || socket == "" {
		return errors.New("stop must run inside its own managed session pane")
	}
	marker, ok := hooks.NewManager(r.configDir).AdoptedPaneAt(os.Getenv("TMUX"), pane)
	if !ok || marker.ID != target.ID || marker.Tool != target.Tool || marker.Conversation != target.AgentSessionID ||
		target.TmuxPaneID != pane || target.TmuxSocket != socket {
		return errors.New("stop must run inside its own managed session pane")
	}
	return nil
}

func (r *runtime) sharedOpencodeTarget() (store.Session, error) {
	shared, err := sharedOpencodeAncestor()
	if err != nil {
		return store.Session{}, err
	}
	if !shared {
		return store.Session{}, errors.New("stop must run inside its own managed session pane")
	}
	return r.opencodeMarkerTarget()
}

// opencodeMarkerTarget binds a shared-service shell to the one active row
// its pane marker names. The marker carries the conversation, and rows that
// duplicate it without a pane are ignored: only a row bound to this exact
// socket and pane may win, and never more than one.
func (r *runtime) opencodeMarkerTarget() (store.Session, error) {
	pane, socket := tmux.OwnPane()
	if pane == "" || socket == "" {
		return store.Session{}, errors.New("cannot verify a Gate Inbox caller pane from the shared OpenCode service; refusing shutdown")
	}
	marker, ok := hooks.NewManager(r.configDir).AdoptedPaneAt(os.Getenv("TMUX"), pane)
	if !ok || !r.isOpencode(marker.Tool) {
		return store.Session{}, ErrUnmanagedCaller
	}
	if !validOpencodeStopConversation(marker.Conversation) {
		return store.Session{}, errors.New("cannot verify an OpenCode conversation from this pane's marker; refusing shutdown")
	}
	rows, err := r.store.ListSessions(true)
	if err != nil {
		return store.Session{}, err
	}
	var markerRow *store.Session
	var bound []store.Session
	for i := range rows {
		row := &rows[i]
		if row.ID == marker.ID {
			markerRow = row
		}
		if row.Tool != marker.Tool || row.AgentSessionID != marker.Conversation || row.Archived || row.Status == "dead" {
			continue
		}
		if row.TmuxSocket == socket && row.TmuxPaneID == pane {
			bound = append(bound, *row)
		}
	}
	if markerRow != nil {
		switch {
		case markerRow.Archived || markerRow.Status == "dead":
			return store.Session{}, errors.New("the calling pane's Gate Inbox row has stopped; refusing shutdown")
		case markerRow.Tool != marker.Tool || markerRow.AgentSessionID != marker.Conversation:
			return store.Session{}, errors.New("the calling pane's marker no longer matches its Gate Inbox row; refusing shutdown")
		case markerRow.TmuxSocket != socket || markerRow.TmuxPaneID != pane:
			return store.Session{}, errors.New("the calling pane's Gate Inbox row is bound to another pane; refusing shutdown")
		}
	}
	switch len(bound) {
	case 0:
		return store.Session{}, errors.New("the calling OpenCode conversation is not bound to this pane; refusing shutdown")
	case 1:
	default:
		return store.Session{}, errors.New("the calling OpenCode conversation belongs to multiple Gate Inbox rows; refusing shutdown")
	}
	if markerRow == nil || bound[0].ID != marker.ID {
		return store.Session{}, errors.New("the calling pane's marker names no bound Gate Inbox row; refusing shutdown")
	}
	target := bound[0]
	if err := r.reach(target); err != nil {
		return store.Session{}, err
	}
	return target, nil
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
	if thread == "" || target.AgentSessionID != thread {
		return StopResult{}, errors.New("stop proof requires a bound conversation")
	}
	switch {
	case target.Tool == "codex" && validCodexStopThread(thread):
	case runtime.isOpencode(target.Tool) && validOpencodeStopConversation(thread):
	default:
		return StopResult{}, errors.New("stop proof requires a bound Codex or OpenCode conversation")
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
