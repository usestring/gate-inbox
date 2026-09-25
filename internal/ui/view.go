// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"fmt"
	"github.com/usestring/gate-inbox/internal/keymap"
	"regexp"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/tracing"
)

func (m *Model) View() tea.View {
	view := tea.NewView(m.frame())
	view.AltScreen = true
	// The app holds mouse reporting so a drag on the split divider and an
	// Alt-click forwarded into a pane both reach handleMouse.
	view.MouseMode = tea.MouseModeCellMotion
	// Ask the terminal to say when it gains and loses focus. It costs
	// nothing and, where the terminal answers, it releases the manager's
	// window pin the instant the operator looks away rather than at the next
	// poll. Inside tmux this is gated on the server's focus-events option,
	// which is off by default -- see visible.go for the read that covers
	// that case without writing to somebody else's tmux server.
	view.ReportFocus = true
	return view
}

// frameUnchanged marks a message whose handler returned without touching
// anything the frame reads. Bubble Tea renders after every message rather than
// on a clock, so a held key -- which arms one superseded preview settle per
// keystroke -- and the idle timers that only re-arm themselves each cost a
// full frame to paint what is already on screen.
func (m *Model) frameUnchanged() { m.frameReuse = true }

// frameReuseWindow bounds how stale a reused frame may be. Nothing a frame
// reads moves on its own except the age column, whose finest step is five
// seconds, so half a second cannot change a rendered age -- and any message
// that does change something paints in full.
const frameReuseWindow = 500 * time.Millisecond

// frame renders the screen and, when the operator is tracing, times it. The
// guard is here rather than inside paint because neither clock read is free
// at sixty frames a second, and because the reuse fast path exists to cost
// almost nothing.
func (m *Model) frame() string {
	if !tracing.Enabled() {
		out, _ := m.paint()
		return out
	}
	started := time.Now()
	out, reused := m.paint()
	m.traceFrame(started, reused)
	return out
}

// paint renders the screen and reports whether the reuse fast path answered
// it rather than a repaint. Split from View so the v2 view fields are set in
// one place and every mode still returns a plain string.
func (m *Model) paint() (string, bool) {
	reuse := m.frameReuse
	m.frameReuse = false
	if m.width == 0 {
		return "loading...", false
	}
	if reuse && m.lastFrame != "" && m.lastFrameW == m.width && m.lastFrameH == m.height &&
		time.Since(m.lastFrameAt) < frameReuseWindow {
		return m.lastFrame, true
	}
	m.painting, m.frameListed, m.frameAgents = true, nil, nil
	m.workMemo = map[string][]workRow{}
	defer func() {
		m.painting, m.frameListed, m.frameAgents = false, nil, nil
		m.workMemo = nil
	}()
	var frame string
	switch m.mode {
	case modeForm:
		frame = m.viewForm()
	case modeHelp:
		frame = m.viewHelp()
	case modeConfirmDelete:
		frame = m.viewConfirm()
	case modeLaunchHint:
		frame = m.viewLaunchHint()
	case modeSettings:
		frame = m.viewSettings()
	case modeFork:
		frame = m.viewFork()
	case modeMigrate:
		frame = m.viewMigrate()
	case modeAccount:
		frame = m.viewAccountSwitch()
	case modeMove:
		frame = m.viewMove()
	case modeGroupForm:
		frame = m.viewGroupForm()
	case modeNameSweep:
		frame = m.viewNameSweep()
	case modeRestorePrompt:
		frame = m.viewRestorePrompt()
	case modeWelcome:
		frame = m.viewWelcome()
	case modeTmuxHint:
		frame = m.viewTmuxHint()
	case modeAgentPick:
		frame = m.viewAgentPick()
	case modeExtensionView:
		frame = m.viewExtension()
	default:
		frame = m.viewListFrame()
	}
	out := clampFrame(frame, m.height)
	if current.Name == "oled" {
		// Explicit cell backgrounds keep OLED black when OSC 11 is ignored.
		out = strings.Join(paintRows(strings.Split(out, "\n"), m.width, m.height, current.Bg), "\n")
	}
	m.lastFrame, m.lastFrameAt = out, time.Now()
	m.lastFrameW, m.lastFrameH = m.width, m.height
	return out, false
}

