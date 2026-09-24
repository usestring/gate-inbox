// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"hash/fnv"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/clipboard"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/git"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/mcpreg"
	"github.com/usestring/gate-inbox/internal/opencode"
	"github.com/usestring/gate-inbox/internal/priority"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/sessname"
	"github.com/usestring/gate-inbox/internal/snippets"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/sysstat"
	"github.com/usestring/gate-inbox/internal/tmux"
	"github.com/usestring/gate-inbox/internal/tracing"
	"github.com/usestring/gate-inbox/internal/worktracker"
)

type mode int

const (
	modeList mode = iota
	modeForm
	modeConfirmDelete
	modeHelp
	modeRename
	modeFork
	modeMigrate
	modeAccount
	modeMove
	modeGroupForm
	modeSettings
	// modeLaunchHint holds a refused spawn's fix in a dialog: the launch
	// stays blocked, and the command that unblocks it is what the user sees.
	modeLaunchHint
	// modeFocus routes the keyboard into the selected session's pane while
	// the list and live preview stay on screen.
	modeFocus
	// modeNameSweep holds the bulk rename sweep: its dry run, its progress,
	// and what it decided about every pane it looked at.
	modeNameSweep
	// modeRestorePrompt holds the startup offer to bring back sessions whose
	// panes are gone: the count, and the picker behind it.
	modeRestorePrompt
	// modeWelcome holds the first-run introduction: what the program is, the
	// keys the workflow is built out of, and the offer of a walkthrough.
	modeWelcome
	// modeTmuxHint holds the startup note about a tmux whose settings drop
	// the links, clipboard writes or clicks the board depends on, and the
	// ~/.tmux.conf line that answers each.
	modeTmuxHint
	// modeAgentPick holds the one question a new session asks: which agent
	// starts here. See agentpick.go.
	modeAgentPick
)

type treeRow struct {
	isGroup        bool
	group          string
	depth          int
	migrationDepth int
	migrationHead  bool
	sess           store.Session
	// art is the pull request or ticket this row draws, and sess is the whole
	// session it hangs off rather than a stand-in assembled for the row. There
	// is therefore no half-populated session anywhere in the tree: a handler
	// that reaches an artifact row and acts on sess acts on the session that
	// opened the artifact, which is the only session the row has ever meant.
	art *workRow
}

// isArtifact marks a row that draws a pull request or a ticket.
func (e treeRow) isArtifact() bool { return e.art != nil }

// isSession marks a row that is a session itself, as against the group above
// it or the work hanging off it.
func (e treeRow) isSession() bool { return !e.isGroup && e.art == nil }

type Model struct {
	cfg    config.Config
	store  *store.Store
	tmux   *tmux.Driver
	hooks  *hooks.Manager
	gitDrv *git.Driver
	engine *status.Engine

	// snips are the operator's canned answers on ctrl+alt keys, read once at
	// startup; snipErr is why there are none, when the file would not be read.
	snips   snippets.Set
	snipErr string

	// keys is the resolved key map: the catalog with the operator's keys.toml
	// applied, read once at startup for the reason snips are. keyProblems is
	// every override that file asked for and did not get, shown in the key
	// map rather than swallowed -- an override that did not take is otherwise
	// a key that does nothing with the explanation in a process nobody sees.
	keys        *keymap.Map
	keyProblems []string

	// setSnapshots writes pane captures before archive or kill takes the
	// windows; a seam so snapshot failures can be exercised without a broken
	// store. A whole batch at a time, because that is one commit on a
	// database every session's tooling is also writing to.
	setSnapshots func(snapshots map[string]string) error

	sessions []store.Session
	rows     []treeRow
	// railWorkColumn is whether anything in rows can be folded open, kept
	// here because railWorkFold is asked per row and the answer is a scan
	// of every row.
	railWorkColumn bool
	// painting is set for the length of one frame, and frameListed and
	// frameAgents are that frame's session sets. The group rollups ask for
	// them once per group row: at eighty-seven sessions over ten groups
	// those two scans were 59% of every byte a frame allocated. Nothing
	// mutates the fleet mid-frame, so a frame answers from one copy and
	// drops it on the way out.
	painting    bool
	frameListed []store.Session
	frameAgents []store.Session
	// visScratch and agentScratch are the backing arrays those two sets are
	// built into while painting. A frame rebuilds both from m.sessions before
	// anything reads them, so the array can be refilled rather than
	// reallocated; at fleet size the two copies were a fifth of a frame's
	// bytes. Only the paint path reuses them, because only there is the
	// lifetime one frame -- listedSessions also answers key handlers, whose
	// callers keep the slice.
	visScratch   []store.Session
	agentScratch []store.Session
	// lastFrame is the frame Bubble Tea was last given, kept so a message
	// that returned without touching anything the frame reads can be
	// answered with it. See frameUnchanged.
	lastFrame   string
	lastFrameAt time.Time
	lastFrameW  int
	lastFrameH  int
	frameReuse  bool
	// keyPaint is the key press whose repaint has not happened yet. The
	// number an operator feels spans two calls -- the handler runs in Update
	// and the result appears in frame -- so the tracer parks the press here
	// and the frame that follows closes it out. Empty unless tracing is on.
	// See rendertrace.go.
	keyPaint keyPaint
	// dispatching is set while a traced handler is running, which is what
	// tells traceStep there is a span waiting to adopt what it measures.
	dispatching bool
	// handlerSteps is what the handler running right now has blocked on,
	// filled by traceStep and taken by the dispatch that ends, so it only
	// ever holds one message's worth. Nil unless tracing is on.
	handlerSteps []handlerStep
	// livePaneMemo is the tmux listing the rebuild in progress reads, held
	// for the length of that rebuild. livePaneHeld says a rebuild is running
	// and livePaneFilled whether the listing has been taken yet -- a rebuild
	// that never asks must not fork one. See holdLivePanes.
	livePaneMemo   map[string]bool
	livePaneHeld   bool
	livePaneFilled bool
	// childSweeping is set while a child sweep is out on a command goroutine,
	// so the poll clock cannot stack a second set of tmux forks behind the
	// set already waiting. See childsweep.go.
	childSweeping bool
	// meterMemo is the machine block at the rail's foot, which reads nothing
	// but the two samples and the width in its key and cost a tenth of every
	// frame to draw for a sample that moves once every two seconds.
	meterMemo    []string
	meterMemoKey meterKey
	meterMemoOK  bool
	// workMemo holds one answer per session for the length of a frame or a
	// rebuild, which is also the longest either treats a session's work as
	// fixed. Nil outside both, which is when the tracker is free to move.
	workMemo map[string][]workRow
	// railCursorSess is the session the tree was built around: the one whose
	// work opens without anybody having asked for it. Held here rather than
	// read off the cursor per row because artifactRows is asked for every
	// session a rebuild walks and the answer is one comparison.
	railCursorSess string

	groups         []string
	groupPaths     map[string]string
	archivedGroups map[string]bool
	priorityGroups map[string]priority.Tier
	snap           sysstat.Snapshot
	proc           sysstat.ProcStat
	procFor        string
	preview        string
	conversation   *conversationView
	agents         agentStats
	// queuedMessages is replaced whole on every refresh rather than merged,
	// so a delivered message's badge clears itself.
	queuedMessages map[string]int

	// archivedChildren counts each parent's archived children, which the
	// list itself cannot see: it drops archived rows, so a fan-out that has
	// been swept up would leave its parent's badge quietly smaller.
	archivedChildren map[string]int
	// searchText is each live session's visible screen, ANSI-stripped and
	// lowercased by the poll that captured it, so the filter can find a
	// session by what it is showing. Replaced whole like queuedMessages: one
	// screen per session and nothing kept from the pass before.
	searchText map[string]string
	// answerableWait marks each child whose wait its parent could answer --
	// an AskUserQuestion the board can read, rather than a permission prompt
	// or a question with no options. Triage reads it to decide whose row a
	// stopped child is; see parentOwns. Replaced whole like searchText, and
	// empty for a session whose pane could not be captured, which leaves the
	// row with the operator where a missing reading belongs.
	answerableWait map[string]bool
	// hookless mirrors refreshMsg.hookless: the rows whose status is
	// pane-derived because nothing is writing their hook file. See
	// hooklessGlyph for what the row does with it.
	hookless map[string]bool
	// history is the full-text index over the board's transcripts, nil when
	// disabled or unavailable. historyHits is the last answer, valid for
	// historyQuery alone; historySeq tags the debounce timer so only the
	// newest keystroke's timer asks.
	history      *search.Index
	historyHits  map[string]search.Hit
	historyQuery string
	historySeq   int

	net netStats

	poller *poller
	// work resolves the pull requests and tickets each session is on. Its
	// reads are concurrency-safe and never fetch, so rows call it while
	// painting; only the refresh runs off the event loop.
	work *worktracker.Tracker
	// clock is the wall clock, so a test can pin an elapsed-hours gauge.
	clock func() time.Time
	// convos is what the agent CLIs recorded about their own conversations,
	// which is where a row's name comes from once one can be attributed to it.
	// Refresh does file and database I/O and only ever runs inside a command.
	convos *convo.Index
	// drift decides when a session's title has stopped describing it.
	drift *sessname.Drift
	// firstPrompts is what each session was started for: the opening user
	// prompts of the conversation attributed to it, by session id, as the
	// naming sweep found them. A session's opening never changes, so an
	// entry here is final once it holds the count the sweep keeps.
	firstPrompts map[string][]string
	// adoptRestored marks the one-time re-registration of adopted panes after
	// the first session load.
	adoptRestored bool
	// nameAfterRefresh asks the next sweep to run a naming pass, for rows an
	// adopt scan has just created and the board has not seen yet.
	nameAfterRefresh bool
	// autoNaming is a naming pass still running. A pass costs a capture per
	// adopted pane, and on a loaded machine it can outlast the ticker that
	// started it; a second one behind it would only queue up more of the same.
	autoNaming bool
	// smartNaming is a single-row naming attempt still running, from r. It is
	// its own flag rather than autoNaming's so a keypress is never blocked by
	// the periodic pass: both the conversation index and the drift tracker are
	// mutex-guarded, so the two may overlap, and making the primary rename key
	// wait on a ticker would be felt as a key that did nothing.
	smartNaming bool
	// sel is the focused-pane selection, written during paint so clicks
	// resolve against the current frame. copied is the size of the last
	// clipboard write, shown once in the status line and cleared on the
	// next selection.
	copied int
	sel    focusSelection
	// forwardingMouse holds an Alt-initiated in-pane click lifecycle until
	// its release. The button and last in-pane cell keep an X10 release
	// paired with its press when it reports MouseButtonNone outside the pane.
	forwardingMouse  bool
	forwardingButton int
	forwardingRow    int
	forwardingCol    int
	// pending is a press in a mouse-tracking pane awaiting its verdict:
	// selection drag or forwarded click.
	pending pendingClick
	pane    paneMirror
	// visible is whether the operator is looking at the manager: its tmux
	// window is the one their client shows, or the terminal reports focus.
	// It gates every window pin -- see visible.go. It starts true so a
	// manager that can never answer the question behaves as it always did.
	visible     bool
	themeDevice string
	// ownPane and ownSocket are the tmux pane the manager is drawing in, from
	// the environment tmux sets inside a pane. Empty when it is not running
	// under tmux at all, which is what makes the visibility read optional.
	ownPane   string
	ownSocket string
	// cursorOn is the caret's blink phase while focused.
	cursorOn bool
	// blinkGen names the one caret timer whose ticks still count. Every
	// entry into focus mode arms a timer, and focusing while already
	// focused -- a triage handover does exactly that on every advance --
	// used to leave the previous one running: two chains toggled the caret
	// twice a period, three toggled it three times, and a drain through a
	// queue of sessions built a caret that flickered instead of blinking.
	// Arming bumps this, so every earlier chain dies on its next tick.
	blinkGen uint64
	// focusScroll is how many lines the focused pane is scrolled back into
	// its history; zero is live at the bottom.
	focusScroll int
	// focusReading is set while a region read is out, focusChasing while an
	// echo chase is. They are what keep a burst of notches from forking a
	// capture each -- see requestFocusRegion and forwardWheel.
	focusReading bool
	focusChasing bool
	// focusCapturing is set while the focused tick's own capture is out, so a
	// tick arriving while the last one is still forked does not stack another
	// on top of it -- see focusCaptureStale.
	focusCapturing bool
	// focusReadingAt is when that read went out, so one that never comes back
	// cannot silence the wheel -- see focusReadStale. focusCapturingAt is the
	// same guarantee for the tick's capture.
	focusReadingAt   time.Time
	focusCapturingAt time.Time
	// focusChasingAt is when the chase went out, so the tick can stand down
	// while it is looking without standing down for good behind one that
	// never returns -- see focusCaptureStale.
	focusChasingAt time.Time
	// echoPending records that input reached the pane while a chase was out,
	// so the frame that chase brings back is not the last word: one more
	// chase follows it, for whatever was typed behind it. See trailingEcho.
	echoPending bool
	// resizeSeq numbers terminal resizes, so the settle timer of a size
	// that has since changed again is recognised and dropped.
	resizeSeq int
	// focusActiveAt is when the focused pane was last typed into, and
	// focusRepaintAt when it last repainted on its own. Together they decide
	// its capture cadence -- see focusecho.go.
	focusActiveAt  time.Time
	focusRepaintAt time.Time
	// focusWheelAt is when the wheel last turned in the pane, which is a
	// third kind of activity: it earns the streaming rate, never the
	// keystroke rate -- see noteFocusWheel.
	focusWheelAt time.Time
	// focusCaptureFor, focusCaptureSec and focusCaptureGeom describe the
	// last capture of the focused pane: which pane it was of, the unix
	// second it was taken in, and the size tmux was holding the pane at.
	// They are what the next tick measures #{window_activity} against
	// before deciding to fork one -- see shouldCapture.
	focusCaptureFor  string
	focusCaptureSec  int64
	focusCaptureGeom [2]int
	// previewAt is when the frame on screen was captured, so a slower
	// capture that lands late cannot paint over a fresher one.
	previewAt time.Time
	// procAt is when the focused view last sampled the process tree behind
	// the pane, which runs far slower than the captures do.
	procAt time.Time
	// focusOnEnter mirrors the persisted focus-key setting; the footer
	// reads it every frame, so it lives here instead of the store.
	focusOnEnter bool
	// comfortableRows mirrors the persisted list density: entries paint
	// their meta on a second line instead of alongside the name. Every
	// rail frame reads it, so it lives here instead of the store.
	comfortableRows bool
	// layout is the persisted override for the tight layout: auto, desktop
	// or mobile. See layout.go.
	layout string
	// layoutShown is the mode the toggle key hid the rail from, so that
	// bringing it back restores what the operator chose rather than auto.
	// It lives for the run, the way chromeShown does.
	layoutShown string
	// palette is the persisted answer to how much of the frame is
	// coloured. The transform itself is package-level; this is the model's
	// copy so the settings panel can round-trip it. See quiet.go.
	palette string
	// glyphs is the persisted mark vocabulary. The set itself lives
	// package-level in currentGlyphs, which every paint reads; this is the
	// model's copy of the choice so the settings panel can round-trip it.
	glyphs string
	// listSort is the persisted order the board's sessions are shown in
	// within each group. See listsort.go.
	listSort string
	// chrome is the persisted override for the legend under the list. See
	// layout.go.
	chrome string
	// chromeShown is the mode the toggle key hid the footer from, so that
	// bringing it back restores what the operator chose rather than auto.
	// It lives for the run: a footer hidden at quit comes back on auto.
	chromeShown string
	// leaveMode is the persisted answer to where ctrl+q goes when it
	// leaves a focused session. See leaveadvance.go.
	leaveMode string
	// newSessionAgent is the persisted answer to which agent n starts, and
	// whether it asks at all. See agentpick.go.
	newSessionAgent string
	// focusedID is the session focus mode is on or was last on, and
	// prevFocusID the one before it: the pair l swaps between. See
	// lastpane.go.
	focusedID         string
	prevFocusID       string
	previewBodyOffset int
	cursor            int
	mode              mode
	showArchived      bool
	// showAllWork lifts the cap on the pull requests and tickets a session
	// hangs on the rail, for a reader who came for the tail. Off at every
	// start: the cap is what keeps one busy session off the whole screen.
	showAllWork bool
	// archiveSweptAt is when the retention window was last checked, so an
	// open manager runs the sweep on its own clock rather than every poll.
	archiveSweptAt  time.Time
	hideEmptyGroups bool
	statusFilter    statusFilter
	collapsed       map[string]bool
	// groupNumbers maps a group to the outline number printed beside it and
	// groupByNumber reads that back, both rebuilt with the rows so a typed
	// number always names the group the rail is showing. jump is the number
	// being typed. See groupnum.go.
	groupNumbers  map[string]string
	groupByNumber map[string]string
	jump          groupJump
	search        string
	searching     bool
	// triage mirrors the persisted status-ordered queue: the rail drops its
	// group structure and sorts by what needs a person, and ctrl+q walks on
	// to the next such session instead of returning to the list.
	triage bool
	// triageScope narrows that queue to one group's subtree -- the group
	// the cursor was in when triage was turned on -- and is "" for a drain
	// of the whole fleet.
	triageScope string
	// autoProceed mirrors the persisted hands-free handover: with it on, the
	// key that answers a focused session in a drain also hands it over. Off
	// by default; see autoproceed.go.
	autoProceed bool
	// gate is the armed drain: triage, hands-free handover and the full
	// width, turned on together and put back together. It lives for the
	// run rather than being persisted; see gate.go.
	gate gateMode
	// muted is the sessions this drain has already been shown, keyed by id.
	// It is what stops the queue handing back work the operator has just
	// done; see mute.go for why it is memory of the pass rather than state
	// on the session.
	muted map[string]muteMark
	// deaf holds the sessions whose panes were found ignoring input, and
	// focusDeaf counts the run of no-repaint keystrokes in the focused one
	// that gets a session there. focusDeafID is which session that run
	// belongs to, so entering a different pane starts its own count rather
	// than inheriting the last one's. See deaf.go.
	deaf        map[string]deafMark
	focusDeaf   int
	focusDeafID string
	// heldAckID is a session entered from the queue on a finished turn whose
	// acknowledgement is being held until the operator leaves it; see
	// focuskeys.go.
	heldAckID string

	form      form
	agentPick agentPick
	groupForm groupForm
	pathSugg  pathComplete
	confirm   confirmTarget
	// archiveConfirm is the persisted answer to whether x asks first, and
	// undo is what the last archive filed away so U can put it back. See
	// archiveundo.go.
	archiveConfirm   string
	undo             archiveUndo
	launchHint       string
	rename           renameTarget
	fork             forkState
	migrate          migrateState
	account          accountState
	quick            quickState
	latestSubmission submissionRescind
	// composerSeq numbers the prompt boxes this run has opened.
	composerSeq int
	settings    settingsState
	help        helpState
	legendPeek  legendPeekState
	nameSweep   nameSweepState
	restore     restorePromptState
	welcome     welcomeState
	tmuxHint    tmuxHintState
	takeover    takeoverState
	// restoreArmed is set by Init, so only a real startup can raise the
	// restore offer; a Model built directly never asks.
	restoreArmed bool
	// restoreAsked marks the startup restore offer as spent, so a fleet the
	// operator dismissed is not offered again on every later refresh.
	restoreAsked bool
	// restoreDecided is the ledger of answered offers, keyed by session id
	// and holding the agent run the answer was about. Loaded from the store
	// on the pass that decides whether to ask, so the answer outlives the run
	// that gave it.
	restoreDecided map[string]time.Time
	// tmuxHintArmed is set by Init, so only a real startup reads the
	// operator's tmux settings and only a real startup can raise the note.
	tmuxHintArmed bool
	// welcomeArmed is set by Init, so only a real startup can raise the
	// first-run introduction; a Model built directly never asks.
	welcomeArmed bool
	moveID       string
	movePath     string
	// editorReturnID is the session an editor request detached from, so the
	// attach it cost can be resumed once the editor is up.
	editorReturnID string

	// awaitedRenames holds what a spawned session launched with, for as long
	// as the agent it carries the rename directive to is still expected to
	// answer. A rename that has not landed by the time this manager run ends
	// is one that is never arriving, so the set is deliberately not persisted.
	awaitedRenames map[string]awaitedRename

	width  int
	height int
	// sessionsSized flips after the first refresh shrinks sessions left
	// over from a previous manager run to the preview panel's width.
	sessionsSized bool
	errBar        errBar
	split         splitState
	// previewGen increments on every cursor move. In-flight captures and
	// settle timers with an older gen are dropped so key-repeat cannot
	// queue a second of tmux work after the user stops.
	previewGen uint64
	// launched is when this run recorded each session it spawned. A poll
	// that listed the store before that has nothing to say about the row.
	launched map[string]time.Time
	// terminalKeyAt is when the last T finished being handled. Held down it
	// autorepeats into a burst of keystrokes, and T is the only key that
	// spawns on the keystroke itself rather than opening a form that would
	// swallow them.
	terminalKeyAt time.Time

	startupPhase     int
	startupAnimating bool

	// version is this build's release tag, shown in settings and carried
	// into the prefilled bug report.
	version string
}

