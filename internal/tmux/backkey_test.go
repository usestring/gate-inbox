package tmux

import (
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

func shellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}

func paneRow(t *testing.T, socket, pane string) string {
	t.Helper()
	out, err := tmuxOn(socket, "display-message", "-p", "-t", pane, "#{"+RowOption+"}").CombinedOutput()
	if err != nil {
		t.Fatalf("display-message: %v: %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func attachedClients(t *testing.T, socket string) int {
	t.Helper()
	out, err := tmuxOn(socket, "list-clients", "-F", "#{client_name}").CombinedOutput()
	if err != nil {
		return 0
	}
	return len(strings.Fields(string(out)))
}

func waitForAttached(t *testing.T, socket string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for attachedClients(t, socket) != want {
		if time.Now().After(deadline) {
			t.Fatalf("%d clients on %s, want %d", attachedClients(t, socket), socket, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// terminalRunning starts a stand-in terminal: a pane on its own server
// running command, so the command's tmux client has a tty and keys typed
// into the stand-in reach it as the operator's keystrokes.
func terminalRunning(t *testing.T, name, command string) string {
	t.Helper()
	terminal := tmuxtest.Socket(t, name)
	if out, err := tmuxOn(terminal, "new-session", "-d", "-s", "terminal", "-x", "80", "-y", "24", command).CombinedOutput(); err != nil {
		t.Fatalf("stand-in terminal: %v: %s", err, out)
	}
	return terminal
}

// An adopted pane is labelled with its row and given the back key, which
// detaches the client the board attached and nobody else's: the operator
// attached to the same session from their own terminal keeps it.
func TestTheBackKeyLeavesAnAdoptedPaneOnlyForTheBoardsClient(t *testing.T) {
	driver := requireTmux(t)
	socket, panes := foreignServer(t)
	id := uniqueID("back")
	adopt(t, driver, id, socket, panes[1])
	if err := driver.MarkAdopted(id); err != nil {
		t.Fatalf("MarkAdopted: %v", err)
	}
	if got := paneRow(t, socket, panes[1]); got != id {
		t.Fatalf("adopted pane labelled %q, want %q", got, id)
	}
	if got := paneRow(t, socket, panes[0]); got != "" {
		t.Fatalf("its neighbour was labelled %q", got)
	}
	if key := driver.backKeyOn(socket); key != "Ctrl+q" {
		t.Fatalf("back key on the adopted pane's server = %q, want Ctrl+q", key)
	}

	// The board's attach: the back key brings it back.
	boardTerminal := terminalRunning(t, "backboard", "env -u TMUX "+shellJoin(driver.AttachCommand(id).Args))
	waitForAttached(t, socket, 1)
	out, err := tmuxOn(socket, "show-options", "-v", "-t", "=user:", clientOption).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		t.Fatalf("the board's client was not recorded: %v %q", err, out)
	}
	board, _ := tmuxOn(socket, "list-clients", "-F", "#{client_name}").Output()
	if strings.TrimSpace(string(out)) != strings.TrimSpace(string(board)) {
		t.Fatalf("recorded client %q, attached %q", out, board)
	}
	// The terminal's own server ran the attach; C-q typed there reaches the
	// attached client as a keystroke.
	if out, err := tmuxOn(boardTerminal, "send-keys", "-t", "terminal", "C-q").CombinedOutput(); err != nil {
		t.Fatalf("send-keys: %v: %s", err, out)
	}
	waitForAttached(t, socket, 0)
	driver.AttachDone(id)
	if out, _ := tmuxOn(socket, "show-options", "-qv", "-t", "=user:", clientOption).CombinedOutput(); strings.TrimSpace(string(out)) != "" {
		t.Fatalf("the board's client record outlived its attach: %q", out)
	}

	// The operator's own client, on the same pane: the key passes through.
	own := terminalRunning(t, "backown", "env -u TMUX tmux -L "+socket+" attach-session -t =user")
	waitForAttached(t, socket, 1)
	if out, err := tmuxOn(socket, "select-pane", "-t", panes[1]).CombinedOutput(); err != nil {
		t.Fatalf("select-pane: %v: %s", err, out)
	}
	if out, err := tmuxOn(own, "send-keys", "-t", "terminal", "C-q").CombinedOutput(); err != nil {
		t.Fatalf("send-keys: %v: %s", err, out)
	}
	time.Sleep(300 * time.Millisecond)
	if n := attachedClients(t, socket); n != 1 {
		t.Fatalf("the operator's own client was detached by the back key (%d clients left)", n)
	}

	driver.Release(id)
	if got := paneRow(t, socket, panes[1]); got != "" {
		t.Fatalf("a released pane is still labelled %q", got)
	}
}

// A back-key binding an earlier build left on a shared server is
// recognised as the board's own and brought up to date; one the operator
// set stays theirs.
func TestSharedBindingsReplaceOnlyTheBoardsOwnOldBackKey(t *testing.T) {
	old := `bind-key -T root C-q if-shell -F "#{m:gi_*,#{session_name}}" detach-client "send-keys C-q"`
	if !ownBackBinding(old) {
		t.Fatal("an earlier build's back key was not recognised")
	}
	if ownBackBinding("bind-key -T root C-q detach-client") {
		t.Fatal("the operator's own C-q was taken for the board's")
	}
	for _, bind := range backBindings() {
		if line := strings.Join(bind, " "); !ownBackBinding(line) || !strings.Contains(line, RowOption) {
			t.Fatalf("this build's back key is not recognised as its own: %q", line)
		}
	}
}
