package mcpshared

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/envname"
)

func TestOneServerHoldsAConfigDir(t *testing.T) {
	dir := t.TempDir()
	listener, lock, err := Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := URL(dir), "http://"+listener.Addr().String(); got != want {
		t.Fatalf("recorded %q, want %q", got, want)
	}
	if _, _, err := Listen(dir); !errors.Is(err, ErrRunning) {
		t.Fatalf("a second server got %v, want ErrRunning", err)
	}
	recorded := URL(dir)
	listener.Close()
	lock.Close()

	// The next server takes the recorded port, so sessions given its URL
	// keep reaching it.
	listener, lock, err = Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	defer listener.Close()
	if URL(dir) != recorded {
		t.Fatalf("restarted at %q, want the recorded %q", URL(dir), recorded)
	}
}

func TestHealthyTellsAServingURLFromAStaleOne(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != HealthPath {
			http.NotFound(w, r)
		}
	}))
	if !Healthy(server.URL) {
		t.Fatal("a serving server read as unhealthy")
	}
	server.Close()
	if Healthy(server.URL) || Healthy("") {
		t.Fatal("a closed server, or none, read as healthy")
	}
}

// The server acts as no session: it never carries the launching session's
// identity, or the tmux pane it was started from.
func TestServerEnvCarriesNoSession(t *testing.T) {
	env := serverEnv([]string{
		envname.SessionID + "=abcd1234", envname.SessionToken + "=secret", "TMUX_PANE=%1",
		config.HomeEnv + "=/elsewhere", "PATH=/bin",
	}, "/home/x/.config/gate-inbox")
	for _, entry := range env {
		if strings.HasPrefix(entry, envname.SessionID+"=") || strings.HasPrefix(entry, envname.SessionToken+"=") || strings.HasPrefix(entry, "TMUX_PANE=") {
			t.Fatalf("server env carries %s", entry)
		}
	}
	if !slices.Contains(env, "PATH=/bin") || !slices.Contains(env, config.HomeEnv+"=/home/x/.config/gate-inbox") {
		t.Fatalf("server env %v lost PATH or the config dir", env)
	}
}
