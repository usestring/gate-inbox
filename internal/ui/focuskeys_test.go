// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

func TestFocusKeyCommand(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.KeyPressMsg
		want string
		ok   bool
	}{
		{"runes", tea.KeyPressMsg{Code: 'h', Text: "hi"}, "send-keys -t gi_x -H 68 69", true},
		{"utf8", tea.KeyPressMsg{Code: 'ש', Text: "ש"}, "send-keys -t gi_x -H d7 a9", true},
		{"space", tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}, "send-keys -t gi_x -H 20", true},
		{"alt-rune", tea.KeyPressMsg{Code: 'b', Mod: tea.ModAlt}, "send-keys -t gi_x -H 1b 62", true},
		{"enter", tea.KeyPressMsg{Code: tea.KeyEnter}, "send-keys -t gi_x Enter", true},
		{"escape", tea.KeyPressMsg{Code: tea.KeyEsc}, "send-keys -t gi_x Escape", true},
		{"ctrl-c", tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, "send-keys -t gi_x C-c", true},
		// The editor sits on alt+o now, so ctrl+o reaches the agent: Claude
		// Code and Gemini CLI both bind it.
		{"ctrl-o", tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl}, "send-keys -t gi_x C-o", true},
		{"tab-not-ctrl-i", tea.KeyPressMsg{Code: tea.KeyTab}, "send-keys -t gi_x Tab", true},
		{"enter-not-ctrl-m", tea.KeyPressMsg{Code: tea.KeyEnter}, "send-keys -t gi_x Enter", true},
		{"shift-tab", tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}, "send-keys -t gi_x BTab", true},
		{"up", tea.KeyPressMsg{Code: tea.KeyUp}, "send-keys -t gi_x Up", true},
		{"left", tea.KeyPressMsg{Code: tea.KeyLeft}, "send-keys -t gi_x Left", true},
		{"alt-up", tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModAlt}, "send-keys -t gi_x M-Up", true},
		{"pgup", tea.KeyPressMsg{Code: tea.KeyPgUp}, "send-keys -t gi_x PPage", true},
		{"backspace", tea.KeyPressMsg{Code: tea.KeyBackspace}, "send-keys -t gi_x BSpace", true},
	}
	for _, c := range cases {
		got, ok := focusKeyCommand("gi_x", c.msg)
		if ok != c.ok || got != c.want {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", c.name, got, ok, c.want, c.ok)
		}
	}
}

// A captured row's background run must survive rendering unchanged: an
// app that paints a bar and resets only the foreground leaves those cells
// with a background in tmux's own grid, and the preview has to reproduce
// exactly those columns, no wider and no narrower.

// bgRun returns, for each column, whether a background color is active,
// by walking the row's SGR sequences the way a terminal would.
func bgRun(row string, width int) []bool {
	out := make([]bool, 0, width)
	bg := false
	i := 0
	for i < len(row) && len(out) < width {
		if row[i] == 0x1b {
			end := i
			for end < len(row) && !strings.ContainsRune("mK", rune(row[end])) {
				end++
			}
			if end < len(row) && row[end] == 'm' {
				params := row[i+2 : end]
				for _, p := range strings.Split(params, ";") {
					switch {
					case p == "0" || p == "" || p == "49":
						bg = false
					case strings.HasPrefix(p, "4") && len(p) == 2:
						bg = true
					case p == "48":
						bg = true
					}
				}
			}
			i = end + 1
			continue
		}
		out = append(out, bg)
		i++
	}
	return out
}

func TestPreviewLinePreservesBackgroundColumns(t *testing.T) {

	raw := "\x1b[38;5;239m\x1b[48;5;237m❯\x1b[39m \x1b[38;5;231mhello there\x1b[39m"
	width := 40
	got := previewLine(raw, width)

	rawCells := bgRun(raw, width)
	gotCells := bgRun(got, width)
	t.Logf("raw plain=%q width=%d", ansi.Strip(raw), ansi.StringWidth(ansi.Strip(raw)))
	t.Logf("raw bg cells=%v", rawCells)
	t.Logf("out bg cells=%v", gotCells)
	if len(gotCells) < len(rawCells) {
		t.Fatalf("rendered shorter than raw: raw %d, rendered %d", len(rawCells), len(gotCells))
	}
	for i := range rawCells {
		if rawCells[i] != gotCells[i] {
			t.Fatalf("column %d background differs: raw %v, rendered %v", i, rawCells[i], gotCells[i])
		}
	}
	for i := len(rawCells); i < len(gotCells); i++ {
		if gotCells[i] {
			t.Fatalf("padding column %d invented a background", i)
		}
	}
}

