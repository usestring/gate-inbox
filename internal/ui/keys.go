// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/clipboard"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

func (m *Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Resize mode owns the keyboard until the drag commits or the user
	// cancels: other bindings would fight the mouse-gated session.
	if m.split.resizeMode {
		switch msg.String() {
		case "left", "h":
			m.nudgeSplit(-1)
			return m, nil
		case "right", "l":
			m.nudgeSplit(1)
			return m, nil
		case m.cap(keymap.ContextList, keymap.Resize), "enter":
			// Enter or a second press of the resize key commits the working
			// ratio.
			return m.exitResizeMode(true)
		case "esc":
			return m.exitResizeMode(false)
		case "q", "ctrl+c":
			m.persistSplitRatio()
			m.split.resizeMode = false
			m.split.dragging = false
			return m, tea.Quit
		default:
			return m, nil
		}
	}

	switch m.mode {
	case modeForm:
		return m.handleFormKey(msg)
	case modeConfirmDelete:
		return m.handleConfirmKey(msg)
	case modeLaunchHint:
		return m.handleLaunchHintKey(msg)
	case modeRename:
		return m.handleRenameKey(msg)
	case modeFork:
		return m.handleForkKey(msg)
	case modeAccount:
		return m.handleAccountKey(msg)
	case modeMigrate:
		return m.handleMigrateKey(msg)
	case modeSettings:
		return m.handleSettingsKey(msg)
	case modeMove:
		return m.handleMoveKey(msg)
	case modeGroupForm:
		return m.handleGroupFormKey(msg)
	case modeFocus:
		return m.handleFocusKey(msg)
	case modeHelp:
		return m.handleHelpKey(msg)
	case modeNameSweep:
		return m.handleNameSweepKey(msg)
	case modeRestorePrompt:
		return m.handleRestorePromptKey(msg)
	case modeWelcome:
		return m.handleWelcomeKey(msg)
	case modeTmuxHint:
		return m.handleTmuxHintKey(msg)
	case modeAgentPick:
		return m.handleAgentPickKey(msg)
	case modeExtensionView:
		return m.handleExtensionViewKey(msg)
	}

	if m.searching {
		return m.handleSearchKey(msg)
	}
	if m.quick.active {
		return m.handleQuickKey(msg)
	}
	if m.legendPeek.visible {
		action, bound := m.action(keymap.ContextList, msg)
		if bound && action == keymap.LegendPeek && !m.legendPeek.sticky {
			m.frameUnchanged()
			return m, m.repeatLegendPeek()
		}
		m.dismissLegendPeek()
		return m, nil
	}

	// A number names a group, and the digits of one arrive as separate
	// keypresses, so they are read ahead of every other binding -- including
	// the artifact row's refusals, since a jump acts on the tree rather than
	// on the row it starts from. Any other key ends the number rather than
	// being swallowed into it.
	if m.isGroupJumpKey(msg.String()) {
		return m, m.typeGroupNumber(msg.String())
	}
	m.clearGroupJump()

	// ctrl+c quits from every screen. It is read here rather than through
	// the map because it is the one key the operator may not move: a board
	// that could not be interrupted is a board somebody has to kill.
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}

	// A status jump is read ahead of the artifact row's refusals for the
	// same reason a group number is: it acts on the list rather than on the
	// row it starts from, and a cursor parked on a pull request is no reason
	// to refuse "take me to the next finished session". One table holds the
	// whole family, so the handler below does not grow a case per state.
	// See statusjump.go.
	action, bound := m.action(keymap.ContextList, msg)
	if jump, isJump := statusJumps[action]; bound && isJump {
		return m.jumpToStatus(jump)
	}

	// An artifact row is a pull request or a ticket. Every key below it acts
	// on a session, so rather than teach thirty handlers to refuse one, the
	// row answers a named few -- move, fold, open the artifact -- and refuses
	// the rest here. The list is what is allowed rather than what is denied,
	// so a session key added later is refused until somebody decides what it
	// should mean on a pull request. Written as actions rather than as keys,
	// so a rebound key is still refused, or still allowed, for the reason it
	// always was.
	if entry, ok := m.cursorRow(); ok && entry.isArtifact() {
		if entry.art.more > 0 {
			// The row stands for what the cap took off, so opening it is
			// lifting the cap: the rows it names come back in its place.
			switch action {
			case keymap.Open, keymap.StepIn, keymap.ShowAllWork:
				return m, m.toggleShowAllWork()
			}
		}
		switch action {
		case keymap.Open, keymap.Editor:
			if entry.art.url == "" {
				m.errBar.text = entry.art.label + " has no link to open"
				return m, nil
			}
			return m, openLink(entry.art.url)
		case keymap.StepIn:
			// Nothing nests under an artifact, and stepping into the pane
			// from here would be aimed at the session, which is not the row.
			return m, nil
		case keymap.StepOut:
			m.toggleRailWork()
			return m, nil
		}
		// An unbound key is refused here too: the row says what it is rather
		// than swallowing the press, which is what it did when every key was
		// a literal.
		if !bound || !artifactRowActions[action] {
			m.errBar.text = entry.art.label + " is work, not a session — select " +
				m.displayName(entry.sess) + " to act on it"
			return m, nil
		}
	}

	// Snippets are read before the list's own bindings and not inside them:
	// they live in a chord namespace nothing below claims, so the order costs
	// nothing, and keeping them out of the switch means a snippet can never
	// shadow a key the manager documents.
	if snip, ok := m.snippetFor(msg.String()); ok {
		return m.sendSnippetToSelected(snip)
	}

	if !bound {
		return m, nil
	}
	if ext, ok := m.extKeys[keymap.ContextList][action]; ok {
		return m, m.runExtensionKey(ext)
	}
	switch action {
	case keymap.Quit:
		return m, tea.Quit
	case keymap.ShowAllWork:
		return m, m.toggleShowAllWork()
	case keymap.CursorUp:
		return m, m.moveCursor(-1)
	case keymap.CursorDown:
		return m, m.moveCursor(1)
	case keymap.CursorTop:
		return m, m.jumpCursor(-1)
	case keymap.CursorBottom:
		return m, m.jumpCursor(1)
	case keymap.PreviewUp, keymap.PreviewDown, keymap.PreviewPageUp,
		keymap.PreviewPageDown, keymap.PreviewTop, keymap.PreviewBottom:
		// The preview scrolls on the same keys focus mode uses, because the
		// wheel is not a gesture every client has: a phone, a tablet
		// trackpad, or any terminal that sends a swipe as arrow keys leaves
		// the manager no notch to route, and plain arrows are the list's own
		// navigation. See keyScrollFocus.
		return m, m.keyScrollFocus(scrollKindOf(action))
	case keymap.ReorderUp:
		return m.reorderSelected(-1)
	case keymap.ReorderDown:
		return m.reorderSelected(1)
	case keymap.Open:
		if entry, ok := m.selectedRow(); ok && entry.isGroup {
			m.toggleCollapse()
			return m, nil
		}
		// ↵ on a session is that session's own key -- focus, or attach --
		// and stays it however many pull requests hang off the row.
		if m.enterFocuses() {
			return m.focusSelected()
		}
		return m.attachSelected()
	case keymap.StepIn:
		// Unfolding claims → first: ↵ already opens a group and focuses a
		// session, so → is the only key left for a session's work.
		if entry, ok := m.selectedRow(); ok && entry.isSession() &&
			m.hasRailWork(entry.sess) && !m.railWorkExpanded(entry.sess.ID) {
			m.toggleRailWork()
			return m, nil
		}
		// Children open after work, on the same key and by the same rule: a
		// row's own contents first, then the sessions it spawned.
		if entry, ok := m.selectedRow(); ok && entry.isSession() &&
			m.hasChildren(entry.sess.ID) && !m.childrenShown(entry.sess.ID) {
			m.toggleCollapse()
			return m, nil
		}
		if entry, ok := m.selectedRow(); ok && entry.isGroup {
			if m.collapsed[entry.group] {
				m.toggleCollapse()
			}
			return m, nil
		}
		return m.focusSelected()
	case keymap.StepOut:
		if entry, ok := m.selectedRow(); ok && entry.isSession() &&
			m.hasChildren(entry.sess.ID) && m.childrenShown(entry.sess.ID) {
			m.toggleCollapse()
			return m, nil
		}
		if entry, ok := m.selectedRow(); ok && entry.isSession() &&
			m.hasRailWork(entry.sess) && m.railWorkExpanded(entry.sess.ID) {
			m.toggleRailWork()
			return m, nil
		}
		if entry, ok := m.selectedRow(); ok && entry.isGroup && !m.collapsed[entry.group] {
			m.toggleCollapse()
		}
		return m, nil
	case keymap.Attach:
		if m.enterFocuses() {
			return m.attachSelected()
		}
		return m.focusSelected()
	case keymap.NewSession:
		return m.startNewSession()
	case keymap.NewSessionForm:
		m.openForm()
	case keymap.NewGroup:
		m.openGroupForm()
	case keymap.Fork:
		m.openFork()
	case keymap.Migrate:
		m.openMigrate()
	case keymap.Revive:
		return m.reviveSelected()
	case keymap.Dismiss:
		return m.dismissSelected()
	case keymap.Priority:
		return m.cyclePrioritySelected()
	case keymap.HandOver:
		return m.handOverSelected()
	case keymap.ReviveAll:
		return m.reviveAllDead()
	case keymap.SwitchAccount:
		m.openAccountSwitch()
	case keymap.Restart:
		return m.restartSelected()
	case keymap.Archive:
		return m.archiveSelected()
	case keymap.ArchiveAll:
		return m.archiveAllLive()
	case keymap.Restore:
		return m.restoreSelected()
	case keymap.Rescind:
		return m.rescindLatestSubmission()
	case "U", "shift+u":
		return m.undoArchive()
	case keymap.LastPane:
		return m.focusLastPane()
	case keymap.ToggleConversation:
		m.toggleConversation()
		return m, nil
	case keymap.QuickInput:
		m.openQuickMode()
	case keymap.FoldAll:
		m.toggleCollapseAll()
	case keymap.StatusFilter:
		return m, m.cycleStatusFilter()
	case keymap.Settings:
		m.openSettings()
	case keymap.Resize:
		return m.enterResizeMode()
	case keymap.ArchivedView:
		m.showArchived = !m.showArchived
		m.requestRefresh()
	case keymap.NewTerminal:
		return m.terminalKey()
	case keymap.Editor:
		return m.openEditor()
	case keymap.EmptyGroups:
		return m, m.toggleEmptyGroups()
	case keymap.Triage:
		if m.gate.on {
			// The gate is that queue plus the mode built around it, so the
			// key that leaves the queue leaves the mode with it rather than
			// stranding a gate with nothing under it.
			return m, m.disarmGate()
		}
		return m, m.toggleTriage()
	case keymap.Gate:
		return m, m.toggleGate()
	case keymap.Search:
		m.searching = true
		m.errBar.text = ""
	case keymap.ClearSearch:
		return m, m.clearSearch()
	case keymap.RenameSelf:
		return m.smartRenameSelected()
	case keymap.Rename:
		m.openRename()
	case keymap.NameSweep:
		return m, m.openNameSweep()
	case keymap.TakeOver:
		return m.takeOverAdopted()
	case keymap.Move:
		m.openMove()
	case keymap.ToggleChrome:
		return m, m.toggleChrome()
	case keymap.ToggleRail:
		return m, m.toggleRail()
	case keymap.LegendPeek:
		return m, m.beginLegendPeek()
	case keymap.Help:
		m.openHelp()
	}
	return m, nil
}

