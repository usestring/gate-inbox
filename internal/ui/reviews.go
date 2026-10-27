package ui

// The review queue: pull requests someone asked the operator to review on
// GitHub, fetched on the ingest key and listed on a Reviews tab of the rail,
// beside the Sessions tab.
//
// An ask is not a session: it has no pane, no status and no group, and the
// only things to do with one are start a review, open it, or dismiss it. So it
// gets a list of its own rather than a row among the sessions, and a key that
// acts on a session is refused on the tab rather than taught what a pull
// request is.

import (
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/usestring/gate-inbox/extension/textfmt"

	"github.com/usestring/gate-inbox/internal/forge"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

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
	if !m.reviewQueue {
		m.errBar.text = "the review queue is experimental: turn it on in Settings → Experimental features"
		return nil
	}
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
	preparing := ""
	var prepErr error
	if msg.ingested && msg.health.OK && msg.err == nil {
		var prepared, left int
		if prepared, left, prepErr = m.prepareReviews(); prepared > 0 {
			preparing = " · preparing " + countOf(prepared, "briefing")
			if left > 0 {
				preparing += fmt.Sprintf(" (%d more on the next fetch)", left)
			}
		}
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
		m.errBar.text = fmt.Sprintf("%s queued%s, but the eyes reaction did not land: %v", countOf(msg.added, "new review"), preparing, msg.ackErr)
	case msg.ingested:
		m.reportDone(fmt.Sprintf("%s · %d already queued%s", countOf(msg.added, "new review"), msg.found-msg.added, preparing))
	}
	if prepErr != nil {
		m.errBar.text = prepErr.Error()
	}
	m.rebuildRows()
	return m, nil
}

// prepareLimit caps how many review sessions one fetch starts, so a long
// queue fills over a few fetches instead of launching every agent at once.
const prepareLimit = 5

// prepareReviews starts a review session in the background for each pull
// request waiting without one, oldest ask first, so its briefing is written
// before the operator opens it. It reports how many it started and how many
// it left for a later fetch, and stops at the first that fails to start.
func (m *Model) prepareReviews() (prepared, left int, err error) {
	if !m.cfg.Integrations.Reviews.Prepares() {
		return 0, 0, nil
	}
	seen := map[string]bool{}
	for _, r := range m.reviews {
		pr := r.Repo + "#" + strconv.Itoa(r.Number)
		if seen[pr] {
			continue
		}
		seen[pr] = true
		if r.State != store.ReviewPending || m.reviewSession(r) != nil {
			continue
		}
		if prepared == prepareLimit {
			left++
			continue
		}
		id, launchErr := m.launchReview(r)
		if launchErr == nil {
			launchErr = m.store.AttachReviewSession(r.Repo, r.Number, id, time.Now())
		}
		if launchErr != nil {
			err = fmt.Errorf("review briefing for %s not started: %w", pr, launchErr)
			break
		}
		prepared++
	}
	if prepared > 0 {
		if open, err := m.store.OpenReviewRequests(); err == nil {
			m.reviews = open
		}
	}
	return prepared, left, err
}

// reviewSession is the live session recorded for any open ask on the pull
// request, or nil when it has none or the one it had is gone or dead.
func (m *Model) reviewSession(r store.ReviewRequest) *store.Session {
	for _, ask := range m.reviews {
		if ask.Repo != r.Repo || ask.Number != r.Number || ask.SessionID == "" {
			continue
		}
		for i := range m.sessions {
			if m.sessions[i].ID == ask.SessionID && m.sessions[i].Status != status.Dead {
				return &m.sessions[i]
			}
		}
	}
	return nil
}

// reviewSessionLabel says where an ask's review session stands: opened by
// the operator, or prepared ahead of them and still writing its briefing or
// done with it.
func (m *Model) reviewSessionLabel(r store.ReviewRequest) (string, Tone) {
	if r.State == store.ReviewStarted {
		return "review started", ToneWarn
	}
	sess := m.reviewSession(r)
	if sess == nil {
		return "", ToneMuted
	}
	switch sess.Status {
	case status.Starting, status.Working, status.Background, status.Delegating:
		return "preparing briefing", ToneMuted
	}
	return "briefing ready", ToneGood
}

