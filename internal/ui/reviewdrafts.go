package ui

// The review being written on a pull request's page: the inline comments the
// review session drafted and the operator's own, each kept, edited or dropped
// in place, and the one review that carries the kept ones to GitHub.

import (
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/forge"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/store"
)

// reviewDraftsMsg carries a pull request's drafts back from the store.
type reviewDraftsMsg struct {
	repo   string
	number int
	drafts []store.ReviewDraft
	err    error
}

// reviewSubmittedMsg is GitHub's answer to a submitted review.
type reviewSubmittedMsg struct {
	review store.ReviewRequest
	event  string
	sent   int
	err    error
}

// loadDrafts reads the open page's drafts off the loop. The review session
// writes them from its own process, so the page reads them again on every
// poll rather than waiting to be told.
func (m *Model) loadDrafts() tea.Cmd {
	page := m.reviewPage()
	st := m.store
	if page == nil || st == nil {
		return nil
	}
	repo, number := page.review.Repo, page.review.Number
	return func() tea.Msg {
		drafts, err := st.ReviewDrafts(repo, number)
		return reviewDraftsMsg{repo: repo, number: number, drafts: drafts, err: err}
	}
}

func (m *Model) finishDrafts(msg reviewDraftsMsg) (tea.Model, tea.Cmd) {
	page := m.reviewPage()
	if page == nil || page.review.Repo != msg.repo || page.review.Number != msg.number {
		return m, nil
	}
	if msg.err != nil {
		m.errBar.text = "review drafts: " + msg.err.Error()
		return m, nil
	}
	page.drafts = msg.drafts
	return m, nil
}

func (m *Model) reviewPage() *prPage {
	if m.reviewsPage == nil {
		return nil
	}
	view, _ := m.reviewsPage.view.(*reviewsView)
	if view == nil {
		return nil
	}
	return view.page
}

// selectedDraft is the draft the draft keys act on.
func (p *prPage) selectedDraft() (store.ReviewDraft, bool) {
	for _, d := range p.drafts {
		if d.ID == p.selected {
			return d, true
		}
	}
	return store.ReviewDraft{}, false
}

// jumpDraft selects the next or the previous draft as the files tab draws
// them, and scrolls to it.
func (v *reviewsView) jumpDraft(forward bool) {
	page := v.page
	detail := v.cache.details[page.key()]
	if detail == nil || len(page.drafts) == 0 {
		return
	}
	page.tab = tabFiles
	at := filesLines(detail, page.drafts, page.selected, page.width).drafts
	order := slices.Collect(maps.Keys(at))
	slices.SortFunc(order, func(a, b int64) int { return at[a] - at[b] })
	i := slices.Index(order, page.selected)
	switch {
	case forward:
		i++
	case i < 0:
		i = len(order) - 1
	default:
		i += len(order) - 1
	}
	page.selected = order[i%len(order)]
	page.scroll[tabFiles] = max(at[page.selected]-2, 0)
}

// markDraft keeps or drops the selected draft.
func (m *Model) markDraft(state string) tea.Cmd {
	page := m.reviewPage()
	d, ok := page.selectedDraft()
	if !ok {
		m.errBar.text = m.pressTo(keymap.ContextReviewPage, keymap.ReviewNextDraft, "select a draft")
		return nil
	}
	if err := m.store.SetReviewDraft(d.ID, state, "", time.Now()); err != nil {
		m.errBar.text = "review drafts: " + err.Error()
		return nil
	}
	return m.loadDrafts()
}

// editDraft opens the selected draft's body for editing.
func (m *Model) editDraft() {
	page := m.reviewPage()
	d, ok := page.selectedDraft()
	if !ok {
		m.errBar.text = m.pressTo(keymap.ContextReviewPage, keymap.ReviewNextDraft, "select a draft")
		return
	}
	page.compose = &draftCompose{draft: d, text: []rune(d.Body)}
}

// commentOnLine opens a new comment on the line the files tab marks: the
// first diff line in view.
func (m *Model) commentOnLine() {
	view := m.reviewsPane().view.(*reviewsView)
	page := view.page
	detail := view.cache.details[page.key()]
	if detail == nil || page.tab != tabFiles {
		m.errBar.text = "comments go on a line of the Files changed tab"
		return
	}
	files := filesLines(detail, page.drafts, page.selected, page.width)
	at, ok := commentLine(files.targets, page.scroll[tabFiles], page.height)
	if !ok {
		m.errBar.text = "no diff line in view to comment on"
		return
	}
	target := files.targets[at]
	target.Repo, target.Number, target.HeadSHA = page.review.Repo, page.review.Number, detail.HeadSHA
	page.compose = &draftCompose{draft: target}
}

