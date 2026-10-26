package ui

// One pull request's page, opened from the review queue: the header GitHub
// draws above a pull request and its three tabs, Conversation, Files changed
// and Checks. The page is drawn by the review queue's own view, which hands
// its body over while a pull request is open, so the queue's cursor is where
// the operator left it when they come back.
//
// The page only ever draws what is cached. Opening it, or the refresh key,
// fetches the pull request off the loop, and the answer lands in the cache
// by repository, number and head commit, so an ask that moved on to a new
// head is fetched again rather than shown stale.

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/forge"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/store"
)

// prTab is one tab of a pull request's page.
type prTab int

const (
	tabConversation prTab = iota
	tabFiles
	tabChecks
	tabCount
)

// prDetailMsg carries a fetched pull request back to the loop.
type prDetailMsg struct {
	key    string
	detail *forge.PRDetail
	health forge.Health
}

// prPage is the pull request the review page has open, and where on it the
// operator is.
type prPage struct {
	review store.ReviewRequest
	tab    prTab
	scroll [tabCount]int
	// height and width are the body's size at the last draw: a page key
	// moves by the height, and a file jump finds each file's header in the
	// lines wrapped at that width.
	height, width int
	// drafts are the review comments not yet submitted, as last read from
	// the store; selected is the one the draft keys act on.
	drafts   []store.ReviewDraft
	selected int64
	compose  *draftCompose
}

func (p *prPage) key() string {
	return prDetailKey(p.review)
}

func prDetailKey(r store.ReviewRequest) string {
	return r.Repo + "#" + strconv.Itoa(r.Number) + "@" + r.HeadSHA
}

// prCache is every pull request fetched while the board runs, and what is
// still on its way.
type prCache struct {
	details  map[string]*forge.PRDetail
	failed   map[string]string
	fetching map[string]bool
}

// openPullRequest opens the page for an ask and fetches the pull request
// unless it is already cached.
func (m *Model) openPullRequest(r store.ReviewRequest) tea.Cmd {
	view := m.reviewsPane().view.(*reviewsView)
	view.page = &prPage{review: r}
	m.setReviewsScreen(keymap.ContextReviewPage)
	return tea.Batch(m.loadDrafts(), m.fetchPullRequest(r, false))
}

// closePullRequest goes back from the page to the queue.
func (m *Model) closePullRequest() {
	m.reviewsPane().view.(*reviewsView).page = nil
	m.setReviewsScreen(keymap.ContextReviews)
}

// setReviewsScreen moves the review page's keys to a screen, on the stored
// page and on the focused copy of it alike.
func (m *Model) setReviewsScreen(screen keymap.Context) {
	m.reviewsPane().screen = screen
	if m.extView.id == reviewsPaneID {
		m.extView.screen = screen
	}
}

// fetchPullRequest asks GitHub for the pull request off the loop. again
// fetches it even when one is cached, for the refresh key.
func (m *Model) fetchPullRequest(r store.ReviewRequest, again bool) tea.Cmd {
	cache := &m.reviewsPane().view.(*reviewsView).cache
	key := prDetailKey(r)
	if cache.fetching[key] || (!again && cache.details[key] != nil) {
		return nil
	}
	if cache.fetching == nil {
		cache.fetching = map[string]bool{}
	}
	cache.fetching[key] = true
	repo, number := r.Repo, r.Number
	return func() tea.Msg {
		detail, health := forge.NewGitHub().PullRequestDetail(repo, number)
		return prDetailMsg{key: key, detail: detail, health: health}
	}
}

