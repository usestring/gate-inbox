package ui

import (
	"strings"
	"testing"
)

// closingView records why it was closed, and runs onClose when told.
type closingView struct {
	runView
	reasons    []CloseReason
	onClose    func(CloseReason)
	panicClose bool
}

func (v *closingView) Closed(reason CloseReason) {
	if v.panicClose {
		panic("closed broke")
	}
	v.reasons = append(v.reasons, reason)
	if v.onClose != nil {
		v.onClose(reason)
	}
}

func oneReason(t *testing.T, name string, v *closingView, want CloseReason) {
	t.Helper()
	if len(v.reasons) != 1 || v.reasons[0] != want {
		t.Errorf("%s: reasons %v, want [%s]", name, v.reasons, want)
	}
}

// A view is told once why it was closed, whichever way it went: the
// screen's close, a press it answered true to, a submit, its handle, or
// another view opened over it.
func TestAViewIsToldWhyItWasClosed(t *testing.T) {
	m, bridge, sent := viewModel(t)
	view := &closingView{}
	bridge.Open("cards", "task", view)
	deliver(t, m, sent)
	viewKey(t, m, key("esc"))
	oneReason(t, "esc", view, CloseDismissed)
	if len(view.keys) != 0 {
		t.Fatalf("esc reached the view: %+v", view.keys)
	}

	view = &closingView{runView: runView{closeOn: "end_task"}}
	bridge.Open("cards", "task", view)
	deliver(t, m, sent)
	viewKey(t, m, key("x"))
	oneReason(t, "a press", view, CloseReturned)

	view = &closingView{runView: runView{closeOn: string(ActionSubmit)}}
	bridge.Open("cards", "task", view)
	deliver(t, m, sent)
	m.tellView(ViewKey{Action: string(ActionSubmit)})
	oneReason(t, "submit", view, CloseSubmitted)

	view = &closingView{}
	handle := bridge.Open("cards", "task", view)
	deliver(t, m, sent)
	handle.Close()
	deliver(t, m, sent)
	oneReason(t, "handle", view, CloseHandle)
	handle.Close()
	deliver(t, m, sent)
	oneReason(t, "a second handle close", view, CloseHandle)

	view = &closingView{}
	bridge.Open("cards", "task", view)
	deliver(t, m, sent)
	next := &closingView{}
	bridge.Open("cards", "task", next)
	deliver(t, m, sent)
	oneReason(t, "replaced", view, CloseReplaced)
	if m.mode != modeExtensionView || m.extView.view != next || len(next.reasons) != 0 {
		t.Fatalf("the replacing view is not the one up: mode %s", m.mode)
	}
}

// A child view that reopens its parent from Closed puts the parent back on
// screen: Closed runs with the board already on its list.
func TestAClosedViewCanReopenItsParent(t *testing.T) {
	m, bridge, sent := viewModel(t)
	parent := &closingView{runView: runView{lines: [][]Span{{{Text: "the parent"}}}}}
	var modeInClosed mode
	child := &closingView{onClose: func(reason CloseReason) {
		modeInClosed = m.mode
		if reason == CloseDismissed {
			bridge.Open("cards", "task", parent)
		}
	}}
	bridge.Open("cards", "task", child)
	deliver(t, m, sent)
	viewKey(t, m, key("esc"))
	if modeInClosed != modeList {
		t.Fatalf("Closed ran in mode %s, want the list", modeInClosed)
	}
	deliver(t, m, sent)
	if m.mode != modeExtensionView || m.extView.view != parent {
		t.Fatalf("mode %s: the parent was not reopened", m.mode)
	}
	if frame := m.viewExtension(); !strings.Contains(frame, "the parent") {
		t.Fatalf("frame lacks the parent:\n%s", frame)
	}
}

// A view that panicked is not told it was closed, and a Closed that panics
// is reported while the board stays on its list.
func TestClosedPanicsAndPanickedViews(t *testing.T) {
	m, bridge, sent := viewModel(t)
	broken := &closingView{runView: runView{panicKey: true}}
	bridge.Open("cards", "task", broken)
	deliver(t, m, sent)
	viewKey(t, m, key("r"))
	if m.mode != modeList || len(broken.reasons) != 0 {
		t.Fatalf("a panicked view: mode %s, reasons %v", m.mode, broken.reasons)
	}

	m.errBar.text = ""
	bridge.Open("cards", "task", &closingView{panicClose: true})
	deliver(t, m, sent)
	viewKey(t, m, key("esc"))
	if m.mode != modeList || !strings.Contains(m.errBar.text, "cards: its view failed as it closed") {
		t.Fatalf("a panicking Closed: mode %s, bar %q", m.mode, m.errBar.text)
	}
}
