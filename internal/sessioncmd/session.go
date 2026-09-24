// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package sessioncmd

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/google/uuid"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/git"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/migrate"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
	"github.com/usestring/gate-inbox/internal/tracing"
)

type Session struct {
	ID        string `json:"id" jsonschema:"agent session id"`
	Name      string `json:"name" jsonschema:"session name shown in Gate Inbox"`
	Tool      string `json:"tool" jsonschema:"agent CLI the session runs"`
	Model     string `json:"model,omitempty" jsonschema:"model that CLI runs on; empty is the CLI's own default"`
	Account   string `json:"account,omitempty" jsonschema:"named subscription the session runs on; empty is the CLI's own login"`
	Group     string `json:"group" jsonschema:"group path holding the session; empty is the root"`
	Directory string `json:"directory" jsonschema:"session's current working directory, or its launch directory when stopped"`
	Status    string `json:"status" jsonschema:"Gate Inbox status: starting, working, waiting, finished, idle, errored or dead"`
	Running   bool   `json:"running" jsonschema:"whether the session currently has a live tmux pane"`
	Archived  bool   `json:"archived" jsonschema:"whether the session is archived out of the active list"`
	Self      bool   `json:"self" jsonschema:"whether this row is the calling session itself"`
	// ParentID is where the board draws this row, and SpawnedBy is who owns
	// it. Nothing the tools hand back used to carry either, so a fan-out that
	// landed flat -- every spawn a sibling of the session that asked for it --
	// was invisible to that session: it could read its children's screens one
	// by one and never learn that none of them were its children.
	//
	// The two differ for every spawn made by a session that is itself a
	// child, because the tree carries one level: such a row is drawn beside
	// its spawner, under their shared root, and answered by its spawner.
	ParentID  string `json:"parent_id,omitempty" jsonschema:"session this row is drawn under on the board; empty for a top-level session"`
	SpawnedBy string `json:"spawned_by,omitempty" jsonschema:"session that spawned this one, which is the only session that can answer its questions or reach it with send_children; empty for a session nobody spawned"`
}

type SessionScreen struct {
	Session Session `json:"session"`
	Output  string  `json:"output" jsonschema:"the session's output: its whole visible pane, or, when since was passed, only the conversation added after that cursor"`
	// Mode says which of those two Output is, so a caller never has to guess
	// whether an empty Output means a blank screen or a child that has not
	// spoken since it last looked.
	Mode string `json:"mode" jsonschema:"pane when output is the whole visible screen, delta when it is only what was added after the since cursor"`
	// Cursor is this read's position in the session's conversation. Passing
	// it back as since on the next read returns only what came after it.
	// Empty for a session whose conversation cannot be read.
	Cursor string `json:"cursor,omitempty" jsonschema:"pass back as since on the next read to get only what the session added after this point"`
	// Digest is the session's state as fields rather than as prose, so a
	// parent can branch on whether a child finished without reading for it.
	Digest ReadDigest `json:"digest" jsonschema:"the session's status, the question it is holding, and its last result"`
	// Degraded names why a requested delta came back as a pane. Empty when
	// nothing was asked for that could not be given.
	Degraded string `json:"degraded,omitempty" jsonschema:"why a requested delta fell back to the whole pane"`
}

type Group struct {
	Path      string `json:"path" jsonschema:"full group path, slash separated; pass this value as a group argument"`
	Directory string `json:"directory,omitempty" jsonschema:"group's default working directory, inherited by sessions created in it"`
	Archived  bool   `json:"archived" jsonschema:"whether the group is archived"`
	Sessions  int    `json:"sessions" jsonschema:"number of active agent sessions directly in this group"`
}

type CreateSessionOptions struct {
	Tool string
	Name string
	// Nil inherits the calling session's group; a pointer to an empty string
	// deliberately targets the root group.
	Group     *string
	Directory string
	Prompt    string
	// Model launches on a chosen model rather than the CLI's own default.
	// Empty is that default, and a tool with no model flag refuses one.
	Model string
	// Account pins a subscription; empty follows the board's routing settings.
	// A tool with no account_env refuses an explicit account.
	Account string
	// Nest files the new session under the caller, which is what a fan-out
	// wants and what the list draws as a tree. Nil is true. Setting it false
	// makes a sibling, which is the only way to put a spawned session in
	// another group -- the store forces a child into its parent's.
	Nest *bool
}

type Sessions struct {
	commands
	newGit func() (*git.Driver, error)
	// claudeHome is where park reads Claude Code's per-process session
	// files from; tests point it at a directory of their own.
	claudeHome string
	// roots are the agent CLIs' state directories a migration locates the
	// source transcript under; tests point them at fixtures.
	roots migrate.Roots
}

func NewSessions(configDir string, words Vocabulary) *Sessions {
	return newSessions(configDir, words, tmux.NewWithSocket, git.New)
}

func newSessions(configDir string, words Vocabulary, newDriver func(socket string) (*tmux.Driver, error), newGit func() (*git.Driver, error)) *Sessions {
	return &Sessions{
		commands:   commands{configDir: configDir, words: words, newDriver: newDriver},
		newGit:     newGit,
		claudeHome: convo.ClaudeHome(),
		roots:      migrate.DefaultRoots(),
	}
}

