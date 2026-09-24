// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package sessioncmd

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/adopt"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/sessionhooks"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
	"github.com/usestring/gate-inbox/internal/tracing"
)

// ParkResult is what one park pass did to the board.
type ParkResult struct {
	// DryRun means Parked is the plan: nothing was stopped or recorded.
	DryRun bool      `json:"dry_run" jsonschema:"whether this was a rehearsal that changed nothing"`
	Parked []Session `json:"parked" jsonschema:"sessions stopped and recorded for unpark"`
	// Promoted and Recovered are the Parked entries that were not managed
	// rows going in: an adopted pane turned into an owned row, and a gi_
	// session with no row given one. Both come back as managed sessions.
	Promoted  int `json:"promoted" jsonschema:"adopted panes stopped and turned into owned rows, so unpark restarts them as managed sessions"`
	Recovered int `json:"recovered" jsonschema:"gi_ sessions that had no board row and were given one before being stopped"`
	Terminals int `json:"terminals" jsonschema:"live terminals left running: a shell has no conversation to resume"`
	// Working names the parked sessions that were mid-turn when stopped.
	// The turn in progress is lost either way; recording it is what lets
	// unpark tell those agents to carry on.
	Working []string `json:"working,omitempty" jsonschema:"sessions stopped mid-turn; unpark tells them to continue"`
	Self    bool     `json:"self" jsonschema:"whether the calling session was left running"`
	// Orphans are gi_* sessions whose agent could not be identified, so no
	// row can say what to relaunch; they are left running and named.
	Orphans []string `json:"orphans,omitempty" jsonschema:"gi_ tmux sessions with no board row and no recognisable agent, left running"`
	// Warnings name a parked session unpark will not bring back as it was:
	// no conversation id was captured, its tool left the config, or its
	// directory is gone. Park stops it anyway, since the reboot would.
	Warnings []string `json:"warnings,omitempty" jsonschema:"parked sessions unpark cannot resume exactly, with the reason"`
	Errors   []string `json:"errors,omitempty" jsonschema:"sessions that could not be stopped, with the reason"`
	// Owed counts an earlier park's ids too, not only this pass's.
	Owed int `json:"owed" jsonschema:"sessions unpark will revive"`
}

// parkPass is the board as one park sees it, read once and shared by the
// managed, adopted and orphan branches.
type parkPass struct {
	runtime     *runtime
	scan        tmux.PaneScan
	procs       *adopt.ProcTable
	claude      []convo.ClaudeSession
	engine      *status.Engine
	result      ParkResult
	parked      []string
	interrupted []string
	dryRun      bool
}

