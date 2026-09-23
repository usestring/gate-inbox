package mcpserver

import (
	"errors"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/sessioncmd"
	"github.com/usestring/gate-inbox/internal/tracetest"
)

// This server runs in its own process, one per agent session, so nothing the
// board records covers it. The session id is what separates one server's spans
// from the dozens beside it in a shared dataset, and without it the whole
// fleet's tool calls arrive as one undifferentiated stream.
func TestEveryToolCallIsRecordedAgainstItsSession(t *testing.T) {
	fake := &fakeSessionCommands{listed: []sessioncmd.Session{{ID: "s1", Name: "one", Tool: "claude"}}}
	session := connectServer(t, serverWithFakes(t, fake))

	spans := tracetest.Capture(t)
	callTool(t, session, "list_sessions", map[string]any{})
	recorded := spans()

	span := tracetest.One(t, recorded, "mcp.tool")
	if span.Attr("tool") != "list_sessions" {
		t.Errorf("tool = %v, want list_sessions", span.Attr("tool"))
	}
	if span.Attr("session") != "abc123" {
		t.Errorf("session = %v, want the session this server was started as", span.Attr("session"))
	}
	if span.Attr("failed") != false {
		t.Errorf("failed = %v, want false", span.Attr("failed"))
	}
}

// A refused tool call comes back as a result carrying IsError, not as a
// transport error. A span reading only the error would report this server as
// having never failed at anything, which is the opposite of what an agent
// complaining about its tools needs to see.
func TestARefusedToolCallIsRecordedAsFailed(t *testing.T) {
	fake := &fakeSessionCommands{err: errors.New("session does not exist")}
	session := connectServer(t, serverWithFakes(t, fake))

	spans := tracetest.Capture(t)
	result := callTool(t, session, "read_session", map[string]any{"session_id": "nope"})
	if !result.IsError {
		t.Fatal("the fake refuses every call, so the result must be an error")
	}
	recorded := spans()

	span := tracetest.One(t, recorded, "mcp.tool")
	if span.Attr("failed") != true {
		t.Errorf("failed = %v, want true", span.Attr("failed"))
	}
}

// The arguments are the prompts and messages agents send each other, and this
// process ships its spans off the machine. The tool's name is the whole of
// what a span may say about a call.
func TestToolArgumentsNeverReachASpan(t *testing.T) {
	fake := &fakeSessionCommands{created: sessioncmd.Session{ID: "new1", Name: "spawned"}}
	session := connectServer(t, serverWithFakes(t, fake))

	prompt := "a task nobody outside this machine should read"
	spans := tracetest.Capture(t)
	callTool(t, session, "create_session", map[string]any{"name": "spawned", "prompt": prompt, "tool": "claude"})
	recorded := spans()

	if len(recorded) == 0 {
		t.Fatal("the call recorded nothing")
	}
	for _, span := range recorded {
		for key, value := range span.Attrs {
			if text, ok := value.(string); ok && strings.Contains(text, prompt) {
				t.Fatalf("span %s attribute %q carries the prompt: %q", span.Name, key, text)
			}
		}
	}
}

// Everything else the server handles -- the handshake, the tool listing -- is
// not a tool call and must not arrive looking like one, or the count of what
// an agent asked for is wrong from the first message.
func TestOnlyToolCallsAreRecorded(t *testing.T) {
	session := connectServer(t, serverWithFakes(t, &fakeSessionCommands{}))

	spans := tracetest.Capture(t)
	if _, err := session.ListTools(t.Context(), nil); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	callTool(t, session, "list_sessions", map[string]any{})
	recorded := spans()

	// One span for the one call, from a session that also asked for the tool
	// listing: the listing is a protocol message, not something an agent
	// waited on a session command for.
	span := tracetest.One(t, recorded, "mcp.tool")
	if span.Attr("tool") != "list_sessions" {
		t.Errorf("the recorded call is %v, want the only tool call made", span.Attr("tool"))
	}
}
