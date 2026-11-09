package ui

import (
	"image/color"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/cifeedback"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/forge"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
	"github.com/usestring/gate-inbox/internal/workspec"
	"github.com/usestring/gate-inbox/internal/worktracker"
)

// workTickInterval only decides how often the tracker is *offered* the work.
// What actually reaches GitHub and Linear is Tracker.due, which holds each
// reference to its own interval, so a fast tick here costs nothing.
const workTickInterval = 30 * time.Second

type workTickMsg struct{}

// workLoadedMsg carries no payload. The tracker is the state and it is
// concurrency-safe, so the message exists to draw the next frame, not to
// hand results back through the event loop.
type workLoadedMsg struct{}

// newWorkTracker is the tracker the board runs: real git, gh and Linear as
// far as the config switches them on, the operator's settle window, and the
// store keeping its sightings.
func newWorkTracker(cfg config.Config, st *store.Store) *worktracker.Tracker {
	gh, linear := forge.NewResolvers(forge.Providers{
		GitHub:       cfg.Integrations.GitHub.On(),
		Linear:       cfg.Integrations.Linear.On(),
		LinearAPIKey: os.Getenv("LINEAR_API_KEY"),
	})
	tracker := worktracker.New(worktracker.NewGit(), gh, linear)
	tracker.SettleAfter = cfg.Work.SettleAfter.Duration
	if st != nil {
		tracker.WithSeen(st)
		if cfg.Work.CIFeedback {
			var agentTools []string
			for name, tool := range cfg.Tools {
				if !tool.Shell {
					agentTools = append(agentTools, name)
				}
			}
			tracker.OnPRRefresh = func(prs map[string]forge.PR) {
				if err := cifeedback.Observe(st, prs, agentTools); err != nil {
					logging.Warn("could not queue CI feedback", logging.Err(err))
				}
			}
		} else if err := st.CancelCIFeedback(time.Now()); err != nil {
			logging.Warn("could not retire disabled CI feedback", logging.Err(err))
		}
	}
	return tracker
}

func (m *Model) workTick() tea.Cmd {
	return tea.Tick(workTickInterval, func(time.Time) tea.Msg { return workTickMsg{} })
}

// refreshWork rediscovers what every session is working on and resolves it.
//
// Everything the model owns is read here, on the event loop; the shelling out
// and the network happen in the returned closure. Reading m.sessions from
// inside a command is a data race -- commands run on their own goroutine while
// Update writes.
func (m *Model) refreshWork() tea.Cmd {
	if m.work == nil {
		return nil
	}
	type row struct {
		id      string
		dir     string
		launch  string
		live    bool
		visible bool
	}
	// Looked-at sessions first, and marked. The tracker refreshes them on a shorter interval
	// and puts them at the head of a capped pass, so what is under the cursor is right now and
	// the rest of a large board drains behind it.
	onScreen := m.attentionSet()
	rows := make([]row, 0, len(m.sessions))
	for _, sess := range m.sessions {
		rows = append(rows, row{
			id:      sess.ID,
			dir:     sess.Cwd,
			launch:  workLaunch(sess),
			live:    sess.Status != status.Dead,
			visible: onScreen[sess.ID],
		})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].visible && !rows[j].visible })
	tracker, driver, stor, history := m.work, m.tmux, m.store, m.history

	return func() tea.Msg {
		var live []string
		for _, r := range rows {
			if r.live {
				live = append(live, r.id)
			}
		}
		panes := workPanes(driver, live)
		sessions := make([]worktracker.Session, 0, len(rows))
		for _, r := range rows {
			tracked := worktracker.Session{
				ID:        r.id,
				Dir:       r.dir,
				Text:      workText(panes[r.id], stor, history, r.id, r.launch),
				Artifacts: publishedArtifacts(stor, history, r.id),
				Live:      r.live,
				Visible:   r.visible,
			}
			tracker.Discover(tracked)
			sessions = append(sessions, tracked)
		}
		tracker.Refresh(sessions)
		return workLoadedMsg{}
	}
}

// workLaunch is the task text a row was given. A migrated row's own launch
// prompt is the hand-over brief, so the task the conversation started on,
// and the tickets and pull requests it named, comes from the opening the
// migration carried over.
func workLaunch(sess store.Session) string {
	if len(sess.MigrationOpening) == 0 {
		return sess.LaunchPrompt
	}
	return strings.Join(append(slices.Clone(sess.MigrationOpening), sess.LaunchPrompt), "\n")
}