// The caret overpaints one cell and nothing else: a row the agent drew
// with its own background must keep exactly that background everywhere
// except the caret, or the row appears to flash a band on every blink.
func TestCaretKeepsRowColours(t *testing.T) {

	raw := "\x1b[48;5;237m\x1b[38;5;231mprompt text here\x1b[0m"
	width := 30
	m := paneAt(t, raw)
	m.pane.cursor = paneCursor{x: 3, y: 0, ok: true}
	m.cursorOn = true

	plainRow := previewLine(raw, width)
	withCaret := m.renderPaneRow(0, raw, width)

	if ansi.Strip(withCaret) != ansi.Strip(plainRow) {
		t.Fatalf("caret changed the row text: %q vs %q", ansi.Strip(withCaret), ansi.Strip(plainRow))
	}

	plainCells := bgRun(plainRow, width)
	caretCells := bgRun(withCaret, width)
	if len(plainCells) != len(caretCells) {
		t.Fatalf("cell counts differ: %d vs %d", len(plainCells), len(caretCells))
	}
	for i := range plainCells {
		if plainCells[i] != caretCells[i] {
			t.Fatalf("column %d background changed by the caret: %v vs %v (row %q)",
				i, plainCells[i], caretCells[i], withCaret)
		}
	}
}

// The caret blinks while focused and stops when focus ends.
func TestCursorBlinks(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "blinker", t.TempDir(), "")
	m.selectSessionRow(t, "blinker")
	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	*m = *updated.(*Model)
	if !m.cursorOn {
		t.Fatal("caret starts hidden")
	}

	updated, cmd := m.Update(cursorBlinkMsg{gen: m.blinkGen})
	*m = *updated.(*Model)
	if m.cursorOn {
		t.Fatal("caret did not blink off")
	}
	if cmd == nil {
		t.Fatal("blink timer was not re-armed while focused")
	}

	// Typing must show the caret again immediately.
	updated, _ = m.handleKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	*m = *updated.(*Model)
	if !m.cursorOn {
		t.Fatal("typing left the caret hidden")
	}

	updated, _ = m.handleKey(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	*m = *updated.(*Model)
	if _, cmd := m.Update(cursorBlinkMsg{gen: m.blinkGen}); cmd != nil {
		t.Fatal("blink timer kept running after focus ended")
	}
}

// Focusing while already focused must not leave the previous caret timer
// running: a triage advance re-focuses on every step, and each surviving
// chain toggled the caret again inside the same period.
func TestReFocusingLeavesOneCaretTimer(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "blinker", t.TempDir(), "")
	m.selectSessionRow(t, "blinker")

	updated, _ := m.focusSelected()
	*m = *updated.(*Model)
	stale := m.blinkGen

	// A second focus, exactly as a handover makes it, with the first timer
	// still out.
	updated, _ = m.focusSelected()
	*m = *updated.(*Model)
	if m.blinkGen == stale {
		t.Fatal("re-focusing did not supersede the timer already out")
	}

	m.cursorOn = true
	updated, cmd := m.Update(cursorBlinkMsg{gen: stale})
	*m = *updated.(*Model)
	if !m.cursorOn {
		t.Fatal("a superseded timer still toggled the caret")
	}
	if cmd != nil {
		t.Fatal("a superseded timer re-armed itself")
	}

	updated, cmd = m.Update(cursorBlinkMsg{gen: m.blinkGen})
	*m = *updated.(*Model)
	if m.cursorOn {
		t.Fatal("the live timer did not blink the caret off")
	}
	if cmd == nil {
		t.Fatal("the live timer was not re-armed")
	}
}