// artifactRowActions is what an artifact row lets through to the list: the
// actions that read no row at all. Moving, folding and opening are answered
// before this map is consulted.
var artifactRowActions = map[keymap.Action]bool{
	keymap.CursorUp: true, keymap.CursorDown: true,
	keymap.CursorTop: true, keymap.CursorBottom: true,
	keymap.FoldAll: true, keymap.Quit: true, keymap.ShowAllWork: true,
	keymap.NewSession: true, keymap.NewSessionForm: true, keymap.NewGroup: true,
	keymap.Search: true, keymap.ClearSearch: true, keymap.LegendPeek: true, keymap.Help: true,
	keymap.NameSweep: true, keymap.TakeOver: true, keymap.Settings: true, keymap.Resize: true,
	keymap.ArchivedView: true, keymap.StatusFilter: true,
	keymap.EmptyGroups: true, keymap.Triage: true, keymap.Gate: true, keymap.ToggleChrome: true,
	keymap.ToggleRail: true,
	// LastPane reads the pair it swaps between, not the row under the
	// cursor, so an artifact row is no reason to swallow it.
	keymap.LastPane: true,
	keymap.Rescind:  true,
}

// stepCursor is one move of the selection: the next row that is not an
// artifact, wrapping at either end.
//
// Artifacts stay rows and stay selectable -- a fold, a rebuild or a search
// can leave the cursor on one, and ↵ opens it there -- but a step never lands
// on one. On this machine that is seventy-odd references between eighty-odd
// sessions, so a rail that stepped through them would take most of a held
// key to cross the fleet.
func (m *Model) stepCursor(from, delta int) int {
	if len(m.rows) == 0 {
		return 0
	}
	step, moves := 1, delta
	if delta < 0 {
		step, moves = -1, -delta
	}
	index := from
	for move := 0; move < moves; move++ {
		for range m.rows {
			index += step
			if index < 0 {
				index = len(m.rows) - 1
			}
			if index >= len(m.rows) {
				index = 0
			}
			if !m.rows[index].isArtifact() {
				break
			}
		}
	}
	return index
}