// attentionSet is the sessions the operator can currently see work for: the
// list's selected row, where the badge and the rows hanging off it are the
// only work state large enough to read. Anywhere else -- a form, a composer,
// a dialog -- nothing is visible and everything falls back to its ordinary
// interval, which is the right answer: the operator is not looking at the
// board at all.
func (m *Model) attentionSet() map[string]bool {
	seen := map[string]bool{}
	if index, ok := m.selectedIndex(); ok && m.rows[index].isSession() {
		seen[m.rows[index].sess.ID] = true
	}
	return seen
}

// workPanes reads the visible screen of every live session a work pass scans,
// chained per tmux server rather than forked per pane. The pass runs every
// thirty seconds over the whole board, and forking capture-pane once per
// session cost a 200-pane box about six tmux processes a second, averaged
// over the tick, from this loop alone. A pane that cannot be read is left
// out; its row is scanned from what else is known about it.
func workPanes(driver *tmux.Driver, ids []string) map[string]string {
	out := make(map[string]string, len(ids))
	if driver == nil || len(ids) == 0 {
		return out
	}
	for id, capture := range driver.CapturePanes(ids) {
		if capture.Err == nil {
			out[id] = ansi.Strip(capture.Text)
		}
	}
	return out
}

// workText is everything a session is known to have said.
//
// What it is showing right now matters most, and for a session the manager did
// not start it is the only thing there is: an adopted session has no launch
// prompt, because nobody here wrote one, and no final capture, because it has
// not ended. Read against a real machine, that left every adopted row with
// nothing to scan and so no pull request and no ticket.
//
// The visible screen, not scrollback. Reading scrollback was tried and
// measured: agent CLIs are full-screen apps, so they run on tmux's alternate
// screen, which keeps no history -- 86 of 87 panes on this machine, one with
// three lines of scrollback. Two thousand lines of capture bought two more
// references out of seventy-eight and cost three times as much per pane.
//
// The transcript comes in through the search index, which reads every
// transcript incrementally anyway and keeps the pull requests the session
// opened itself. Without that a pull request opened an hour ago, now scrolled
// off the screen, was not the session's work as far as the board could tell.
// Only what it opened: a session mentions many pull requests it is not on.
//
// pane is the visible screen, already read by workPanes and stripped; an
// empty one is a session with no screen to scan.
func workText(pane string, stor *store.Store, history *search.Index, id, launch string) string {
	text := launch
	if pane != "" {
		text += "\n" + pane
	}
	if opened := openedPRs(stor, history, id); len(opened) > 0 {
		text += "\n" + strings.Join(opened, "\n")
	}
	if stor == nil {
		return text
	}
	if snapshot, err := stor.Snapshot(id); err == nil && snapshot != "" {
		text += "\n" + snapshot
	}
	return text
}

// openedPRs is every pull request the session is known to have opened: what
// the index has read from the transcript, unioned with what the store
// remembers, each URL on its own line as the tracker's creation rule expects.
//
// The store is written through here, not in the index, because the index is
// deliberately stateless and rebuilt from files; the store is what survives a
// restart onto a new conversation and the CLI's own history retention. A URL
// the store already holds is not written again, so a quiet board costs no
// transactions.
func openedPRs(stor *store.Store, history *search.Index, id string) []string {
	var stored []string
	if stor != nil {
		stored, _ = stor.OpenedPRs(id)
	}
	seen := make(map[string]bool, len(stored))
	for _, url := range stored {
		seen[url] = true
	}
	urls := stored
	var fresh []string
	if history != nil {
		for _, url := range strings.Split(history.Created(id), "\n") {
			if url == "" || seen[url] {
				continue
			}
			seen[url] = true
			urls = append(urls, url)
			fresh = append(fresh, url)
		}
	}
	if stor != nil && len(fresh) > 0 {
		if err := stor.RecordOpenedPRs(id, fresh, time.Now()); err != nil {
			logging.Warn("work: could not record opened pull requests", logging.Err(err))
		}
	}
	return urls
}

