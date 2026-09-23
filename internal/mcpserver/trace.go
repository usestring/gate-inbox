package mcpserver

import (
	"context"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/tracing"
)

// This server is the least observed part of the system and the most numerous:
// one process per agent session, dozens of them on a busy machine, each one
// doing the same work the board does and none of it on the board's clock. An
// agent that says a tool call took a while has had no evidence behind it, and
// the board's own trace cannot supply any -- these calls happen in another
// process entirely.
//
// So the spans are taken here, and they carry the session id, which is what
// separates one server's spans from the forty-seven beside it in a shared
// dataset.

// traceTools times every tool call the server serves.
//
// One middleware rather than a span inside each of the thirty handlers: the
// handlers are a thin layer over sessioncmd, which is traced on its own, and
// what this adds is the boundary -- what the agent asked for, and how long it
// waited for an answer.
//
// The tool's name goes on the span; its arguments never do. They are the
// prompts, messages and commands agents send each other, and this leaves the
// machine.
func traceTools(sessionID string) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if !tracing.Enabled() {
				return next(ctx, method, req)
			}
			params, isCall := req.GetParams().(*mcp.CallToolParamsRaw)
			if !isCall {
				return next(ctx, method, req)
			}
			started := time.Now()
			result, err := next(ctx, method, req)
			// A refused tool call comes back as a result carrying IsError
			// rather than as an error: textResult turns every failure into
			// one, so a span reading only err would report this server as
			// having never failed at anything.
			called, _ := result.(*mcp.CallToolResult)
			tracing.Record("mcp.tool", started, time.Now(), err,
				tracing.Attr{Key: "tool", Value: params.Name},
				tracing.Attr{Key: "session", Value: sessionID},
				tracing.Attr{Key: "failed", Value: err != nil || (called != nil && called.IsError)})
			return result, err
		}
	}
}

// startTracing opens the exporter for this process, which the board does for
// itself in main and nothing was doing here.
//
// Separate from the board's because these are separate processes: a server
// started by an agent's CLI inherits whatever environment that session was
// launched with, so an operator who wants a fleet of them recording sets the
// variable where the sessions are launched rather than where the board is. An
// unset variable starts nothing, exactly as on the board.
//
// A destination that cannot be opened is logged and not returned: the board
// can refuse to start over a bad trace target because a person is watching it
// do so, and this process has nobody to tell. Refusing here would take an
// agent's whole tool surface away to report a telemetry misconfiguration.
func startTracing() *tracing.Tracer {
	tracer, err := tracing.Start(os.Getenv(tracing.Env))
	if err != nil {
		logging.Warn("mcp server not tracing", logging.Err(err))
		return nil
	}
	if where := tracer.Where(); where != "" {
		logging.Info("tracing mcp tool calls", "to", where)
	}
	return tracer
}