// edgeCursor is where a jump to an end of the list lands: the first row that
// is not an artifact, or the last. A jump reads the same rule a step does,
// because a tree can end in a session's pull requests and landing there would
// hand the next key a row that refuses it.
func (m *Model) edgeCursor(delta int) int {
	step, index := 1, 0
	if delta > 0 {
		step, index = -1, len(m.rows)-1
	}
	for ; index >= 0 && index < len(m.rows); index += step {
		if !m.rows[index].isArtifact() {
			return index
		}
	}
	return m.cursor
}

// jumpCursor takes the selection to the top or the bottom of the list in one
// key, for the fleets a held j crosses too slowly to be navigation.
func (m *Model) jumpCursor(delta int) tea.Cmd {
	if len(m.rows) == 0 {
		return nil
	}
	return m.settleCursor(m.edgeCursor(delta))
}

// moveCursor shifts the selection and schedules a debounced preview
// fetch. Key-repeat only bumps the gen; a single capture runs after the
// cursor settles so holding j/k cannot pile up tmux work.
func (m *Model) moveCursor(delta int) tea.Cmd {
	if len(m.rows) == 0 {
		return nil
	}
	return m.settleCursor(m.stepCursor(m.cursor, delta))
}

// settleCursor puts the selection on a row and schedules the preview that
// follows it. Every way the cursor moves ends here, so a step and a jump
// leave the same state behind them.
func (m *Model) settleCursor(index int) tea.Cmd {
	previous, left := m.cursor, m.railCursorSess
	m.cursor = index
	if m.cursor == previous {
		return nil
	}
	// The step lands before the rebuild, never after: rebuildRows restores the
	// cursor by row identity, so it needs the row already chosen. And only
	// when a row actually moved -- most of this machine's sessions are on
	// nothing, so most steps leave the tree they are in already correct.
	if entered := m.cursorSessionID(); m.autoExpands(left) || m.autoExpands(entered) {
		m.rebuildRows()
	}
	m.clearPreviewState()
	if _, ok := m.selected(); !ok {
		return nil
	}
	m.previewGen++
	return m.schedulePreview()
}