// finishPullRequest takes in a fetched pull request. A failed refresh keeps
// the page it had and says why on the error bar.
func (m *Model) finishPullRequest(msg prDetailMsg) (tea.Model, tea.Cmd) {
	cache := &m.reviewsPane().view.(*reviewsView).cache
	delete(cache.fetching, msg.key)
	if !msg.health.OK {
		reason := msg.health.Reason
		if reason == "" {
			reason = "GitHub did not answer"
		}
		if cache.failed == nil {
			cache.failed = map[string]string{}
		}
		cache.failed[msg.key] = reason
		if cache.details[msg.key] != nil {
			m.errBar.text = "pull request not refreshed: " + reason
		}
		return m, nil
	}
	if cache.details == nil {
		cache.details = map[string]*forge.PRDetail{}
	}
	cache.details[msg.key] = msg.detail
	delete(cache.failed, msg.key)
	return m, nil
}

// handleReviewPageKey answers a key on an open pull request's page.
func (m *Model) handleReviewPageKey(action keymap.Action, bound bool) (tea.Model, tea.Cmd) {
	view := m.reviewsPane().view.(*reviewsView)
	page := view.page
	if !bound || page == nil {
		return m, nil
	}
	scroll := &page.scroll[page.tab]
	step := max(page.height-2, 1)
	switch action {
	case ActionClose:
		m.closePullRequest()
	case keymap.ReviewNextTab:
		page.tab = (page.tab + 1) % tabCount
	case keymap.ReviewConversation:
		page.tab = tabConversation
	case keymap.ReviewFiles:
		page.tab = tabFiles
	case keymap.ReviewChecks:
		page.tab = tabChecks
	case keymap.CursorUp:
		*scroll--
	case keymap.CursorDown:
		*scroll++
	case keymap.PageUp:
		*scroll -= step
	case keymap.PageDown:
		*scroll += step
	case keymap.Top:
		*scroll = 0
	case keymap.Bottom:
		*scroll = 1 << 30
	case keymap.ReviewNextFile, keymap.ReviewPrevFile:
		view.jumpFile(action == keymap.ReviewNextFile)
	case keymap.ReviewOpen:
		return m, openLink(page.review.URL + [...]string{"", "/files", "/checks"}[page.tab])
	case keymap.ReviewStart:
		return m.startReview(page.review)
	case keymap.ReviewDismiss:
		r := page.review
		m.closePullRequest()
		m.setReviewState(r, store.ReviewDismissed, "")
		m.reportDone("dismissed " + r.Repo + "#" + strconv.Itoa(r.Number))
	case keymap.Refresh:
		return m, tea.Batch(m.loadDrafts(), m.fetchPullRequest(page.review, true))
	case keymap.ReviewNextDraft, keymap.ReviewPrevDraft:
		view.jumpDraft(action == keymap.ReviewNextDraft)
	case keymap.ReviewKeepDraft:
		return m, m.markDraft(store.DraftKept)
	case keymap.ReviewDropDraft:
		return m, m.markDraft(store.DraftDropped)
	case keymap.ReviewEditDraft:
		m.editDraft()
	case keymap.ReviewComment:
		m.commentOnLine()
	case keymap.ReviewApprove:
		m.composeReview(forge.ReviewApprove)
	case keymap.ReviewRequestChanges:
		m.composeReview(forge.ReviewRequestChanges)
	case keymap.ReviewCommentOnly:
		m.composeReview(forge.ReviewComment)
	}
	return m, nil
}

// jumpFile moves the files tab to the next or the previous file's header.
func (v *reviewsView) jumpFile(forward bool) {
	page := v.page
	detail := v.cache.details[page.key()]
	if detail == nil {
		return
	}
	page.tab = tabFiles
	starts := filesLines(detail, page.drafts, page.selected, page.width).starts
	at := page.scroll[tabFiles]
	if forward {
		for _, start := range starts {
			if start > at {
				page.scroll[tabFiles] = start
				return
			}
		}
		return
	}
	for i := len(starts) - 1; i >= 0; i-- {
		if starts[i] < at {
			page.scroll[tabFiles] = starts[i]
			return
		}
	}
}

func (v *reviewsView) pageTitle() string {
	r := v.page.review
	return "Review · " + r.Repo + "#" + strconv.Itoa(r.Number)
}