func countOf(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
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

// onReviewsTab is whether the rail lists the review queue rather than the
// sessions.
func (m *Model) onReviewsTab() bool { return m.reviewQueue && m.reviewsTab }

// reviewsPR is the pull request's page opened from the Reviews tab, made the
// first time it is asked for and kept while the board runs, so a pull request
// fetched once is not fetched again on the next visit.
func (m *Model) reviewsPR() *reviewsView {
	if m.reviewsPage == nil {
		m.reviewsPage = &reviewsView{m: m}
	}
	return m.reviewsPage
}

// reviewsView is the one pull request opened from the Reviews tab, and every
// pull request fetched while the board runs. See reviewpage.go.
type reviewsView struct {
	m     *Model
	page  *prPage
	cache prCache
}

// reviewPageOpen is whether the content column shows a pull request's page
// rather than the selected ask.
func (m *Model) reviewPageOpen() bool { return m.onReviewsTab() && m.reviewPage() != nil }

// selectedReview is the ask under the tab's cursor.
func (m *Model) selectedReview() (store.ReviewRequest, bool) {
	if len(m.reviews) == 0 {
		return store.ReviewRequest{}, false
	}
	m.reviewsCursor = min(max(m.reviewsCursor, 0), len(m.reviews)-1)
	return m.reviews[m.reviewsCursor], true
}

// reviewsTabBar heads the rail while the experiment is on, naming the list
// under it and the count waiting on the other one.
func (m *Model) reviewsTabBar(width int) string {
	tab := func(label string, on bool) string {
		if on {
			return selectedLabelStyle.Render(label)
		}
		return mutedStyle.Render(label)
	}
	reviews := "Reviews"
	if pending, _ := m.reviewCounts(); pending > 0 {
		reviews += " (" + strconv.Itoa(pending) + ")"
	}
	bar := spaces(railInset) + tab("Sessions", !m.reviewsTab) + subtleText(" │ ") + tab(reviews, m.reviewsTab)
	hint := ""
	if key := m.tightCap(keymap.ContextList, keymap.ReviewsTab); key != "" {
		hint = subtleText(key + " switch")
	}
	return paint(rowColumns(bar, hint, width-railGutter), width, panelHex())
}

// reviewLines is how many rail lines one ask takes.
const reviewLines = 2

// reviewTabLines is the Reviews tab's list: each ask as its pull request and
// title, then who asked and what it changes.
func (m *Model) reviewTabLines(width, height int) []contentLine {
	if len(m.reviews) == 0 {
		var lines []contentLine
		for _, line := range m.emptyReviewsLines(width, height) {
			lines = append(lines, contentLine{text: line})
		}
		return lines
	}
	m.selectedReview()
	styles := viewStyles()
	inner := max(width-railGutter, 1)
	render := func(spans []Span) string {
		var b strings.Builder
		for _, span := range spans {
			b.WriteString(styles[viewStyleKey{span.Tone, span.Bold}].Render(cleanText(span.Text)))
		}
		return textfmt.TruncateWidth(b.String(), inner, "…")
	}
	first := max(0, (m.reviewsCursor+1)*reviewLines-height+reviewLines-1) / reviewLines
	var lines []contentLine
	for i := first; i < len(m.reviews) && len(lines) < height; i++ {
		bg := panelHex()
		if i == m.reviewsCursor {
			bg = selectedHex()
		}
		head, meta := reviewSpans(m.reviews[i], i == m.reviewsCursor)
		if label, tone := m.reviewSessionLabel(m.reviews[i]); label != "" && m.reviews[i].State != store.ReviewStarted {
			meta = append(meta, Span{Text: " · ", Tone: ToneMuted}, Span{Text: label, Tone: tone})
		}
		lines = append(lines,
			contentLine{text: paint(spaces(railInset)+render(head), width, bg)},
			contentLine{text: paint(spaces(railInset)+render(meta), width, bg)})
	}
	return lines[:min(len(lines), height)]
}

func (m *Model) emptyReviewsLines(width, height int) []string {
	title := centerLine(lipgloss.NewStyle().Bold(true).Foreground(colorBright).Render("nothing to review"), width)
	block := []string{title, "", centerLine(m.keyHint(keymap.ContextReviews, keymap.Refresh, "fetches from GitHub"), width)}
	if height < len(block) {
		return block[:max(height, 0)]
	}
	out := make([]string, height)
	copy(out[(height-len(block))/2:], block)
	return out
}

// reviewSpans is one ask's two lines: the pull request and its title, then
// who asked, when, and what the change weighs.
func reviewSpans(r store.ReviewRequest, selected bool) (head, meta []Span) {
	marker := "  "
	if selected {
		marker = "❯ "
	}
	head = []Span{
		{Text: marker, Tone: ToneAccent, Bold: true},
		{Text: r.Repo + "#" + strconv.Itoa(r.Number) + " ", Tone: ToneAccent},
		{Text: r.Title, Bold: selected},
	}
	meta = []Span{{Text: "  "}}
	add := func(text string, tone Tone) {
		if len(meta) > 1 {
			meta = append(meta, Span{Text: " · ", Tone: ToneMuted})
		}
		meta = append(meta, Span{Text: text, Tone: tone})
	}
	if r.Author != "" {
		add("by "+r.Author, ToneMuted)
	}
	add(reviewAge(time.Since(r.TriggeredAt)), ToneMuted)
	meta = append(meta, Span{Text: " · ", Tone: ToneMuted},
		Span{Text: "+" + strconv.Itoa(r.Additions), Tone: ToneGood},
		Span{Text: " -" + strconv.Itoa(r.Deletions), Tone: ToneBad})
	if glyph := checksGlyph(forge.ChecksState(r.Checks)); glyph != "" {
		add(glyph, checksTone(forge.ChecksState(r.Checks)))
	}
	if r.Draft {
		add("draft", ToneMuted)
	}
	if r.State == store.ReviewStarted {
		add("started", ToneWarn)
	}
	return head, meta
}

// reviewDetailLines is the content column on the Reviews tab: the selected
// ask in full, with the keys that act on it.
func (m *Model) reviewDetailLines(width int) []string {
	r, ok := m.selectedReview()
	if !ok {
		return []string{"", mutedStyle.Render("Nothing is waiting for your review.")}
	}
	styles := viewStyles()
	line := func(text string, tone Tone, bold bool) string {
		return textfmt.TruncateWidth(styles[viewStyleKey{tone, bold}].Render(cleanText(text)), width, "…")
	}
	out := []string{"",
		line(r.Repo+"#"+strconv.Itoa(r.Number), ToneAccent, true),
		textfmt.TruncateWidth(lipgloss.NewStyle().Bold(true).Foreground(colorBright).Render(cleanText(r.Title)), width, "…"),
		"",
	}
	if r.Author != "" {
		out = append(out, line("by "+r.Author, ToneMuted, false))
	}
	out = append(out, line(triggerLabel(r.Trigger)+" "+reviewAge(time.Since(r.TriggeredAt)), ToneMuted, false))
	change := fmt.Sprintf("+%d -%d · %s", r.Additions, r.Deletions, countOf(r.ChangedFiles, "file"))
	if glyph := checksGlyph(forge.ChecksState(r.Checks)); glyph != "" {
		change += " · " + glyph + " checks"
	}
	if r.Draft {
		change += " · draft"
	}
	out = append(out, line(change, ToneMuted, false))
	if label, tone := m.reviewSessionLabel(r); label != "" {
		out = append(out, line(label, tone, false))
	}
	if r.URL != "" {
		out = append(out, "", line(r.URL, ToneMuted, false))
	}
	return out
}

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

// toggleReviewsTab switches the rail between the sessions and the review
// queue. Leaving the queue in a triage walk is how the walk moves past it,
// the way leaving a session does.
func (m *Model) toggleReviewsTab() tea.Cmd {
	if !m.reviewQueue {
		m.errBar.text = "the review queue is experimental: turn it on in Settings → Experimental features"
		return nil
	}
	if !m.reviewsTab {
		m.reviewsTab = true
		m.triageResume = false
		return nil
	}
	return m.leaveReviewsTab(true)
}

// leaveReviewsTab puts the sessions back on the rail, and in a triage walk
// moves on to the next session that needs a person.
func (m *Model) leaveReviewsTab(advance bool) tea.Cmd {
	m.reviewsTab = false
	if !advance || !m.advancesOnLeave() {
		return nil
	}
	m.markReviewsSeen()
	return m.advanceTriage("")
}

// reviewsTabAllows are the list actions the Reviews tab passes through: none
// of them acts on the row under the cursor.
var reviewsTabAllows = map[keymap.Action]bool{
	keymap.Quit: true, keymap.Settings: true, keymap.Help: true,
	keymap.QuickActions: true, keymap.Triage: true, keymap.ReviewsTab: true,
	keymap.IngestReviews: true, keymap.LegendPeek: true,
}

// reviewsTabKey answers a key while the Reviews tab is up. Its own keys come
// first; a list key that acts on a session is refused rather than aimed at
// whatever row the hidden sessions list left the cursor on.
func (m *Model) reviewsTabKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	if !m.onReviewsTab() {
		return m, nil, false
	}
	if m.reviewPageOpen() {
		return m.reviewPageKey(msg)
	}
	if action, bound := m.action(keymap.ContextReviews, msg); bound {
		model, cmd := m.reviewsTabAction(action)
		return model, cmd, true
	}
	action, bound := m.action(keymap.ContextList, msg)
	if !bound {
		return m, nil, true
	}
	return m.reviewsTabListAction(action)
}