type netStats struct {
	up       uint64
	down     uint64
	rates    bool
	prevSent uint64
	prevRecv uint64
	prevAt   time.Time
	prevOK   bool
}

// paneMirror is the focused pane's state: mouse ownership, appetite for
// pointer moves, report encoding and history depth as the last read of them
// reported, so the wheel routes without a tmux round trip mid-Update.
// box, columnX and cursor are hit-test geometry written during paint; geom
// is the last width×height told to tmux per session id, skipping no-op
// resize-window calls that otherwise stall the UI.
type paneMirror struct {
	// forID is the session the fields below were read from. Focusing a pane
	// does not prove them current on its own: the read that fills them is a
	// command, and it may still be in flight.
	forID   string
	mouse   bool
	motion  bool
	sgr     bool
	history int
	box     paneBox
	columnX int
	cursor  paneCursor
	geom    map[string][2]int
	// resizing is set while a resize pass is out on its own goroutine, so
	// the passes cannot pile up one per poll result.
	resizing bool
}

type errBar struct {
	text  string
	done  string
	shown string
	age   int
}

// worked reports whether the message on the bar is an action that went
// through, so it can be styled as an outcome rather than a failure. Only
// reportDone fills done, and any later write to text alone leaves it
// behind, so a message says it worked or reads as a failure.
func (e errBar) worked() bool { return e.text != "" && e.text == e.done }

// reportDone puts an action that went through on the status bar.
func (m *Model) reportDone(text string) {
	m.errBar.text, m.errBar.done = text, text
}

// splitState is the horizontal sessions/sidebar split. ratio is the left
// panel's share of the terminal width; resizeMode arms keyboard divider
// nudging.
type splitState struct {
	ratio       float64
	ratioBefore float64
	resizeMode  bool
	dragging    bool
}

// confirmTarget.action values. There is no zero value among them on purpose:
// an action left unset is a builder that forgot one, and the dispatch says so
// rather than picking a default that takes something away.
const (
	actionArchive = "archive"
	actionRestore = "restore"
	actionRestart = "restart"
	actionRevive  = "revive"
	actionResume  = "resume"
	// actionTakeover restarts adopted panes as managed sessions; see
	// takeover.go.
	actionTakeover = "takeover"
)

type confirmTarget struct {
	isGroup  bool
	path     string
	label    string
	sessions []store.Session
	action   string
	// ack is the tick a wide answer has to pass before y means anything,
	// empty on every dialog that names what it is about. y/↵ is one
	// keystroke, and one keystroke is the right price for a session the
	// operator picked out and the wrong price for every session on screen.
	// acked is the tick itself; nudged marks a y pressed before it, which is
	// what turns the row into a prompt rather than leaving the key silent.
	ack    string
	acked  bool
	nudged bool
	// keptChildren are the spawned sessions this dialog is offering to leave
	// running, empty on every dialog that has no such choice. A fan-out's
	// children are somebody's work in progress, and taking eight of them
	// down with their parent because the parent was the row under the
	// cursor is the mistake this choice exists to make refusable.
	keptChildren []store.Session
	keepChildren bool
	// fromFocus is the session the dialog was raised from inside its own
	// pane, empty for every dialog raised from the list. Declining one of
	// those is a mis-key taken back rather than a request to leave the
	// agent, so the keys go back into the pane instead of to the list.
	fromFocus string
}

type renameTarget struct {
	sessID    string
	input     textinput.Model
	toolNames []string
	toolIndex int
}

// quickState is the inline prompt bar docked under the preview: active
// across cursor moves, so the target follows the selection. The tool is
// the spawn CLI for group targets, cycled with tab. A pasted image lands
// at the caret as an "[Image #N]" token that renders as a chip and steps,
// deletes, and wraps as one unit; on submit each token becomes its path.
type quickState struct {
	active bool
	composer
	toolNames      []string
	toolIndex      int
	closeAfterSend bool
}

type settingsState struct {
	toolNames       []string
	toolIndex       int
	accountRouting  string
	poolAvailable   bool
	themeIndex      int
	field           int
	quickCloseSend  bool
	enterFocuses    bool
	comfortableRows bool
	layout          string
	palette         string
	glyphs          string
	archiveConfirm  string
	listSort        string
	chrome          string
	leaveMode       string
	newSessionAgent string
	autoProceed     bool
	// backdropSync is the backdrop mode as the picker holds it: true
	// repaints the terminal to the theme, false leaves it alone.
	backdropSync bool
	// cliPicker is the sub-panel for which CLIs appear when creating sessions.
	cliPicker bool
	cliNames  []string
	cliHidden map[string]bool
	cliCursor int
}

const (
	settingsFieldTool = iota
	settingsFieldAccountRouting
	settingsFieldNewSessionAgent
	settingsFieldTheme
	settingsFieldBackdrop
	settingsFieldDensity
	settingsFieldLayout
	settingsFieldPalette
	settingsFieldGlyphs
	settingsFieldArchiveConfirm
	settingsFieldListSort
	settingsFieldChrome
	settingsFieldLeave
	settingsFieldQuickClose
	settingsFieldFocusKey
	settingsFieldAutoProceed
	settingsFieldSnippets
	settingsFieldCLIs
	settingsFieldGuide
	settingsFieldVersion
	settingsFieldCount
)