// Park stops every live agent session on the board and records the set,
// so a machine can be rebooted and Unpark can bring exactly those sessions
// back. A managed session is stopped as Kill does. An adopted pane is
// stopped too, and its row promoted to a managed one: the conversation is
// read off the agent's own process, so Unpark restarts it as a gi_
// session on that conversation rather than in the window it borrowed. An
// gi_ session with no row is given one first, when its agent can be told
// from its process tree. Terminals, the calling session and rows already
// dead are left as they are and counted. Unlike the other commands it
// needs no calling session: the reboot is run from a plain shell, and a
// session that is parking the board is itself about to go.
//
// A dry run walks the same board and reports the same plan without
// stopping anything or touching the set, which is how the plan is checked
// against a live board that must stay up.
func (s *Sessions) Park(sessionID string, dryRun bool) (result ParkResult, err error) {
	// One pass over the whole board, ending every session on it. A person
	// runs this before a reboot and watches it, so what it costs per board is
	// worth having as a number rather than as an impression.
	op := start("sessioncmd.park", tracing.Attr{Key: "dry_run", Value: dryRun})
	defer func() { op.done(&err, tracing.Attr{Key: "parked", Value: len(result.Parked)}) }()
	runtime, err := s.open()
	if err != nil {
		return ParkResult{}, err
	}
	defer runtime.store.Close()
	sessions, err := runtime.store.ListSessions(false)
	if err != nil {
		return ParkResult{}, err
	}
	// This process holds no adopted registry, so the rows have to tell the
	// driver where their panes are before the scan can say whether they
	// are up.
	for _, sess := range sessions {
		if sess.TmuxPaneID != "" {
			if err := runtime.driver.Adopt(sess.ID, tmux.Target{Socket: sess.TmuxSocket, Name: sess.TmuxPaneID}); err != nil {
				return ParkResult{}, err
			}
		}
	}
	scan, err := runtime.driver.ScanPanes()
	if err != nil {
		return ParkResult{}, err
	}
	engine, err := status.NewEngine(runtime.cfg)
	if err != nil {
		return ParkResult{}, err
	}
	pass := &parkPass{
		runtime: runtime,
		scan:    scan,
		procs:   adopt.NewProcTable(),
		claude:  convo.LiveClaudeSessions(s.claudeHome),
		engine:  engine,
		result:  ParkResult{DryRun: dryRun},
		dryRun:  dryRun,
	}
	rows := make(map[string]bool, len(sessions))
	for _, sess := range sessions {
		rows[sess.ID] = true
		if scan.PIDs[sess.ID] == 0 {
			continue
		}
		switch {
		case sess.TmuxPaneID != "":
			s.parkAdopted(pass, sess)
		case runtime.cfg.Tools[sess.Tool].Shell:
			pass.result.Terminals++
		case sess.ID == sessionID:
			pass.result.Self = true
		default:
			s.parkManaged(pass, sess)
		}
	}
	orphans := make([]string, 0)
	for id := range scan.PIDs {
		if !rows[id] {
			orphans = append(orphans, id)
		}
	}
	sort.Strings(orphans)
	for _, id := range orphans {
		s.parkOrphan(pass, id)
	}
	owed, err := runtime.store.Parked()
	if err != nil {
		return ParkResult{}, err
	}
	if dryRun {
		for _, id := range pass.parked {
			if !slices.Contains(owed, id) {
				owed = append(owed, id)
			}
		}
	} else {
		if err := runtime.store.AddParked(pass.parked); err != nil {
			return ParkResult{}, err
		}
		if err := runtime.store.AddInterrupted(pass.interrupted); err != nil {
			return ParkResult{}, err
		}
		if owed, err = runtime.store.Parked(); err != nil {
			return ParkResult{}, err
		}
	}
	pass.result.Owed = len(owed)
	return pass.result, nil
}

func (p *parkPass) fail(sess store.Session, err error) {
	p.result.Errors = append(p.result.Errors, fmt.Sprintf("%s (id %s): %v", sess.Name, sess.ID, err))
}

// record puts a stopped, or about-to-be-stopped, row on the result. The
// warning and the mid-turn check both read state the kill destroys: which
// conversation the pane held, and what its screen was showing.
func (p *parkPass) record(sess store.Session, pane string) {
	if warning := reviveWarning(p.runtime, sess); warning != "" {
		p.result.Warnings = append(p.result.Warnings, fmt.Sprintf("%s (id %s): %s", sess.Name, sess.ID, warning))
	}
	if p.working(sess, pane) {
		p.result.Working = append(p.result.Working, sess.ID)
		p.interrupted = append(p.interrupted, sess.ID)
	}
	if !p.dryRun {
		sess.Status = status.Dead
	}
	p.result.Parked = append(p.result.Parked, p.runtime.sessionInfo(sess, p.dryRun, false))
	p.parked = append(p.parked, sess.ID)
}

// working reads the screen the way the poller does, so a row whose stored
// status is stale because no manager was polling still gets the right
// answer; only a screen the rules cannot read falls back to the row.
func (p *parkPass) working(sess store.Session, pane string) bool {
	if pane != "" {
		if state, ok := p.engine.Match(sess.Tool, ansi.Strip(pane)); ok {
			return state == status.Working
		}
	}
	return sess.Status == status.Working
}

func (s *Sessions) parkManaged(p *parkPass, sess store.Session) {
	pane, _ := p.runtime.driver.CapturePane(sess.ID)
	if !p.dryRun {
		if err := s.endSessionWith(p.runtime, sess, pane); err != nil {
			p.fail(sess, err)
			return
		}
	}
	p.record(sess, pane)
}

