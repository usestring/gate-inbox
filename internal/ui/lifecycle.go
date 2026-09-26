// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"errors"
	"fmt"
	"github.com/usestring/gate-inbox/internal/keymap"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/adopt"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/sessionhooks"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// deadSessionHint names both ways back from a dead row: revive resumes the
// conversation it held, restart drops it. It reads the keys off the map
// rather than spelling them, because a hint that names a key the operator
// rebound is worse than no hint: it sends them to a key that does nothing.
func (m *Model) deadSessionHint() string {
	return "session is dead - press " + m.cap(keymap.ContextList, keymap.Revive) +
		" to revive or " + m.cap(keymap.ContextList, keymap.Restart) + " to restart"
}

// archiveRetention is how long a row waits in the archived view before the
// sweep deletes it for good. Long enough that a week of not needing a
// session is the answer to whether it was worth keeping, short enough that
// the archive stays a holding pen rather than a second board.
const archiveRetention = 7 * 24 * time.Hour

// archiveSweepEvery is how often the retention window is checked. The rows
// it finds have been sitting for a week, so the check does not have to be
// prompt -- and this way an open manager pays for it a handful of times a
// day rather than on every poll.
const archiveSweepEvery = 30 * time.Minute

// archiveWindowPhrase is the same clause in every dialog that files a row
// away, because the window is the half of archiving an operator does not
// already know: the rest of the sentence reads like tidying.
const archiveWindowPhrase = "deleted for good after 7 days"

// archivedFocusHint refuses focus on an archived row: archiving kills the
// pane and the manager stops watching it, so focus could only forward keys
// into a snapshot frozen at the moment it was archived.
func (m *Model) archivedFocusHint() string {
	return "archived session - press " + m.cap(keymap.ContextList, keymap.Restore) +
		" to restore it before entering"
}

// shellPromptHint refuses to write into a shell. SendText pastes and then
// presses Enter, so a sentence meant for an agent would run as a command
// on the user's machine. Entering the session is how text reaches a shell,
// where what is typed is plainly a command.
func shellPromptHint(name string) string {
	return name + " is a shell, not an agent - enter it to type there"
}

func (m *Model) attachSelected() (tea.Model, tea.Cmd) {
	sess, ok := m.selected()
	if !ok {
		return m, nil
	}
	if !m.tmux.Exists(sess.ID) {
		m.errBar.text = m.deadSessionHint()
		return m, nil
	}
	m.errBar.text = ""
	if err := m.store.AcknowledgeFinished(sess.ID); err != nil {
		m.errBar.text = err.Error()
		return m, nil
	}
	return m, m.attachCmd(sess.ID)
}

func (m *Model) attachCmd(id string) tea.Cmd {
	// Flip the window back to auto-sizing so it fills the terminal on attach;
	// attachDoneMsg re-pins it to the preview width on detach. Clearing the
	// cached hash first keeps the poller from reading this reflow as
	// streaming output, same as the detach-side resize (reflowSessions).
	// A failure here still attaches: the worst outcome is a stale window
	// size, which beats locking the session out (issue #114).
	var prepErr error
	m.poller.reflowSessions([]string{id}, func() {
		prepErr = m.tmux.PrepareAttach(id)
	})
	if prepErr != nil {
		m.errBar.text = prepErr.Error()
	}
	return tea.ExecProcess(m.tmux.AttachCommand(id), func(err error) tea.Msg {
		return attachDoneMsg{sessID: id, err: err}
	})
}

func (m *Model) reattach(id string) tea.Cmd {
	driver := m.tmux
	// Read before the closure: the hint reads the key map, and the closure
	// runs off the event loop where the model must not be touched.
	dead := m.deadSessionHint()
	stor := m.store
	poller := m.poller
	return func() tea.Msg {
		if !driver.Exists(id) {
			return reattachPreparedMsg{sessID: id, err: errors.New(dead)}
		}
		sess, err := stor.Get(id)
		if err != nil {
			return reattachPreparedMsg{sessID: id, err: err}
		}
		if sess.Status == status.Finished {
			if err := stor.AcknowledgeFinished(sess.ID); err != nil {
				return reattachPreparedMsg{sessID: id, err: err}
			}
		}
		var prepErr error
		poller.reflowSessions([]string{id}, func() {
			prepErr = driver.PrepareAttach(id)
		})
		var warn string
		if prepErr != nil {
			warn = prepErr.Error()
		}
		return reattachPreparedMsg{sessID: id, warn: warn}
	}
}

// reviveSelected relaunches a dead session's tmux session under the same
// id, keeping its name, group, and history. Tools with a revive_command
// resume where they left off (e.g. claude --continue). On a group row it
// revives the whole subtree, mirroring the group kill.
func (m *Model) reviveSelected() (tea.Model, tea.Cmd) {
	entry, ok := m.selectedRow()
	if !ok {
		return m, nil
	}
	if entry.isGroup {
		return m.reviveMany(m.sessionsInGroup(entry.group), "no dead sessions to revive in "+entry.group)
	}
	set, err := m.sessionAndChildren(entry.sess)
	if err != nil {
		m.errBar.text = err.Error()
		return m, nil
	}
	dead := false
	for _, sess := range set {
		if !m.tmux.Exists(sess.ID) {
			dead = true
			break
		}
	}
	// The row the operator pressed v on is a running agent, so v is about
	// that agent: end it and bring it back on the conversation it is
	// already on.
	//
	// Its dead children come along only if asked for. A long-lived parent
	// collects them -- one fan-out reached 47, every one of them archived
	// weeks earlier -- and reviving that set is minutes of spawning, a
	// fleet's worth of memory, and no part of what the person meant by
	// "restart this session". Worse, the old path skipped the live parent
	// while it brought the 47 back, so the one session named in the dialog
	// was the only one it did not touch.
	if m.tmux.Exists(entry.sess.ID) {
		followers := deadSessions(m, set, entry.sess.ID)
		m.confirm = confirmTarget{
			action:       actionResume,
			sessions:     append([]store.Session{entry.sess}, followers...),
			keptChildren: followers,
			keepChildren: true,
			label: fmt.Sprintf("restart %s on the conversation it is on? ends the running agent and resumes where it left off.",
				entry.sess.Name),
		}
		m.mode = modeConfirmDelete
		return m, nil
	}
	if len(set) > 1 && dead {
		m.confirm = confirmTarget{
			action:   actionRevive,
			sessions: set,
			label: followConfirmLabel("revive", entry.sess.Name, len(set)-1,
				"brings it back.",
				"brings them back."),
		}
		m.mode = modeConfirmDelete
		return m, nil
	}
	if err := m.reviveSession(entry.sess); err != nil {
		m.reportLaunchError(err)
		return m, nil
	}
	m.errBar.text = m.degradedResumeNotice(entry.sess)
	m.requestRefresh()
	return m, nil
}