// reviewPageKey answers a key while a pull request's page is open on the
// Reviews tab: a summary or comment being written takes every key, then the
// page's own keys, then the list keys the tab passes through.
func (m *Model) reviewPageKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	if m.reviewPage().compose != nil {
		model, cmd := m.handleComposeKey(msg)
		return model, cmd, true
	}
	if action, bound := m.action(keymap.ContextReviewPage, msg); bound {
		model, cmd := m.handleReviewPageKey(action, bound)
		return model, cmd, true
	}
	if action, bound := m.action(keymap.ContextList, msg); bound && reviewsTabAllows[action] {
		return m, nil, false
	}
	return m, nil, true
}

// reviewPageLines is the content column while a pull request's page is open:
// its title, then the page.
func (m *Model) reviewPageLines(width, height int) []string {
	view := m.reviewsPR()
	styles := viewStyles()
	out := []string{"", styles[viewStyleKey{ToneMuted, true}].Render(textfmt.TruncateWidth(cleanText(view.pageTitle()), width, "…")), ""}
	for _, row := range view.renderPage(width, max(height-len(out), 1)) {
		var b strings.Builder
		for _, span := range row {
			b.WriteString(styles[viewStyleKey{span.Tone, span.Bold}].Render(cleanText(span.Text)))
		}
		out = append(out, textfmt.TruncateWidth(b.String(), width, "…"))
	}
	return out
}

