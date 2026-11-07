package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeServerEnv makes the test binary an opencode server: it answers
// /api/health and /api/session with the password it was started with, and
// records every session request's body in the file the variable names.
const fakeServerEnv = "GATE_INBOX_TEST_FAKE_OPENCODE"

func fakeServe() {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		os.Exit(1)
	}
	password := os.Getenv(PasswordEnv)
	authed := func(r *http.Request) bool {
		user, pass, ok := r.BasicAuth()
		return ok && user == serverUser && pass == password
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			w.WriteHeader(http.StatusUnauthorized)
		}
	})
	mux.HandleFunc("/api/session", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) || r.Method != http.MethodPost {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		raw, _ := json.Marshal(map[string]any{"body": body, "env": map[string]string{
			"config": os.Getenv("OPENCODE_CONFIG"), "row": os.Getenv("GATE_INBOX_SESSION_ID") + os.Getenv("GATE_INBOX_SESSION_TOKEN"), "fff": os.Getenv("OPENCODE_DISABLE_FFF"),
		}})
		f, _ := os.OpenFile(os.Getenv(fakeServerEnv), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		fmt.Fprintln(f, string(raw))
		f.Close()
		fmt.Fprint(w, `{"data":{"id":"ses_fake1"}}`)
	})
	fmt.Printf("server listening on http://%s\n", listener.Addr())
	_ = http.Serve(listener, mux)
}

// fakeOpencode is a directory holding an "opencode" that is this test binary
// in fake-server mode, and the file its session requests land in.
func fakeOpencode(t *testing.T) (bin, requests string) {
	t.Helper()
	dir := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin = filepath.Join(dir, "opencode")
	if err := os.Symlink(self, bin); err != nil {
		t.Fatal(err)
	}
	requests = filepath.Join(dir, "requests.jsonl")
	t.Setenv(fakeServerEnv, requests)
	return bin, requests
}

func stopServer(t *testing.T, home string) {
	t.Cleanup(func() {
		if srv, err := readServer(filepath.Join(home, serverStateName)); err == nil {
			_ = syscall.Kill(srv.PID, syscall.SIGTERM)
		}
	})
}

