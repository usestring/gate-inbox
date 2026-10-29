package mcpserver

import (
	"context"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/usestring/gate-inbox/internal/sessioncmd"
)

// A user who presses Esc while create_session runs cancels the call, and
// Claude Code tells the model the call was rejected. The launch was already
// under way, so without a withdraw the session it made goes on working on a
// task the user turned down.
func TestCancelledCreateSessionWithdrawsTheSpawn(t *testing.T) {
	fake := &fakeSessionCommands{
		created:       sessioncmd.Session{ID: "c0ffee01", Name: "turned-down", Tool: "claude"},
		createStarted: make(chan struct{}),
		createRelease: make(chan struct{}),
		withdrawn:     make(chan string, 1),
	}
	session := connectServer(t, newServer(t.TempDir(), "abc123", "test", &fakeTerminalCommands{}, fake))

	ctx, cancel := context.WithCancel(context.Background())
	called := make(chan error, 1)
	go func() {
		_, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      "create_session",
			Arguments: map[string]any{"name": "turned-down", "prompt": "investigate"},
		})
		called <- err
	}()
	<-fake.createStarted
	cancel()
	if err := <-called; err == nil {
		t.Fatal("a cancelled call reported success to the client")
	}
	// The cancellation notice reaches the server before the launch returns,
	// the way Esc lands mid-launch.
	time.Sleep(200 * time.Millisecond)
	close(fake.createRelease)

	select {
	case id := <-fake.withdrawn:
		if id != "c0ffee01" {
			t.Fatalf("withdrew %q, want the session the call created", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the session a cancelled create_session launched was left running")
	}
}

// A call nobody cancelled keeps its session.
func TestCreateSessionKeepsTheSpawnWhenNotCancelled(t *testing.T) {
	fake := &fakeSessionCommands{
		created:   sessioncmd.Session{ID: "a1b2c3d4", Name: "worker", Tool: "claude"},
		withdrawn: make(chan string, 1),
	}
	session := connectServer(t, newServer(t.TempDir(), "abc123", "test", &fakeTerminalCommands{}, fake))
	if text, isError := callText(t, session, "create_session", map[string]any{"name": "worker", "prompt": "go"}); isError {
		t.Fatalf("create_session = %q", text)
	}
	select {
	case id := <-fake.withdrawn:
		t.Fatalf("withdrew %q from a call that completed", id)
	default:
	}
}
