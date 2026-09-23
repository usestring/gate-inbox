package ui

import (
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// The question-dialog Left fix, end to end over the real thing.
//
// Everything else about this fix poses its pane by hand: a fixture for the
// dialog, a cursor where tmux once reported one. That pins the decision
// logic, and it is exactly what kept passing while the operator stayed
// stuck -- the fixture cannot drift from what opencode draws, and the posed
// cursor cannot be where opencode really parks it. So this one launches a
// real opencode, has it ask a real question through its question tool, and
// proves both halves against the live pane: Left is a no-op inside the
// dialog itself, and the manager still steps back to the list on it.
//
// It needs a working opencode on PATH with a default model that answers --
// one question-tool turn -- and takes a few minutes, so it skips unless
// GATE_INBOX_E2E_OPENCODE_QUESTION is set -- an operator check, not CI:
//
//	GATE_INBOX_E2E_OPENCODE_QUESTION=1 \
//	  go test ./internal/ui/ -run TestOpencodeQuestionDialogLeavesFocusE2E -timeout 15m -v
func TestOpencodeQuestionDialogLeavesFocusE2E(t *testing.T) {
	if os.Getenv("GATE_INBOX_E2E_OPENCODE_QUESTION") == "" {
		t.Skip("GATE_INBOX_E2E_OPENCODE_QUESTION unset")
	}
	if _, err := exec.LookPath("opencode"); err != nil {
		t.Skip("opencode not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	m := buildModel(t)
	// The shipped rules are what has to read the live pane; the fixture
	// config carries no opencode tool.
	m.engine = liveEngine(t)

	dir := t.TempDir()
	sess := store.Session{
		ID: newID(), Name: "oc-e2e", Tool: "opencode", Cwd: dir,
		Status: status.Idle, CreatedAt: time.Now(), LastStatusAt: time.Now(),
	}
	if err := m.tmux.Create(sess.ID, dir, "opencode", nil, m.previewPaneWidth(), m.previewPaneHeight()); err != nil {
		t.Fatalf("launch opencode: %v", err)
	}
	t.Cleanup(func() { m.tmux.Kill(sess.ID) })
	if err := m.store.CreateSession(sess); err != nil {
		t.Fatalf("store session: %v", err)
	}
	m.sessions = append(m.sessions, sess)
	m.rebuildRows()

	boot := e2eWaitFor(t, 90*time.Second, "opencode composer", func(pane string) bool {
		return strings.Contains(pane, "┃") && strings.Contains(pane, "╹")
	}, func() string { return e2eCapture(t, m, sess.ID) })
	_ = boot

	if err := m.tmux.SendText(sess.ID, "use the question tool to ask me which color I like, with 3 options. Just ask, without running anything else first."); err != nil {
		t.Fatalf("send prompt: %v", err)
	}
	// The prompt has to be visible in the composer before Enter, or the key
	// submits an empty box and the agent idles instead of asking.
	e2eWaitFor(t, 60*time.Second, "prompt text in composer", func(pane string) bool {
		return strings.Contains(ansi.Strip(pane), "which color I like")
	}, func() string { return e2eCapture(t, m, sess.ID) })
	if err := m.tmux.SendKeys(sess.ID, "Enter"); err != nil {
		t.Fatalf("submit prompt: %v", err)
	}
	dialog := e2eWaitFor(t, 6*time.Minute, "opencode question dialog", func(pane string) bool {
		// The control-mode capture carries styling; the rules read the
		// plain text underneath, the same stripping the preview path
		// applies before every match.
		plain := ansi.Strip(pane)
		state, matched := m.engine.RuleMatch("opencode", plain)
		return matched && state == status.Waiting &&
			opencodeQuestionLegend.MatchString(plain) &&
			!m.engine.DialogOwnsArrows("opencode", plain)
	}, func() string { return e2eCapture(t, m, sess.ID) })
	_ = dialog
	cursorBefore := e2eCursor(t, m, sess.ID)
	t.Logf("dialog up; caret parked at %+v", cursorBefore)

	// Half one: Left is a no-op inside the live dialog, so it is spare and
	// the manager may spend it. The clock in the status column ticks, so it
	// is masked out of the comparison.
	if err := m.tmux.SendKeys(sess.ID, "Left"); err != nil {
		t.Fatalf("send Left to the pane: %v", err)
	}
	time.Sleep(2 * time.Second)
	after := e2eCapture(t, m, sess.ID)
	if e2eMaskClock(after) != e2eMaskClock(dialog) {
		t.Fatalf("Left changed the live dialog, so it is not spare:\nbefore:\n%s\nafter:\n%s", e2eTail(dialog), e2eTail(after))
	}
	if cursorAfter := e2eCursor(t, m, sess.ID); cursorAfter != cursorBefore {
		t.Fatalf("Left moved the live caret from %+v to %+v, so it is not spare", cursorBefore, cursorAfter)
	}
	if state, matched := m.engine.RuleMatch("opencode", ansi.Strip(after)); !matched || state != status.Waiting {
		t.Fatalf("the dialog stopped reading waiting after Left: (%q, %v)", state, matched)
	}

	// Half two: the manager steps back to the list on that same key, read
	// through the production preview and cursor path rather than posed rows.
	m.selectSessionRow(t, "oc-e2e")
	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	*m = *updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("after enter, mode = %v, err = %q", m.mode, m.errBar.text)
	}
	m.applyCmd(t, m.previewCmd(m.rows[m.cursor].sess, m.previewGen, false))
	// The focus path repins the window to the preview size, so the first
	// capture can land mid-resize; poll until the dialog is on screen.
	e2eWaitFor(t, 60*time.Second, "dialog in focused preview", func(_ string) bool {
		m.applyCmd(t, m.previewCmd(m.rows[m.cursor].sess, m.previewGen, false))
		return strings.Contains(ansi.Strip(m.preview), "esc dismiss")
	}, func() string { return m.preview })
	t.Logf("focused preview caret at %+v", m.pane.cursor)
	// Production renders before any key arrives, which records the painted
	// box paneTextLines reads through; pose the same so the key travels
	// the production text path rather than the pre-render fallback.
	rows := len(strings.Split(strings.TrimSuffix(m.preview, "\n"), "\n"))
	m.pane.box.height, m.pane.box.width = rows, 500
	updated, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	*m = *updated.(*Model)
	if m.mode != modeList {
		t.Fatalf("Left did not leave the live opencode dialog, mode = %v, err = %q", m.mode, m.errBar.text)
	}
}

// e2eWaitFor polls the live pane until ok holds, returning the pane that
// held. On expiry it fails with the tail of what the pane actually showed,
// which is what says whether opencode was still booting, still thinking, or
// parked somewhere unexpected.
func e2eWaitFor(t *testing.T, timeout time.Duration, what string, ok func(pane string) bool, capture func() string) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	start := time.Now()
	for {
		pane := capture()
		if ok(pane) {
			return pane
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s; pane showed:\n%s", what, e2eTail(pane))
		}
		// A stalled wait is otherwise silent for minutes; log where the
		// pane is so a timeout says whether opencode was still thinking,
		// parked on trust, or errored.
		if elapsed := time.Since(start); elapsed > 60*time.Second && int(elapsed.Seconds())%60 < 2 {
			t.Logf("still waiting for %s at %v; pane tail:\n%s", what, elapsed.Round(time.Second), e2eTail(pane))
		}
		time.Sleep(2 * time.Second)
	}
}

func e2eCapture(t *testing.T, m *Model, id string) string {
	t.Helper()
	pane, err := m.tmux.CapturePane(id)
	if err != nil {
		t.Fatalf("capture pane: %v", err)
	}
	return pane
}

type e2ePoint struct{ x, y int }

func e2eCursor(t *testing.T, m *Model, id string) e2ePoint {
	t.Helper()
	state, err := m.tmux.PaneState(id, paneStateFormat)
	if err != nil {
		t.Fatalf("pane state: %v", err)
	}
	parts := strings.Split(strings.TrimSpace(state), ",")
	if len(parts) < 2 {
		t.Fatalf("unparseable pane state %q", state)
	}
	x, errX := strconv.Atoi(strings.TrimSpace(parts[0]))
	y, errY := strconv.Atoi(strings.TrimSpace(parts[1]))
	if errX != nil || errY != nil {
		t.Fatalf("unparseable pane state %q", state)
	}
	return e2ePoint{x: x, y: y}
}

var e2eClock = regexp.MustCompile(`\d{1,2}:\d{2}\s?[AP]M`)

func e2eMaskClock(pane string) string { return e2eClock.ReplaceAllString(pane, "HH:MM") }

// e2eTail keeps failure output to the live end of the pane, where whatever
// the test was waiting on either is or is not.
func e2eTail(pane string) string {
	lines := strings.Split(strings.TrimSuffix(pane, "\n"), "\n")
	if len(lines) > 25 {
		lines = lines[len(lines)-25:]
	}
	return strings.Join(lines, "\n")
}