// agentStats aggregates process-tree usage across all live sessions.
// cpu and ram are shares of this machine (0–100); rss is absolute bytes.
type agentStats struct {
	count int
	cpu   float64
	ram   float64
	rss   uint64
}

type refreshMsg struct {
	sessions []store.Session
	// listedAt is when the pass read that list, which is a whole pass of
	// tmux and ps calls before the UI sees it.
	listedAt       time.Time
	groups         []string
	groupPaths     map[string]string
	archivedGroups map[string]bool
	priorityGroups map[string]priority.Tier
	snap           sysstat.Snapshot
	snapOK         bool
	proc           sysstat.ProcStat
	procFor        string
	preview        string
	// previewAt is when that frame was captured, which is what keeps a pass
	// that took seconds from painting over a frame taken since it started.
	previewAt        time.Time
	agents           agentStats
	queuedMessages   map[string]int
	archivedChildren map[string]int
	searchText       map[string]string
	answerableWait   map[string]bool
	// hookless is every session whose agent no longer carries the hook
	// settings flag, so its status is coming off the pane rather than out of
	// its status file. Replaced whole each pass like queuedMessages, which is
	// what makes the mark lapse the moment a relaunch restores the flag.
	hookless map[string]bool
}

// previewMsg is every pane frame the model receives. There used to be three
// of these -- a mirror push, a settle capture and a tick capture -- and the
// rules for which one won were spread across as many handlers. There is one
// now, and "the newest capture wins" is decided in one place, by at.
type previewMsg struct {
	sessID  string
	preview string
	proc    sysstat.ProcStat
	// procOK marks a frame that actually sampled the process tree. The
	// focused cadence is far too fast to walk /proc on, so most frames carry
	// no stats and must leave the ones on screen alone rather than zero them.
	procOK bool
	// facts is where the caret sits and what the pane's application has
	// claimed; factsOK marks a frame that read them.
	facts   paneFacts
	factsOK bool
	// at is when the capture was taken. Captures run concurrently -- a
	// keystroke chase can overtake a tick already in flight -- and an older
	// frame painting over a newer one is a character that appears and then
	// vanishes.
	at time.Time
	// gen is the previewGen the capture was scheduled for; mismatched
	// gens are discarded so a hold-j burst cannot paint a stale session.
	gen uint64
	// chase marks a frame the keystroke chase went and fetched, as opposed
	// to one a tick brought back, and echoed is that chase's own verdict:
	// the pane moved off the baseline taken before the key, rather than the
	// budget running out. Only a chase can say anything about what a key
	// did, and only the chase holds the frame to measure it against -- the
	// one on screen may predate its baseline. See deaf.go.
	chase  bool
	echoed bool
	// skipped marks a tick that read the pane's activity stamp, found the
	// pane could not have changed since the last capture, and forked
	// nothing. It carries facts and process stats -- both ride the pooled
	// pipe and /proc, neither is a fork -- but no frame.
	skipped bool
}

// previewSettleMsg fires after the cursor has stopped moving so one
// capture runs instead of one per key-repeat tick.
type previewSettleMsg struct {
	gen uint64
}

// previewSettle is how long we wait after the last cursor move before
// talking to tmux. Short enough to feel instant, long enough to collapse
// a held j/k burst into a single capture.
const previewSettle = 16 * time.Millisecond

// The selected session's pane is re-captured on its own timer. The full
// poll is deliberately slow (it lists panes, samples every process tree and
// writes the store), which left the preview refreshing on the poll cadence
// and reading as a still image of a live agent. One capture of one pane is
// cheap, but it is still a tmux exec, so the rate follows the session: an
// agent that is producing output earns a fast cadence, one that is waiting
// on a human does not.
const (
	startupInterval     = 80 * time.Millisecond
	previewIntervalLive = 300 * time.Millisecond
	previewIntervalCalm = 1200 * time.Millisecond
)

// cursorBlinkMsg toggles the focused pane's caret. gen is the timer that
// sent it; a tick from a superseded one is dropped.
type cursorBlinkMsg struct{ gen uint64 }

// cursorBlinkInterval is the caret's half period, matching the rate most
// terminals blink their own.
const cursorBlinkInterval = 530 * time.Millisecond

// cursorBlink re-arms the caret timer. It runs only while a session is
// focused; every other mode lets the timer die. Calling it while a timer is
// already out is safe and is the point of blinkGen: the new chain takes over
// and the old one stops at its next tick, so the caret keeps one period
// however many times focus is entered.
func (m *Model) cursorBlink() tea.Cmd {
	m.blinkGen++
	gen := m.blinkGen
	return tea.Tick(cursorBlinkInterval, func(time.Time) tea.Msg { return cursorBlinkMsg{gen: gen} })
}

// previewTickMsg drives that timer.
type previewTickMsg struct{}

type startupTickMsg struct{}

func (m *Model) hasStartingRow() bool {
	for _, row := range m.rows {
		if row.isSession() && row.sess.Status == status.Starting {
			return true
		}
	}
	return false
}

func (m *Model) previewInterval() time.Duration {
	// A focused pane is one somebody is typing into, and it has its own two
	// rates -- see focusecho.go. The list cadences below are for a pane
	// under the cursor, which is being glanced at rather than worked in.
	if m.mode == modeFocus {
		if m.focusActive() {
			return focusIntervalActive
		}
		// A pane writing its own output is being read rather than typed into,
		// so it is sampled at a rate a person can follow instead of at the
		// rate a keystroke needs. A pane the wheel is turning is the same
		// case: scrolling is reading, and its frames are the pane's to
		// produce, not a keystroke's to wait on.
		if m.focusStreaming() || m.focusWheeling() {
			return focusIntervalStream
		}
		return focusIntervalIdle
	}
	// A notch just forwarded into the pane is somebody working in it, whatever
	// the row's status says: the frames it provokes must not wait out the
	// cadence of a session that looks idle from the outside.
	if m.focusActive() || m.focusWheeling() {
		return previewIntervalLive
	}
	if sess, ok := m.selected(); ok {
		switch sess.Status {
		case status.Working, status.Starting:
			return previewIntervalLive
		}
	}
	return previewIntervalCalm
}

func (m *Model) previewTick() tea.Cmd {
	return tea.Tick(m.previewInterval(), func(time.Time) tea.Msg { return previewTickMsg{} })
}

func (m *Model) needsLoaderTick() bool {
	return m.hasStartingRow()
}

func (m *Model) startStartupTick() tea.Cmd {
	if m.startupAnimating || !m.needsLoaderTick() {
		return nil
	}
	m.startupAnimating = true
	return func() tea.Msg { return startupTickMsg{} }
}

func (m *Model) startupTick() tea.Cmd {
	return tea.Tick(startupInterval, func(time.Time) tea.Msg { return startupTickMsg{} })
}

type errMsg struct{ err error }

// deferStoreWrite runs a store write off the Bubble Tea event loop.
//
// The store keeps one connection, and a poll pass occupies it for as long as
// its own reads and writes take -- seconds, on a board this size. A write
// issued from a key handler waits behind that, and the operator feels it as a
// key that did nothing: pressing "i" cost most of a second.
//
// Only an acknowledgement or a preference belongs here. The model has already
// changed and the frame is painted from it, so the database is merely catching
// up, and the cost of losing the write is a setting that does not survive a
// restart. The failure still reaches the error bar, one beat late, because
// errMsg is the only way a closure may report one: writing m.errBar from a
// command is a data race.
// afterStoreWrite runs a write and then the command that re-reads what it
// changed, in that order.
//
// tea.Batch runs its commands concurrently, so a write batched with a reload
// is a race the reload usually loses: the screen re-reads the file before the
// write reaches it and paints the old value, which makes a setting that did
// change look like one that did not. Anything that writes and then shows the
// result has to sequence them.
func afterStoreWrite(write func() error, then tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		if err := write(); err != nil {
			return errMsg{err}
		}
		if then == nil {
			return nil
		}
		return then()
	}
}

