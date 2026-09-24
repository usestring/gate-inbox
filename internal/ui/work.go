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
			launch:  sess.LaunchPrompt,
			live:    sess.Status != status.Dead,
			visible: onScreen[sess.ID],
		})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].visible && !rows[j].visible })
	tracker, driver, stor, history := m.work, m.tmux, m.store, m.history

	return func() tea.Msg {
		sessions := make([]worktracker.Session, 0, len(rows))
		for _, r := range rows {
			tracked := worktracker.Session{
				ID:      r.id,
				Dir:     r.dir,
				Text:    workText(driver, stor, history, r.id, r.launch, r.live),
				Live:    r.live,
				Visible: r.visible,
			}
			tracker.Discover(tracked)
			sessions = append(sessions, tracked)
		}
		tracker.Refresh(sessions)
		return workLoadedMsg{}
	}
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
func workText(driver *tmux.Driver, stor *store.Store, history *search.Index, id, launch string, live bool) string {
	text := launch
	if live && driver != nil {
		if pane, err := driver.CapturePane(id); err == nil {
			text += "\n" + ansi.Strip(pane)
		}
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
	prs, tickets, needsYou := 0, 0, false
	for _, row := range rows {
		if row.kind == "TICKET" {
			tickets++
		} else {
			prs++
		}
		needsYou = needsYou || row.needsYou
	}
	mark := ""
	if needsYou {
		mark = lipgloss.NewStyle().Foreground(colorErrored).Render(statusGlyph(status.Waiting)) + " "
	}
	for _, rung := range countLadder {
		text := countText(prs, tickets, rung.sep, rung.form)
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
	prUnit     = countUnit{"p", "pr", "pr", "prs"}
	ticketUnit = countUnit{"i", "is", "issue", "issues"}
)

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

// countText joins the two counts. A kind the session is not on is left out
// rather than written as a zero: "3 issues" is the whole truth about a
// session on no pull requests, and "0 prs · 3 issues" spends four cells
// saying nothing.
func countText(prs, tickets int, sep string, form countForm) string {
	switch {
	case prs > 0 && tickets > 0:
		return prUnit.text(prs, form) + sep + ticketUnit.text(tickets, form)
	case prs > 0:
		return prUnit.text(prs, form)
	case tickets > 0:
		return ticketUnit.text(tickets, form)
	}
	return ""
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

// workRow is one artifact a session is on: a pull request, or a ticket.
type workRow struct {
	needsYou bool
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
				needsYou: pr.NeedsYou(), key: key, kind: "PR",
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
				key: key, kind: "TICKET", label: ticket.Identifier, short: ticket.Identifier,
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

// sortWorkRows puts pull requests above tickets, then whatever wants a person
// first. A pull request is the thing the operator acts on and a ticket is the
// trail it leaves, so the kind outranks the blocker; only a pull request can
// be blocked on a person anyway.
func sortWorkRows(rows []workRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		if iPR, jPR := rows[i].kind == "PR", rows[j].kind == "PR"; iPR != jPR {
			return iPR
		}
		return rows[i].needsYou && !rows[j].needsYou
	})
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
		glyph:     checksGlyph(forge.ChecksPending),
		stateHint: colorIdle,
	}
	if ref.Kind == workspec.KindTicket {
		row.kind, row.label, row.short = "TICKET", ref.Identifier, ref.Identifier
		return row
	}
	row.kind = "PR"
	row.label = ref.Repo + "#" + strconv.Itoa(ref.Number)
	row.short = "#" + strconv.Itoa(ref.Number)
	// A pull request's address is its repository and number, both of which the
	// reference already holds, so the row is clickable before GitHub has
	// answered for it. A ticket's address needs the workspace slug, which only
	// Linear knows.
	row.url = "https://github.com/" + ref.Repo + "/pull/" + strconv.Itoa(ref.Number)
	return row
}

// prGlyph is a pull request reduced to the one mark a folded session shows
// for it. The shapes are the list's own status marks rather than a second
// vocabulary: what is blocked on a person wears the same ◆ a waiting session
// does, so a reader who knows the list can already read this line. Shape
// carries the state and colour reinforces it, which is what keeps the row
// that wants you separable from the four that do not on a terminal rendering
// no colour at all.
func prGlyph(pr forge.PR) string {
	switch {
	case pr.NeedsYou():
		return statusGlyph(status.Waiting)
	case pr.State == forge.PRMerged:
		return statusGlyph(status.Finished)
	case pr.State == forge.PRDraft:
		return statusGlyph(status.Starting)
	case pr.State == forge.PRClosed:
		return statusGlyph(status.Idle)
	case pr.Checks == forge.ChecksPending:
		return checksGlyph(forge.ChecksPending)
	default:
		return statusGlyph(status.Working)
	}
}

// ticketGlyph is the ticket half of the same vocabulary, read off Linear's
// own category rather than the per-team state name.
func ticketGlyph(ticket forge.Ticket) string {
	switch {
	case ticket.Done():
		return statusGlyph(status.Finished)
	case ticket.StateType == "started":
		return statusGlyph(status.Working)
	default:
		return statusGlyph(status.Idle)
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
	if sessID == "" || m.work == nil {
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
func (m *Model) railWorkExpanded(sessID string) bool {
	if folded, decided := m.workFoldDecision(sessID); decided {
		return !folded
	}
	return sessID != "" && sessID == m.railCursorSess
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