// parkAdopted stops a pane the manager never started and keeps its row as
// a managed one. The pane is somebody else's, and ending it is what a
// reboot would do anyway; what the reboot would not do is remember the
// conversation, which is read here off the agent's own process while it
// is still running.
func (s *Sessions) parkAdopted(p *parkPass, sess store.Session) {
	pid := p.scan.PIDs[sess.ID]
	convID, cwd := s.conversationInPane(p, sess.Tool, pid, sess.Cwd)
	if convID != "" {
		sess.AgentSessionID = convID
	}
	sess.Cwd = cwd
	promoted := sess
	promoted.TmuxSocket, promoted.TmuxPaneID = "", ""
	pane, _ := p.runtime.driver.CapturePane(sess.ID)
	if !p.dryRun {
		if pane != "" {
			if err := p.runtime.store.SetSnapshot(sess.ID, pane); err != nil {
				p.fail(sess, err)
				return
			}
		}
		if err := p.runtime.driver.KillAdopted(sess.ID); err != nil {
			p.fail(sess, err)
			return
		}
		p.runtime.driver.Release(sess.ID)
		if err := p.runtime.store.PromoteAdopted(sess.ID, cwd, convID); err != nil {
			p.fail(sess, err)
			return
		}
		if err := p.runtime.store.UpdateStatus(sess.ID, status.Dead); err != nil {
			p.fail(sess, err)
			return
		}
	}
	p.result.Promoted++
	p.record(promoted, pane)
}

// parkOrphan gives a gi_ session with no row one, when its process tree
// names a configured tool, and then stops it like any managed session. A
// pane whose agent cannot be told is left running and named: a row that
// relaunches the wrong program is worse than no row.
func (s *Sessions) parkOrphan(p *parkPass, id string) {
	pid := p.scan.PIDs[id]
	tool, ok := identifyTool(p.runtime.cfg.Tools, p.procs.Cmdlines(int32(pid)))
	if !ok {
		p.result.Orphans = append(p.result.Orphans, id)
		return
	}
	fallback, err := p.runtime.driver.PaneCurrentPath(id)
	if err != nil {
		fallback = ""
	}
	convID, cwd := s.conversationInPane(p, tool, pid, fallback)
	if cwd == "" {
		p.result.Orphans = append(p.result.Orphans, id)
		return
	}
	sess := store.Session{
		ID:             id,
		Name:           filepath.Base(cwd),
		NameSource:     store.SourceDerived,
		Tool:           tool,
		Cwd:            cwd,
		Status:         status.Idle,
		AgentSessionID: convID,
	}
	pane, _ := p.runtime.driver.CapturePane(id)
	if !p.dryRun {
		if err := p.runtime.store.CreateSession(sess); err != nil {
			p.fail(sess, err)
			return
		}
		if err := s.endSessionWith(p.runtime, sess, pane); err != nil {
			p.fail(sess, err)
			return
		}
	}
	p.result.Recovered++
	p.record(sess, pane)
}

// conversationInPane reads the conversation the agent in a pane holds, and
// the directory it holds it in, off the agent's own process. Claude Code
// writes a per-process session file naming both; a process match is an
// identity. Other tools fall back to the pane's directory and no id, which
// reviveWarning then reports.
func (s *Sessions) conversationInPane(p *parkPass, tool string, pid int, fallbackCwd string) (convID, cwd string) {
	cwd = fallbackCwd
	if tool != "claude" || pid == 0 {
		return "", cwd
	}
	session, ok := convo.ClaudeSessionInTree(p.claude, p.procs.PIDs(int32(pid)))
	if !ok {
		return "", cwd
	}
	if session.Cwd != "" {
		cwd = session.Cwd
	}
	return session.SessionID, cwd
}

// identifyTool names the configured tool whose program runs in a process
// tree, by the program name alone: the pane may have been launched with
// any flags, and a shell block is never a candidate, since a shell is what
// every pane runs.
func identifyTool(tools map[string]config.Tool, cmdlines []string) (string, bool) {
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		tool := tools[name]
		if tool.Shell || strings.TrimSpace(tool.Command) == "" {
			continue
		}
		program := filepath.Base(strings.Fields(tool.Command)[0])
		for _, line := range cmdlines {
			fields := strings.Fields(line)
			if len(fields) > 0 && filepath.Base(fields[0]) == program {
				return name, true
			}
		}
	}
	return "", false
}