func deferStoreWrite(write func() error) tea.Cmd {
	return func() tea.Msg {
		if err := write(); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

type attachDoneMsg struct {
	sessID string
	err    error
}

func New(cfg config.Config, st *store.Store, driver *tmux.Driver, engine *status.Engine, hookManager *hooks.Manager, version string) *Model {
	statusSources := make(map[string]string, len(cfg.Tools))
	sessionStores := make(map[string]string, len(cfg.Tools))
	mcpStyles := make(map[string]string, len(cfg.Tools))
	shellTools := make(map[string]bool, len(cfg.Tools))
	interruptKeys := make(map[string][]string, len(cfg.Tools))
	for name, tool := range cfg.Tools {
		statusSources[name] = tool.StatusSource
		sessionStores[name] = tool.SessionStore
		mcpStyles[name] = mcpreg.Style(name, tool.MCP)
		shellTools[name] = tool.Shell
		interruptKeys[name] = tool.InterruptKeys
	}
	// A missing git binary only disables what reads a repository's root;
	// everything else works without it, so the error surfaces on first use.
	gitDriver, _ := git.New()
	// Set before the theme so the first paint is already narrowed, rather
	// than the board flashing its full palette on the way up.
	quietPalette = storedPalette(st) == paletteQuiet
	ResolveBackdrop()
	backdropMode = storedBackdrop(st)
	applyTheme(themes[themeIndex(storedTheme(st))])
	// The mark set is package-level like the palette, so it is put in
	// force here rather than read off the model on every paint.
	applyGlyphSet(storedGlyphs(st))
	model := &Model{
		conversation:    &conversationView{locator: newHistoryLocator()},
		cfg:             cfg,
		store:           st,
		tmux:            driver,
		hooks:           hookManager,
		gitDrv:          gitDriver,
		engine:          engine,
		setSnapshots:    st.SetSnapshots,
		work:            newWorkTracker(cfg, st),
		convos:          newConvoIndex(),
		drift:           sessname.NewDrift(),
		firstPrompts:    map[string][]string{},
		poller:          newPoller(st, driver, engine, hookManager, gitDriver, statusSources, sessionStores, mcpStyles, shellTools, cfg.PollInterval.Duration),
		collapsed:       loadCollapsed(st),
		split:           splitState{ratio: loadSplitRatio(st)},
		focusOnEnter:    storedFocusOnEnter(st),
		comfortableRows: storedComfortableRows(st),
		layout:          storedLayout(st),
		palette:         storedPalette(st),
		glyphs:          storedGlyphs(st),
		archiveConfirm:  storedArchiveConfirm(st),
		listSort:        storedListSort(st),
		chrome:          storedChrome(st),
		leaveMode:       storedLeaveMode(st),
		newSessionAgent: storedNewSessionAgent(st),
		triage:          storedTriage(st),
		triageScope:     storedTriageScope(st),
		autoProceed:     storedAutoProceed(st),
		mode:            modeList,
		version:         version,
		// Assume the operator is looking until something says otherwise. A
		// manager that cannot answer the question -- not under tmux, and a
		// terminal that never reports focus -- then behaves exactly as it
		// did before any of this, rather than releasing pins nobody asked
		// it to release.
		visible: true,
	}
	model.poller.interruptKeys = interruptKeys
	model.ownPane, model.ownSocket = tmux.OwnPane()
	model.initDeviceTheme()
	model.loadKeys()
	model.loadSnippets()
	model.seedFromStore()
	model.noteOpencodeVersion(opencode.Cached())
	return model
}

// noteOpencodeVersion warns the operator when they are still on OpenCode v1,
// which the board does not support: its sessions launch with v2's flags and
// its store is never read. The warning lands on the status bar -- transient
// like every other notice -- plus a line in the log for the record.
func (m *Model) noteOpencodeVersion(report opencode.Report) {
	logging.Info("opencode detected",
		"binary", report.Version,
		"schema", report.Schema.String(),
		"db", report.Path)
	if notice := report.UpgradeNotice(); notice != "" {
		m.errBar.text = notice
	}
}

// seedFromStore puts the sessions sqlite already knows about on the board
// before the first poll pass runs.
//
// A pass costs a subprocess per session, so with a fleet of adopted panes it
// takes seconds, and the board was empty for every one of them -- the program
// looked broken at exactly the moment somebody opens it. These rows carry the
// status the last run left behind, which the first pass corrects.
//
// Registering the adopted targets here matters as much: without it the first
// pass looks for every adopted pane as gi_<id> on our own socket, which is a
// wasted subprocess each and answers dead for all of them.
func (m *Model) seedFromStore() {
	if m.store == nil {
		return
	}
	// The same argument, for the work column: without this it is blank until every
	// reference on the board has been asked about over the network, which is both the
	// slowest thing the manager does and the largest single spend of its GitHub quota.
	// Restored rows are drawn as remembered, with the time they were taken, and each is
	// due on the first tick.
	if m.work != nil {
		m.work.Memory = m.store
		if err := m.work.Restore(); err != nil {
			logging.Info("work state restore", logging.Err(err))
		}
	}
	m.dropSelfRow()
	sessions, err := m.poller.listSessions(false)
	if err != nil || len(sessions) == 0 {
		logging.Info("store seeded", "sessions", 0, logging.Err(err))
		return
	}
	m.sessions = sessions
	m.adoptRestored = true
	m.restoreAdopted()
	m.rebuildRows()
	logging.Info("store seeded", "sessions", len(sessions))
}

// storedTheme reads the persisted theme name. A read failure falls back to
// the default theme: the UI still paints, just not in the chosen palette.
func storedTheme(st *store.Store) string {
	name, err := st.Setting(themeSetting)
	if err != nil {
		return ""
	}
	return name
}

// storedBackdrop reads the persisted backdrop mode. Anything but an
// explicit "sync" reads as inherit, so a store that cannot be read, or one
// written before the setting existed, leaves the operator's terminal colors
// alone rather than starting to repaint their window.
func storedBackdrop(st *store.Store) string {
	value, err := st.Setting(backdropSetting)
	if err != nil || value != backdropSync {
		return backdropInherit
	}
	return backdropSync
}

const collapsedSetting = "collapsed_groups"

// loadCollapsed restores the set of folded group paths persisted from a
// previous run so the tree opens in the same shape the user left it.
func loadCollapsed(st *store.Store) map[string]bool {
	collapsed := map[string]bool{}
	raw, err := st.Setting(collapsedSetting)
	if err != nil || raw == "" {
		return collapsed
	}
	var paths []string
	if err := json.Unmarshal([]byte(raw), &paths); err != nil {
		return collapsed
	}
	for _, path := range paths {
		collapsed[path] = true
	}
	return collapsed
}

// persistCollapsed saves the currently folded group paths so the state
// survives across launches.
func (m *Model) persistCollapsed() {
	paths := make([]string, 0, len(m.collapsed))
	for path, folded := range m.collapsed {
		if folded {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	raw, err := json.Marshal(paths)
	if err != nil {
		m.errBar.text = err.Error()
		return
	}
	// Timed apart from the handler around it. This is a write over the single
	// connection the whole program shares, so a fold opened while a poll pass
	// holds that connection waits out the rest of the pass -- which is how an
	// arrow key comes to be the slowest thing on the board for half a second.
	// Whether that is what happened is now a query rather than a guess.
	started := time.Now()
	err = m.store.SetSetting(collapsedSetting, string(raw))
	m.traceStep("ui.settingWrite", started, tracing.Attr{Key: "setting", Value: collapsedSetting})
	if err != nil {
		m.errBar.text = err.Error()
	}
}

// ObserveBoard has every poll pass report to observer. It is set before
// StartPoller, so the first pass is reported too.
func (m *Model) ObserveBoard(observer BoardObserver) {
	m.poller.observer = observer
}

// PinStatuses has every poll pass show a session pins holds at that status,
// and returns what asks for a pass now, for pins to call when one changes so
// the board shows it without waiting for the next tick. It is set before
// StartPoller.
func (m *Model) PinStatuses(pins StatusPins) (refresh func()) {
	m.poller.pins = pins
	return m.poller.requestRefresh
}

// StartPoller launches the background polling loop. It runs outside the
// bubbletea event loop so statuses keep updating while the TUI is
// suspended inside a tmux attach.
func (m *Model) StartPoller(send func(tea.Msg)) {
	m.syncPollInput()
	// The history index starts here rather than in New so a Model built
	// without a poller — every test — never spends a refresher on it.
	if m.history = openHistoryIndex(); m.history != nil {
		m.poller.locator = newHistoryLocator()
		m.poller.historyFormats = historyToolFormats(m.cfg)
		go m.runHistoryIndex(send)
	}
	go m.poller.run(send)
}

func (m *Model) syncPollInput() {
	selectedID := ""
	if sess, ok := m.selected(); ok {
		selectedID = sess.ID
	}
	m.poller.setInput(m.showArchived, selectedID)
}

// requestRefresh publishes the current UI state to the poller and asks
// for an immediate pass.
func (m *Model) requestRefresh() {
	m.syncPollInput()
	m.poller.requestRefresh()
}

func (m *Model) Init() tea.Cmd {
	m.restoreArmed = true
	m.takeover.offer = true
	m.welcomeArmed = true
	m.tmuxHintArmed = true
	// Raised before the first poll rather than after one: the card does not
	// read any session state, and a first run has none to wait for.
	m.maybeOpenWelcome()
	m.syncPollInput()
	return tea.Batch(conversationTick(), m.syncPaneTheme(), m.refreshExistingSessionUX, m.previewTick(), m.startStartupTick(), m.sweepPastes, m.pasteSweepTick(), m.refreshWork(), m.workTick(), m.adoptStart(), m.autoNameTick(), m.checkTmuxConfig)
}

// pasteSweepMsg carries the result of one pass over the pastes directory.
type pasteSweepMsg struct{ err error }

type pasteSweepTickMsg struct{}

// pasteSweepInterval keeps clearing old pasted images while the manager
// stays open. A manager left running for weeks would otherwise sweep once
// at startup and then let screenshots pile up in temp until the next
// restart.
const pasteSweepInterval = 24 * time.Hour

// sweepStalePastes is the seam tests swap for a fake sweep.
var sweepStalePastes = func() error { return clipboard.SweepStale(clipboard.StaleAfter) }

// sweepPastes clears images pasted long enough ago that no agent will open
// them again. It runs off the event loop: the pastes directory lives in
// temp, where a slow disk must not stall a keystroke.
func (m *Model) sweepPastes() tea.Msg {
	return pasteSweepMsg{err: sweepStalePastes()}
}

func (m *Model) pasteSweepTick() tea.Cmd {
	return tea.Tick(pasteSweepInterval, func(time.Time) tea.Msg { return pasteSweepTickMsg{} })
}

// refreshExistingSessionUX re-applies the tmux bindings and status bar to
// sessions that were already running when the manager started, so a session
// created before an update still gets the current key bindings (the
// server-global alt+o editor key) and footer.
func (m *Model) refreshExistingSessionUX() tea.Msg {
	if err := m.tmux.EnsureBindings(); err != nil {
		return errMsg{err}
	}
	sessions, err := m.poller.listSessions(true)
	if err != nil {
		return errMsg{err}
	}
	entries := make([]tmux.ChromeEntry, 0, len(sessions))
	for _, sess := range sessions {
		entries = append(entries, tmux.ChromeEntry{ID: sess.ID, Label: sessionLabel(sess.Group, sess.Name)})
	}
	// Best effort, and in two command lists rather than seven forks a session:
	// this runs before the first frame is true, and on a board of fifty it was
	// hundreds of processes deep. A session that dies mid-pass costs its own
	// batch a retry, not the pass.
	_ = m.tmux.RefreshChromeBatch(entries)
	return nil
}

// visibleSessions filters to the sessions the current view scope shows:
// active ones normally, archived ones in the archived view. It also
// covers the frames between a scope toggle and the next refresh, when
// m.sessions still carries the other scope's list. Status filters apply
// later via listedSessions.
func (m *Model) visibleSessions() []store.Session {
	visible := m.visScratch[:0]
	if !m.painting {
		visible = make([]store.Session, 0, len(m.sessions))
	}
	for _, sess := range m.sessions {
		if sess.Archived == m.showArchived {
			visible = append(visible, sess)
		}
	}
	if m.painting {
		m.visScratch = visible
	}
	return visible
}

// listedSessions is the archived scope narrowed by the status filter.
// Header counts, group rollups, and the tree all share this set so the
// numbers always match what the list can show.
//
// The selected session stays listed when its status leaves the filter
// (finished → idle on enter/ack) so rebuild cannot eject the cursor mid-work.
func (m *Model) listedSessions() []store.Session {
	if m.painting && m.frameListed != nil {
		return m.frameListed
	}
	listed := m.computeListedSessions()
	if m.painting {
		m.frameListed = listed
	}
	return listed
}

func (m *Model) computeListedSessions() []store.Session {
	visible := m.visibleSessions()
	if !m.statusFilter.active() {
		return visible
	}
	heldID := ""
	if sess, ok := m.selected(); ok {
		heldID = sess.ID
	}
	listed := make([]store.Session, 0, len(visible))
	for _, sess := range visible {
		if sess.ID == heldID {
			listed = append(listed, sess)
			continue
		}
		if m.statusFilter.matches(sess.Status) || m.attentionViaChild(sess) {
			listed = append(listed, sess)
		}
	}
	return listed
}

// listedAgents is listedSessions without the shells, for the rollups that
// describe what a group is working on.
func (m *Model) listedAgents() []store.Session {
	if m.painting && m.frameAgents != nil {
		return m.frameAgents
	}
	listed := m.listedSessions()
	agents := m.agentScratch[:0]
	if !m.painting {
		agents = make([]store.Session, 0, len(listed))
	}
	for _, sess := range listed {
		if !m.isShell(sess.Tool) {
			agents = append(agents, sess)
		}
	}
	if m.painting {
		m.agentScratch, m.frameAgents = agents, agents
	}
	return agents
}

func (m *Model) selected() (store.Session, bool) {
	entry, ok := m.selectedRow()
	if !ok || entry.isGroup {
		return store.Session{}, false
	}
	return entry.sess, true
}

// selectedRow is the row an action applies to, which on an artifact row is
// the session it hangs off. Every session-scoped key in the package reads the
// tree through here, so none of them can be aimed at a pull request: what a
// caller gets back is a session row or a group row and never a third thing.
func (m *Model) selectedRow() (treeRow, bool) {
	index, ok := m.selectedIndex()
	if !ok {
		return treeRow{}, false
	}
	return m.rows[index], true
}

// selectedIndex is where that row sits. Artifacts are emitted directly under
// their session, so the nearest row above that is not one is that session.
func (m *Model) selectedIndex() (int, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return 0, false
	}
	index := m.cursor
	for index > 0 && m.rows[index].isArtifact() {
		index--
	}
	return index, true
}

// cursorRow is the row the cursor is literally on, artifact rows included.
// Painting, navigation, folding and opening a link read this; nothing that
// acts on a session does.
func (m *Model) cursorRow() (treeRow, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return treeRow{}, false
	}
	return m.rows[m.cursor], true
}

// focusSession puts the cursor on a session's row, for the keys that make
// one and leave the user on it. A session filtered out of the current view
// has no row, and the cursor stays where it was.
func (m *Model) focusSession(id string) {
	for i, row := range m.rows {
		if row.isSession() && row.sess.ID == id {
			m.cursor = i
			// The cursor decides which session's work is open, so a cursor
			// put somewhere rather than stepped there rebuilds too.
			m.rebuildRows()
			return
		}
	}
}

// schedulePreview arms a single capture after previewSettle. Call after
// bumping previewGen so earlier timers and in-flight captures go stale.
func (m *Model) schedulePreview() tea.Cmd {
	gen := m.previewGen
	return tea.Tick(previewSettle, func(time.Time) tea.Msg {
		return previewSettleMsg{gen: gen}
	})
}

// previewCmd captures one session's pane and process stats off the
// render loop. gen tags the result so a newer cursor move can discard it.
// Size pins stay on resizeSessions / create / attach — not on every look —
// so a settle capture is one capture-pane, not resize+capture+pid storms.
// procDue reports whether the process tree behind the previewed pane is old
// enough to resample, stamping the clock when it is. The walk is the most
// expensive thing a preview does, so it runs on its own cadence instead of
// riding every capture -- see focusCaptureCmd, which splits them the same way.
func (m *Model) procDue() bool {
	if time.Since(m.procAt) < procInterval {
		return false
	}
	m.procAt = time.Now()
	return true
}

func (m *Model) previewCmd(sess store.Session, gen uint64, withProc bool) tea.Cmd {
	return func() tea.Msg {
		msg := previewMsg{sessID: sess.ID, gen: gen, at: time.Now()}
		if sess.Archived || !m.tmux.Exists(sess.ID) {
			snapshot, err := storedPreview(m.store, m.tmux, sess.ID)
			if err != nil {
				return errMsg{err}
			}
			msg.preview = snapshot
			return msg
		}
		if pane, err := m.tmux.CapturePane(sess.ID); err == nil {
			msg.preview = pane
		}
		// A notch over the preview routes on these -- whether the pane's
		// application owns the mouse, in what encoding, and how deep its
		// history runs -- so they are read on the same pass as the text
		// rather than only while the pane is focused.
		msg.facts, msg.factsOK = readFacts(m.tmux, sess.ID), true
		// The process tree is walked on its own slow clock, never with the
		// text. It is the single most expensive thing a preview does -- a
		// pane capture is one tmux call, this is a walk of /proc per process
		// behind the pane -- and paying it per capture is what made a run of
		// cursor moves feel like the board had stopped answering.
		if withProc {
			if pid, err := m.tmux.PanePID(sess.ID); err == nil {
				memTotal, _ := sysstat.MemTotalBytes()
				msg.proc = sysstat.Trees([]int{pid})[pid].ScaleToHost(sysstat.LogicalCPUs(), memTotal)
				msg.procOK = true
			}
		}
		msg.at = time.Now()
		return msg
	}
}

// refreshCmd runs one synchronous polling pass; the background poller
// covers normal operation, this exists for tests and explicit refreshes.
func (m *Model) refreshCmd() tea.Cmd {
	m.syncPollInput()
	return func() tea.Msg {
		return m.poller.refreshOnce()
	}
}

// paneGeomMsg is what one resize pass told tmux, carried back to the event
// loop so the geometry cache is still written in exactly one place.
type paneGeomMsg struct {
	width  int
	height int
	sized  []string
	// released is the sessions whose windows were handed back on this pass.
	// Their cached geometry has to go with them: the size the manager last
	// asked for is no longer the size the window has.
	released []string
}

// previewedSession is the one session whose pane is on screen right now, or
// "" when none is: a modal is up, the manager is blurred, or there is no
// preview panel to fill.
//
// It is the whole input to the pinning policy below. Exactly one window is
// ever pinned, so the answer to "which window may the manager hold?" is a
// single id rather than a set.
func (m *Model) previewedSession() string {
	if !m.visible {
		// Somebody is looking at another window, or at nothing. A pin is
		// only ever justified by a preview being read, and there is no
		// reader.
		return ""
	}
	if m.mode != modeList && m.mode != modeRename && m.mode != modeFocus {
		return ""
	}
	if m.previewPaneWidth() <= 0 || m.previewPaneHeight() <= 0 {
		return ""
	}
	sess, ok := m.selected()
	if !ok || sess.Archived {
		return ""
	}
	return sess.ID
}

// resizeSessions holds the previewed session's tmux window at the preview
// panel's size, and hands every other window back.
//
// resize-window forces a window's window-size to "manual", which is the only
// thing that keeps a detached window at a size the manager chose -- and it is
// a pin, on a board that is mostly windows the operator also uses directly in
// tmux. This used to run over every non-archived session and never released
// any of them, so a manager that had been up for an hour held all ~25 of the
// operator's windows frozen at the width of its preview panel, including the
// ones they were working in themselves.
//
// The policy now is that a window is pinned only while the manager is
// actually showing that pane, and released as soon as it is not: the cursor
// moves off the row, focus is left, a modal covers the preview, or the
// operator switches to another tmux window entirely. The release is driven
// from the driver's own record of what it pinned rather than from anything
// the model remembers, so a pin taken on a path the model has forgotten is
// still found and handed back.
//
// It returns a command rather than doing the work, because both halves of
// that work are unbounded: reflowSessions waits on the poller's lock, which
// a pass holds for its whole duration, and the resizes are a tmux round-trip
// per session. Run on the event loop -- where a poll result, a terminal
// resize and a divider drag all land -- that is the whole board freezing
// until the pass in flight finishes, with every keypress queued behind it.
func (m *Model) resizeSessions() tea.Cmd {
	if m.pane.resizing || m.tmux == nil {
		return nil
	}
	width, height := m.previewPaneWidth(), m.previewPaneHeight()
	if m.pane.geom == nil {
		m.pane.geom = map[string][2]int{}
	}
	keep := m.previewedSession()
	// Everything the driver is holding that is not the previewed pane. Read
	// from the driver, not from m.pane.geom: geom is a cache of sizes the
	// manager asked for, while this is the set it actually took over.
	var release []string
	for _, id := range m.tmux.PinnedSessions() {
		if id != keep {
			release = append(release, id)
		}
	}
	pin := ""
	if keep != "" && width > 0 && height > 0 {
		if last, ok := m.pane.geom[keep]; !ok || last[0] != width || last[1] != height {
			pin = keep
		}
	}
	if pin == "" && len(release) == 0 {
		return nil
	}
	m.pane.resizing = true
	poller, driver := m.poller, m.tmux
	return func() tea.Msg {
		var sized []string
		touched := append([]string(nil), release...)
		if pin != "" {
			touched = append(touched, pin)
		}
		// Pause polling for the whole clear+resize window so a mid-reflow
		// capture cannot compare against a pre-resize hash. Releasing
		// reflows the window exactly as pinning does, so both belong inside
		// it.
		poller.reflowSessions(touched, func() {
			var wg sync.WaitGroup
			for _, id := range release {
				wg.Add(1)
				go func(id string) {
					defer wg.Done()
					driver.ReleaseSize(id)
				}(id)
			}
			if pin != "" {
				if err := driver.Resize(pin, width, height); err == nil {
					sized = append(sized, pin)
				}
			}
			wg.Wait()
		})
		return paneGeomMsg{width: width, height: height, sized: sized, released: release}
	}
}

// applyPaneGeom records what a resize pass actually told tmux, and re-runs
// only when the preview box moved while it was out there. A session tmux
// refused stays out of the cache, so re-running on that alone would ask for
// the same refusal on every pass from then on.
func (m *Model) applyPaneGeom(msg paneGeomMsg) tea.Cmd {
	m.pane.resizing = false
	if m.pane.geom == nil {
		m.pane.geom = map[string][2]int{}
	}
	for _, id := range msg.sized {
		m.pane.geom[id] = [2]int{msg.width, msg.height}
	}
	// A released window went back to whatever sizing it had, so the cache
	// must forget what the manager once set it to -- otherwise re-selecting
	// that row would match the cache and skip the resize it now needs.
	for _, id := range msg.released {
		delete(m.pane.geom, id)
	}
	if msg.width == m.previewPaneWidth() && msg.height == m.previewPaneHeight() {
		return nil
	}
	return m.resizeSessions()
}

// update is the model's own dispatch. Update, in logdispatch.go, is the
// exported entry point: it records the press and the branch it took before
// handing over here.
func (m *Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Resuming from a tmux attach re-sends the current size unchanged; only
		// a real resize needs the per-session tmux resize calls, so an
		// unchanged size skips them and keeps detach latency flat.
		//
		// The backdrop is not the same question. A reattach is often a new
		// terminal entirely -- the operator SSHing back in -- which has never
		// been told our background and holds none of the frame's cells, so
		// the diffed redraw would leave its own colour wherever it had
		// nothing to rewrite. The clear puts every cell out again.
		if msg.Width == m.width && msg.Height == m.height {
			SyncTerminalBackground()
			return m, tea.ClearScreen
		}
		m.width = msg.Width
		m.height = msg.Height
		// Re-assert the terminal backdrop: a reattach or a fresh outer
		// terminal delivers a size message and may carry stale colors.
		SyncTerminalBackground()
		// Geometry cache is stale for every session after a real resize.
		m.pane.geom = nil
		resize := m.settleResizeLater()
		switch m.mode {
		case modeForm:
			m.syncFormFieldWidths()
		case modeGroupForm:
			m.syncGroupFormFieldWidths()
		}
		// A scrolled-back pane holds its frame against live captures, so
		// nothing else would repaint it at the new size: the stale frame
		// would sit cropped until the next wheel notch. Re-read the region
		// at the new height, clamped to the history the reflowed pane has.
		if m.scrolledBack() {
			if sess, ok := m.selected(); ok {
				m.reclampFocusScroll()
				resize = tea.Batch(resize, m.requestFocusRegion(sess.ID))
			}
		}
		return m, resize

	case paneGeomMsg:
		geom := m.applyPaneGeom(msg)
		// The pane has just reflowed under a scrolled-back view, whose
		// frame nothing else repaints: read the region again at the size
		// the pane now has.
		if m.scrolledBack() {
			if sess, ok := m.selected(); ok {
				geom = tea.Batch(geom, m.requestFocusRegion(sess.ID))
			}
		}
		return m, geom

	case resizeSettleMsg:
		if msg.seq != m.resizeSeq {
			return m, nil
		}
		return m, m.resizeSessions()

	case tea.FocusMsg:
		// The terminal itself says the operator came back. Inside tmux this
		// only arrives when they have focus-events on, which is why the
		// polled read below exists as well.
		return m, tea.Batch(m.setVisible(true), m.ownPaneVisibleCmd())

	case tea.BlurMsg:
		return m, m.setVisible(false)

	case visibleMsg:
		visible, ok := applyVisible(msg.state)
		if !ok {
			return m, nil
		}
		if msg.deviceErr != nil {
			m.errBar.text = msg.deviceErr.Error()
		}
		return m, tea.Batch(m.setVisible(visible), m.loadDeviceTheme(msg.device))

	case browserOpenMsg:
		m.handleBrowserOpen(msg)
		return m, nil

	case startupTickMsg:
		if !m.needsLoaderTick() {
			m.startupAnimating = false
			return m, nil
		}
		m.startupPhase++
		return m, m.startupTick()

	case previewTickMsg:
		// Only the list keeps a live pane on screen; the modal screens have
		// no preview to feed, so they skip the capture and just keep the
		// timer alive.
		sess, ok := m.selected()
		if !ok || (m.mode != modeList && m.mode != modeRename && m.mode != modeFocus) {
			m.frameUnchanged()
			return m, m.previewTick()
		}
		// The focused pane reads its own facts on the same round trip as its
		// text, and samples the process tree far more rarely than it
		// captures -- see focusCaptureCmd. Everything else is a row under
		// the cursor, which wants the stats with every frame.
		if m.mode == modeFocus {
			// One tick capture at a time. A capture is a fork of several
			// milliseconds and the active cadence is 12ms, so a tick was
			// issuing another before the last had returned: the excess
			// arrived as frames nobody could read, and the event loop spent
			// itself on them instead of on the keys waiting behind them.
			// The stale window is what keeps a capture that never comes back
			// from silencing the cadence for good, as focusReadStale does
			// for a region read.
			if m.focusCapturing && time.Since(m.focusCapturingAt) < focusCaptureStale {
				m.frameUnchanged()
				return m, m.previewTick()
			}
			// A chase is a capture loop already looking at this pane, and
			// the frame it brings back is the frame this tick would take.
			// Forking a second read beside it doubled the capture rate of a
			// flick for frames that were never newer than the chase's own.
			if m.focusChasing && time.Since(m.focusChasingAt) < focusCaptureStale {
				m.frameUnchanged()
				return m, m.previewTick()
			}
			// Under the wheel every notch's chase lands a frame of its own,
			// and a frame younger than one tick is the frame this tick would
			// take. A flick then costs one capture per notch rather than one
			// per notch plus one per tick; a pane being typed into keeps its
			// tick, because there the frame owed is the one behind the key.
			if m.focusWheeling() && !m.focusActive() && time.Since(m.previewAt) < focusIntervalStream {
				m.frameUnchanged()
				return m, m.previewTick()
			}
			withProc := time.Since(m.procAt) >= procInterval
			if withProc {
				m.procAt = time.Now()
			}
			// The capture is conditional: the command below asks tmux over
			// the pooled pipe whether the pane has written anything since
			// the last look, and forks only if it has. A focused pane
			// nobody is typing into and whose agent is not printing costs
			// no forks at all.
			capture := m.focusCaptureCmd(sess.ID, m.previewGen, withProc, m.captureGate(sess.ID))
			if capture != nil {
				m.focusCapturing, m.focusCapturingAt = true, time.Now()
				m.focusCaptureFor, m.focusCaptureGeom = sess.ID, m.pane.geom[sess.ID]
			}
			// A tick that only arms a capture has changed nothing the frame
			// reads either. What it wrote -- focusCapturing, the pane it was
			// for, the geometry it went out against, the process clock -- no
			// view reads, and the capture's answer paints when it lands, as
			// previewMsg. Without saying so the tick provoked a full repaint
			// of the frame already on screen, once every twelve milliseconds
			// for as long as somebody was typing.
			m.frameUnchanged()
			return m, tea.Batch(capture, m.previewTick())
		}
		// A scrolled-back preview holds still, so this tick's capture would be
		// dropped on arrival -- after walking the pane's whole process tree to
		// get there, on every tick the operator spends reading it.
		if m.scrolledBack() {
			m.frameUnchanged()
			return m, m.previewTick()
		}
		// The same again for the row under the cursor: the capture is off in
		// a command and nothing on the way to it moved. This one only bites
		// on a live row, whose 300ms tick lands inside frameReuseWindow; a
		// calm row ticks every 1200ms, falls outside the window and repaints
		// as it always did, which is the window doing its job rather than
		// this call failing to.
		m.frameUnchanged()
		return m, tea.Batch(m.previewCmd(sess, m.previewGen, m.procDue()), m.previewTick())

	case conversationTickMsg:
		return m, tea.Batch(m.readConversation(), conversationTick())
	case conversationMsg:
		m.applyConversation(msg)
		return m, nil
	case historyIndexedMsg:
		return m, m.historySearchCmd()
	case historyDebounceMsg:
		if msg.seq != m.historySeq {
			return m, nil
		}
		return m, m.historySearchCmd()
	case historySearchMsg:
		m.applyHistorySearch(msg)
		return m, nil

	case refreshMsg:
		m.ageError()
		// The focused session can die or vanish under us; fall back to the
		// list rather than typing into nothing.
		sessions := m.keepPendingLaunches(msg.sessions, msg.listedAt)
		var focusExit tea.Cmd
		if m.mode == modeFocus {
			if sess, ok := m.selected(); !ok || !slices.ContainsFunc(sessions, func(current store.Session) bool {
				return current.ID == sess.ID && current.Status != status.Dead
			}) {
				focusExit = m.leaveFocus()
			}
		}
		m.sessions = sessions
		m.dropHeldAckOnNewTurn()
		// A pane taken by the last adopt scan is on the board under its
		// directory's basename, and this is the first pass that can see the
		// row. Naming it here rather than waiting for the naming ticker is
		// the difference between half a minute of "sample-repo-71" and none. The
		// flag is only cleared once a pass actually starts, so an eager
		// naming that collides with one already running is not lost.
		var nameNow tea.Cmd
		if m.nameAfterRefresh {
			if nameNow = m.autoNameScan(); nameNow != nil {
				m.nameAfterRefresh = false
			}
		}
		// The driver's adoption registry is in memory, so a restart starts
		// empty. Re-register once the rows are loaded, not at Init, where
		// there are no sessions to read them from yet.
		if !m.adoptRestored {
			m.adoptRestored = true
			m.restoreAdopted()
		}
		// This is the first pass whose statuses came from a real pane scan,
		// so it is the earliest point a dead row means a missing pane rather
		// than a row the poller has not reached yet.
		m.maybeOpenRestorePrompt()
		// A busy pane the operator agreed to take over is taken on the pass
		// that first sees it idle. Quiet unless something moved: the count
		// still owed was said when the answer was given.
		if result := m.takeoverPass(); result.taken > 0 || len(result.failed) > 0 {
			m.reportTakeover(result)
		}
		m.maybeOpenTakeoverPrompt()
		m.groups = msg.groups
		m.groupPaths = msg.groupPaths
		m.archivedGroups = msg.archivedGroups
		m.priorityGroups = msg.priorityGroups
		m.agents = msg.agents
		m.queuedMessages = msg.queuedMessages
		m.archivedChildren = msg.archivedChildren
		m.searchText = msg.searchText
		m.answerableWait = msg.answerableWait
		m.hookless = msg.hookless
		if msg.snapOK {
			m.snap = msg.snap
			m.updateNetRates(msg.snap)
		}
		if !m.sessionsSized && m.width > 0 && len(m.sessions) > 0 {
			m.sessionsSized = true
			// Sessions left from a previous run carry that run's window
			// size, which our cache knows nothing about, so the first pass
			// re-asserts geometry for every one of them.
			m.pane.geom = nil
		}
		// The preview box changes height for more reasons than a terminal
		// resize: the quick bar opening, the status line appearing, a new
		// badge in the header. A pane left at the old height paints a dead
		// band under its output, so every pass re-asserts the geometry.
		// The call is free when nothing moved: it diffs against paneGeom.
		var resize tea.Cmd
		if m.sessionsSized && m.width > 0 {
			resize = m.resizeSessions()
		}
		// The archive's retention window expires with nobody watching, so
		// the poll is what notices. It runs on its own much slower clock;
		// this call is free on the passes in between.
		sweep := m.sweepExpiredArchives()
		// Off the loop, so a busy tmux server delays a child's filing rather
		// than the board's next frame. What it finds lands as childSweptMsg.
		childSweep := m.sweepFinishedChildren()
		m.rebuildRows()
		// A pass that ran with a stale selection (a session created this
		// tick) carries the wrong preview; resync and fetch it directly.
		if sess, ok := m.selected(); ok && sess.ID != msg.procFor {
			m.syncPollInput()
			m.previewGen++
			return m, tea.Batch(focusExit, nameNow, resize, sweep, childSweep, m.previewCmd(sess, m.previewGen, m.procDue()), m.startStartupTick())
		}
		m.proc = msg.proc
		m.procFor = msg.procFor
		m.setPreviewAt(msg.preview, msg.previewAt)
		return m, tea.Batch(focusExit, nameNow, resize, sweep, childSweep, m.ownPaneVisibleCmd(), m.startStartupTick())

	case childSweptMsg:
		m.applyChildSweep(msg)
		return m, nil

	case adoptTickMsg:
		return m, tea.Batch(m.adoptScan(), m.adoptTick())

	case adoptedMsg:
		if msg.err != nil {
			m.errBar.text = "adopting a pane: " + msg.err.Error()
		}
		if msg.taken > 0 {
			// The rows are in the store but not yet on the board; the sweep
			// is what reads them back.
			m.nameAfterRefresh = true
			m.poller.requestRefresh()
		}
		return m, nil

	case autoNameTickMsg:
		return m, tea.Batch(m.autoNameScan(), m.autoNameTick())

	case autoNamedMsg:
		m.autoNaming = false
		if m.conversation != nil && msg.targets != nil {
			m.conversation.targets = msg.targets
		}
		if msg.err != nil {
			m.errBar.text = "naming a session: " + msg.err.Error()
		}
		m.applyFirstPrompts(msg.prompts)
		if m.applyRenames(msg.renamed) {
			m.rebuildRows()
			// The rail is already right; this is for everything else a pass
			// derives from a session row.
			m.poller.requestRefresh()
		}
		return m, nil

	case smartRenamedMsg:
		return m.applySmartRename(msg)

	case nameSweepPlanMsg:
		m.applyNameSweepPlan(msg)
		return m, nil

	case nameSweepDoneMsg:
		m.applyNameSweepResult(msg)
		m.errBar.text = nameSweepSummary(msg.result)
		// A renamed agent writes its name to a mailbox the poll reads back,
		// so the board needs a pass before any of it shows.
		m.requestRefresh()
		return m, nil

	case workTickMsg:
		return m, tea.Batch(m.refreshWork(), m.workTick())

	case workLoadedMsg:
		// Work that has just resolved is work the tree can hang rows off.
		m.rebuildRows()
		return m, nil

	case pasteSweepMsg:
		if msg.err != nil {
			m.errBar.text = "clearing old pasted images: " + msg.err.Error()
		}
		return m, nil

	case tmuxConfigMsg:
		m.maybeOpenTmuxHint(msg.findings)
		return m, nil

	case pasteSweepTickMsg:
		return m, tea.Batch(m.sweepPastes, m.pasteSweepTick())

	case groupJumpSettleMsg:
		if msg.gen != m.jump.gen {
			// Superseded by a further digit, which armed its own timer.
			m.frameUnchanged()
			return m, nil
		}
		m.jump.buffer = ""
		return m, nil

	case previewSettleMsg:
		if msg.gen != m.previewGen {
			// Every cursor move arms one of these and tea.Tick cannot be
			// cancelled, so a held key delivers one superseded settle per
			// keystroke, with nothing on screen to show for it.
			m.frameUnchanged()
			return m, nil
		}
		sess, ok := m.selected()
		if !ok {
			m.frameUnchanged()
			return m, nil
		}
		// The cursor coming to rest is the only place a window is worth
		// taking over. Doing it here rather than on every cursor move is
		// what keeps a held j from reflowing twenty of the operator's
		// windows on its way past them.
		return m, tea.Batch(m.resizeSessions(), m.previewCmd(sess, msg.gen, m.procDue()))

	case cursorBlinkMsg:
		if msg.gen != m.blinkGen || m.mode != modeFocus {
			return m, nil
		}
		m.cursorOn = !m.cursorOn
		return m, m.cursorBlink()

	case focusCopiedMsg:
		m.errBar.text = ""
		m.copied = msg.chars
		return m, nil

	case sessionIDCopiedMsg:
		if msg.err != nil {
			m.errBar.text = "could not copy session id: " + msg.err.Error()
			return m, nil
		}
		m.errBar.text = "copied session id " + msg.id
		return m, nil

	case focusScrollMsg:
		// Cleared before the session check: a frame that arrives for a row
		// the cursor has left is dropped, and a flag left set behind it would
		// mean no notch on any row ever reads again.
		m.focusReading = false
		sess, ok := m.selected()
		if !ok || sess.ID != msg.sessID {
			return m, nil
		}
		return m, m.applyFocusScroll(sess.ID, msg)

	case paneStateMsg:
		sess, ok := m.selected()
		if !ok || sess.ID != msg.sessID {
			return m, nil
		}
		var facts paneFacts
		applyPaneState(&facts, msg.state)
		m.storePaneState(msg.sessID, facts)
		return m, nil

	case previewMsg:
		// Any frame at all means the manager has just looked at the pane, so
		// the next notch may arm its own chase again and the next tick may
		// take its own capture.
		m.focusChasing = false
		m.focusCapturing = false
		if msg.gen != 0 && msg.gen != m.previewGen {
			return m, nil
		}
		sess, ok := m.selected()
		if !ok || sess.ID != msg.sessID {
			return m, nil
		}
		// A tick that forked nothing has no frame to land. Its facts still
		// do -- the caret and the pane's mouse claims were read on the same
		// round trip as the stamp -- and so do its process stats, which are
		// sampled on their own cadence and must not lapse just because the
		// pane is quiet.
		if msg.skipped {
			onScreen := m.paneFactsOnScreen(msg)
			if msg.factsOK {
				m.storePaneState(msg.sessID, msg.facts)
				m.pane.cursor = paneCursor{x: msg.facts.cursorX, y: msg.facts.cursorY, ok: msg.facts.cursorOK}
			}
			if msg.procOK {
				m.proc, m.procFor = msg.proc, msg.sessID
			}
			if onScreen && !msg.procOK {
				m.frameUnchanged()
			}
			return m, m.trailingEcho(sess)
		}
		// The second this frame was captured in is what the next tick's
		// stamp is measured against. A chase's frame is deliberately not
		// counted: it stamps itself after its capture rather than before,
		// so a capture at .999 can carry the next second and claim to have
		// seen output it took no look at. Every chase runs inside a
		// keystroke or wheel window, which forces the tick's capture anyway,
		// so leaving them out costs nothing and keeps the mark honest.
		if at := msg.at; !msg.chase && !at.IsZero() && at.Unix() > m.focusCaptureSec {
			m.focusCaptureSec, m.focusCaptureFor = at.Unix(), msg.sessID
		}
		// An older capture must never paint over a newer one. Two are in
		// flight whenever a keystroke chase overtakes a tick, and a frame
		// from before the key would take the typed character back off the
		// screen for one beat.
		if !msg.at.IsZero() && msg.at.Before(m.previewAt) {
			return m, nil
		}
		if !msg.at.IsZero() {
			m.previewAt = msg.at
		}
		// Read before the facts land below, which is what it compares against.
		factsOnScreen := m.paneFactsOnScreen(msg)
		if msg.factsOK {
			m.storePaneState(msg.sessID, msg.facts)
			m.pane.cursor = paneCursor{x: msg.facts.cursorX, y: msg.facts.cursorY, ok: msg.facts.cursorOK}
		}
		if msg.procOK {
			m.proc = msg.proc
			m.procFor = msg.sessID
		}
		// Whether the pane painted, measured against whatever this frame is
		// owed a comparison with: the chase carries its own answer, because
		// its baseline was taken before the key and the frame on screen may
		// be older still. A tick has no baseline but the screen, and read
		// before setPreview replaces it.
		painted := msg.preview != m.preview
		if msg.chase {
			painted = msg.echoed
			if msg.echoed {
				// The frame behind the key is in hand, so the keystroke tier
				// has nothing left to catch -- see releaseFocusActive.
				m.releaseFocusActive()
			}
		}
		// A tick that read the same frame, the same caret and the same pane
		// claims as the ones on screen has nothing to paint. Most ticks on an
		// idle pane are that tick, and each one cost a full frame -- the
		// renderer skips an identical frame, but only after it is rendered.
		// A chase is left out: its deaf count below is part of the frame.
		if !painted && !msg.chase && !msg.procOK && factsOnScreen {
			m.frameUnchanged()
		}
		m.setPreview(msg.preview)
		m.noteEcho(sess, msg.chase, painted)
		return m, m.trailingEcho(sess)

	case errMsg:
		m.errBar.text = msg.err.Error()
		return m, nil

	case pasteImageMsg:
		return m.handlePasteImageMsg(msg)

	case pasteTextMsg:
		return m.handlePasteTextMsg(msg)

	case attachDoneMsg:
		// An agent that repainted the terminal background for itself leaves
		// it on ours; the resume's WindowSizeMsg skips its own sync when the
		// size is unchanged, so the detach restores the theme's here.
		SyncTerminalBackground()
		// The attach client sized the window to the full terminal and tmux
		// keeps that size on detach; shrink it back to the preview panel so
		// the capture is not clipped on the right.
		if m.pane.geom != nil {
			delete(m.pane.geom, msg.sessID)
		}
		width, height := m.previewPaneWidth(), m.previewPaneHeight()
		m.poller.reflowSessions([]string{msg.sessID}, func() {
			_ = m.tmux.Resize(msg.sessID, width, height)
		})
		if m.pane.geom == nil {
			m.pane.geom = map[string][2]int{}
		}
		m.pane.geom[msg.sessID] = [2]int{width, height}
		if msg.err != nil {
			m.errBar.text = msg.err.Error()
			m.requestRefresh()
			return m, nil
		}
		// alt+o inside the session leaves a marker before detaching; consume
		// it here and carry it out for the session just attached.
		request, err := m.tmux.PendingRequest()
		if err != nil {
			m.errBar.text = err.Error()
		} else if request != "" {
			// A failed clear leaves the marker set, which would replay the
			// request on every later detach, so surface it and stay in the
			// list rather than letting the request reset m.errBar.text and
			// hide it.
			if clearErr := m.tmux.ClearRequest(); clearErr != nil {
				m.errBar.text = clearErr.Error()
				m.requestRefresh()
				return m, nil
			}
			// The request acts on the row under the cursor, and the cursor
			// is not where the request came from: a poll handled ahead of
			// this message rebuilds the rows, and a filter can drop the
			// session that detached out of the list entirely.
			m.focusSession(msg.sessID)
			sess, ok := m.selected()
			if !ok || sess.ID != msg.sessID {
				m.errBar.text = "the session that asked for it has left the list"
				m.requestRefresh()
				return m, nil
			}
			switch request {
			case tmux.RequestEditor:
				_, cmd := m.openEditor()
				// The request cost the session its client, so the manager
				// goes back into it once the editor is up, or once a
				// terminal editor closes. A refused launch returns no
				// command and stays in the list with its reason.
				if cmd != nil {
					m.editorReturnID = sess.ID
				}
				return m, cmd
			}
		}
		m.requestRefresh()
		return m, nil

	case editorDoneMsg:
		if msg.tookScreen {
			// The terminal comes back from an editor painted in the
			// editor's background. Mouse reporting re-arms itself: it is a
			// View field now, so the next frame restores it.
			SyncTerminalBackground()
		}
		if msg.err != nil {
			// Going back into the session would hide the only account of
			// what went wrong, so a failed editor keeps the list.
			m.errBar.text = msg.err.Error()
			m.editorReturnID = ""
			return m, nil
		}
		if msg.name != "" {
			m.reportDone("opened " + msg.path + " in " + msg.name)
		}
		if id := m.editorReturnID; id != "" {
			m.editorReturnID = ""
			return m, m.reattach(id)
		}
		return m, nil

	case reattachPreparedMsg:
		if msg.err != nil {
			m.errBar.text = msg.err.Error()
			return m, nil
		}
		m.errBar.text = msg.warn
		return m, tea.ExecProcess(m.tmux.AttachCommand(msg.sessID), func(err error) tea.Msg {
			return attachDoneMsg{sessID: msg.sessID, err: err}
		})

	case legendPeekDecayMsg:
		visible := m.legendPeek.visible
		m.settleLegendPeek(msg)
		if visible == m.legendPeek.visible {
			m.frameUnchanged()
		}
		return m, nil

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.KeyPressMsg:
		model, cmd := m.handleKey(msg)
		m.syncPollInput()
		return model, cmd

	case tea.PasteMsg:
		if m.quick.active && (m.mode == modeList || m.showsConversation()) {
			m.quick.input.InsertString(msg.Content)
			return m, nil
		}
		if m.mode == modeFocus {
			return m.handleFocusPaste(msg)
		}
	}
	return m, nil
}

// clearPreviewState drops everything that belonged to the row the cursor is
// leaving: its frame, its process reading, how far its pane was scrolled
// back, and the pane facts a wheel notch routes on. Facts outliving their
// session would route the next session's notch on the last one's answers.
func (m *Model) clearPreviewState() {
	m.preview = ""
	m.proc = sysstat.ProcStat{}
	m.procFor = ""
	m.focusScroll = 0
	m.focusReading = false
	m.focusChasing = false
	m.echoPending = false
	// The run belongs to the pane that was on screen; the mark it may have
	// left does not, and stays for the rail and the drain to read.
	m.focusDeaf, m.focusDeafID = 0, ""
	m.pane.forID = ""
	m.pane.mouse = false
	m.pane.motion = false
	m.pane.sgr = false
	m.pane.history = 0
	m.pane.cursor = paneCursor{}
}

// trailingEcho is the chase owed to input that reached the pane while another
// chase was already out. Typing repeats faster than a chase completes and one
// chase per key forks a capture per key -- but a key whose chase was suppressed
// still put a character on the pane, and the frame that just landed may
// predate it. So the landing arms one more chase, measured against that frame:
// whatever was typed behind it shows up as a change to it.
//
// One chase at a time still holds -- this is that single chase, handed on --
// so a run of typing costs one in flight rather than one per key, and the last
// character is still brought back by a look rather than by a tick.
func (m *Model) trailingEcho(sess store.Session) tea.Cmd {
	if !m.echoPending || m.mode != modeFocus || m.focusChasing || m.scrolledBack() {
		return nil
	}
	m.echoPending = false
	return m.startChase(sess, m.preview)
}

// captureGate assembles what the focused tick's capture decision needs.
//
// The force cases are the ones tmux's stamp cannot report: a keystroke the
// pane may have discarded, a wheel notch, a pane the manager has not
// captured before, and a reflow under a size tmux was just told -- a resize
// moves the text on screen without the pane writing a byte.
//
// A pane repainting on its own is in the set for a different reason. Its
// stamp would pass the gate on nearly every tick anyway, so gating it saves
// no forks -- and the stamp read that decides so is a pipe round trip on the
// path of a pane the operator is watching frame by frame. Measured on this
// box, gating the streaming tier cost about a tenth of its frame rate to
// skip nothing: 16.5 captures a second became 14.8. So the tier the stamp
// pays for is the idle one, which is where a focused pane actually sits.
func (m *Model) captureGate(sessID string) captureGate {
	return captureGate{
		force: m.focusActive() || m.focusWheeling() || m.focusStreaming() ||
			m.focusCaptureFor != sessID || m.pane.geom[sessID] != m.focusCaptureGeom,
		sinceSec: m.focusCaptureSec,
	}
}

// startChase arms one echo chase against baseline and marks it out, so the
// tick stands down while it looks and a notch behind it rides it rather than
// forking its own -- see the previewTickMsg guard and forwardWheel.
func (m *Model) startChase(sess store.Session, baseline string) tea.Cmd {
	m.focusChasing, m.focusChasingAt = true, time.Now()
	return m.focusEchoCmd(sess.ID, m.previewGen, baseline, m.echoBudget(sess.Tool))
}

// paneFactsOnScreen reports whether the facts a frame carries are the ones
// already stored for the pane on screen, so landing them would change
// nothing the frame reads. A frame with no facts changes none.
func (m *Model) paneFactsOnScreen(msg previewMsg) bool {
	if !msg.factsOK {
		return true
	}
	if m.pane.forID != msg.sessID {
		return false
	}
	f := msg.facts
	return m.pane.mouse == f.paneMouse && m.pane.motion == f.paneMotion &&
		m.pane.sgr == f.paneSGR && m.pane.history == f.historySize &&
		m.pane.cursor == paneCursor{x: f.cursorX, y: f.cursorY, ok: f.cursorOK} &&
		// storePaneState folds a leftover offset once the app owns the wheel,
		// and reclamps against the history; either would move the frame.
		!(f.paneMouse && m.focusScroll != 0) &&
		m.focusScroll == clampFocusOffset(m.focusScroll, f.historySize)
}

// storePaneState records what tmux last said about the pane on screen. Every
// path that reads them lands here, so a pane whose agent tracks the mouse
// routes the same however the facts were fetched.
func (m *Model) storePaneState(sessID string, facts paneFacts) {
	m.pane.forID = sessID
	m.pane.mouse = facts.paneMouse
	m.pane.motion = facts.paneMotion
	m.pane.sgr = facts.paneSGR
	m.pane.history = facts.historySize
	// Once the app owns the wheel, nothing can walk a leftover offset back
	// down, and holding it would freeze the view for good.
	if m.pane.mouse && m.focusScroll != 0 {
		m.focusScroll = 0
	}
	m.reclampFocusScroll()
}

// setPreviewAt is setPreview for a frame that knows when it was captured.
// Every frame in the program races every other one -- a keystroke chase, the
// focused tick and a poll pass that started seconds ago can all land in the
// same beat -- and the only rule that makes that safe is that the newest
// capture wins. A frame with no timestamp is trusted, because the paths that
// carry none are the ones with nothing fresher to displace.
func (m *Model) setPreviewAt(preview string, at time.Time) {
	if !at.IsZero() {
		if at.Before(m.previewAt) {
			return
		}
		m.previewAt = at
	}
	m.setPreview(preview)
}

func (m *Model) setPreview(preview string) {
	// A scrolled-back pane holds still: a live-bottom frame landing mid-read
	// yanks the view away from what the operator is reading.
	if m.scrolledBack() {
		return
	}
	// A pane that repainted is a pane worth following for a moment: an agent
	// that has started writing usually keeps writing, and this is what gives
	// its output a cadence of its own without a second timer. Its own, not
	// typing's -- see focusIntervalStream.
	if m.mode == modeFocus && preview != m.preview {
		m.noteFocusRepaint()
	}
	m.preview = preview
}

// sessionGone reports whether id is absent from a refresh's session list.
func sessionGone(sessions []store.Session, id string) bool {
	for _, sess := range sessions {
		if sess.ID == id {
			return false
		}
	}
	return true
}

// updateNetRates diffs cumulative interface counters between polls into
// bytes-per-second rates. Counters can reset (sleep, interface changes),
// so a backwards jump just reseeds the baseline.
func (m *Model) updateNetRates(snap sysstat.Snapshot) {
	now := time.Now()
	if m.net.prevOK && snap.NetOK &&
		snap.NetSent >= m.net.prevSent && snap.NetRecv >= m.net.prevRecv {
		if dt := now.Sub(m.net.prevAt).Seconds(); dt > 0 {
			m.net.up = uint64(float64(snap.NetSent-m.net.prevSent) / dt)
			m.net.down = uint64(float64(snap.NetRecv-m.net.prevRecv) / dt)
			m.net.rates = true
		}
	}
	m.net.prevSent = snap.NetSent
	m.net.prevRecv = snap.NetRecv
	m.net.prevAt = now
	m.net.prevOK = snap.NetOK
}

// ageError clears a status message after it has survived a couple of poll
// ticks, so transient errors self-dismiss without any per-callsite timers.
func (m *Model) ageError() {
	if m.errBar.text == "" {
		m.errBar = errBar{}
		return
	}
	if m.errBar.text != m.errBar.shown {
		m.errBar.shown, m.errBar.age = m.errBar.text, 0
		return
	}
	m.errBar.age++
	if m.errBar.age >= 2 {
		m.errBar = errBar{}
	}
}

func hashString(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}

// rootGroup is the path every ungrouped session already carries.
const rootGroup = ""

// isRoot marks the pinned top-level row, which is not a stored group.
func (e treeRow) isRoot() bool { return e.isGroup && e.group == rootGroup }

// rowsBelowRoot is the tree without its pinned row: empty means the rail
// has nothing to list, however many rows it paints.
func rowsBelowRoot(rows []treeRow) []treeRow {
	if len(rows) > 0 && rows[0].isRoot() {
		return rows[1:]
	}
	return rows
}

func rowKey(entry treeRow) string {
	switch {
	case entry.isGroup:
		return "g:" + entry.group
	case entry.isArtifact():
		return "a:" + entry.sess.ID + ":" + entry.art.kind + ":" + entry.art.label
	}
	return "s:" + entry.sess.ID
}

// rebuildRows builds the tree, and builds it once more when the cursor did
// not land on the session the first pass was built for.
//
// Which session's work is open follows the cursor, and where the cursor lands
// follows the tree, so the two have to be settled against each other. One
// repeat settles them: the second pass is built for the session the first
// left the cursor on, and a tree built for the session already under the
// cursor cannot move it again.
func (m *Model) rebuildRows() {
	// One tmux listing for the whole rebuild, which is what buildTree already
	// believed it was taking. Both builds and the triage sort inside each of
	// them ask, and every ask was its own fork. See holdLivePanes.
	defer m.releaseLivePanes(m.holdLivePanes())
	m.pruneMutes(m.sessions)
	m.buildTree()
	if m.cursorSessionID() != m.railCursorSess {
		m.buildTree()
	}
}

// cursorSessionID is the session the cursor is on, which on an artifact row
// is the session it hangs off, and on a group row is nothing.
func (m *Model) cursorSessionID() string {
	index, ok := m.selectedIndex()
	if !ok {
		return ""
	}
	if entry := m.rows[index]; !entry.isGroup {
		return entry.sess.ID
	}
	return ""
}

// buildTree walks the group tree depth-first and emits one row per
// group node and per session, honoring collapse state, search, and the
// status filter. The cursor follows the previously selected row's
// identity, so list changes from the 2s poll never yank the selection.
func (m *Model) buildTree() {
	m.railCursorSess = m.cursorSessionID()
	m.workMemo = map[string][]workRow{}
	defer func() { m.workMemo = nil }()
	previousKey := ""
	// The cursor's own row, not the row an action would apply to: a cursor
	// parked on an artifact has to come back to that artifact, and the poll
	// rebuilds the tree every couple of seconds.
	if entry, ok := m.cursorRow(); ok {
		previousKey = rowKey(entry)
	}
	query := strings.ToLower(strings.TrimSpace(m.search))
	prunedView := query != "" || m.statusFilter.active()

	listed := store.OrderLinkedSessions(m.listedSessions())
	listedIDs := make(map[string]bool, len(listed))
	for _, sess := range listed {
		listedIDs[sess.ID] = true
	}
	byID := make(map[string]store.Session, len(m.sessions))
	for _, sess := range m.sessions {
		byID[sess.ID] = sess
	}
	// m.sessions arrives ordered by the store (group, sort_order), so
	// per-group slices inherit the user's manual order.
	matched := make(map[string]bool, len(listed))
	for _, sess := range listed {
		if query == "" || matchesSearch(sess, query, m.searchText[sess.ID]) {
			matched[sess.ID] = true
		} else if _, ok := m.historyHit(sess); ok {
			matched[sess.ID] = true
		}
	}
	// A parent the search itself missed still comes along to carry its
	// matching children, in the store's order rather than after them.
	carried := map[string]bool{}
	for _, sess := range listed {
		if !matched[sess.ID] || sess.ParentID == "" || !listedIDs[sess.ParentID] {
			continue
		}
		carried[sess.ParentID] = true
	}
	sessionsByGroup := map[string][]store.Session{}
	childrenByParent := map[string][]store.Session{}
	for _, sess := range listed {
		if sess.ParentID != "" {
			if _, ok := byID[sess.ParentID]; ok {
				if matched[sess.ID] {
					childrenByParent[sess.ParentID] = append(childrenByParent[sess.ParentID], sess)
				}
				continue
			}
		}
		if matched[sess.ID] || carried[sess.ID] {
			sessionsByGroup[sess.Group] = append(sessionsByGroup[sess.Group], sess)
		}
	}
	walked := map[string]bool{}
	for _, groupSessions := range sessionsByGroup {
		for _, sess := range groupSessions {
			walked[sess.ID] = true
		}
	}
	orphaned := map[string]bool{}
	// A child whose parent never made it into a group paints un-nested, in
	// the store's order rather than the order the parent map happens to
	// yield.
	for _, sess := range listed {
		if _, nested := childrenByParent[sess.ParentID]; !nested || walked[sess.ParentID] {
			continue
		}
		if !matched[sess.ID] {
			continue
		}
		sessionsByGroup[sess.Group] = append(sessionsByGroup[sess.Group], sess)
		orphaned[sess.ParentID] = true
	}
	for parentID := range orphaned {
		delete(childrenByParent, parentID)
	}
	// After the orphan pass, so a child that had to paint un-nested is
	// ordered with the sessions it is sitting among rather than left at the
	// end of the group. Triage and search order their own queues further
	// down and never reach this. See listsort.go.
	if m.sortsList() {
		for _, groupSessions := range sessionsByGroup {
			m.sortGroupSessions(groupSessions)
		}
	}
	// After the sort too: a block a helper floats heads its group whatever
	// order the rest are in. See rolefloat.go.
	if roots := floatedRoots(m.sessions); len(roots) > 0 {
		for group, groupSessions := range sessionsByGroup {
			sessionsByGroup[group] = floatBlocks(groupSessions, m.sessions, roots)
		}
	}

	paths := groupClosure(m.groups, m.sessions)
	// A scope whose group is gone -- deleted, renamed, or restored from a
	// setting older than the board -- would drain an empty rail with
	// nothing on screen to say why, so it widens back to the fleet. An
	// empty closure is a board that has not loaded its first poll yet,
	// not a group that went missing.
	if m.triageScope != "" && len(paths) > 0 && !paths[m.triageScope] {
		m.triageScope = ""
	}
	if m.showArchived {
		// The archived view keeps groups that hold archived sessions plus any
		// group whose subtree was archived as a whole (even with no sessions),
		// instead of the full tree skeleton.
		kept := pathsWithSessions(paths, sessionsByGroup)
		for path := range paths {
			if m.groupEffectivelyArchived(path) {
				addWithAncestors(kept, path)
			}
		}
		paths = kept
	} else {
		// The active view hides any archived group and its whole subtree.
		for path := range paths {
			if m.groupEffectivelyArchived(path) {
				delete(paths, path)
			}
		}
		if prunedView {
			paths = pathsWithSessions(paths, sessionsByGroup)
		}
	}
	if m.hideEmptyGroups {
		// This is a presentation filter only: stored groups remain available
		// to forms and return to the tree as soon as the toggle is switched
		// off. Ancestors of groups with visible sessions stay in the tree.
		paths = pathsWithSessions(paths, sessionsByGroup)
	}
	children := childIndex(paths, m.groups)
	// One tmux listing for the whole rebuild, and only where it is read:
	// triage asks whether a child's parent is still up, and nothing else
	// here does.
	var livePanes map[string]bool
	if m.triage && m.tmux != nil {
		livePanes = m.livePanes()
	}

	// Folds are a browsing convenience for the active tree; the archived,
	// search, and status-filter views already prune to matching groups, so
	// honoring folds there would hide the very sessions the user came for.
	honorFolds := !prunedView && !m.showArchived
	// Children fold on the same terms, and on one more: triage is a queue of
	// what needs a person, and a child blocked on a question is exactly what
	// it was opened to surface. Groups still flatten there by their own
	// route, so this gate is the children's alone.
	foldChildren := honorFolds && !m.triage

	// Root is a standing move and spawn target; its sessions stay flat.
	rows := make([]treeRow, 0, len(m.sessions)+len(paths)+1)
	appendSession := func(sess store.Session, depth int) {
		rows = append(rows, treeRow{sess: sess, depth: depth})
		rows = append(rows, m.artifactRows(sess, depth+1)...)
		// Folds are the browsing view's convenience only. The pruned views
		// -- triage, search, the status filter, the archive -- were opened
		// to find a session, and a child blocked on a person is exactly
		// what they exist to surface, so they draw every child there is.
		hidden := foldChildren && !m.childrenShown(sess.ID)
		childDepth := depth + 1
		if childDepth > childDepthCap {
			childDepth = childDepthCap
		}
		for _, child := range childrenByParent[sess.ID] {
			// A shell is the session's own terminal, opened with T and
			// expected on screen; only spawned agents fold away.
			if hidden && m.foldsAway(child) {
				continue
			}
			// Triage draws every child except the ones somebody else is
			// already answering; see parentOwns.
			if m.triage && m.foldsAway(child) && m.parentOwns(child, time.Now(), livePanes) {
				continue
			}
			rows = append(rows, treeRow{sess: child, depth: childDepth})
			rows = append(rows, m.artifactRows(child, childDepth+1)...)
		}
	}
	// A search is answered by the sessions it matched and triage by the
	// queue it built, so root's rollup goes with the rest of the group rows
	// in both. Over a flat queue the row reads as that queue's heading, and
	// a heading saying "root" above sessions drawn from one group answers
	// the wrong question -- as does one counting the ungrouped sessions
	// above a rail holding every session there is. It is also what the
	// cursor would otherwise land on.
	if query == "" && !m.triage {
		rows = append(rows, treeRow{isGroup: true, group: rootGroup})
	}
	if m.triage || query != "" {
		// Groups organise; triage and search drain. A session blocked on a
		// person inside a folded group is the thing triage exists to
		// surface, and a match inside one is what the query asked for, so
		// the sessions flatten into one list and the group rows go away.
		// Work and terminals still hang off the session they belong to.
		// Drawn from the store's order rather than from the group map, whose
		// iteration order is not one: triage sorts what it is handed, and a
		// search shows it as filed.
		kept := make(map[string]bool, len(listed))
		for path, groupSessions := range sessionsByGroup {
			if path != "" && !paths[path] {
				continue
			}
			// A child session rides in with its parent wherever the
			// parent landed: it is part of that session's work, not a
			// queue entry of its own to be scoped separately.
			if m.triage && !m.inTriageScope(path) {
				continue
			}
			for _, sess := range groupSessions {
				kept[sess.ID] = true
			}
		}
		queue := make([]store.Session, 0, len(kept))
		for _, sess := range listed {
			if kept[sess.ID] {
				queue = append(queue, sess)
			}
		}
		if m.triage {
			m.sortTriageWithChildren(queue, childrenByParent)
		} else if query != "" {
			m.rankByRelevance(queue, childrenByParent, query)
		}
		for _, sess := range queue {
			appendSession(sess, 0)
		}
		connectMigrationRows(rows)
		m.rows = rows
		m.clearGroupNumbers()
		// The fold column is a fact about the rows on screen, so every
		// build has to count them. Reading the count a previous tree build
		// left works only if there was one: a board that comes up with
		// triage already on takes this branch first and would reserve
		// nothing while its rows plainly have work to fold. A status filter
		// prunes the queue here too, so there is no build that can be
		// trusted to have counted these rows already.
		m.railWorkColumn = m.anyRailWork(rows)
		if query != "" {
			// The first match is the answer. Left where it was, the cursor
			// lands on whichever match happens to sit at that index --
			// restoreCursor lifts it back only if that row is still here.
			m.cursor = 0
		}
		m.restoreCursor(previousKey)
		return
	}
	m.numberGroups(children)
	for _, sess := range sessionsByGroup[""] {
		appendSession(sess, 0)
	}
	var walk func(path string, depth int)
	walk = func(path string, depth int) {
		rows = append(rows, treeRow{isGroup: true, group: path, depth: depth})
		if honorFolds && m.collapsed[path] {
			return
		}
		for _, sess := range sessionsByGroup[path] {
			appendSession(sess, depth+1)
		}
		for _, child := range children[path] {
			walk(child, depth+1)
		}
	}
	for _, root := range children[""] {
		walk(root, 0)
	}

	connectMigrationRows(rows)
	m.rows = rows
	m.railWorkColumn = m.anyRailWork(rows)
	m.restoreCursor(previousKey)
}

func (m *Model) restoreCursor(previousKey string) {
	if previousKey != "" {
		for i, entry := range m.rows {
			if rowKey(entry) == previousKey {
				m.cursor = i
				break
			}
		}
	} else if m.cursor == 0 && len(m.rows) > 1 && m.rows[0].isRoot() {
		// A launch opens on a session, not on root's rollup.
		m.cursor = 1
	}
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// groupClosure unions stored groups with groups referenced by sessions,
// then adds every ancestor so partial paths always render.
func groupClosure(groups []string, sessions []store.Session) map[string]bool {
	paths := map[string]bool{}
	add := func(path string) {
		for path != "" {
			paths[path] = true
			idx := strings.LastIndex(path, "/")
			if idx < 0 {
				break
			}
			path = path[:idx]
		}
	}
	for _, g := range groups {
		add(g)
	}
	for _, sess := range sessions {
		add(sess.Group)
	}
	return paths
}

func (m *Model) groupEffectivelyArchived(path string) bool {
	return store.EffectivelyArchived(m.archivedGroups, path)
}

func addWithAncestors(set map[string]bool, path string) {
	for path != "" {
		set[path] = true
		idx := strings.LastIndex(path, "/")
		if idx < 0 {
			break
		}
		path = path[:idx]
	}
}

func pathsWithSessions(paths map[string]bool, sessionsByGroup map[string][]store.Session) map[string]bool {
	kept := map[string]bool{}
	for path := range paths {
		for group := range sessionsByGroup {
			if inGroupSubtree(group, path) {
				kept[path] = true
				break
			}
		}
	}
	return kept
}

// childIndex maps each group to its ordered children: stored groups in
// the user's manual order, synthesized ancestors alphabetically after.
func childIndex(paths map[string]bool, ordered []string) map[string][]string {
	rank := make(map[string]int, len(ordered))
	for i, name := range ordered {
		rank[name] = i
	}
	children := map[string][]string{}
	for path := range paths {
		parent := parentGroup(path)
		children[parent] = append(children[parent], path)
	}
	for _, siblings := range children {
		sort.SliceStable(siblings, func(i, j int) bool {
			ri, oki := rank[siblings[i]]
			rj, okj := rank[siblings[j]]
			if oki && okj {
				return ri < rj
			}
			if oki != okj {
				return oki
			}
			return siblings[i] < siblings[j]
		})
	}
	return children
}

func baseName(path string) string {
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		return path[idx+1:]
	}
	return path
}

// matchesMetadata is a hit on what the row itself prints. Kept apart from
// matchesSearch so a row can say which of the two kept it.
func matchesMetadata(sess store.Session, query string) bool {
	if matchesLiteralMetadata(sess, query) {
		return true
	}
	_, matched := fuzzyMetadataScore(sess, query)
	return matched
}

func matchesLiteralMetadata(sess store.Session, query string) bool {
	return search.Match(strings.ToLower(sess.Name), query) ||
		search.Match(strings.ToLower(sess.Tool), query) ||
		search.Match(strings.ToLower(sess.Group), query) ||
		search.Match(strings.ToLower(sess.Status), query)
}

// matchesSearch also reaches into what the session is showing, so the one
// that mentioned a hostname or a ticket is findable without having been
// named for it. paneText is already stripped and lowercased by the poll, and
// is empty for a session with no capture, which then matches on metadata
// alone the way it always did.
func matchesSearch(sess store.Session, query, paneText string) bool {
	return matchesMetadata(sess, query) || search.Match(paneText, query)
}

func newID() string {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format("150405")))
	}
	return hex.EncodeToString(buf)
}

func (m *Model) sessionByID(id string) (store.Session, bool) {
	for _, sess := range m.sessions {
		if sess.ID == id {
			return sess, true
		}
	}
	return store.Session{}, false
}