// reviveAllDead relaunches every dead session in the current view, resuming
// each by its captured id where one exists.
func (m *Model) reviveAllDead() (tea.Model, tea.Cmd) {
	return m.reviveMany(m.listedSessions(), "no dead sessions to revive")
}

// reviveMany relaunches every dead session in the list. It revives what it
// can and names the first failure rather than stopping, so one broken
// session does not block the rest.
func (m *Model) reviveMany(sessions []store.Session, emptyNotice string) (tea.Model, tea.Cmd) {
	revived, degraded := 0, 0
	var firstErr string
	for _, sess := range sessions {
		if sess.Status != status.Dead {
			continue
		}
		if err := m.reviveSession(sess); err != nil {
			if firstErr == "" {
				firstErr = err.Error()
			}
			continue
		}
		revived++
		if m.degradedResumeNotice(sess) != "" {
			degraded++
		}
	}
	switch {
	case revived == 0 && firstErr == "":
		m.errBar.text = emptyNotice
	case firstErr != "":
		m.errBar.text = fmt.Sprintf("revived %d, first error: %s", revived, firstErr)
	case degraded > 0:
		m.errBar.text = fmt.Sprintf("revived %d, %d without a captured id (used --continue)", revived, degraded)
	default:
		m.errBar.text = ""
	}
	m.requestRefresh()
	return m, nil
}

// sessionsInGroup lists the sessions the current view shows at or below a
// group, so a group action covers exactly the rows under it on screen.
func (m *Model) sessionsInGroup(path string) []store.Session {
	var sessions []store.Session
	for _, sess := range m.listedSessions() {
		if inGroupSubtree(sess.Group, path) {
			sessions = append(sessions, sess)
		}
	}
	return sessions
}

// degradedResumeNotice warns when a revived session had to fall back to the
// working directory's most recent conversation because its own conversation
// id was never captured, which resumes the wrong conversation whenever
// sessions share a directory.
func (m *Model) degradedResumeNotice(sess store.Session) string {
	tool, ok := m.cfg.Tools[sess.Tool]
	if !ok || sess.AgentSessionID != "" || tool.ResumeByIDCommand == "" {
		return ""
	}
	return fmt.Sprintf("revived %s with --continue: no conversation id captured, may resume the wrong conversation", sess.Name)
}

// reviveSession relaunches one dead session under its old id, keeping its
// name, group, and history. When the session's own conversation id was
// captured, it resumes that exact conversation via the tool's
// resume_by_id_command instead of the working directory's most recent one,
// which would be the wrong conversation whenever sessions share a cwd.
func (m *Model) reviveSession(sess store.Session) error {
	if m.tmux.Exists(sess.ID) {
		return fmt.Errorf("session %s is still running; revive only applies to dead sessions", sess.Name)
	}
	if live, running := m.supersededBy(sess); running {
		return fmt.Errorf("%s's conversation is already running in %s; a revive would start a second agent on it", sess.Name, live.Name)
	}
	return m.launchOnHeldConversation(sess)
}

// resumeSession is revive for a session that is still running: it ends the
// agent and brings it straight back on the conversation it was already on.
//
// The restart is the point rather than a side effect. A session picks up
// its skills, its settings and its MCP servers when the CLI process starts,
// so an agent that has been up since before those changed is answering with
// the old ones and cannot reload them from inside. Restarting on an empty
// context (actionRestart) buys that at the price of everything the
// conversation knows, which is why it was never the answer to "the tools
// are stale".
func (m *Model) resumeSession(sess store.Session) error {
	if m.tmux.Exists(sess.ID) {
		if err := m.killSession(sess); err != nil {
			return err
		}
	}
	return m.launchOnHeldConversation(sess)
}

// launchOnHeldConversation relaunches sess on the conversation the row
// already points at, whether it got there by dying or by being ended a
// moment ago.
func (m *Model) launchOnHeldConversation(sess store.Session) error {
	tool, ok := m.cfg.Tools[sess.Tool]
	if !ok {
		return fmt.Errorf("tool %s is no longer configured", sess.Tool)
	}
	if !isDir(sess.Cwd) {
		return fmt.Errorf("working directory no longer exists: %s", sess.Cwd)
	}
	bind := func() error {
		launchedAt := time.Now()
		if err := m.store.SetAgentLaunchedAt(sess.ID, launchedAt); err != nil {
			return err
		}
		m.bindReviveLocally(sess.ID, launchedAt)
		return nil
	}
	revive, err := launch.ReviveCommand(sess.Tool, tool, sess.AgentSessionID, sess.Model)
	if err != nil {
		return err
	}
	if err := m.relaunchSession(sess, tool, revive, status.Starting, bind); err != nil {
		return err
	}
	m.rebuildRows()
	return nil
}

// relaunchSession puts a dead session's row back on a running tmux window
// under its old id, keeping its name, group and history. Both revive and
// restart end here; they differ only in the command they hand it and in
// bindConversation, which records the conversation the new pane is on once
// the launch has actually taken. A launch that fails leaves the row exactly
// as it was, still pointing at the conversation it can be revived on.
func (m *Model) relaunchSession(sess store.Session, tool config.Tool, baseCommand, newStatus string, bindConversation func() error) error {
	sessionHooks, err := sessionhooks.Current()
	if err != nil {
		return err
	}
	contributed, err := sessionhooks.Env(sessionHooks, sess, extension.LaunchRelaunch, "", nil)
	if err != nil {
		return err
	}
	command, env, err := m.buildLaunch(sess.Tool, tool, baseCommand, sess.ID, sess.Model, sess.Account, contributed)
	if err != nil {
		return err
	}
	if err := m.tmux.Create(sess.ID, sess.Cwd, command, env, m.previewPaneWidth(), m.previewPaneHeight()); err != nil {
		return err
	}
	if bindConversation != nil {
		if err := bindConversation(); err != nil {
			_ = m.tmux.Kill(sess.ID)
			return err
		}
	}
	if err := m.tmux.SetLabel(sess.ID, sessionLabel(sess.Group, sess.Name)); err != nil {
		return err
	}
	if err := m.store.UpdateStatus(sess.ID, newStatus); err != nil {
		return err
	}
	accounts.RecordLaunch(m.store, sess.ID, sess.Tool, sess.Account)
	// A leftover ack from the previous life must not swallow the relaunched
	// agent's first finished alert.
	return m.store.SetAcked(sess.ID, false)
}

