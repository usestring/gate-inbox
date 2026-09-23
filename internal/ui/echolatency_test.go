package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/config"
)

// echoBudget is what a keystroke may cost before it is on screen.
//
// Measured on a real adopted pane on the development box, the echo lands in
// well under a millisecond: the keystroke goes down the pooled control pipe
// and the chase that follows it captures the pane the moment it repaints.
// The budget is two orders of magnitude looser than that because this runs
// on shared CI, where a fork or a scheduler hiccup is worth absorbing. It is
// still forty times tighter than what it replaced, which was up to a full
// preview tick -- 300ms, measured at 302ms mean on the same pane.
const echoBudget = 250 * time.Millisecond

// A focused adopted pane must echo typing without any control client of its
// own, and must echo it promptly.
//
// Both halves of that sentence are scar tissue. On 2026-08-27 the manager
// opened a control-mode client into the operator's own tmux server on every
// focus change; it reached 584 clients, the server died, and every live
// agent on it went with it. The first fix refused adopted panes outright,
// which held the invariant but left 23 of the operator's 25 panes echoing on
// a 300ms timer. This test pins both: nothing attaches to the operator's
// session, AND the character is on screen quickly.
func TestFocusedAdoptedPaneEchoesQuicklyWithNoClientOfItsOwn(t *testing.T) {
	m, socket, _ := adoptedFocus(t, "cat")
	sess, ok := m.selected()
	if !ok {
		t.Fatal("no session selected after focusing one")
	}
	// One poll, which is what re-asserts pane geometry: adoption sizes the
	// pane while the cursor is still on root, and the focused panel is a few
	// rows shorter than the panel that measured it. A pane taller than the
	// box it is painted into is cropped to its live end, so the row this
	// test types into would be the one cropped away.
	m.applyCmd(t, m.refreshCmd())

	const typed = "echo-marker-42"
	start := time.Now()
	updated, cmd := m.handleKey(tea.KeyPressMsg{Code: 'e', Text: typed})
	*m = *updated.(*Model)
	if m.errBar.text != "" {
		t.Fatalf("forwarding set err: %q", m.errBar.text)
	}
	runCmd(t, m, cmd)
	elapsed := time.Since(start)
	if !strings.Contains(m.frame(), typed) {
		t.Fatalf("the keystroke's own chase did not put %q on screen:\n%s", typed, m.frame())
	}
	if elapsed > echoBudget {
		t.Fatalf("echo took %v, budget %v", elapsed, echoBudget)
	}
	t.Logf("adopted pane keystroke-to-visible-echo: %v", elapsed)

	// The whole point of the chase is that it needs no client on the pane's
	// own server beyond the one anchor the manager attaches for itself.
	assertNoClientOnForeignSessions(t, socket)
	_ = sess
}

// A focused pane nobody is typing into must not be sampled hard. The fast
// cadence is bought by activity and has to be given back, or a pane left on
// screen costs the operator a capture every twelve milliseconds forever.
func TestIdleFocusedPaneFallsBackToTheCalmCadence(t *testing.T) {
	m, _, _ := adoptedFocus(t, "cat")
	if got := m.previewInterval(); got != focusIntervalActive {
		t.Fatalf("just focused: interval = %v, want %v", got, focusIntervalActive)
	}
	// Age the activity mark rather than sleeping out the real window: what
	// is under test is the rule, not the clock.
	m.focusActiveAt = time.Now().Add(-focusActiveFor - time.Millisecond)
	if got := m.previewInterval(); got != focusIntervalIdle {
		t.Fatalf("idle focused pane: interval = %v, want %v", got, focusIntervalIdle)
	}
	m.noteFocusActivity()
	if got := m.previewInterval(); got != focusIntervalActive {
		t.Fatalf("after a keystroke: interval = %v, want %v", got, focusIntervalActive)
	}
}

// runCmd runs a command and everything it batched, feeding each message back
// through Update. tea.Batch hands back a list of commands rather than a
// message, and a test that ran only the outer command would never execute the
// echo chase that is the subject here.
func runCmd(t testing.TB, m *Model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	switch msg := msg.(type) {
	case nil:
		return
	case tea.BatchMsg:
		for _, sub := range msg {
			runCmd(t, m, sub)
		}
	default:
		updated, next := m.Update(msg)
		*m = *updated.(*Model)
		runCmd(t, m, next)
	}
}

// assertNoClientOnForeignSessions is the outage regression check in its
// simplest form: whatever the manager attached to this server, it is not a
// session the manager did not create.
//
// It reads tmux's own client list rather than any counter the manager keeps,
// because the thing that went wrong was clients the manager had lost track
// of. The anchor is the one session the manager makes for itself, and a
// client on it is the pooled pipe every read now rides.
func assertNoClientOnForeignSessions(t *testing.T, socket string) {
	t.Helper()
	out, err := tmuxOnSocket(socket, "list-clients", "-F", "#{client_session}").CombinedOutput()
	if err != nil {
		// No clients at all makes list-clients exit non-zero on some tmux
		// versions, which is the strongest possible pass.
		return
	}
	for _, session := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		session = strings.TrimSpace(session)
		if session == "" || strings.HasPrefix(session, "gi_") {
			continue
		}
		t.Fatalf("the manager is a client of %q, a session it did not create: this is the attachment that killed the operator's tmux server", session)
	}
}

