package tmux

import (
	"strings"
	"testing"
)

// The poll pass reads three things about a pane it is about to type into --
// the screen, the caret and whether a person has typed there lately -- and
// it used to fork a tmux for each of the last two, per session, every pass.
// They ride the capture's own command list now, so what has to hold is that
// the batch answers exactly what those two commands answer.
func TestCapturedStateMatchesTheCommandsItReplaces(t *testing.T) {
	driver := requireTmux(t)
	id := uniqueID("carets")
	const prompt = "state-marker> "
	if err := driver.Create(id, "/tmp", "printf '"+prompt+"'; exec cat", nil, 80, 24); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { driver.Kill(id) })
	waitForPane(t, driver.socket, sessionName(id), strings.TrimSpace(prompt))

	capture, listed := driver.CapturePanes([]string{id})[id]
	if !listed || capture.Err != nil {
		t.Fatalf("capture: listed=%v err=%v", listed, capture.Err)
	}
	if !capture.State.Read {
		t.Fatalf("the chain brought back no state for %s: %q", id, capture.Text)
	}
	// The caret sits after the prompt the tool drew, which is the shape the
	// draft check reads: a caret at the end of a marker row and nothing
	// written past it is an empty composer.
	if capture.State.CursorX != len(prompt) || capture.State.CursorY != 0 {
		t.Errorf("caret at %d,%d, want %d,0 -- one column past %q",
			capture.State.CursorX, capture.State.CursorY, len(prompt), prompt)
	}
	x, y, err := driver.Cursor(id)
	if err != nil {
		t.Fatalf("Cursor: %v", err)
	}
	if x != capture.State.CursorX || y != capture.State.CursorY {
		t.Errorf("the chain read the caret at %d,%d where display-message reads %d,%d",
			capture.State.CursorX, capture.State.CursorY, x, y)
	}
	at, err := driver.SessionInputAt(id)
	if err != nil {
		t.Fatalf("SessionInputAt: %v", err)
	}
	if !capture.State.InputAt.Equal(at) {
		t.Errorf("the chain read the last keystroke at %v where SessionInputAt reads %v", capture.State.InputAt, at)
	}
	// And specifically the zero time here, since a session no client has
	// attached to must not report its own creation as somebody typing --
	// that would hold every message to a fresh session for three seconds.
	if !at.IsZero() {
		t.Errorf("a session nobody has typed into reports a keystroke at %v", at)
	}
}

// The scrollback sweep asks for no state, so its blocks must come back the
// way they always did: text only, and the separator still the thing that
// ends them.
func TestAChainWithoutStateReadsTheSameText(t *testing.T) {
	driver := requireTmux(t)
	socket, panes := chainServer(t, 3)
	ids := adoptAll(t, driver, socket, panes)

	withState, states, err := driver.chainCapture(socket, targetsOf(driver, ids), true, "-e")
	if err != nil {
		t.Fatalf("chained capture with state: %v", err)
	}
	plain, none, err := driver.chainCapture(socket, targetsOf(driver, ids), false, "-e")
	if err != nil {
		t.Fatalf("chained capture without state: %v", err)
	}
	for i := range ids {
		if withState[i] != plain[i] {
			t.Errorf("pane %d: asking for state changed the text\nwith:    %q\nwithout: %q",
				i, tailOf(withState[i]), tailOf(plain[i]))
		}
		if !states[i].Read {
			t.Errorf("pane %d: no state came back", i)
		}
		if none[i].Read {
			t.Errorf("pane %d: state came back from a chain that asked for none: %+v", i, none[i])
		}
	}
}

func targetsOf(driver *Driver, ids []string) []string {
	targets := make([]string, len(ids))
	for i, id := range ids {
		targets[i] = driver.TargetName(id)
	}
	return targets
}