// restartSelected asks to relaunch the selected session with an empty
// context: the same row, directory and tool, running a brand new
// conversation instead of resuming the one it held.
func (m *Model) restartSelected() (tea.Model, tea.Cmd) {
	entry, ok := m.selectedRow()
	if !ok {
		return m, nil
	}
	if entry.isGroup {
		m.errBar.text = "restart applies to a session; pick one under " + displayGroup(entry.group)
		return m, nil
	}
	label := fmt.Sprintf("restart %s with an empty context? its current conversation is left behind.", entry.sess.Name)
	if m.tmux.Exists(entry.sess.ID) {
		label = fmt.Sprintf("restart %s with an empty context? ends the running agent and leaves its conversation behind.", entry.sess.Name)
	}
	m.confirm = confirmTarget{
		action:   actionRestart,
		sessions: []store.Session{entry.sess},
		label:    label,
	}
	m.mode = modeConfirmDelete
	return m, nil
}

// restartSession relaunches a session's tool from scratch. The conversation
// it was resuming is retired rather than resumed, so the agent comes back
// with the same name, directory and group but no context to carry.
func (m *Model) restartSession(sess store.Session) error {
	tool, ok := m.cfg.Tools[sess.Tool]
	if !ok {
		return fmt.Errorf("tool %s is no longer configured", sess.Tool)
	}
	if !isDir(sess.Cwd) {
		return fmt.Errorf("working directory no longer exists: %s", sess.Cwd)
	}
	if err := m.killSession(sess); err != nil {
		return err
	}
	baseCommand, agentSessionID := restartLaunch(tool)
	bind := func() error {
		launchedAt := time.Now()
		if err := m.store.RestartAgent(sess.ID, agentSessionID, launchedAt); err != nil {
			return err
		}
		m.bindRestartLocally(sess.ID, agentSessionID, launchedAt)
		return nil
	}
	// Starting, like a fresh spawn: the row reads as booting until the new
	// agent paints its pane.
	return m.relaunchSession(sess, tool, baseCommand, status.Starting, bind)
}

// restartLaunch builds what a restart runs: the tool's plain launch command,
// exactly as a brand new session gets it, plus a fresh conversation id for
// the tools that take one. Tools that mint their own id instead get nothing
// to carry, and the poller captures what they wrote.
func restartLaunch(tool config.Tool) (baseCommand, agentSessionID string) {
	if tool.SessionIDFlag == "" {
		return tool.Command, ""
	}
	agentSessionID = uuid.NewString()
	return tool.Command + " " + tool.SessionIDFlag + " " + agentSessionID, agentSessionID
}

// bindRestartLocally mirrors the store write in the loaded rows, so the list
// redraws on the new conversation before the next poll re-reads it.
func (m *Model) bindRestartLocally(id, agentSessionID string, launchedAt time.Time) {
	for i := range m.sessions {
		if m.sessions[i].ID != id {
			continue
		}
		if m.sessions[i].AgentSessionID != "" {
			m.sessions[i].RetiredAgentSessionID = m.sessions[i].AgentSessionID
		}
		m.sessions[i].AgentSessionID = agentSessionID
		m.sessions[i].AgentLaunchedAt = launchedAt
		m.sessions[i].Status = status.Starting
		m.sessions[i].Acked = false
	}
}

// bindReviveLocally mirrors the store write in the loaded rows, so the
// startup loader can paint before the next poll re-reads them.
func (m *Model) bindReviveLocally(id string, launchedAt time.Time) {
	for i := range m.sessions {
		if m.sessions[i].ID != id {
			continue
		}
		m.sessions[i].AgentLaunchedAt = launchedAt
		m.sessions[i].Status = status.Starting
		m.sessions[i].Acked = false
	}
	if sel, ok := m.selected(); ok && sel.ID == id {
		// A killed or archived row still holds its last pane; that would
		// count as painted and hide the loader. Its scroll offset goes with
		// it: a frame held back for a pane the operator was reading would
		// outlast the pane itself and keep the loader off the screen.
		m.clearPreviewState()
	}
}

// archiveSelected files the selected session away, or the whole subtree under
// a selected group. This is the one teardown the board has: it ends the pane,
// freeing everything the agent held, and files the row into the archived view
// (t) where u restores it and v revives it. A row left there runs out its
// retention window and is deleted for good -- see sweepExpiredArchives.
//
// A dead row archives too. The key means "I am done with this", and refusing
// on a session that already died would leave the only way to clear it being
// to wait out the window.
func (m *Model) archiveSelected() (tea.Model, tea.Cmd) {
	entry, ok := m.selectedRow()
	if !ok {
		return m, nil
	}
	if entry.isGroup {
		if entry.isRoot() {
			m.errBar.text = "root is the top level; end the groups under it instead"
			return m, nil
		}
		// Adopted panes stay in the set here, unlike the whole-view sweep:
		// their rows go where the group row goes, so leaving the panes up
		// would strand an agent nothing on the board still points at. The
		// count in the note is what warns about them.
		subtree := stillLive(m.sessionsInGroup(entry.group))
		_, adopted := splitAdopted(subtree)
		label := fmt.Sprintf("end group %s (%d sessions)? frees their RAM, t finds them, %s.%s",
			entry.group, len(subtree), archiveWindowPhrase, adoptedSetNote(len(adopted)))
		if len(subtree) == 0 {
			// An empty group is still worth filing away, and saying it holds
			// no sessions beats an "(0 sessions)" the reader has to decode.
			label = fmt.Sprintf("end group %s? nothing is running in it, t finds it.", entry.group)
		}
		m.confirm = confirmTarget{
			isGroup:  true,
			path:     entry.group,
			action:   actionArchive,
			sessions: subtree,
			label:    label,
		}
	} else {
		target, ok := m.archiveConfirmFor(entry.sess)
		if !ok {
			return m, nil
		}
		m.confirm = target
	}
	m.mode = modeConfirmDelete
	if m.skipsArchiveConfirm() {
		label := m.confirm.sessions[0].Name
		model, cmd := m.answerConfirm()
		if m.errBar.text == "" {
			m.archivedNotice(label)
		}
		return model, cmd
	}
	return m, nil
}