// A launch with no session opens one on the shared server, in its own
// directory and on its model, binds it to its row and attaches the TUI to
// it; the next launch reuses the server rather than start a second. The
// server never sees the launching row's identity or private config.
func TestSharedAttachStartsReusesAndBinds(t *testing.T) {
	bin, requests := fakeOpencode(t)
	home := t.TempDir()
	stopServer(t, home)
	t.Setenv("GATE_INBOX_SESSION_ID", "rowA")
	t.Setenv("GATE_INBOX_SESSION_TOKEN", "tokA")
	t.Setenv("OPENCODE_CONFIG", "session-config.json")
	attach := Attach{Home: home, Row: "rowA", Token: "tokA", Model: "acme/model-x", Auto: true, ServerConfig: "/shared/config.json", Dir: "/work/a", Argv: []string{bin, "--standalone"}}
	argv, env, err := attach.Shared(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	srv, err := readServer(filepath.Join(home, serverStateName))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{bin, "--session", "ses_fake1", "--server", srv.URL, "--auto"}
	if !slices.Equal(argv, want) {
		t.Fatalf("argv = %q, want %q", argv, want)
	}
	if env[PasswordEnv] != srv.Password || srv.Password == "" {
		t.Fatalf("TUI env = %v, server %+v", env, srv)
	}
	if raw, err := os.ReadFile(filepath.Join(BindingsDir(home), "ses_fake1")); err != nil || string(raw) != "rowA tokA\n" {
		t.Fatalf("binding = %q, %v", raw, err)
	}
	if row, ok := BoundRow(home, "ses_fake1"); !ok || row != "rowA" {
		t.Fatalf("BoundRow = %q %v", row, ok)
	}
	if SharedServerPID(home) == 0 {
		t.Fatal("no server pid recorded")
	}
	if raw, err := os.ReadFile(filepath.Join(home, "hooks", "rowA.conversation")); err != nil || string(raw) != "ses_fake1\n" {
		t.Fatalf("conversation mailbox = %q, %v", raw, err)
	}
	raw, _ := os.ReadFile(requests)
	var first struct {
		Body map[string]any    `json:"body"`
		Env  map[string]string `json:"env"`
	}
	if err := json.Unmarshal([]byte(strings.Split(string(raw), "\n")[0]), &first); err != nil {
		t.Fatal(err)
	}
	if got, _ := json.Marshal(first.Body); string(got) != `{"location":{"directory":"/work/a"},"model":{"id":"model-x","providerID":"acme"}}` {
		t.Fatalf("session request = %s", got)
	}
	if first.Env["config"] != "/shared/config.json" || first.Env["row"] != "" || first.Env["fff"] != "1" {
		t.Fatalf("server env = %v", first.Env)
	}

	// A revive names its session already: it is bound, not created, and the
	// same server takes it.
	again := Attach{Home: home, Row: "rowB", Token: "tokB", ServerConfig: "/shared/config.json", Dir: "/work/b", Argv: []string{bin, "--session", "ses_old2"}}
	argv, _, err = again.Shared(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(argv, []string{bin, "--session", "ses_old2", "--server", srv.URL}) {
		t.Fatalf("revive argv = %q", argv)
	}
	if reused, _ := readServer(filepath.Join(home, serverStateName)); reused.PID != srv.PID {
		t.Fatalf("second launch started another server: %d then %d", srv.PID, reused.PID)
	}
	if raw, _ := os.ReadFile(requests); strings.Count(string(raw), "\n") != 1 {
		t.Fatalf("a revive created a session:\n%s", raw)
	}
	if raw, _ := os.ReadFile(filepath.Join(BindingsDir(home), "ses_old2")); string(raw) != "rowB tokB\n" {
		t.Fatalf("revive binding = %q", raw)
	}
}

// A server that has gone away is replaced, never handed a session.
func TestSharedAttachReplacesADeadServer(t *testing.T) {
	bin, _ := fakeOpencode(t)
	home := t.TempDir()
	stopServer(t, home)
	first, err := EnsureServer(context.Background(), home, bin, "/c.json")
	if err != nil {
		t.Fatal(err)
	}
	_ = syscall.Kill(first.PID, syscall.SIGKILL)
	deadline := time.Now().Add(5 * time.Second)
	for first.alive(context.Background()) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	second, err := EnsureServer(context.Background(), home, bin, "/c.json")
	if err != nil {
		t.Fatal(err)
	}
	if second.PID == first.PID || second.URL == first.URL {
		t.Fatalf("dead server reused: %+v", second)
	}
}

// Whatever fails, the session can run standalone: the launch's own argv with
// a private server, never the flag twice.
func TestAttachStandalone(t *testing.T) {
	a := Attach{Argv: []string{"opencode", "--session", "ses_x"}}
	if got := a.Standalone(); !slices.Equal(got, []string{"opencode", "--session", "ses_x", "--standalone"}) {
		t.Fatalf("standalone = %q", got)
	}
	a.Argv = append(a.Argv, "--standalone")
	if got := a.Standalone(); !slices.Equal(got, []string{"opencode", "--session", "ses_x", "--standalone"}) {
		t.Fatalf("standalone twice = %q", got)
	}
	if _, _, err := (Attach{Argv: []string{"opencode"}}).Shared(context.Background()); err == nil {
		t.Fatal("attached with no row")
	}
	if _, _, err := (Attach{Row: "r", Argv: []string{"opencode"}}).Shared(context.Background()); err == nil {
		t.Fatal("attached with no caller token")
	}
}

func TestSharedAttachRejectsBadInput(t *testing.T) {
	if err := writeBinding(t.TempDir(), "../../etc/passwd", "row", "tok"); err == nil {
		t.Fatal("wrote a binding outside its directory")
	}
	if err := writeBinding(t.TempDir(), "ses_a", "row extra", "tok"); err == nil {
		t.Fatal("wrote a binding the plugin would split wrongly")
	}
	if _, err := (Server{}).CreateSession(context.Background(), "/d", "no-slash"); err == nil {
		t.Fatal("accepted a model without a provider")
	}
}