// agent resolves a target id to an agent session, refusing the ids that
// belong to terminals so an agent never types a sentence at a shell.
func (r *runtime) agent(id string) (store.Session, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return store.Session{}, fmt.Errorf("session_id is empty; call %s to get one", r.words.ListSessions)
	}
	sess, err := r.store.Get(id)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Session{}, fmt.Errorf("session %s does not exist; call %s for current ids", id, r.words.ListSessions)
	}
	if err != nil {
		return store.Session{}, err
	}
	if r.cfg.Tools[sess.Tool].Shell {
		return store.Session{}, fmt.Errorf("session %s is a terminal, not an agent; use the terminal tools for it", id)
	}
	return sess, nil
}

// deliverable refuses a target the manager would never type into.
//
// Archived is tested before running because it is the more useful answer of
// the two: archiving now ends the pane, so an archived row is also a dead
// one, and reporting only that it is not running would send the caller to
// revive it -- which puts a process back but leaves the row off the poller,
// so the next message would queue against it just the same.
func (r *runtime) deliverable(target store.Session) error {
	if target.Archived {
		return fmt.Errorf("session %s is archived, so Gate Inbox no longer polls it; restore it with %s first", target.ID, r.words.Restore)
	}
	if !r.driver.Exists(target.ID) {
		return fmt.Errorf("session %s is not running; revive it with %s first", target.ID, r.words.Revive)
	}
	if r.cfg.Tools[target.Tool].ActivityCutoff == "" {
		return fmt.Errorf("tool %q declares no activity_cutoff, so Gate Inbox cannot tell when %s is ready to read a message; add one to config.toml", target.Tool, target.ID)
	}
	return nil
}

func (r *runtime) sessionInfo(sess store.Session, running, self bool) Session {
	dir := sess.Cwd
	if running {
		if current, err := r.driver.PaneCurrentPath(sess.ID); err == nil {
			dir = current
		}
	}
	return Session{
		ID:        sess.ID,
		Name:      sess.Name,
		Tool:      sess.Tool,
		Model:     sess.Model,
		Account:   sess.Account,
		Group:     sess.Group,
		Directory: dir,
		Status:    sess.Status,
		Running:   running,
		Archived:  sess.Archived,
		Self:      self,
		ParentID:  sess.ParentID,
		SpawnedBy: store.SpawnerOf(sess),
	}
}

// A board that has run for months is mostly history: of the 631 rows on
// the board this was measured against, 539 were archived, and listing them
// all came to 229KB across the two renderings the MCP front sends. A
// parent that only wants to know which of its children finished paid for
// every one of those rows, because the schema offered nothing else to ask
// for. These bound what it can ask for instead.
const (
	DefaultSessionLimit = 50
	// MaxSessionLimit is well past a board anybody reads row by row; a
	// caller that wants the whole table still has to say so.
	MaxSessionLimit = 500
	// SelfParent is what a caller passes for its own id. It knows that id,
	// but spelling it out on every call is the kind of friction that stops
	// a caller filtering at all.
	SelfParent = "me"
)

// ListOptions narrows the session list. The zero value is the cheap read:
// unarchived, every state, the first DefaultSessionLimit rows.
type ListOptions struct {
	// Parent keeps only the sessions that one spawned, reported as
	// parent_id is stored. SelfParent resolves to the caller, which is how
	// a manager reads its own fan-out and nothing else.
	Parent string
	// Status keeps only rows in these states; empty keeps every state.
	Status []string
	// IncludeArchived reads the filed rows too. The store decodes a launch
	// prompt per archived row and takes several times as long for it, and
	// on a long-lived board they are most of the table -- so only a caller
	// looking for a row to restore should ask.
	IncludeArchived bool
	// Limit caps the rows returned, after filtering. Zero takes
	// DefaultSessionLimit and anything over MaxSessionLimit is refused.
	Limit int
	// After is a Cursor from an earlier list: only the rows after it are
	// returned, which is how a caller reads a board wider than
	// MaxSessionLimit. A cursor holds a place in the order rather than a
	// count, so a row archived or started between two pages shifts
	// neither. With Parent set, rows come in creation order, whose key never
	// moves, so every row is read once. Without it they come in board order,
	// which a reorder or a group move changes: a row moved across the
	// cursor mid-scan is skipped or read twice.
	After string
	after *store.ListKey
}

// SessionList carries the rows plus what the limit hid, because a manager
// that silently saw 50 of its 80 children would act on a wrong board.
type SessionList struct {
	Sessions []Session `json:"sessions"`
	Matched  int       `json:"matched" jsonschema:"how many sessions matched the filters, before limit"`
	Returned int       `json:"returned" jsonschema:"how many rows are in sessions"`
	// Truncated is stated rather than left to matched > returned, so a
	// caller reading the structured payload does not have to derive it.
	Truncated bool `json:"truncated" jsonschema:"true when limit left matching sessions out; narrow parent or status, or raise limit"`
	// Cursor is passed back as After to read the rows after this page;
	// empty when none follow.
	Cursor string `json:"-"`
}

func (o ListOptions) normalize(callerID string) (ListOptions, error) {
	parent := strings.TrimSpace(o.Parent)
	if strings.EqualFold(parent, SelfParent) {
		parent = callerID
	}
	o.Parent = parent
	states := make([]string, 0, len(o.Status))
	seen := map[string]bool{}
	for _, raw := range o.Status {
		state, err := normalizeState(raw)
		if err != nil {
			return ListOptions{}, err
		}
		if !seen[state] {
			seen[state] = true
			states = append(states, state)
		}
	}
	o.Status = states
	switch {
	case o.Limit == 0:
		o.Limit = DefaultSessionLimit
	case o.Limit < 0 || o.Limit > MaxSessionLimit:
		return ListOptions{}, fmt.Errorf("limit %d is out of range; ask for between 1 and %d", o.Limit, MaxSessionLimit)
	}
	if o.After != "" {
		after, err := store.ParseListKey(o.After)
		if err != nil {
			return ListOptions{}, err
		}
		if after.Creation != (o.Parent != "") {
			return ListOptions{}, errors.New("cursor is from a list with a different parent filter")
		}
		o.after = &after
	}
	return o, nil
}