// stillLive drops the rows that are already filed away. Archiving one again
// would stamp it with a new archive time, and the whole-view and whole-group
// gestures reach every row the archived view lists -- so without this, one X
// in that view would quietly buy every session on screen another seven days.
func stillLive(sessions []store.Session) []store.Session {
	kept := sessions[:0:0]
	for _, sess := range sessions {
		if !sess.Archived {
			kept = append(kept, sess)
		}
	}
	return kept
}

// archiveConfirmFor builds the dialog for filing away one session and
// whatever it spawned. Not ok when the tree could not be read, with the
// reason already on the bar.
func (m *Model) archiveConfirmFor(sess store.Session) (confirmTarget, bool) {
	if sess.Archived {
		m.errBar.text = sess.Name + " is already archived - u restores it"
		return confirmTarget{}, false
	}
	sessions, err := m.sessionAndChildren(sess)
	if err != nil {
		m.errBar.text = err.Error()
		return confirmTarget{}, false
	}
	sessions = stillLive(sessions)
	// Agent children are named rather than counted. A terminal under a
	// session is that session's own shell and goes with it without comment;
	// a spawned agent is somebody's work in progress, and the dialog has to
	// say whose before it offers to end it.
	var spawned []store.Session
	for _, kid := range sessions {
		if kid.ID != sess.ID && !m.isShell(kid.Tool) {
			spawned = append(spawned, kid)
		}
	}
	label := followConfirmLabel("end", sess.Name, len(sessions)-1-len(spawned),
		"frees its RAM, t finds it, "+archiveWindowPhrase+".",
		"frees their RAM, t finds them, "+archiveWindowPhrase+".")
	if len(sessions) == 1 && sessions[0].TmuxPaneID != "" {
		label = adoptedArchiveLabel(sessions[0])
	} else {
		_, adopted := splitAdopted(sessions)
		label += adoptedSetNote(len(adopted))
	}
	if len(spawned) > 0 {
		label += " " + spawnedChildrenNote(m, spawned)
	}
	return confirmTarget{action: actionArchive, sessions: sessions, label: label, keptChildren: spawned}, true
}

// spawnedChildrenNote names the sessions a parent would take with it, and the
// key that leaves them running.
func spawnedChildrenNote(m *Model, spawned []store.Session) string {
	names := make([]string, 0, len(spawned))
	for _, kid := range spawned {
		names = append(names, m.displayName(kid))
	}
	unit := "session"
	if len(spawned) != 1 {
		unit = "sessions"
	}
	return fmt.Sprintf("it spawned %d %s (%s) — k keeps them running.",
		len(spawned), unit, strings.Join(names, ", "))
}

// archiveAllLive files away every session the current view lists, the batch
// counterpart to V. Panes the manager never started are left where they are:
// a sweep is a wide gesture, and ending somebody else's agent belongs to the
// row that warns about it by name.
func (m *Model) archiveAllLive() (tea.Model, tea.Cmd) {
	managed, adopted := splitAdopted(stillLive(m.listedSessions()))
	if len(managed) == 0 {
		m.errBar.text = sweepEmptyText("nothing to end", len(adopted))
		return m, nil
	}
	m.confirm = confirmTarget{
		action:   actionArchive,
		sessions: managed,
		label: fmt.Sprintf("end every session listed (%d)? frees their RAM, t finds them, %s.%s",
			len(managed), archiveWindowPhrase, sweepSkippedNote(len(adopted))),
		ack: fmt.Sprintf("yes, end all %d and start their 7 days", len(managed)),
	}
	m.mode = modeConfirmDelete
	return m, nil
}

// killSession ends one session's tmux window, freeing everything its agent
// held, while the store row keeps the name, group, history and conversation
// id that revive needs. The pane is captured first so the preview still
// shows the agent's last output once the window is gone.
func (m *Model) killSession(sess store.Session) error {
	// A pane the manager adopted belongs to whoever started it, and this is
	// the kill every path reaches for that has not warned about the pane:
	// restart, revive, anything that only wants the session gone. Refusing
	// here is what keeps those from ending an agent nobody warned about.
	// killAdoptedSession is the way through, and it is only ever called from
	// a confirmed answer to a dialog that names the pane -- the archive, and
	// the sweep that later clears what the archive left, each of which
	// spells out that the pane and its agent die.
	if sess.TmuxPaneID != "" {
		return fmt.Errorf("%s is a pane the manager did not start: end it from its own row, which warns first", sess.Name)
	}
	return m.endSession(sess, m.tmux.Kill)
}

// killAdoptedSession ends a pane the manager never started, taking the agent
// in it down with it. Only a confirmed archive, or the retention sweep that
// clears what one left behind, calls this.
func (m *Model) killAdoptedSession(sess store.Session) error {
	if sess.TmuxPaneID == "" {
		return m.killSession(sess)
	}
	return m.endSession(sess, m.tmux.KillAdopted)
}

// endSession is the bookkeeping both kills share: freeze the pane, end it,
// and leave the row reading dead.
func (m *Model) endSession(sess store.Session, kill func(string) error) error {
	if !m.tmux.Exists(sess.ID) {
		return nil
	}
	if pane, err := m.tmux.CapturePane(sess.ID); err == nil && pane != "" {
		if err := m.setSnapshots(map[string]string{sess.ID: pane}); err != nil {
			return err
		}
	}
	if err := m.killPanes([]killTarget{{sess: sess, kill: kill}})[sess.ID]; err != nil {
		return err
	}
	if err := m.forgetKilled(sess); err != nil {
		return err
	}
	if err := m.store.RecordEnd(sess, store.EndKilled); err != nil {
		return err
	}
	return m.store.UpdateStatus(sess.ID, status.Dead)
}

