// Package mcpshared runs one Gate Inbox MCP server for every session of a
// config dir, instead of one process per session.
//
// Every launched session used to start its own `gate-inbox mcp`: a board of
// 180 sessions ran 180 copies of the same server, nearly all idle, each with
// its own Go heap and tool schemas. One HTTP server on loopback serves them
// all. Each request names its session in a header the session's CLI fills in
// from its own environment and proves it with that session's caller token
// (mcpserver.CallerHeader), so the server needs no state per session.
//
// This package owns where that server is and that only one runs: the URL it
// serves at, recorded in the config dir for launches to read; the lock it
// holds while serving; and starting it when a launch finds none answering.
// mcpserver.ServeShared is the server itself.
package mcpshared

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/envname"
	"github.com/usestring/gate-inbox/internal/mcpreg"
	"github.com/usestring/gate-inbox/internal/parentseal"
)

const (
	// Verb is the subcommand that runs the shared server.
	Verb = "mcp-serve"
	// urlFile records the URL the server answers at; a launch writes it into
	// the session's MCP config.
	urlFile = "mcp-serve.url"
	// lockFile is held, flock'd, by the one server running for a config dir.
	lockFile = "mcp-serve.lock"
	// HealthPath answers 200 without a caller, so a launch can tell a
	// serving server from a stale URL.
	HealthPath = "/healthz"
	// CallerHeader and CallerTokenHeader name and prove a request's
	// caller; a session's CLI expands them from $GATE_INBOX_SESSION_ID and
	// $GATE_INBOX_SESSION_TOKEN, so the server needs nothing per session.
	CallerHeader      = "X-Gate-Inbox-Session"
	CallerTokenHeader = "X-Gate-Inbox-Token"
	// MCPPath is where the MCP endpoint for a tool profile is served:
	// MCPPath + profile ("all", "core" or "admin").
	MCPPath = "/mcp/"
	// listenerFDEnv and lockFDEnv hand the listening socket and the lock
	// across an upgrade's exec, so the new binary takes over the same port
	// with no moment where nothing answers.
	listenerFDEnv = "GATE_INBOX_MCP_SERVE_FD"
	lockFDEnv     = "GATE_INBOX_MCP_SERVE_LOCK_FD"
)

// ErrRunning is Listen finding another server already serving the config dir.
var ErrRunning = errors.New("a shared MCP server is already running for this config dir")

