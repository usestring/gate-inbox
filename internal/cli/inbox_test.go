package cli

import (
	"context"
	"testing"

	"github.com/usestring/gate-inbox/internal/inbox"
)

// A router is the operator's own session, whatever shell started the server.
func TestInboxSpawnerLaunchesTopLevel(t *testing.T) {
	fake := &fakeSessions{session: sampleSession()}
	spawner, err := newInboxSpawner(inbox.Config{Group: "inbox", Directory: "/work", Tool: "codex"}, fake)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := spawner.Spawn(context.Background(), inbox.Spawn{Name: "inbox-iphone-hi", Prompt: "brief"}); err != nil {
		t.Fatal(err)
	}
	opts := fake.opts
	if fake.callerID != "" || opts.Nest == nil || *opts.Nest || opts.Group == nil || *opts.Group != "inbox" ||
		opts.Directory != "/work" || opts.Tool != "codex" || opts.Name != "inbox-iphone-hi" || opts.Prompt != "brief" {
		t.Fatalf("caller %q, options %+v", fake.callerID, opts)
	}
}