// renderPage draws the open pull request: its header, which stays put, and
// the tab below it, which scrolls.
func (v *reviewsView) renderPage(width, height int) [][]Span {
	page := v.page
	key := page.key()
	detail := v.cache.details[key]
	out := prHeader(page.review, detail, page.tab)
	if page.compose != nil {
		out = append(out, page.compose.lines(width)...)
	}
	room := max(height-len(out), 1)
	page.height, page.width = room, width

	var body [][]Span
	var targets map[int]store.ReviewDraft
	switch {
	case detail != nil:
		switch page.tab {
		case tabConversation:
			body = conversationLines(detail, width)
		case tabFiles:
			files := filesLines(detail, page.drafts, page.selected, width)
			body, targets = files.lines, files.targets
		case tabChecks:
			body = checksLines(detail)
		}
	case v.cache.failed[key] != "" && !v.cache.fetching[key]:
		body = [][]Span{
			{{Text: "Could not load the pull request: " + v.cache.failed[key], Tone: ToneBad}},
			{{Text: v.m.pressTo(keymap.ContextReviewPage, keymap.Refresh, "try again"), Tone: ToneMuted}},
		}
	default:
		body = [][]Span{{{Text: "Fetching the pull request from GitHub…", Tone: ToneMuted}}}
	}
	scroll := &page.scroll[page.tab]
	*scroll = min(max(*scroll, 0), max(len(body)-room, 0))
	shown := append([][]Span(nil), body[*scroll:min(len(body), *scroll+room)]...)
	if at, ok := commentLine(targets, *scroll, len(shown)); ok {
		shown[at-*scroll] = markCommentLine(shown[at-*scroll])
	}
	return append(out, shown...)
}

// prHeader is the block GitHub draws above a pull request: its title, who
// wants to merge what into where, its checks and size, and the tabs. Until
// the pull request is fetched it is drawn from the ask.
func prHeader(r store.ReviewRequest, detail *forge.PRDetail, tab prTab) [][]Span {
	title, author, checks := r.Title, r.Author, forge.ChecksState(r.Checks)
	adds, dels, files := r.Additions, r.Deletions, r.ChangedFiles
	draft := r.Draft
	if detail != nil {
		title, author, checks, draft = detail.Title, detail.Author, detail.Checks, detail.Draft
		adds, dels, files = detail.Additions, detail.Deletions, len(detail.Files)
	}
	out := [][]Span{{
		{Text: r.Repo + "#" + strconv.Itoa(r.Number) + " ", Tone: ToneAccent},
		{Text: title, Bold: true},
	}}
	who := []Span{}
	if draft {
		who = append(who, Span{Text: "Draft · ", Tone: ToneMuted})
	}
	if detail != nil {
		who = append(who,
			Span{Text: author, Bold: true},
			Span{Text: " wants to merge " + countOf(detail.Commits, "commit") + " into ", Tone: ToneMuted},
			Span{Text: detail.BaseRef, Tone: ToneAccent},
			Span{Text: " from ", Tone: ToneMuted},
			Span{Text: detail.HeadRef, Tone: ToneAccent})
	} else if author != "" {
		who = append(who, Span{Text: "by ", Tone: ToneMuted}, Span{Text: author, Bold: true})
	}
	out = append(out, who)
	meta := []Span{
		{Text: "+" + strconv.Itoa(adds), Tone: ToneGood},
		{Text: " -" + strconv.Itoa(dels), Tone: ToneBad},
		{Text: " · " + countOf(files, "file"), Tone: ToneMuted},
	}
	if glyph := checksGlyph(checks); glyph != "" {
		meta = append(meta, Span{Text: " · ", Tone: ToneMuted}, Span{Text: glyph + " checks " + string(checks), Tone: checksTone(checks)})
	}
	out = append(out, meta, nil)

	names := [tabCount]string{"Conversation", "Files changed", "Checks"}
	if detail != nil {
		names[tabConversation] += " " + strconv.Itoa(len(detail.Timeline))
		names[tabFiles] += " " + strconv.Itoa(len(detail.Files))
		names[tabChecks] += " " + strconv.Itoa(len(detail.CheckRuns))
	}
	var tabs []Span
	for i, name := range names {
		if prTab(i) == tab {
			tabs = append(tabs, Span{Text: "[" + name + "]", Tone: ToneAccent, Bold: true})
		} else {
			tabs = append(tabs, Span{Text: " " + name + " ", Tone: ToneMuted})
		}
		tabs = append(tabs, Span{Text: "  "})
	}
	return append(out, tabs, nil)
}

