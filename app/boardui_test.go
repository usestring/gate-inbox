package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// TestExternalBuildAddsKeysAndBadgesToTheBoard is the proof for the UI seam:
// a module importing app and extension only puts a badge on the rows it is
// told about, and a key of its own on the list answers with the row it was
// pressed on.
func TestExternalBuildAddsKeysAndBadgesToTheBoard(t *testing.T) {
	script, err := exec.LookPath("script")
	if err != nil {
		t.Skip("script(1) is needed to give the board a terminal")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("the board polls a tmux server")
	}
	bin := buildFixture(t)
	socket := tmuxtest.NewSocket("ui")
	env := fixtureHome(t, "tmux_socket = \""+socket+"\"\n")
	home := envValue(env, "GATE_INBOX_HOME")
	t.Cleanup(func() { killTestServer(t, envValue(env, "TMUX_TMPDIR"), socket) })
	seedSessions(t, filepath.Join(home, "state.db"))
	skipWelcome(t, filepath.Join(home, "state.db"))
	data := filepath.Join(home, "extensions", "noop")

	// script(1)'s terminal has no size until one is set, and a board with
	// no columns draws no rows to badge.
	sized := "stty cols 240 rows 40 && exec " + bin
	board := exec.Command(script, "-qec", sized, "/dev/null")
	if runtime.GOOS != "linux" {
		board = exec.Command(script, "-q", "/dev/null", "sh", "-c", sized)
	}
	board.Env = env
	keys, err := board.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out := &syncBuffer{}
	board.Stdout, board.Stderr = out, out
	if err := board.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() {
		board.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		keys.Close()
		board.Process.Kill()
		<-exited
	})

	// A fresh board asks about the seeded sessions' missing panes first;
	// esc answers that it should leave them, and uncovers the list.
	waitForOutput(t, out, "noop:dead", exited, func() { keys.Write([]byte("\x1b")) })

	// The press is repeated until it lands: a prompt the board raises at
	// start can hold the first one, and esc puts any of them away.
	pressed := filepath.Join(data, "pressed.txt")
	deadline := time.Now().Add(20 * time.Second)
	for {
		if body, err := os.ReadFile(pressed); err == nil && len(body) > 0 {
			if got := string(body); got != "ca11e400 \"\" true\n" {
				t.Fatalf("pressed.txt = %q, want the caller's row with the board's context live", got)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the extension's key never ran; board output:\n%s", ansi.Strip(out.String()))
		}
		keys.Write([]byte("\x1b"))
		time.Sleep(150 * time.Millisecond)
		keys.Write([]byte("Z"))
		select {
		case <-exited:
			t.Fatalf("the board exited:\n%s", ansi.Strip(out.String()))
		case <-time.After(350 * time.Millisecond):
		}
	}
}

func skipWelcome(t *testing.T, path string) {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetSetting("welcome_seen", "1"); err != nil {
		t.Fatal(err)
	}
}

// waitForOutput waits for the board to draw want, calling nudge between
// looks.
func waitForOutput(t *testing.T, out *syncBuffer, want string, exited <-chan struct{}, nudge func()) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(ansi.Strip(out.String()), want) {
			return
		}
		nudge()
		select {
		case <-exited:
			t.Fatalf("the board exited before drawing %q:\n%s", want, ansi.Strip(out.String()))
		case <-time.After(500 * time.Millisecond):
		}
	}
	t.Fatalf("the board never drew %q:\n%s", want, ansi.Strip(out.String()))
}

// syncBuffer is output the board writes from one goroutine while the test
// reads it from another.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