// The setting swaps which key focuses and which attaches, and persists.
func TestSettingsSwapsFocusKey(t *testing.T) {
	m := buildModel(t)
	m.openSettings()
	if !m.settings.enterFocuses {
		t.Fatal("settings should open with enter focusing")
	}
	card := ansi.Strip(m.viewSettings())
	if !strings.Contains(card, "session keys") {
		t.Fatalf("settings card has no session keys row:\n%s", card)
	}
	for i := 0; i < settingsFieldFocusKey; i++ {
		m.handleSettingsKey(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if m.settings.field != settingsFieldFocusKey {
		t.Fatalf("stepping down should reach the session keys field, got %d", m.settings.field)
	}
	m.handleSettingsKey(tea.KeyPressMsg{Code: tea.KeyRight})
	if !strings.Contains(ansi.Strip(m.viewSettings()), "attach") {
		t.Fatal("swapped card does not read attach on enter")
	}
	m.handleSettingsKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.enterFocuses() {
		t.Fatal("swapped choice did not persist")
	}
}

// With the keys swapped, Enter attaches and A focuses.
func TestSwappedKeysRouteActions(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "swapped", t.TempDir(), "")
	m.selectSessionRow(t, "swapped")

	// Default: enter focuses.
	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	*m = *updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("enter did not focus by default, mode = %v", m.mode)
	}
	updated, _ = m.handleKey(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	*m = *updated.(*Model)

	// Swap through the settings screen, the same path a user takes.
	m.openSettings()
	m.settings.field = settingsFieldFocusKey
	m.cycleSetting(1)
	m.handleSettingsKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if chosen, err := m.store.Setting(focusKeySetting); err != nil || chosen != "attach" {
		t.Fatalf("swap did not persist, chosen = %q, err = %v", chosen, err)
	}
	// Swapped: A focuses instead.
	updated, _ = m.handleKey(tea.KeyPressMsg{Code: 'A', Text: "A"})
	*m = *updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("A did not focus after the swap, mode = %v, err = %q", m.mode, m.errBar.text)
	}
}

// Enter on a live session row focuses it; typed keys land in its pane and
// ctrl+q returns to the list without touching the pane.
func TestFocusModeForwardsKeys(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "focusme", t.TempDir(), "")
	m.selectSessionRow(t, "focusme")

	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	*m = *updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("after enter, mode = %v, err = %q", m.mode, m.errBar.text)
	}

	sess := m.rows[m.cursor].sess
	for _, msg := range []tea.KeyPressMsg{
		{Code: 'p', Text: "ping-focus"},
		{Code: tea.KeyEnter},
	} {
		updated, _ := m.handleKey(msg)
		*m = *updated.(*Model)
	}
	if m.errBar.text != "" {
		t.Fatalf("forwarding set err: %q", m.errBar.text)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		pane, err := m.tmux.CapturePane(sess.ID)
		if err != nil {
			t.Fatalf("capture: %v", err)
		}
		if strings.Contains(pane, "ping-focus") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("typed text never reached pane: %q", pane)
		}
		time.Sleep(30 * time.Millisecond)
	}

	updated, _ = m.handleKey(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	*m = *updated.(*Model)
	if m.mode != modeList {
		t.Fatalf("ctrl+q left mode = %v", m.mode)
	}
}

// A focused session that disappears drops the UI back to the list.
func TestFocusModeExitsWhenSessionDies(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "doomed", t.TempDir(), "")
	m.selectSessionRow(t, "doomed")

	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	*m = *updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("after enter, mode = %v", m.mode)
	}

	sess := m.rows[m.cursor].sess
	if err := m.store.Delete(sess.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	m.tmux.Kill(sess.ID)
	m.applyCmd(t, m.refreshCmd())
	if m.mode != modeList {
		t.Fatalf("after session death, mode = %v", m.mode)
	}
}

// Ctrl+R has no review to open any more, so in focus it is an ordinary
// keystroke for the pane and the manager stays where it was.
func TestFocusCtrlRStaysInFocus(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "focusrev", t.TempDir(), "")
	m.selectSessionRow(t, "focusrev")

	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	*m = *updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("after enter, mode = %v, err = %q", m.mode, m.errBar.text)
	}

	updated, _ = m.handleKey(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	*m = *updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("ctrl+r in focus should stay in the pane, mode = %v, err = %q", m.mode, m.errBar.text)
	}
}