// reorderSelected moves the selected session among its group siblings,
// or the selected group among the groups sharing its parent.
func (m *Model) reorderSelected(delta int) (tea.Model, tea.Cmd) {
	entry, ok := m.selectedRow()
	if !ok {
		return m, nil
	}
	if m.triage {
		m.errBar.text = "triage orders by status — press " + m.cap(keymap.ContextList, keymap.Triage) + " to leave triage before reordering"
		return m, nil
	}
	if refusal := m.listSortRefusal(); refusal != "" {
		m.errBar.text = refusal
		return m, nil
	}
	if entry.isRoot() {
		m.errBar.text = "root stays at the top of the list"
		return m, nil
	}
	target, ok := m.visibleReorderTarget(entry, delta)
	if !ok {
		edge := "top"
		if delta > 0 {
			edge = "bottom"
		}
		what := "group"
		if !entry.isGroup {
			what = "session"
		}
		m.errBar.text = fmt.Sprintf("%s already at the %s of its level", what, edge)
		return m, nil
	}

	var err error
	var groupSiblings []string
	if entry.isGroup {
		groupSiblings = m.knownGroupSiblings(parentGroup(entry.group))
		err = m.store.SwapGroupOrder(entry.group, target.group, groupSiblings...)
	} else {
		err = m.store.SwapSessionOrder(entry.sess.ID, target.sess.ID)
	}
	if err != nil {
		m.errBar.text = err.Error()
		return m, nil
	}
	// Mirror the swap in memory so the list redraws instantly; the next
	// poll re-reads the authoritative order from the store.
	if entry.isGroup {
		m.materializeGroupsLocal(groupSiblings)
		m.swapGroupLocal(entry.group, target.group)
	} else {
		m.swapSessionLocal(entry.sess.ID, target.sess.ID)
	}
	m.errBar.text = ""
	m.rebuildRows()
	m.requestRefresh()
	return m, nil
}