// publishedArtifacts is every page the session published itself: what its
// extensions attached through extension.AttachWork, which the store holds, and
// the Claude artifacts the index read from the transcript -- an Artifact call
// paired with the result that printed the page's address. Only those: a page
// the session merely linked was somebody else's work.
//
// As with openedPRs, what the index finds is written through to the store, so
// a page stays the session's work after a restart moves it onto a new
// conversation. A page the store already holds under the same title is not
// written again, so a quiet board costs no transactions.
func publishedArtifacts(stor *store.Store, history *search.Index, id string) []workspec.Ref {
	var stored []store.SessionWork
	if stor != nil {
		var err error
		if stored, err = stor.SessionWorkItems(id); err != nil {
			logging.Warn("work: could not read attached work", logging.Err(err))
		}
	}
	held := make(map[string]string, len(stored))
	var refs []workspec.Ref
	for _, item := range stored {
		if item.Kind != string(workspec.KindArtifact) {
			continue
		}
		if item.Source == "" {
			held[item.ID] = item.Title
		}
		refs = append(refs, workspec.AttachedRef(item.ID, item.URL, item.Title))
	}
	if history == nil {
		return refs
	}
	var found []workspec.Ref
	for _, line := range strings.Split(history.Published(id), "\n") {
		url, title, _ := strings.Cut(line, "\t")
		if ref, ok := workspec.ArtifactRef(url, title); ok {
			found = append(found, ref)
		}
	}
	var fresh []store.SessionWork
	for _, ref := range workspec.Dedupe(found) {
		if title, ok := held[ref.URL]; ok && title == ref.Title {
			continue
		}
		fresh = append(fresh, store.SessionWork{Kind: string(workspec.KindArtifact), ID: ref.URL, URL: ref.URL, Title: ref.Title})
	}
	if stor != nil && len(fresh) > 0 {
		if err := stor.AttachSessionWork(id, fresh, time.Now()); err != nil {
			logging.Warn("work: could not record published pages", logging.Err(err))
		}
	}
	return append(refs, found...)
}

// checksGlyph gives CI a shape before it gives it a colour, so the column
// still reads on a terminal that renders none.
func prColor(pr forge.PR) color.Color {
	switch {
	case pr.NeedsYou():
		return colorErrored
	case pr.State == forge.PRMerged:
		return colorFinished
	case pr.State == forge.PRDraft, pr.State == forge.PRClosed:
		return colorIdle
	default:
		return colorWorking
	}
}

// workBadge is a folded session's whole workload, counted: "10 prs · 3
// issues", led by one mark when something under it wants a person.
//
// It names nothing. Naming a session's pull request and its ticket on the meta
// line is what put a reference somewhere other than the tree, and it named two
// of however many there were with no sign the rest existed -- on this machine
// eleven sessions were under-reported that way, and one wearing a green mark
// was the one with a red check on a pull request the badge did not reach.
// Counts say how much there is and the mark says whether any of it wants a
// person; the tree says which.
//
// room is the columns the name line can spare once the meta has taken its
// own; countLadder is what gives way as it runs out, and below the last rung
// the marks that were here before words were still say how much there is. A
// rail too narrow for even one mark shows none: the fold arrow beside the
// name already says there is work, so nothing is lost but the count.
func (m *Model) workBadge(sessID string, room int) string {
	if m.work == nil {
		return ""
	}
	rows := m.workRowsFor(sessID)
	if len(rows) == 0 || room < badgeSeparator+1 {
		return ""
	}
	inner := room - badgeSeparator
	summary := workCounts(rows, inner)
	if summary == "" {
		summary = workSummary(rows, marksInRoom(inner))
	}
	return badgeGap + summary
}

// workSummary is a folded session's whole workload on one line: one mark per
// artifact, in the order the rail lists them, so folding hides the detail
// without hiding what needs doing.
//
// max caps the marks, because a session on twenty artifacts would otherwise
// push its own name off the line. The rows arrive with whatever wants a person
// first, so the marks that survive the cap are the ones worth surviving, and
// the count that replaces the rest still says how much is folded away.
func workSummary(rows []workRow, max int) string {
	shown, hidden := rows, 0
	if max < 1 {
		max = 1
	}
	if len(rows) > max {
		shown, hidden = rows[:max-1], len(rows)-(max-1)
	}
	marks := make([]string, 0, len(shown)+1)
	for _, row := range shown {
		marks = append(marks, tinted(row.stateHint).Render(row.glyph))
	}
	if hidden > 0 {
		marks = append(marks, subtleText("+"+strconv.Itoa(hidden)))
	}
	return strings.Join(marks, " ")
}

// countLadder is the order the badge gives ground in, widest first: the
// punctuation, then the words, then the mark, then the words again. It is a
// package-level slice and the rungs are built one at a time, because
// workBadge is asked for every folded session a frame paints.
var countLadder = []struct {
	sep    string
	form   countForm
	marked bool
}{
	{" · ", countWords, true},
	{" ", countWords, true},
	{" ", countShort, true},
	{" ", countShort, false},
	{" ", countTiny, false},
}

