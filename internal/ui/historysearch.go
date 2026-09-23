package ui

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/opencode"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/store"
)

// History search is the third thing the filter reads, after a row's own
// metadata and the screen its pane is showing: everything the session has
// said and run, from the transcript its tool keeps. The index lives in
// internal/search; this file is the manager's side of it — which rows to
// index, when to refresh, and how a keystroke asks.
//
// Two goroutines touch the index. The refresher, started beside the poller,
// walks the board's targets under a byte budget and reports when the index
// changed. Queries run as commands, debounced so a burst of keystrokes asks
// once, and tagged with the query they answer so a late answer to an old
// query is dropped rather than shown. Update owns the hits, like everything
// else the rail draws from.

const (
	// historyRefreshInterval is the idle cadence: one stat per target.
	historyRefreshInterval = 3 * time.Second
	// historyRefreshBudget bounds one pass's transcript reading. Parsing
	// real transcripts runs at ~75MB/s per core, so a pass is well under a
	// second and a cold fleet of 160MB is a few passes.
	historyRefreshBudget = 16 << 20
	// historyCatchUpPause is the gap between passes while catching up, so a
	// cold build shares the CPU with the poller instead of taking it.
	historyCatchUpPause = 50 * time.Millisecond
	// historyDebounce is how long a keystroke waits for the next before the
	// query runs. Under one frame at the poll cadence, over any key repeat.
	historyDebounce = 60 * time.Millisecond
	// historyHitLimit caps a query's answer; the board never has this many rows.
	historyHitLimit = 1000
	// HistorySearchEnv turns the index off when set to "off". The filter then
	// reads metadata and pane text alone, as it did before the index.
	HistorySearchEnv = "GATE_INBOX_HISTORY_SEARCH"
)

// historyIndexedMsg says a refresh changed the index: a query on screen is
// worth asking again.
type historyIndexedMsg struct{ progress search.Progress }

// historyDebounceMsg fires after the debounce; seq says which keystroke
// armed it, so an older timer landing after a newer keystroke is ignored.
type historyDebounceMsg struct{ seq int }

// historySearchMsg is one query's answer, best hit first.
type historySearchMsg struct {
	query string
	hits  []search.Hit
	err   error
}

// openHistoryIndex builds the index, or returns nil when it is disabled,
// which the filter treats as no history. The index lives in memory and is
// rebuilt from the transcripts on every start.
func openHistoryIndex() *search.Index {
	if strings.EqualFold(strings.TrimSpace(os.Getenv(HistorySearchEnv)), "off") {
		return nil
	}
	return search.New(search.Options{OpenCodeDB: opencodeDBPath()})
}

// opencodeDBPath is where opencode keeps its database, resolved the way
// opencode itself resolves it (see internal/opencode): GATE_OPENCODE_DB
// wins, else OPENCODE_DB names the file under XDG_DATA_HOME or
// ~/.local/share.
func opencodeDBPath() string {
	return opencode.DBPath()
}

// newHistoryLocator resolves rows to transcripts under the same roots the
// naming index and id capture already read.
func newHistoryLocator() *search.Locator {
	claude := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
	codex := ""
	if home := os.Getenv("CODEX_HOME"); home != "" {
		codex = filepath.Join(home, "sessions")
	}
	if home, err := os.UserHomeDir(); err == nil {
		if claude == "" {
			claude = filepath.Join(home, ".claude")
		}
		if codex == "" {
			codex = filepath.Join(home, ".codex", "sessions")
		}
	}
	return search.NewLocator(claude, codex)
}