// UnparkResult is what one unpark pass did to the board.
type UnparkResult struct {
	Revived []Session `json:"revived" jsonschema:"sessions relaunched on their old rows"`
	// Continued counts revives that had no conversation id and fell back to
	// the tool's revive command, which resumes the directory's most recent
	// conversation rather than the one the session held.
	Continued      int      `json:"continued" jsonschema:"revives that used the tool's continue command because no conversation id was captured"`
	Nudged         []string `json:"nudged,omitempty" jsonschema:"sessions that were mid-turn when parked and were told to continue on relaunch"`
	AlreadyRunning int      `json:"already_running" jsonschema:"parked sessions found running and dropped from the set"`
	Gone           int      `json:"gone" jsonschema:"parked sessions whose row was deleted or archived since, dropped from the set"`
	Errors         []string `json:"errors,omitempty" jsonschema:"sessions that could not be revived, kept in the set for a retry"`
	Owed           int      `json:"owed" jsonschema:"sessions still in the parked set after this pass"`
}

// Unpark relaunches every session Park recorded, oldest first, and drops
// each from the set as it comes back. A session that could not be
// relaunched stays in the set so the next Unpark tries it again; one that is
// already running, deleted or archived is dropped, since there is nothing
// left to owe it.
func (s *Sessions) Unpark(sessionID string) (unparked UnparkResult, err error) {
	// The other half, and the more expensive one: every parked session is a
	// CLI relaunched and a conversation resumed.
	op := start("sessioncmd.unpark")
	defer func() { op.done(&err, tracing.Attr{Key: "revived", Value: len(unparked.Revived)}) }()
	runtime, err := s.open()
	if err != nil {
		return UnparkResult{}, err
	}
	defer runtime.store.Close()
	ids, err := runtime.store.Parked()
	if err != nil {
		return UnparkResult{}, err
	}
	interrupted, err := runtime.store.Interrupted()
	if err != nil {
		return UnparkResult{}, err
	}
	var result UnparkResult
	var done []string
	var rows []store.Session
	for _, id := range ids {
		sess, err := runtime.store.Get(id)
		if errors.Is(err, sql.ErrNoRows) {
			result.Gone++
			done = append(done, id)
			continue
		}
		if err != nil {
			return UnparkResult{}, err
		}
		if sess.Archived {
			result.Gone++
			done = append(done, id)
			continue
		}
		rows = append(rows, sess)
	}
	// The order the board grew in, which puts a session before anything it
	// spawned, rather than the order park happened to stop them in.
	sort.SliceStable(rows, func(a, b int) bool {
		return rows[a].CreatedAt.Before(rows[b].CreatedAt)
	})
	for _, sess := range rows {
		if runtime.driver.Exists(sess.ID) {
			result.AlreadyRunning++
			done = append(done, sess.ID)
			continue
		}
		prompt := ""
		if slices.Contains(interrupted, sess.ID) {
			prompt = continuePrompt
		}
		tool, err := s.relaunch(runtime, sess, prompt)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s (id %s): %v", sess.Name, sess.ID, err))
			continue
		}
		if prompt != "" {
			result.Nudged = append(result.Nudged, sess.ID)
		}
		if sess.AgentSessionID == "" && tool.ResumeByIDCommand != "" {
			result.Continued++
		}
		sess.Status = tool.DefaultStatus
		result.Revived = append(result.Revived, runtime.sessionInfo(sess, true, sess.ID == sessionID))
		done = append(done, sess.ID)
	}
	if err := runtime.store.RemoveParked(done); err != nil {
		return UnparkResult{}, err
	}
	if err := runtime.store.RemoveInterrupted(done); err != nil {
		return UnparkResult{}, err
	}
	result.Owed = len(ids) - len(done)
	return result, nil
}

