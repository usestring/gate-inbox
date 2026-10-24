package ui

// The review queue: pull requests someone asked the operator to review on
// GitHub, fetched on the ingest key and drawn as one Reviews row at the top of
// the list.
//
// The row is not a session, a group or an extension's header, so every key
// that acts on a session refuses it the way an artifact row does. Opening it
// shows its page where a session's pane is drawn, through the same head-pane
// path an extension's header page takes, and the page's keys are the board's
// own rather than an extension's.

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/forge"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// reviewsPaneID is the review page's view id. Extension handles count up
// from one, so a negative id can never be mistaken for one of theirs.
const reviewsPaneID = -1

// reviewsMsg carries the open asks back to the loop, after a load or an
// ingest. ingested marks the ingest key's answer, which reports what it
// found; a plain load reports nothing unless it failed.
type reviewsMsg struct {
	open     []store.ReviewRequest
	err      error
	ingested bool
	added    int
	found    int
	health   forge.Health
	ackErr   error
}

// loadReviews reads the open asks off the loop.
func (m *Model) loadReviews() tea.Cmd {
	st := m.store
	if st == nil {
		return nil
	}
	return func() tea.Msg {
		open, err := st.OpenReviewRequests()
		return reviewsMsg{open: open, err: err}
	}
}

// ingestReviews asks GitHub for the reviews asked of the operator, stores the
// new asks and acknowledges them. Everything it reads from the model is read
// here, on the loop; the command itself touches only what it was handed.
func (m *Model) ingestReviews() tea.Cmd {
	cfg := m.cfg.Integrations.Reviews
	if !cfg.On() {
		m.errBar.text = "the review queue is switched off: [integrations.reviews] enabled = false"
		return nil
	}
	if m.reviewsFetching {
		return nil
	}
	m.reviewsFetching = true
	m.reportDone("fetching reviews from GitHub…")
	st := m.store
	search := forge.ReviewSearch{
		Label:      cfg.LabelOrDefault(),
		Command:    cfg.CommandOrDefault(),
		Orgs:       cfg.Orgs,
		Commenters: cfg.Commenters,
	}
	for _, trigger := range cfg.TriggerList() {
		search.Triggers = append(search.Triggers, forge.Trigger(trigger))
	}
	ack := cfg.Acks()
	return func() tea.Msg {
		gh := forge.NewGitHub()
		found, health := gh.ReviewRequests(search)
		msg := reviewsMsg{ingested: true, found: len(found), health: health}
		if !health.OK {
			msg.open, msg.err = st.OpenReviewRequests()
			return msg
		}
		rows := make([]store.ReviewRequest, 0, len(found))
		subjects := map[string]string{}
		for _, r := range found {
			rows = append(rows, storedReview(r))
			subjects[r.Key()] = r.AckSubject
		}
		added, err := st.UpsertReviewRequests(rows, time.Now())
		if err != nil {
			msg.err = err
			return msg
		}
		msg.added = len(added)
		if ack && len(added) > 0 {
			acks := make([]string, 0, len(added))
			for _, r := range added {
				acks = append(acks, subjects[reviewKey(r)])
			}
			msg.ackErr = gh.Acknowledge(acks)
		}
		msg.open, msg.err = st.OpenReviewRequests()
		return msg
	}
}

func storedReview(r forge.ReviewRequest) store.ReviewRequest {
	return store.ReviewRequest{
		Repo: r.Repo, Number: r.Number, Trigger: string(r.Trigger), TriggerID: r.TriggerID,
		Title: r.Title, Author: r.Author, URL: r.URL, HeadSHA: r.HeadSHA, Draft: r.Draft,
		Additions: r.Additions, Deletions: r.Deletions, ChangedFiles: r.ChangedFiles,
		Checks: string(r.Checks), TriggeredAt: r.TriggeredAt,
	}
}

// reviewKey is forge.ReviewRequest.Key for a stored ask.
func reviewKey(r store.ReviewRequest) string {
	return fmt.Sprintf("%s#%d/%s/%s", r.Repo, r.Number, r.Trigger, r.TriggerID)
}

// finishReviews takes in what a load or an ingest found.
func (m *Model) finishReviews(msg reviewsMsg) (tea.Model, tea.Cmd) {
	if msg.ingested {
		m.reviewsFetching = false
	}
	if msg.err == nil {
		m.reviews = msg.open
	}
	switch {
	case msg.ingested && !msg.health.OK:
		reason := msg.health.Reason
		if reason == "" {
			reason = "GitHub did not answer"
		}
		m.errBar.text = "reviews not fetched: " + reason
	case msg.err != nil:
		m.errBar.text = "review queue: " + msg.err.Error()
	case msg.ingested && msg.ackErr != nil:
		m.errBar.text = fmt.Sprintf("%s queued, but the eyes reaction did not land: %v", countOf(msg.added, "new review"), msg.ackErr)
	case msg.ingested:
		m.reportDone(fmt.Sprintf("%s · %d already queued", countOf(msg.added, "new review"), msg.found-msg.added))
	}
	m.rebuildRows()
	return m, nil
}