// keeps reads the stored row rather than the built one: sessionInfo forks
// a tmux process per running session to ask its pane for a current path,
// and a row the filter drops should never cost that.
func (o ListOptions) keeps(sess store.Session) bool {
	if o.Parent != "" && sess.ParentID != o.Parent {
		return false
	}
	if len(o.Status) == 0 {
		return true
	}
	return slices.Contains(o.Status, sess.Status)
}

// List reports the sessions matching opts.
func (s *Sessions) List(sessionID string, opts ListOptions) (list SessionList, err error) {
	// The first call nearly every agent makes, and it is not free: the
	// session table, then a tmux server asked which of those panes still
	// exist.
	op := start("sessioncmd.list", sessionAttr(sessionID))
	defer func() {
		op.done(&err,
			tracing.Attr{Key: "sessions", Value: list.Returned},
			tracing.Attr{Key: "matched", Value: list.Matched},
			tracing.Attr{Key: "archived", Value: opts.IncludeArchived})
	}()
	runtime, err := s.open()
	if err != nil {
		return SessionList{}, err
	}
	defer runtime.store.Close()
	caller, err := runtime.caller(sessionID)
	if err != nil {
		return SessionList{}, err
	}
	return runtime.list(caller.ID, opts)
}

// list is List once the caller is settled. callerID is empty for the board,
// which is no session: nothing is marked as the caller's own row.
func (r *runtime) list(callerID string, opts ListOptions) (SessionList, error) {
	opts, err := opts.normalize(callerID)
	if err != nil {
		return SessionList{}, err
	}
	stored, err := r.store.ListSessions(opts.IncludeArchived)
	if err != nil {
		return SessionList{}, err
	}
	panes, err := r.driver.Panes()
	if err != nil {
		return SessionList{}, err
	}
	sessions := make([]Session, 0)
	matched := 0
	more := false
	var last store.ListKey
	var keys []store.ListKey
	if opts.Parent != "" {
		stored, keys = store.ByCreation(stored)
	} else {
		keys = store.ListKeys(stored)
	}
	for i, sess := range stored {
		if r.cfg.Tools[sess.Tool].Shell {
			continue
		}
		if !opts.keeps(sess) {
			continue
		}
		matched++
		if opts.after != nil && !keys[i].After(*opts.after) {
			continue
		}
		if len(sessions) == opts.Limit {
			more = true
			continue
		}
		_, running := panes[sess.ID]
		sessions = append(sessions, r.sessionInfo(sess, running, callerID != "" && sess.ID == callerID))
		last = keys[i]
	}
	list := SessionList{
		Sessions:  sessions,
		Matched:   matched,
		Returned:  len(sessions),
		Truncated: more,
	}
	if more {
		list.Cursor = last.String()
	}
	return list, nil
}

func (s *Sessions) Groups(sessionID string) ([]Group, error) {
	runtime, err := s.open()
	if err != nil {
		return nil, err
	}
	defer runtime.store.Close()
	if _, err := runtime.caller(sessionID); err != nil {
		return nil, err
	}
	stored, err := runtime.store.Groups()
	if err != nil {
		return nil, err
	}
	sessions, err := runtime.store.ListSessions(true)
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int, len(stored))
	for _, sess := range sessions {
		if runtime.cfg.Tools[sess.Tool].Shell || sess.Archived {
			continue
		}
		counts[sess.Group]++
	}
	groups := make([]Group, 0, len(stored))
	for _, group := range stored {
		groups = append(groups, Group{
			Path:      group.Name,
			Directory: group.Path,
			Archived:  group.Archived,
			Sessions:  counts[group.Name],
		})
	}
	return groups, nil
}

// CreateGroup adds a group under an existing parent, so sessions spawned
// later can be filed into it. Directory becomes the group's inherited
// default working directory.
func (s *Sessions) CreateGroup(sessionID, path, directory string) (Group, error) {
	path = strings.Trim(strings.TrimSpace(path), "/")
	if path == "" {
		return Group{}, errors.New("group path is empty")
	}
	runtime, err := s.open()
	if err != nil {
		return Group{}, err
	}
	defer runtime.store.Close()
	if _, err := runtime.caller(sessionID); err != nil {
		return Group{}, err
	}
	existing, err := runtime.store.Groups()
	if err != nil {
		return Group{}, err
	}
	for _, group := range existing {
		if group.Name == path {
			return Group{}, fmt.Errorf("group %q already exists", path)
		}
	}
	if parent := parentGroup(path); parent != "" {
		known := false
		for _, group := range existing {
			if group.Name == parent {
				known = true
				break
			}
		}
		if !known {
			return Group{}, fmt.Errorf("parent group %q does not exist; create it first", parent)
		}
	}
	resolved := ""
	if strings.TrimSpace(directory) != "" {
		resolved, err = resolveTerminalDirectory(directory)
		if err != nil {
			return Group{}, err
		}
	}
	if err := runtime.store.CreateGroup(path, resolved); err != nil {
		return Group{}, err
	}
	return Group{Path: path, Directory: resolved}, nil
}