// conversationLines is the Conversation tab: the description, then every
// comment and review in the order they were made.
func conversationLines(detail *forge.PRDetail, width int) [][]Span {
	out := [][]Span{{
		{Text: detail.Author, Bold: true},
		{Text: " opened this · " + reviewAge(time.Since(detailOpenedAt(detail))), Tone: ToneMuted},
	}}
	if strings.TrimSpace(detail.Body) == "" {
		out = append(out, []Span{{Text: "  No description provided.", Tone: ToneMuted}})
	} else {
		out = append(out, bodyLines(detail.Body, "  ", width)...)
	}
	for _, c := range detail.Timeline {
		verb, tone := "commented", ToneMuted
		switch c.Review {
		case "APPROVED":
			verb, tone = "approved these changes", ToneGood
		case "CHANGES_REQUESTED":
			verb, tone = "requested changes", ToneBad
		case "COMMENTED", "DISMISSED":
			verb = "reviewed"
		}
		out = append(out, nil, []Span{
			{Text: c.Author, Bold: true},
			{Text: " " + verb, Tone: tone},
			{Text: " · " + reviewAge(time.Since(c.At)), Tone: ToneMuted},
		})
		if strings.TrimSpace(c.Body) != "" {
			out = append(out, bodyLines(c.Body, "  ", width)...)
		}
	}
	if open := openThreads(detail); open > 0 {
		out = append(out, nil, []Span{{Text: countOf(open, "unresolved conversation") + " on the diff: see Files changed", Tone: ToneWarn}})
	}
	return out
}

// detailOpenedAt stands in for when the pull request was opened, which the
// page does not fetch: nothing in its conversation is older.
func detailOpenedAt(detail *forge.PRDetail) time.Time {
	if len(detail.Timeline) > 0 {
		return detail.Timeline[0].At
	}
	return detail.FetchedAt
}

func openThreads(detail *forge.PRDetail) int {
	n := 0
	for _, t := range detail.Threads {
		if !t.Resolved {
			n++
		}
	}
	return n
}

// filesBody is the Files changed tab as drawn: its lines, the line each
// file's header is on, the first line of each draft placed on the diff, and
// the diff line each body line would take a new comment on.
type filesBody struct {
	lines   [][]Span
	starts  []int
	drafts  map[int64]int
	targets map[int]store.ReviewDraft
}