// killTarget is one pane to end and the kill its provenance calls for:
// KillAdopted for a pane the manager never started, Kill for its own.
type killTarget struct {
	sess store.Session
	kill func(string) error
}

// killPanes ends a whole batch of panes under ONE poller pause, and reports
// the kills that failed by session id.
//
// The pause is the expensive half, which is why it is taken once. A pass
// holds the poller's lock for its whole duration -- the listing, a capture
// per session, the process walk and the status writes -- and on a board of a
// few dozen sessions that runs into seconds when the store's write lock is
// contended. Pausing per session made archiving a group wait out one pass
// per row, in series, on the event loop, with the board frozen behind it.
//
// The pause itself is not optional: a poll that captured a half-killed pane
// would compare it against a pre-kill hash and read the teardown as
// streaming output.
func (m *Model) killPanes(targets []killTarget) map[string]error {
	errs := map[string]error{}
	if len(targets) == 0 {
		return errs
	}
	ids := make([]string, 0, len(targets))
	for _, target := range targets {
		ids = append(ids, target.sess.ID)
	}
	// Drops the pane hashes a revived session would be compared against, in
	// the same pause.
	m.poller.reflowSessions(ids, func() {
		for _, target := range targets {
			if err := target.kill(target.sess.ID); err != nil {
				errs[target.sess.ID] = err
			}
		}
	})
	return errs
}

// forgetKilled clears what a dead pane leaves behind: the hook status file
// that would otherwise decide what a revived session reads as, and the row's
// status on screen.
func (m *Model) forgetKilled(sess store.Session) error {
	if err := m.hooks.Remove(sess.ID); err != nil {
		return err
	}
	for i := range m.sessions {
		if m.sessions[i].ID == sess.ID {
			m.sessions[i].Status = status.Dead
		}
	}
	return nil
}

// livePanes answers "which panes are still up" from one tmux listing, in
// place of the has-session fork per session that Exists costs. A group
// archive used to ask that question three times a row -- once to capture,
// once to kill, and once more inside the kill itself.
//
// A listing that fails answers nothing and comes back nil, which paneIsLive
// reads as "ask per session": absent from a scan that never happened is not
// evidence a pane is gone, and this decides what gets killed.
//
// A rebuild holds one listing for all of it -- see holdLivePanes. It asks
// from more places than it looks: rebuildRows builds the tree twice to settle
// the cursor, sortTriageWithChildren asks again inside each build, and a
// refresh can rebuild twice over, which is eight forked listings for one
// keystroke's worth of rows. Within one rebuild their answers are read as
// interchangeable anyway.
func (m *Model) livePanes() map[string]bool {
	if m.livePaneHeld && m.livePaneFilled {
		return m.livePaneMemo
	}
	started := time.Now()
	scan, err := m.tmux.ScanPanes()
	m.traceStep("ui.livePanes", started)
	var live map[string]bool
	if err == nil {
		live = make(map[string]bool, len(scan.PIDs))
		for id := range scan.PIDs {
			live[id] = true
		}
	}
	if m.livePaneHeld {
		// A failed listing is memoised as the nil it answered with. Asking
		// again inside the same rebuild would fork at a server that has just
		// refused to answer, and a tree built half from a listing and half
		// from per-session questions is a tree built from two different
		// moments.
		m.livePaneMemo, m.livePaneFilled = live, true
	}
	return live
}

// holdLivePanes pins the listing a rebuild reads for the length of that
// rebuild, and hands back what was pinned before it so the caller can put it
// back. Release by defer.
//
// Nothing is listed up front: a rebuild that never asks -- which is every
// rebuild outside triage -- must not start forking one because this exists.
// The first ask fills the memo and every ask after it reads that.
//
// It saves and restores rather than simply clearing so that a rebuild reached
// from inside another one cannot, on its way out, drop the hold its caller is
// still relying on. Nothing nests today; the cost of making that true by
// construction is three fields.
func (m *Model) holdLivePanes() livePaneHold {
	held := livePaneHold{on: m.livePaneHeld, memo: m.livePaneMemo, filled: m.livePaneFilled}
	m.livePaneHeld, m.livePaneMemo, m.livePaneFilled = true, nil, false
	return held
}

// livePaneHold is what holdLivePanes displaced.
type livePaneHold struct {
	on     bool
	memo   map[string]bool
	filled bool
}

func (m *Model) releaseLivePanes(held livePaneHold) {
	m.livePaneHeld, m.livePaneMemo, m.livePaneFilled = held.on, held.memo, held.filled
}

// paneIsLive reads one session out of a livePanes listing, falling back to
// the per-session question when there is no listing to read.
func (m *Model) paneIsLive(sess store.Session, live map[string]bool) bool {
	if live == nil {
		return m.tmux.Exists(sess.ID)
	}
	return live[sess.ID]
}

func (m *Model) restoreSelected() (tea.Model, tea.Cmd) {
	entry, ok := m.selectedRow()
	if !ok {
		return m, nil
	}
	if entry.isGroup {
		subtree, err := m.store.SessionsInSubtree(entry.group)
		if err != nil {
			m.errBar.text = err.Error()
			return m, nil
		}
		m.confirm = confirmTarget{
			isGroup:  true,
			path:     entry.group,
			action:   actionRestore,
			sessions: subtree,
			label:    fmt.Sprintf("restore group %s (%d sessions)? brings them back.", entry.group, len(subtree)),
		}
	} else {
		sessions, err := m.sessionAndChildren(entry.sess)
		if err != nil {
			m.errBar.text = err.Error()
			return m, nil
		}
		m.confirm = confirmTarget{
			action:   actionRestore,
			sessions: sessions,
			label: followConfirmLabel("restore", entry.sess.Name, len(sessions)-1,
				"brings it back.",
				"brings them back."),
		}
	}
	m.mode = modeConfirmDelete
	return m, nil
}