func countOf(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// withReviewsRow puts the Reviews row at the top of a tree, under root's
// rollup when there is one. A search is answered by the sessions it matched,
// so the row stays off it.
func (m *Model) withReviewsRow(rows []treeRow, query string) []treeRow {
	if len(m.reviews) == 0 || query != "" {
		return rows
	}
	at := 0
	if len(rows) > 0 && rows[0].isRoot() {
		at = 1
	}
	return append(rows[:at:at], append([]treeRow{{reviews: true}}, rows[at:]...)...)
}

// reviewCounts is how many open asks are still waiting, and how many have a
// review session.
func (m *Model) reviewCounts() (pending, started int) {
	for _, r := range m.reviews {
		if r.State == store.ReviewStarted {
			started++
		} else {
			pending++
		}
	}
	return pending, started
}

// renderReviewsEntry draws the Reviews row in the shape a header row has: a
// mark, a name, and the count against the far edge.
func (m *Model) renderReviewsEntry(selected bool, width int, pad, bg string) string {
	nameStyle := mutedStyle
	metaText := subtleText
	if selected {
		nameStyle = selectedLabelStyle
		metaText = mutedText
	}
	pending, started := m.reviewCounts()
	detail := strconv.Itoa(pending) + " pending"
	if started > 0 {
		detail += " · " + strconv.Itoa(started) + " started"
	}
	head := pad + tinted(statusColor(status.Waiting)).Render("◆") + " " + nameStyle.Render("Reviews")
	return paint(rowColumns(head, metaText(detail), width-railGutter), width, bg)
}

// reviewsPane is the page behind the Reviews row, made the first time it is
// asked for and kept while the board runs, so its cursor survives a leave.
func (m *Model) reviewsPane() *openView {
	if m.reviewsPage == nil {
		m.reviewsPage = &openView{id: reviewsPaneID, owner: "reviews", screen: keymap.ContextReviews, view: &reviewsView{m: m}}
	}
	return m.reviewsPage
}

// reviewsView is the review page: every open ask, in the shape GitHub lists
// review requests in, or the one pull request opened from it. See
// reviewpage.go.
type reviewsView struct {
	m      *Model
	cursor int
	page   *prPage
	cache  prCache
}

func (v *reviewsView) Title() string {
	if v.page != nil {
		return v.pageTitle()
	}
	pending, _ := v.m.reviewCounts()
	return "Reviews · " + countOf(pending, "pending request")
}

// selected is the ask under the page's cursor.
func (v *reviewsView) selected() (store.ReviewRequest, bool) {
	if len(v.m.reviews) == 0 {
		return store.ReviewRequest{}, false
	}
	v.cursor = min(max(v.cursor, 0), len(v.m.reviews)-1)
	return v.m.reviews[v.cursor], true
}

// reviewLines is how many lines one ask takes on the page.
const reviewLines = 3

func (v *reviewsView) Render(width, height int) [][]Span {
	if v.page != nil {
		return v.renderPage(width, height)
	}
	if len(v.m.reviews) == 0 {
		return [][]Span{{{Text: "Nothing is waiting for your review.", Tone: ToneMuted}}}
	}
	v.selected()
	first := (max(0, (v.cursor+1)*reviewLines-height) + reviewLines - 1) / reviewLines
	var out [][]Span
	for i := first; i < len(v.m.reviews) && len(out) < height; i++ {
		r := v.m.reviews[i]
		marker := "  "
		if i == v.cursor {
			marker = "❯ "
		}
		out = append(out, []Span{
			{Text: marker, Tone: ToneAccent, Bold: true},
			{Text: r.Repo + "#" + strconv.Itoa(r.Number) + " ", Tone: ToneAccent},
			{Text: r.Title, Bold: i == v.cursor},
		})
		meta := []Span{{Text: "  "}}
		add := func(text string, tone Tone) {
			if len(meta) > 1 {
				meta = append(meta, Span{Text: " · ", Tone: ToneMuted})
			}
			meta = append(meta, Span{Text: text, Tone: tone})
		}
		if r.Author != "" {
			add("by "+r.Author, ToneMuted)
		}
		add(triggerLabel(r.Trigger)+" "+reviewAge(time.Since(r.TriggeredAt)), ToneMuted)
		meta = append(meta, Span{Text: " · ", Tone: ToneMuted},
			Span{Text: "+" + strconv.Itoa(r.Additions), Tone: ToneGood},
			Span{Text: " -" + strconv.Itoa(r.Deletions), Tone: ToneBad})
		add(countOf(r.ChangedFiles, "file"), ToneMuted)
		if glyph := checksGlyph(forge.ChecksState(r.Checks)); glyph != "" {
			add(glyph+" checks", checksTone(forge.ChecksState(r.Checks)))
		}
		if r.Draft {
			add("draft", ToneMuted)
		}
		if r.State == store.ReviewStarted {
			add("review started", ToneWarn)
		}
		out = append(out, meta, nil)
	}
	return out[:min(len(out), height)]
}

func (v *reviewsView) Key(ViewKey) bool { return false }

func checksTone(state forge.ChecksState) Tone {
	switch state {
	case forge.ChecksPassing:
		return ToneGood
	case forge.ChecksFailing:
		return ToneBad
	case forge.ChecksPending:
		return ToneWarn
	}
	return ToneMuted
}

func triggerLabel(trigger string) string {
	switch forge.Trigger(trigger) {
	case forge.TriggerLabel:
		return "labelled"
	case forge.TriggerComment:
		return "asked in a comment"
	}
	return "requested"
}

// reviewAge is how long ago an ask was made, in the one unit that reads.
func reviewAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + "m ago"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + "h ago"
	}
	return strconv.Itoa(int(d/(24*time.Hour))) + "d ago"
}