// GroupRemoval is what deleting a group did, since a fleet that filed work
// under one wants to know where its sessions went.
type GroupRemoval struct {
	Removed []string `json:"removed" jsonschema:"group paths that no longer exist, the named group and anything nested under it"`
	Moved   []string `json:"moved,omitempty" jsonschema:"ids of sessions that were filed under those groups and now sit in the root group"`
}

// DeleteGroup removes a group and the groups nested under it. Sessions
// filed there move to the root rather than going with it: an agent
// tidying up the group it opened for a finished fleet is not asking to
// destroy whatever is still running in it.
func (s *Sessions) DeleteGroup(sessionID, path string) (GroupRemoval, error) {
	path = strings.Trim(strings.TrimSpace(path), "/")
	if path == "" {
		return GroupRemoval{}, errors.New("group path is empty; the root group cannot be deleted")
	}
	runtime, err := s.open()
	if err != nil {
		return GroupRemoval{}, err
	}
	defer runtime.store.Close()
	if _, err := runtime.caller(sessionID); err != nil {
		return GroupRemoval{}, err
	}
	removed, moved, err := runtime.store.RemoveGroup(path)
	if errors.Is(err, store.ErrGroupNotFound) {
		return GroupRemoval{}, fmt.Errorf("group %q does not exist; call %s for current paths", path, runtime.words.ListGroups)
	}
	if err != nil {
		return GroupRemoval{}, err
	}
	return GroupRemoval{Removed: removed, Moved: moved}, nil
}

func (s *Sessions) Create(sessionID string, opts CreateSessionOptions) (created Session, err error) {
	// The most expensive thing this package does, and the one a fan-out
	// repeats: a pane, a CLI process behind it, and the row filed for both.
	// The tool and the caller are on the span because a spawn that is slow is
	// usually slow for one CLI rather than for all of them; the prompt handed
	// to the new agent is not, and must not be.
	op := start("sessioncmd.create", sessionAttr(sessionID))
	defer func() {
		op.done(&err,
			tracing.Attr{Key: "tool", Value: opts.Tool},
			tracing.Attr{Key: "created", Value: created.ID})
	}()
	runtime, err := s.open()
	if err != nil {
		return Session{}, err
	}
	defer runtime.store.Close()
	caller, err := runtime.caller(sessionID)
	if err != nil {
		return Session{}, err
	}
	toolName := strings.TrimSpace(opts.Tool)
	if toolName == "" {
		toolName = caller.Tool
	}
	tool, known := runtime.cfg.Tools[toolName]
	if !known {
		return Session{}, fmt.Errorf("tool %q is not configured; configured tools are %s", toolName, strings.Join(agentToolNames(runtime), ", "))
	}
	if tool.Shell {
		return Session{}, fmt.Errorf("tool %q opens a shell, not an agent; use %s for that", toolName, runtime.words.CreateTerminal)
	}
	nest := opts.Nest == nil || *opts.Nest
	// createTarget first, so a group that does not exist is reported as
	// missing rather than as a nesting conflict.
	group, dir, err := runtime.createTarget(caller, opts.Group, opts.Directory)
	if err != nil {
		return Session{}, err
	}
	// A child is forced into its parent's group by the store, so a group
	// asked for here would be silently dropped -- and the cwd resolved from
	// it dropped with it. Refuse instead, in create_terminal's words.
	if nest && group != caller.Group {
		return Session{}, fmt.Errorf("set nest false to place in another group")
	}
	// An agent spawning an agent is a fan-out, and the row belongs to the
	// session that asked for it, the way create_terminal records a shell.
	//
	// The store carries one level of parenthood, which is why a grandchild is
	// filed against the caller's own parent rather than under the caller, the
	// same flattening create_terminal does for a shell.
	//
	// That flattening is placement and nothing else. Who owns the row --
	// who answers its dialogs, who send_children reaches, who hears that it
	// stopped -- is spawned_by, set below to the caller at whatever depth,
	// because the caller is the session that wanted the work done. See
	// store/spawner.go for what reading both off parent_id cost.
	parentID := ""
	create := runtime.store.LaunchSession
	if nest {
		if caller.ParentID == "" {
			parentID = caller.ID
		} else {
			parentID = caller.ParentID
			create = func(row store.Session, launch func() error) error {
				return runtime.store.LaunchSessionBeside(row, caller.ID, launch)
			}
		}
	} else if caller.ParentID != "" {
		parentID = caller.ParentID
	}
	prompt := strings.TrimSpace(opts.Prompt)
	if strings.HasPrefix(prompt, "-") && tool.PromptFlag == "" {
		return Session{}, fmt.Errorf(`prompt cannot start with "-" for %s, which takes its prompt as a bare argument and would read it as a flag`, toolName)
	}
	name := strings.TrimSpace(opts.Name)
	autoNamed := name == ""
	id := uuid.NewString()[:8]
	if autoNamed {
		name = toolName + "-" + id[:4]
	}

	account, err := runtime.accountOr(opts.Account, tool, id)
	if err != nil {
		return Session{}, err
	}
	// The pane may have to open somewhere else to get past the CLI's trust
	// dialog; the agent is told to change into dir either way, and the row
	// below records dir, which is what work and git discovery read.
	launchDir := launchDirectory(caller.Cwd, dir)
	workdir := ""
	if launchDir != dir {
		workdir = dir
	}
	plan, err := launch.Assemble(toolName, tool, prompt, workdir, autoNamed, opts.Model, account)
	if err != nil {
		return Session{}, err
	}
	manager := hooks.NewManager(s.configDir)
	command, env, err := launch.Environment(manager, toolName, tool, plan.Command, id, plan.Model, plan.Account)
	if err != nil {
		return Session{}, err
	}
	sess := store.Session{
		ID:             id,
		Name:           name,
		Tool:           toolName,
		Cwd:            dir,
		Group:          group,
		Status:         status.Starting,
		AgentSessionID: plan.AgentSessionID,
		PendingInputs:  plan.PendingInputs,
		LaunchPrompt:   plan.LaunchPrompt,
		ParentID:       parentID,
		SpawnedBy:      caller.ID,
		Model:          plan.Model,
		Account:        plan.Account,
	}
	launched := false
	if err := create(sess, func() error {
		err := runtime.driver.Create(sess.ID, launchDir, command, env, 0, 0)
		launched = err == nil
		return err
	}); err != nil {
		if launched {
			_ = runtime.driver.Kill(sess.ID)
		}
		return Session{}, err
	}
	accounts.RecordLaunch(runtime.store, sess.ID, sess.Tool, sess.Account)
	logging.Info("session created by an agent",
		"caller", caller.ID, "callerTool", caller.Tool,
		"session", sess.ID, "parent", sess.ParentID,
		"tool", sess.Tool, "group", sess.Group, "nest", nest)
	_ = runtime.driver.SetLabel(sess.ID, sessionLabel(sess.Group, sess.Name))
	info := runtime.sessionInfo(sess, true, false)
	// sessionInfo prefers the live pane path, which is where a session has
	// got to. A diverted pane has not got anywhere yet -- the agent changes
	// directory on its first turn -- so the caller is told the directory it
	// asked for rather than the one the pane opened in.
	info.Directory = sess.Cwd
	return info, nil
}

