// Package debugserver exposes net/http/pprof on loopback, and only when the
// operator asks for it.
//
// The manager is the longest-lived process this module ships -- it runs for
// days against a board of sessions that come and go -- and until this existed
// there was no way to look inside a running one. Answering "is it leaking?"
// meant reading RSS from the outside and guessing, which cannot tell a heap
// that grows from a heap that is merely fragmented, and cannot see a parked
// goroutine at all.
//
// Importing net/http/pprof registers its handlers on http.DefaultServeMux as
// a side effect, which is why this package builds its own mux instead: the
// manager must not acquire a debug surface merely because something in its
// dependency tree imported the same package.
package debugserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"strconv"
	"strings"
	"time"
)

// Env is the switch. Unset or empty means no listener is opened and no port
// is bound: the default build of the manager has no debug surface at all.
const Env = "GATE_INBOX_PPROF"

// shutdownGrace bounds how long Close waits for an in-flight profile to
// finish. A 30-second CPU profile outlives it, and that is the intent: the
// manager is quitting, and the operator's terminal must come back.
const shutdownGrace = 2 * time.Second

// Server is a running pprof listener.
type Server struct {
	http *http.Server
	ln   net.Listener
}

// Addr reports the address actually bound, which is what a caller asked for
// port 0 needs in order to connect.
func (s *Server) Addr() string {
	if s == nil || s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// Close stops the listener. It is safe on a nil Server, so the caller can
// defer it without first testing whether profiling was ever switched on.
func (s *Server) Close() error {
	if s == nil || s.http == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	err := s.http.Shutdown(ctx)
	// Shutdown closes only the listeners Serve has already registered, and
	// Serve runs on its own goroutine: a Close that lands first finds none
	// and leaves the port bound. Closing the listener here is what makes
	// Close mean it. A second close after Shutdown already did it returns
	// ErrClosed, which is not a failure to report.
	if closeErr := s.ln.Close(); err == nil && !errors.Is(closeErr, net.ErrClosed) {
		err = closeErr
	}
	return err
}

// Start opens a pprof listener at spec, or returns (nil, nil) when spec is
// empty -- the case that must stay free, since it is every run that did not
// ask for this.
//
// A returned error is worth surfacing rather than swallowing: an operator who
// set the variable and got no listener would otherwise spend the soak
// wondering why nothing answers on the port.
func Start(spec string) (*Server, error) {
	addr, err := Resolve(spec)
	if err != nil || addr == "" {
		return nil, err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("pprof listen %s: %w", addr, err)
	}
	server := &http.Server{
		Handler: mux(),
		// A profile is fetched with an explicit ?seconds=, so a read that
		// takes minutes is the normal case and must not be cut off. The
		// header deadline still keeps a stuck connection from holding a
		// goroutine open forever.
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// There is nowhere to report this: the TUI owns the terminal by
			// the time it could happen, and the manager keeps running
			// without its debug surface rather than dying for it.
			_ = err
		}
	}()
	return &Server{http: server, ln: ln}, nil
}

// mux serves pprof's handlers off a mux of this package's own, rather than
// letting the import's registration on http.DefaultServeMux be what answers.
func mux() *http.ServeMux {
	m := http.NewServeMux()
	m.HandleFunc("/debug/pprof/", pprof.Index)
	m.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	m.HandleFunc("/debug/pprof/profile", pprof.Profile)
	m.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	m.HandleFunc("/debug/pprof/trace", pprof.Trace)
	return m
}

// Resolve turns the environment variable's value into a listen address,
// refusing anything that is not loopback.
//
// The refusal is the point of the function. A pprof surface serves the
// process's whole heap, its command line and its goroutine stacks to anyone
// who can reach the port, and the manager runs on a shared development host. A typo of
// ":6060" for "127.0.0.1:6060" is one character and would publish all of it
// to the network, so a bare port and a bare-colon port are both read as
// loopback rather than treated as the wildcard bind they mean to net.Listen.
func Resolve(spec string) (string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", nil
	}
	// A bare port, and ":port", both mean loopback here.
	if port, err := strconv.Atoi(strings.TrimPrefix(spec, ":")); err == nil {
		if port < 0 || port > 65535 {
			return "", fmt.Errorf("%s: port %d out of range", Env, port)
		}
		return net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), nil
	}
	host, port, err := net.SplitHostPort(spec)
	if err != nil {
		return "", fmt.Errorf("%s: %q is not host:port", Env, spec)
	}
	// An empty host is the wildcard to net.Listen, not loopback. A named
	// port reaches here with one -- ":postgresql" splits to host "" and port
	// "postgresql" -- and passing that through would publish the heap on
	// every interface, which is the single thing this function exists to
	// prevent. Name the interface instead of letting the empty string mean
	// "all of them".
	if host == "" {
		host = "127.0.0.1"
	}
	if !isLoopback(host) {
		return "", fmt.Errorf("%s: %q is not a loopback address; pprof serves the heap and goroutine stacks and is bound to loopback only", Env, host)
	}
	return net.JoinHostPort(host, port), nil
}

// isLoopback accepts the names and literals that cannot leave the machine.
// "localhost" is taken on its name rather than resolved: a resolver that
// answers with a routable address for it is a reason to refuse, and this
// function is not the place to discover that.
func isLoopback(host string) bool {
	if strings.ToLower(strings.TrimSpace(host)) == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
