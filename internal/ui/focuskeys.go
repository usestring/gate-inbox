// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// focusKeyID is a key as bubbletea reports it: a code plus the modifiers
// that are part of the tmux name. Alt is left out - it prefixes whatever
// name the rest of the key resolves to.
type focusKeyID struct {
	code rune
	mod  tea.KeyMod
}

// focusNamedKeys maps bubbletea keys to tmux send-keys key names. Ctrl+I and
// Ctrl+M need no entry of their own: the terminal sends them as the Tab and
// Enter bytes and bubbletea decodes them back to those keys, so they reach
// the pane under those names.
var focusNamedKeys = map[focusKeyID]string{
	{code: tea.KeyEnter}:                    "Enter",
	{code: tea.KeyTab}:                      "Tab",
	{code: tea.KeyTab, mod: tea.ModShift}:   "BTab",
	{code: tea.KeyBackspace}:                "BSpace",
	{code: tea.KeyEsc}:                      "Escape",
	{code: tea.KeyUp}:                       "Up",
	{code: tea.KeyDown}:                     "Down",
	{code: tea.KeyLeft}:                     "Left",
	{code: tea.KeyRight}:                    "Right",
	{code: tea.KeyUp, mod: tea.ModShift}:    "S-Up",
	{code: tea.KeyDown, mod: tea.ModShift}:  "S-Down",
	{code: tea.KeyLeft, mod: tea.ModShift}:  "S-Left",
	{code: tea.KeyRight, mod: tea.ModShift}: "S-Right",
	{code: tea.KeyUp, mod: tea.ModCtrl}:     "C-Up",
	{code: tea.KeyDown, mod: tea.ModCtrl}:   "C-Down",
	{code: tea.KeyLeft, mod: tea.ModCtrl}:   "C-Left",
	{code: tea.KeyRight, mod: tea.ModCtrl}:  "C-Right",
	{code: tea.KeyHome}:                     "Home",
	{code: tea.KeyEnd}:                      "End",
	{code: tea.KeyPgUp}:                     "PPage",
	{code: tea.KeyPgDown}:                   "NPage",
	{code: tea.KeyDelete}:                   "DC",
	{code: tea.KeyInsert}:                   "IC",
	{code: tea.KeyF1}:                       "F1",
	{code: tea.KeyF2}:                       "F2",
	{code: tea.KeyF3}:                       "F3",
	{code: tea.KeyF4}:                       "F4",
	{code: tea.KeyF5}:                       "F5",
	{code: tea.KeyF6}:                       "F6",
	{code: tea.KeyF7}:                       "F7",
	{code: tea.KeyF8}:                       "F8",
	{code: tea.KeyF9}:                       "F9",
	{code: tea.KeyF10}:                      "F10",
	{code: tea.KeyF11}:                      "F11",
	{code: tea.KeyF12}:                      "F12",
}

func init() {
	for r := 'a'; r <= 'z'; r++ {
		focusNamedKeys[focusKeyID{code: r, mod: tea.ModCtrl}] = "C-" + string(r)
	}
}

// focusKeyCommand encodes one key press as a tmux send-keys command for
// the focused session. Text goes as hex byte codes (-H), which sidesteps
// tmux command-line quoting entirely; special keys go by tmux key name.
// ok is false for keys tmux cannot represent, which are dropped.
func focusKeyCommand(target string, msg tea.KeyMsg) (string, bool) {
	key := msg.Key()
	if text := focusKeyText(key); text != "" {
		raw := []byte(text)
		codes := make([]string, 0, len(raw)+1)
		if key.Mod.Contains(tea.ModAlt) {
			// Alt arrives as an ESC prefix on the wire; replay it as one.
			codes = append(codes, "1b")
		}
		for _, b := range raw {
			codes = append(codes, fmt.Sprintf("%02x", b))
		}
		return "send-keys -t " + target + " -H " + strings.Join(codes, " "), true
	}
	name, ok := focusNamedKeys[focusKeyID{code: key.Code, mod: key.Mod &^ tea.ModAlt}]
	if !ok {
		return "", false
	}
	if key.Mod.Contains(tea.ModAlt) {
		name = "M-" + name
	}
	return "send-keys -t " + target + " " + name, true
}

