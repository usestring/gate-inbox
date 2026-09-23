package debugserver

import (
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestResolveDefaultsToNothing(t *testing.T) {
	for _, spec := range []string{"", "   "} {
		addr, err := Resolve(spec)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", spec, err)
		}
		if addr != "" {
			t.Fatalf("Resolve(%q) = %q, want no listener", spec, addr)
		}
	}
}

func TestResolveBindsLoopback(t *testing.T) {
	cases := map[string]string{
		"6060":           "127.0.0.1:6060",
		":6060":          "127.0.0.1:6060",
		" 6060 ":         "127.0.0.1:6060",
		"127.0.0.1:6060": "127.0.0.1:6060",
		"localhost:6060": "localhost:6060",
		"127.0.0.2:6060": "127.0.0.2:6060",
		"[::1]:6060":     "[::1]:6060",
	}
	for spec, want := range cases {
		got, err := Resolve(spec)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", spec, err)
		}
		if got != want {
			t.Fatalf("Resolve(%q) = %q, want %q", spec, got, want)
		}
	}
}

// The refusal is the security property, not a nicety: pprof serves the heap,
// the command line and every goroutine stack to whoever reaches the port.
func TestResolveRefusesNonLoopback(t *testing.T) {
	for _, spec := range []string{
		"0.0.0.0:6060",
		"10.0.0.4:6060",
		"[::]:6060",
		"example.com:6060",
		"nonsense",
	} {
		if addr, err := Resolve(spec); err == nil {
			t.Fatalf("Resolve(%q) = %q, want refusal", spec, addr)
		}
	}
}

func TestResolveRejectsPortOutOfRange(t *testing.T) {
	if _, err := Resolve("70000"); err == nil {
		t.Fatal("Resolve(70000): want refusal")
	}
}

// An unset switch must cost nothing at all -- no listener, and a Close that a
// caller can defer without first testing whether it started.
func TestStartInertWhenUnset(t *testing.T) {
	server, err := Start("")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if server != nil {
		t.Fatalf("Start(\"\") = %v, want nil", server)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("Close on nil: %v", err)
	}
	if addr := server.Addr(); addr != "" {
		t.Fatalf("Addr on nil = %q", addr)
	}
}

func TestStartServesProfiles(t *testing.T) {
	server, err := Start("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { server.Close() })

	host, _, err := net.SplitHostPort(server.Addr())
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", server.Addr(), err)
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		t.Fatalf("bound %q, want loopback", server.Addr())
	}

	client := &http.Client{Timeout: 10 * time.Second}
	for _, path := range []string{"/debug/pprof/heap?debug=1", "/debug/pprof/goroutine?debug=1"} {
		resp, err := client.Get("http://" + server.Addr() + path)
		if err != nil {
			t.Fatalf("get %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("get %s: status %d", path, resp.StatusCode)
		}
		if len(body) == 0 {
			t.Fatalf("get %s: empty profile", path)
		}
	}

	// The index must be this package's mux rather than whatever else may
	// have registered on http.DefaultServeMux.
	resp, err := client.Get("http://" + server.Addr() + "/debug/pprof/")
	if err != nil {
		t.Fatalf("get index: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "goroutine") {
		t.Fatalf("index does not list profiles: %q", string(body))
	}
}

func TestCloseStopsListening(t *testing.T) {
	server, err := Start("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	addr := server.Addr()
	if err := server.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if conn, err := net.DialTimeout("tcp", addr, 2*time.Second); err == nil {
		conn.Close()
		t.Fatalf("still listening on %s after Close", addr)
	}
}
