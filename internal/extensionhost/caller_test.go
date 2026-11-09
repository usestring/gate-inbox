package extensionhost

import (
	"context"
	"testing"

	"github.com/usestring/gate-inbox/extension"
)

func TestSessionsActAsTheRequestsCaller(t *testing.T) {
	ctx := extension.ContextWithCaller(context.Background(), "caller1")
	own := &sessions{caller: "own0001"}
	if got := own.callerFor(context.Background()); got != "own0001" {
		t.Fatalf("with no caller on the request, acted as %q", got)
	}
	if got := own.callerFor(ctx); got != "caller1" {
		t.Fatalf("acted as %q, want the request's caller", got)
	}
	operator := &sessions{operator: true}
	if got := operator.callerFor(ctx); got != "" {
		t.Fatalf("the operator's Host acted as %q", got)
	}
}