// focusKeyText returns the characters a key stands for, or "" for a key that
// carries none and has to go by name. An alt-modified printable rune has to
// be rebuilt from its code: bubbletea clears Key.Text when it folds the ESC
// prefix into the modifier, and it reports a shifted rune as the unshifted
// one plus ModShift, so the shifted code is the character actually typed.
func focusKeyText(key tea.Key) string {
	if key.Text != "" {
		return key.Text
	}
	if !key.Mod.Contains(tea.ModAlt) || key.Mod&^(tea.ModAlt|tea.ModShift) != 0 {
		return ""
	}
	code := key.Code
	if key.Mod.Contains(tea.ModShift) {
		if key.ShiftedCode != 0 {
			code = key.ShiftedCode
		} else {
			code = unicode.ToUpper(code)
		}
	}
	if code < ' ' || code == 0x7f || code > unicode.MaxRune {
		return ""
	}
	return string(code)
}

// focusSelected enters focus mode: keys go to the selected session's pane
// while the manager, its rail and its live preview stay on screen.
func (m *Model) focusSelected() (tea.Model, tea.Cmd) {
	sess, ok := m.selected()
	if !ok {
		return m, nil
	}
	if sess.Archived {
		m.errBar.text = m.archivedFocusHint()
		return m, nil
	}
	if !m.tmux.Exists(sess.ID) {
		m.errBar.text = m.deadSessionHint()
		return m, nil
	}
	m.errBar.text = ""
	if m.triage {
		// A drain hands the session over and the operator starts reading and
		// typing an answer into it. Acknowledging here spends the alert
		// immediately, and a poll or so later -- which is about when the
		// first characters go in -- the row they are working in drops from
		// finished to idle and slides down the queue under them. Nothing
		// about being handed a session says it has been dealt with, so the
		// acknowledgement waits for the one act that does say it: leaving.
		//
		// Held whatever the status, not only when finished: the write is
		// conditional on finished in the store and the next poll drops a
		// hold on a session that is not, so holding costs nothing, while
		// writing here would put the drain's entry -- pressing "i" over a
		// queue -- behind whichever poll pass owns the connection.
		m.heldAckID = sess.ID
	} else if err := m.store.AcknowledgeFinished(sess.ID); err != nil {
		m.errBar.text = err.Error()
		return m, nil
	}
	if m.gate.on && m.focusedID != sess.ID && m.quick.active {
		m.quick.active = false
		m.quick.release()
	}
	m.mode = modeFocus
	// Recorded here rather than at the top: every guard above returns
	// without a pane on screen, and a refusal must not count as a session
	// the operator was on. See lastpane.go.
	m.noteFocused(sess.ID)
	// Focusing is somebody sitting down at the pane, so it starts on the
	// fast cadence rather than waiting for the first keystroke to earn it.
	m.noteFocusActivity()
	m.sel = focusSelection{}
	m.copied = 0
	m.cursorOn = true
	m.focusScroll = 0
	// Pane facts read from a previously focused session must not route this
	// one's wheel. The read below reports the real values a moment later;
	// facts already held for this same session stay, so re-entering a pane
	// does not route it as a plain pane with no history in between.
	if m.pane.forID != sess.ID {
		m.pane.mouse = false
		m.pane.motion = false
		m.pane.sgr = false
		m.pane.history = 0
	}
	// Mouse reporting makes the pane a closed window: clicks land here
	// instead of the host terminal, so a drag selects pane text alone and
	// never the rail beside it. The view declares it for the whole app, so
	// entering focus mode no longer has to arm it.
	// Entering focus is the moment the pane has to be at the preview
	// panel's geometry: the operator is about to read and type into it, and
	// a pane still at its own window's size paints a band across the top of
	// a taller panel. Pinning is now tied to being previewed rather than
	// done to every session up front, so this is where a focused pane earns
	// its size.
	return m, tea.Batch(m.cursorBlink(), m.resizeSessions(), m.paneStateCmd(sess.ID))
}

