package opencode

// A private `opencode serve` per session costs it 270-370 MB of the ~480 it
// holds; the TUI in its pane is the rest. One server per board, with every
// launched session's TUI attached to it, leaves each session costing only
// its TUI: measured by PSS, a server with one, three and six TUIs attached
// holds about the same memory.
//
// What a private server gave a session for free has to be rebuilt here:
//
//   - The server reads its own environment and config, never a client's, so
//     it is started with the board's generic config and no row id, and the
//     MCP server it spawns serves every session at once. The global plugin
//     puts the calling row and its caller token into every one of the
//     board's tool calls from the binding Attach records, and the MCP server
//     acts as that row once the token proves it.
//   - The model a session was asked to run on goes on the session itself,
//     created here before the TUI opens it, and skipping permission prompts
//     is the TUI's own --auto.
//   - A shell the server runs for a session gets the environment set on
//     that session, or the server's own when none is. Attach sets the
//     launch's environment on the session, so a shell has its row's id,
//     caller token and pane as a private server's would, and never the
//     shared server's password. The global plugin fills the row in from the
//     binding for a session whose environment was lost.
//   - A private server dies with its pane; the shared one is every
//     session's, so a supervisor restarts it on the same address and
//     password, which its TUIs reconnect to on their own, and sets each
//     session's environment on it again.
//
// Whatever goes wrong on the way, the session still starts: Attach falls back
// to the private server it would have had.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v4/process"
	"github.com/usestring/gate-inbox/internal/envname"
	"github.com/usestring/gate-inbox/internal/parentseal"
)

// PasswordEnv is the variable an opencode client reads its server's password
// from, and the server its own.
const PasswordEnv = "OPENCODE_SERVER_PASSWORD"

// serverUser is the user opencode's server expects in HTTP basic auth.
const serverUser = "opencode"

// serverStateName is the file under the board home that names the running
// shared server; serverLockName serialises starting one.
const (
	serverStateName = "opencode-server.json"
	serverLockName  = "opencode-server.lock"
	serverLogName   = "opencode-server.log"
)

// BindingsDir is where Attach records which row each opencode session is:
// one file per session id holding the row id and the row's caller token,
// which the global plugin puts into the session's calls to the board's
// tools. It sits under the board's key directory, which managed sandboxes
// cannot read, so one agent cannot take another's token from it.
func BindingsDir(home string) string {
	return filepath.Join(home, parentseal.KeyDirName, "opencode-sessions")
}

// serverEnvDrop is what a shared server must not inherit from the launch
// that happened to start it: a row's identity, its pane, and its private
// config would all be taken for every other session's.
var serverEnvDrop = []string{"GATE_INBOX_SESSION_ID", envname.SessionToken, "GATE_INBOX_EXIT_FILE", "TMUX", "TMUX_PANE", "OPENCODE_CONFIG", PasswordEnv, "OPENCODE_PASSWORD"}

// serverMemoryEnv trims the shared server the way a launch trims a private
// one: no in-memory index of the working tree.
var serverMemoryEnv = map[string]string{"OPENCODE_DISABLE_FFF": "1"}

// Server is the board's shared opencode server.
type Server struct {
	PID      int    `json:"pid"`
	URL      string `json:"url"`
	Password string `json:"password"`
	// Supervisor is the process that restarts the server when it dies.
	Supervisor int `json:"supervisor,omitempty"`
}

// Attach is one launch of an opencode TUI against the shared server.
type Attach struct {
	// Home is the board's config directory.
	Home string
	// Exe is the gate-inbox binary, which supervises the shared server.
	Exe string
	// Row is the Gate Inbox session the TUI shows, and Token the caller
	// token that proves it (see parentseal.CallerToken).
	Row   string
	Token string
	// Model is provider/model, or "" for the CLI's default.
	Model string
	// Auto skips the session's permission prompts.
	Auto bool
	// ServerConfig is the generated config the shared server loads.
	ServerConfig string
	// Dir is the session's working directory.
	Dir string
	// Argv is the opencode command the launch would have run.
	Argv []string
}

