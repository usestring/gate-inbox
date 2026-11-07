package extension

import "context"

type callerKey struct{}

// ContextWithCaller is ctx carrying id as the session a request acts as.
//
// The MCP server sets it on every tool call, once it has established who is
// calling. A server shared by many sessions serves them all from one
// process, so the caller travels with each call rather than living in the
// SessionContext its tools were registered with, which there names the
// server's default. A Host's session services act as this caller when ctx
// carries one.
func ContextWithCaller(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, callerKey{}, id)
}

// CallerFrom is the session ctx says a request acts as, and whether it says
// one. A tool that needs its caller's id reads it here first, and falls
// back to SessionContext.SessionID only when ctx carries none.
func CallerFrom(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(callerKey{}).(string)
	return id, ok
}