// caretAtInputStart reports whether the agent's caret sits at the head of
// its prompt, with nothing but the prompt marker to its left. Left is a
// no-op for the agent there, which is what frees the key to mean "back to
// the list" without ever costing a keystroke inside the prompt: anywhere
// else it still moves the caret.
//
// A wrapped prompt's continuation rows carry no marker, so a caret at the
// head of one of them forwards Left as usual and reaches the end of the
// row above.
func (m *Model) caretAtInputStart(sessID, tool string) bool {
	rows, y, ok := m.caretRows(sessID)
	if !ok {
		return false
	}
	row, caretX := ansi.Strip(rows[y]), m.pane.cursor.x
	prefix, ok := m.engine.InputPrefix(tool, row)
	if !ok {
		return false
	}
	if textBeforeCaret(m.engine, tool, row, caretX) || caretX < cellWidth(prefix) {
		return false
	}
	return m.composerAboveIsBlank(tool, rows, y)
}

// composerAboveIsBlank reports whether the rows of the composer above the
// caret's own are empty, which is how a message that has wrapped is told from
// a prompt nobody has typed at.
//
// It exists because a boxed composer marks EVERY row it owns, not just the
// first. A tool whose marker is drawn once (claude's "\u276f") leaves a wrapped
// continuation row unmarked and never reaches here, but opencode draws its bar
// down the whole box, so a caret resting at the head of a blank second line --
// the operator pressed Enter for a newline mid-message -- looks exactly like a
// caret at the head of an empty prompt. Reading only the caret's row, Left
// would abandon a half-written message to go back to the list.
func (m *Model) composerAboveIsBlank(tool string, rows []string, y int) bool {
	for i := y - 1; i >= 0; i-- {
		row := ansi.Strip(rows[i])
		prefix, ok := m.engine.InputPrefix(tool, row)
		if !ok {
			return true
		}
		if strings.TrimSpace(row[len(prefix):]) != "" {
			return false
		}
	}
	return true
}

// caretRow is the pane row the caret is standing on, stripped of styling,
// with the column it stands at. Every judgement about what a key means to the
// pane starts here, and every one of them needs the same three things to be
// true first: a status engine to read the row with, a cursor position that
// belongs to this session, and a pane showing its own live bottom. A
// scrolled-back pane is showing history, so the row under the caret is not
// where the next keystroke lands.
func (m *Model) caretRow(sessID string) (string, int, bool) {
	rows, y, ok := m.caretRows(sessID)
	if !ok {
		return "", 0, false
	}
	return ansi.Strip(rows[y]), m.pane.cursor.x, true
}

// caretRows is caretRow's pane: its rows as captured, with the index of the
// one the caret stands on. Anything that has to read the rows around the
// caret -- a composer that has wrapped over several of them -- needs the pane
// rather than the single row.
//
// The rows come back styled. Stripping is left to whoever reads a row because
// this runs on the keystroke path, several times per key, and the caret's
// neighbourhood is a handful of rows out of a screenful: stripping the whole
// capture up front would spend the pane's entire height on every press to
// read three rows of it.
func (m *Model) caretRows(sessID string) ([]string, int, bool) {
	if m.engine == nil || !m.pane.cursor.ok || m.pane.forID != sessID || m.scrolledBack() {
		return nil, 0, false
	}
	rows := strings.Split(strings.TrimSuffix(m.preview, "\n"), "\n")
	if m.pane.cursor.y < 0 || m.pane.cursor.y >= len(rows) {
		return nil, 0, false
	}
	return rows, m.pane.cursor.y, true
}

