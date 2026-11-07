package mcpserver

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/mcpshared"
	"github.com/usestring/gate-inbox/internal/parentseal"
)

// upgradeCheck is how often the shared server looks for a replaced binary.
const upgradeCheck = 30 * time.Second

// ServeShared serves every session of configDir from this one process,
// over HTTP on loopback, until ctx ends (see mcpshared). It returns nil at
// once when another server already holds the config dir.
//
// It is stateless: every request carries its caller, so the server keeps
// nothing per session, any request can land on any process, and the
// process can be replaced between two requests without a client noticing.
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
	go func() {
		<-ctx.Done()
		server.Close()
	}()
	go shared.followUpgrades(ctx, exe, func() error {
		// Idle connections would die with the exec; closing them first has
		// clients open fresh ones to the new process instead.
		server.SetKeepAlivesEnabled(false)
		return mcpshared.Reexec(exe, listener, lock)
	})
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
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
	// inFlight counts requests being served, which an upgrade waits out.
	inFlight atomic.Int64
}

func (s *sharedServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(mcpshared.HealthPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
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
		s.inFlight.Add(1)
		defer s.inFlight.Add(-1)
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
		s.servers[profile] = server
	}
	return server
}

// followUpgrades moves the server onto a replaced binary once no request is
// in flight, so the sessions it serves pick up the upgrade without being
// restarted.
func (s *sharedServer) followUpgrades(ctx context.Context, exe string, reexec func() error) {
	stamp := mcpshared.BinaryStamp(exe)
	ticker := time.NewTicker(upgradeCheck)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if now := mcpshared.BinaryStamp(exe); now == "" || now == stamp || s.inFlight.Load() > 0 {
			continue
		}
		logging.Info("shared MCP server moving onto the upgraded binary", "exe", exe)
		if err := reexec(); err != nil {
			logging.Warn("shared MCP server could not exec the upgraded binary", logging.Err(err))
			stamp = mcpshared.BinaryStamp(exe)
		}
	}
}
