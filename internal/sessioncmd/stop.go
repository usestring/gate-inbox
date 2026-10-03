package sessioncmd

import (
	"errors"
	"os"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/tmux"
)

type StopResult struct {
	Target Session `json:"session"`
	DryRun bool    `json:"dry_run"`
}

// Stop leaves the lifecycle worker outside the pane it will end, so killing
// the caller cannot interrupt the end record or the extensions' kill notices.
func (s *Sessions) Stop(sessionID string, dryRun bool) (StopResult, error) {
	runtime, err := s.open()
	if err != nil {
		return StopResult{}, err
	}
	defer runtime.store.Close()
	target, err := runtime.agent(sessionID)
	if err != nil {
		return StopResult{}, err
	}
	if target.TmuxPaneID != "" || runtime.driver.OwnSessionID() != target.ID {
		return StopResult{}, errors.New("stop must run inside its own managed session pane")
	}
	result := StopResult{Target: runtime.sessionInfo(target, true, true), DryRun: dryRun}
	if dryRun {
		return result, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return StopResult{}, err
	}
	command := []string{"env", "GATE_INBOX_HOME=" + s.configDir,
		"GATE_INBOX_SESSION_ID=" + target.ID, "GATE_INBOX_TMUX_SOCKET=" + runtime.driver.SocketName()}
	for _, name := range []string{"HOME", "PATH", "TMUX_TMPDIR", "GATE_INBOX_TEST_TMUX_TMPDIR"} {
		if value, ok := os.LookupEnv(name); ok {
			command = append(command, name+"="+value)
		}
	}
	command = append(command, executable, "_finish-stop", target.LaunchTime().Format(time.RFC3339Nano), os.Getenv("TMUX_PANE"))
	for i, word := range command {
		command[i] = tmux.ShellQuote(word)
	}
	if err := runtime.driver.RunOutsidePane(target.ID, strings.Join(command, " ")); err != nil {
		return StopResult{}, err
	}
	// A shell running stop as its last command must not exit its pane before
	// the worker verifies it and takes ownership of the lifecycle writes.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !runtime.driver.Exists(target.ID) {
			return result, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return StopResult{}, errors.New("queued stop did not end this session within 10 seconds")
}

// FinishStop pins the queued request to one launch and pane: an intervening
// revive must not let the old worker end a new agent on the same board row.
func (s *Sessions) FinishStop(sessionID, launched, pane string) (Session, error) {
	runtime, err := s.open()
	if err != nil {
		return Session{}, err
	}
	defer runtime.store.Close()
	target, err := runtime.agent(sessionID)
	if err != nil {
		return Session{}, err
	}
	if target.TmuxPaneID != "" || launched != target.LaunchTime().Format(time.RFC3339Nano) {
		return Session{}, errors.New("stop request no longer matches this managed launch")
	}
	current, err := runtime.driver.PaneID(target.ID)
	if err != nil {
		return Session{}, err
	}
	if pane == "" || current != pane {
		return Session{}, errors.New("stop request no longer matches this session's pane")
	}
	return s.stopSession(runtime, target, extension.KillByCLI, sessionID)
}