// alt+o opens the focused session's directory the way the list's o does,
// and a windowed editor leaves the focus where it was.
func TestFocusAltOOpensEditor(t *testing.T) {
	m := buildModel(t)
	launched := captureEditor(t, "code")
	dir := t.TempDir()
	createSession(t, m, "focusedit", dir, "")
	m.selectSessionRow(t, "focusedit")

	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	*m = *updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("after enter, mode = %v, err = %q", m.mode, m.errBar.text)
	}

	updated, cmd := m.handleKey(tea.KeyPressMsg{Code: 'o', Mod: tea.ModAlt})
	*m = *updated.(*Model)
	if cmd == nil {
		t.Fatalf("alt+o in focus returned no launch, err = %q", m.errBar.text)
	}
	m.applyCmd(t, cmd)

	if want := []string{"code", resolved(t, dir)}; !slices.Equal(*launched, want) {
		t.Fatalf("launched %v, want %v", *launched, want)
	}
	if m.mode != modeFocus {
		t.Fatalf("a windowed editor should leave the focus alone, mode = %v", m.mode)
	}
	if !strings.Contains(m.errBar.text, "code") {
		t.Fatalf("status line should name the editor, got %q", m.errBar.text)
	}
}

// An editor that took the terminal hands it back with mouse reporting off.
// v2 reapplies it from the view on the next frame rather than from a command,
// so this asserts the frame, not the batch.
func TestFocusEditorThatTookTheScreenRearmsMouse(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "screenedit", t.TempDir(), "")
	m.selectSessionRow(t, "screenedit")
	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	*m = *updated.(*Model)

	updated, _ = m.Update(editorDoneMsg{tookScreen: true})
	*m = *updated.(*Model)
	if got := m.View().MouseMode; got != tea.MouseModeCellMotion {
		t.Fatalf("mouse reporting was not re-armed: MouseMode = %v", got)
	}

	// A windowed editor never took the terminal, so it has nothing to undo.
	updated, cmd := m.Update(editorDoneMsg{name: "code", path: "/tmp"})
	*m = *updated.(*Model)
	if cmd != nil {
		t.Fatalf("a windowed editor should leave the terminal alone, got %T", cmd())
	}
}

// Leaving focus must keep mouse reporting: handing it back would let a
// wheel notch scroll the manager out of view from the list.
func TestFocusExitKeepsMouse(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "mouseback", t.TempDir(), "")
	m.selectSessionRow(t, "mouseback")

	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	*m = *updated.(*Model)
	if got := m.View().MouseMode; got != tea.MouseModeCellMotion {
		t.Fatalf("entering focus never enabled mouse reporting: MouseMode = %v", got)
	}

	// Leaving keeps mouse reporting on: handing the wheel back to the
	// terminal here would let a notch scroll the manager out of view.
	updated, _ = m.handleKey(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	*m = *updated.(*Model)
	if got := m.View().MouseMode; got != tea.MouseModeCellMotion {
		t.Fatalf("leaving focus released mouse reporting: MouseMode = %v", got)
	}
	if m.sel.active {
		t.Fatal("selection survived leaving focus")
	}
}

// The rail marks which session is focused, so the mode is readable from
// the list as well as from the pane, and marks it without the row's text
// moving: focus recolours the cursor's box, muted to accent, in place.
func TestRailMarksFocusInPlace(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "badged", t.TempDir(), "")
	m.selectSessionRow(t, "badged")

	rule := func(hex string) string {
		r, g, b := hexRGB(hex)
		return fmt.Sprintf("38;2;%d;%d;%d", r, g, b)
	}
	listed := m.entryLines(m.rows, 0, 60, 20)
	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	*m = *updated.(*Model)
	focused := m.entryLines(m.rows, 0, 60, 20)
	before, after := railLinesRaw(listed), railLinesRaw(focused)
	if strings.Count(after, rule(current.Dim)) >= strings.Count(before, rule(current.Dim)) ||
		strings.Count(after, rule(current.Accent)) <= strings.Count(before, rule(current.Accent)) {
		t.Fatal("focusing did not move the box from the muted tone to the accent")
	}
	if railLinesText(focused) != railLinesText(listed) {
		t.Fatalf("focusing moved the rail's text:\n%s\nvs\n%s", railLinesText(listed), railLinesText(focused))
	}
}

func railLinesRaw(lines []contentLine) string {
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(line.text + "\n")
	}
	return b.String()
}