// reviewsTabListAction is what a list action does on the Reviews tab: it
// runs if it does not act on a session, and is refused if it does.
func (m *Model) reviewsTabListAction(action keymap.Action) (tea.Model, tea.Cmd, bool) {
	if !m.onReviewsTab() {
		return m, nil, false
	}
	if reviewsTabAllows[action] {
		return m, nil, false
	}
	if model, cmd := m.reviewsTabAction(action); model != nil {
		return model, cmd, true
	}
	m.errBar.text = "that key acts on a session — " + m.tightCap(keymap.ContextList, keymap.ReviewsTab) + " goes back to Sessions"
	return m, nil, true
}

// reviewsTabAction answers one of the Reviews tab's own actions. A nil model
// is an action the tab does not have.
func (m *Model) reviewsTabAction(action keymap.Action) (tea.Model, tea.Cmd) {
	switch action {
	case ActionClose:
		return m, m.leaveReviewsTab(true)
	case keymap.CursorUp:
		m.reviewsCursor = max(m.reviewsCursor-1, 0)
		return m, nil
	case keymap.CursorDown:
		m.reviewsCursor = min(m.reviewsCursor+1, max(len(m.reviews)-1, 0))
		return m, nil
	case keymap.Refresh:
		return m, m.ingestReviews()
	case keymap.ReviewOpen, keymap.ReviewDismiss, keymap.ReviewStart, keymap.ReviewView:
	default:
		return nil, nil
	}
	r, ok := m.selectedReview()
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
	case keymap.ReviewView:
		return m, m.openPullRequest(r)
	}
	return m.startReview(r)
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
	id := ""
	if sess := m.reviewSession(r); sess != nil {
		id = sess.ID
	} else {
		launched, err := m.launchReview(r)
		if err != nil {
			m.reportLaunchError(err)
			return m, nil
		}
		id = launched
		m.errBar.text = ""
	}
	m.setReviewState(r, store.ReviewStarted, id)
	m.reviewsTab = false
	m.statusFilter = statusFilterAll
	m.toolFilter = ""
	return m.landInNewSession(id)
}

// launchReview starts the review session for an ask's pull request without
// landing in it.
func (m *Model) launchReview(r store.ReviewRequest) (string, error) {
	cfg := m.cfg.Integrations.Reviews
	tool := cfg.Tool
	if tool == "" {
		tool = m.defaultTool()
	}
	if tool == "" {
		return "", errors.New("no CLIs enabled to review with")
	}
	dir, ok := resolveExistingDir(cfg.Directory, m.groupDefaultDir(rootGroup))
	if !ok {
		return "", errors.New("working directory does not exist: " + dir)
	}
	prompt := strings.NewReplacer(
		"{repo}", r.Repo,
		"{number}", strconv.Itoa(r.Number),
		"{head}", r.HeadSHA,
		"{url}", r.URL,
	).Replace(cfg.PromptOrDefault())
	name := "review-" + path.Base(r.Repo) + "-" + strconv.Itoa(r.Number)
	return m.spawnSessionAs(tool, cfg.Model, name, dir, rootGroup, prompt, false, store.SourceUser)
}
