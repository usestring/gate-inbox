package mcpshared

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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

func TestHealthyTellsTheServerFromAStaleURLOrASquatter(t *testing.T) {
	dir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proof, _ := parentseal.ServerProof(dir, r.URL.Query().Get("nonce"))
		_, _ = w.Write([]byte(proof))
	}))
	if !Healthy(dir, server.URL) {
		t.Fatal("the config dir's own server read as unhealthy")
	}
	if Healthy(t.TempDir(), server.URL) {
		t.Fatal("another config dir's server passed as this one's")
	}
	server.Close()
	if Healthy(dir, server.URL) || Healthy(dir, "") {
		t.Fatal("a closed server, or none, read as healthy")
	}
	// Before the proof, anything answering 200 at the recorded address was
	// taken for the server and handed every session's token.
	squatter := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer squatter.Close()
	if Healthy(dir, squatter.URL) {
		t.Fatal("a squatter answering 200 read as the server")
	}
}

// A server never moves off a recorded address another process holds: the
// sessions given it would keep sending that process their tokens unwarned.
func TestListenRefusesARecordedAddressAnotherProcessHolds(t *testing.T) {
	squatter, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer squatter.Close()
	dir := t.TempDir()
	if err := record(dir, squatter.Addr()); err != nil {
		t.Fatal(err)
	}
	if listener, lock, err := Listen(dir); err == nil {
		listener.Close()
		lock.Close()
		t.Fatalf("listened at %s with the recorded address held", listener.Addr())
	}
	if _, err := Ensure(dir, "/bin/false", time.Second); err == nil {
		t.Fatal("Ensure handed out a squatter's address")
	}
}

// A launch waits for the server only as long as it is told to.
func TestEnsureWaitsNoLongerThanAsked(t *testing.T) {
	dir := t.TempDir()
	slow := filepath.Join(dir, "slow")
	if err := os.WriteFile(slow, []byte("#!/bin/sh\nexec sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := Ensure(dir, slow, 200*time.Millisecond); err == nil {
		t.Fatal("a server that never answered was handed out")
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("Ensure took %s with a 200ms wait", took)
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
	path, err := WriteClaudeConfig(dir, "http://127.0.0.1:4567", "core", "abcd1234")
	if err != nil {
		t.Fatal(err)
	}
	if path != ClaudeConfigPath(dir, "abcd1234") || filepath.Dir(path) != parentseal.KeyDir(dir) {
		t.Fatalf("config at %s, want it in the key dir", path)
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
	if entry.Headers[CallerHeader] != "abcd1234" || !parentseal.VerifyCaller(dir, "abcd1234", entry.Headers[CallerTokenHeader]) {
		t.Fatalf("headers = %v, want the session and its own token", entry.Headers)
	}
}