// workCounts is the workload in words, in the widest form that fits room, and
// empty when none does.
func workCounts(rows []workRow, room int) string {
	var counts workCount
	needsYou := false
	for _, row := range rows {
		switch row.kind {
		case kindTicket:
			counts.tickets++
		case kindArtifact:
			counts.artifacts++
		default:
			counts.prs++
		}
		needsYou = needsYou || row.needsYou
	}
	mark := ""
	if needsYou {
		mark = lipgloss.NewStyle().Foreground(colorErrored).Render(statusGlyph(status.Waiting)) + " "
	}
	for _, rung := range countLadder {
		text := countText(counts, rung.sep, rung.form)
		if rung.marked {
			text = mark + text
		}
		if textfmt.Width(text) <= room {
			return text
		}
	}
	return ""
}

// countForm is how much of a count's unit survives the width it is given:
// the whole word, two letters, or one. The number never gives way, because
// the number is the answer.
type countForm int

const (
	countWords countForm = iota
	countShort
	countTiny
)

// countUnit is one kind of artifact at each of those widths.
type countUnit struct{ tiny, short, one, many string }

var (
	prUnit       = countUnit{"p", "pr", "pr", "prs"}
	ticketUnit   = countUnit{"i", "is", "issue", "issues"}
	artifactUnit = countUnit{"a", "ar", "artifact", "artifacts"}
)

// workCount is how many of each kind a session is on.
type workCount struct{ prs, tickets, artifacts int }

func (u countUnit) text(n int, form countForm) string {
	switch form {
	case countTiny:
		return strconv.Itoa(n) + u.tiny
	case countShort:
		return strconv.Itoa(n) + u.short
	}
	if n == 1 {
		return strconv.Itoa(n) + " " + u.one
	}
	return strconv.Itoa(n) + " " + u.many
}

// countText joins the counts. A kind the session is not on is left out
// rather than written as a zero: "3 issues" is the whole truth about a
// session on no pull requests, and "0 prs · 3 issues" spends four cells
// saying nothing.
func countText(counts workCount, sep string, form countForm) string {
	var parts []string
	for _, kind := range []struct {
		n    int
		unit countUnit
	}{{counts.prs, prUnit}, {counts.tickets, ticketUnit}, {counts.artifacts, artifactUnit}} {
		if kind.n > 0 {
			parts = append(parts, kind.unit.text(kind.n, form))
		}
	}
	return strings.Join(parts, sep)
}

// badgeGap is what separates the badge from the name it follows, and
// badgeSeparator is its width. Two spaces rather than the " · " this used
// when the badge lived inside the meta line: the badge is a run of its own
// beside the name, not another field on a dotted list.
const (
	badgeGap       = "  "
	badgeSeparator = len(badgeGap)
)

// marksInRoom is how many marks fit: one, then two cells for each after it.
func marksInRoom(room int) int { return (room + 1) / 2 }

// The kinds of work row, as workRow.kind holds them.
const (
	kindPR       = "PR"
	kindTicket   = "TICKET"
	kindArtifact = "ARTIFACT"
)

// workRow is one artifact a session is on: a pull request, a ticket, or a page
// it published.
type workRow struct {
	needsYou bool
	// rank is where the row sorts among its kind, most wanting a person first,
	// and updated breaks a tie newest first. See sortWorkRows.
	rank    int
	updated time.Time
	// key is the tracker's name for the reference, which is what a sighting
	// is recorded against.
	key string
	// more is set on the one row the rail draws in place of a session's
	// tail: how many rows it stands for. Such a row names nothing, and W
	// puts the rows it stands for back on the rail.
	more  int
	kind  string
	label string
	// short is the label with everything but the identity dropped, for a card
	// too narrow to hold the repository. A truncated "example-org/sample-repo…"
	// loses the number, which is the only part that names the pull request.
	short  string
	detail string
	url    string
	// glyph is the row folded down to one mark, for the line a collapsed
	// session shows instead of its children.
	glyph     string
	stateHint color.Color
	// from is the child session a rolled-up row came from, empty on a
	// session's own work.
	from string
}

// identity is what makes two rows the same artifact. An unlooked row has no
// tracker key, but its kind and label already name the reference.
func (r workRow) identity() string {
	if r.key != "" {
		return r.key
	}
	return r.kind + ":" + r.label
}

// workRowsFor is everything one session and its children are on, pull requests
// above tickets and, within them, whatever wants a person first -- the list's
// own promise, so the two orderings agree.
//
// Every reference gets a row. One the sources have answered for and had
// nothing to say about is the exception, and the only one: workspec's ticket
// pattern matches "CVE-2024" and "HTTP-2" as readily as it matches a ticket
// key, so a source saying it has no such issue is the answer that a mention
// was never work. Silence is not that answer, which is why an unanswered
// reference still gets a row.
func (m *Model) workRowsFor(sessID string) []workRow {
	if rows, ok := m.workMemo[sessID]; ok {
		return rows
	}
	rows := m.buildWorkRows(sessID)
	if m.workMemo != nil {
		m.workMemo[sessID] = rows
	}
	return rows
}