func agentToolNames(r *runtime) []string {
	names := make([]string, 0, len(r.cfg.Tools))
	for _, name := range r.cfg.ToolNames() {
		if !r.cfg.Tools[name].Shell {
			names = append(names, name)
		}
	}
	return names
}

type SendResult struct {
	MessageID     int64 `json:"message_id" jsonschema:"pass to message_status to see whether it arrived"`
	QueuePosition int   `json:"queue_position" jsonschema:"this message's place in the recipient's queue; 1 means it is next"`
	ManagerAwake  bool  `json:"manager_awake" jsonschema:"whether Gate Inbox is running to deliver it; a message queued while it is closed waits until it opens again"`
	// Held is why nothing will type this message in as things stand.
	// message_status computes the same sentence, but only for a sender that
	// thought to ask, and a send to a child on a dialog gives it no reason
	// to: it comes back with an id and a queue position like any other.
	Held string `json:"held,omitempty" jsonschema:"why nothing will type this message into the recipient as things stand, and what to do about it; empty means it is simply waiting for the agent to be at rest"`
	// Superseded counts the sender's own earlier messages on this subject
	// that this one replaced in the queue.
	Superseded int  `json:"superseded,omitempty" jsonschema:"how many earlier queued messages from this sender on the same subject this one replaced"`
	Interrupt  bool `json:"interrupt,omitempty" jsonschema:"whether the recipient's running turn is stopped before the message is typed in"`
}

// maxMessageBytes bounds one message. An instruction to another agent is
// prose; the queue and rate caps count messages, and this is what keeps one
// of them from being a file paste that fills the recipient's prompt.
const maxMessageBytes = 8000

// maxSubjectBytes bounds the supersession key. It names what a message is
// about so a later one can replace it; a sender that puts the message in it
// supersedes nothing, since no second send would ever match.
const maxSubjectBytes = 200

// Send queues a message for another agent. It is deliberately not typed
// into the pane here: several tools keep their input line drawn under an
// approval dialog, so a message sent the moment it is written would answer
// that dialog. The manager's poller types it in once the target can read it:
// at rest, or mid-turn for a tool that queues typed input (config.TypeAhead).
// With interrupt, the poller first stops the recipient's running turn with
// its tool's interrupt_keys, so the message is the next turn rather than
// something read after the step in hand.
func (s *Sessions) Send(sessionID, targetID, message, subject string, interrupt bool) (SendResult, error) {
	return s.send(sessionID, targetID, message, subject, false, interrupt)
}

// SendAsHuman queues a message as the operator's own words, delivered without
// the cross-session envelope, which is what the TUI's send has always done and
// the CLI's could not say.
//
// The caller session is optional here: a person sends from their own terminal,
// which carries no session id, and requiring one is what made a human's message
// arrive under whatever worker's id their shell happened to hold.
func (s *Sessions) SendAsHuman(sessionID, targetID, message, subject string, interrupt bool) (SendResult, error) {
	return s.send(sessionID, targetID, message, subject, true, interrupt)
}