// clampFrame pins a rendered frame to exactly height rows so the outer
// terminal cannot scroll the TUI away when a layout overshoots.
func clampFrame(frame string, height int) string {
	if height <= 0 {
		return frame
	}
	lines := strings.Split(frame, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		// Padding rows carry the backdrop too; a bare row would show the
		// terminal's own background through the bottom of a short frame.
		lines = append(lines, paint("", 1, backdropHex()))
	}
	return strings.Join(lines, "\n")
}

// splitWidths is the body's horizontal split: the sessions panel takes
// splitRatio of the terminal (default 30%), floored so both sides stay
// usable when the window is wide enough.
func (m *Model) splitWidths() (int, int) {
	if m.width <= 0 {
		return 0, 0
	}
	// One panel, not two slivers. A zero right width is the signal to draw
	// it -- which is also what the board layout asks for, on a terminal wide
	// enough for two.
	if m.width < minSplitWidth || m.layout == layoutBoard {
		return m.width, 0
	}
	ratio := m.split.ratio
	if ratio <= 0 || ratio >= 1 {
		ratio = defaultSplitRatio
	}
	leftWidth := int(float64(m.width)*ratio + 0.5)
	leftWidth = clampSplitLeft(leftWidth, m.width)
	return leftWidth, m.width - leftWidth
}

// previewPaneWidth is the sidebar's inner content width: the columns the
// pane preview can actually show. Sessions size to it so captured lines
// fit the panel instead of getting clipped on the right.
func (m *Model) previewPaneWidth() int {
	leftWidth, rightWidth := m.splitWidths()
	// A collapsed preview still has a pane behind it, and this width sizes the
	// real tmux pane rather than only the drawing of it. Falling through as
	// zero would start every agent in a one-column terminal.
	if rightWidth == 0 {
		return max(1, leftWidth-2)
	}
	// Reserve the border even in list mode so focusing does not reflow the agent.
	w := rightWidth - 3
	if w < 1 {
		return 1
	}
	return w
}

// previewPaneHeight is the rows of session pane content the Preview
// section can show with nothing transient over it, which is what tmux is
// pinned to: the painted view crops a taller pane, where resizing it for
// a passing overlay would cost an agent a full transcript redraw.
func (m *Model) previewPaneHeight() int {
	if m.height < 1 {
		return 1
	}
	avail := m.listBodyHeight()
	if avail < 1 {
		return 1
	}
	// Mirrors contentLines: the pane fills the body, with the frame's own
	// top rule -- not a row of the body -- seaming it off the header.
	rest := avail
	if rest < 3 {
		// Preview section is hidden; keep a tiny pane for create/attach paths.
		return 3
	}
	return rest
}

// statusLine is the transient message: prompts, search, and self-dismissing
// errors. It floats in a card over the frame rather than taking a row, so the
// body keeps its height whether or not a notice is up.
func (m *Model) statusLine() string {
	switch {
	// Errors outrank the focus notices: a scrolled or focused pane must
	// not hide a failure report.
	case m.mode == modeFocus && m.errBar.text != "":
		return m.statusMessage("✕", "●")
	case m.scrolledBack():
		// Nothing typed reaches a pane nobody is focused on, so an unfocused
		// preview is named its own way back: the wheel, or leaving the row.
		catchUp := keymap.Display("alt+down") + ", wheel down or type to catch up"
		if m.mode != modeFocus {
			catchUp = "wheel down or move the cursor to catch up"
		}
		return keyStyle.Render("scrolled ") +
			subtleStyle.Render(fmt.Sprintf("%d lines back · %s", m.focusScroll, catchUp))
	case m.mode == modeFocus && m.copied > 0:
		return keyStyle.Render("copied ") +
			subtleStyle.Render(fmt.Sprintf("%d chars to clipboard", m.copied))
	case m.split.resizeMode:
		hint := "←→ resize · drag divider · enter set · esc cancel"
		if m.split.dragging {
			hint = "release to set · esc cancels"
		}
		return keyStyle.Render("resize ") + subtleStyle.Render(hint)
	case m.errBar.text != "":
		return m.statusMessage("✕", "●")
	default:
		return ""
	}
}

// statusMessage styles whatever sits on the status bar: an action that went
// through reads as an outcome, everything else as a failure, in the glyphs
// the calling surface marks the two with.
func (m *Model) statusMessage(fail, done string) string {
	if m.errBar.worked() {
		return doneStyle.Render(done + " " + m.errBar.text)
	}
	return errStyle.Render(fail + " " + m.errBar.text)
}