// buildWorkRows is workRowsFor without the memo: three maps out of the
// tracker, a sort by evidence and a sort by who is blocked. The callers ask
// per row rather than per session — railWorkFold and workBadge both ask for
// every session a frame paints, and anyRailWork asks for every row in the
// tree — so a frame and a rebuild each hold one answer per session.
//
// A session's rows include what its children are on, each tagged with the
// child it came from. The operator reads a parent to see what came out of its
// subtree, and a folded parent draws none of its children, so the parent's
// own rows are the only place that answer can live.
func (m *Model) buildWorkRows(sessID string) []workRow {
	rows := m.ownWorkRows(sessID)
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		seen[row.identity()] = true
	}
	for _, child := range m.descendants(sessID) {
		from := m.displayName(child)
		for _, row := range m.ownWorkRows(child.ID) {
			if seen[row.identity()] {
				continue
			}
			seen[row.identity()] = true
			row.from = from
			rows = append(rows, row)
		}
	}
	sortWorkRows(rows)
	return rows
}

// descendants is every session under sessID, nearest first. Archived ones are
// left out, since work the operator put away should not come back on the
// parent, but their own live children are not.
func (m *Model) descendants(sessID string) []store.Session {
	var out []store.Session
	visited := map[string]bool{sessID: true}
	frontier := []string{sessID}
	for len(frontier) > 0 {
		var next []string
		for _, sess := range m.sessions {
			if visited[sess.ID] || !slices.Contains(frontier, sess.ParentID) {
				continue
			}
			visited[sess.ID] = true
			next = append(next, sess.ID)
			if !sess.Archived {
				out = append(out, sess)
			}
		}
		frontier = next
	}
	return out
}

// ownWorkRows is the work one session itself is on, in evidence order.
func (m *Model) ownWorkRows(sessID string) []workRow {
	work := m.work.For(sessID)
	var rows []workRow
	for _, ref := range workspec.ByEvidence(work.Refs) {
		key := ref.Key()
		// An artifact the settle window has taken off the board is over and
		// was on screen while it was over. The rail is the only place work is
		// drawn now, and it is a list of what is live.
		if work.Retired[key] {
			continue
		}
		// A value with no look behind it came back from the last run. It is drawn, because
		// an empty work column after a restart is worse than a remembered one, but it is
		// drawn as remembered: dimmed, and stamped with when it was actually true. It
		// stops saying "as of" as soon as this run has confirmed it, which is within a
		// tick — restored references are due immediately.
		remembered := !work.Looked[key]
		if ref.Kind == workspec.KindArtifact {
			rows = append(rows, publishedRow(ref))
			continue
		}
		if pr, ok := work.PRs[key]; ok {
			detail := string(pr.State)
			if glyph := checksGlyph(pr.Checks); glyph != "" {
				detail += " " + glyph
			}
			if pr.Review == forge.ReviewChangesRequested {
				detail += " · changes requested"
			}
			colour := prColor(pr)
			if remembered {
				detail += asOf(pr.FetchedAt)
				colour = colorIdle
			}
			rows = append(rows, workRow{
				needsYou: pr.NeedsYou(), rank: int(pr.Rank()), updated: pr.UpdatedAt, key: key, kind: kindPR,
				label: pr.Repo + "#" + strconv.Itoa(pr.Number), short: "#" + strconv.Itoa(pr.Number),
				detail: detail, url: pr.URL, glyph: prGlyph(pr), stateHint: colour,
			})
			continue
		}
		if ticket, ok := work.Tickets[key]; ok {
			colour := colorWorking
			if ticket.Done() {
				colour = colorFinished
			}
			detail := ticket.State
			if remembered {
				detail += asOf(ticket.FetchedAt)
				colour = colorIdle
			}
			rows = append(rows, workRow{
				rank: ticketRank(ticket), key: key, kind: kindTicket, label: ticket.Identifier, short: ticket.Identifier,
				detail: detail, url: ticket.URL, glyph: ticketGlyph(ticket), stateHint: colour,
			})
			continue
		}
		if !work.Looked[key] {
			rows = append(rows, unlookedRow(ref))
		}
	}
	return rows
}