// snapshotLive stores the last pane of every still-live session. Archive
// calls it before any kill so a capture failure cannot drop a frame.
//
// One write for the batch, and the kill that follows reuses these captures
// rather than taking its own: the pane it would capture is the one already
// stored here, a frame later.
func (m *Model) snapshotLive(sessions []store.Session, live map[string]bool) error {
	shots := make(map[string]string, len(sessions))
	for _, sess := range sessions {
		if !m.paneIsLive(sess, live) {
			continue
		}
		pane, err := m.tmux.CapturePane(sess.ID)
		if err != nil {
			return err
		}
		if pane == "" {
			continue
		}
		shots[sess.ID] = pane
	}
	return m.setSnapshots(shots)
}

// archiveConfirmed ends every session in the answered dialog and files the
// ones that died away, in one pause and one commit. It reports what failed
// without throwing away what succeeded.
//
// Every pane is killed before anything is written, and the write then covers
// exactly the panes that died -- so a pane that survived is never filed away
// as dead, and a dead one is never left behind a row still reading live. The
// panes are frozen by the snapshot pass above, so nothing on screen depends
// on the commit landing between one kill and the next: one contended write
// lock for the batch is enough.
func (m *Model) archiveConfirmed(live map[string]bool) string {
	var failed []string
	if m.confirm.keepChildren {
		m.confirm.sessions = withoutSessions(m.confirm.sessions, m.confirm.keptChildren)
	}
	// A parent leaving the list takes its fold with it, so a new session
	// reusing the id cannot inherit somebody else's decision.
	for _, sess := range m.confirm.sessions {
		if m.hasChildren(sess.ID) {
			m.clearChildFold(sess.ID)
		}
	}
	targets := make([]killTarget, 0, len(m.confirm.sessions))
	for _, sess := range m.confirm.sessions {
		if !m.paneIsLive(sess, live) {
			// Nothing to end. The row is still filed away below, which is
			// what archiving a dead session has always done.
			continue
		}
		kill := m.tmux.Kill
		if sess.TmuxPaneID != "" {
			// The dialog named the pane and the server and said what dies
			// with it; this is the answer to that, and the only reason a
			// batch may reach KillAdopted at all. killSession's refusal
			// still guards every path that did not warn.
			kill = m.tmux.KillAdopted
		}
		targets = append(targets, killTarget{sess: sess, kill: kill})
	}
	killErrs := m.killPanes(targets)
	// Failures are dropped from the batch below before this is read, so
	// being in it is what "this archive ended it" means.
	ended := make(map[string]bool, len(targets))
	for _, target := range targets {
		ended[target.sess.ID] = true
	}

	done := 0
	filed := make([]string, 0, len(m.confirm.sessions))
	killed := make([]string, 0, len(targets))
	for _, sess := range m.confirm.sessions {
		if err := killErrs[sess.ID]; err != nil {
			failed = append(failed, err.Error())
			continue
		}
		// Only a session this archive actually ended is cleaned up after as
		// one. A row that was already dead is filed away with the status it
		// stopped on, and its hook files were cleared when it died.
		if ended[sess.ID] {
			if err := m.forgetKilled(sess); err != nil {
				failed = append(failed, err.Error())
				continue
			}
			// Filed away is already never offered back, but a row restored
			// from the archive still has to read as ended on purpose.
			if err := m.store.RecordEnd(sess, store.EndArchived); err != nil {
				failed = append(failed, err.Error())
				continue
			}
			killed = append(killed, sess.ID)
		}
		filed = append(filed, sess.ID)
		// A group archive leaves the launch record alone, the way it always
		// has: restoring the group revives from it.
		if !m.confirm.isGroup {
			m.forgetLaunch(sess.ID)
		}
		done++
	}
	missing, err := m.store.ArchiveKilled(filed, killed)
	if err != nil {
		failed = append(failed, err.Error())
		done = 0
	} else {
		// A row that went out from under the dialog is reported the way the
		// per-session write used to report it, and does not count as filed.
		for _, id := range missing {
			failed = append(failed, fmt.Sprintf("session %s: %v", id, store.ErrSessionGone))
			done--
		}
		// The rest leave the active list on this frame rather than on the
		// next poll: the tree filters on what is in hand, and a row that
		// stays a poll longer reads as an archive that did not take.
		m.markArchivedLocally(filed, missing)
	}
	// The group row itself only follows its sessions once they have all
	// gone; a group marked archived over a session still running is the
	// stranding this is here to avoid.
	if m.confirm.isGroup && len(failed) == 0 {
		if err := m.store.SetGroupArchived(m.confirm.path, true); err != nil {
			failed = append(failed, err.Error())
		}
	}
	if len(failed) == 0 {
		return ""
	}
	return fmt.Sprintf("archived %d of %d: %s", done, len(m.confirm.sessions), strings.Join(failed, "; "))
}

// markArchivedLocally files rows away in the list on screen, matching the
// commit that just landed. Ids the commit could not find are left alone.
func (m *Model) markArchivedLocally(ids []string, missing []string) {
	if len(ids) == 0 {
		return
	}
	filed := make(map[string]bool, len(ids))
	for _, id := range ids {
		filed[id] = true
	}
	for _, id := range missing {
		delete(filed, id)
	}
	for i := range m.sessions {
		if filed[m.sessions[i].ID] {
			m.sessions[i].Archived = true
		}
	}
}

// sweepExpiredArchives deletes the rows whose stay in the archive has run
// past the retention window. It runs off the poll rather than off a
// key: the whole point of the window is that it expires without anyone
// watching.
//
// It reports its own failures to the bar and carries on. A sweep that cannot
// finish is not worth interrupting the operator for -- the rows it did not
// reach are a week old and will still be there on the next pass.
func (m *Model) sweepExpiredArchives() tea.Cmd {
	if time.Since(m.archiveSweptAt) < archiveSweepEvery {
		return nil
	}
	m.archiveSweptAt = time.Now()
	return m.sweepArchivesBefore(time.Now().Add(-archiveRetention))
}

