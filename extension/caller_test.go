package extension_test

import (
	"context"
	"testing"

	"github.com/usestring/gate-inbox/extension"
)

// A tool registered for one session acts as it; one registered into a
// shared server, with no session of its own, acts as each call's caller.
func TestSessionContextCallerPrefersTheCallsCaller(t *testing.T) {
	own := extension.SessionContext{SessionID: "own0001"}
	if got := own.Caller(context.Background()); got != "own0001" {
		t.Fatalf("got %q, want the registered session", got)
	}
	shared := extension.SessionContext{}
	if got := shared.Caller(extension.ContextWithCaller(context.Background(), "caller1")); got != "caller1" {
		t.Fatalf("got %q, want the call's caller", got)
	}
}