// textTypedAtPrompt reports whether the caret sits on the tool's input line
// with something written ahead of it: a message half or wholly composed, as
// opposed to an empty prompt or a dialog marker.
func (m *Model) textTypedAtPrompt(sessID, tool string) bool {
	row, caretX, ok := m.caretRow(sessID)
	if !ok {
		return false
	}
	if _, ok := m.engine.InputPrefix(tool, row); !ok {
		return false
	}
	return textBeforeCaret(m.engine, tool, row, caretX)
}

// selectionDialogUp reports whether the session is stopped on a dialog
// waiting to be chosen from: the caret parked short of the prompt marker
// rather than past it, with the status rules agreeing the session is waiting
// on a person.
//
// The rule match is what proves a dialog is up at all. Without it a composer
// whose caret happens to sit inside the marker would read the same.
func (m *Model) selectionDialogUp(sessID, tool string) bool {
	row, caretX, ok := m.caretRow(sessID)
	if !ok {
		return false
	}
	prefix, ok := m.engine.InputPrefix(tool, row)
	if !ok || caretX >= cellWidth(prefix) {
		return false
	}
	pane := strings.Join(m.paneTextLines(), "\n")
	state, matched := m.engine.RuleMatch(tool, pane)
	return matched && state == status.Waiting
}

// leftLeavesFocus reports whether Left means "back to the list" rather than a
// keystroke the pane wanted: at the head of a prompt, and on a dialog that
// does nothing with the horizontal arrows.
//
// The dialog half is the one that matters in a drain. A selection dialog puts
// the caret on its own marker, which is not the head of a prompt, so Left used
// to reach the agent and the operator stayed pinned in the session -- exactly
// when they most want to step out and come back, since the session is asking
// them something. A dialog that navigates with the arrows still keeps them.
//
// A dialog with a question stepper answers that question itself, and better
// than its legend does: Left is the previous question there, so it leaves only
// on the first entry, where the dialog has nothing to step back to and the key
// is going spare.
func (m *Model) leftLeavesFocus(sessID, tool string) bool {
	if m.caretAtInputStart(sessID, tool) {
		return true
	}
	if tool == "opencode" && m.opencodeQuestionDialogUp() {
		// A custom answer being typed into the dialog stays the pane's:
		// Left there is line editing, not a spare key.
		if row, caretX, ok := m.caretRow(sessID); ok {
			if _, isInput := m.engine.InputPrefix(tool, row); isInput && textBeforeCaret(m.engine, tool, row, caretX) {
				return false
			}
		}
		return true
	}
	if !m.selectionDialogUp(sessID, tool) {
		return false
	}
	if first, ok := m.engine.DialogStepIsFirst(tool, m.preview); ok {
		return first
	}
	return !m.engine.DialogOwnsArrows(tool, strings.Join(m.paneTextLines(), "\n"))
}

// opencodeQuestionLegend is the legend an opencode question dialog closes
// with. It mirrors the waiting rule the shipped config reads the dialog by --
// "enter <verb>  esc dismiss", the verb being the dialog's own (submit,
// toggle, confirm) -- so the two agree on what counts as the dialog.
var opencodeQuestionLegend = regexp.MustCompile(`(?m)^.*\benter (?:submit|toggle|confirm)[ \x{A0}]+esc dismiss[ \x{A0}]*$`)

// opencodeQuestionDialogUp reports whether an opencode session is stopped on
// one of its question dialogs.
//
// The caret cannot say so: opencode parks it outside the dialog it is asking
// from, at the end of the "→ Asked N question(s)" summary line above the
// composer box, so selectionDialogUp's caret-on-the-marker rule never fires
// there and Left would be forwarded into a dialog that does nothing with the
// horizontal arrows. The pane text says it instead, read the same way the
// status rules read it.
//
// The permission overlay is the counter-case and is excluded two ways: its
// "⇆ select" legend owns Left (see arrow_dialog_line), and it draws no
// enter-verb/esc-dismiss legend of its own.
func (m *Model) opencodeQuestionDialogUp() bool {
	if m.engine == nil {
		return false
	}
	pane := strings.Join(m.paneTextLines(), "\n")
	if pane == "" {
		// No painted box yet -- the first frame before a render records
		// one. The control-mode capture carries styling the rules do
		// not read, so strip it the way the preview path does.
		pane = ansi.Strip(m.preview)
	}
	state, matched := m.engine.RuleMatch("opencode", pane)
	if !matched || state != status.Waiting {
		return false
	}
	if m.engine.DialogOwnsArrows("opencode", pane) {
		return false
	}
	return opencodeQuestionLegend.MatchString(pane)
}