// inGroupSubtree reports whether a session's group sits at or below the
// given group in the tree.
func inGroupSubtree(sessGroup, group string) bool {
	return sessGroup == group || strings.HasPrefix(sessGroup, group+"/")
}

func (m *Model) groupSessionCount(path string) int {
	count := 0
	for _, sess := range m.listedAgents() {
		if inGroupSubtree(sess.Group, path) {
			count++
		}
	}
	return count
}

// rowColumns lays a list row out as a name column and a right-aligned meta
// column, so status, tool and age line up down the list instead of ragging
// off the end of each name. Rows too narrow to split keep meta inline and
// let the caller's truncation decide what survives.
func rowColumns(lead, meta string, width int) string {
	if meta == "" {
		return lead
	}
	const gap = 2
	leadWidth := textfmt.Width(lead)
	metaWidth := textfmt.Width(meta)
	if width < 1 || leadWidth+gap+metaWidth > width {
		return lead + spaces(gap) + meta
	}
	return lead + spaces(width-leadWidth-metaWidth) + meta
}

func (m *Model) renamingRow(entry treeRow) bool {
	return m.mode == modeRename && entry.isSession() && entry.sess.ID == m.rename.sessID
}

// renameRowInput renders the inline name editor in place of the row's
// label, keeping the row's glyph so the edit reads in context.
func (m *Model) renameRowInput(entry treeRow, width int) string {
	lead := m.sessionGlyph(entry.sess)
	if fieldWidth := width - 4; fieldWidth >= 5 {
		m.rename.input.SetWidth(fieldWidth)
	}
	return lead + " " + m.rename.input.View()
}

// divider renders a labeled section rule that fills the given width: an
// accent tick, the label, then a hairline out to the edge.
func divider(label string, width int) string {
	head := sectionStyle.Render("▍"+label) + " "
	dashes := width - textfmt.Width(label) - 2
	if dashes < 0 {
		dashes = 0
	}
	return head + lipgloss.NewStyle().Foreground(colorBorder).Render(strings.Repeat("─", dashes))
}

const quickBarMaxRows = 5

// quickBarRows is the rows the typed text needs at the current width,
// capped so the bar never swallows the sidebar. Single-line values (the
// normal case) count exact soft-wrap rows; pasted multi-line values are
// estimated, with the textarea scrolling to keep the cursor visible.
func (m *Model) quickBarRows(textWidth int) int {
	return textareaRows(m.quick.input, textWidth, quickBarMaxRows)
}

func textareaRows(input textarea.Model, textWidth, maxRows int) int {
	rows := 0
	if input.LineCount() == 1 {
		rows = input.LineInfo().Height
	} else {
		if textWidth < 1 {
			textWidth = 1
		}
		// A line filling its last row exactly wraps onto one more empty row.
		for _, line := range strings.Split(input.Value(), "\n") {
			rows += 1 + max(lipgloss.Width(line), 1)/textWidth
		}
	}
	if rows > maxRows {
		rows = maxRows
	}
	if rows < 1 {
		rows = 1
	}
	return rows
}

func (m *Model) selectedGroup() (string, bool) {
	if entry, ok := m.selectedRow(); ok && entry.isGroup {
		return entry.group, true
	}
	return "", false
}

func parentGroup(group string) string {
	if idx := strings.LastIndex(group, "/"); idx >= 0 {
		return group[:idx]
	}
	return ""
}

// groupStatusBreakdown renders "2 working · 1 waiting" for the subtree,
// each count tinted in its status color, skipping zero statuses.
func (m *Model) groupStatusBreakdown(group string) string {
	counts := m.groupStatusCounts(group)
	var parts []string
	for _, st := range []string{status.Working, status.Waiting, status.Finished, status.Errored, status.Idle, status.Dead} {
		if counts[st] > 0 {
			// The count carries the state's color, the word stays quiet: a
			// rollup line should read as one texture, not as six labels
			// competing with the session names above it.
			parts = append(parts, statusTint(st, fmt.Sprintf("%d", counts[st]))+subtleText(" "+st))
		}
	}
	return strings.Join(parts, subtleText(" · "))
}

func (m *Model) groupStatusCounts(group string) map[string]int {
	counts := map[string]int{}
	for _, sess := range m.listedAgents() {
		if inGroupSubtree(sess.Group, group) {
			counts[sess.Status]++
		}
	}
	return counts
}

