package sessioncmd

import (
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/tracetest"
)

// A spawn is the most expensive thing an agent can ask this package for, and
// until now it was invisible: the log line says it happened, nothing says what
// it cost. The tool is on the span because a slow spawn is nearly always slow
// for one CLI rather than for all of them.
func TestASpawnIsRecordedWithItsToolAndItsResult(t *testing.T) {
	h := newSessionHarness(t)

	prompt := "a task nobody outside this machine should read"
	spans := tracetest.Capture(t)
	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Tool: "echoer", Name: "spawned", Prompt: prompt})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	recorded := spans()

	span := tracetest.One(t, recorded, "sessioncmd.create")
	if span.Attr("session") != h.caller.ID {
		t.Errorf("session = %v, want the calling session %s", span.Attr("session"), h.caller.ID)
	}
	if span.Attr("tool") != "echoer" {
		t.Errorf("tool = %v, want echoer", span.Attr("tool"))
	}
	if span.Attr("created") != created.ID {
		t.Errorf("created = %v, want the new session %s", span.Attr("created"), created.ID)
	}
	if span.Failed {
		t.Error("the spawn succeeded, so its span must not carry a failure")
	}
	// Every command here opens the config, a tmux connection and the store
	// before it does anything, and that fixed cost has to be separable from
	// the work itself.
	opens := tracetest.Named(recorded, "sessioncmd.open")
	if len(opens) == 0 {
		t.Fatal("no sessioncmd.open span: the fixed cost under every command is not attributed")
	}
	// The command's span has to cover the work, not the moment it was
	// reported. A span assembled at the return rather than at the entry is
	// well-formed, carries every attribute asked of it, and measures nothing
	// -- and the open it contains is what proves which of the two this is.
	if !span.Brackets(opens[0]) {
		t.Errorf("the create span (%s to %s) does not cover the open it contains (%s to %s)",
			span.Start, span.End, opens[0].Start, opens[0].End)
	}
	assertNothingCarries(t, recorded, prompt)
}

// A refused spawn has to read as refused. Create returns before it opens a
// pane for half a dozen reasons, and a span that reported those as ordinary
// spawns would put the cheap failures in with the expensive successes.
func TestARefusedSpawnCarriesItsFailure(t *testing.T) {
	h := newSessionHarness(t)

	spans := tracetest.Capture(t)
	if _, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Tool: "nonexistent-cli"}); err == nil {
		t.Fatal("an unconfigured tool must be refused")
	}
	recorded := spans()

	span := tracetest.One(t, recorded, "sessioncmd.create")
	if !span.Failed {
		t.Error("the spawn was refused, so its span must carry the failure")
	}
	if span.Attr("created") != "" {
		t.Errorf("created = %v, want empty: nothing was created", span.Attr("created"))
	}
}

// The message is queued, not typed, so what this command costs is the store
// and the checks around it -- but the message is one agent's words to another
// and must not leave the machine. Its length may; its text may not.
func TestASendReportsTheSizeAndNotTheMessage(t *testing.T) {
	h := newSessionHarness(t)
	target, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Tool: "resting", Name: "target"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	message := "please look at the failing deploy on staging"
	spans := tracetest.Capture(t)
	if _, err := h.sessions.Send(h.caller.ID, target.ID, message, "", false); err != nil {
		t.Fatalf("Send: %v", err)
	}
	recorded := spans()

	span := tracetest.One(t, recorded, "sessioncmd.send")
	if span.Attr("session") != target.ID {
		t.Errorf("session = %v, want the target %s", span.Attr("session"), target.ID)
	}
	if span.Attr("message.bytes") != int64(len(message)) {
		t.Errorf("message.bytes = %v, want %d", span.Attr("message.bytes"), len(message))
	}
	if span.Attr("as_human") != false {
		t.Errorf("as_human = %v, want false", span.Attr("as_human"))
	}
	assertNothingCarries(t, recorded, message)
}

// Killing a session is what an operator presses a key for and then waits on.
func TestAKillIsRecordedAgainstTheSessionItEnded(t *testing.T) {
	h := newSessionHarness(t)
	target, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Tool: "resting", Name: "doomed"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	spans := tracetest.Capture(t)
	if _, err := h.sessions.Kill(h.caller.ID, target.ID); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	recorded := spans()

	span := tracetest.One(t, recorded, "sessioncmd.kill")
	if span.Attr("session") != target.ID {
		t.Errorf("session = %v, want the killed session %s", span.Attr("session"), target.ID)
	}
	if span.Failed {
		t.Error("the kill succeeded and must not carry a failure")
	}
}

// A terminal send carries which of the two kinds went in, because that is the
// difference between pasting text and pressing a key, and never the command
// itself: it is typed by an agent and runs on this machine.
func TestATerminalSendRecordsTheKindAndNotTheCommand(t *testing.T) {
	h := newSessionHarness(t)
	terminal, err := h.terminals.Create(h.caller.ID, CreateTerminalOptions{})
	if err != nil {
		t.Fatalf("Create terminal: %v", err)
	}

	command := "cat /home/user/.ssh/id_ed25519"
	spans := tracetest.Capture(t)
	if _, err := h.terminals.Send(h.caller.ID, terminal.ID, command, nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	recorded := spans()

	span := tracetest.One(t, recorded, "sessioncmd.terminals.send")
	if span.Attr("terminal") != terminal.ID {
		t.Errorf("terminal = %v, want %s", span.Attr("terminal"), terminal.ID)
	}
	if span.Attr("sent") != "command" {
		t.Errorf("sent = %v, want command", span.Attr("sent"))
	}
	assertNothingCarries(t, recorded, command)
}

// assertNothingCarries fails when any span holds the text. A span that named
// what a session was told would put prompts and commands in a dataset off this
// machine, which is the one mistake this instrumentation cannot make.
func assertNothingCarries(t *testing.T, spans []tracetest.Span, text string) {
	t.Helper()
	for _, span := range spans {
		for key, value := range span.Attrs {
			if carried, ok := value.(string); ok && strings.Contains(carried, text) {
				t.Fatalf("span %s attribute %q carries %q", span.Name, key, carried)
			}
		}
	}
}