// Standalone is the argv a session runs with a private server: the launch's
// own, whose environment already names its per-session config.
func (a Attach) Standalone() []string {
	argv := slices.Clone(a.Argv)
	if !slices.Contains(argv, "--standalone") {
		argv = append(argv, "--standalone")
	}
	return argv
}

// Shared starts or reuses the board's server, opens the session on it and
// records which row it is, and returns the TUI's argv and the variables to
// add to its environment.
func (a Attach) Shared(ctx context.Context) ([]string, map[string]string, error) {
	if a.Row == "" || a.Token == "" {
		return nil, nil, errors.New("opencode: no row and caller token to attach as")
	}
	srv, err := EnsureServer(ctx, a.Home, a.Exe, a.Argv[0], a.ServerConfig)
	if err != nil {
		return nil, nil, err
	}
	argv := slices.DeleteFunc(slices.Clone(a.Argv), func(arg string) bool { return arg == "--standalone" })
	session := sessionFlag(argv)
	if session == "" {
		if session, err = srv.CreateSession(ctx, a.Dir, a.Model); err != nil {
			return nil, nil, err
		}
		argv = append(argv, "--session", session)
	}
	if err := writeBinding(a.Home, session, a.Row, a.Token); err != nil {
		return nil, nil, err
	}
	// The session's shells run with this, the environment the TUI is about
	// to get, as a private server's would.
	env := shellEnv(os.Environ())
	if err := writeSessionEnv(a.Home, session, env); err != nil {
		return nil, nil, err
	}
	if err := srv.SetEnvironment(ctx, session, env); err != nil {
		return nil, nil, err
	}
	// The poller binds the row to its conversation from this mailbox, as it
	// does for one the plugin reports, so the row knows its session at once.
	if err := os.MkdirAll(filepath.Join(a.Home, "hooks"), 0o755); err != nil {
		return nil, nil, err
	}
	if err := writeWhole(filepath.Join(a.Home, "hooks", a.Row+".conversation"), []byte(session+"\n"), 0o644); err != nil {
		return nil, nil, err
	}
	argv = append(argv, "--server", srv.URL)
	if a.Auto && !slices.Contains(argv, "--auto") {
		argv = append(argv, "--auto")
	}
	return argv, map[string]string{PasswordEnv: srv.Password}, nil
}

// sessionFlag is the session id an argv already names with --session or -s.
func sessionFlag(argv []string) string {
	for i, arg := range argv {
		for _, flag := range []string{"--session", "-s"} {
			if arg == flag && i+1 < len(argv) {
				return argv[i+1]
			}
			if value, ok := strings.CutPrefix(arg, flag+"="); ok {
				return value
			}
		}
	}
	return ""
}

var bindingName = regexp.MustCompile(`^ses_[A-Za-z0-9]+$`)

func writeBinding(home, session, row, token string) error {
	if !bindingName.MatchString(session) {
		return fmt.Errorf("opencode: %q is not a session id", session)
	}
	if strings.ContainsAny(row+token, " \n") {
		return errors.New("opencode: row or token holds whitespace")
	}
	dir := BindingsDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return writeWhole(filepath.Join(dir, session), []byte(row+" "+token+"\n"), 0o600)
}

// shellEnv is environ as a shell on the shared server gets it: the server's
// password would let any session read and drive every other one.
func shellEnv(environ []string) map[string]string {
	env := make(map[string]string, len(environ))
	for _, kv := range environ {
		if key, value, ok := strings.Cut(kv, "="); ok && key != PasswordEnv && key != "OPENCODE_PASSWORD" {
			env[key] = value
		}
	}
	return env
}

// sessionEnvSuffix names the file beside a binding holding the environment
// Attach set on the session, which the supervisor sets again on a restarted
// server: the server keeps it in memory only.
const sessionEnvSuffix = ".env"

func writeSessionEnv(home, session string, env map[string]string) error {
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return writeWhole(filepath.Join(BindingsDir(home), session+sessionEnvSuffix), raw, 0o600)
}