// groupStatusGlyphs is the subtree's rollup written in dots: each state
// present as its own glyph and count, tinted its own color. A one-line
// group row has no width to spell the states out, and the glyphs are the
// same ones the sessions under it wear.
func (m *Model) groupStatusGlyphs(group string) string {
	counts := m.groupStatusCounts(group)
	var parts []string
	for _, st := range []string{status.Working, status.Waiting, status.Finished, status.Errored, status.Idle, status.Dead} {
		if counts[st] == 0 {
			continue
		}
		parts = append(parts, statusTint(st, fmt.Sprintf("%s %d", statusGlyph(st), counts[st])))
	}
	return strings.Join(parts, subtleText("  "))
}

// previewDangerSeqs strips capture sequences that would scroll or clear the
// outer manager terminal when embedded in View output: erase (K/J), scroll
// (S/T), insert/delete lines (L/M), set scroll region (r), and the 7-bit
// index / reverse-index / next-line controls (D/M/E).
var previewDangerSeqs = regexp.MustCompile(
	`\x1b\[[0-9;]*[KJLMSTr]|\x1b[DEM]`,
)

func previewLine(line string, width int) string {
	line = previewDangerSeqs.ReplaceAllString(line, "")
	line = strings.Map(func(r rune) rune {
		if r < 0x20 && r != 0x1b && r != '\t' {
			return -1
		}
		return r
	}, line)
	w := textfmt.Width(line)
	if w > width {
		line = textfmt.TruncateWidth(line, width, "")
		w = textfmt.Width(line)
	}
	// Reset before padding so an open background from the agent does not
	// paint the rest of the column.
	if strings.ContainsRune(line, 0x1b) {
		line += "\x1b[0m"
	}
	if w < width {
		line += spaces(width - w)
	}
	return line
}

// expandPaneTabs writes a captured row's tabs out as the spaces the pane
// already painted. tmux serializes a tab as the single byte even though it
// spans the cells up to the next eight-column stop, so a row that keeps one
// measures narrower than it paints and overflows its column. The stops
// count from the row's start, which is pane column zero; escape sequences
// hold no column. A tab that would reach past the row's last cell stops on
// it, which is where tmux leaves the cursor.
func expandPaneTabs(line string, width int) string {
	if !strings.ContainsRune(line, '\t') {
		return line
	}
	const tabStop = 8
	var out strings.Builder
	column := 0
	for i, segment := range strings.Split(line, "\t") {
		if i > 0 {
			pad := max(min(tabStop-column%tabStop, width-1-column), 0)
			out.WriteString(spaces(pad))
			column += pad
		}
		out.WriteString(segment)
		column += textfmt.Width(segment)
	}
	return out.String()
}