// reviveWarning is what relaunch will refuse or degrade for a row, read
// before the pane goes so a park can say which sessions will not come back
// as they were.
func reviveWarning(runtime *runtime, sess store.Session) string {
	tool, known := runtime.cfg.Tools[sess.Tool]
	if !known {
		return "tool " + sess.Tool + " is no longer configured, so unpark cannot relaunch it"
	}
	if _, err := resolveTerminalDirectory(sess.Cwd); err != nil {
		return "working directory no longer exists: " + sess.Cwd
	}
	if sess.AgentSessionID == "" && tool.ResumeByIDCommand != "" {
		return "no conversation id captured, so unpark will use the tool's continue command"
	}
	return ""
}

// continuePrompt is what an agent stopped mid-turn is told on relaunch.
// The conversation has the interrupted turn's context; the one word is
// what asks the agent to pick it back up.
const continuePrompt = "continue"

// endSession is the stop Kill and Park share: freeze the screen, end the
// pane, and leave the row dead with the conversation id revive needs.
func (s *Sessions) endSession(runtime *runtime, target store.Session) error {
	pane, _ := runtime.driver.CapturePane(target.ID)
	return s.endSessionWith(runtime, target, pane)
}

// endSessionWith is endSession for a caller that already read the screen.
func (s *Sessions) endSessionWith(runtime *runtime, target store.Session, pane string) error {
	if runtime.driver.Exists(target.ID) {
		if pane != "" {
			if err := runtime.store.SetSnapshot(target.ID, pane); err != nil {
				return err
			}
		}
		if err := runtime.driver.Kill(target.ID); err != nil {
			return err
		}
	}
	// The agent dies without running its session-end hook, so a leftover
	// status file would otherwise decide what a revived session reads as.
	if err := hooks.NewManager(s.configDir).Remove(target.ID); err != nil {
		return err
	}
	return runtime.store.UpdateStatus(target.ID, status.Dead)
}

// relaunch is the relaunch Revive and Unpark share: the dead row's tool,
// directory and conversation id decide the command, and the row comes back
// in the tool's default status with its finished alert re-armed. A prompt
// rides the command line the way a spawn's does, or is queued for the
// poller to type when the tool takes its prompt as input.
func (s *Sessions) relaunch(runtime *runtime, target store.Session, prompt string) (config.Tool, error) {
	tool, known := runtime.cfg.Tools[target.Tool]
	if !known {
		return config.Tool{}, fmt.Errorf("tool %s is no longer configured", target.Tool)
	}
	if _, err := resolveTerminalDirectory(target.Cwd); err != nil {
		return config.Tool{}, fmt.Errorf("working directory no longer exists: %s", target.Cwd)
	}
	revive, err := launch.ReviveCommand(target.Tool, tool, target.AgentSessionID, target.Model)
	if err != nil {
		return config.Tool{}, err
	}
	base := launch.WithPrompt(tool, revive, prompt)
	sessionHooks, err := sessionhooks.Current()
	if err != nil {
		return config.Tool{}, err
	}
	contributed, err := sessionhooks.Env(sessionHooks, target, extension.LaunchRelaunch, "", nil)
	if err != nil {
		return config.Tool{}, err
	}
	command, env, err := launch.Environment(hooks.NewManager(s.configDir), target.Tool, tool, base, target.ID, target.Model, target.Account, contributed)
	if err != nil {
		return config.Tool{}, err
	}
	if err := runtime.driver.Create(target.ID, target.Cwd, command, env, 0, 0); err != nil {
		return config.Tool{}, err
	}
	_ = runtime.driver.SetLabel(target.ID, sessionLabel(target.Group, target.Name))
	// Stamped the way the board's revive stamps it: the child sweep reads
	// the launch to tell this life's reports from the last one's.
	if err := runtime.store.SetAgentLaunchedAt(target.ID, time.Now()); err != nil {
		return config.Tool{}, err
	}
	if err := runtime.store.UpdateStatus(target.ID, tool.DefaultStatus); err != nil {
		return config.Tool{}, err
	}
	if err := runtime.store.SetAcked(target.ID, false); err != nil {
		return config.Tool{}, err
	}
	if prompt != "" && tool.PromptMode == "send" {
		if err := runtime.store.QueuePendingInput(target.ID, prompt); err != nil {
			return config.Tool{}, err
		}
	}
	accounts.RecordLaunch(runtime.store, target.ID, target.Tool, target.Account)
	return tool, nil
}
