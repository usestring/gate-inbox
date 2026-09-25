package sessioncmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// paneRunning files an agent row of tool whose pane runs script, once the
// pane shows ready.
func paneRunning(t *testing.T, h *sessionHarness, id, tool, script, ready string) store.Session {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "pane.sh")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}
	sess := store.Session{ID: id, Name: id, Tool: tool, Cwd: dir, Group: "backend", Status: status.Idle}
	if err := h.driver.Create(sess.ID, dir, "sh "+path, nil, 80, 24); err != nil {
		t.Fatalf("create pane: %v", err)
	}
	t.Cleanup(func() { _ = h.driver.Kill(sess.ID) })
	waitPane(t, h, sess.ID, ready)
	if err := h.store.CreateSession(sess); err != nil {
		t.Fatalf("create row: %v", err)
	}
	return sess
}

func waitPane(t *testing.T, h *sessionHarness, id, want string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		pane, err := h.driver.CapturePane(id)
		if err == nil && strings.Contains(ansi.Strip(pane), want) {
			return ansi.Strip(pane)
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane never showed %q:\n%s", want, pane)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func shortConfirmWatch(t *testing.T) {
	wait, poll := confirmWait, confirmPoll
	confirmWait, confirmPoll = 600*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { confirmWait, confirmPoll = wait, poll })
}

const promptThenEcho = `printf '❯ '; read l; printf 'ran:%s\n' "$l"; sleep 60`

func TestBoardCommandTypesAtARestingPrompt(t *testing.T) {
	h := newSessionHarness(t)
	sess := paneRunning(t, h, "cmd00001", "echoer", promptThenEcho, "❯")
	if err := h.sessions.BoardCommand(context.Background(), sess.ID, "/model fast", ""); err != nil {
		t.Fatalf("BoardCommand: %v", err)
	}
	waitPane(t, h, sess.ID, "ran:/model fast")
}

// A command's own confirmation is answered once, on the row that goes
// ahead, and the command is done when it clears.
func TestBoardCommandConfirmsItsOwnDialog(t *testing.T) {
	shortConfirmWatch(t)
	h := newSessionHarness(t)
	script := `printf '❯ '; read l
printf 'Switch model?\n\n❯ 1. Yes, switch to Fast\n  2. No, go back\n'
read a; printf '\033[H\033[2Jswitched:%s\n❯ ' "$l"; sleep 60`
	sess := paneRunning(t, h, "cmd00002", "echoer", script, "❯")
	if err := h.sessions.BoardCommand(context.Background(), sess.ID, "/model fast", "yes, switch to"); err != nil {
		t.Fatalf("BoardCommand: %v", err)
	}
	waitPane(t, h, sess.ID, "switched:/model fast")
}

// A menu whose selection is on anything but the go-ahead is only read.
func TestBoardCommandLeavesAnotherSelectionAlone(t *testing.T) {
	shortConfirmWatch(t)
	h := newSessionHarness(t)
	script := `printf '❯ '; read l
printf 'Switch model?\n\n❯ 1. No, go back\n  2. Yes, switch to Fast\n'
read a; printf 'answered\n'; sleep 60`
	sess := paneRunning(t, h, "cmd00003", "echoer", script, "❯")
	if err := h.sessions.BoardCommand(context.Background(), sess.ID, "/model fast", "Yes, switch to"); err != nil {
		t.Fatalf("BoardCommand: %v", err)
	}
	pane := waitPane(t, h, sess.ID, "No, go back")
	if strings.Contains(pane, "answered") {
		t.Fatalf("a menu selected on another row was answered:\n%s", pane)
	}
}

// A confirmation that does not clear after Enter is reported, not assumed.
func TestBoardCommandReportsAConfirmationThatStays(t *testing.T) {
	shortConfirmWatch(t)
	h := newSessionHarness(t)
	script := `printf '❯ '; read l
printf 'Switch model?\n\n❯ 1. Yes, switch to Fast\n  2. No, go back\n'; sleep 60`
	sess := paneRunning(t, h, "cmd00004", "echoer", script, "❯")
	err := h.sessions.BoardCommand(context.Background(), sess.ID, "/model fast", "Yes, switch to")
	if !errors.Is(err, extension.ErrCommandHeld) {
		t.Fatalf("BoardCommand err = %v, want ErrCommandHeld", err)
	}
}