// filesLines is the Files changed tab: each file's header and its diff with
// both line numbers in the gutter, and each review thread and draft drawn
// under the line it is on. A thread or draft whose line is no longer in the
// diff is listed at the end of its file, as GitHub folds an outdated one.
func filesLines(detail *forge.PRDetail, drafts []store.ReviewDraft, selected int64, width int) filesBody {
	body := filesBody{drafts: map[int64]int{}, targets: map[int]store.ReviewDraft{}}
	byPath := map[string][]forge.ReviewThread{}
	for _, t := range detail.Threads {
		byPath[t.Path] = append(byPath[t.Path], t)
	}
	draftsByPath := map[string][]store.ReviewDraft{}
	for _, d := range drafts {
		draftsByPath[d.Path] = append(draftsByPath[d.Path], d)
	}
	if len(detail.Files) == 0 {
		body.lines = [][]Span{{{Text: "No files changed.", Tone: ToneMuted}}}
		return body
	}
	putDraft := func(d store.ReviewDraft) {
		body.drafts[d.ID] = len(body.lines)
		body.lines = append(body.lines, draftLines(d, d.ID == selected, width)...)
	}
	for i, f := range detail.Files {
		if i > 0 {
			body.lines = append(body.lines, nil)
		}
		body.starts = append(body.starts, len(body.lines))
		name := f.Path
		if f.Previous != "" && f.Previous != f.Path {
			name = f.Previous + " → " + f.Path
		}
		body.lines = append(body.lines, []Span{
			{Text: name, Tone: ToneAccent, Bold: true},
			{Text: "  +" + strconv.Itoa(f.Additions), Tone: ToneGood},
			{Text: " -" + strconv.Itoa(f.Deletions), Tone: ToneBad},
			{Text: "  " + f.Status, Tone: ToneMuted},
		})
		threads, fileDrafts := byPath[f.Path], draftsByPath[f.Path]
		delete(draftsByPath, f.Path)
		placed := make([]bool, len(threads))
		placedDraft := make([]bool, len(fileDrafts))
		lines := forge.ParsePatch(f.Patch)
		if len(lines) == 0 {
			body.lines = append(body.lines, []Span{{Text: "  No diff to show: a binary file, or one too large for GitHub to serve.", Tone: ToneMuted}})
		}
		for _, line := range lines {
			if target, ok := lineTarget(f.Path, line); ok {
				body.targets[len(body.lines)] = target
			}
			body.lines = append(body.lines, diffLine(line))
			for j, t := range threads {
				if !placed[j] && threadOn(t, line) {
					placed[j] = true
					body.lines = append(body.lines, threadLines(t, width)...)
				}
			}
			for j, d := range fileDrafts {
				if !placedDraft[j] && onLine(d.Side, d.Line, line) {
					placedDraft[j] = true
					putDraft(d)
				}
			}
		}
		for j, t := range threads {
			if !placed[j] {
				label := "  Outdated conversation"
				if t.Line > 0 {
					label += ", line " + strconv.Itoa(t.Line)
				}
				body.lines = append(body.lines, []Span{{Text: label, Tone: ToneMuted}})
				body.lines = append(body.lines, threadLines(t, width)...)
			}
		}
		for j, d := range fileDrafts {
			if !placedDraft[j] {
				body.lines = append(body.lines, []Span{{Text: "  Not on a line of this diff: line " + strconv.Itoa(d.Line) + ", left out of the review", Tone: ToneMuted}})
				putDraft(d)
			}
		}
	}
	for _, f := range detail.Files {
		for _, d := range draftsByPath[f.Path] {
			putDraft(d)
		}
	}
	for path, rest := range draftsByPath {
		if len(rest) == 0 {
			continue
		}
		body.lines = append(body.lines, nil, []Span{{Text: path + ": not in this pull request, left out of the review", Tone: ToneMuted}})
		for _, d := range rest {
			putDraft(d)
		}
	}
	return body
}

// threadOn reports whether a thread is pinned to a diff line. GitHub numbers
// a thread by the side it is on: the old file's line for a removed line, and
// the new file's for an added or kept one.
func threadOn(t forge.ReviewThread, line forge.DiffLine) bool {
	return !t.Outdated && onLine(t.Side, t.Line, line)
}

func onLine(side string, n int, line forge.DiffLine) bool {
	if n == 0 || line.Kind == forge.DiffHunk || line.Kind == forge.DiffNote {
		return false
	}
	if side == "LEFT" {
		return line.Kind != forge.DiffAdd && line.Old == n
	}
	return line.Kind != forge.DiffDelete && line.New == n
}

// lineTarget is where a comment on a diff line goes: a removed line is
// commented on in the old file, every other line in the new one.
func lineTarget(path string, line forge.DiffLine) (store.ReviewDraft, bool) {
	switch line.Kind {
	case forge.DiffHunk, forge.DiffNote:
		return store.ReviewDraft{}, false
	case forge.DiffDelete:
		return store.ReviewDraft{Path: path, Line: line.Old, Side: "LEFT"}, true
	}
	return store.ReviewDraft{Path: path, Line: line.New, Side: "RIGHT"}, true
}