func writeWhole(path string, content []byte, mode os.FileMode) error {
	staging := fmt.Sprintf("%s.%d.part", path, os.Getpid())
	if err := os.WriteFile(staging, content, mode); err != nil {
		return err
	}
	return os.Rename(staging, path)
}

// EnsureServer returns the board's shared server, starting one under a
// supervisor when none runs. exe is the gate-inbox binary that supervises it,
// bin the opencode executable and config the generated config it loads.
func EnsureServer(ctx context.Context, home, exe, bin, config string) (Server, error) {
	lock, err := os.OpenFile(filepath.Join(home, serverLockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return Server{}, err
	}
	defer lock.Close()
	// Two launches at once would otherwise start two servers, and the
	// second's state would orphan the first along with its sessions.
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return Server{}, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	statePath := filepath.Join(home, serverStateName)
	srv, err := readServer(statePath)
	if err == nil && srv.alive(ctx) {
		return srv, nil
	}
	// A live supervisor is restarting its server; a second one would fight
	// it for the address every attached TUI reconnects to.
	if err == nil && supervising(ctx, srv.Supervisor) {
		return awaitServer(ctx, statePath, srv.Supervisor, 0)
	}
	pid, err := startSupervisor(ctx, home, exe, bin, config)
	if err != nil {
		return Server{}, err
	}
	return awaitServer(ctx, statePath, pid, srv.PID)
}

// awaitServer waits for the supervisor to record a server that answers,
// other than stale, the one an earlier supervisor left behind.
func awaitServer(ctx context.Context, statePath string, supervisor, stale int) (Server, error) {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if srv, err := readServer(statePath); err == nil && srv.Supervisor == supervisor && srv.PID != stale && srv.alive(ctx) {
			return srv, nil
		}
		if !supervising(ctx, supervisor) {
			return Server{}, fmt.Errorf("opencode: shared server supervisor exited; see %s", filepath.Join(filepath.Dir(statePath), serverLogName))
		}
		select {
		case <-ctx.Done():
			return Server{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return Server{}, fmt.Errorf("opencode: shared server did not answer within 30s; see %s", filepath.Join(filepath.Dir(statePath), serverLogName))
}

func readServer(path string) (Server, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Server{}, err
	}
	var srv Server
	if err := json.Unmarshal(raw, &srv); err != nil {
		return Server{}, err
	}
	if srv.PID <= 0 || srv.URL == "" || srv.Password == "" {
		return Server{}, errors.New("opencode: incomplete server state")
	}
	return srv, nil
}

// alive is the recorded server still running and answering with its
// password. The process check comes first: a reused pid, or a port another
// program took over, must not be handed sessions.
func (s Server) alive(ctx context.Context) bool {
	proc, err := process.NewProcessWithContext(ctx, int32(s.PID))
	if err != nil {
		return false
	}
	argv, err := proc.CmdlineSliceWithContext(ctx)
	if err != nil || !IsServeArgv(argv) {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	resp, err := s.request(ctx, http.MethodGet, "/api/health", nil)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// supervising is pid a running supervisor of a shared server.
func supervising(ctx context.Context, pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := process.NewProcessWithContext(ctx, int32(pid))
	if err != nil {
		return false
	}
	argv, err := proc.CmdlineSliceWithContext(ctx)
	return err == nil && slices.Contains(argv, ServeVerb)
}

// IsServeArgv is an opencode server's argv.
func IsServeArgv(argv []string) bool {
	return len(argv) >= 2 && filepath.Base(argv[0]) == "opencode" && argv[1] == "serve"
}

var listening = regexp.MustCompile(`server listening on (http://\S+)`)

// ServeVerb is the gate-inbox verb that supervises the shared server.
const ServeVerb = "opencode-serve"

// startSupervisor starts `exe opencode-serve`, which runs the server and
// restarts it, and returns its pid.
func startSupervisor(ctx context.Context, home, exe, bin, config string) (int, error) {
	if exe == "" {
		return 0, errors.New("opencode: no gate-inbox binary to supervise the shared server")
	}
	password, err := newPassword()
	if err != nil {
		return 0, err
	}
	logPath := filepath.Join(home, serverLogName)
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		return 0, err
	}
	// The server outlives the launch that starts it and every session on it.
	// A shell forks its supervisor and exits, so the supervisor's parent is
	// init rather than the first session's TUI: the board's memory view,
	// which sums a pane's process tree, does not charge one session for
	// everyone's server, and nothing that ends that pane's tree ends the
	// others' sessions. Its own session keeps the pane's hangup off it, and
	// it runs from a neutral directory.
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", `"$0" "$1" --home "$2" --config "$3" -- "$4" </dev/null >>"$5" 2>&1 & echo $!`,
		exe, ServeVerb, home, config, bin, logPath)
	cmd.Env = serverEnv(os.Environ(), config, password)
	if dir, err := os.UserHomeDir(); err == nil {
		cmd.Dir = dir
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("opencode: start shared server: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("opencode: start shared server: no pid in %q", out)
	}
	return pid, nil
}

// Supervise runs the board's shared server and keeps it running: when it
// dies it starts again on the same address and password, so the TUIs
// attached to it reconnect by themselves, and each session's environment is
// set on it again. It gives up after the server has failed five times in a
// row within a minute of starting, leaving the attached TUIs showing the
// connection lost, which the board reads as errored. It returns when ctx
// ends, taking the server down with it.
func Supervise(ctx context.Context, home, bin, config string) error {
	password := os.Getenv(PasswordEnv)
	if password == "" {
		return errors.New("opencode: supervisor started without a server password")
	}
	logPath := filepath.Join(home, serverLogName)
	statePath := filepath.Join(home, serverStateName)
	port := "0"
	for failures, restart := 0, false; ; restart = true {
		started := time.Now()
		srv, wait, err := runServer(ctx, home, bin, config, password, port)
		if err == nil {
			port = srv.URL[strings.LastIndex(srv.URL, ":")+1:]
			raw, _ := json.Marshal(srv)
			err = writeWhole(statePath, raw, 0o600)
			if err == nil && restart {
				restoreEnvironments(ctx, home, srv)
			}
			if err == nil {
				err = wait()
			}
		}
		if ctx.Err() != nil {
			return nil
		}
		if time.Since(started) < time.Minute {
			failures++
		} else {
			failures = 1
		}
		appendLog(logPath, fmt.Sprintf("gate-inbox: shared server exited (%v); failure %d of 5", err, failures))
		if failures >= 5 {
			return fmt.Errorf("opencode: shared server failed %d times in a row; see %s", failures, logPath)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Duration(failures*failures) * 500 * time.Millisecond):
		}
	}
}

// runServer starts one opencode server on port and waits until it answers.
// wait returns when it exits.
func runServer(ctx context.Context, home, bin, config, password, port string) (Server, func() error, error) {
	logPath := filepath.Join(home, serverLogName)
	offset := int64(0)
	if info, err := os.Stat(logPath); err == nil {
		offset = info.Size()
	}
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return Server{}, nil, err
	}
	defer logFile.Close()
	cmd := exec.CommandContext(ctx, bin, "serve", "--hostname", "127.0.0.1", "--port", port)
	cmd.Env = serverEnv(os.Environ(), config, password)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 10 * time.Second
	if dir, err := os.UserHomeDir(); err == nil {
		cmd.Dir = dir
	}
	if err := cmd.Start(); err != nil {
		return Server{}, nil, err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	wait := func() error { return <-exited }
	deadline := time.After(30 * time.Second)
	for {
		if url := listenURL(logPath, offset); url != "" {
			srv := Server{PID: cmd.Process.Pid, URL: url, Password: password, Supervisor: os.Getpid()}
			if srv.alive(ctx) {
				return srv, wait, nil
			}
		}
		select {
		case err := <-exited:
			return Server{}, nil, fmt.Errorf("exited at start: %v", err)
		case <-deadline:
			_ = cmd.Process.Signal(syscall.SIGTERM)
			return Server{}, nil, errors.New("did not answer within 30s")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// restoreEnvironments sets every bound session's environment on a restarted
// server. A session that server does not have refuses it, which is fine.
func restoreEnvironments(ctx context.Context, home string, srv Server) {
	entries, err := os.ReadDir(BindingsDir(home))
	if err != nil {
		return
	}
	for _, entry := range entries {
		session, ok := strings.CutSuffix(entry.Name(), sessionEnvSuffix)
		if !ok || !bindingName.MatchString(session) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(BindingsDir(home), entry.Name()))
		if err != nil {
			continue
		}
		var env map[string]string
		if json.Unmarshal(raw, &env) != nil {
			continue
		}
		_ = srv.SetEnvironment(ctx, session, env)
	}
}

func appendLog(path, line string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, line)
}

// listenURL is the address a server reported in its log past offset.
func listenURL(logPath string, offset int64) string {
	f, err := os.Open(logPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return ""
	}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if m := listening.FindStringSubmatch(scanner.Text()); m != nil {
			return m[1]
		}
	}
	return ""
}

func serverEnv(environ []string, config, password string) []string {
	env := slices.DeleteFunc(slices.Clone(environ), func(kv string) bool {
		key, _, _ := strings.Cut(kv, "=")
		_, memory := serverMemoryEnv[key]
		return slices.Contains(serverEnvDrop, key) || memory
	})
	env = append(env, "OPENCODE_CONFIG="+config, PasswordEnv+"="+password)
	for key, value := range serverMemoryEnv {
		// An operator's own setting in the launching environment wins.
		if current, ok := lookup(environ, key); ok {
			value = current
		}
		env = append(env, key+"="+value)
	}
	return env
}

func lookup(environ []string, key string) (string, bool) {
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v, true
		}
	}
	return "", false
}