// composeReview opens the review's own summary, ahead of submitting it.
func (m *Model) composeReview(event string) {
	view := m.reviewsPane().view.(*reviewsView)
	page := view.page
	if m.reviewsSubmitting[page.review.Repo+"#"+strconv.Itoa(page.review.Number)] {
		m.errBar.text = "a review is already being submitted for this pull request"
		return
	}
	detail := view.cache.details[page.key()]
	if detail == nil {
		m.errBar.text = "the pull request is still loading"
		return
	}
	threads, left := reviewThreads(detail, page.drafts)
	page.compose = &draftCompose{event: event, threads: len(threads), left: left}
}

// commentLine is the first diff line a comment can go on in the rows in view.
func commentLine(targets map[int]store.ReviewDraft, from, rows int) (int, bool) {
	for at := from; at < from+rows; at++ {
		if _, ok := targets[at]; ok {
			return at, true
		}
	}
	return 0, false
}

func markCommentLine(line []Span) []Span {
	out := append([]Span(nil), line...)
	if len(out) > 0 && len(out[0].Text) == diffGutter {
		out[0] = Span{Text: out[0].Text[:diffGutter-2] + "▸ ", Tone: ToneAccent, Bold: true}
	}
	return out
}

// reviewThreads is what a submitted review carries: each kept draft that is
// still on a line of the same head's diff. A stale or off-diff kept draft is
// counted in left rather than sent.
func reviewThreads(detail *forge.PRDetail, drafts []store.ReviewDraft) (threads []forge.DraftThread, left int) {
	patches := map[string][]forge.DiffLine{}
	for _, f := range detail.Files {
		patches[f.Path] = forge.ParsePatch(f.Patch)
	}
	for _, d := range drafts {
		if d.State != store.DraftKept {
			continue
		}
		if d.HeadSHA != detail.HeadSHA {
			left++
			continue
		}
		on := false
		for _, line := range patches[d.Path] {
			if onLine(d.Side, d.Line, line) {
				on = true
				break
			}
		}
		if !on {
			left++
			continue
		}
		threads = append(threads, forge.DraftThread{Path: d.Path, Line: d.Line, Side: d.Side, Body: d.Body})
	}
	return threads, left
}

// draftLines draws a draft under its line, set in from the diff by a bar
// apart from a review thread's: the selected one is marked, and a dropped
// one is muted.
func draftLines(d store.ReviewDraft, selected bool, width int) [][]Span {
	bar := strings.Repeat(" ", diffGutter) + "│ "
	if selected {
		bar = strings.Repeat(" ", diffGutter) + "┃ "
	}
	tone := ToneAccent
	if d.State == store.DraftDropped {
		tone = ToneMuted
	}
	who := "agent"
	if d.Author == store.DraftByOperator {
		who = "you"
	}
	head := []Span{{Text: bar, Tone: tone, Bold: selected}, {Text: "draft · " + who, Bold: true}}
	if selected {
		head = append(head, Span{Text: " · selected", Tone: ToneAccent})
	}
	if d.Kind == "question" {
		head = append(head, Span{Text: " · question", Tone: ToneWarn})
	}
	stateTone := ToneMuted
	if d.State == store.DraftKept {
		stateTone = ToneGood
	}
	head = append(head, Span{Text: " · " + d.State, Tone: stateTone})
	out := [][]Span{head}
	room := width - textfmt.Width(bar)
	if d.Context != "" {
		for _, line := range bodyLines(d.Context, "", room) {
			out = append(out, append([]Span{{Text: bar, Tone: tone, Bold: selected}}, mute(line)...))
		}
	}
	body := bodyLines(d.Body, "", room)
	if d.State == store.DraftDropped {
		for i := range body {
			body[i] = mute(body[i])
		}
	}
	for _, line := range body {
		out = append(out, append([]Span{{Text: bar, Tone: tone, Bold: selected}}, line...))
	}
	return out
}

func mute(line []Span) []Span {
	out := make([]Span, len(line))
	for i, s := range line {
		s.Tone = ToneMuted
		out[i] = s
	}
	return out
}

// draftCompose is text being written on the page: a new comment on a line, a
// draft's new body, or, with event set, the summary of the review about to
// be submitted.
type draftCompose struct {
	draft   store.ReviewDraft
	event   string
	threads int
	left    int
	text    []rune
}

var reviewEventNames = map[string]string{
	forge.ReviewApprove:        "Approve",
	forge.ReviewRequestChanges: "Request changes",
	forge.ReviewComment:        "Comment",
}