// historyToolFormats maps each configured tool name to the transcript format
// the index reads it as. session_store names the format outright for the
// tools that mint their own ids; a tool named for a reader the index has
// ("claude") is that reader whatever wrapper its command runs; otherwise the
// command's binary decides, so an entry named "cc" that runs claude indexes
// as claude. A tool the index has no reader for keeps its own name and takes
// the generic reader.
func historyToolFormats(cfg config.Config) map[string]string {
	readers := map[string]bool{search.ToolClaude: true, search.ToolCodex: true, search.ToolOpenCode: true}
	formats := make(map[string]string, len(cfg.Tools))
	for name, tool := range cfg.Tools {
		format := tool.SessionStore
		if format == "" && readers[name] {
			format = name
		}
		if format == "" {
			if fields := strings.Fields(tool.Command); len(fields) > 0 {
				format = filepath.Base(fields[0])
			}
		}
		if format == "" {
			format = name
		}
		formats[name] = format
	}
	return formats
}

// historyTargets is what the index should hold: one target per listed row
// whose conversation has an id and a transcript the locator can find. Rows
// with no id yet resolve on a later pass, once capture has stamped them.
func (p *poller) historyTargets(sessions []store.Session) []search.Target {
	if p.locator == nil {
		return nil
	}
	targets := make([]search.Target, 0, len(sessions))
	for _, sess := range sessions {
		tool := p.historyFormats[sess.Tool]
		if tool == "" {
			tool = sess.Tool
		}
		if target, ok := p.locator.Target(sess.ID, tool, sess.Cwd, sess.AgentSessionID); ok {
			targets = append(targets, target)
		}
	}
	return targets
}

// setHistoryTargets publishes a pass's targets for the refresher.
func (p *poller) setHistoryTargets(targets []search.Target) {
	p.mu.Lock()
	p.searchTargets = targets
	p.mu.Unlock()
}

func (p *poller) currentHistoryTargets() []search.Target {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.searchTargets
}

// runHistoryIndex refreshes the index until the program exits. Sends run on
// their own goroutine for the same reason the poller's do: the UI stops
// receiving while suspended inside a tmux attach, and an index pass must
// never wait on it.
func (m *Model) runHistoryIndex(send func(tea.Msg)) {
	// Captured once: the goroutine outlives any test that copies the model,
	// and reads nothing from it after this line.
	index, poller := m.history, m.poller
	pending := make(chan tea.Msg, 1)
	go func() {
		for msg := range pending {
			send(msg)
		}
	}()
	ticker := time.NewTicker(historyRefreshInterval)
	defer ticker.Stop()
	var lastErr string
	for {
		targets := poller.currentHistoryTargets()
		if targets == nil {
			// No pass has listed the board yet. An empty target set would
			// read as "every row left" and prune the whole index at startup.
			<-ticker.C
			continue
		}
		progress, err := index.Refresh(targets, historyRefreshBudget)
		if err != nil {
			// One failing file must not flood the log every pass.
			if msg := err.Error(); msg != lastErr {
				lastErr = msg
				logging.Warn("history search: refresh failed", logging.Err(err))
			}
		} else {
			lastErr = ""
		}
		if progress.Changed {
			select {
			case pending <- historyIndexedMsg{progress: progress}:
			default:
			}
		}
		if err == nil && !progress.Done {
			time.Sleep(historyCatchUpPause)
			continue
		}
		<-ticker.C
	}
}

// historyQueryText is the query the index is asked, normalized exactly as
// rebuildRows normalizes the filter so a hit set and the rows it applies to
// are compared byte for byte.
func (m *Model) historyQueryText() string {
	return strings.ToLower(strings.TrimSpace(m.search))
}

// scheduleHistorySearch is the keystroke side: arm the debounce for the
// query now on screen. Hits from an earlier query stay applied until the new
// answer lands, so the list does not flicker empty between keystrokes.
func (m *Model) scheduleHistorySearch() tea.Cmd {
	if m.history == nil {
		return nil
	}
	m.historySeq++
	seq := m.historySeq
	if m.historyQueryText() == "" {
		m.historyHits, m.historyQuery = nil, ""
		return nil
	}
	return tea.Tick(historyDebounce, func(time.Time) tea.Msg { return historyDebounceMsg{seq: seq} })
}