// diffGutter is the width of the two line-number columns and their gaps.
const diffGutter = 12

func diffLine(line forge.DiffLine) []Span {
	number := func(n int) string {
		if n == 0 {
			return "    "
		}
		return fmt.Sprintf("%4d", n)
	}
	text := strings.ReplaceAll(line.Text, "\t", "    ")
	switch line.Kind {
	case forge.DiffHunk, forge.DiffNote:
		return []Span{{Text: strings.Repeat(" ", diffGutter) + text, Tone: ToneMuted}}
	case forge.DiffAdd:
		return []Span{{Text: number(0) + " " + number(line.New) + "   ", Tone: ToneMuted}, {Text: "+" + text, Tone: ToneGood}}
	case forge.DiffDelete:
		return []Span{{Text: number(line.Old) + " " + number(0) + "   ", Tone: ToneMuted}, {Text: "-" + text, Tone: ToneBad}}
	}
	return []Span{{Text: number(line.Old) + " " + number(line.New) + "   ", Tone: ToneMuted}, {Text: " " + text}}
}

// threadLines draws a review thread under its line, set in from the diff by
// a bar: warm while it is open, muted once it is resolved.
func threadLines(t forge.ReviewThread, width int) [][]Span {
	bar := strings.Repeat(" ", diffGutter) + "┃ "
	tone := ToneWarn
	if t.Resolved {
		tone = ToneMuted
	}
	var out [][]Span
	if t.Resolved {
		out = append(out, []Span{{Text: bar, Tone: tone}, {Text: "resolved", Tone: ToneMuted}})
	}
	for _, c := range t.Comments {
		out = append(out, []Span{
			{Text: bar, Tone: tone},
			{Text: c.Author, Bold: true},
			{Text: " · " + reviewAge(time.Since(c.At)), Tone: ToneMuted},
		})
		for _, line := range bodyLines(c.Body, "", width-textfmt.Width(bar)) {
			out = append(out, append([]Span{{Text: bar, Tone: tone}}, line...))
		}
	}
	return out
}

// checksLines is the Checks tab: each check on the head commit with its
// state and its own verdict, the failing ones first.
func checksLines(detail *forge.PRDetail) [][]Span {
	if len(detail.CheckRuns) == 0 {
		return [][]Span{{{Text: "No checks ran on the head commit.", Tone: ToneMuted}}}
	}
	counts := map[forge.ChecksState]int{}
	for _, c := range detail.CheckRuns {
		counts[c.State]++
	}
	out := [][]Span{{{Text: fmt.Sprintf("%d failing · %d pending · %d passing",
		counts[forge.ChecksFailing], counts[forge.ChecksPending], counts[forge.ChecksPassing]), Tone: ToneMuted}}, nil}
	for _, state := range []forge.ChecksState{forge.ChecksFailing, forge.ChecksPending, forge.ChecksPassing} {
		for _, c := range detail.CheckRuns {
			if c.State != state {
				continue
			}
			out = append(out, []Span{
				{Text: checksGlyph(c.State) + " ", Tone: checksTone(c.State)},
				{Text: c.Name},
				{Text: " · " + strings.ToLower(strings.ReplaceAll(c.Verdict, "_", " ")), Tone: ToneMuted},
			})
		}
	}
	return out
}

// bodyLines wraps markdown as it was written: GitHub's rendering is not
// attempted, but its line breaks are kept.
func bodyLines(body, indent string, width int) [][]Span {
	var out [][]Span
	room := max(width-textfmt.Width(indent), 10)
	for _, para := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		para = strings.ReplaceAll(para, "\t", "    ")
		if strings.TrimSpace(para) == "" {
			out = append(out, nil)
			continue
		}
		for _, line := range textfmt.Wrap(para, room) {
			out = append(out, []Span{{Text: indent + line}})
		}
	}
	return out
}
