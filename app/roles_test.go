package app

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// restingTool stays up, and says when it can read a message, so a send to
// it is deliverable.
const restingTool = `
[tools.resting]
command = "sh -c 'sleep 30' --"
default_status = "idle"
activity_cutoff = "(?m)^x"
`

// An extension compiled outside this module declares how its role's
// sessions are treated, and the CLI honours it: the helper is left out of
// its parent's send-children, and its send to its parent goes through the
// extension's Relayer and is queued as the operator's words.
func TestExternalBuildsRolesChangeHowTheirSessionsAreTreated(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("a spawn launches a tmux pane")
	}
	bin := buildFixture(t)
	socket := tmuxtest.NewSocket("roles")
	env := fixtureHome(t, "tmux_socket = \""+socket+"\"\n"+restingTool)
	home := envValue(env, "GATE_INBOX_HOME")
	t.Cleanup(func() { killTestServer(t, envValue(env, "TMUX_TMPDIR"), socket) })
	seedSessions(t, filepath.Join(home, "state.db"))
	data := filepath.Join(home, "extensions", "noop")

	run := func(as string, args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		cmd.Env = append(env, "GATE_INBOX_SESSION_ID="+as)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	out, err := run("ca11e400", "spawn", "--tool", "resting", "--name", "worker", "--directory", t.TempDir(), "--json")
	if err != nil {
		t.Fatalf("spawn: %v\n%s", err, out)
	}
	worker := idOf(t, out)
	const helper = "4e1be401"
	seedHelper(t, filepath.Join(home, "state.db"), store.Session{
		ID: helper, Name: "helper", Tool: "resting", Status: "working",
		ParentID: worker, Role: "noop/helper", CreatedAt: time.Now(),
	})

	out, err = run(worker, "send-children", "the branch moved")
	if err != nil {
		t.Fatalf("send-children: %v\n%s", err, out)
	}
	if !strings.Contains(out, "queued for 0 of 1 children") || !strings.Contains(out, helper) || !strings.Contains(out, "not part of the fan-out") {
		t.Fatalf("send-children did not skip the helper:\n%s", out)
	}

	out, err = run(helper, "send", worker, "yes")
	if err != nil {
		t.Fatalf("relay: %v\n%s", err, out)
	}
	msg := headMessage(t, filepath.Join(home, "state.db"), worker)
	if msg.SenderID != store.RelayedHumanSenderID || msg.Body != "the operator says: yes" {
		t.Fatalf("queued %+v, want the Relayer's words as the operator's, relayed", msg)
	}

	out, err = run(helper, "send", worker, "for noop")
	if err != nil || !strings.Contains(out, "nothing was queued") {
		t.Fatalf("a relay noop kept: %v\n%s", err, out)
	}
	if got := readEventually(t, filepath.Join(data, "relayed.txt")); got != helper+">"+worker+" yes\n"+helper+">"+worker+" for noop\n" {
		t.Fatalf("relayed.txt = %q", got)
	}
	if again := headMessage(t, filepath.Join(home, "state.db"), worker); again.ID != msg.ID {
		t.Fatalf("a relay the extension kept queued %+v", again)
	}
}

// seedHelper files sess as a leaf, the way BoardLaunch files a helper, so
// its parent may itself be somebody's child.
func seedHelper(t *testing.T, path string, sess store.Session) {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.CreateSessionLeaf(sess); err != nil {
		t.Fatal(err)
	}
}

func headMessage(t *testing.T, path, id string) store.InboxMessage {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	msg, ok, err := st.HeadMessage(id)
	if err != nil || !ok {
		t.Fatalf("nothing queued for %s: %v", id, err)
	}
	return msg
}