// reviewsRowAction answers an action pressed while the cursor is on the
// Reviews row: opening it focuses its page, and a key that acts on a session
// is refused rather than aimed at whatever happens to sit below.
func (m *Model) reviewsRowAction(action keymap.Action, bound bool) (tea.Model, tea.Cmd, bool) {
	entry, ok := m.cursorRow()
	if !ok || !entry.reviews {
		return m, nil, false
	}
	switch action {
	case keymap.Open, keymap.StepIn:
		m.focusHeadPane()
		return m, nil, true
	}
	if !bound || !artifactRowActions[action] {
		m.errBar.text = "Reviews is the review queue, not a session — open it to act on a review"
		return m, nil, true
	}
	return m, nil, false
}

// handleReviewsKey answers a key on the focused review page.
func (m *Model) handleReviewsKey(action keymap.Action, bound bool) (tea.Model, tea.Cmd) {
	if !bound {
		return m, nil
	}
	view := m.reviewsPane().view.(*reviewsView)
	switch action {
	case keymap.CursorUp:
		view.cursor = max(view.cursor-1, 0)
		return m, nil
	case keymap.CursorDown:
		view.cursor = min(view.cursor+1, max(len(m.reviews)-1, 0))
		return m, nil
	case keymap.Refresh:
		return m, m.ingestReviews()
	}
	r, ok := view.selected()
	if !ok {
		return m, nil
	}
	switch action {
	case keymap.ReviewOpen:
		return m, openLink(r.URL)
	case keymap.ReviewDismiss:
		m.setReviewState(r, store.ReviewDismissed, "")
		m.reportDone("dismissed " + r.Repo + "#" + strconv.Itoa(r.Number))
		return m, nil
	case keymap.ReviewStart:
		return m.startReview(r)
	case keymap.ReviewView:
		return m, m.openPullRequest(r)
	}
	return m, nil
}

// setReviewState moves every open ask on the pull request, and reloads the
// queue so the row and the page follow.
func (m *Model) setReviewState(r store.ReviewRequest, state, sessionID string) {
	if err := m.store.SetPullRequestReviewState(r.Repo, r.Number, state, sessionID, time.Now()); err != nil {
		m.errBar.text = "review queue: " + err.Error()
		return
	}
	if open, err := m.store.OpenReviewRequests(); err == nil {
		m.reviews = open
	}
	m.rebuildRows()
}

// startReview launches the review session for an ask, or lands on the one
// already running for it.
func (m *Model) startReview(r store.ReviewRequest) (tea.Model, tea.Cmd) {
	for _, ask := range m.reviews {
		if ask.Repo != r.Repo || ask.Number != r.Number || ask.SessionID == "" {
			continue
		}
		for _, sess := range m.sessions {
			if sess.ID == ask.SessionID {
				m.setReviewState(r, store.ReviewStarted, sess.ID)
				m.leaveHeadPane(false)
				m.statusFilter = statusFilterAll
				m.toolFilter = ""
				return m.landInNewSession(sess.ID)
			}
		}
	}
	cfg := m.cfg.Integrations.Reviews
	tool := cfg.Tool
	if tool == "" {
		tool = m.defaultTool()
	}
	if tool == "" {
		m.errBar.text = "no CLIs enabled to review with"
		return m, nil
	}
	dir, ok := resolveExistingDir(cfg.Directory, m.groupDefaultDir(rootGroup))
	if !ok {
		m.errBar.text = "working directory does not exist: " + dir
		return m, nil
	}
	prompt := strings.NewReplacer(
		"{repo}", r.Repo,
		"{number}", strconv.Itoa(r.Number),
		"{head}", r.HeadSHA,
		"{url}", r.URL,
	).Replace(cfg.PromptOrDefault())
	name := "review-" + path.Base(r.Repo) + "-" + strconv.Itoa(r.Number)
	id, err := m.spawnSessionAs(tool, cfg.Model, name, dir, rootGroup, prompt, false, store.SourceUser)
	if err != nil {
		m.reportLaunchError(err)
		return m, nil
	}
	m.setReviewState(r, store.ReviewStarted, id)
	m.leaveHeadPane(false)
	m.statusFilter = statusFilterAll
	m.toolFilter = ""
	m.errBar.text = ""
	return m.landInNewSession(id)
}