// paneExact returns up to n lines of pane text as the pane painted them,
// preserving blank rows so a full-screen agent TUI looks the same in the
// preview. When the capture is taller than the panel (stale size), the
// bottom n lines are kept — the visible end of the pane. Rows arrive here
// before anything measures them, so this is where a tab becomes the cells
// it covers and the frame, the caret and the selection all count one set
// of columns.
func paneExact(pane string, n, width int) []string {
	if n <= 0 || pane == "" {
		return nil
	}
	// capture-pane often ends with a trailing newline; drop only that.
	pane = strings.TrimSuffix(pane, "\n")
	lines := strings.Split(pane, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	for i, line := range lines {
		lines[i] = expandPaneTabs(line, width)
	}
	return lines
}

func padToHeight(s string, height int) string {
	missing := height - lipgloss.Height(s)
	if missing > 0 {
		s += strings.Repeat("\n", missing)
	}
	return s
}

// viewFooter is the app's legend: a tier of keys for whatever the cursor is
// on, then a quieter tier for the keys that always apply. A transient mode
// (quick prompt, rename, resize) owns the legend alone while it is up.
func (m *Model) viewFooter() string {
	// A half-typed group number is the one piece of state with no home on a
	// row: the rail tints what it could still match, and the footer says what
	// has been typed so far.
	if m.jump.buffer != "" {
		return m.transientFooter(legendSection{title: "Group " + m.jump.buffer, pairs: [][2]string{
			{"0-9", "walk in"}, {m.tightCap(keymap.ContextList, keymap.Dismiss), "level separator"},
			{"any other key", "done"},
		}})
	}
	if m.quick.active && (m.mode != modeFocus || m.showsConversation()) {
		pairs := [][2]string{
			{"↵", "send"}, {"↑↓", "switch target"}, {"tab", "tool: " + m.quickTool()},
			{"esc", "close"},
		}
		if m.mode == modeFocus {
			pairs = [][2]string{{"↵", "send"}, {"alt+enter", "newline"}, {"esc", "close"},
				{m.fullCap(keymap.ContextFocus, keymap.ToggleConversation), m.conversationToggleLabel()},
				{m.fullCap(keymap.ContextFocus, keymap.ToggleGateInput), "terminal"}, {"pgup/pgdn", "scroll"}}
		} else if m.showsConversation() {
			pairs = append(pairs, [2]string{m.fullCap(keymap.ContextList, keymap.ToggleConversation), m.conversationToggleLabel()})
		}
		if m.quick.message() == "" && m.canRescindLatestSubmission() {
			pairs = append(pairs, [2]string{m.fullCap(keymap.ContextList, keymap.Rescind), "rescind latest"})
		}
		return m.transientFooter(legendSection{title: "Prompt", pairs: pairs})
	}
	if m.split.resizeMode {
		return m.transientFooter(legendSection{title: "Resize", pairs: [][2]string{
			{"←→", "nudge"}, {"drag", "divider"},
			{m.tightCap(keymap.ContextList, keymap.Resize) + " / release", "commit"},
			{"esc", "cancel"},
		}})
	}
	if m.mode == modeRename {
		pairs := [][2]string{{"↵", "save"}, {"esc", "cancel"}}
		if tool := m.renameTool(); tool != "" {
			pairs = [][2]string{{"tab", "tool: " + tool}, {"↵", "save"}, {"esc", "cancel"}}
		}
		return m.transientFooter(legendSection{title: "Rename", pairs: pairs})
	}
	// Focused, the keyboard belongs to the agent: the tier says so in its
	// title, carries the few keys the manager keeps, and drops the app-wide
	// tier, which would name keys the agent receives.
	if m.mode == modeFocus && m.gate.on {
		// The gate's tier names the whole session, not only the three keys
		// the mode is built on: v1's gate view carried the same controls, and
		// an operator draining a queue should not have to leave it to end a
		// session, spawn, copy an id or step back. It leads with the drain's
		// own gestures; the row is what alt+, and the chrome setting hide.
		//
		// Answers stay ahead of secondary controls in the two-row budget;
		// editor and copy remain discoverable in H.
		pairs := [][2]string{
			{"1-9 / ↵", "answer"},
			{m.gateCap(keymap.Dismiss), "skip"},
			{m.gateCap(keymap.PreviewTop), "top"},
			{m.gateCap(keymap.PreviewBottom), "bottom"},
			{m.gateCap(keymap.LeaveHard), "exit"},
		}
		if m.gate.menu {
			pairs[0] = [2]string{"space", "reply"}
			pairs = append([][2]string{
				{m.fullCap(keymap.ContextFocus, keymap.ToggleConversation), m.conversationToggleLabel()},
				{m.fullCap(keymap.ContextFocus, keymap.ToggleGateInput), "terminal"},
			}, pairs...)
		}
		pairs = append(pairs, m.snippetLegend().pairs...)
		pairs = append(pairs, [][2]string{
			{m.gateCap(keymap.Archive), "end"},
			{m.gateCap(keymap.NewSession), "new"},
			{m.gateCap(keymap.CopySessionID), "copy ID"},
			{m.gateCap(keymap.LastPane), "back"},
			{m.gateCap(keymap.Editor), "editor"},
		}...)
		return m.transientFooter(legendSection{title: "Gate", pairs: pairs})
	}
	if m.mode == modeFocus {
		// In triage the two exits stop being synonyms -- ctrl+q hands over the
		// next session instead of returning -- so naming them together would
		// leave a user who wants out pressing the key that keeps them in.
		// § joins them outside triage, but the footer row is full at the
		// widths that matter -- naming a third key here wraps it -- and § is
		// the one exit a user does not have to be told about. Help names it.
		exits := [][2]string{{m.capJoinFull(keymap.ContextFocus, " / ", keymap.Leave, keymap.LeaveHard), "back to manager"}}
		if m.advancesOnLeave() {
			// Focused, the rail's TRIAGE badge is off screen, so this line
			// is the only place left that can say which group ctrl+q will
			// hand over next -- and landing in a session from somebody
			// else's group is exactly what the scope exists to prevent.
			//
			// It is also the only place the walk is named at all for
			// somebody driving it from the setting rather than from a
			// queue they armed: an advance nothing announces reads as the
			// manager losing the operator's place. See leaveadvance.go.
			out := "back to manager"
			if m.triage {
				out = "stop triage"
			}
			exits = [][2]string{
				{m.capJoinFull(keymap.ContextFocus, " / ", keymap.Leave, keymap.HandOver), m.leaveAdvanceHint()},
				{m.fullCap(keymap.ContextFocus, keymap.LeaveHard), out},
			}
		}
		pairs := [][2]string{{"typing", "goes to the agent"}}
		pairs = append(pairs, exits...)
		if m.canRescindLatestSubmission() {
			pairs = append(pairs, [2]string{m.fullCap(keymap.ContextFocus, keymap.Rescind), "rescind latest"})
		}
		// A single-row footer holds one or two pairs, and the one it must hold
		// is the way out: on a phone the key that gets back to the manager
		// is the only one the operator cannot guess.
		if m.legendRows() == 1 {
			pairs = append(append([][2]string{}, exits...), [2]string{"typing", "goes to the agent"})
		}
		pairs = append(pairs,
			[2]string{m.fullCap(keymap.ContextFocus, keymap.Editor), "editor"},
			// Named on the footer rather than left to the key map: it is
			// destructive, and a key nobody knows about is a key nobody
			// uses on purpose and somebody eventually hits by accident.
			[2]string{m.fullCap(keymap.ContextFocus, keymap.Archive), "end"},
			// The footer holds one row: the word and line gestures are in
			// the key map, where there is room to name all three.
			[2]string{"drag / click", "copy"},
		)
		if m.pane.mouse {
			pairs = append(pairs, [2]string{"click / " + keymap.Display("alt+drag"), "agent UI"})
		}
		return m.transientFooter(legendSection{title: "Focused", pairs: pairs})
	}
	return m.listFooter()
}

func (m *Model) listFooter() string {
	sections := []legendSection{m.defaultRowLegend(), m.defaultViewLegend()}
	footer := legendBar(sections, m.width, m.legendRows())
	peekCap := m.tightCap(keymap.ContextList, keymap.LegendPeek)
	if footer == "" || peekCap == "" || strings.Contains(footer, keyCapQuiet(peekCap, "more")) {
		return footer
	}
	lines := splitLines(footer)
	lines[len(lines)-1] = legendBar([]legendSection{{title: "View", quiet: true, pairs: [][2]string{{peekCap, "more"}}}}, m.width, 1)
	return strings.Join(lines, "\n")
}

func (m *Model) defaultRowLegend() legendSection {
	row, ok := m.cursorRow()
	if !ok {
		return legendSection{}
	}
	if row.isArtifact() {
		return m.rowLegend()
	}
	openKey := m.tightCap(keymap.ContextList, keymap.Open)
	if row.isGroup {
		action := "fold"
		if m.collapsed[row.group] {
			action = "unfold"
		}
		pairs := [][2]string{{openKey, action}, {m.tightCap(keymap.ContextList, keymap.Editor), "editor"}}
		if m.applies(keymap.ContextList, keymap.Archive, row) {
			pairs = append(pairs, [2]string{m.capJoin(keymap.ContextList, "/", keymap.Archive, keymap.ArchiveAll), "end / all"})
		}
		return legendSection{title: "Group", pairs: pairs}
	}

	attachKey := m.tightCap(keymap.ContextList, keymap.Attach)
	enterHint, attachHint := "focus / fold", "attach"
	if !m.enterFocuses() {
		enterHint, attachHint = "attach / fold", "focus"
		attachKey = openKey
	}
	if row.sess.Archived {
		return legendSection{title: "Session", pairs: [][2]string{
			{attachKey, "attach"},
			{m.tightCap(keymap.ContextList, keymap.Restore), "restore to focus"},
		}}
	}

	title := "Session"
	pairs := [][2]string{{openKey, enterHint}, {m.tightCap(keymap.ContextList, keymap.Attach), attachHint}}
	if m.applies(keymap.ContextList, keymap.QuickInput, row) {
		pairs = append(pairs, [2]string{m.tightCap(keymap.ContextList, keymap.QuickInput), "prompt"})
	} else if m.isShell(row.sess.Tool) {
		title = "Shell"
	}
	if m.applies(keymap.ContextList, keymap.Dismiss, row) {
		action := "mute"
		switch {
		case m.isMuted(row.sess):
			action = "un-mute"
		case row.sess.Status == status.Finished:
			action = "mark idle"
		}
		pairs = append(pairs, [2]string{m.tightCap(keymap.ContextList, keymap.Dismiss), action})
	}
	pairs = append(pairs,
		[2]string{m.tightCap(keymap.ContextList, keymap.Archive), "end"},
		[2]string{m.tightCap(keymap.ContextList, keymap.Editor), "editor"},
	)
	return legendSection{title: title, pairs: pairs}
}

func (m *Model) defaultViewLegend() legendSection {
	list := keymap.ContextList
	pairs := [][2]string{{m.navCap(list), "navigate"}, {m.tightCap(list, keymap.NewSession), "new"}}
	if m.showArchived {
		pairs = append(pairs,
			[2]string{m.tightCap(list, keymap.Search), "search"},
			[2]string{m.tightCap(list, keymap.ArchivedView), "back to active"},
		)
	} else {
		statusAction := "attention"
		if m.statusFilter.active() {
			statusAction = "show all"
		}
		triageAction := "triage"
		if m.triage {
			triageAction = "back to groups"
		}
		pairs = append(pairs,
			[2]string{m.tightCap(list, keymap.Search), "search"},
			[2]string{m.tightCap(list, keymap.StatusFilter), statusAction},
			[2]string{m.tightCap(list, keymap.Triage), triageAction},
		)
	}
	pairs = append(pairs, [2]string{m.tightCap(list, keymap.LegendPeek), "more"})
	return legendSection{title: "View", quiet: true, pairs: pairs}
}

func (m *Model) peekLegendSections() []legendSection {
	sections := []legendSection{m.rowLegend(), m.viewLegend()}
	if snips := m.snippetLegend(); len(snips.pairs) > 0 {
		sections = append(sections, snips)
	}
	return sections
}

func (m *Model) rowLegend() legendSection {
	row, ok := m.cursorRow()
	if !ok {
		return legendSection{}
	}
	if row.isArtifact() {
		title := "Pull request"
		if row.art.kind == "TICKET" {
			title = "Ticket"
		}
		return legendSection{title: title, pairs: [][2]string{
			{m.capJoin(keymap.ContextList, "/", keymap.Open, keymap.Editor), "open"},
			{m.tightCap(keymap.ContextList, keymap.StepOut), "fold"},
			{m.navCap(keymap.ContextList), "navigate"},
		}}
	}
	if row.isGroup {
		foldAction := "fold"
		if m.collapsed[row.group] {
			foldAction = "unfold"
		}
		pairs := [][2]string{{m.tightCap(keymap.ContextList, keymap.Open), foldAction}}
		for _, candidate := range []struct {
			action keymap.Action
			text   string
		}{
			{keymap.Editor, "editor"}, {keymap.RenameSelf, "rename"}, {keymap.Move, "move"},
			{keymap.Priority, priorityLegend(m.priorityGroups[row.group])},
			{keymap.Archive, "end"}, {keymap.Revive, "revive"}, {keymap.Restore, "restore"},
		} {
			if m.applies(keymap.ContextList, candidate.action, row) {
				pairs = append(pairs, [2]string{m.tightCap(keymap.ContextList, candidate.action), candidate.text})
			}
		}
		return legendSection{title: "Group", pairs: pairs}
	}

	title := "Session"
	if m.isShell(row.sess.Tool) {
		title = "Shell"
	}
	if row.sess.Archived {
		return legendSection{title: title, pairs: [][2]string{
			{m.tightCap(keymap.ContextList, keymap.Attach), "attach"},
			{m.tightCap(keymap.ContextList, keymap.Restore), "restore to focus"},
		}}
	}
	section := m.defaultRowLegend()
	section.title = title
	pairs := append([][2]string{}, section.pairs...)
	for _, candidate := range []struct {
		action keymap.Action
		text   string
	}{
		{keymap.Fork, "fork"}, {keymap.RenameSelf, "rename"}, {keymap.Move, "move"},
		{keymap.Revive, "revive"}, {keymap.ReviveAll, "revive all"},
		{keymap.SwitchAccount, "account"}, {keymap.Restart, "restart"},
		{keymap.ArchiveAll, "end all"},
		{keymap.Priority, priorityLegend(row.sess.Priority)},
	} {
		if m.applies(keymap.ContextList, candidate.action, row) {
			pairs = append(pairs, [2]string{m.tightCap(keymap.ContextList, candidate.action), candidate.text})
		}
	}
	if len(m.undo.sessions) > 0 {
		pairs = append(pairs, [2]string{"U", "undo archive"})
	}
	return legendSection{title: title, pairs: pairs}
}

func (m *Model) viewLegend() legendSection {
	row := m.currentLegendRow()
	list := keymap.ContextList
	pairs := [][2]string{{m.navCap(list), "navigate"}}
	for _, candidate := range []struct {
		action keymap.Action
		text   string
	}{
		{keymap.LastPane, "last pane"}, {keymap.Rescind, "rescind latest"},
		{keymap.NewSession, "new"}, {keymap.NewSessionForm, "new…"},
		{keymap.NewTerminal, "terminal"}, {keymap.NewGroup, "group"}, {keymap.Search, "search"},
		{keymap.ArchivedView, "archived"}, {keymap.StatusFilter, "attention"},
		{keymap.Triage, "triage"}, {keymap.EmptyGroups, "hide empty"},
		{keymap.Help, "full key map"}, {keymap.Quit, "quit"},
		{keymap.FoldAll, "fold all"}, {keymap.Resize, "resize"}, {keymap.Settings, "settings"},
	} {
		if !m.applies(list, candidate.action, row) {
			continue
		}
		text := candidate.text
		switch candidate.action {
		case keymap.ArchivedView:
			if m.showArchived {
				text = "back to active"
			}
		case keymap.StatusFilter:
			if m.statusFilter.active() {
				text = "show all"
			}
		case keymap.Triage:
			if m.triage {
				text = "back to groups"
				if m.triageScope != "" {
					text = "triage: " + baseName(m.triageScope)
				}
			}
		case keymap.EmptyGroups:
			if m.hideEmptyGroups {
				text = "show empty"
			}
		case keymap.FoldAll:
			if m.allFoldsCollapsed() {
				text = "unfold all"
			}
		}
		pairs = append(pairs, [2]string{m.tightCap(list, candidate.action), text})
	}
	if m.applies(list, keymap.ReorderUp, row) || m.applies(list, keymap.ReorderDown, row) {
		pairs = append(pairs, [2]string{m.capJoin(list, "/", keymap.ReorderUp, keymap.ReorderDown), "reorder"})
	}
	return legendSection{title: "Available", quiet: true, pairs: pairs}
}

// footerRows is the rows the footer takes: none when the legend is hidden,
// which lipgloss.Height would still count as one.
func (m *Model) footerRows() int {
	footer := m.viewFooter()
	if footer == "" {
		return 0
	}
	return lipgloss.Height(footer)
}

// transientFooter renders one tier at the list footer's height: the footer
// sets the preview box, and a box that moves resizes every session's pane,
// which costs an agent drawing on the normal screen a full transcript redraw.
func (m *Model) transientFooter(section legendSection) string {
	rows := m.legendRows()
	if rows == 0 {
		return ""
	}
	return padToHeight(legendBar([]legendSection{section}, m.width, rows), lipgloss.Height(m.listFooter()))
}

func displayGroup(path string) string {
	if path == "" {
		return "root"
	}
	return path
}

// relSince is t's age worded as a moment in the past, for columns that
// answer "when did this last happen" rather than "how long has this run".
func relSince(t time.Time) string {
	return textfmt.Age(time.Since(t)) + " ago"
}

func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// truncateTail keeps the end of the string (best for paths).
func truncateTail(s string, max int) string {
	runes := []rune(s)
	if max <= 0 {
		return ""
	}
	if len(runes) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	return "…" + string(runes[len(runes)-max+1:])
}

func truncatePath(path string, limit int) string {
	runes := []rune(path)
	if limit <= 0 {
		return ""
	}
	if len(runes) <= limit {
		return path
	}
	if limit == 1 {
		return "…"
	}
	tail := string(runes[len(runes)-limit+1:])
	if i := strings.IndexByte(tail, '/'); i >= 0 && i < len(tail)-1 {
		return "…" + tail[i:]
	}
	return "…" + tail
}

// scrollWindow keeps the cursor visible inside a height-limited window of
// single-line rows, reserving one line for each overflow indicator.
func scrollWindow(total, cursor, height int) (int, int) {
	if total <= height {
		return 0, total
	}
	visible := height - 2
	if visible < 1 {
		visible = 1
	}
	start := cursor - visible/2
	if start < 0 {
		start = 0
	}
	if start+visible > total {
		start = total - visible
	}
	return start, start + visible
}
