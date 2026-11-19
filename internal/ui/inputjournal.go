package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/logging"
)

// The input journal is what answers "the board stopped opening sessions"
// after the fact. On 2026-11-02 a board took no Enter for twenty minutes
// while the list still scrolled, and the info log held nothing between the
// last mode change and the restart: the per-key record is debug, and every
// refusal on the open path writes only to the status bar. Whether keys had
// stopped arriving (the terminal) or arrived and were swallowed (a branch of
// handleKey, a hold, a guard) could not be told apart.
//
// So the board keeps, at every level:
//   - a ring of the last inputs, each with the branch that took it, the mode
//     either side and what the status bar said after -- read by a dump;
//   - one info line whenever an open key leaves the board on the list;
//   - one info line a minute while somebody is at the board, counting keys
//     and mouse events by branch, so a stretch with mouse and no keys shows.

// inputJournalSize is how many inputs the ring keeps: a few minutes of
// someone working the board, which is the window a dump is asked about.
const inputJournalSize = 256

// inputSummaryEvery is the shortest gap between two input summaries.
const inputSummaryEvery = time.Minute

type inputEntry struct {
	at     time.Time
	kind   string // "key" or the mouse message type
	input  string // the key, or the mouse event as Bubble Tea prints it
	branch string
	before mode
	after  mode
	row    string
	status string // the status bar after the input
	took   time.Duration
}

type inputJournal struct {
	ring  [inputJournalSize]inputEntry
	next  int
	total int

	lastKeyAt   time.Time
	lastMouseAt time.Time

	// refusedOpens are the times of recent open keys that left the board on
	// the list; see stuckOpen.
	refusedOpens []time.Time

	// The current summary window.
	windowStart time.Time
	keys        map[string]int // by branch
	mice        int
	recent      []string // the last few keys in the window
}

func (j *inputJournal) add(e inputEntry) {
	j.ring[j.next] = e
	j.next = (j.next + 1) % inputJournalSize
	j.total++
	if e.kind == "key" {
		j.lastKeyAt = e.at
		if j.keys == nil {
			j.keys = map[string]int{}
		}
		j.keys[e.branch]++
		j.recent = append(j.recent, e.input)
		if len(j.recent) > 12 {
			j.recent = j.recent[len(j.recent)-12:]
		}
	} else {
		j.lastMouseAt = e.at
		j.mice++
	}
	if j.windowStart.IsZero() {
		j.windowStart = e.at
	}
}

// entries returns the ring oldest first.
func (j *inputJournal) entries() []inputEntry {
	n := min(j.total, inputJournalSize)
	out := make([]inputEntry, 0, n)
	start := (j.next - n + inputJournalSize) % inputJournalSize
	for i := range n {
		out = append(out, j.ring[(start+i)%inputJournalSize])
	}
	return out
}

// summaryDue reports whether a window has run its minute, and if so returns
// its fields and opens the next one.
func (j *inputJournal) summaryDue(now time.Time) ([]any, bool) {
	if j.windowStart.IsZero() || now.Sub(j.windowStart) < inputSummaryEvery {
		return nil, false
	}
	keys := 0
	branches := make([]string, 0, len(j.keys))
	for branch, n := range j.keys {
		keys += n
		branches = append(branches, fmt.Sprintf("%s=%d", branch, n))
	}
	sort.Strings(branches)
	fields := []any{
		"window", now.Sub(j.windowStart).Round(time.Second).String(),
		"keys", keys,
		"mouse", j.mice,
		"byBranch", strings.Join(branches, ","),
		"recentKeys", strings.Join(j.recent, " "),
	}
	j.windowStart, j.keys, j.mice, j.recent = time.Time{}, nil, 0, nil
	return fields, true
}

// stuckOpenPresses open keys that each leave the board on the list within
// stuckOpenWindow is somebody hammering Enter at a board that will not open:
// one refusal is a hold or a dead pane, three in a row is worth a dump.
const (
	stuckOpenPresses = 3
	stuckOpenWindow  = time.Minute
)

// stuckOpen records a refused open at now and reports whether enough have
// piled up to call the board stuck. It forgets them once it has said so.
func (j *inputJournal) stuckOpen(now time.Time) bool {
	kept := j.refusedOpens[:0]
	for _, at := range j.refusedOpens {
		if now.Sub(at) < stuckOpenWindow {
			kept = append(kept, at)
		}
	}
	j.refusedOpens = append(kept, now)
	if len(j.refusedOpens) < stuckOpenPresses {
		return false
	}
	j.refusedOpens = nil
	return true
}

// inputMeta is what an input is described by before the handler runs.
type inputMeta struct {
	kind   string
	input  string
	branch string
	row    string
}

// inputBefore reads msg as an input worth journaling. Key releases and pastes
// are left out: a release is half of a press already recorded, and a paste is
// text the journal has no business keeping.
func (m *Model) inputBefore(msg tea.Msg) (inputMeta, bool) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return inputMeta{kind: "key", input: msg.String(), branch: m.dispatchBranch(), row: m.journalRow()}, true
	case tea.MouseMsg:
		return inputMeta{kind: strings.TrimPrefix(fmt.Sprintf("%T", msg), "tea."), input: msg.String(),
			branch: m.dispatchBranch(), row: m.journalRow()}, true
	}
	return inputMeta{}, false
}