// textBeforeCaret reports whether anything but blanks sits between a tool's
// prompt marker and the caret on this row, which is how a line someone has
// half written is told from an empty prompt. A row that is not an input line
// carries no such text. Claude pads its marker with a non-breaking space, so
// blank means any space rune, not the ASCII one alone; tmux trims a row's
// trailing blanks, so a row that ends before the caret is blank the rest of
// the way.
func textBeforeCaret(engine *status.Engine, tool, row string, caretX int) bool {
	prefix, ok := engine.InputPrefix(tool, row)
	if !ok {
		return false
	}
	return status.TextBetweenCells(row, cellWidth(prefix), caretX)
}

// leaveFocus returns to the list. Mouse reporting stays on: handing it back
// to the terminal here would let a wheel notch scroll the manager out of
// view, so the list swallows the wheel instead.
func (m *Model) leaveFocus() tea.Cmd {
	if m.gate.on && m.quick.active {
		m.quick.active = false
		m.quick.release()
	}
	m.mode = modeList
	m.sel = focusSelection{}
	m.pending = pendingClick{}
	m.clearForwardingMouse()
	m.copied = 0
	return m.releaseHeldAck()
}

// dropHeldAckOnNewTurn lets go of a held acknowledgement once a poll reports
// the session doing something other than resting on the turn it was held
// for. The operator answered, the agent went back to work, and whatever it
// ends on next is a turn they have not been shown -- so leaving must raise
// that one rather than spend the hold on it. A session that has left the
// board is dropped for the same reason: there is nothing to acknowledge.
func (m *Model) dropHeldAckOnNewTurn() {
	if m.heldAckID == "" {
		return
	}
	for _, sess := range m.sessions {
		if sess.ID != m.heldAckID {
			continue
		}
		if sess.Status != status.Finished {
			m.heldAckID = ""
		}
		return
	}
	m.heldAckID = ""
}

// releaseHeldAck spends the acknowledgement focusSelected held back. Every
// way out of a focused session runs through leaveFocus -- ctrl+q on to the
// next one, ctrl+\ out of the drain, the pane being archived or vanishing --
// so this is the only place it is spent, and clearing the field here rather
// than inside the command keeps the next session's hold from overwriting it
// when an advance batches the two together.
//
// The write is deferred off the event loop for the reason persistTriage's is:
// a poll pass owns the single connection for as long as its own reads take,
// and a key that waits behind it feels dead. A session archived or killed in
// the meantime has nothing left to acknowledge.
func (m *Model) releaseHeldAck() tea.Cmd {
	id := m.heldAckID
	m.heldAckID = ""
	if id == "" {
		return nil
	}
	stor := m.store
	return deferStoreWrite(func() error {
		return ignoreDeletedSession(stor.AcknowledgeFinished(id))
	})
}