// visibleReorderTarget finds the next rendered sibling. Filters and archive
// scope therefore cannot turn a successful reorder into an invisible swap.
func (m *Model) visibleReorderTarget(entry treeRow, delta int) (treeRow, bool) {
	step := 1
	if delta < 0 {
		step = -1
	}
	start, _ := m.selectedIndex()
	for i := start + step; i >= 0 && i < len(m.rows); i += step {
		candidate := m.rows[i]
		// An artifact is not a sibling of anything, and it carries the very
		// session the scan started from: left in, it would offer that session
		// itself as its own swap target.
		if candidate.isArtifact() {
			continue
		}
		if candidate.isRoot() {
			// parentGroup("") is "" too, so root would match a top-level
			// group as its own sibling.
			continue
		}
		if entry.isGroup {
			if candidate.isGroup && parentGroup(candidate.group) == parentGroup(entry.group) {
				return candidate, true
			}
			continue
		}
		if !candidate.isGroup && candidate.sess.Group == entry.sess.Group && candidate.sess.ParentID == entry.sess.ParentID && !store.Linked(entry.sess, candidate.sess) {
			return candidate, true
		}
	}
	return treeRow{}, false
}

func (m *Model) knownGroupSiblings(parent string) []string {
	paths := groupClosure(m.groups, m.sessions)
	return childIndex(paths, m.groups)[parent]
}

func (m *Model) materializeGroupsLocal(paths []string) {
	known := make(map[string]bool, len(m.groups))
	for _, group := range m.groups {
		known[group] = true
	}
	for _, path := range paths {
		if !known[path] {
			m.groups = append(m.groups, path)
			known[path] = true
		}
	}
}

func (m *Model) swapSessionLocal(id, targetID string) {
	ordered, err := store.SwapLinkedSessions(m.sessions, id, targetID)
	if err != nil {
		m.errBar.text = err.Error()
		return
	}
	m.sessions = ordered
}

func (m *Model) swapGroupLocal(path, targetPath string) {
	current, target := -1, -1
	for i, name := range m.groups {
		switch name {
		case path:
			current = i
		case targetPath:
			target = i
		}
	}
	if current >= 0 && target >= 0 {
		m.groups[current], m.groups[target] = m.groups[target], m.groups[current]
	}
}

func (m *Model) toggleCollapse() {
	entry, ok := m.selectedRow()
	if !ok {
		return
	}
	// A parent's children fold under the same key its group does. The
	// nearer container wins: on a row that has children, this key is about
	// those children, and the group is still one row up.
	if entry.isSession() && m.hasChildren(entry.sess.ID) {
		m.setChildrenFolded(entry.sess.ID, m.childrenShown(entry.sess.ID))
		m.persistCollapsed()
		m.rebuildRows()
		return
	}
	path := entry.group
	if !entry.isGroup {
		path = entry.sess.Group
	}
	if path == "" {
		return
	}
	m.collapsed[path] = !m.collapsed[path]
	m.persistCollapsed()
	m.rebuildRows()
}

// toggleCollapseAll folds every group and every session's work when any of
// them is open, and unfolds them all when they are already collapsed, so one
// key flips the whole tree.
func (m *Model) toggleCollapseAll() {
	m.parkCursorOffArtifact()
	groups := groupClosure(m.groups, m.sessions)
	collapse := !m.allFoldsCollapsed()
	for group := range groups {
		m.collapsed[group] = collapse
	}
	for _, sess := range m.sessions {
		if !m.hasRailWork(sess) {
			continue
		}
		if collapse {
			// Folding forgets the decision rather than writing one. Writing
			// "folded" on all eighty-odd would leave nothing undecided for
			// the cursor to open, and F would be a door that only shuts.
			m.clearWorkFold(sess.ID)
			continue
		}
		m.setWorkFolded(sess.ID, false)
	}
	for _, sess := range m.sessions {
		if !m.hasChildren(sess.ID) {
			continue
		}
		if collapse {
			// Children fold by default, so forgetting the decision is
			// already "folded" -- and it leaves the cursor free to open one.
			m.clearChildFold(sess.ID)
			continue
		}
		m.setChildrenFolded(sess.ID, false)
	}
	m.persistCollapsed()
	m.rebuildRows()
}