func newPassword() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (s Server) request(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.URL+path, reader)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(serverUser, s.Password)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// Loopback only: an operator's HTTP proxy must not see the password.
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	return client.Do(req)
}

// CreateSession opens a new session in dir, on model when one is named
// (provider/model), and returns its id.
func (s Server) CreateSession(ctx context.Context, dir, model string) (string, error) {
	body := map[string]any{"location": map[string]string{"directory": dir}}
	if model != "" {
		provider, id, ok := strings.Cut(model, "/")
		if !ok || provider == "" || id == "" {
			return "", fmt.Errorf("opencode: model %q is not provider/model", model)
		}
		body["model"] = map[string]string{"providerID": provider, "id": id}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := s.request(ctx, http.MethodPost, "/api/session", body)
	if err != nil {
		return "", fmt.Errorf("opencode: create session: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("opencode: create session: %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.Data.ID == "" {
		return "", fmt.Errorf("opencode: create session returned no id: %s", strings.TrimSpace(string(raw)))
	}
	return created.Data.ID, nil
}

// SetEnvironment replaces the environment the server runs session's shell
// commands with.
func (s Server) SetEnvironment(ctx context.Context, session string, env map[string]string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := s.request(ctx, http.MethodPut, "/api/session/"+session+"/environment", map[string]any{"variables": env})
	if err != nil {
		return fmt.Errorf("opencode: set session environment: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("opencode: set session environment: %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	return nil
}

// BoundCaller is the row the launch bound an opencode session to on the
// shared server, and that row's caller token, if any.
func BoundCaller(home, session string) (row, token string, ok bool) {
	if !bindingName.MatchString(session) {
		return "", "", false
	}
	raw, err := os.ReadFile(filepath.Join(BindingsDir(home), session))
	if err != nil {
		return "", "", false
	}
	row, token, _ = strings.Cut(strings.TrimSpace(string(raw)), " ")
	return row, token, row != "" && token != ""
}

// BoundRow is the row the launch bound an opencode session to on the shared
// server, if any.
func BoundRow(home, session string) (string, bool) {
	row, _, ok := BoundCaller(home, session)
	return row, ok
}

// SharedServerPID is the process of the board's shared server as last
// recorded, or 0.
func SharedServerPID(home string) int {
	srv, err := readServer(filepath.Join(home, serverStateName))
	if err != nil {
		return 0
	}
	return srv.PID
}
