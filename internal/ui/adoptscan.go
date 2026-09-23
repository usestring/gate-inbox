package ui

import (
	"database/sql"
	"errors"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/adopt"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/sessname"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// adoptScanInterval is how often the manager repeats the search for agent
// panes it did not start. Slow on purpose: a scan spawns a tmux process per
// server, captures every pane and reads the process table, and a session
// somebody started by hand is not urgent.
//
// It is a steady-state figure only. The first scan does not wait for it -- see
// adoptStart -- because until one has run there is nothing on the board at all,
// and an empty board reads as a broken program rather than as a slow one.
const adoptScanInterval = 45 * time.Second

type adoptTickMsg struct{}

// adoptedMsg carries what a scan took, so the next frame shows it.
type adoptedMsg struct {
	taken      int
	candidates int
	rejected   string
	err        error
}

func (m *Model) adoptTick() tea.Cmd {
	return tea.Tick(adoptScanInterval, func(time.Time) tea.Msg { return adoptTickMsg{} })
}

// adoptStart is what startup asks for instead of adoptTick: the first scan runs
// now and the timer covers every scan after it.
func (m *Model) adoptStart() tea.Cmd {
	return tea.Batch(m.adoptScan(), m.adoptTick())
}

// adoptScan finds agent panes on other tmux servers and takes the ones it is
// confident about.
//
// Everything the scan needs is read here, on the event loop, and the work
// happens in the returned closure. Reading the model from inside a command is a
// data race: commands run on their own goroutine while Update writes.
func (m *Model) adoptScan() tea.Cmd {
	if m.store == nil || m.tmux == nil {
		return nil
	}
	tools := m.adoptTools()
	if len(tools) == 0 {
		return nil
	}
	run := &adoptRun{
		tools:    tools,
		self:     m.selfPaneKey(),
		known:    make(map[string]bool, len(m.sessions)),
		names:    make(map[string]bool, len(m.sessions)),
		index:    m.convos,
		drift:    m.drift,
		stor:     m.store,
		driver:   m.tmux,
		rejected: map[string]int{},
	}
	for _, sess := range m.sessions {
		if key := adoptKey(sess.TmuxSocket, sess.TmuxPaneID); key != "" {
			run.known[key] = true
		}
		run.names[sess.Name] = true
	}
	sockets := m.adoptSockets()

	return func() tea.Msg {
		started := time.Now()
		var candidates []adopt.Candidate
		for _, socket := range sockets {
			candidates = append(candidates, adopt.Panes(socket)...)
		}
		// The rows are read after the panes, and from the store rather than
		// the model, so archived sessions still count as accounted for.
		// take also checks managed ids against the live store, since a launch
		// can commit after this snapshot.
		rows, err := run.stor.ListSessions(true)
		if err != nil {
			logging.Warn("adopt scan failed", "at", "list sessions", logging.Err(err))
			return adoptedMsg{err: err}
		}
		run.onBoard = onBoardSessions(rows)

		taken, err := run.take(candidates, adopt.NewProcTable())
		logging.Info("adopt scan",
			"sockets", sockets, "candidates", len(candidates), "taken", taken,
			"rejected", rejectionSummary(run.rejected), "took", time.Since(started).Round(time.Millisecond).String())
		return adoptedMsg{taken: taken, candidates: len(candidates), rejected: rejectionSummary(run.rejected), err: err}
	}
}

// adoptRun is one scan's decision state: what the board already holds, and
// what it has decided so far.
//
// It is a value rather than a closure because the scan has to survive being
// pointed at a server other than the ones a running manager reads. Its own
// tests build a tmux server of the shape that broke it -- one session, many
// windows, an agent in each -- and drive take over exactly that, which a
// closure that lists sockets for itself cannot be asked to do without
// reaching tmux's default server, where the operator's real agents live.
type adoptRun struct {
	tools []adopt.Tool
	// self is the adoptKey of the pane the manager itself is drawing in, or
	// empty when it is not running under tmux. Never adopted -- see
	// adoptable.
	self string
	// known is every pane the board already addresses, by socket and pane id.
	// This is the dedupe that matters: it is what a pane is, and it holds
	// across scans and restarts because it is rebuilt from the rows.
	known map[string]bool
	// onBoard is every gi_ session a row accounts for. Only the manager's own
	// sessions belong here -- see adoptable.
	onBoard map[string]bool
	// names is every name the board is using, so a new row cannot collide
	// with one.
	names    map[string]bool
	index    *convo.Index
	drift    *sessname.Drift
	stor     *store.Store
	driver   *tmux.Driver
	rejected map[string]int
}

func (r *adoptRun) reject(candidate adopt.Candidate, why string, extra ...any) {
	r.rejected[why]++
	if !logging.Enabled(logging.LevelDebug) {
		return
	}
	logging.Debug("adopt rejected", append([]any{
		"socket", candidate.Socket, "pane", candidate.PaneID,
		"session", candidate.Session, "cwd", candidate.Cwd, "why", why,
	}, extra...)...)
}

// take identifies the panes worth adopting, names them together, and writes a
// row for each. It reports how many it took.
//
// Identification first, creation second, with the naming pass in between. The
// names have to be decided over the whole batch rather than one row at a
// time: eleven windows of the operator's "main" session are eleven agents in
// one checkout, so a per-row decision sees eleven identical directory
// basenames and can only number them.
func (r *adoptRun) take(candidates []adopt.Candidate, procs *adopt.ProcTable) (int, error) {
	var accepted []adoptCandidate
	for _, candidate := range candidates {
		if ok, why := adoptable(candidate, r.self, r.known, r.onBoard); !ok {
			r.reject(candidate, why)
			continue
		}
		pane, err := adopt.Capture(candidate.Socket, candidate.PaneID)
		if err != nil {
			r.reject(candidate, "capture failed", "err", err.Error())
			continue
		}
		match, ok := adopt.Identify(candidate, r.tools, pane, procs)
		if !ok {
			r.reject(candidate, "no tool matched")
			continue
		}
		if !match.Confident() {
			r.reject(candidate, "not confident", "tool", match.Tool, "signals", signalNames(match.Signals))
			continue
		}
		id, recovered, ok, err := r.rowID(candidate)
		if err != nil {
			return 0, err
		}
		if !ok {
			continue
		}
		// The pane key is claimed as soon as the pane is accepted, so two
		// candidates naming the same pane -- the same socket reached under
		// two spellings, say -- cannot both reach creation below.
		r.known[adoptKey(candidate.Socket, candidate.PaneID)] = true
		if tmux.Managed(candidate.Session) {
			// A recovered gi_ session may have been split into several panes;
			// one row for it is the recovery, two is a mess. A foreign
			// session is not claimed: its other windows are other agents, and
			// the pane key above is what keeps this one from being taken
			// twice.
			r.onBoard[candidate.Session] = true
		}
		accepted = append(accepted, adoptCandidate{
			id: id, recovered: recovered, candidate: candidate, tool: match.Tool, pane: pane,
		})
	}

	titled := adoptNames(accepted, procs, r.names, r.index, r.drift)

	taken := 0
	for _, entry := range accepted {
		candidate := entry.candidate
		name, fromTitle := titled[entry.id]
		source := store.SourceTitle
		if !fromTitle {
			// No conversation could be attributed to this pane, so the
			// directory basename is all there is. Recording it as derived is
			// what lets the periodic naming pass replace it once the agent
			// has written a title.
			name, source = adoptName(candidate.Cwd, candidate.Session, r.names), store.SourceDerived
		}
		sess := store.Session{
			ID:         entry.id,
			Name:       name,
			NameSource: source,
			Tool:       entry.tool,
			Cwd:        candidate.Cwd,
			Status:     status.Idle,
			TmuxSocket: candidate.Socket,
			TmuxPaneID: candidate.PaneID,
		}
		if err := r.stor.CreateSession(sess); err != nil {
			if entry.recovered {
				// A launch may commit between the lookup and this insert.
				if _, lookupErr := r.stor.Get(sess.ID); lookupErr == nil {
					r.reject(candidate, "managed session already has a row")
					continue
				}
			}
			logging.Warn("adopt scan failed", "at", "create session",
				"socket", candidate.Socket, "pane", candidate.PaneID,
				"taken", taken, logging.Err(err))
			return taken, err
		}
		target := tmux.Target{Socket: candidate.Socket, Name: candidate.PaneID}
		if err := r.driver.Adopt(sess.ID, target); err != nil {
			logging.Warn("adopt scan failed", "at", "register target",
				"session", sess.ID, "socket", candidate.Socket, "pane", candidate.PaneID,
				"taken", taken, logging.Err(err))
			return taken, err
		}
		r.names[sess.Name] = true
		taken++
		logging.Info("adopt took a pane",
			"session", sess.ID, "name", sess.Name, "tool", sess.Tool,
			"socket", candidate.Socket, "pane", candidate.PaneID, "cwd", candidate.Cwd)
	}
	return taken, nil
}

func (r *adoptRun) rowID(candidate adopt.Candidate) (id string, recovered, ok bool, err error) {
	if !tmux.Managed(candidate.Session) {
		return newID(), false, true, nil
	}
	id, named := tmux.SessionID(candidate.Session)
	if !named {
		r.reject(candidate, "managed session name carries no row id")
		return "", false, false, nil
	}
	switch _, err := r.stor.Get(id); {
	case err == nil:
		r.reject(candidate, "managed session already has a row")
		r.onBoard[candidate.Session] = true
		return "", false, false, nil
	case errors.Is(err, sql.ErrNoRows):
		return id, true, true, nil
	default:
		return "", false, false, err
	}
}

// adoptCandidate is a pane identification accepted, held until the batch can
// be named together.
type adoptCandidate struct {
	// id is chosen before the row exists because the naming pass keys its
	// answers by it, and the row is created with the name that comes back.
	id        string
	candidate adopt.Candidate
	tool      string
	recovered bool
	// pane is the capture identification already paid for. Naming compares it
	// against what the conversations said, so re-capturing would be a second
	// tmux round trip per pane for text this scan is still holding.
	pane string
}

// adoptNames gives each accepted pane the name its own conversation carries,
// and returns only the ones it could attribute. A pane left out keeps the
// directory-basename placeholder its caller falls back to.
//
// The cwd is not enough to name these rows. The operator's panes are nearly
// all in one checkout, so naming from the directory would hand twenty-one
// rows the same word and leave the rail reading sample-repo, sample-repo-2 ... sample-repo-21 --
// which says nothing about which agent is which, and is exactly the rail the
// per-row rename in smartrename.go exists to repair by hand.
//
// So this uses the signal that rename uses: convo.Link separates panes
// sharing a directory on the agent's own process, since Claude Code writes a
// sidecar naming the pid and the conversation, and a pid in this pane's
// process tree is an identity rather than a resemblance. The proc table and
// the pane capture are the scan's own, already read.
//
// Batched rather than per row because convo.Link is a batch assignment: it
// weighs a conversation against every pane competing for it, and the solo
// signal it leans on does not exist for a single pane considered alone.
func adoptNames(accepted []adoptCandidate, procs *adopt.ProcTable, taken map[string]bool, index *convo.Index, drift *sessname.Drift) map[string]string {
	named := map[string]string{}
	if index == nil || len(accepted) == 0 {
		return named
	}
	panes := make([]convo.Pane, 0, len(accepted))
	var dirs []string
	seenDir := map[string]bool{}
	for _, entry := range accepted {
		panes = append(panes, convo.Pane{
			Key:  entry.id,
			Tool: entry.tool,
			Cwd:  entry.candidate.Cwd,
			Text: convo.Normalize(entry.pane),
			PIDs: procs.PIDs(entry.candidate.PID),
		})
		if dir := entry.candidate.Cwd; dir != "" && !seenDir[dir] {
			seenDir[dir] = true
			dirs = append(dirs, dir)
		}
	}
	// A refresh that failed partway still published what it read, so the link
	// is attempted either way: one unreadable tool must not cost every pane
	// its name.
	if err := index.Refresh(dirs); err != nil {
		logging.Warn("adopt naming read the conversations partially", logging.Err(err))
	}
	assignment := convo.Link(panes, index.Conversations())

	entries := make([]sessname.Entry, 0, len(accepted))
	for _, entry := range accepted {
		match, ok := assignment.For(entry.id)
		if !ok {
			continue
		}
		title := drift.Title(entry.id, match.Conversation.Title, match.Conversation.Prompts)
		if title == "" {
			// A conversation too young to have been titled. Nothing to say
			// yet, and the periodic naming pass will come back for it.
			continue
		}
		entries = append(entries, sessname.Entry{
			ID:       entry.id,
			Title:    title,
			Fallback: filepath.Base(entry.candidate.Cwd),
		})
	}
	// Assign settles collisions across the batch as well as against the
	// board, which is what keeps two agents working on the same ticket from
	// both wanting one name.
	for id, name := range sessname.Assign(sessname.Kebab{}, entries, taken) {
		if name == "" {
			continue
		}
		named[id] = name
		// Reserved immediately so the directory-basename fallback, which
		// disambiguates against this same set, cannot hand the name to a pane
		// this pass could not attribute.
		taken[name] = true
	}
	return named
}

// signalNames renders what identification actually saw, since "not
// confident" on its own says nothing about which signal it was. A refusal
// reading "signals=prompt" is the rule working; one reading
// "signals=command" would be a bug.
func signalNames(signals []adopt.Signal) string {
	names := make([]string, len(signals))
	for i, signal := range signals {
		names[i] = string(signal)
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

// rejectionSummary keeps the shape of a scan on one line at info, so the
// per-candidate detail is only needed when the counts look wrong.
func rejectionSummary(counts map[string]int) string {
	if len(counts) == 0 {
		return "none"
	}
	reasons := make([]string, 0, len(counts))
	for reason, count := range counts {
		reasons = append(reasons, reason+"="+strconv.Itoa(count))
	}
	sort.Strings(reasons)
	return strings.Join(reasons, " ")
}

// restoreAdopted re-registers the panes a previous run took, so an adopted
// session survives a restart of the manager.
//
// A pane id is stable for the pane's life but not beyond it, so a row whose
// pane closed while the manager was down is registered here like any other and
// then dropped by the poll loop, which is the one place that decides a pane is
// gone and does it on evidence.
func (m *Model) restoreAdopted() {
	if m.tmux == nil {
		return
	}
	for _, sess := range m.sessions {
		if sess.TmuxPaneID == "" {
			continue
		}
		target := tmux.Target{Socket: sess.TmuxSocket, Name: sess.TmuxPaneID}
		if err := m.tmux.Adopt(sess.ID, target); err != nil {
			m.errBar.text = err.Error()
		}
	}
}

// dropSelfRow deletes a row pointing at the manager's own pane.
//
// The scan refuses that pane now, but adoption only ever adds -- so a row an
// older store already holds for it is never revisited, and it still opens the
// manager inside the manager. This is the only thing that takes one off.
//
// Deleting is safe because such a row is an adopted one by construction: the
// pane is the manager's, so nothing here created it and nothing here kills it.
// Only the row goes. Archived rows are included, since an archived self-row is
// one the operator can unarchive and open.
func (m *Model) dropSelfRow() {
	self := m.selfPaneKey()
	if m.store == nil || self == "" {
		return
	}
	rows, err := m.store.ListSessions(true)
	if err != nil {
		logging.Warn("pruning the manager's own row failed", "at", "list sessions", logging.Err(err))
		return
	}
	for _, sess := range rows {
		if adoptKey(sess.TmuxSocket, sess.TmuxPaneID) != self {
			continue
		}
		if err := m.store.Delete(sess.ID); err != nil {
			logging.Warn("pruning the manager's own row failed", "at", "delete",
				"session", sess.ID, logging.Err(err))
			continue
		}
		logging.Info("dropped a row pointing at the manager's own pane",
			"session", sess.ID, "name", sess.Name,
			"socket", sess.TmuxSocket, "pane", sess.TmuxPaneID)
	}
}

// adoptTools is the configured tools as identification needs them.
//
// A block with no command is dropped, because the command is what identifies
// a pane. A block with no prompt marker is kept: it used to be dropped as a
// permanent near-miss, back when a match needed both signals, and under the
// process-decides rule it identifies its panes perfectly well without one.
// An unparseable marker costs the block only its weaker signal.
func (m *Model) adoptTools() []adopt.Tool {
	var tools []adopt.Tool
	for name, tool := range m.cfg.Tools {
		if tool.Shell || tool.Command == "" {
			continue
		}
		var prompt *regexp.Regexp
		if tool.ActivityCutoff != "" {
			compiled, err := regexp.Compile(tool.ActivityCutoff)
			if err == nil {
				prompt = compiled
			}
		}
		tools = append(tools, adopt.Tool{
			Name:    name,
			Command: tool.Command,
			Prompt:  prompt,
			Shell:   tool.Shell,
		})
	}
	return tools
}

// adoptSockets is the tmux servers a scan looks at.
//
// The default server, where a session somebody started by hand lives, plus
// anything configured. The default one is scanned even when it is the
// manager's own server, which it is by default: dropping it would blind the
// scan to every pane the operator started. What keeps the manager from
// adopting its own sessions there is their gi_ name, not the socket.
//
// Deliberately not every socket in tmux's directory. Sockets are left behind
// when a server exits, so that directory is mostly dead files -- a sweep is a
// tmux process per entry, nearly all of them failing.
func (m *Model) adoptSockets() []string {
	sockets := []string{adopt.DefaultSocket}
	seen := map[string]bool{adopt.DefaultSocket: true}
	for _, socket := range m.cfg.AdoptSockets {
		if socket == "" || seen[socket] || socket == m.tmux.SocketName() {
			continue
		}
		seen[socket] = true
		sockets = append(sockets, socket)
	}
	return sockets
}

// onBoardSessions is the tmux session name of every row the store holds, so a
// pane can be asked whether anything still points at it.
func onBoardSessions(rows []store.Session) map[string]bool {
	names := make(map[string]bool, len(rows))
	for _, sess := range rows {
		names[tmux.SessionName(sess.ID)] = true
	}
	return names
}

// adoptable reports whether a scanned pane is one the scan may take, and
// names the refusal when it is not, so the rejection counters say which rule
// fired rather than lumping every "not this one" together.
//
// Never the manager's own pane. The scan takes a pane for what is running in
// it, and the manager is normally started from inside a pane an agent already
// holds -- so `claude` is in the manager's own process ancestry, the command
// signal matches, and since #997 that signal is sufficient on its own. The row
// it produced opened the manager inside the manager, and that one nests
// without limit. self is matched on (socket, pane id), the same identity
// adoption already dedupes on, rather than on a name or a guess about the
// process: pane identity is the only thing that distinguishes this manager's
// pane from any other pane a claude process is in.
//
// A *sibling* manager's pane stays adoptable. That is deliberate: the operator
// runs more than one, and a second manager's pane is a real pane with real
// content that opens exactly once -- it shows the sibling, not a copy of the
// caller, so there is no regress to prevent. Refusing it would also mean
// identifying a manager by its process, which is the guess this rule avoids.
//
// Never a pane already on the board. That is the whole rule for a session the
// manager did not create, because in one of those the unit of an agent is the
// pane: the operator runs a dozen independent agents as windows of a
// long-lived session called "main", and they share nothing but a name.
//
// The session-wide refusal below is only for the manager's own gi_ sessions,
// and it stays exactly as strict as it was. The manager shares a server with
// the panes it scans, and taking one of its own sessions a second time would
// give that pane a row that no longer answers to revive, rename or kill --
// and a managed session that was split into several panes is one agent, so
// one row for it is the recovery and two is a mess.
//
// An gi_ session with no row is the opposite case and the reason the scan
// looks at its own server at all. It is an orphan -- an agent still running
// with nothing left pointing at it, which is what a lost, moved or
// freshly-initialised store leaves behind -- and recovering it onto the board
// is the only way anybody finds it again.
//
// Generalising the session refusal to foreign sessions is what made the board
// unusable: 21 of the operator's 23 running agents were rejected because one
// other window of the same tmux session already had a row.
func adoptable(candidate adopt.Candidate, self string, known, onBoard map[string]bool) (bool, string) {
	key := adoptKey(candidate.Socket, candidate.PaneID)
	if self != "" && key == self {
		return false, "the manager's own pane"
	}
	if known[key] {
		return false, "pane already adopted"
	}
	if !tmux.Managed(candidate.Session) {
		return true, ""
	}
	if onBoard[candidate.Session] {
		// This pane is new, but it is another pane of a managed session a row
		// already accounts for, so a split window of one of the manager's own
		// agents is passed over.
		return false, "managed session already has a row"
	}
	return true, ""
}

// selfPaneKey is the pane the manager itself is drawing in, in the identity
// adoption dedupes on. Empty when the manager is not running under tmux, which
// every caller reads as "there is no pane of ours to exclude".
func (m *Model) selfPaneKey() string {
	return adoptKey(m.ownSocket, m.ownPane)
}

// adoptKey identifies a pane independently of which session row holds it.
func adoptKey(socket, pane string) string {
	if pane == "" {
		return ""
	}
	return socket + "\x00" + pane
}

// adoptName is the fallback for a pane adoptNames could not attribute to a
// conversation: the directory it is working in, which is what its operator
// would call it. Falls back again to the tmux session it came from, and
// disambiguates against names already on the board.
//
// Its numbering is a placeholder, which is why a row named here is written as
// store.SourceDerived: the periodic naming pass is then free to replace it
// the moment the agent writes a title.
func adoptName(cwd, session string, taken map[string]bool) string {
	name := filepath.Base(cwd)
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = session
	}
	if name == "" {
		name = "adopted"
	}
	candidate := name
	for i := 2; taken[candidate]; i++ {
		candidate = name + "-" + strconv.Itoa(i)
	}
	return candidate
}
