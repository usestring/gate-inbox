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
	port := "0"
	for i, arg := range os.Args {
		if arg == "--port" && i+1 < len(os.Args) {
			port = os.Args[i+1]
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:"+port)
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
	mux.HandleFunc("PUT /api/session/{id}/environment", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body map[string]map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		raw, _ := json.Marshal(map[string]any{"session": r.PathValue("id"), "server": os.Getpid(), "env": body["variables"]})
		f, _ := os.OpenFile(os.Getenv(fakeServerEnv)+".env", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		fmt.Fprintln(f, string(raw))
		f.Close()
		w.WriteHeader(http.StatusNoContent)
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
			_ = syscall.Kill(srv.Supervisor, syscall.SIGTERM)
			_ = syscall.Kill(srv.PID, syscall.SIGTERM)
		}
	})
}

// testExe is the test binary, which TestMain runs as the supervisor.
func testExe(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe
}

// sessionEnvs is every environment the fake servers were given, in order.
func sessionEnvs(t *testing.T, requests string) []struct {
	Session string            `json:"session"`
	Server  int               `json:"server"`
	Env     map[string]string `json:"env"`
} {
	t.Helper()
	raw, _ := os.ReadFile(requests + ".env")
	var out []struct {
		Session string            `json:"session"`
		Server  int               `json:"server"`
		Env     map[string]string `json:"env"`
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var entry struct {
			Session string            `json:"session"`
			Server  int               `json:"server"`
			Env     map[string]string `json:"env"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		out = append(out, entry)
	}
	return out
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
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
	t.Setenv("OPENCODE_CONFIG", "session-config.json")
	t.Setenv(PasswordEnv, "leaked")
	t.Setenv("TMUX_PANE", "%7")
	attach := Attach{Home: home, Exe: testExe(t), Row: "rowA", Model: "acme/model-x", Auto: true, ServerConfig: "/shared/config.json", Dir: "/work/a", Argv: []string{bin, "--standalone"}}
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
	// The binding carries the row's caller token, so it verifies.
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
	// The session's shells get the launch's environment, without the
	// server's password, and it is kept for a restarted server.
	envs := sessionEnvs(t, requests)
	if len(envs) != 1 || envs[0].Session != "ses_fake1" {
		t.Fatalf("session environments = %+v", envs)
	}
	if env := envs[0].Env; env["GATE_INBOX_SESSION_ID"] != "rowA" || env["TMUX_PANE"] != "%7" || env["OPENCODE_CONFIG"] != "session-config.json" {
		t.Fatalf("session environment = %v", env)
	}
	if _, leaked := envs[0].Env[PasswordEnv]; leaked {
		t.Fatalf("server password in a session's environment: %v", envs[0].Env)
	}
	if info, err := os.Stat(filepath.Join(BindingsDir(home), "ses_fake1.env")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("kept session environment: %v %v", info, err)
	}

	// A revive names its session already: it is bound, not created, and the
	// same server takes it.
	again := Attach{Home: home, Exe: testExe(t), Row: "rowB", ServerConfig: "/shared/config.json", Dir: "/work/b", Argv: []string{bin, "--session", "ses_old2"}}
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
	if row, ok := BoundRow(home, "ses_old2"); !ok || row != "rowB" {
		t.Fatalf("revive binding = %q %v", row, ok)
	}
}

// A server that dies comes back on the same address and password, which
// its TUIs reconnect to, and every session gets its environment back.
func TestSharedServerRestartsWhereItWas(t *testing.T) {
	bin, requests := fakeOpencode(t)
	home := t.TempDir()
	stopServer(t, home)
	t.Setenv("GATE_INBOX_SESSION_ID", "rowA")
	attach := Attach{Home: home, Exe: testExe(t), Row: "rowA", ServerConfig: "/c.json", Dir: "/w", Argv: []string{bin}}
	if _, _, err := attach.Shared(context.Background()); err != nil {
		t.Fatal(err)
	}
	first, err := readServer(filepath.Join(home, serverStateName))
	if err != nil {
		t.Fatal(err)
	}
	_ = syscall.Kill(first.PID, syscall.SIGKILL)
	var second Server
	waitFor(t, "a restarted server", func() bool {
		second, err = readServer(filepath.Join(home, serverStateName))
		return err == nil && second.PID != first.PID && second.alive(context.Background())
	})
	if second.URL != first.URL || second.Password != first.Password || second.Supervisor != first.Supervisor {
		t.Fatalf("restarted as %+v, was %+v", second, first)
	}
	waitFor(t, "the session's environment set again", func() bool {
		envs := sessionEnvs(t, requests)
		last := envs[len(envs)-1]
		return last.Server == second.PID && last.Session == "ses_fake1" && last.Env["GATE_INBOX_SESSION_ID"] == "rowA"
	})
	if got, err := EnsureServer(context.Background(), home, testExe(t), bin, "/c.json"); err != nil || got.PID != second.PID {
		t.Fatalf("EnsureServer after restart = %+v, %v", got, err)
	}
}

// A server whose supervisor is gone too is replaced, never handed a session.
func TestSharedAttachReplacesADeadServer(t *testing.T) {
	bin, _ := fakeOpencode(t)
	home := t.TempDir()
	stopServer(t, home)
	first, err := EnsureServer(context.Background(), home, testExe(t), bin, "/c.json")
	if err != nil {
		t.Fatal(err)
	}
	_ = syscall.Kill(first.Supervisor, syscall.SIGKILL)
	_ = syscall.Kill(first.PID, syscall.SIGKILL)
	waitFor(t, "the server to die", func() bool {
		return !first.alive(context.Background()) && !supervising(context.Background(), first.Supervisor)
	})
	second, err := EnsureServer(context.Background(), home, testExe(t), bin, "/c.json")
	if err != nil {
		t.Fatal(err)
	}
	if second.PID == first.PID || second.Supervisor == first.Supervisor {
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
}

func TestSharedAttachRejectsBadInput(t *testing.T) {
	if err := bindSession(t.TempDir(), "../../etc/passwd", "row"); err == nil {
		t.Fatal("wrote a binding outside its directory")
	}
	if err := bindSession(t.TempDir(), "ses_a", "row extra"); err == nil {
		t.Fatal("wrote a binding the plugin would split wrongly")
	}
	forged := t.TempDir()
	if err := bindSession(forged, "ses_b", "rowB"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(BindingsDir(forged), "ses_a"), []byte("rowA tokA\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if row, ok := BoundRow(forged, "ses_a"); ok {
		t.Fatalf("a binding without the row's token named %q", row)
	}
	if _, err := (Server{}).CreateSession(context.Background(), "/d", "no-slash"); err == nil {
		t.Fatal("accepted a model without a provider")
	}
}
