package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/keymap"
)

// runView is a view that records the keys it is told and draws what it is
// given.
type runView struct {
	lines       [][]Span
	keys        []ViewKey
	closeOn     string
	panicRender bool
	panicKey    bool
}

func (v *runView) Title() string { return "Task: \x1b[2Jwidgets" }

func (v *runView) Render(width, height int) [][]Span {
	if v.panicRender {
		panic("render broke")
	}
	return v.lines
}

func (v *runView) Key(key ViewKey) bool {
	if v.panicKey {
		panic("key broke")
	}
	v.keys = append(v.keys, key)
	return key.Action != "" && key.Action == v.closeOn
}

// viewModel is a list with one extension that declares a task screen, and the
// bridge its messages arrive on.
func viewModel(t *testing.T, keys ...ExtensionKey) (*Model, *ExtensionBridge, chan tea.Msg) {
	t.Helper()
	m := childModel(t)
	if len(keys) == 0 {
		keys = []ExtensionKey{
			{Screen: "task", Action: "nudge", Keys: []string{"r"}, Label: "nudge it"},
			{Screen: "task", Action: "end_task", Keys: []string{"x"}, Label: "end the task"},
		}
	}
	bridge := NewExtensionBridge([]string{"cards"})
	m.InstallExtensions([]ExtensionUI{{Owner: "cards", Keys: keys}}, bridge)
	sent := make(chan tea.Msg, 16)
	bridge.Attach(func(msg tea.Msg) { sent <- msg })
	return m, bridge, sent
}

func deliver(t *testing.T, m *Model, sent chan tea.Msg) {
	t.Helper()
	select {
	case msg := <-sent:
		m.Update(msg)
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was delivered")
	}
}

func viewKey(t *testing.T, m *Model, k tea.KeyPressMsg) {
	t.Helper()
	if _, cmd := m.handleKey(k); cmd != nil {
		t.Fatalf("a view key asked for a command")
	}
}

// A view opened from the list draws inside the board's card with its title,
// its body and its screen's keys as the hint, and is told each press with the
// action it stands for there.
func TestAnExtensionViewDrawsAndIsToldItsKeys(t *testing.T) {
	m, bridge, sent := viewModel(t)
	view := &runView{lines: [][]Span{
		{{Text: "goal ", Bold: true}, {Text: "widgets\tall\x1b[31m regions", Tone: ToneAccent}},
		{{Text: "3/5 done", Tone: ToneGood}},
	}, closeOn: "end_task"}
	bridge.Open("cards", "task", view)
	deliver(t, m, sent)
	if m.mode != modeExtensionView {
		t.Fatalf("mode = %s", m.mode)
	}
	frame := ansi.Strip(m.viewExtension())
	for _, want := range []string{"Task: [2Jwidgets", "goal widgetsall[31m regions", "3/5 done", "nudge it", "end the task", "close"} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame lacks %q:\n%s", want, frame)
		}
	}
	if strings.Contains(m.viewExtension(), "\x1b[2J") || strings.Contains(m.viewExtension(), "\t") {
		t.Fatal("the frame carries the view's control characters")
	}

	viewKey(t, m, key("r"))
	viewKey(t, m, key("é"))
	if len(view.keys) != 2 || view.keys[0].Action != "nudge" || view.keys[1].Action != "" || view.keys[1].Text != "é" {
		t.Fatalf("keys = %+v", view.keys)
	}
	viewKey(t, m, key("x"))
	if m.mode != modeList {
		t.Fatal("a key the view answered true to did not close it")
	}
}

// Every view screen has a way off it: esc when the extension binds no close,
// and the board answers close itself, before the view sees it.
func TestAViewScreenAlwaysHasAClose(t *testing.T) {
	m, bridge, sent := viewModel(t)
	view := &runView{}
	bridge.Open("cards", "task", view)
	deliver(t, m, sent)
	viewKey(t, m, key("esc"))
	if m.mode != modeList || len(view.keys) != 0 {
		t.Fatalf("esc: mode %s, view told %+v", m.mode, view.keys)
	}
	if _, problems := m.km().Rebind("task", ActionClose, nil); len(problems) == 0 {
		t.Fatal("the task screen's close could be unbound")
	}

	m, bridge, sent = viewModel(t, ExtensionKey{Screen: "task", Action: "close", Keys: []string{"q"}, Label: "back"})
	bridge.Open("cards", "task", &runView{})
	deliver(t, m, sent)
	viewKey(t, m, key("q"))
	if m.mode != modeList {
		t.Fatal("the extension's own close did not close the view")
	}
	if action, ok := m.km().Action("task", "esc"); ok {
		t.Fatalf("esc is bound to %s although the extension named its own close", action)
	}
}