// sortWorkRows puts pull requests above tickets and tickets above published
// pages, then orders each kind by how much it wants a person: forge.PR.Rank for
// a pull request -- blocked, ready to merge, awaiting review, checks running,
// draft, merged, closed -- and open before done for a ticket. A pull request is
// the thing the operator acts on and a ticket is the trail it leaves, so the
// kind outranks the blocker; a published page wants nobody, so it goes last.
//
// Within a rank the newest activity leads. A row with no timestamp, one
// restored from before the column existed, keeps its evidence order behind
// the rows that have one.
func sortWorkRows(rows []workRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		if a, b := kindOrder(rows[i].kind), kindOrder(rows[j].kind); a != b {
			return a < b
		}
		if rows[i].rank != rows[j].rank {
			return rows[i].rank < rows[j].rank
		}
		return rows[i].updated.After(rows[j].updated)
	})
}

// kindOrder is where a kind of row sorts on the rail.
func kindOrder(kind string) int {
	switch kind {
	case kindPR:
		return 0
	case kindTicket:
		return 1
	}
	return 2
}

const (
	ticketRankStarted = iota
	ticketRankTodo
	ticketRankDone
)

// ticketRank puts a ticket still being worked above one that is not started,
// and both above a done one. A ticket Linear has not answered for sorts as not
// started.
func ticketRank(ticket forge.Ticket) int {
	switch {
	case ticket.Done():
		return ticketRankDone
	case ticket.StateType == "started":
		return ticketRankStarted
	default:
		return ticketRankTodo
	}
}

// noteWorkSeen records that these rows were on a screen the operator had
// open, which is what starts the settle clock on the ones that are over.
// "Had open" rather than "had in view": every row under an expanded session,
// whether or not the rail had scrolled to it. A row below the fold of a
// screen nobody scrolls is exactly the one that would otherwise never leave,
// and the operator's chance to see it is what the clock is for.
func (m *Model) noteWorkSeen(rows []workRow) {
	if m.work == nil || len(rows) == 0 {
		return
	}
	keys := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.key != "" {
			keys = append(keys, row.key)
		}
	}
	m.work.MarkSeen(keys)
}

// asOf stamps a remembered value with when it was last true.
//
// The date as well as the time once it is not from today: "as of 09:12" on a Monday morning, for a
// value taken on Friday, is the one way this could mislead worse than an empty row would.
func asOf(at time.Time) string {
	if at.IsZero() {
		return " · remembered"
	}
	local := at.Local()
	if now := time.Now(); local.YearDay() == now.YearDay() && local.Year() == now.Year() {
		return " · as of " + local.Format("15:04")
	}
	return " · as of " + local.Format("Jan 2 15:04")
}

// unlookedRow is a reference nothing has answered for yet, drawn rather than
// dropped. "We could not look" is not "nothing is there" -- the rule the work
// view's health line already keeps for the sources as a whole, kept here for
// the one reference, because the rail has no health line and a session whose
// every reference went unanswered used to read as a session working on
// nothing.
func unlookedRow(ref workspec.Ref) workRow {
	row := workRow{
		detail:    "not looked up",
		stateHint: colorIdle,
	}
	if ref.Kind == workspec.KindTicket {
		row.kind, row.label, row.short = kindTicket, ref.Identifier, ref.Identifier
		row.glyph = currentGlyphs.ticketTodo
		row.rank = ticketRankTodo
		return row
	}
	row.kind = kindPR
	row.rank = int(forge.RankUnlooked)
	row.glyph = currentGlyphs.prPending
	row.label = ref.Repo + "#" + strconv.Itoa(ref.Number)
	row.short = "#" + strconv.Itoa(ref.Number)
	// A pull request's address is its repository and number, both of which the
	// reference already holds, so the row is clickable before GitHub has
	// answered for it. A ticket's address needs the workspace slug, which only
	// Linear knows.
	row.url = "https://github.com/" + ref.Repo + "/pull/" + strconv.Itoa(ref.Number)
	return row
}

// publishedRow is a page the session published. It has no state to look up --
// it is live for as long as the session that published it is on the board --
// so the row is the title the publish call gave it, the page's id when it gave
// none, and where it is hosted.
func publishedRow(ref workspec.Ref) workRow {
	host := strings.TrimPrefix(ref.URL, "https://")
	host, _, _ = strings.Cut(host, "/")
	label := ref.Title
	if label == "" {
		label = ref.Identifier
	}
	return workRow{
		key: ref.Key(), kind: kindArtifact, label: label, short: label,
		detail: host, url: ref.URL, glyph: currentGlyphs.artifact, stateHint: colorWorking,
	}
}