func (c *draftCompose) lines(width int) [][]Span {
	var title string
	switch {
	case c.event != "":
		title = reviewEventNames[c.event] + " with " + countOf(c.threads, "inline comment")
		if c.left > 0 {
			title += " (" + countOf(c.left, "kept draft") + " stale or off the diff, left out)"
		}
		title += ". Summary:"
	case c.draft.ID != 0:
		title = "Edit the draft on " + c.draft.Path + ":" + strconv.Itoa(c.draft.Line) + ":"
	default:
		title = "Comment on " + c.draft.Path + ":" + strconv.Itoa(c.draft.Line) + ":"
	}
	out := [][]Span{nil, {{Text: title, Tone: ToneAccent, Bold: true}}}
	text := bodyLines(string(c.text)+"▌", "  ", width)
	if len(text) == 0 {
		text = [][]Span{{{Text: "  ▌"}}}
	}
	out = append(out, text...)
	verb := "save"
	if c.event != "" {
		verb = "submit to GitHub"
	}
	return append(out, []Span{{Text: "enter " + verb + " · alt+enter new line · esc cancel", Tone: ToneMuted}}, nil)
}

// handleComposeKey types into the open compose box. Enter finishes it.
func (m *Model) handleComposeKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	page := m.reviewPage()
	c := page.compose
	switch msg.String() {
	case "esc":
		page.compose = nil
	case "enter":
		page.compose = nil
		return m, m.finishCompose(page, c)
	case "alt+enter", "shift+enter", "ctrl+j":
		c.text = append(c.text, '\n')
	case "backspace":
		if len(c.text) > 0 {
			c.text = c.text[:len(c.text)-1]
		}
	case "ctrl+u":
		c.text = nil
	default:
		c.text = append(c.text, []rune(cleanText(msg.Key().Text))...)
	}
	return m, nil
}

func (m *Model) finishCompose(page *prPage, c *draftCompose) tea.Cmd {
	body := strings.TrimSpace(string(c.text))
	if c.event != "" {
		return m.submitReview(page, c.event, body)
	}
	if body == "" {
		m.errBar.text = "an empty comment was not saved"
		return nil
	}
	now := time.Now()
	if c.draft.ID != 0 {
		if err := m.store.SetReviewDraft(c.draft.ID, store.DraftKept, body, now); err != nil {
			m.errBar.text = "review drafts: " + err.Error()
			return nil
		}
		return m.loadDrafts()
	}
	d := c.draft
	d.Body, d.Kind, d.Author, d.State = body, "comment", store.DraftByOperator, store.DraftKept
	added, err := m.store.AddReviewDraft(d, now)
	if err != nil {
		m.errBar.text = "review drafts: " + err.Error()
		return nil
	}
	page.selected = added.ID
	return m.loadDrafts()
}

// submitReview posts the review off the loop.
func (m *Model) submitReview(page *prPage, event, body string) tea.Cmd {
	name := page.review.Repo + "#" + strconv.Itoa(page.review.Number)
	if m.reviewsSubmitting[name] {
		m.errBar.text = "a review is already being submitted for this pull request"
		return nil
	}
	detail := m.reviewsPane().view.(*reviewsView).cache.details[page.key()]
	if detail == nil {
		return nil
	}
	threads, _ := reviewThreads(detail, page.drafts)
	if event == forge.ReviewComment && body == "" && len(threads) == 0 {
		m.errBar.text = "nothing to submit: a comment review needs a summary or a kept draft"
		return nil
	}
	if event == forge.ReviewRequestChanges && body == "" && len(threads) == 0 {
		m.errBar.text = "say what to change: a summary or a kept draft"
		return nil
	}
	review, prID, head := page.review, detail.NodeID, detail.HeadSHA
	if m.reviewsSubmitting == nil {
		m.reviewsSubmitting = make(map[string]bool)
	}
	m.reviewsSubmitting[name] = true
	m.reportDone("submitting the review to GitHub…")
	return func() tea.Msg {
		err := forge.NewGitHub().SubmitReview(prID, head, event, body, threads)
		return reviewSubmittedMsg{review: review, event: event, sent: len(threads), err: err}
	}
}

func (m *Model) finishSubmit(msg reviewSubmittedMsg) (tea.Model, tea.Cmd) {
	r := msg.review
	name := r.Repo + "#" + strconv.Itoa(r.Number)
	delete(m.reviewsSubmitting, name)
	if msg.err != nil {
		m.errBar.done = ""
		m.errBar.text = "review not submitted on " + name + ": " + msg.err.Error()
		return m, nil
	}
	if err := m.store.FinishReviewDrafts(r.Repo, r.Number, time.Now()); err != nil {
		m.errBar.text = "review drafts: " + err.Error()
	}
	if page := m.reviewPage(); page != nil && page.review.Repo == r.Repo && page.review.Number == r.Number {
		m.closePullRequest()
	}
	m.setReviewState(r, store.ReviewSubmitted, r.SessionID)
	m.reportDone(strings.ToLower(reviewEventNames[msg.event]) + " submitted on " + name + " with " + countOf(msg.sent, "inline comment"))
	return m, nil
}
