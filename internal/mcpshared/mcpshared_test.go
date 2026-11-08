package mcpshared

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/envname"
	"github.com/usestring/gate-inbox/internal/mcpreg"
	"github.com/usestring/gate-inbox/internal/parentseal"
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
		envname.SessionID + "=abcd1234", "TMUX_PANE=%1",
		config.HomeEnv + "=/elsewhere", "PATH=/bin",
	}, "/home/x/.config/gate-inbox")
	for _, entry := range env {
		if strings.HasPrefix(entry, envname.SessionID+"=") || strings.HasPrefix(entry, "TMUX_PANE=") {
			t.Fatalf("server env carries %s", entry)
		}
	}
	if !slices.Contains(env, "PATH=/bin") || !slices.Contains(env, config.HomeEnv+"=/home/x/.config/gate-inbox") {
		t.Fatalf("server env %v lost PATH or the config dir", env)
	}
}

// A session's config names it and its token outright, readable by its owner
// alone, beside the keys a managed session cannot read.
func TestClaudeConfigNamesItsSessionPrivately(t *testing.T) {
	dir := t.TempDir()
	path := ClaudeConfigPath(dir, "abcd1234")
	if filepath.Dir(path) != parentseal.KeyDir(dir) {
		t.Fatalf("config at %s, want it in the key dir", path)
	}
	if err := WriteClaudeConfig(path, "http://127.0.0.1:4567", "core", "abcd1234", "tok"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config is %v, want 0600", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	entry := config.MCPServers[mcpreg.ServerName]
	if entry.Type != "http" || entry.URL != "http://127.0.0.1:4567"+MCPPath+"core" {
		t.Fatalf("entry = %+v, want an http entry for the core profile", entry)
	}
	if entry.Headers[CallerHeader] != "abcd1234" || entry.Headers[CallerTokenHeader] != "tok" {
		t.Fatalf("headers = %v, want the session and its token", entry.Headers)
	}
}