func TestBoardCommandRefusals(t *testing.T) {
	h := newSessionHarness(t)
	ctx := context.Background()
	resting := paneRunning(t, h, "cmd00010", "echoer", promptThenEcho, "❯")
	dialogPane := paneRunning(t, h, "cmd00011", "dialog",
		`printf 'Do you want to proceed?\n  1. Yes\n  2. No\nEnter to confirm\n❯ '; sleep 60`, "Enter to confirm")
	blind := paneRunning(t, h, "cmd00012", "blind", promptThenEcho, "❯")
	parked := paneRunning(t, h, "cmd00013", "scroller",
		`printf 'history\nJump to bottom\n❯ '; sleep 60`, "Jump to bottom")
	for _, tc := range []struct {
		name, id, text string
		want           error
	}{
		{"prose", resting.ID, "please switch", nil},
		{"two lines", resting.ID, "/model\nfast", nil},
		{"on a dialog", dialogPane.ID, "/model fast", extension.ErrNotAtPrompt},
		{"parked", parked.ID, "/model fast", extension.ErrNotAtPrompt},
		{"no input line", blind.ID, "/model fast", extension.ErrNoCommandLine},
	} {
		err := h.sessions.BoardCommand(ctx, tc.id, tc.text, "")
		if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
			t.Errorf("%s: err = %v, want a refusal wrapping %v", tc.name, err, tc.want)
		}
	}
	if pane := waitPane(t, h, resting.ID, "❯"); strings.Contains(pane, "ran:") {
		t.Fatalf("a refused command reached the pane:\n%s", pane)
	}
	if err := h.driver.Kill(resting.ID); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if err := h.sessions.BoardCommand(ctx, resting.ID, "/model fast", ""); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("dead session: err = %v, want not running", err)
	}
}

// BoardRead's AtPrompt is the reading BoardCommand refuses on: true exactly
// where a command would be typed.
func TestBoardReadAtPromptIsWhereACommandWouldBeTyped(t *testing.T) {
	h := newSessionHarness(t)
	resting := paneRunning(t, h, "cmd00030", "echoer", promptThenEcho, "❯")
	dialogPane := paneRunning(t, h, "cmd00031", "dialog",
		`printf 'Do you want to proceed?\n  1. Yes\n  2. No\nEnter to confirm\n❯ '; sleep 60`, "Enter to confirm")
	blind := paneRunning(t, h, "cmd00032", "blind", promptThenEcho, "❯")
	parked := paneRunning(t, h, "cmd00033", "scroller",
		`printf 'history\nJump to bottom\n❯ '; sleep 60`, "Jump to bottom")
	for _, tc := range []struct {
		name, id string
		want     bool
	}{
		{"resting", resting.ID, true},
		{"on a dialog", dialogPane.ID, false},
		{"no input line", blind.ID, false},
		{"parked", parked.ID, false},
	} {
		read, err := h.sessions.BoardRead(tc.id)
		if err != nil {
			t.Fatalf("%s: BoardRead: %v", tc.name, err)
		}
		if read.AtPrompt != tc.want {
			t.Errorf("%s: AtPrompt = %v, want %v", tc.name, read.AtPrompt, tc.want)
		}
		if got := h.sessions.BoardCommand(context.Background(), tc.id, "/model fast", "") == nil; tc.name != "resting" && got {
			t.Errorf("%s: BoardCommand typed where AtPrompt is false", tc.name)
		}
	}
	if err := h.driver.Kill(resting.ID); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if read, err := h.sessions.BoardRead(resting.ID); err != nil || read.Live || read.AtPrompt {
		t.Fatalf("a dead session: %+v, %v; want a stored screen, not at its prompt", read, err)
	}
}

func TestBoardUnparkPressesTheJumpBackKey(t *testing.T) {
	h := newSessionHarness(t)
	sess := paneRunning(t, h, "cmd00020", "scroller",
		`printf 'history\nJump to bottom\n'; read l; printf '\033[H\033[2Jlive\n❯ '; sleep 60`, "Jump to bottom")
	parked, err := h.sessions.BoardUnpark(sess.ID)
	if err != nil || !parked {
		t.Fatalf("BoardUnpark = %v, %v; want parked", parked, err)
	}
	waitPane(t, h, sess.ID, "live")
	parked, err = h.sessions.BoardUnpark(sess.ID)
	if err != nil || parked {
		t.Fatalf("BoardUnpark at the bottom = %v, %v; want not parked", parked, err)
	}
	// A tool with no affordance configured is never parked, whatever it shows.
	other := paneRunning(t, h, "cmd00021", "echoer", `printf 'Jump to bottom\n❯ '; sleep 60`, "Jump to bottom")
	if parked, err := h.sessions.BoardUnpark(other.ID); err != nil || parked {
		t.Fatalf("BoardUnpark without an affordance = %v, %v; want not parked", parked, err)
	}
}