// prGlyph is a pull request reduced to the one mark a folded session shows
// for it. It is a square, never one of the list's circles, so an expanded
// session's work does not read as more sessions; fill carries the state as
// it does on the circles, and colour reinforces it.
func prGlyph(pr forge.PR) string {
	switch {
	case pr.NeedsYou():
		return currentGlyphs.prNeedsYou
	case pr.State == forge.PRMerged:
		return currentGlyphs.prMerged
	case pr.State == forge.PRDraft:
		return currentGlyphs.prDraft
	case pr.State == forge.PRClosed:
		return currentGlyphs.prClosed
	case pr.Checks == forge.ChecksPending:
		return currentGlyphs.prPending
	default:
		return currentGlyphs.prOpen
	}
}

// ticketGlyph is the ticket half of the same vocabulary, a triangle read off
// Linear's own category rather than the per-team state name.
func ticketGlyph(ticket forge.Ticket) string {
	switch {
	case ticket.Done():
		return currentGlyphs.ticketDone
	case ticket.StateType == "started":
		return currentGlyphs.ticketStarted
	default:
		return currentGlyphs.ticketTodo
	}
}

// workFoldPrefix namespaces a session's fold inside the tree's collapsed set.
// A fold here makes the same promise the list makes about a group — it
// survives a restart — and the set is where that promise is already kept. A
// group path is a slash-separated name, so the prefix cannot collide with one.
const workFoldPrefix = "work:"

func (m *Model) workFolded(sessID string) bool { return m.collapsed[workFoldPrefix+sessID] }

func (m *Model) setWorkFolded(sessID string, folded bool) {
	if m.collapsed == nil {
		m.collapsed = map[string]bool{}
	}
	m.collapsed[workFoldPrefix+sessID] = folded
}

// railWorkRows is what hangs off a session in the main list: everything it is
// on, pull requests above tickets.
func (m *Model) railWorkRows(sess store.Session) []workRow {
	if m.work == nil {
		return nil
	}
	return m.workRowsFor(sess.ID)
}

// hasRailWork reports whether a session is expandable at all. A session on
// nothing must stay exactly the row it was.
func (m *Model) hasRailWork(sess store.Session) bool { return len(m.railWorkRows(sess)) > 0 }

// workFoldDecision is what the user has said about a session's work, if
// anything. Auto-expansion fills the gap and is not itself a decision, which
// is what keeps F's direction and the footer's label from changing every time
// the cursor moves.
func (m *Model) workFoldDecision(sessID string) (folded, decided bool) {
	folded, decided = m.collapsed[workFoldPrefix+sessID]
	return folded, decided
}

func (m *Model) clearWorkFold(sessID string) { delete(m.collapsed, workFoldPrefix+sessID) }

// autoExpands reports whether a session's work is on screen only because the
// cursor is on it, which is the one thing a step can add or take away.
func (m *Model) autoExpands(sessID string) bool {
	if sessID == "" || m.work == nil || m.compactRail() {
		return false
	}
	if _, decided := m.workFoldDecision(sessID); decided {
		return false
	}
	return len(m.workRowsFor(sessID)) > 0
}

// railWorkExpanded says whether a session's artifacts are on the rail. The
// rail carries the whole fleet at once -- eighty-odd sessions and seventy-odd
// references on this machine -- so exactly one undecided session opens, the
// one under the cursor.
//
// An explicit fold outranks that: → ← space and F are things a person did,
// and a default only ever fills a cell nobody has written.
//
// A compact rail opens none by default. On a phone a pull request and its
// ticket are two of the dozen rows the list has, and the cursor would spend
// them on every session it crossed; the row's own badge already counts them.
func (m *Model) railWorkExpanded(sessID string) bool {
	if folded, decided := m.workFoldDecision(sessID); decided {
		return !folded
	}
	return sessID != "" && sessID == m.railCursorSess && !m.compactRail()
}

// railWorkCap is the most rows a session hangs on the rail. The rail is the
// whole fleet on one screen, and the cursor opens a session's work as it
// passes; a session on twenty pull requests would push the next session off
// the screen every time the cursor crossed it. The rows arrive pull requests
// first and blocked ones first among those, so what the cap cuts is the
// tickets and the quiet tail -- and W lifts it when the tail is what you came
// for.
const railWorkCap = 5

