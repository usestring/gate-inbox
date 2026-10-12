//go:build darwin || linux

package codexbind

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/junegunn/go-shellwords"
	"github.com/shirou/gopsutil/v4/process"
	"golang.org/x/sys/unix"
)

type bridge struct {
	lifecycle sync.Mutex
	mu        sync.Mutex
	identity  Identity
	token     string
	upstream  *websocket.Conn
	pending   map[string]chan message
	launches  map[string]string
	lock      *os.File
	lockKey   string
	dir       string
	busy      bool
	connected bool
	tools     map[string]activeTool
}

type activeTool struct{ ID, Thread, Turn, Command string }

type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params map[string]any  `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

func secret() string {
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(token[:])
}

// Run owns one native terminal client. A shared server's response supplies the
// thread id; Unix peer credentials supply the daemon and client processes.
func Run(ctx context.Context, home, server string, argv []string) error {
	if len(argv) == 0 {
		return errors.New("codex-client requires a Codex command after --")
	}
	for _, arg := range argv[1:] {
		if arg == "--remote" || strings.HasPrefix(arg, "--remote=") || arg == "--no-daemon" || arg == "--add-dir" || strings.HasPrefix(arg, "--add-dir=") {
			return fmt.Errorf("codex-client cannot bind %s; use a private --no-daemon launch for this mode", arg)
		}
	}
	identity, err := ownPane(home)
	if err != nil {
		return err
	}
	registry := filepath.Join("/tmp", fmt.Sprintf("gi-codex-bindings-%d", os.Geteuid()))
	if err := os.MkdirAll(registry, 0700); err != nil {
		return err
	}
	if err := privateDirectory(registry); err != nil {
		return err
	}
	// Unix socket paths have a small platform limit; the random directory owns
	// the socket's lifetime and is never reused by another bridge.
	dir, err := os.MkdirTemp("/tmp", "gi-codex-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "client.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return err
	}
	defer listener.Close()
	b := &bridge{identity: identity, pending: map[string]chan message{}, launches: map[string]string{}, dir: registry}
	defer func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.lock != nil {
			b.lock.Close()
			b.lock = nil
		}
	}()
	args := append([]string{"--remote", "unix://" + path}, argv[1:]...)
	cmd := exec.CommandContext(ctx, argv[0], args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	b.identity.Client, err = ProcessPin(cmd.Process.Pid)
	if err != nil {
		return err
	}
	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	httpServer := &http.Server{ReadHeaderTimeout: 5 * time.Second, ConnContext: func(ctx context.Context, c net.Conn) context.Context {
		pid, err := unixPeer(c.(*net.UnixConn))
		if err != nil {
			pid = 0
		}
		return context.WithValue(ctx, peerKey{}, pid)
	}}
	httpServer.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/resolve" || r.URL.Path == "/verify-worker" || r.URL.Path == "/interrupt" {
			b.control(w, r)
			return
		}
		b.proxy(serveCtx, w, r, server, path)
	})
	serveDone := make(chan error, 1)
	go func() { serveDone <- httpServer.Serve(listener) }()
	err = cmd.Wait()
	cancel()
	httpServer.Close()
	<-serveDone
	return err
}

type peerKey struct{}

func ownPane(home string) (Identity, error) {
	socket, _, _ := strings.Cut(os.Getenv("TMUX"), ",")
	pane := os.Getenv("TMUX_PANE")
	if !filepath.IsAbs(socket) || pane == "" {
		return Identity{}, errors.New("codex-client must launch inside its own tmux pane")
	}
	out, err := exec.Command("tmux", "-S", socket, "display-message", "-p", "-t", pane, "#{pane_pid}").Output()
	if err != nil {
		return Identity{}, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || !Descends(os.Getpid(), pid) {
		return Identity{}, errors.New("inherited tmux pane does not own this client launch")
	}
	root, err := ProcessPin(pid)
	if err != nil {
		return Identity{}, err
	}
	self, err := ProcessPin(os.Getpid())
	if err != nil {
		return Identity{}, err
	}
	return Identity{Home: home, Socket: socket, Pane: pane, PaneProcess: root, Bridge: self}, nil
}

func (b *bridge) proxy(ctx context.Context, w http.ResponseWriter, r *http.Request, server, path string) {
	pid, _ := r.Context().Value(peerKey{}).(int)
	b.mu.Lock()
	if b.busy || !b.identity.LiveClient(pid) {
		b.mu.Unlock()
		http.Error(w, "foreign client", 403)
		return
	}
	b.busy = true
	b.mu.Unlock()
	defer b.disconnect()
	var daemon int
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, "unix", server)
		if err != nil {
			return nil, err
		}
		daemon, err = unixPeer(conn.(*net.UnixConn))
		if err != nil {
			conn.Close()
			return nil, err
		}
		return conn, nil
	}}
	defer transport.CloseIdleConnections()
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	up, _, err := websocket.Dial(dialCtx, "ws://localhost", &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}})
	cancel()
	if err != nil {
		http.Error(w, "Codex server unavailable", 502)
		return
	}
	defer up.CloseNow()
	up.SetReadLimit(32 << 20)
	daemonPin, err := ProcessPin(daemon)
	if err != nil || !codexDaemon(daemon) || !sameProcessExecutable(daemon, b.identity.Client.PID) {
		http.Error(w, "unverified Codex daemon", 403)
		return
	}
	down, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer down.CloseNow()
	down.SetReadLimit(32 << 20)
	b.mu.Lock()
	b.identity.Daemon = daemonPin
	b.upstream = up
	b.connected = true
	b.mu.Unlock()
	ctx, cancel = context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- b.readUpstream(ctx, up, down); cancel() }()
	for ctx.Err() == nil {
		kind, data, err := down.Read(ctx)
		if err != nil {
			break
		}
		data, err = b.prepare(data, path)
		if err != nil {
			break
		}
		if err = up.Write(ctx, kind, data); err != nil {
			break
		}
	}
	cancel()
	<-done
}

// A dropped connection takes its capability, pending launches, and observed
// commands with it, so the same verified client can reconnect and bind afresh.
func (b *bridge) disconnect() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.busy, b.connected, b.token = false, false, ""
	clear(b.launches)
	clear(b.tools)
}

func (i Identity) LiveClient(pid int) bool {
	return i.Client.Live() && i.Bridge.Live() && i.PaneProcess.Live() && Descends(pid, i.Client.PID) && Descends(i.Client.PID, i.Bridge.PID)
}

func codexDaemon(pid int) bool {
	p, err := process.NewProcess(int32(pid))
	if err != nil {
		return false
	}
	argv, err := p.CmdlineSlice()
	return err == nil && len(argv) > 1 && filepath.Base(argv[0]) == "codex" && argv[1] == "app-server"
}

func (b *bridge) prepare(data []byte, path string) ([]byte, error) {
	var m message
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m.Method == "initialize" {
		capabilities, _ := m.Params["capabilities"].(map[string]any)
		if capabilities != nil {
			optout, _ := capabilities["optOutNotificationMethods"].([]any)
			kept := []any{}
			for _, method := range optout {
				if method != "item/started" && method != "item/completed" && method != "turn/completed" {
					kept = append(kept, method)
				}
			}
			capabilities["optOutNotificationMethods"] = kept
			return json.Marshal(m)
		}
	}
	if m.Method != "thread/start" && m.Method != "thread/resume" && m.Method != "thread/fork" {
		return data, nil
	}
	// The TUI uses ephemeral threads for structured checks alongside the
	// visible session. Those must never acquire or revoke its pane binding.
	if ephemeral, _ := m.Params["ephemeral"].(bool); ephemeral {
		return data, nil
	}
	if len(m.ID) == 0 || m.Params == nil {
		return nil, errors.New("unidentified thread request")
	}
	b.lifecycle.Lock()
	defer b.lifecycle.Unlock()
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.launches) != 0 {
		return nil, errors.New("ambiguous concurrent thread binding")
	}
	if m.Method == "thread/resume" {
		thread, _ := m.Params["threadId"].(string)
		if thread == "" {
			return nil, errors.New("resume has no thread identity")
		}
		if err := b.claim(thread); err != nil {
			return nil, err
		}
	}
	token := secret()
	b.token = ""
	b.identity.Thread = ""
	b.launches[string(m.ID)] = token
	config, _ := m.Params["config"].(map[string]any)
	if config == nil {
		config = map[string]any{}
	}
	features, _ := config["features"].(map[string]any)
	if features == nil {
		features = map[string]any{}
	}
	features["shell_snapshot"] = false
	features["code_mode_host"] = false
	config["features"] = features
	policy, _ := config["shell_environment_policy"].(map[string]any)
	if policy == nil {
		policy = map[string]any{}
	}
	set, _ := policy["set"].(map[string]any)
	if set == nil {
		set = map[string]any{}
	}
	set[Env] = path + "#" + token
	set["GATE_INBOX_HOME"] = b.identity.Home
	set["GATE_INBOX_SESSION_ID"] = ""
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	set["GATE_INBOX_BIN"] = executable
	policy["set"] = set
	config["shell_environment_policy"] = policy
	m.Params["config"] = config
	return json.Marshal(m)
}

func (b *bridge) readUpstream(ctx context.Context, up, down *websocket.Conn) error {
	for ctx.Err() == nil {
		kind, data, err := up.Read(ctx)
		if err != nil {
			return err
		}
		var m message
		if err = json.Unmarshal(data, &m); err != nil {
			return err
		}
		b.mu.Lock()
		b.observe(m)
		ch := b.pending[string(m.ID)]
		if ch != nil {
			delete(b.pending, string(m.ID))
			b.mu.Unlock()
			ch <- m
			continue
		}
		token := b.launches[string(m.ID)]
		if token != "" {
			delete(b.launches, string(m.ID))
			var result struct {
				Thread struct {
					ID string `json:"id"`
				} `json:"thread"`
			}
			if len(m.Error) != 0 || json.Unmarshal(m.Result, &result) != nil || result.Thread.ID == "" {
				b.mu.Unlock()
				return errors.New("thread binding rejected")
			}
			if err = b.claim(result.Thread.ID); err != nil {
				b.mu.Unlock()
				return err
			}
			b.identity.Thread = result.Thread.ID
			b.token = token
		}
		b.mu.Unlock()
		if err = down.Write(ctx, kind, data); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// Running commands arrive as events; older servers omit them from thread/read
// until completion. Only commands on this live connection can verify a caller.
func (b *bridge) observe(m message) {
	thread, _ := m.Params["threadId"].(string)
	turn, _ := m.Params["turnId"].(string)
	item, _ := m.Params["item"].(map[string]any)
	id, _ := item["id"].(string)
	switch m.Method {
	case "item/started":
		typ, _ := item["type"].(string)
		command, _ := item["command"].(string)
		if typ == "commandExecution" && id != "" && thread != "" && turn != "" {
			if b.tools == nil {
				b.tools = map[string]activeTool{}
			}
			if len(b.tools) >= 4096 {
				b.token = ""
				return
			}
			b.tools[id] = activeTool{id, thread, turn, command}
		}
	case "item/completed":
		delete(b.tools, id)
	case "turn/completed":
		for id, tool := range b.tools {
			if tool.Thread == thread {
				delete(b.tools, id)
			}
		}
	}
}

func (b *bridge) claim(thread string) error {
	key := sha256.Sum256([]byte(fmt.Sprintf("%v:%s", b.identity.Daemon, thread)))
	path := filepath.Join(b.dir, hex.EncodeToString(key[:])+".lock")
	if b.lockKey == path {
		return nil
	}
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), path)
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		return errors.New("thread already bound to another terminal client")
	}
	if b.lock != nil {
		b.lock.Close()
	}
	b.lock = file
	b.lockKey = path
	return nil
}

func (b *bridge) rpc(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	id, _ := json.Marshal("gate-inbox-" + secret())
	ch := make(chan message, 1)
	b.mu.Lock()
	b.pending[string(id)] = ch
	up := b.upstream
	b.mu.Unlock()
	defer func() { b.mu.Lock(); delete(b.pending, string(id)); b.mu.Unlock() }()
	data, err := json.Marshal(message{ID: id, Method: method, Params: params})
	if err != nil {
		return nil, err
	}
	if err = up.Write(ctx, websocket.MessageText, data); err != nil {
		return nil, err
	}
	select {
	case reply := <-ch:
		if len(reply.Error) != 0 {
			return nil, errors.New("Codex lifecycle request refused")
		}
		return reply.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (b *bridge) control(w http.ResponseWriter, r *http.Request) {
	thread, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256))
	if err != nil || r.Method != "POST" {
		http.Error(w, "invalid request", 400)
		return
	}
	b.mu.Lock()
	identity := b.identity
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	reason := ""
	switch {
	case !b.connected:
		reason = "native client connection is closed"
	case b.token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(b.token)) != 1:
		reason = "caller capability is stale or foreign"
	case identity.Thread != string(thread):
		reason = "caller thread is not bound by this bridge"
	case !identity.Live():
		reason = "bound process start identity or ancestry changed"
	}
	b.mu.Unlock()
	if reason != "" {
		http.Error(w, reason, 403)
		return
	}
	pid, _ := r.Context().Value(peerKey{}).(int)
	if r.URL.Path == "/resolve" {
		meta, err := callerMetadata(pid, identity.Daemon.PID)
		if err != nil {
			http.Error(w, err.Error(), 403)
			return
		}
		if meta.Thread != identity.Thread || !strings.HasSuffix(meta.Binding, "#"+token) {
			http.Error(w, "caller does not match its runtime tool launch", 403)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		defer cancel()
		if err = b.verifyTool(ctx, identity, meta); err != nil {
			http.Error(w, "runtime did not verify the calling tool", 403)
			return
		}
	} else if !sameExecutable(pid) {
		http.Error(w, "unverified lifecycle worker", 403)
		return
	}
	if r.URL.Path == "/interrupt" {
		pid, _ := r.Context().Value(peerKey{}).(int)
		if !sameExecutable(pid) {
			http.Error(w, "unverified lifecycle worker", 403)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		defer cancel()
		if err = b.interrupt(ctx, identity, token); err != nil {
			http.Error(w, "lifecycle refused", 409)
			return
		}
	}
	b.mu.Lock()
	valid := b.connected && b.identity == identity && b.token == token
	b.mu.Unlock()
	if !valid {
		http.Error(w, "binding changed during verification", 403)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(identity)
}

func (b *bridge) verifyTool(ctx context.Context, identity Identity, meta metadata) error {
	for {
		ok, err := b.toolMatches(ctx, identity, meta)
		if err != nil || ok {
			return err
		}
		select {
		case <-ctx.Done():
			return errors.New("calling tool is not active in the bound thread")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (b *bridge) toolMatches(ctx context.Context, identity Identity, meta metadata) (bool, error) {
	data, err := b.rpc(ctx, "thread/read", map[string]any{"threadId": identity.Thread, "includeTurns": true})
	if err != nil {
		return false, err
	}
	var result struct {
		Thread struct {
			ID    string `json:"id"`
			Turns []struct {
				ID     string `json:"id"`
				Status string `json:"status"`
				Items  []struct {
					ID      string `json:"id"`
					Type    string `json:"type"`
					Status  string `json:"status"`
					Command string `json:"command"`
				} `json:"items"`
			} `json:"turns"`
		} `json:"thread"`
	}
	if err = json.Unmarshal(data, &result); err != nil || result.Thread.ID != identity.Thread {
		return false, errors.New("runtime thread changed")
	}
	activeTurns := map[string]bool{}
	for _, turn := range result.Thread.Turns {
		if turn.Status == "inProgress" {
			activeTurns[turn.ID] = true
		}
	}
	b.mu.Lock()
	tools := make(map[string]activeTool, len(b.tools))
	for id, tool := range b.tools {
		if tool.Thread == identity.Thread && activeTurns[tool.Turn] {
			tools[id] = tool
		}
	}
	b.mu.Unlock()
	for _, turn := range result.Thread.Turns {
		if turn.Status == "inProgress" {
			for _, item := range turn.Items {
				if item.Type != "commandExecution" || item.Status != "inProgress" {
					continue
				}
				tools[item.ID] = activeTool{item.ID, identity.Thread, turn.ID, item.Command}
			}
		}
	}
	matches := 0
	var matchedLaunch toolLaunch
	for _, tool := range tools {
		matchesCall := meta.Call != "" && tool.ID == meta.Call
		if meta.Call == "" {
			args, err := (&shellwords.Parser{}).Parse(tool.Command)
			if err != nil || len(args) == 0 {
				continue
			}
			for _, launch := range meta.Launches {
				if slices.Equal(args, launch.Args) && launch.Process.Live() {
					matchesCall = true
					matchedLaunch = launch
					break
				}
			}
		}
		if matchesCall {
			matches++
		}
	}
	if matches == 0 {
		return false, nil
	}
	if matches != 1 || !meta.Process.Live() || (meta.Call == "" && !uniqueLaunch(matchedLaunch, identity.Daemon.PID)) {
		return false, errors.New("calling tool is ambiguous or its process changed")
	}
	b.mu.Lock()
	valid := b.connected && b.identity == identity
	b.mu.Unlock()
	if !valid {
		return false, errors.New("binding changed during tool verification")
	}
	return true, nil
}

func (b *bridge) interrupt(ctx context.Context, identity Identity, token string) error {
	b.lifecycle.Lock()
	defer b.lifecycle.Unlock()
	data, err := b.rpc(ctx, "thread/read", map[string]any{"threadId": identity.Thread, "includeTurns": true})
	if err != nil {
		return err
	}
	var result struct {
		Thread struct {
			ID     string `json:"id"`
			Status struct {
				Type string `json:"type"`
			} `json:"status"`
			Turns []struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"turns"`
		} `json:"thread"`
	}
	if err = json.Unmarshal(data, &result); err != nil || result.Thread.ID != identity.Thread {
		return errors.New("runtime thread changed")
	}
	var active []string
	for _, turn := range result.Thread.Turns {
		if turn.Status == "inProgress" {
			active = append(active, turn.ID)
		}
	}
	if len(active) > 1 {
		return errors.New("ambiguous active turn")
	}
	if result.Thread.Status.Type != "idle" && (result.Thread.Status.Type != "active" || len(active) != 1) {
		return errors.New("runtime did not establish one active turn or an idle thread")
	}
	b.mu.Lock()
	valid := b.connected && b.identity == identity && b.token == token && identity.Live()
	b.mu.Unlock()
	if !valid {
		return errors.New("binding changed during lifecycle verification")
	}
	if len(active) == 1 {
		_, err = b.rpc(ctx, "turn/interrupt", map[string]any{"threadId": identity.Thread, "turnId": active[0]})
	}
	return err
}
