package app

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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
	socket := tmuxtest.NewSocket("formfield")
	env := fixtureHome(t, "tmux_socket = \""+socket+"\"\n"+envEchoTool)
	home := envValue(env, "GATE_INBOX_HOME")
	t.Cleanup(func() { killTestServer(t, envValue(env, "TMUX_TMPDIR"), socket) })
	seedSessions(t, filepath.Join(home, "state.db"))
	data := filepath.Join(home, "extensions", "noop")

	board := exec.Command(script, "-qec", bin, "/dev/null")
	if runtime.GOOS != "linux" {
		board = exec.Command(script, "-q", "/dev/null", bin)
	}
	board.Env = env
	board.Dir = t.TempDir()
	keys, err := board.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	board.Stdout, board.Stderr = &out, &out
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
	waitForFile(t, filepath.Join(data, "started.txt"), "", exited, &out)

	// ctrl+n opens the form on the CLI picker; typing picks envecho, up
	// wraps onto the extension's field after the form's own, space turns
	// it on, and enter launches.
	for _, k := range []string{"\x0e", "envecho", "\x1b[A", " ", "\r"} {
		time.Sleep(300 * time.Millisecond)
		if _, err := keys.Write([]byte(k)); err != nil {
			t.Fatalf("type %q: %v", k, err)
		}
	}
	got := waitForFile(t, filepath.Join(data, "form.txt"), "envecho-", exited, &out)
	if strings.TrimSpace(got) == "" || !strings.HasSuffix(strings.TrimSpace(got), " items=on") || strings.Count(got, "\n") != 1 {
		t.Fatalf("form.txt = %q, want one launch from the form with items=on", got)
	}
	name, _, _ := strings.Cut(strings.TrimSpace(got), " ")
	id := sessionNamed(t, filepath.Join(home, "state.db"), name)
	if spawned := waitForFile(t, filepath.Join(data, "formspawned.txt"), id, exited, &out); spawned != id+" items=on\n" {
		t.Fatalf("formspawned.txt = %q, want %q once", spawned, id+" items=on")
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