// URL is the address recorded by the server last to serve configDir, or
// empty when none has. It may be stale: Healthy says whether it answers.
func URL(configDir string) string {
	data, err := os.ReadFile(filepath.Join(configDir, urlFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// Healthy reports whether a server answers at url.
func Healthy(url string) bool {
	if url == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+HealthPath, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// Ensure is the URL of a server serving configDir, starting exe's when none
// answers. The server is started detached, in a session of its own, so it
// outlives the launch that started it; the lock makes a race between two
// launches start one.
func Ensure(configDir, exe string) (string, error) {
	if url := URL(configDir); Healthy(url) {
		return url, nil
	}
	cmd := exec.Command(exe, Verb)
	cmd.Env = serverEnv(os.Environ(), configDir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	deadline := time.After(5 * time.Second)
	for {
		if url := URL(configDir); Healthy(url) {
			return url, nil
		}
		select {
		case err := <-exited:
			// The lock's holder exiting early is another launch's server
			// winning the race; anything else is a server that cannot start.
			if url := URL(configDir); Healthy(url) {
				return url, nil
			}
			return "", fmt.Errorf("shared MCP server exited: %v", err)
		case <-deadline:
			return "", errors.New("shared MCP server did not answer within 5s")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// serverEnv is the launching process's environment for the server, without
// the variables that make a process one session: the server acts as no
// session of its own, only as each request's caller.
func serverEnv(env []string, configDir string) []string {
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case envname.SessionID, envname.SessionToken, envname.StatusFile, envname.ExitFile, envname.AgentPID,
			config.HomeEnv, "TMUX", "TMUX_PANE", listenerFDEnv, lockFDEnv:
			continue
		}
		out = append(out, entry)
	}
	return append(out, config.HomeEnv+"="+configDir)
}

// Listen takes the lock for configDir and a loopback listener, recording
// its URL. A server exec'd by an upgrade inherits both instead. The port is
// the recorded one when it is free, so the URL sessions were given keeps
// working across a restart; another is taken only when it is not.
func Listen(configDir string) (net.Listener, *os.File, error) {
	if listener, lock, ok, err := inherited(); ok || err != nil {
		return listener, lock, err
	}
	lock, err := os.OpenFile(filepath.Join(configDir, lockFile), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, nil, ErrRunning
		}
		return nil, nil, err
	}
	listener, err := listenRecorded(configDir)
	if err != nil {
		lock.Close()
		return nil, nil, err
	}
	if err := record(configDir, listener.Addr()); err != nil {
		listener.Close()
		lock.Close()
		return nil, nil, err
	}
	return listener, lock, nil
}

func listenRecorded(configDir string) (net.Listener, error) {
	if recorded := URL(configDir); recorded != "" {
		host := strings.TrimPrefix(recorded, "http://")
		if listener, err := net.Listen("tcp", host); err == nil {
			return listener, nil
		}
	}
	return net.Listen("tcp", "127.0.0.1:0")
}

func record(configDir string, addr net.Addr) error {
	path := filepath.Join(configDir, urlFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte("http://"+addr.String()+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// inherited is the listener and lock an upgrading server exec'd this one
// with, if it did.
func inherited() (net.Listener, *os.File, bool, error) {
	listenerFD, lockFD := os.Getenv(listenerFDEnv), os.Getenv(lockFDEnv)
	if listenerFD == "" || lockFD == "" {
		return nil, nil, false, nil
	}
	os.Unsetenv(listenerFDEnv)
	os.Unsetenv(lockFDEnv)
	lfd, err1 := strconv.Atoi(listenerFD)
	kfd, err2 := strconv.Atoi(lockFD)
	if err := errors.Join(err1, err2); err != nil {
		return nil, nil, true, err
	}
	for _, fd := range []int{lfd, kfd} {
		syscall.CloseOnExec(fd)
	}
	file := os.NewFile(uintptr(lfd), "mcp-serve listener")
	listener, err := net.FileListener(file)
	file.Close()
	if err != nil {
		return nil, nil, true, err
	}
	return listener, os.NewFile(uintptr(kfd), "mcp-serve lock"), true, nil
}

// Reexec replaces this process with exe, serving from the same listener
// under the same lock: how a running server moves onto an upgraded binary.
// It returns only on failure.
func Reexec(exe string, listener net.Listener, lock *os.File) error {
	tcp, ok := listener.(*net.TCPListener)
	if !ok {
		return errors.New("listener is not TCP")
	}
	file, err := tcp.File()
	if err != nil {
		return err
	}
	for _, fd := range []uintptr{file.Fd(), lock.Fd()} {
		if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, fd, syscall.F_SETFD, 0); errno != 0 {
			return errno
		}
	}
	env := append(os.Environ(),
		listenerFDEnv+"="+strconv.Itoa(int(file.Fd())),
		lockFDEnv+"="+strconv.Itoa(int(lock.Fd())))
	return syscall.Exec(exe, []string{exe, Verb}, env)
}

// BinaryStamp changes when the binary at path is replaced.
func BinaryStamp(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return info.ModTime().String() + "/" + strconv.FormatInt(info.Size(), 10)
}

// ClaudeConfigPath is where a launch writes session id's MCP config for the
// shared server.
//
// The config names the session and its caller token outright rather than
// leaving claude to expand them from its environment: Claude Code documents
// that expansion for .mcp.json, not for --mcp-config, and reads a variable
// whose name looks like a credential as empty in a remote server's headers.
// Holding a token, it lives beside the keys, where a managed session's
// sandbox and Read tool are denied; claude itself reads it at startup.
func ClaudeConfigPath(configDir, id string) string {
	return filepath.Join(parentseal.KeyDir(configDir), "mcp-claude-"+id+".json")
}

// WriteClaudeConfig writes the config at path pointing a claude at the
// shared server at url, for profile's tools, as session id with token.
func WriteClaudeConfig(path, url, profile, id, token string) error {
	if profile == "" {
		profile = "all"
	}
	content, err := json.MarshalIndent(map[string]any{
		"mcpServers": map[string]any{
			mcpreg.ServerName: map[string]any{
				"type": "http",
				"url":  url + MCPPath + profile,
				"headers": map[string]string{
					CallerHeader:      id,
					CallerTokenHeader: token,
				},
			},
		},
	}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mcp-claude-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