// The operator's key file reaches a view screen as a table of its own, and
// the key map lists the screen's keys under the extension and the screen.
func TestTheKeyFileReachesAViewScreen(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, keymap.FileName), []byte("[task]\nnudge = [\"s\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := childModel(t)
	m.hooks = hooks.NewManager(dir)
	bridge := NewExtensionBridge([]string{"cards"})
	m.InstallExtensions([]ExtensionUI{{Owner: "cards", Keys: []ExtensionKey{
		{Screen: "task", Action: "nudge", Keys: []string{"r"}, Label: "nudge it"},
	}}}, bridge)
	if len(m.keyProblems) != 0 {
		t.Fatalf("problems: %v", m.keyProblems)
	}
	if action, _ := m.km().Action("task", "s"); action != "nudge" {
		t.Fatalf("s on task answers %q", action)
	}
	var titles []string
	for _, section := range m.resolvedHelp() {
		titles = append(titles, section.title)
	}
	if !strings.Contains(strings.Join(titles, "|"), "cards · task") {
		t.Fatalf("sections: %v", titles)
	}
}

// A view asked for while the operator is on another screen is not shown, a
// handle to a view no longer on screen does nothing, and a screen nobody
// declared keys for is refused.
func TestAViewOpensOnlyWhereItCannotTakeTheScreenAway(t *testing.T) {
	m, bridge, sent := viewModel(t)
	m.mode = modeHelp
	bridge.Open("cards", "task", &runView{})
	deliver(t, m, sent)
	if m.mode != modeHelp {
		t.Fatalf("mode = %s, want the help screen left up", m.mode)
	}

	m.mode = modeList
	stale := bridge.Open("cards", "task", &runView{})
	deliver(t, m, sent)
	current := bridge.Open("cards", "task", &runView{})
	deliver(t, m, sent)
	stale.Close()
	deliver(t, m, sent)
	if m.mode != modeExtensionView {
		t.Fatal("a stale handle closed the view that replaced it")
	}
	current.Refresh()
	deliver(t, m, sent)
	current.Close()
	deliver(t, m, sent)
	if m.mode != modeList {
		t.Fatal("the handle did not close its view")
	}

	bridge.Open("cards", "elsewhere", &runView{})
	deliver(t, m, sent)
	if m.mode != modeList || !strings.Contains(m.errBar.text, "elsewhere") {
		t.Fatalf("an undeclared screen: mode %s, bar %q", m.mode, m.errBar.text)
	}
}

// A view that panics is closed and reported, and the board goes on.
func TestAPanickingViewIsClosed(t *testing.T) {
	for name, view := range map[string]*runView{
		"render": {panicRender: true},
		"key":    {panicKey: true},
	} {
		m, bridge, sent := viewModel(t)
		bridge.Open("cards", "task", view)
		deliver(t, m, sent)
		if name == "render" {
			m.viewExtension()
		} else {
			viewKey(t, m, key("r"))
		}
		if m.mode != modeList || !strings.Contains(m.errBar.text, "cards: its view failed") {
			t.Errorf("%s: mode %s, bar %q", name, m.mode, m.errBar.text)
		}
	}
}

// An extension may not add keys to one of the board's own screens other
// than the list: those answer every key themselves.
func TestExtensionKeysStayOffTheBoardsOwnScreens(t *testing.T) {
	m, _, _ := viewModel(t, ExtensionKey{Screen: string(keymap.ContextFocus), Action: "peek", Keys: []string{"alt+p"}})
	if action, ok := m.km().Action(keymap.ContextFocus, "alt+p"); ok {
		t.Fatalf("alt+p in focus answers %s", action)
	}
	if len(m.extScreens) != 0 {
		t.Fatalf("screens: %v", m.extScreens)
	}
}