// journalRow names the cursor's row the way the debug key record does.
func (m *Model) journalRow() string {
	entry, ok := m.cursorRow()
	switch {
	case !ok:
		return "none"
	case entry.isGroup:
		return "group:" + entry.group
	case entry.isArtifact():
		return "artifact:" + entry.art.label
	case entry.isHead():
		return "header:" + entry.sess.ID
	}
	state := entry.sess.Status
	if entry.sess.Archived {
		state += ",archived"
	}
	return "session:" + entry.sess.ID + "(" + state + ")"
}

// journalInput records an input once its handler has run, and writes the
// info lines described at the top of this file.
func (m *Model) journalInput(meta inputMeta, msg tea.Msg, before, after mode, started time.Time) {
	now := time.Now()
	m.inputs.add(inputEntry{
		at: started, kind: meta.kind, input: meta.input, branch: meta.branch,
		before: before, after: after, row: meta.row, status: m.errBar.text, took: now.Sub(started),
	})
	if key, ok := msg.(tea.KeyPressMsg); ok && before == modeList && after == modeList {
		if action, bound := m.action(keymap.ContextList, key); bound &&
			(action == keymap.Open || action == keymap.Attach) && !strings.HasPrefix(meta.row, "group:") {
			logging.Info("open key left the board on the list",
				"key", meta.input, "branch", meta.branch, "row", meta.row,
				"cursor", fmt.Sprintf("%d/%d", m.cursor, len(m.rows)),
				"searching", m.searching, "quick", m.quick.active, "triage", m.triage,
				"openHeld", m.openHeld, "resize", m.split.resizeMode,
				"status", m.errBar.text)
			if m.inputs.stuckOpen(now) && m.autoDump != nil {
				m.autoDump(fmt.Sprintf("an open key left the board on the list %d times within %s",
					stuckOpenPresses, stuckOpenWindow))
			}
		}
	}
	if fields, due := m.inputs.summaryDue(now); due {
		logging.Info("input summary", append(fields, "mode", after.String(), "branch", m.dispatchBranch())...)
	}
}

// DumpRequest asks the board for its input journal and the state that routes
// keys. It is sent from outside the event loop (see app's SIGUSR1 handler),
// and the answer goes back on Reply, which must be buffered: a requester that
// gave up waiting must not block the loop.
type DumpRequest struct {
	Reply chan<- string
}

// LoopPing is the event-loop watchdog's ping (see app's dumper). The loop
// answers on Reply, which must be buffered; a ping that finds the loop busy
// for long enough is the watchdog's evidence that it is wedged.
type LoopPing struct {
	Reply chan<- struct{}
}

// SetAutoDump installs what the board calls when it decides on its own that
// a dump is worth writing. It must not block: it is called on the event loop.
func (m *Model) SetAutoDump(dump func(reason string)) {
	m.autoDump = dump
}

// dumpState renders the journal and the key-routing state as text.
func (m *Model) dumpState() string {
	var b strings.Builder
	now := time.Now()
	ago := func(t time.Time) string {
		if t.IsZero() {
			return "never"
		}
		return now.Sub(t).Round(time.Millisecond).String() + " ago"
	}
	fmt.Fprintf(&b, "== board state at %s\n", now.UTC().Format(time.RFC3339Nano))
	fmt.Fprintf(&b, "mode=%s branch=%s enterFocuses=%v\n", m.mode, m.dispatchBranch(), m.enterFocuses())
	fmt.Fprintf(&b, "searching=%v search=%q quick=%v triage=%v triageScope=%q legendPeek=%v\n",
		m.searching, m.search, m.quick.active, m.triage, m.triageScope, m.legendPeek.visible)
	fmt.Fprintf(&b, "resizeMode=%v dragging=%v openHeld=%q groupJump=%+v keyReleases=%v\n",
		m.split.resizeMode, m.split.dragging, m.openHeld, m.jump, m.keyReleases)
	fmt.Fprintf(&b, "cursor=%d/%d row=%s focusedSession=%q\n", m.cursor, len(m.rows), m.journalRow(), m.pane.forID)
	fmt.Fprintf(&b, "statusBar=%q size=%dx%d\n", m.errBar.text, m.width, m.height)
	fmt.Fprintf(&b, "lastKey=%s lastMouse=%s lastInteraction=%s inputsSeen=%d\n",
		ago(m.inputs.lastKeyAt), ago(m.inputs.lastMouseAt), ago(m.lastInteractionAt), m.inputs.total)
	fmt.Fprintf(&b, "\n== last %d inputs, oldest first\n", min(m.inputs.total, inputJournalSize))
	for _, e := range m.inputs.entries() {
		modes := e.before.String()
		if e.after != e.before {
			modes += "->" + e.after.String()
		}
		fmt.Fprintf(&b, "%s %-18s %-14q branch=%-10s mode=%-14s took=%-8s row=%s",
			e.at.UTC().Format("15:04:05.000"), e.kind, e.input, e.branch, modes,
			e.took.Round(time.Microsecond), e.row)
		if e.status != "" {
			fmt.Fprintf(&b, " status=%q", e.status)
		}
		b.WriteByte('\n')
	}
	return b.String()
}