// historySearchCmd asks the index about the query on screen, off the event
// loop. Nil when there is nothing to ask.
func (m *Model) historySearchCmd() tea.Cmd {
	index := m.history
	query := m.historyQueryText()
	if index == nil || query == "" {
		return nil
	}
	return func() tea.Msg {
		hits, ok, err := index.Search(query, historyHitLimit)
		if !ok {
			hits = nil
		}
		return historySearchMsg{query: query, hits: hits, err: err}
	}
}

// applyHistorySearch takes an answer, if it is to the query still on screen.
func (m *Model) applyHistorySearch(msg historySearchMsg) {
	if msg.err != nil {
		logging.Warn("history search: query failed", logging.Err(msg.err), "query", msg.query)
	}
	if msg.query != m.historyQueryText() {
		return
	}
	hits := make(map[string]search.Hit, len(msg.hits))
	for _, hit := range msg.hits {
		hits[hit.Key] = hit
	}
	m.historyHits, m.historyQuery = hits, msg.query
	m.rebuildRows()
}

// historyHit is the row's transcript hit for the query on screen, if the
// index has answered that query.
func (m *Model) historyHit(sess store.Session) (search.Hit, bool) {
	query := m.historyQueryText()
	if query == "" || m.historyQuery != query {
		return search.Hit{}, false
	}
	hit, ok := m.historyHits[sess.ID]
	return hit, ok
}

// matchedInHistory is a row the query found in the session's transcript and
// nowhere the row prints or shows, so the rail can say where the hit was.
func (m *Model) matchedInHistory(sess store.Session) bool {
	if _, ok := m.historyHit(sess); !ok {
		return false
	}
	return !matchesSearch(sess, m.historyQueryText(), m.searchText[sess.ID])
}

// historyHitBadge wears the search field's glyph like the pane badge, names
// the transcript as where the query was found, and says how often when it
// was more than once.
func historyHitBadge(hit search.Hit) string {
	badge := keyStyle.Render("≡") + subtleStyle.Render("hist")
	if hit.Count > 1 {
		badge += subtleStyle.Render("·" + strconv.Itoa(hit.Count))
	}
	return badge
}

// Relevance tiers of a row for the query on screen: the query in what the
// row itself prints comes first, then fuzzy metadata matches, then what its
// pane shows, then its transcript. Fuzzy matches use fzf's score.
// Within the transcript tier the index's score orders rows,
// most and most recent mentions first; ties keep the board's own order. A
// parent carried along only to hold matching children ranks as its best
// child does, so a family stays where its strongest member belongs.
type relevance struct {
	tier  int
	score float64
}

const (
	tierMetadata = iota
	tierFuzzyMetadata
	tierPane
	tierHistory
	tierCarried
)

func (a relevance) better(b relevance) bool {
	if a.tier != b.tier {
		return a.tier < b.tier
	}
	return a.score > b.score
}

func (m *Model) relevanceOf(sess store.Session, query string) relevance {
	if matchesLiteralMetadata(sess, query) {
		return relevance{tierMetadata, 0}
	}
	if score, matched := fuzzyMetadataScore(sess, query); matched {
		return relevance{tierFuzzyMetadata, float64(score)}
	}
	if search.Match(m.searchText[sess.ID], query) {
		return relevance{tierPane, 0}
	}
	if hit, ok := m.historyHit(sess); ok {
		return relevance{tierHistory, hit.Score}
	}
	return relevance{tierCarried, 0}
}

// rankByRelevance orders a query's rows best first, stably, so rows the
// query does not distinguish keep the order the board gave them.
func (m *Model) rankByRelevance(rows []store.Session, children map[string][]store.Session, query string) {
	rank := make(map[string]relevance, len(rows))
	for _, sess := range rows {
		best := m.relevanceOf(sess, query)
		for _, child := range children[sess.ID] {
			if r := m.relevanceOf(child, query); r.better(best) {
				best = r
			}
		}
		rank[sess.ID] = best
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return rank[rows[i].ID].better(rank[rows[j].ID])
	})
}
