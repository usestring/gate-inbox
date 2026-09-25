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
// a module importing app and extension only puts badges and a header on the
// rows it is told about, one of the badges as rungs that narrow with the
// row and one placed right after the session's name, a key of its own on
// the list answers with the row it was pressed on and raises a desktop
// alert, the view that key opens is drawn, told the keys of its own screen,
// and closed by the board on esc, a child view it
// opens is told it was dismissed and puts the parent back, a form's field
// is typed into on the board and handed back on submit, which the form is
// told closed it, and a filter of its own narrows the list under a badge in
// the header, and a session it says needs a person is kept by the
// attention filter whatever its status.
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
	alerts := filepath.Join(home, "alerts.txt")
	env = stubNotifiers(t, env, alerts)

	// script(1)'s terminal has no size until one is set, and a board with
	// no columns draws no rows to badge. At this width the row, beside the
	// preview, has room for every badge but not the widest rung.
	sized := "stty cols 256 rows 40 && exec " + bin
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
	// The header the extension set over a row is drawn with it.
	waitForOutput(t, out, "noop head ca11e400", exited, func() {})
	// A badge of rungs is drawn after the badge before it, as the widest
	// rung the row has room for beside the preview: its mark and numbers.
	waitForOutput(t, out, "noop:dead ◈ 2c · 3/h", exited, func() {})
	// A badge placed after the name is drawn right after it, ahead of the
	// fold's count and the badges in the slot.
	waitForOutput(t, out, "the caller items  ↳1 · ✕1 noop:dead ◈ 2c · 3/h ", exited, func() {})
	// The widths it measured its rungs by, through Line.Width, are the
	// widths the row fitted them to.
	waitForFile(t, filepath.Join(data, "rungs.txt"), "16 10 1\n", exited, &strings.Builder{})

	// The press is repeated until it lands: a prompt the board raises at
	// start can hold the first one, and esc puts any of them away.
	pressed := filepath.Join(data, "pressed.txt")
	deadline := time.Now().Add(20 * time.Second)
	for {
		if body, err := os.ReadFile(pressed); err == nil && len(body) > 0 {
			if got := string(body); got != "ca11e400 \"\" true\n" {
				t.Fatalf("pressed.txt = %q, want the caller's row with the board's context live", got)
			}
			break
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

	// The key's alert reached the system notifier as one line, the row it
	// named as its subject.
	if body := waitForFile(t, alerts, "marked by noop", exited, &strings.Builder{}); !strings.Contains(body, "ca11e400") {
		t.Fatalf("alerts.txt = %q, want the key's alert with its row", body)
	}

	waitForOutput(t, out, "row ca11e400", exited, func() {})
	if !strings.Contains(ansi.Strip(out.String()), "noop peek") {
		t.Fatalf("the view's title was not drawn:\n%s", ansi.Strip(out.String()))
	}
	keys.Write([]byte("n"))
	viewKeys := filepath.Join(data, "viewkeys.txt")
	waitForFile(t, viewKeys, "peek_note n\n", exited, &strings.Builder{})

	// The view opens a child over itself. esc dismisses the child, which is
	// told so and puts its parent back: the next n reaches the parent.
	keys.Write([]byte("d"))
	waitForOutput(t, out, "child of ca11e400", exited, func() {})
	keys.Write([]byte("\x1b"))
	closed := filepath.Join(data, "closed.txt")
	waitForFile(t, closed, "child dismissed\n", exited, &strings.Builder{})
	time.Sleep(300 * time.Millisecond)
	keys.Write([]byte("n"))
	pressedOnParent := "peek_note n\npeek_child d\npeek_note n\n"
	waitForFile(t, viewKeys, pressedOnParent, exited, &strings.Builder{})

	// esc is the board's: the view is closed and never told of the key, and
	// a later n lands on the list, where nothing is bound to it.
	keys.Write([]byte("\x1b"))
	time.Sleep(300 * time.Millisecond)
	keys.Write([]byte("n"))
	time.Sleep(time.Second)
	if body, _ := os.ReadFile(viewKeys); string(body) != pressedOnParent {
		t.Fatalf("viewkeys.txt = %q, want only the presses before esc closed the view", body)
	}

	// A form's fields are the board's to type into: the letters land there
	// rather than on the view, down and up move between the fields, and
	// enter hands the view the values. The n above opened the new-session
	// form; esc puts it away first.
	keys.Write([]byte("\x1b"))
	time.Sleep(300 * time.Millisecond)
	keys.Write([]byte("U"))
	waitForOutput(t, out, "write a note", exited, func() {})
	keys.Write([]byte(" board"))
	time.Sleep(300 * time.Millisecond)
	keys.Write([]byte("\x1b[B"))
	time.Sleep(300 * time.Millisecond)
	keys.Write([]byte("ok"))
	time.Sleep(300 * time.Millisecond)
	keys.Write([]byte("\x1b[A"))
	time.Sleep(300 * time.Millisecond)
	keys.Write([]byte("\r"))
	waitForFile(t, filepath.Join(data, "submitted.txt"), "note from board ok\n", exited, &strings.Builder{})
	// The submit closed the form, which was told why.
	waitForFile(t, closed, "child dismissed\ncompose submitted\n", exited, &strings.Builder{})

	// The extension's filter is a key on the list, and the header names it
	// while it is on.
	time.Sleep(300 * time.Millisecond)
	keys.Write([]byte("Y"))
	waitForOutput(t, out, "ROOTS", exited, func() {})

	// The child's pane is dead and the extension hid it from the tree, but
	// the extension says it needs a person: with the roots filter lifted,
	// the attention filter draws it, header and all.
	keys.Write([]byte("Y"))
	time.Sleep(300 * time.Millisecond)
	drawn := len(out.String())
	keys.Write([]byte("w"))
	deadline = time.Now().Add(20 * time.Second)
	for !strings.Contains(ansi.Strip(out.String()[drawn:]), "noop head c41d0001") {
		if time.Now().After(deadline) {
			t.Fatalf("the attention filter never drew the child the extension flagged:\n%s", ansi.Strip(out.String()[drawn:]))
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// stubNotifiers puts a notify-send and an osascript first on the board's
// PATH that write their arguments to record, one call a line, and takes out
// the markers that would send an alert to the terminal instead.
func stubNotifiers(t *testing.T, env []string, record string) []string {
	t.Helper()
	bin := t.TempDir()
	stub := "#!/bin/sh\necho \"$*\" >> '" + record + "'\n"
	for _, name := range []string{"notify-send", "osascript"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(stub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	out := make([]string, 0, len(env)+2)
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		switch key {
		case "TERM_PROGRAM", "CMUX_WORKSPACE_ID", "TERM":
			continue
		case "PATH":
			entry = "PATH=" + bin + string(os.PathListSeparator) + value
		}
		out = append(out, entry)
	}
	return append(out, "TERM=xterm-256color")
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

// TestExternalBuildConfirmsBeforeOpening is the proof for the open warning:
// a module importing app and extension only asks the board to confirm before
// opening one session. Enter on that row shows the warning and opens
// nothing, esc drops it, and only a second Enter goes in; the row it does not
// warn about opens on the first Enter.
func TestExternalBuildConfirmsBeforeOpening(t *testing.T) {
	script, err := exec.LookPath("script")
	if err != nil {
		t.Skip("script(1) is needed to give the board a terminal")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("the board focuses a tmux pane")
	}
	bin := buildFixture(t)
	socket := tmuxtest.NewSocket("open")
	env := fixtureHome(t, "tmux_socket = \""+socket+"\"\n\n[extensions.noop]\nconfirm = [\"0be0a001\"]\n")
	home := envValue(env, "GATE_INBOX_HOME")
	tmuxDir := envValue(env, "TMUX_TMPDIR")
	// The panes are made on the board's own server, under the same scratch
	// directory, and taken down there by path.
	t.Setenv("TMUX_TMPDIR", tmuxDir)
	t.Cleanup(func() { killTestServer(t, tmuxDir, socket) })
	db := filepath.Join(home, "state.db")
	paintSession(t, db, socket, "0be0a001", "the held pane\n")
	paintSession(t, db, socket, "0be0a002", "the free pane\n")
	skipWelcome(t, db)
	sortByName(t, db)

	sized := "stty cols 200 rows 40 && exec " + bin
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
	press := func(k string) {
		t.Helper()
		if _, err := keys.Write([]byte(k)); err != nil {
			t.Fatalf("press %q: %v", k, err)
		}
	}
	// since is what the board drew after mark; focused is its focus footer.
	mark := func() int { return len(ansi.Strip(out.String())) }
	since := func(at int) string { return ansi.Strip(out.String())[at:] }
	const focused = "back to manager"
	settle := func() {
		select {
		case <-exited:
			t.Fatalf("the board exited:\n%s", ansi.Strip(out.String()))
		case <-time.After(700 * time.Millisecond):
		}
	}

	waitForOutput(t, out, "painted 0be0a002", exited, func() {})
	// The rows sort by name under the group's own row, so one down from
	// home is the held row, and end is the free one.
	press("\x1b[H")
	settle()
	press("\x1b[B")
	settle()

	at := mark()
	press("\r")
	waitForOutput(t, out, "another party is driving 0be0a001", exited, func() {})
	settle()
	if strings.Contains(since(at), focused) {
		t.Fatalf("the first Enter on the held row opened it:\n%s", since(at))
	}

	at = mark()
	press("\x1b")
	settle()
	press("\r")
	settle()
	if strings.Contains(since(at), focused) {
		t.Fatalf("Enter after esc opened the held row:\n%s", since(at))
	}

	at = mark()
	press("\r")
	waitForOutput(t, out, focused, exited, func() {})
	if !strings.Contains(since(at), focused) {
		t.Fatalf("the second Enter on the held row did not open it:\n%s", since(at))
	}

	// Leave the pane, move to the free row, and open it on one press.
	press("\x11")
	settle()
	press("\x1b[F")
	settle()
	at = mark()
	press("\r")
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(since(at), focused) {
		if time.Now().After(deadline) {
			t.Fatalf("the free row did not open on the first Enter:\n%s", since(at))
		}
		settle()
	}
	if strings.Contains(since(at), "another party is driving") {
		t.Fatalf("the free row was held:\n%s", since(at))
	}
}

func sortByName(t *testing.T, path string) {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetSetting("list_sort", "name"); err != nil {
		t.Fatal(err)
	}
}

// TestExternalBuildCarriesOldNamesOverToAnExtension checks extension aliases
// end to end: a filter declared from outside the module starts on because
// the store still holds the toggle under the name it used to have, and
// answers the key the operator's key file gave its old action name.
func TestExternalBuildCarriesOldNamesOverToAnExtension(t *testing.T) {
	script, err := exec.LookPath("script")
	if err != nil {
		t.Skip("script(1) is needed to give the board a terminal")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("the board polls a tmux server")
	}
	bin := buildFixture(t)
	socket := tmuxtest.NewSocket("alias")
	env := fixtureHome(t, "tmux_socket = \""+socket+"\"\n")
	home := envValue(env, "GATE_INBOX_HOME")
	t.Cleanup(func() { killTestServer(t, envValue(env, "TMUX_TMPDIR"), socket) })
	statePath := filepath.Join(home, "state.db")
	seedSessions(t, statePath)
	skipWelcome(t, statePath)
	st, err := store.Open(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("items_only", "on"); err != nil {
		t.Fatal(err)
	}
	st.Close()
	if err := os.WriteFile(filepath.Join(home, "keys.toml"), []byte("[list]\nitems_filter = [\"L\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

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

	// The filter is on from the start, and the key that lifts it is the
	// one the key file wrote under the old action name.
	waitForOutput(t, out, "ITEMS", exited, func() { keys.Write([]byte("\x1b")) })
	waitForOutput(t, out, "L show all", exited, func() {})

	read := func(key string) string {
		t.Helper()
		st, err := store.Open(statePath)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		value, err := st.Setting(key)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	if got := read("extension_filter.items.items_view"); got != "on" {
		t.Fatalf("the filter's own setting = %q, want the carried on stored", got)
	}
	if got := read("items_only"); got != "on" {
		t.Fatalf("the old setting = %q, want it left as it was", got)
	}

	// L toggles the filter off, and the board stores that under the
	// filter's own name.
	deadline := time.Now().Add(20 * time.Second)
	for read("extension_filter.items.items_view") != "off" {
		if time.Now().After(deadline) {
			t.Fatalf("L never lifted the filter; board output:\n%s", ansi.Strip(out.String()))
		}
		keys.Write([]byte("\x1b"))
		time.Sleep(150 * time.Millisecond)
		keys.Write([]byte("L"))
		select {
		case <-exited:
			t.Fatalf("the board exited:\n%s", ansi.Strip(out.String()))
		case <-time.After(500 * time.Millisecond):
		}
	}
}