// A poll pass must not repaint over a frame captured while it was running.
//
// This is the failure the echo path made reachable. A pass takes seconds on a
// loaded board and carries the preview it read at the start of them; the
// keystroke chase captures in well under a millisecond. Without an ordering
// rule the sequence is: the character appears, the pass lands, the character
// vanishes, and the next tick brings it back -- a flicker on every keystroke
// that happens to race a pass.
func TestAPollPassDoesNotRepaintOverAFresherFrame(t *testing.T) {
	m, _, _ := adoptedFocus(t, "cat")
	sess, ok := m.selected()
	if !ok {
		t.Fatal("no session selected")
	}
	passStarted := time.Now()

	// The frame the keystroke chase captured, after that pass began.
	fresh := previewMsg{
		sessID:  sess.ID,
		preview: "typed-just-now\n",
		at:      passStarted.Add(500 * time.Millisecond),
	}
	updated, _ := m.Update(fresh)
	*m = *updated.(*Model)
	if m.preview != fresh.preview {
		t.Fatalf("the chase frame did not land: preview = %q", m.preview)
	}

	// The pass finishing, carrying what the pane looked like before the key.
	updated, _ = m.Update(refreshMsg{
		sessions:  m.sessions,
		listedAt:  passStarted,
		procFor:   sess.ID,
		preview:   "before-the-key\n",
		previewAt: passStarted,
	})
	*m = *updated.(*Model)
	if m.preview != fresh.preview {
		t.Fatalf("a poll frame captured at %v repainted over one captured at %v: preview = %q",
			passStarted, fresh.at, m.preview)
	}
}

// The echo chase must measure the keystroke against the pane, not against the
// frame on screen.
//
// Those two are the same thing only while nothing else has touched the pane
// since it was last painted, and plenty does: a resize reflows it, and a poll
// pass may have read it at a different size. When they differ, a chase that
// stopped at "the pane no longer matches what is on screen" was satisfied by
// its very first look -- reporting a frame captured *before* the key as
// though it were the echo, and leaving the typed character to wait for the
// next tick.
//
// This is that case made deterministic. The frame on screen is set to
// something the pane certainly does not contain, so a chase comparing against
// it would return immediately and paint a frame with no marker in it. The
// only way the marker arrives is if the baseline is a real capture of the
// pane taken before the key went out.
func TestTheEchoChaseMeasuresAgainstThePaneNotTheFrameOnScreen(t *testing.T) {
	needsQuietBox(t)
	m, _, _ := adoptedFocus(t, "cat")
	sess, ok := m.selected()
	if !ok {
		t.Fatal("no session selected after focusing one")
	}
	m.applyCmd(t, m.refreshCmd())

	// A frame on screen that does not describe the pane, which is what a
	// resize or a differently-sized poll capture leaves behind.
	m.preview = "a frame this pane has never held\n"

	const typed = "baseline-marker-7"
	updated, cmd := m.handleKey(tea.KeyPressMsg{Code: 'b', Text: typed})
	*m = *updated.(*Model)
	if m.errBar.text != "" {
		t.Fatalf("forwarding set err: %q", m.errBar.text)
	}
	runCmd(t, m, cmd)

	if !strings.Contains(m.frame(), typed) {
		t.Fatalf("the chase stopped on a frame captured before the key: %q never reached the screen, preview = %q",
			typed, m.preview)
	}
	_ = sess
}

// The chase's budget belongs to the tool in the pane, not to the board. A TUI
// that repaints slower than the default would otherwise have every keystroke
// handed to the tick, which is how codex came to feel laggy next to Claude
// Code on the same board.
func TestTheEchoBudgetFollowsTheToolInThePane(t *testing.T) {
	m := &Model{cfg: config.Config{Tools: map[string]config.Tool{
		"codex":  {Command: "codex", EchoBudget: config.Duration{Duration: 90 * time.Millisecond}},
		"claude": {Command: "claude"},
	}}}
	if got := m.echoBudget("codex"); got != 90*time.Millisecond {
		t.Fatalf("codex echo budget = %v want its own 90ms", got)
	}
	if got := m.echoBudget("claude"); got != focusEchoBudgetDefault {
		t.Fatalf("claude echo budget = %v want the default %v", got, focusEchoBudgetDefault)
	}
	if got := m.echoBudget("nothing-configured"); got != focusEchoBudgetDefault {
		t.Fatalf("unknown tool echo budget = %v want the default %v", got, focusEchoBudgetDefault)
	}
}