func (s *Sessions) send(sessionID, targetID, message, subject string, asHuman, interrupt bool) (result SendResult, err error) {
	// The message is queued rather than typed, so what this costs is the
	// store and the checks around it. Its length is on the span because a
	// large one is a real cost here; its text is not, because it is one
	// agent's words to another.
	op := start("sessioncmd.send", sessionAttr(targetID))
	defer func() {
		op.done(&err,
			tracing.Attr{Key: "message.bytes", Value: len(message)},
			tracing.Attr{Key: "as_human", Value: asHuman})
	}()
	message = strings.TrimSpace(message)
	if message == "" {
		return SendResult{}, errors.New("message is empty")
	}
	if len(message) > maxMessageBytes {
		return SendResult{}, fmt.Errorf("message is %d bytes, over the %d byte limit; shorten it to the instruction and point the agent at a file or a task for the detail", len(message), maxMessageBytes)
	}
	subject = strings.TrimSpace(subject)
	if len(subject) > maxSubjectBytes {
		return SendResult{}, fmt.Errorf("subject is %d bytes, over the %d byte limit; it is a label for what the message is about, not the message", len(subject), maxSubjectBytes)
	}
	runtime, err := s.open()
	if err != nil {
		return SendResult{}, err
	}
	defer runtime.store.Close()
	var caller store.Session
	if !asHuman || sessionID != "" {
		if caller, err = runtime.caller(sessionID); err != nil {
			return SendResult{}, err
		}
	}
	target, err := runtime.agent(targetID)
	if err != nil {
		return SendResult{}, err
	}
	if caller.ID != "" && target.ID == caller.ID {
		return SendResult{}, errors.New("a session cannot message itself")
	}
	// Refused up front rather than queued: without keys the poller has no way
	// to stop the turn, and a message that silently arrived late would be the
	// very thing the sender asked to avoid.
	if interrupt && len(runtime.cfg.Tools[target.Tool].InterruptKeys) == 0 {
		return SendResult{}, fmt.Errorf("session %s runs %s, which has no interrupt_keys configured, so there is no safe way to stop its turn; send without interrupt and it is typed in as soon as the session can read it", target.ID, target.Tool)
	}
	now := time.Now()
	if err := runtime.deliverable(target); err != nil {
		return SendResult{}, err
	}
	senderID, senderName := caller.ID, caller.Name
	if asHuman {
		senderID, senderName = store.HumanSenderID, ""
	}
	id, superseded, err := runtime.store.Enqueue(store.InboxMessage{
		SessionID:   target.ID,
		SenderID:    senderID,
		SenderName:  senderName,
		Body:        message,
		Fingerprint: fingerprint(message),
		Subject:     subject,
		Interrupt:   interrupt,
		SentAt:      now,
	}, store.DefaultInboxLimits)
	if err != nil {
		return SendResult{}, err
	}
	// Answering is the acknowledgement: whatever this session was sent by
	// the agent it is now writing to has plainly been read. A person sending
	// from their own terminal has no inbox of their own to clear.
	if caller.ID != "" {
		if err := runtime.store.MarkRead(caller.ID, target.ID, now); err != nil {
			return SendResult{}, err
		}
	}
	queued, err := runtime.store.QueuedCount(target.ID)
	if err != nil {
		return SendResult{}, err
	}
	awake, err := runtime.managerAwake(now)
	if err != nil {
		return SendResult{}, err
	}
	// The same gate the poller runs, run once here so the sender learns from
	// the send itself. A hold is not a failure -- the message stays queued
	// and lands when the recipient is at rest -- so it rides the result
	// rather than an error, and a capture that will not read leaves the
	// send reported rather than failed.
	held, heldErr := runtime.heldReason(target.ID)
	if heldErr != nil {
		logging.Info("could not tell a sender why its message is held",
			"session", target.ID, "message", id, logging.Err(heldErr))
	}
	return SendResult{
		MessageID:     id,
		QueuePosition: queued,
		ManagerAwake:  awake,
		Held:          held,
		Superseded:    superseded,
		Interrupt:     interrupt,
	}, nil
}

// MessageStatus reports what happened to a message this session sent, or one
// sent to a session it spawned.
func (s *Sessions) MessageStatus(sessionID string, messageID int64) (MessageState, error) {
	runtime, err := s.open()
	if err != nil {
		return MessageState{}, err
	}
	defer runtime.store.Close()
	caller, err := runtime.caller(sessionID)
	if err != nil {
		return MessageState{}, err
	}
	msg, err := runtime.store.Message(messageID, caller.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return MessageState{}, fmt.Errorf("message %d was not sent by this session or to a session it spawned, or has aged out of the log", messageID)
	}
	if err != nil {
		return MessageState{}, err
	}
	state := MessageState{
		MessageID: msg.ID,
		SessionID: msg.SessionID,
		Body:      msg.Body,
		Interrupt: msg.Interrupt,
	}
	switch {
	// Supersession stamps the delivery column too, so it is read before it,
	// and before the drop it is not: this message left the queue because the
	// sender replaced it, which is the one exit that is not a loss.
	case msg.SupersededBy != 0:
		state.State = "superseded"
		replacer := "you"
		if msg.SenderID != caller.ID {
			replacer = "its sender"
		}
		state.Reason = fmt.Sprintf("%s replaced it with message %d on the same subject before it was typed in; that message is the one the agent reads", replacer, msg.SupersededBy)
	// A drop stamps the delivery column as well, so that a message nothing
	// will ever type leaves the queue, and is read first for that reason.
	case !msg.DroppedAt.IsZero():
		state.State = "dropped"
		state.Reason = "Gate Inbox could not type it into the pane, and never retries a message; send it again"
	case !msg.ReadAt.IsZero():
		state.State = "answered"
		state.DeliveredAt = msg.DeliveredAt.Format(time.RFC3339)
	case !msg.DeliveredAt.IsZero():
		state.State = "delivered"
		state.DeliveredAt = msg.DeliveredAt.Format(time.RFC3339)
	default:
		state.State = "queued"
		held, err := runtime.heldReason(msg.SessionID)
		if err != nil {
			return MessageState{}, err
		}
		if held != "" {
			state.State = "held"
			state.Reason = held
		}
	}
	return state, nil
}