// sweepArchivesBefore is the sweep itself, against a cutoff rather than the
// clock: everything archived before it goes.
func (m *Model) sweepArchivesBefore(cutoff time.Time) tea.Cmd {
	expired, err := m.store.ExpiredArchives(cutoff)
	if err != nil {
		m.errBar.text = "archive sweep: " + err.Error()
		return nil
	}
	if len(expired) > 0 {
		if err := m.deleteSessions(expired); err != nil {
			m.errBar.text = "archive sweep: " + err.Error()
			return nil
		}
	}
	// An archived group with nothing left under it goes with its sessions.
	// A group still holding one -- archived or live -- stays, and so does
	// every ancestor above it, so nothing is left without a home. This runs
	// whether or not a session expired on this pass: a group archived with
	// nothing under it has no session to wait for, and skipping the prune
	// when the session sweep found nothing is what would strand it forever.
	removed, err := m.store.PruneArchivedGroups("")
	if err != nil {
		m.errBar.text = "archive sweep: " + err.Error()
	}
	for _, path := range removed {
		delete(m.collapsed, path)
	}
	if len(removed) > 0 {
		m.persistCollapsed()
	}
	if len(expired) > 0 || len(removed) > 0 {
		m.rebuildRows()
	}
	return nil
}

func (m *Model) sessionAndChildren(sess store.Session) ([]store.Session, error) {
	kids, err := m.store.Children(sess.ID)
	if err != nil {
		return nil, err
	}
	out := make([]store.Session, 0, 1+len(kids))
	out = append(out, sess)
	return append(out, kids...), nil
}

// childrenFirst orders a follow-set so terminals go before the agent they
// hang under: a cleanup that fails partway leaves no row pointing at a
// parent that is already gone.
func childrenFirst(sessions []store.Session) []store.Session {
	ordered := make([]store.Session, 0, len(sessions))
	for _, sess := range sessions {
		if sess.ParentID != "" {
			ordered = append(ordered, sess)
		}
	}
	for _, sess := range sessions {
		if sess.ParentID == "" {
			ordered = append(ordered, sess)
		}
	}
	return ordered
}

// splitAdopted separates the sessions the manager started itself from the
// panes it only adopted, which every batch has to treat differently.
func splitAdopted(sessions []store.Session) (managed, adopted []store.Session) {
	for _, sess := range sessions {
		if sess.TmuxPaneID != "" {
			adopted = append(adopted, sess)
			continue
		}
		managed = append(managed, sess)
	}
	return managed, adopted
}

func adoptedPanes(n int) string {
	if n == 1 {
		return "1 adopted pane"
	}
	return fmt.Sprintf("%d adopted panes", n)
}

// sweepSkippedNote tells a wide archive's confirmation what it is leaving
// behind. A sweep skips adopted panes rather than counting them, because one
// keystroke over a whole view cannot be an informed answer about a pane
// somebody else is working in: a count says how many, never which, and y
// covers the batch either way. Ending one is a decision per pane, taken on
// its own row against a warning that names the pane.
func sweepSkippedNote(adopted int) string {
	if adopted == 0 {
		return ""
	}
	verb := "stays"
	if adopted != 1 {
		verb = "stay"
	}
	return fmt.Sprintf(" %s %s up: kill those from their own row.", adoptedPanes(adopted), verb)
}

// sweepEmptyText keeps a sweep with nothing of its own to end from reading
// as an empty board when adopted panes are running.
func sweepEmptyText(empty string, adopted int) string {
	if adopted == 0 {
		return empty
	}
	return fmt.Sprintf("%s (%s live, and a sweep leaves those alone: kill one from its own row)", empty, adoptedPanes(adopted))
}

// adoptedWhere names the pane a warning is about. "Adopted" is the manager's
// word for it and settles nothing for somebody deciding whether to end it;
// the server and the pane id are what they can go and look at first.
func adoptedWhere(sess store.Session) string {
	socket := sess.TmuxSocket
	if socket == "" {
		socket = adopt.DefaultSocket
	}
	return fmt.Sprintf("pane %s on tmux server %s", sess.TmuxPaneID, socket)
}

// adoptedSetNote warns an archive over a set -- a group, or a session with
// terminals under it -- about the panes in it the manager never started. It
// cannot leave them out the way a sweep does: their rows go where the set
// goes, so saying how many is all that is left.
func adoptedSetNote(adopted int) string {
	switch {
	case adopted == 0:
		return ""
	case adopted == 1:
		return " 1 of them is a pane the manager did not start: this kills that pane too, its agent and unsaved work with it."
	default:
		return fmt.Sprintf(" %d of them are panes the manager did not start: this kills those too, their agents and unsaved work with them.", adopted)
	}
}

// adoptedArchiveLabel warns that ending a pane the manager never started
// reaches past the row. "frees its RAM, t to find it" is a sentence about
// housekeeping -- and the row does come back, but the agent in the pane does
// not. An operator draining a queue at speed has to
// read that here, in the same words the kill and delete dialogs use.
func adoptedArchiveLabel(sess store.Session) string {
	return fmt.Sprintf("the manager did not start %s. it is %s. end it? this kills the pane, not just the row: the agent running in it dies, and whatever it has not saved dies with it. t finds the row again, the agent is gone.",
		sess.Name, adoptedWhere(sess))
}

// deadSessions are the members of set that are not running, skipping the one
// the operator selected, which the caller handles itself.
func deadSessions(m *Model, set []store.Session, selected string) []store.Session {
	var dead []store.Session
	for _, sess := range set {
		if sess.ID == selected || m.tmux.Exists(sess.ID) {
			continue
		}
		dead = append(dead, sess)
	}
	return dead
}

func followConfirmLabel(verb, name string, extra int, one, many string) string {
	if extra <= 0 {
		return fmt.Sprintf("%s %s? %s", verb, name, one)
	}
	unit := "terminal"
	if extra != 1 {
		unit = "terminals"
	}
	return fmt.Sprintf("%s %s and %d %s? %s", verb, name, extra, unit, many)
}

