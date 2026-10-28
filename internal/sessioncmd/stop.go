package sessioncmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shirou/gopsutil/v4/process"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

var ErrUnmanagedCaller = errors.New("caller is not a Gate Inbox session")

type StopResult struct {
	Managed     bool    `json:"managed"`
	Target      Session `json:"session"`
	DryRun      bool    `json:"dry_run"`
	Archive     bool    `json:"archive"`
	CallerProof string  `json:"caller_proof,omitempty"`
}

// Stop leaves the lifecycle worker outside the pane it will end, so killing
// the caller cannot interrupt the end record or the extensions' kill notices.
// With archive the worker also files the row out of the active list once the
// session has ended: a session cannot archive itself through Archive, which
// would end its caller before the row is written.
func (s *Sessions) Stop(sessionID string, dryRun, archive bool) (StopResult, error) {
	if sessionID == "" {
		if _, err := os.Stat(filepath.Join(s.configDir, "state.db")); errors.Is(err, os.ErrNotExist) {
			return StopResult{}, ErrUnmanagedCaller
		} else if err != nil {
			return StopResult{}, err
		}
	}
	runtime, err := s.open()
	if err != nil {
		return StopResult{}, err
	}
	defer runtime.store.Close()
	target, err := runtime.stopCaller(sessionID)
	if err != nil {
		return StopResult{}, err
	}
	pin, err := runtime.paneProcess(target)
	if err != nil {
		return StopResult{}, err
	}
	owned, err := runtime.paneAncestor(target)
	if err != nil {
		return StopResult{}, err
	}
	result := StopResult{Managed: true, Target: runtime.sessionInfo(target, true, true), DryRun: dryRun, Archive: archive}
	if !owned {
		if err := runtime.codexStopCandidate(target); err != nil {
			return StopResult{}, err
		}
		result.CallerProof = uuid.NewString()
	}
	if dryRun && owned {
		return result, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return StopResult{}, err
	}
	command := []string{"env", "GATE_INBOX_HOME=" + s.configDir,
		"GATE_INBOX_SESSION_ID=" + target.ID, "CODEX_THREAD_ID=", "GATE_INBOX_TMUX_SOCKET=" + runtime.driver.SocketName()}
	for _, name := range []string{"HOME", "PATH", "TMUX_TMPDIR", "GATE_INBOX_TEST_TMUX_TMPDIR"} {
		if value, ok := os.LookupEnv(name); ok {
			command = append(command, name+"="+value)
		}
	}
	pane, err := runtime.driver.PaneID(target.ID)
	if err != nil {
		return StopResult{}, err
	}
	command = append(command, executable, "_finish-stop")
	if archive {
		command = append(command, "--archive")
	}
	if result.CallerProof != "" {
		command = append(command, "--caller-proof", result.CallerProof, "--caller-thread", target.AgentSessionID)
		if dryRun {
			command = append(command, "--dry-run")
		}
	}
	command = append(command, target.LaunchTime().Format(time.RFC3339Nano), pane, pin)
	for i, word := range command {
		command[i] = tmux.ShellQuote(word)
	}
	if err := runtime.driver.RunOutsidePane(target.ID, strings.Join(command, " ")); err != nil {
		return StopResult{}, err
	}
	if result.CallerProof != "" {
		return result, nil
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
func (s *Sessions) FinishStop(sessionID, launched, pane, processPin string, archive bool) (Session, error) {
	return s.finishStop(sessionID, launched, pane, processPin, "", archive)
}

func (s *Sessions) finishStop(sessionID, launched, pane, processPin, thread string, archive bool) (Session, error) {
	runtime, err := s.open()
	if err != nil {
		return Session{}, err
	}
	defer runtime.store.Close()
	target, err := runtime.agent(sessionID)
	if err != nil {
		return Session{}, err
	}
	if launched != target.LaunchTime().Format(time.RFC3339Nano) {
		return Session{}, errors.New("stop request no longer matches this managed launch")
	}
	if thread != "" && thread != target.AgentSessionID {
		return Session{}, errors.New("stop request no longer matches this conversation")
	}
	if err := runtime.reach(target); err != nil {
		return Session{}, err
	}
	current, err := runtime.driver.PaneID(target.ID)
	if err != nil {
		return Session{}, err
	}
	if pane == "" || current != pane {
		return Session{}, errors.New("stop request no longer matches this session's pane")
	}
	currentPin, err := runtime.paneProcess(target)
	if err != nil || currentPin != processPin {
		return Session{}, errors.New("stop request no longer matches this pane process")
	}
	stopped, err := s.stopSession(runtime, target, extension.KillByCLI, target.ID, func(id string) error {
		row, err := runtime.store.Get(id)
		if err != nil {
			return err
		}
		if launched != row.LaunchTime().Format(time.RFC3339Nano) {
			return errors.New("stop request no longer matches this managed launch")
		}
		if thread != "" && thread != row.AgentSessionID {
			return errors.New("stop request no longer matches this conversation")
		}
		if row.TmuxPaneID != target.TmuxPaneID || row.TmuxSocket != target.TmuxSocket {
			return errors.New("stop request no longer matches this adopted target")
		}
		pid, _, _ := strings.Cut(processPin, ":")
		if target.TmuxPaneID != "" {
			return runtime.driver.KillAdoptedPane(id, pane, pid)
		}
		return runtime.driver.KillPane(id, pane, pid)
	})
	if err != nil || !archive {
		return stopped, err
	}
	archived, err := runtime.store.ArchiveStoppedLaunch(target)
	if err != nil {
		return Session{}, err
	}
	stopped.Archived = archived
	return stopped, nil
}

func (r *runtime) stopCaller(id string) (store.Session, error) {
	if id != "" {
		target, err := r.agent(id)
		if err != nil {
			return target, err
		}
		if err := r.reach(target); err != nil {
			return target, err
		}
		return target, nil
	}
	rows, err := r.store.ListSessions(true)
	if err != nil {
		return store.Session{}, err
	}
	var matches []store.Session
	var codexCandidates []store.Session
	unresolvedCodex := false
	shared, err := sharedCodexAncestor()
	if err != nil {
		return store.Session{}, err
	}
	for _, row := range rows {
		if r.cfg.Tools[row.Tool].Shell {
			continue
		}
		if err := r.reach(row); err != nil {
			return store.Session{}, err
		}
		if !row.Archived && row.Status != "dead" {
			if shared && row.Tool == "codex" && validCodexStopThread(row.AgentSessionID) && row.AgentSessionID == os.Getenv("CODEX_THREAD_ID") {
				codexCandidates = append(codexCandidates, row)
			}
			owned, err := r.paneAncestor(row)
			if err != nil {
				return store.Session{}, err
			}
			if owned {
				matches = append(matches, row)
			}
		}
		if row.Tool == "codex" && (row.AgentSessionID == os.Getenv("CODEX_THREAD_ID") && row.AgentSessionID != "" || row.TmuxPaneID != "" && shared) {
			unresolvedCodex = true
		}
	}
	if len(codexCandidates) > 1 {
		return store.Session{}, errors.New("calling Codex thread belongs to multiple Gate Inbox rows")
	}
	if len(codexCandidates) == 1 {
		return codexCandidates[0], nil
	}
	if len(matches) > 1 {
		return store.Session{}, errors.New("calling process belongs to multiple Gate Inbox rows")
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if unresolvedCodex {
		return store.Session{}, errors.New("cannot verify a Gate Inbox caller pane from the shared Codex daemon; refusing shutdown")
	}
	return store.Session{}, ErrUnmanagedCaller
}

func (r *runtime) paneProcess(target store.Session) (string, error) {
	state, err := r.driver.PaneState(target.ID, "#{pane_pid}")
	if err != nil {
		return "", err
	}
	pid, err := strconv.ParseInt(strings.TrimSpace(state), 10, 32)
	if err != nil || pid <= 0 {
		return "", errors.New("pane process is unavailable")
	}
	proc, err := process.NewProcess(int32(pid))
	if err != nil {
		return "", err
	}
	started, err := proc.CreateTime()
	if err != nil || started <= 0 {
		return "", errors.New("pane process start identity is unavailable")
	}
	return fmt.Sprintf("%d:%d", pid, started), nil
}

func (r *runtime) paneAncestor(target store.Session) (bool, error) {
	state, err := r.driver.PaneState(target.ID, "#{pane_pid}")
	if err == nil && strings.TrimSpace(state) == "" {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	number, err := strconv.Atoi(strings.TrimSpace(state))
	if err != nil || number <= 0 {
		return false, errors.New("invalid pane process identity")
	}
	ancestors, err := callerAncestors()
	if err != nil {
		return false, err
	}
	return slices.Contains(ancestors, number), nil
}

func sharedCodexAncestor() (bool, error) {
	ancestors, err := callerAncestors()
	if err != nil {
		return false, err
	}
	for _, pid := range ancestors {
		proc, err := process.NewProcess(int32(pid))
		if err != nil {
			return false, err
		}
		argv, err := proc.CmdlineSlice()
		if err != nil {
			return false, err
		}
		if len(argv) < 2 || filepath.Base(argv[0]) != "codex" {
			continue
		}
		if argv[1] == "app-server" || argv[1] == "exec-server" {
			return true, nil
		}
	}
	return false, nil
}

func callerAncestors() ([]int, error) {
	pid := int32(os.Getpid())
	var ancestors []int
	for len(ancestors) < 32 && pid > 1 {
		ancestors = append(ancestors, int(pid))
		proc, err := process.NewProcess(pid)
		if err != nil {
			return nil, err
		}
		pid, err = proc.Ppid()
		if err != nil {
			return nil, err
		}
	}
	if pid > 1 {
		return nil, errors.New("caller ancestry exceeds verification bound")
	}
	return ancestors, nil
}