// heldReason names why a queued message is not moving when the recipient is
// what stops it: a session the manager no longer visits, or a screen the
// delivery gate refuses. The gate is the poller's own, run here against the
// recipient's current pane, so a sender is never promised a hold the manager
// does not keep. A recipient merely mid-turn is not held: that clears itself.
func (r *runtime) heldReason(sessionID string) (string, error) {
	target, err := r.store.Get(sessionID)
	if err != nil {
		return "", err
	}
	if target.Archived {
		return fmt.Sprintf("session %s is archived, so Gate Inbox no longer polls it and nothing will type this in; restore it with %s", sessionID, r.words.Restore), nil
	}
	if !r.driver.Exists(target.ID) {
		return fmt.Sprintf("session %s is not running, so nothing will type this in; revive it with %s", sessionID, r.words.Revive), nil
	}
	// A tool block deleted after the message was queued leaves the reader
	// with nothing to judge readiness by, so the poller never types it in.
	if r.cfg.Tools[target.Tool].ActivityCutoff == "" {
		return fmt.Sprintf("tool %q declares no activity_cutoff, so Gate Inbox cannot tell when %s is ready and nothing will type this in; add one to config.toml", target.Tool, sessionID), nil
	}
	// The poller types into a resting session, and an errored one is not
	// resting: it is showing whatever stopped it, often a limit its agent
	// cannot clear on its own.
	if target.Status == status.Errored {
		return fmt.Sprintf("session %s is errored, so nothing will type this in until it recovers; read its screen with %s", sessionID, r.words.Read), nil
	}
	pane, err := r.driver.CapturePane(target.ID)
	if err != nil {
		return "", err
	}
	engine, err := status.NewEngine(r.cfg)
	if err != nil {
		return "", err
	}
	clean := ansi.Strip(pane)
	if engine.TypingHold(target.Tool, clean) == status.Waiting {
		return fmt.Sprintf("session %s is sitting on a dialog, and nothing is typed into a session while one is on its screen; read that screen and answer it", sessionID), nil
	}
	// The same two reads the poller makes before it pastes. A person's
	// draft is a hold that clears when they submit or clear it, which can
	// be a while; a keystroke a moment ago clears on its own.
	if at, err := r.driver.SessionInputAt(target.ID); err == nil && !at.IsZero() && time.Since(at) < status.OperatorQuiet {
		return fmt.Sprintf("someone is typing in session %s right now; it is typed in once they have paused", sessionID), nil
	}
	if caretX, caretY, err := r.driver.Cursor(target.ID); err == nil && engine.DraftInComposer(target.Tool, clean, caretX, caretY) {
		return fmt.Sprintf("someone has text written at session %s's prompt, and delivery would submit it with this message; it is typed in once that prompt is empty", sessionID), nil
	}
	return "", nil
}

type MessageState struct {
	MessageID   int64  `json:"message_id"`
	SessionID   string `json:"session_id" jsonschema:"session the message was addressed to"`
	Body        string `json:"body"`
	State       string `json:"state" jsonschema:"queued (waiting for the agent to be at rest), superseded (a later message from you on the same subject replaced it), held (nothing will type it in as things stand: the recipient is sitting on a dialog, has a person typing or a draft at its prompt, is archived or not running, and reason says which), delivered (typed into its prompt), dropped (it never reached the prompt and is not retried), or answered (it has since messaged back)"`
	DeliveredAt string `json:"delivered_at,omitempty" jsonschema:"RFC3339 time the message reached the prompt"`
	Reason      string `json:"reason,omitempty" jsonschema:"why the message is in that state, and what to do about it"`
	Interrupt   bool   `json:"interrupt,omitempty" jsonschema:"sent with interrupt: the recipient's running turn is stopped before it is typed in, unless a dialog is showing"`
}

// managerAwake reports whether a manager has polled recently enough to
// still be delivering. Queued messages only move while it runs.
func (r *runtime) managerAwake(now time.Time) (bool, error) {
	raw, err := r.store.Setting(store.PollerHeartbeatKey)
	if err != nil || raw == "" {
		return false, err
	}
	stamp, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return false, fmt.Errorf("poller heartbeat %q is not a timestamp: %w", raw, err)
	}
	return now.Sub(time.Unix(0, stamp)) < max(3*r.cfg.PollInterval.Duration, store.PollerHeartbeatStale), nil
}

// fingerprint is store.Fingerprint under the name this package's callers
// already use; the rule itself belongs beside the dedupe window it feeds.
func fingerprint(message string) string { return store.Fingerprint(message) }

// Read is what another session is doing, in one of two shapes.
//
// With no cursor it is what it always was: the pane, whole, plus the digest
// and a cursor to come back with. With a cursor it is the conversation added
// since that cursor, which for a child that has been running for an hour is
// the hour rather than the last screenful of it, and for a child that has
// said nothing is a few bytes rather than a few thousand.
//
// The pane is still the fallback and still the default, because it is the
// only shape that works for every tool. since is a request, not a demand: a
// session whose transcript cannot be read returns the pane with Degraded
// saying why, rather than an error over a capability the caller did not know
// it was asking for.
func (s *Sessions) Read(sessionID, targetID, since string) (screen SessionScreen, err error) {
	// A capture off a tmux server, or the stored snapshot when the pane is
	// gone. The screen it returns is pane text and stays here.
	defer start("sessioncmd.read", sessionAttr(targetID)).done(&err)
	runtime, err := s.open()
	if err != nil {
		return SessionScreen{}, err
	}
	defer runtime.store.Close()
	if _, err := runtime.caller(sessionID); err != nil {
		return SessionScreen{}, err
	}
	target, err := runtime.agent(targetID)
	if err != nil {
		return SessionScreen{}, err
	}
	running := runtime.driver.Exists(target.ID)
	var pane string
	if running {
		if pane, err = runtime.driver.CapturePane(target.ID); err != nil {
			return SessionScreen{}, err
		}
	} else {
		// A dead row still has a last word worth reading, and its transcript
		// outlives its pane, so the digest and the delta both still work.
		if pane, err = runtime.store.Snapshot(target.ID); err != nil {
			return SessionScreen{}, err
		}
	}
	pane = strings.TrimRight(ansi.Strip(pane), "\r\n")

	cursor, delta, note, readable := s.readDelta(target, since)
	screen = SessionScreen{
		Session: runtime.sessionInfo(target, running, target.ID == sessionID),
		Cursor:  cursor,
		Digest:  runtime.digest(target, pane, running, delta),
	}
	if since != "" && readable {
		screen.Mode, screen.Output, screen.Degraded = "delta", renderDelta(delta), note
		return screen, nil
	}
	if !running && pane == "" {
		return SessionScreen{}, fmt.Errorf("session %s is not running and has no captured screen", target.ID)
	}
	screen.Mode, screen.Output = "pane", pane
	if since != "" {
		screen.Degraded = note
	}
	return screen, nil
}

