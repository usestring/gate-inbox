package app

import (
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// An extension compiled outside this module adds a toggle to the board's
// new-session form, and a session launched from the form with it on reaches
// the extension's launch contributor with the toggle's value, and its form
// spawn observer once with the new session.
func TestExternalBuildAddsANewSessionFormField(t *testing.T) {
	script, err := exec.LookPath("script")
	if err != nil {
		t.Skip("script(1) is needed to give the board a terminal")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("the board launches a tmux pane")
	}
	bin := buildFixture(t)
	socket := tmuxtest.Socket(t, "formfield")
	env := fixtureHome(t, "tmux_socket = \""+socket+"\"\n"+envEchoTool)
	home := envValue(env, "GATE_INBOX_HOME")
	t.Cleanup(func() { killTestServer(t, envValue(env, "TMUX_TMPDIR"), socket) })
	seedSessions(t, filepath.Join(home, "state.db"))
	skipWelcome(t, filepath.Join(home, "state.db"))
	// The reopen card would come up over the list on the first pass and
	// take the keys; "never" settles the seeded dead rows with a notice.
	setSetting(t, filepath.Join(home, "state.db"), "reopen_sessions", "never")
	data := filepath.Join(home, "extensions", "noop")

	// script(1)'s terminal has no size until one is set, and a board with
	// no columns draws nothing to wait on.
	sized := "stty cols 256 rows 40 && exec " + bin
	board := exec.Command(script, "-qec", sized, "/dev/null")
	if runtime.GOOS != "linux" {
		board = exec.Command(script, "-q", "/dev/null", "sh", "-c", sized)
	}
	board.Env = env
	board.Dir = t.TempDir()
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
	// The extension starts before the board reads its terminal, so the
	// list's first polled row is what says a key will land.
	waitForOutput(t, out, "noop:dead", exited, func() {})

	// ctrl+n opens the form on the CLI picker; typing picks envecho, up
	// wraps onto the extension's field after the form's own, space turns
	// it on, and enter launches. Each key waits for the frame the one
	// before it drew. The board redraws only the cells that changed, so
	// space's frame is the "n" of "on".
	for _, step := range []struct{ key, drawn string }{
		{"\x0e", "items ◂ off ▸"},
		{"envecho", "envecho ▸"},
		{"\x1b[A", "❯ items"},
		{" ", "n ▸"},
	} {
		typeAndWait(t, keys, out, step.key, step.drawn, exited)
	}
	if _, err := keys.Write([]byte("\r")); err != nil {
		t.Fatalf("type enter: %v", err)
	}
	got := waitForFile(t, filepath.Join(data, "form.txt"), "envecho-", exited, out)
	if strings.TrimSpace(got) == "" || !strings.HasSuffix(strings.TrimSpace(got), " items=on") || strings.Count(got, "\n") != 1 {
		t.Fatalf("form.txt = %q, want one launch from the form with items=on", got)
	}
	name, _, _ := strings.Cut(strings.TrimSpace(got), " ")
	id := sessionNamed(t, filepath.Join(home, "state.db"), name)
	if spawned := waitForFile(t, filepath.Join(data, "formspawned.txt"), id, exited, out); spawned != id+" items=on\n" {
		t.Fatalf("formspawned.txt = %q, want %q once", spawned, id+" items=on")
	}
}

// typeAndWait types key and waits for the board to draw drawn after it.
func typeAndWait(t *testing.T, keys io.Writer, out *syncBuffer, key, drawn string, exited <-chan struct{}) {
	t.Helper()
	from := len(out.String())
	if _, err := keys.Write([]byte(key)); err != nil {
		t.Fatalf("type %q: %v", key, err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(ansi.Strip(out.String()[from:]), drawn) {
		select {
		case <-exited:
			t.Fatalf("the board exited before drawing %q after %q:\n%s", drawn, key, ansi.Strip(out.String()))
		case <-time.After(50 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatalf("the board never drew %q after %q:\n%s", drawn, key, ansi.Strip(out.String()))
		}
	}
}

// setSetting stores one board setting before the board starts.
func setSetting(t *testing.T, path, key, value string) {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetSetting(key, value); err != nil {
		t.Fatal(err)
	}
}

// sessionNamed is the id of the one session called name.
func sessionNamed(t *testing.T, path, name string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if id := findSessionNamed(t, path, name); id != "" || time.Now().After(deadline) {
			if id == "" {
				t.Fatalf("no session named %q", name)
			}
			return id
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// findSessionNamed is the id of the stored session called name, or "".
func findSessionNamed(t *testing.T, path, name string) string {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sessions, err := st.ListSessions(true)
	if err != nil {
		t.Fatal(err)
	}
	for _, sess := range sessions {
		if sess.Name == name {
			return sess.ID
		}
	}
	return ""
}