// A paste while focused goes through the tmux paste path as one block, so
// the agent's composer receives the newlines instead of Enter presses that
// submit the prompt mid-paste.
func TestFocusPasteKeepsPromptInComposer(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "paster", t.TempDir(), "")
	m.selectSessionRow(t, "paster")
	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	*m = *updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("enter did not focus, mode = %v", m.mode)
	}

	var pastedID, pastedText string
	calls := 0
	restore := pasteFocused
	pasteFocused = func(d *tmux.Driver, id, text string) error {
		calls++
		pastedID, pastedText = id, text
		return nil
	}
	t.Cleanup(func() { pasteFocused = restore })

	text := "line one\nline two\n"
	updated, _ = m.Update(tea.PasteMsg{Content: text})
	*m = *updated.(*Model)

	if calls != 1 {
		t.Fatalf("paste path called %d times, want 1 (err=%q)", calls, m.errBar.text)
	}
	if pastedText != text {
		t.Fatalf("pasted text = %q, want %q", pastedText, text)
	}
	if wantID := m.sessionRows()[0].ID; pastedID != wantID {
		t.Fatalf("pasted into %q, want %q", pastedID, wantID)
	}
}

// Mouse reporting is off by default and only enabled during resize mode.
// After a tmux attach/detach, no mouse re-arming is needed because mouse
// is off: the terminal handles native text selection directly.
// This test verifies the handler returns no mouse-enable command.
func TestDetachNoMouseReArm(t *testing.T) {
	m := buildModel(t)
	clearRequestOnCleanup(t, m)

	_, cmd := m.Update(attachDoneMsg{})
	if cmd != nil {
		t.Fatalf("detach should not re-arm mouse, got %T", cmd)
	}
}

// Ctrl+\ mirrors ctrl+q: it leaves focus without touching the pane.
func TestFocusModeCtrlBackslashUnfocuses(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "focusme", t.TempDir(), "")
	m.selectSessionRow(t, "focusme")

	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	*m = *updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("after enter, mode = %v, err = %q", m.mode, m.errBar.text)
	}
	updated, _ = m.handleKey(tea.KeyPressMsg{Code: '\\', Mod: tea.ModCtrl})
	*m = *updated.(*Model)
	if m.mode != modeList {
		t.Fatalf("ctrl+\\ left mode = %v", m.mode)
	}
}

// caretModel is a focused model whose pane mirror is posed by hand: the
// captured rows, and the caret cell tmux reported over them.
func caretModel(t *testing.T, cursor paneCursor, rows ...string) *Model {
	t.Helper()
	engine, err := status.NewEngine(config.Config{Tools: map[string]config.Tool{
		"claude":    {ActivityCutoff: `(?m)^❯`},
		"gemini":    {ActivityCutoff: `(?m)^\s*[>!*] `},
		"unmarked":  {},
		"wide-mark": {ActivityCutoff: `(?m)^→`},
		// A boxed composer: the cutoff closes the box a row BELOW the one
		// the caret types on, so the composer names its own rows.
		"boxed": {ActivityCutoff: `(?m)^\s*╹`, InputLine: `^[ \x{A0}]*┃`},
	}})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	m := &Model{engine: engine, mode: modeFocus}
	m.preview = strings.Join(rows, "\n") + "\n"
	m.pane.forID = "s1"
	m.pane.cursor = cursor
	return m
}

