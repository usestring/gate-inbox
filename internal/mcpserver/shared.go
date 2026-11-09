package mcpserver

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/mcpshared"
	"github.com/usestring/gate-inbox/internal/parentseal"
	"github.com/usestring/gate-inbox/internal/sessioncmd"
)

// upgradeCheck is how often the shared server looks for a replaced binary.
const upgradeCheck = 30 * time.Second

// ServeShared serves every session of configDir from this one process,
// over HTTP on loopback, until ctx ends (see mcpshared). It returns nil at
// once when another server already holds the config dir.
//
// It is stateless: every request carries its caller, so the server keeps
// nothing per session, and on an upgrade the new binary can take new
// requests while this process finishes the ones it holds.
func ServeShared(ctx context.Context, configDir, version string, extensions []extension.Extension) error {
	listener, lock, err := mcpshared.Listen(configDir)
	if errors.Is(err, mcpshared.ErrRunning) {
		return nil
	}
	if err != nil {
		return err
	}
	defer lock.Close()
	shared := &sharedServer{configDir: configDir, version: version, extensions: extensions}
	server := &http.Server{Handler: shared.handler(), ReadHeaderTimeout: 10 * time.Second}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logging.Info("shared MCP server listening", "addr", listener.Addr().String())
	handedOff := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			server.Close()
		case <-handedOff:
			// The upgraded process accepts on the same socket from here.
			// This one stops accepting, closes its idle connections, so
			// their clients reconnect to it, and finishes the calls it
			// holds: a wait_for_session runs up to MaxWaitTimeout.
			drain, cancel := context.WithTimeout(context.Background(), sessioncmd.MaxWaitTimeout+30*time.Second)
			defer cancel()
			if err := server.Shutdown(drain); err != nil {
				server.Close()
			}
		}
	}()
	go shared.followUpgrades(ctx, exe, func() error {
		if err := mcpshared.Handoff(configDir, exe, listener, lock); err != nil {
			return err
		}
		close(handedOff)
		return nil
	})
	err = server.Serve(listener)
	if !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-stopped
	return nil
}

type sharedServer struct {
	configDir  string
	version    string
	extensions []extension.Extension

	mu sync.Mutex
	// servers holds one MCP server per tool profile, built on first use.
	// The tool schemas are the bulk of a server's memory, so they are held
	// once per profile for the whole board rather than once per session.
	servers map[Profile]*mcp.Server
}

func (s *sharedServer) handler() http.Handler {
	mux := http.NewServeMux()
	// Health answers with proof of the key (mcpshared.Healthy), so a launch
	// never takes another process at this address for the server.
	mux.HandleFunc(mcpshared.HealthPath, func(w http.ResponseWriter, r *http.Request) {
		nonce := r.URL.Query().Get("nonce")
		if len(nonce) > 128 {
			http.Error(w, "nonce too long", http.StatusBadRequest)
			return
		}
		proof, err := parentseal.ServerProof(s.configDir, nonce)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, proof)
	})
	mcpHandler := mcp.NewStreamableHTTPHandler(s.serverFor, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	mux.Handle(mcpshared.MCPPath, s.authorized(mcpHandler))
	return mux
}

// authorized serves only a request that proves its caller, so a process
// without a session's token -- another user on the machine, or a page in a
// browser -- reaches nothing, not even the tool list.
func (s *sharedServer) authorized(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.Header.Get(CallerHeader))
		if id == "" || !parentseal.VerifyCaller(s.configDir, id, r.Header.Get(CallerTokenHeader)) {
			http.Error(w, "this server answers Gate Inbox sessions only: send "+CallerHeader+" and "+CallerTokenHeader, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// serverFor is the MCP server for the profile the request's path names, or
// nil (refused by the SDK) for a path naming none.
func (s *sharedServer) serverFor(r *http.Request) *mcp.Server {
	profile, err := ParseProfile(strings.TrimPrefix(r.URL.Path, mcpshared.MCPPath))
	if err != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.servers == nil {
		s.servers = map[Profile]*mcp.Server{}
	}
	server, ok := s.servers[profile]
	if !ok {
		// No session of its own: every call's caller comes from its request.
		server = NewProfileServer(s.configDir, "", s.version, s.extensions, "", profile)
		server.AddReceivingMiddleware(fixedTools)
		s.servers[profile] = server
	}
	return server
}

// fixedTools clears the tools capability's listChanged from what the
// server tells a client it can do. The SDK sets it for any server with
// tools, but this one's tool list never changes after it is built, and a
// client told it may change holds a subscriptions/listen stream open to
// hear about it. That stream is what strands a session when the server
// restarts: Claude Code reads the stream dropping as the server failing,
// retries for about 15 s, and then gives the server up until the user
// reconnects it by hand, while the board brings the server back on the same
// URL. With no stream held, a restart is invisible to a session between
// calls, and its next call reaches the new process.
func fixedTools(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		result, err := next(ctx, method, req)
		var caps *mcp.ServerCapabilities
		switch r := result.(type) {
		case *mcp.InitializeResult:
			caps = r.Capabilities
		case *mcp.DiscoverResult:
			caps = r.Capabilities
		}
		if caps != nil && caps.Tools != nil {
			caps.Tools.ListChanged = false
		}
		return result, err
	}
}

// followUpgrades hands the server over to a replaced binary
// (mcpshared.Handoff), so the sessions it serves pick up the upgrade without
// being restarted, and stops once one has taken over.
func (s *sharedServer) followUpgrades(ctx context.Context, exe string, handoff func() error) {
	stamp := mcpshared.BinaryStamp(exe)
	ticker := time.NewTicker(upgradeCheck)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if now := mcpshared.BinaryStamp(exe); now == "" || now == stamp {
			continue
		}
		logging.Info("shared MCP server handing over to the upgraded binary", "exe", exe)
		if err := handoff(); err != nil {
			logging.Warn("shared MCP server could not start the upgraded binary", logging.Err(err))
			stamp = mcpshared.BinaryStamp(exe)
			continue
		}
		return
	}
}