// artifactRows is an expanded session's work as tree rows. They carry the
// session itself, so the tree holds no session that was invented for a row.
func (m *Model) artifactRows(sess store.Session, depth int) []treeRow {
	if !m.railWorkExpanded(sess.ID) {
		return nil
	}
	work := m.railWorkRows(sess)
	if !m.showAllWork && len(work) > railWorkCap {
		shown := work[:railWorkCap-1]
		work = append(shown[:len(shown):len(shown)], m.moreRow(len(work)-len(shown)))
	}
	// The rail is built on the event loop and only for rows it is about to
	// draw, so this is the one place the rail's sightings are taken.
	m.noteWorkSeen(work)
	rows := make([]treeRow, 0, len(work))
	for i := range work {
		rows = append(rows, treeRow{sess: sess, depth: depth, art: &work[i]})
	}
	return rows
}

// moreRow is the rail's last row under a session with more work than the
// cap: how many rows it stands for, and the key that puts them back -- read
// off the key map, because that key is rebindable and a row naming the
// default would send a reader to a key their board no longer answers.
func (m *Model) moreRow(hidden int) workRow {
	detail := "↵ shows all"
	if key := m.tightCap(keymap.ContextList, keymap.ShowAllWork); key != "" {
		detail = key + " shows all"
	}
	return workRow{
		more: hidden, glyph: "…", stateHint: colorIdle,
		label: strconv.Itoa(hidden) + " more", short: strconv.Itoa(hidden) + " more",
		detail: detail,
	}
}

// toggleShowAllWork lifts the rail's cap, or puts it back. It is the whole
// board at once rather than the session under the cursor: a cap the reader
// has just been shown the end of is not one they want back the moment the
// cursor moves on.
//
// The cursor parks off an artifact when the row it is on is about to go:
// every row past the cap when the cap comes back, and the count line itself
// when it lifts. Lifting takes nothing else away, so a cursor on an ordinary
// artifact stays where the reader left it.
//
// Lifting also re-reads github and linear, which is the manual refresh the
// board otherwise has none of once the work card is gone: asking for every
// row is asking whether they are still true, and the ticker's own pass can
// be most of a minute away.
func (m *Model) toggleShowAllWork() tea.Cmd {
	entry, onRow := m.cursorRow()
	if m.showAllWork || (onRow && entry.isArtifact() && entry.art.more > 0) {
		m.parkCursorOffArtifact()
	}
	m.showAllWork = !m.showAllWork
	m.rebuildRows()
	if !m.showAllWork {
		return nil
	}
	return m.refreshWork()
}

// toggleRailWork opens or shuts the work under the session the cursor is on,
// or under the artifact it is on. The cursor lands on that session first: the
// row it was on may be one the fold is about to hide.
func (m *Model) toggleRailWork() {
	index, ok := m.selectedIndex()
	if !ok {
		return
	}
	entry := m.rows[index]
	if entry.isGroup || !m.hasRailWork(entry.sess) {
		return
	}
	m.cursor = index
	m.setWorkFolded(entry.sess.ID, m.railWorkExpanded(entry.sess.ID))
	m.persistWorkFolds()
	m.rebuildRows()
}

// parkCursorOffArtifact moves the cursor onto the session an artifact hangs
// off, for the keys that are about to fold that session's work away.
func (m *Model) parkCursorOffArtifact() {
	if entry, ok := m.cursorRow(); !ok || !entry.isArtifact() {
		return
	}
	if index, ok := m.selectedIndex(); ok {
		m.cursor = index
	}
}

// railFoldWidth is the fold mark and the space after it. Every session row
// spends it or none does: a row indented past the row above it reads as that
// row's child, and a rail with nothing to fold would rather have the cells.
const railFoldWidth = 2

func (m *Model) anyRailWork(rows []treeRow) bool {
	for _, row := range rows {
		if row.isSession() && m.hasRailWork(row.sess) {
			return true
		}
	}
	return false
}

// railFoldReserve is the width railWorkFold is holding, which a row's second
// line has to clear to stay under the name on its first.
func (m *Model) railFoldReserve() int {
	if !m.railWorkColumn {
		return 0
	}
	return railFoldWidth
}

// railWorkFold is the mark that says a session has children and whether they
// are showing, in the group row's own vocabulary.
func (m *Model) railWorkFold(sess store.Session) string {
	if !m.railWorkColumn || !m.hasRailWork(sess) {
		return spaces(m.railFoldReserve())
	}
	if m.railWorkExpanded(sess.ID) {
		return subtleText("▾") + " "
	}
	return subtleText("▸") + " "
}

// persistWorkFolds writes the folds through the setting the list already
// persists. A model with no store cannot be written to and is only ever a
// test's.
func (m *Model) persistWorkFolds() {
	if m.store == nil {
		return
	}
	m.persistCollapsed()
}