// Left is only free to mean "back to the list" where the agent would do
// nothing with it: at the head of its prompt, with the marker alone to
// the caret's left.
func TestCaretAtInputStart(t *testing.T) {
	cases := []struct {
		name   string
		tool   string
		cursor paneCursor
		rows   []string
		want   bool
	}{
		// tmux trims a row's trailing blanks, so an empty prompt is the
		// marker alone with the caret out on padding the row lacks.
		{"empty prompt", "claude", paneCursor{x: 2, y: 1, ok: true}, []string{"output", "❯"}, true},
		// Claude pads its marker with a non-breaking space, so the cell
		// between marker and caret is blank without being an ASCII space.
		{"nbsp padded prompt", "claude", paneCursor{x: 2, y: 0, ok: true}, []string{"❯\u00a0"}, true},
		{"nbsp padded with input", "claude", paneCursor{x: 2, y: 0, ok: true}, []string{"❯\u00a0write a test"}, true},
		{"nbsp padded mid-input", "claude", paneCursor{x: 5, y: 0, ok: true}, []string{"❯\u00a0write a test"}, false},
		{"caret before typed text", "claude", paneCursor{x: 2, y: 0, ok: true}, []string{"❯ hi"}, true},
		{"caret after typed text", "claude", paneCursor{x: 4, y: 0, ok: true}, []string{"❯ hi"}, false},
		{"caret one in", "claude", paneCursor{x: 3, y: 0, ok: true}, []string{"❯ hi"}, false},
		{"caret on the marker", "claude", paneCursor{x: 0, y: 0, ok: true}, []string{"❯ hi"}, false},
		// A wrapped prompt's continuation rows carry no marker: Left there
		// reaches the end of the row above and belongs to the agent.
		{"wrapped continuation", "claude", paneCursor{x: 2, y: 1, ok: true}, []string{"❯ a long", "  wrapped"}, false},
		{"plain output row", "claude", paneCursor{x: 2, y: 0, ok: true}, []string{"some output"}, false},
		// The marker has to open the row: one quoted mid-line is not a prompt.
		{"quoted marker", "claude", paneCursor{x: 8, y: 0, ok: true}, []string{"we use ❯ here"}, false},
		{"indented marker", "gemini", paneCursor{x: 4, y: 0, ok: true}, []string{"  > "}, true},
		{"indented marker mid-input", "gemini", paneCursor{x: 6, y: 0, ok: true}, []string{"  > hi"}, false},
		{"tool without a marker", "unmarked", paneCursor{x: 2, y: 0, ok: true}, []string{"❯"}, false},
		{"unknown tool", "nosuch", paneCursor{x: 2, y: 0, ok: true}, []string{"❯"}, false},
		{"no cursor report", "claude", paneCursor{x: 2, y: 1}, []string{"output", "❯"}, false},
		{"cursor row past the capture", "claude", paneCursor{x: 2, y: 9, ok: true}, []string{"❯"}, false},
		// A boxed composer's cutoff is the row under the box, so before
		// input_line existed no row of it was an input line at all and Left
		// was forwarded to the agent forever. This is that bug's assertion.
		{"boxed empty composer", "boxed", paneCursor{x: 5, y: 1, ok: true}, []string{"  ┃", "  ┃", "  ╹▀▀▀"}, true},
		{"boxed composer with text", "boxed", paneCursor{x: 20, y: 1, ok: true}, []string{"  ┃", "  ┃  do a full check", "  ╹▀▀▀"}, false},
		{"boxed caret before its text", "boxed", paneCursor{x: 5, y: 1, ok: true}, []string{"  ┃", "  ┃  do a full check", "  ╹▀▀▀"}, true},
		// Every row of a box carries the bar, so a blank second line of a
		// wrapped message looks like an empty prompt on its own row. The
		// rows above are what tell them apart.
		{"boxed blank line under text", "boxed", paneCursor{x: 5, y: 2, ok: true}, []string{"  ┃", "  ┃  first line", "  ┃", "  ╹▀▀▀"}, false},
		{"boxed blank line under blanks", "boxed", paneCursor{x: 5, y: 2, ok: true}, []string{"  ┃", "  ┃", "  ┃", "  ╹▀▀▀"}, true},
		// The caret standing on the bar itself has the marker to its right,
		// not its left, so it is not at the head of anything.
		{"boxed caret on the bar", "boxed", paneCursor{x: 2, y: 0, ok: true}, []string{"  ┃", "  ╹▀▀▀"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := caretModel(t, c.cursor, c.rows...)
			if got := m.caretAtInputStart("s1", c.tool); got != c.want {
				t.Fatalf("caretAtInputStart = %v, want %v", got, c.want)
			}
		})
	}
}

// A double-width marker is measured in cells, not runes, so the caret's
// column lines up with the one tmux reported.
func TestCaretAtInputStartMeasuresMarkerInCells(t *testing.T) {
	m := caretModel(t, paneCursor{x: 2, y: 0, ok: true}, "→ hi")
	if !m.caretAtInputStart("s1", "wide-mark") {
		t.Fatal("caret at the head of a wide-marker prompt was not recognised")
	}
}

// The mirror belongs to whichever session pushed it, and a scrolled-back
// pane's rows no longer line up with the live caret: neither can decide.
func TestCaretAtInputStartNeedsCurrentPane(t *testing.T) {
	m := caretModel(t, paneCursor{x: 2, y: 0, ok: true}, "❯")
	if m.caretAtInputStart("other", "claude") {
		t.Fatal("another session's pane mirror decided the caret")
	}
	m.focusScroll = 3
	if m.caretAtInputStart("s1", "claude") {
		t.Fatal("a scrolled-back pane decided the caret")
	}
}

