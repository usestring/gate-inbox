package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/mcpshared"
	"github.com/usestring/gate-inbox/internal/parentseal"
)

// Every tool acts as one session, its caller, and every call establishes
// which. A server started for one session knows from its own environment. A
// server many sessions share -- opencode's serve daemon, or one HTTP server
// for a whole board -- cannot: its caller comes with each request. A claude
// sends headers its launch wrote into its MCP config; opencode names its own
// conversation in _meta.sessionID, which its launch bound to the session
// (parentseal.BindAgentSession). Either way the session is proven with its
// caller token, which only its own launch holds, in files under the key dir.
const (
	// CallerHeader and CallerTokenHeader name a request's caller to a
	// shared HTTP server.
	CallerHeader      = mcpshared.CallerHeader
	CallerTokenHeader = mcpshared.CallerTokenHeader
	// CallerArg and CallerTokenArg name a call's caller in the call itself,
	// for a client that can add arguments to a call but not headers. No
	// client does so today: opencode strips any argument a tool does not
	// advertise. Every tool takes them, and none advertises them, since the
	// model is never the one to fill them in.
	CallerArg      = "caller_session_id"
	CallerTokenArg = "caller_session_token"
)

// callers resolves the caller of each tool call and hands it on in the
// call's context (extension.ContextWithCaller), where every tool reads it.
type callers struct {
	configDir string
	// own is the session this process was started for, from its
	// environment: what a call acts as when nothing else names a caller.
	// Empty for a server shared by many sessions.
	own string
}

func (c callers) middleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		params, isCall := req.GetParams().(*mcp.CallToolParamsRaw)
		if !isCall {
			return next(ctx, method, req)
		}
		var claimed, token string
		// The arguments go before the tool sees them, so its input schema
		// never has to allow them.
		params.Arguments, claimed, token = takeCallerArgs(params.Arguments)
		var header http.Header
		if extra := req.GetExtra(); extra != nil {
			header = extra.Header
		}
		agentSession, _ := params.Meta["sessionID"].(string)
		id, err := c.resolve(header, agentSession, claimed, token)
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil
		}
		return next(extension.ContextWithCaller(ctx, id), method, req)
	}
}

// resolve is the session a call acts as. The connection's own caller is
// the one a shared server's request headers name and prove; else, for a
// server with no session of its own, the session the launch bound the
// calling agent's own conversation (agentSession, the call's
// _meta.sessionID) to; else this process's session. An argument naming
// another session wins only with that session's token: without it, one
// session could act as any other, answer another's children or kill it.
func (c callers) resolve(header http.Header, agentSession, claimed, token string) (string, error) {
	connection := c.own
	if id := strings.TrimSpace(header.Get(CallerHeader)); id != "" {
		if !parentseal.VerifyCaller(c.configDir, id, header.Get(CallerTokenHeader)) {
			return "", fmt.Errorf("the %s header names session %s without its token in %s, which the session's launch writes into its MCP config",
				CallerHeader, id, CallerTokenHeader)
		}
		connection = id
	} else if c.own == "" && agentSession != "" {
		// A server of a session's own never reads it: its caller is fixed
		// by who started it, and a binding must not move it.
		connection = parentseal.AgentSessionCaller(c.configDir, agentSession)
	}
	claimed = strings.TrimSpace(claimed)
	if claimed == "" || claimed == connection {
		return connection, nil
	}
	if !parentseal.VerifyCaller(c.configDir, claimed, strings.TrimSpace(token)) {
		return "", fmt.Errorf("%s %s needs that session's own %s", CallerArg, claimed, CallerTokenArg)
	}
	if connection != "" {
		logging.Info("mcp caller override", "connection", connection, "caller", claimed)
	}
	return claimed, nil
}

// takeCallerArgs is raw without the caller arguments, and their values.
// Arguments that are not a JSON object are left for the tool to refuse.
func takeCallerArgs(raw json.RawMessage) (json.RawMessage, string, string) {
	var args map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &args) != nil {
		return raw, "", ""
	}
	idRaw, hasID := args[CallerArg]
	tokenRaw, hasToken := args[CallerTokenArg]
	if !hasID && !hasToken {
		return raw, "", ""
	}
	var id, token string
	_ = json.Unmarshal(idRaw, &id)
	_ = json.Unmarshal(tokenRaw, &token)
	delete(args, CallerArg)
	delete(args, CallerTokenArg)
	rest, err := json.Marshal(args)
	if err != nil {
		return raw, id, token
	}
	return rest, id, token
}

// callerOf is the session a tool call acts as, as the callers middleware
// resolved it.
func callerOf(ctx context.Context) string {
	id, _ := extension.CallerFrom(ctx)
	return id
}