func (m *Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// A dialog carrying a tick keeps the keys until the tick is answered, so
	// this runs ahead of the mode bookkeeping below: neither the toggle nor
	// a y pressed too early is an answer, and both leave the dialog up.
	// The keep toggle is read before the tick and before the answer: it
	// changes what y does rather than answering for it, so the dialog stays
	// up either way and one ask covers both outcomes.
	if len(m.confirm.keptChildren) > 0 && msg.String() == "k" {
		m.confirm.keepChildren = !m.confirm.keepChildren
		return m, nil
	}
	confirmed := m.isAction(keymap.ContextConfirm, keymap.Confirm, msg)
	if m.confirm.ack != "" {
		switch {
		case m.isAction(keymap.ContextConfirm, keymap.Toggle, msg):
			m.confirm.acked = !m.confirm.acked
			m.confirm.nudged = false
			return m, nil
		case confirmed:
			if !m.confirm.acked {
				m.confirm.nudged = true
				return m, nil
			}
		}
	}
	fromFocus, answered := m.confirm.fromFocus, false
	// A relaunch the manager refused opened the hint dialog, and a triage
	// advance has already focused the session it moved on to; either owns
	// the mode. A dialog raised from a focused pane and then declined puts
	// the keys back in that pane. Every other answer falls back to the list.
	defer func() {
		switch {
		case m.mode == modeLaunchHint || m.mode == modeFocus:
		case fromFocus != "" && !answered:
			m.mode = modeFocus
		default:
			m.mode = modeList
		}
	}()
	if confirmed {
		answered = true
		// Follow-up work a confirmed answer leaves that must not run on the
		// event loop. Nothing the dialog offers has any: deleting a row for
		// good is the retention sweep's job now.
		var after tea.Cmd
		switch m.confirm.action {
		case actionArchive:
			// One listing for the whole dialog: the capture pass and the
			// kill pass both need to know which panes are still up, and
			// asking tmux per session per pass was most of what an archive
			// spent its time on.
			live := m.livePanes()
			if err := m.snapshotLive(m.confirm.sessions, live); err != nil {
				m.errBar.text = err.Error()
				return m, nil
			}
			m.errBar.text = m.archiveConfirmed(live)
			if m.errBar.text == "" {
				// Only a clean archive is offered back: a partial one left
				// some panes up, and a restore over that set would revive
				// sessions that never went anywhere. See archiveundo.go.
				m.noteArchived(m.confirm)
			}
			m.rebuildRows()
			var exit tea.Cmd
			if fromFocus != "" {
				// The pane the keys were going to is gone.
				exit = m.leaveFocus()
			}
			if m.triage {
				// A triage drain carries straight on into the queue rather
				// than stopping on the list, which is what the operator
				// asked for by archiving mid-drain -- and from the list as
				// well as from a pane, since in triage the board is one
				// queue being drained and taking a session off it is the
				// same request wherever it was made. It resumes at the head
				// rather than under the row that left, for the reason
				// enterTriageHead gives.
				after = tea.Batch(exit, m.enterTriageHead())
			} else {
				after = exit
			}
		case actionRestore:
			// Each session leaves the archive as it comes back, so a later
			// failure cannot strand a running one in the archived view.
			for _, sess := range m.confirm.sessions {
				if !m.tmux.Exists(sess.ID) {
					if err := m.reviveSession(sess); err != nil {
						m.reportLaunchError(err)
						return m, nil
					}
				}
				if m.confirm.isGroup {
					continue
				}
				if err := m.store.SetArchived(sess.ID, false); err != nil {
					m.errBar.text = err.Error()
					return m, nil
				}
			}
			if m.confirm.isGroup {
				if err := m.store.SetGroupArchived(m.confirm.path, false); err != nil {
					m.errBar.text = err.Error()
					return m, nil
				}
			}
			m.errBar.text = ""
		case actionRestart:
			for _, sess := range m.confirm.sessions {
				if err := m.restartSession(sess); err != nil {
					m.reportLaunchError(err)
					return m, nil
				}
			}
			m.errBar.text = ""
			m.rebuildRows()
		case actionResume:
			sessions := m.confirm.sessions
			if m.confirm.keepChildren {
				sessions = withoutSessions(sessions, m.confirm.keptChildren)
			}
			for _, sess := range sessions {
				// The selected session is running and is restarted; a
				// follower taken along is dead and is simply brought back.
				launch := m.resumeSession
				if !m.tmux.Exists(sess.ID) {
					launch = m.reviveSession
				}
				if err := launch(sess); err != nil {
					m.reportLaunchError(err)
					return m, nil
				}
			}
			m.errBar.text = ""
		case actionRevive:
			for _, sess := range m.confirm.sessions {
				if m.tmux.Exists(sess.ID) {
					continue
				}
				if err := m.reviveSession(sess); err != nil {
					m.reportLaunchError(err)
					return m, nil
				}
			}
			m.errBar.text = ""
		default:
			m.errBar.text = fmt.Sprintf("unknown confirm action %q", m.confirm.action)
			return m, nil
		}
		m.confirm = confirmTarget{}
		m.requestRefresh()
		return m, after
	}
	m.confirm = confirmTarget{}
	return m, nil
}

// deleteSessions takes every session given off the board for good: its pane,
// its hook files and its row.
//
// Children first, so a parent is never dropped out from under a row that is
// still pointing at it.
func (m *Model) deleteSessions(sessions []store.Session) error {
	for _, sess := range childrenFirst(sessions) {
		// An adopted row's pane goes with it. Every path here has already
		// ended it -- the archive did, and the window it then sat out is the
		// operator's chance to have said otherwise. Release still follows
		// the kill: the pane is
		// gone, and the row that was tracking it is going, so the driver has
		// nothing left to address.
		if sess.TmuxPaneID != "" {
			if err := m.killAdoptedSession(sess); err != nil {
				return err
			}
			m.tmux.Release(sess.ID)
		} else if err := m.tmux.Kill(sess.ID); err != nil {
			return err
		}
		if err := m.hooks.Remove(sess.ID); err != nil {
			return err
		}
		if err := m.hooks.RemoveName(sess.ID); err != nil {
			return err
		}
		delete(m.awaitedRenames, sess.ID)
		m.forgetLaunch(sess.ID)
		if err := m.store.Delete(sess.ID); err != nil {
			return err
		}
	}
	return nil
}

// withoutSessions drops a set of rows from a batch, for the archive that was
// told to leave the children where they are.
func withoutSessions(sessions, drop []store.Session) []store.Session {
	if len(drop) == 0 {
		return sessions
	}
	dropped := make(map[string]bool, len(drop))
	for _, sess := range drop {
		dropped[sess.ID] = true
	}
	kept := make([]store.Session, 0, len(sessions))
	for _, sess := range sessions {
		if !dropped[sess.ID] {
			kept = append(kept, sess)
		}
	}
	return kept
}