// allFoldsCollapsed reports whether the whole tree is folded, which is
// what decides the direction F takes and the label the footer offers. A
// tree with nothing foldable in it is not folded: there is nothing to
// unfold, and the label must not offer it.
func (m *Model) allFoldsCollapsed() bool {
	any := false
	for group := range groupClosure(m.groups, m.sessions) {
		if !m.collapsed[group] {
			return false
		}
		any = true
	}
	for _, sess := range m.sessions {
		if !m.hasRailWork(sess) {
			continue
		}
		// The decision, not the rendering: the session under the cursor is
		// open because nobody has decided anything about it, and reading it
		// as open would make F alternate with where the cursor happens to be.
		if folded, decided := m.workFoldDecision(sess.ID); decided && !folded {
			return false
		}
		any = true
	}
	for _, sess := range m.sessions {
		if !m.hasChildren(sess.ID) {
			continue
		}
		if folded, decided := m.childFoldDecision(sess.ID); decided && !folded {
			return false
		}
		any = true
	}
	return any
}

// pasteFocused is the seam tests swap to observe pastes into the pane.
var pasteFocused = func(driver *tmux.Driver, id, text string) error {
	return driver.Paste(id, text)
}

// cycleStatusFilter advances the list status filter (all → attention → …).
// Modes live in statusFilterCycle so new ones only need a const and a
// matches case; this handler stays the same.
func (m *Model) cycleStatusFilter() tea.Cmd {
	previousKey := ""
	if entry, ok := m.selectedRow(); ok {
		previousKey = rowKey(entry)
	}
	m.statusFilter = m.statusFilter.next()
	m.rebuildRows()
	return m.afterListFilter(previousKey)
}

// toggleEmptyGroups hides or restores group rows whose subtree has no
// sessions in the current active/archive view. It never changes the store.
func (m *Model) toggleEmptyGroups() tea.Cmd {
	previousKey := ""
	if entry, ok := m.selectedRow(); ok {
		previousKey = rowKey(entry)
	}
	m.hideEmptyGroups = !m.hideEmptyGroups
	m.rebuildRows()
	return m.afterListFilter(previousKey)
}

// afterListFilter keeps the preview tied to the selection when a filter
// change leaves the cursor on the same row, and refreshes it when not.
func (m *Model) afterListFilter(previousKey string) tea.Cmd {
	currentKey := ""
	if entry, ok := m.selectedRow(); ok {
		currentKey = rowKey(entry)
	}
	if currentKey == previousKey {
		return nil
	}

	m.clearPreviewState()
	m.previewGen++
	m.syncPollInput()
	if _, ok := m.selected(); ok {
		return m.schedulePreview()
	}
	return nil
}

// warn carries a PrepareAttach failure: shown to the user, but the attach
// still proceeds, unlike err which cancels it.
type reattachPreparedMsg struct {
	sessID string
	err    error
	warn   string
}

// captureClipboardImage is the seam the quick bar uses to save a pasted
// image to a temp file; tests swap it for a fake.
var captureClipboardImage = clipboard.SaveImage

const listDensitySetting = "list_density"

const focusKeySetting = "focus_key"

const quickCloseSetting = "quick_prompt_close"

// hiddenToolsSetting lists CLI tools omitted from new-session pickers
// (comma-separated names). Empty means every configured tool is shown.
const hiddenToolsSetting = "hidden_tools"

func (m *Model) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.searching = false
	case "esc":
		m.searching = false
		return m, m.clearSearch()
	case "backspace":
		if runes := []rune(m.search); len(runes) > 0 {
			m.search = string(runes[:len(runes)-1])
		}
		m.rebuildRows()
		return m, m.scheduleHistorySearch()
	default:
		if text := msg.Key().Text; text != "" {
			m.search += text
			m.rebuildRows()
			return m, m.scheduleHistorySearch()
		}
	}
	return m, nil
}

// clearSearch drops the query and re-lists. A query that outlives its field
// with no way back is what makes filtered-away sessions read as sessions
// that are gone, so esc answers from the list as well as from the field.
func (m *Model) clearSearch() tea.Cmd {
	if m.search == "" {
		return nil
	}
	previousKey := ""
	if entry, ok := m.selectedRow(); ok {
		previousKey = rowKey(entry)
	}
	m.search = ""
	m.historyHits, m.historyQuery = nil, ""
	m.historySeq++
	m.rebuildRows()
	return m.afterListFilter(previousKey)
}