// Left leaves focus at the head of the prompt and reaches the agent
// anywhere else, so a typed prompt keeps its caret movement.
func TestFocusLeftUnfocusesAtPromptHead(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "leftie", t.TempDir(), "")
	m.selectSessionRow(t, "leftie")

	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	*m = *updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("after enter, mode = %v, err = %q", m.mode, m.errBar.text)
	}
	sess := m.rows[m.cursor].sess
	m.rows[m.cursor].sess.Tool = "claude-hooked"
	m.pane.forID = sess.ID
	m.pane.cursor = paneCursor{x: 4, y: 0, ok: true}
	m.preview = "❯ hi\n"

	updated, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	*m = *updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("left inside a typed prompt left focus, mode = %v", m.mode)
	}
	if m.errBar.text != "" {
		t.Fatalf("forwarding left set err: %q", m.errBar.text)
	}

	m.pane.cursor = paneCursor{x: 2, y: 0, ok: true}
	updated, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	*m = *updated.(*Model)
	if m.mode != modeList {
		t.Fatalf("left at the prompt head did not unfocus, mode = %v", m.mode)
	}
}

// Alt+Left is a word jump inside the prompt, so it stays the agent's.
func TestFocusAltLeftStaysWithTheAgent(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "altleft", t.TempDir(), "")
	m.selectSessionRow(t, "altleft")

	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	*m = *updated.(*Model)
	sess := m.rows[m.cursor].sess
	m.rows[m.cursor].sess.Tool = "claude-hooked"
	m.pane.forID = sess.ID
	m.pane.cursor = paneCursor{x: 2, y: 0, ok: true}
	m.preview = "❯ hi\n"

	updated, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModAlt})
	*m = *updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("alt+left left focus, mode = %v", m.mode)
	}
}

// uiForeignServer stands in for a tmux server the manager does not own: a
// session someone else created, on its own socket, running the given command.
// Returns the socket and the pane id.
func uiForeignServer(t testing.TB, command string) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	socket := tmuxtest.NewSocket("uiforeign")
	// kill-server fails whenever no server is up, which is the normal case.
	tmuxOnSocket(socket, "kill-server").Run()
	if out, err := tmuxOnSocket(socket, "new-session", "-d", "-s", "user", "-c", "/tmp", "-x", "80", "-y", "24", command).CombinedOutput(); err != nil {
		t.Fatalf("foreign new-session: %v: %s", err, out)
	}
	t.Cleanup(func() { tmuxOnSocket(socket, "kill-server").Run() })
	out, err := tmuxOnSocket(socket, "list-panes", "-t", "user", "-F", "#{pane_id}").CombinedOutput()
	if err != nil {
		t.Fatalf("foreign list-panes: %v: %s", err, out)
	}
	panes := strings.Fields(string(out))
	if len(panes) != 1 {
		t.Fatalf("foreign server should have 1 pane, got %q", panes)
	}
	return socket, panes[0]
}

// adoptedFocus focuses a model on a pane on a foreign server, the way
// adoption leaves it: a store row carrying the pane's location, a driver that
// resolves the id to it, and no control client, because tmux control mode is
// refused on a pane someone else is watching.
func adoptedFocus(t testing.TB, command string) (*Model, string, string) {
	t.Helper()
	m := buildModel(t)
	socket, pane := uiForeignServer(t, command)
	const id = "adoptedfocus"
	if err := m.store.CreateSession(store.Session{
		ID:           id,
		Name:         "adopted",
		Tool:         "claude",
		Cwd:          t.TempDir(),
		Status:       status.Idle,
		CreatedAt:    time.Now(),
		LastStatusAt: time.Now(),
		TmuxSocket:   socket,
		TmuxPaneID:   pane,
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := m.tmux.Adopt(id, tmux.Target{Socket: socket, Name: pane}); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	m.applyCmd(t, nil)
	m.selectSessionRow(t, "adopted")
	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	*m = *updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("after enter, mode = %v, err = %q", m.mode, m.errBar.text)
	}
	return m, socket, pane
}

// foreignPaneContains waits for a foreign server's pane to show what was sent
// to it.
func foreignPaneContains(t testing.TB, socket, pane string, match func(string) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		out, err := exec.Command("tmux", "-L", socket, "capture-pane", "-p", "-t", pane).CombinedOutput()
		if err != nil {
			t.Fatalf("capture foreign pane: %v: %s", err, out)
		}
		if match(string(out)) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("nothing matching ever reached the adopted pane: %q", out)
		}
		time.Sleep(30 * time.Millisecond)
	}
}