// handleFocusKey forwards every key into the focused pane. Ctrl+Q and
// ctrl+\ return to the list, alt+o opens the editor and ctrl+x archives the
// session, and every plain character - q included - reaches the agent.
//
// § is ctrl+q's one-press alias and reads the same in both modes: in triage
// it hands over, outside it returns to the manager. In triage mode ctrl+q
// keeps going instead of stopping: it hands the user the next session that
// needs them, which is the whole point of draining a queue (see mute.go).
// ctrl+\ stays the way out of the run.
//
// A forwarded key paints nothing. The character it stands for is not on
// screen yet -- it appears in the frame the echo chase brings back once the
// pane has painted it, which is the frame the operator actually reads -- and
// the caret was already lit, because typing is what lights it. So the frame
// after a forwarded key is the frame already on screen, and painting it
// again was the whole cost of a keystroke: the forward itself is a write
// down a pipe the manager already holds. TestFocusedKeystrokeLatencyBreakdown
// is the measurement.
func (m *Model) handleFocusKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	action, bound := m.action(keymap.ContextFocus, msg)
	if m.gate.on {
		if bound && action == keymap.ToggleGateInput {
			m.gate.menu = !m.gate.menu
			m.sel = focusSelection{}
			m.pending = pendingClick{}
			m.clearForwardingMouse()
			return m, nil
		}
		if m.showsConversation() {
			if bound && action == keymap.ToggleConversation {
				m.toggleConversation()
				return m, nil
			}
			if m.quick.active && !(bound && (action == keymap.Leave || action == keymap.LeaveHard || action == keymap.HandOver)) {
				if press, ok := msg.(tea.KeyPressMsg); ok {
					return m.handleQuickKey(press)
				}
				return m, nil
			}
			if keyName(msg) == "space" {
				m.openQuickMode()
				return m, nil
			}
		}
		// A snippet is the one unbound key the menu lets through: a bare ±
		// is unmodified and would otherwise be read as a menu letter.
		_, snip := m.snippetFor(msg.String())
		if m.gate.menu && !bound && !snip && msg.Key().Mod&^tea.ModShift == 0 {
			if menuAction, ok := gateMenuAction(keyName(msg)); ok {
				action, bound = menuAction, true
			} else {
				return m, nil
			}
		}
		if m.gate.menu && !bound && !snip {
			return m, nil
		}
	}
	if bound && (action == keymap.Leave || action == keymap.LeaveHard || action == keymap.HandOver) {
		leftID := ""
		sess, onRow := m.selected()
		if onRow {
			leftID = sess.ID
		}
		if action == keymap.LeaveHard || !m.advancesOnLeave() {
			// ctrl+\ is the way out of the run, and out of the gate with
			// it: the operator asking to stop here means the drain is over,
			// so the rail and the queue they had before it come back rather
			// than being left for them to undo by hand.
			if m.gate.on {
				return m, tea.Batch(m.leaveFocus(), m.disarmGate())
			}
			return m, m.leaveFocus()
		}
		// Handing the session over is the operator saying they are done with
		// it. Muting it rather than skipping it for one hop is what makes the
		// queue converge: the status it is left showing lags a poll behind
		// whatever was just typed into it, so a plain skip only defers the
		// return by one session. See mute.go.
		if onRow {
			return m, m.handOverFocused(sess)
		}
		return m, tea.Batch(m.leaveFocus(), m.advanceTriage(leftID))
	}
	sess, ok := m.selected()
	if !ok {
		return m, m.leaveFocus()
	}
	if bound && action == keymap.Rescind {
		return m.rescindLatestSubmission()
	}
	// A snippet answers the pane the same way. This handler is why they are
	// confined to one chord -- everything it does not claim is forwarded to
	// the agent -- and the snippets package doc has that reasoning in full.
	if snip, ok := m.snippetFor(msg.String()); ok {
		m.noteFocusActivity()
		// A snippet is a whole answer -- the sentence the drain exists to
		// send -- so auto-proceed treats it as one. A send that failed has
		// left the session unanswered and its reason in the bar, and handing
		// it over would carry both off screen.
		if m.sendSentence(sess, snip.Text, snip.Quoted()) && m.autoProceeds() {
			return m, m.handOverFocused(sess)
		}
		return m, nil
	}
	// alt+o opens the session's directory in an editor, matching the binding
	// a real attach gets, and the list's own o; ctrl+x archives, matching the
	// list's x. A windowed editor leaves the focus where it is; one that
	// draws in the terminal takes it back on exit.
	//
	// The editor stays on alt because everything this handler does not claim
	// reaches the agent, and alt+o is a key nothing behind it wanted. Archive
	// is the deliberate exception: ctrl+x is readline's kill-line prefix and an
	// agent's line editor answers it, so claiming it does take a working key
	// away from the pane. A one-chord archive from inside a focused session is
	// worth that trade; alt+x goes back to the agent to pay for it.
	//
	// Matched on the name rebuilt from the code and the modifiers rather
	// than on String(), which answers with Key.Text where a terminal reports
	// one - and an enhanced keyboard protocol does report text for an
	// alt-modified rune, which would leave the key reading as a plain "o"
	// and forwarding on. See keyName.
	if bound {
		switch {
		case action == keymap.Archive:
			return m.archiveFocused(sess)
		case action == keymap.NewSession:
			// Add a session without giving up the queue: the spawn's own
			// landing is redirected back here when a gate is armed. See
			// gate.go.
			return m.gateSpawn()
		case action == keymap.CopySessionID:
			return m.copySessionID(sess)
		case action == keymap.LastPane:
			// Back to the gate you just stepped past, the way v1's `,`
			// reopened the previous one.
			return m.gateBack()
		case action == keymap.Editor:
			return m.openEditor()
		case action == keymap.Dismiss:
			// The skip a one-at-a-time drain needs: a session that turns
			// out to want nothing is taken off the queue from inside it,
			// rather than left first and dismissed from a row the gate is
			// not showing. See gate.go.
			return m, m.dismissFocused(sess)
		case action == keymap.ToggleChrome:
			// The footer is the manager's own row, not the agent's: hiding
			// it from here gives the pane the rows back without leaving
			// the session to say so.
			return m, m.toggleChrome()
		case action == keymap.ToggleRail:
			// The list beside the pane is the manager's own columns, and
			// the same argument runs sideways: a session that wants the
			// width gets it without a trip back to the board.
			return m, m.toggleRail()
		case isScrollAction(action):
			// The pane scrolls for the operator with no wheel: a phone, or a
			// terminal that sends a swipe as arrows.
			return m, m.keyScrollFocus(scrollKindOf(action))
		}
	}
	key := msg.Key()
	if bound && action == keymap.BackAtPrompt && key.Mod == 0 && m.leftLeavesFocus(sess.ID, sess.Tool) {
		return m, m.leaveFocus()
	}
	// Whether this key answers the session, read before anything below moves:
	// it is the pane as the operator saw it when they pressed the key that
	// says what the press meant -- a dialog to choose from, or a line with a
	// message written on it. Once the key has landed the dialog is gone and
	// the line is clear, and the scrolled-back pane the next lines pull back
	// to its live bottom was showing history rather than where the key lands.
	submitted := m.answersFocused(sess, msg)
	answered := m.autoProceeds() && submitted
	// Typing puts the cursor back on: a caret that blinks out mid-keystroke
	// reads as a dropped character.
	quiet := m.cursorOn
	m.cursorOn = true
	// Keystrokes land at the live bottom, so the view follows them there.
	var resume tea.Cmd
	if m.scrolledBack() {
		quiet = false
		m.focusScroll = 0
		resume = m.requestFocusRegion(sess.ID)
	}
	command, ok := focusKeyCommand(m.tmux.TargetName(sess.ID), msg)
	if !ok {
		return m, resume
	}
	// What the pane looked like before this key, which is what the chase
	// below measures the echo against. Read here rather than inside the
	// chase so it is unambiguously "before": the send is on this goroutine
	// and in order, and a baseline taken after it could already contain the
	// echo it is supposed to detect.
	//
	// Only when this key is arming a chase. The baseline is a forked capture
	// and so is every look the chase takes, and a held key repeats faster
	// than a chase completes: one each would fork dozens of times a second,
	// which is the shape that pinned a core on the wheel. A key that arms
	// nothing sets echoPending instead, and the chase already out lands a
	// trailing one for it -- so no character waits on a tick.
	baseline := ""
	chase := resume == nil && !m.focusChasing
	if chase {
		baseline = m.echoBaseline(sess.ID)
	} else if resume == nil {
		m.echoPending = true
	}
	m.poller.noteOperatorInput(sess.ID)
	// SendRawAt takes the pooled pipe where there is one and forks where
	// there is not, so this is one write down a pipe the manager already
	// holds in the case that matters.
	if err := m.sendFocusKey(sess.ID, command); err != nil {
		m.errBar.text = err.Error()
		quiet = false
		// The answer never reached the pane, so the session is still asking
		// and there is nothing to hand over.
		answered = false
		submitted = false
	}
	if submitted {
		m.noteSubmission(sess)
	}
	// The answer is in. Auto-proceed spends it the way § does -- mute, leave,
	// enter the next session that needs a person -- so a drain is one answer
	// after another rather than an answer and a handover each time. Nothing
	// here chases the echo: the pane this key was typed into is no longer the
	// one on screen.
	if answered {
		return m, m.handOverFocused(sess)
	}
	// The key is the pane's now, so start looking for what it did with it.
	// This is the whole echo path: no timer, no client, and no cost at all
	// on a pane nobody is typing into.
	m.noteFocusActivity()
	// A scrolled-back pane is being pulled back to its live bottom by this
	// very key, and the region read is what paints that; chasing the live
	// frame alongside it would race the two. That is the other half of what
	// chase already answers.
	var echo tea.Cmd
	if chase {
		echo = m.startChase(sess, baseline)
	}
	if quiet {
		m.frameUnchanged()
	}
	return m, tea.Batch(resume, echo)
}