// Get reports one agent session the way List reports it, without reading
// its pane.
func (s *Sessions) Get(sessionID, targetID string) (got Session, err error) {
	defer start("sessioncmd.get", sessionAttr(targetID)).done(&err)
	runtime, err := s.open()
	if err != nil {
		return Session{}, err
	}
	defer runtime.store.Close()
	if _, err := runtime.caller(sessionID); err != nil {
		return Session{}, err
	}
	target, err := runtime.agent(targetID)
	if err != nil {
		return Session{}, err
	}
	return runtime.sessionInfo(target, runtime.driver.Exists(target.ID), target.ID == sessionID), nil
}

// Kill stops a session's pane and leaves its row dead, keeping the last
// screen so the manager can still show it and a revive can resume the
// conversation it held.
func (s *Sessions) Kill(sessionID, targetID string) (killed Session, err error) {
	defer start("sessioncmd.kill", sessionAttr(targetID)).done(&err)
	runtime, err := s.open()
	if err != nil {
		return Session{}, err
	}
	defer runtime.store.Close()
	if _, err := runtime.caller(sessionID); err != nil {
		return Session{}, err
	}
	target, err := runtime.agent(targetID)
	if err != nil {
		return Session{}, err
	}
	if target.ID == sessionID {
		return Session{}, errors.New("a session cannot kill itself")
	}
	if err := s.endSession(runtime, target); err != nil {
		return Session{}, err
	}
	target.Status = status.Dead
	return runtime.sessionInfo(target, false, false), nil
}

// Revive relaunches a dead session under its old id, keeping its name,
// group and history, and resuming the conversation it held where the tool
// can.
func (s *Sessions) Revive(sessionID, targetID string) (revived Session, err error) {
	// Relaunches a CLI and resumes its conversation, which is a process and
	// whatever that CLI does to read its own history back.
	defer start("sessioncmd.revive", sessionAttr(targetID)).done(&err)
	runtime, err := s.open()
	if err != nil {
		return Session{}, err
	}
	defer runtime.store.Close()
	if _, err := runtime.caller(sessionID); err != nil {
		return Session{}, err
	}
	target, err := runtime.agent(targetID)
	if err != nil {
		return Session{}, err
	}
	if runtime.driver.Exists(target.ID) {
		return Session{}, fmt.Errorf("session %s is still running; revive only applies to dead sessions", target.ID)
	}
	tool, err := s.relaunch(runtime, target, "")
	if err != nil {
		return Session{}, err
	}
	target.Status = tool.DefaultStatus
	return runtime.sessionInfo(target, true, false), nil
}

// Archive files a session out of the active list, or restores it. Archiving
// a running session ends it, the way the board's own archive does: the last
// screen is captured first so an archived row still shows one, and the row
// comes back with revive_session.
//
// It ends the agent because the alternative leaked one. Archiving is the
// documented way an agent files its own finished spawns away, so a
// non-destructive archive left a process per spawn running against a row
// nobody was watching -- a fleet's worth of them, holding their memory for
// days, invisible on the active list. The board path has always killed;
// only this one did not, so the same verb meant two different things
// depending on who called it.
func (s *Sessions) Archive(sessionID, targetID string, archived bool) (filed Session, err error) {
	// Archiving a running session also ends it, so this is two commands wide
	// depending on the flag; the flag is on the span for that reason.
	defer start("sessioncmd.archive", sessionAttr(targetID),
		tracing.Attr{Key: "archived", Value: archived}).done(&err)
	runtime, err := s.open()
	if err != nil {
		return Session{}, err
	}
	defer runtime.store.Close()
	if _, err := runtime.caller(sessionID); err != nil {
		return Session{}, err
	}
	target, err := runtime.agent(targetID)
	if err != nil {
		return Session{}, err
	}
	if target.ID == sessionID && archived {
		return Session{}, errors.New("a session cannot archive itself")
	}
	running := runtime.driver.Exists(target.ID)
	if archived && running {
		// endSessionWith is what kill and park share: it snapshots the screen,
		// ends the pane, and clears the hook status file a revived session
		// would otherwise read its status from.
		pane, _ := runtime.driver.CapturePane(target.ID)
		if err := s.endSessionWith(runtime, target, pane); err != nil {
			return Session{}, err
		}
		target.Status = status.Dead
		running = false
	}
	if err := runtime.store.SetArchived(target.ID, archived); err != nil {
		return Session{}, err
	}
	target.Archived = archived
	return runtime.sessionInfo(target, running, false), nil
}