// Focus mode types into whatever the driver resolves the session to. For an
// adopted pane that is a pane id on someone else's server, and the command
// has to be both aimed there and run there: gi_<id> on the manager's own
// socket names nothing at all.
func TestFocusKeysReachAnAdoptedPane(t *testing.T) {
	m, socket, pane := adoptedFocus(t, "cat")
	for _, msg := range []tea.KeyPressMsg{
		{Code: 'p', Text: "ping-adopted"},
		{Code: tea.KeyEnter},
	} {
		updated, _ := m.handleKey(msg)
		*m = *updated.(*Model)
	}
	if m.errBar.text != "" {
		t.Fatalf("forwarding set err: %q", m.errBar.text)
	}
	foreignPaneContains(t, socket, pane, func(out string) bool {
		return strings.Contains(out, "ping-adopted")
	})
}

// archiveInPlace marks a session archived without killing its pane, which is
// the state the archived view leaves behind after a revive (v) there.
func archiveInPlace(t *testing.T, m *Model, name string) {
	t.Helper()
	sess := m.sessionRows()[0]
	if sess.Name != name {
		t.Fatalf("first row = %q, want %q", sess.Name, name)
	}
	if err := m.store.SetArchived(sess.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	m.sessions[0].Archived = true
	m.showArchived = true
	m.rebuildRows()
	m.selectSessionRow(t, name)
	if !m.tmux.Exists(sess.ID) {
		t.Fatal("precondition: the archived row's pane should still exist")
	}
}

// The focus key must stay the focus key on every row. An archived row used to
// attach instead, which replaces the whole manager with the raw pane and
// leaves detaching as the only way back.
func TestFocusKeyOnArchivedRowNeverAttaches(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "shelved", t.TempDir(), "")
	archiveInPlace(t, m, "shelved")
	if !m.enterFocuses() {
		t.Fatal("precondition: enter should focus by default")
	}

	updated, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	*m = *updated.(*Model)
	if cmd != nil {
		t.Fatal("enter on an archived row started a command, want no attach")
	}
	if m.mode != modeList {
		t.Fatalf("mode = %v, want the list", m.mode)
	}
	if m.errBar.text != m.archivedFocusHint() {
		t.Fatalf("err = %q, want %q", m.errBar.text, m.archivedFocusHint())
	}
}

// The pair stays coherent when the setting swaps it: whichever key focuses is
// the one an archived row refuses, and the other still attaches.
func TestSwappedFocusKeyOnArchivedRowNeverAttaches(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "shelved-swap", t.TempDir(), "")
	m.openSettings()
	m.settings.field = settingsFieldFocusKey
	m.cycleSetting(1)
	m.handleSettingsKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.enterFocuses() {
		t.Fatal("precondition: the swap should leave attach on enter")
	}
	archiveInPlace(t, m, "shelved-swap")

	updated, cmd := m.handleKey(tea.KeyPressMsg{Code: 'A', Text: "A"})
	*m = *updated.(*Model)
	if cmd != nil {
		t.Fatal("A (the focus key here) started a command, want no attach")
	}
	if m.errBar.text != m.archivedFocusHint() {
		t.Fatalf("err = %q, want %q", m.errBar.text, m.archivedFocusHint())
	}
	if footer := ansi.Strip(m.viewFooter()); !strings.Contains(footer, "↵ attach") {
		t.Fatalf("the footer should name enter as what attaches here:\n%s", footer)
	}
	if _, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd == nil {
		t.Fatalf("enter (the attach key here) did not attach, err = %q", m.errBar.text)
	}
}

// The legend is where a row says what its keys mean, so an archived row has
// to stop advertising a focus key it will not honour.
func TestArchivedRowLegendOffersAttachAndRestore(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "shelved-legend", t.TempDir(), "")
	m.selectSessionRow(t, "shelved-legend")
	if live := ansi.Strip(m.viewFooter()); !strings.Contains(live, "↵ focus / fold") {
		t.Fatalf("a live row should offer focus on enter:\n%s", live)
	}

	archiveInPlace(t, m, "shelved-legend")
	footer := ansi.Strip(m.viewFooter())
	if strings.Contains(footer, "focus / fold") {
		t.Fatalf("an archived row still advertises focus on enter:\n%s", footer)
	}
	if !strings.Contains(footer, "A attach") {
		t.Fatalf("an archived row should name the key that attaches:\n%s", footer)
	}
	if !strings.Contains(footer, "u restore to focus") {
		t.Fatalf("an archived row should name the way back to focus:\n%s", footer)
	}
}