// handleFocusPaste sends a bracketed paste into the focused pane through the
// tmux buffer: as raw key bytes its newlines would land as Enter presses and
// submit the agent's prompt.
func (m *Model) handleFocusPaste(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	if m.showsConversation() {
		return m, nil
	}
	sess, ok := m.selected()
	if !ok {
		return m, m.leaveFocus()
	}
	quiet := m.cursorOn
	m.cursorOn = true
	// Pasted text lands at the live bottom, so the view follows it there.
	var resume tea.Cmd
	if m.scrolledBack() {
		quiet = false
		m.focusScroll = 0
		resume = m.requestFocusRegion(sess.ID)
	}
	// The same "before this key" baseline the keystroke path takes, and for
	// the same reason: the frame on screen is not a reliable picture of the
	// pane once anything else has touched it.
	baseline := ""
	chase := resume == nil && !m.focusChasing
	if chase {
		baseline = m.echoBaseline(sess.ID)
	} else if resume == nil {
		m.echoPending = true
	}
	m.poller.noteOperatorInput(sess.ID)
	if err := m.sendFocusPaste(sess.ID, msg.Content); err != nil {
		m.errBar.text = err.Error()
		quiet = false
	}
	m.noteFocusActivity()
	var echo tea.Cmd
	if chase {
		echo = m.startChase(sess, baseline)
	}
	if quiet {
		m.frameUnchanged()
	}
	return m, tea.Batch(resume, echo)
}

// archiveFocused files the focused session away from inside its own pane, so
// a triage drain never has to go back to the list to be done with a session.
//
// It asks first. Every other key here reaches the agent, which makes this one
// reachable by a slip of the hand in the middle of typing to it, and what it
// takes is an agent mid-task in a pane holding real work. The dialog is the
// one the list's x raises, so the same answer means the same thing wherever
// it was asked; declining puts the keys back in the pane rather than dropping
// the operator on the list.
func (m *Model) archiveFocused(sess store.Session) (tea.Model, tea.Cmd) {
	target, ok := m.archiveConfirmFor(sess)
	if !ok {
		return m, nil
	}
	target.fromFocus = sess.ID
	m.confirm = target
	m.mode = modeConfirmDelete
	return m, nil
}
