//go:build darwin || linux

package codexbind

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

const Env = "GATE_INBOX_CALLER_BINDING"

type Pin struct {
	PID     int   `json:"pid"`
	Started int64 `json:"started"`
}

func ProcessPin(pid int) (Pin, error) {
	p, err := process.NewProcess(int32(pid))
	if err != nil {
		return Pin{}, err
	}
	uids, err := p.Uids()
	if err != nil || len(uids) == 0 || uids[0] != uint32(os.Geteuid()) {
		return Pin{}, errors.New("process owner could not be verified")
	}
	started, err := p.CreateTime()
	if err != nil || started <= 0 {
		return Pin{}, errors.New("process start identity unavailable")
	}
	return Pin{pid, started}, nil
}

func (p Pin) Live() bool {
	current, err := ProcessPin(p.PID)
	return err == nil && p == current
}

func Descends(pid, ancestor int) bool {
	for range 32 {
		if pid == ancestor {
			return true
		}
		if pid <= 1 {
			return false
		}
		p, err := process.NewProcess(int32(pid))
		if err != nil {
			return false
		}
		parent, err := p.Ppid()
		if err != nil {
			return false
		}
		pid = int(parent)
	}
	return false
}

type Identity struct {
	Home        string `json:"home"`
	Socket      string `json:"socket"`
	Pane        string `json:"pane"`
	PaneProcess Pin    `json:"pane_process"`
	Client      Pin    `json:"client"`
	Bridge      Pin    `json:"bridge"`
	Daemon      Pin    `json:"daemon"`
	Thread      string `json:"thread"`
}

func (i Identity) Live() bool {
	return i.PaneProcess.Live() && i.Client.Live() && i.Bridge.Live() && i.Daemon.Live() &&
		Descends(i.Client.PID, i.Bridge.PID) && Descends(i.Bridge.PID, i.PaneProcess.PID)
}

func unixPeer(conn *net.UnixConn) (int, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, err
	}
	var pid int
	var peerErr error
	err = raw.Control(func(fd uintptr) { pid, peerErr = peerPID(int(fd)) })
	if err != nil {
		return 0, err
	}
	return pid, peerErr
}

func sameExecutable(pid int) bool {
	p, err := process.NewProcess(int32(pid))
	if err != nil {
		return false
	}
	exe, err := p.Exe()
	if err != nil {
		return false
	}
	self, err := os.Executable()
	if err != nil {
		return false
	}
	a, err := os.Stat(exe)
	if err != nil {
		return false
	}
	b, err := os.Stat(self)
	return err == nil && os.SameFile(a, b)
}

func sameProcessExecutable(first, second int) bool {
	paths := make([]string, 2)
	for n, pid := range []int{first, second} {
		p, err := process.NewProcess(int32(pid))
		if err != nil {
			return false
		}
		paths[n], err = p.Exe()
		if err != nil {
			return false
		}
	}
	a, err := os.Stat(paths[0])
	if err != nil {
		return false
	}
	b, err := os.Stat(paths[1])
	return err == nil && os.SameFile(a, b)
}

func privateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("caller binding directory is not private")
	}
	return nil
}

// Resolve asks the live bridge, never a conversation index or inherited pane
// address. The capability was inserted in this thread's runtime configuration.
func Resolve(ctx context.Context, binding, thread string, worker bool) (Identity, error) {
	action := "resolve"
	if worker {
		action = "verify-worker"
	}
	return control(ctx, binding, thread, action, worker)
}

func Interrupt(ctx context.Context, binding, thread string) (Identity, error) {
	return control(ctx, binding, thread, "interrupt", true)
}

func control(ctx context.Context, binding, thread, action string, worker bool) (Identity, error) {
	path, token, ok := strings.Cut(binding, "#")
	if !ok || len(token) != 64 || thread == "" || !filepath.IsAbs(path) {
		return Identity{}, errors.New("invalid Codex caller capability")
	}
	if err := privateDirectory(filepath.Dir(path)); err != nil {
		return Identity{}, err
	}
	var peer int
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
		if err != nil {
			return nil, err
		}
		pid, err := unixPeer(conn.(*net.UnixConn))
		if err != nil || !sameExecutable(pid) {
			conn.Close()
			return nil, errors.New("caller bridge executable could not be verified")
		}
		peer = pid
		return conn, nil
	}}
	defer transport.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, "POST", "http://bridge/"+action, strings.NewReader(thread))
	if err != nil {
		return Identity{}, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		return Identity{}, fmt.Errorf("caller bridge unavailable: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 256))
		return Identity{}, fmt.Errorf("caller bridge refused identity: %s", strings.TrimSpace(string(body)))
	}
	var identity Identity
	if err := json.NewDecoder(http.MaxBytesReader(nil, response.Body, 8192)).Decode(&identity); err != nil {
		return Identity{}, err
	}
	if identity.Bridge.PID != peer || identity.Thread != thread || !identity.Live() {
		return Identity{}, errors.New("caller binding process identity changed")
	}
	if !worker && !Descends(os.Getpid(), identity.Daemon.PID) {
		return Identity{}, errors.New("caller is not hosted by the bound Codex daemon")
	}
	return identity, nil
}

func (i Identity) PanePin() string {
	return strconv.Itoa(i.PaneProcess.PID) + ":" + strconv.FormatInt(i.PaneProcess.Started, 10)
}

type metadata struct {
	Thread, Call, Binding string
	Process               Pin
	Launches              []toolLaunch
}

type toolLaunch struct {
	Process Pin
	Args    []string
}

// Shells can hide their launch environment after startup. Retain every pinned
// ancestor command so runtime command evidence can still establish the caller.
func callerMetadata(pid, daemon int) (metadata, error) {
	var found metadata
	var launches []toolLaunch
	for range 32 {
		if pid == daemon {
			if found.Thread == "" || found.Binding == "" {
				return metadata{}, errors.New("runtime tool launch metadata unavailable")
			}
			found.Launches = launches
			return found, nil
		}
		if pid <= 1 {
			return metadata{}, errors.New("caller left its bound daemon ancestry")
		}
		p, err := process.NewProcess(int32(pid))
		if err != nil {
			return metadata{}, err
		}
		env, err := initialEnvironment(pid)
		if err != nil {
			return metadata{}, err
		}
		var current metadata
		for _, entry := range env {
			key, value, _ := strings.Cut(entry, "=")
			switch key {
			case "CODEX_THREAD_ID":
				current.Thread = value
			case "CODEX_TOOL_CALL_ID":
				current.Call = value
			case Env:
				current.Binding = value
			}
		}
		pin, err := ProcessPin(pid)
		if err != nil {
			return metadata{}, err
		}
		args, err := p.CmdlineSlice()
		if err != nil {
			return metadata{}, err
		}
		launches = append(launches, toolLaunch{pin, args})
		if current.Thread != "" && current.Binding != "" {
			current.Process = pin
			found = current
		}
		parent, err := p.Ppid()
		if err != nil {
			return metadata{}, err
		}
		pid = int(parent)
	}
	return metadata{}, errors.New("tool launch ancestry exceeded verification bound")
}

// A runtime command without a tool-call ID must match one kernel process in
// the daemon tree. Identical parallel commands are ambiguous and must refuse.
func uniqueLaunch(launch toolLaunch, daemon int) bool {
	ps, err := process.Processes()
	if err != nil || len(ps) > 8192 || !launch.Process.Live() {
		return false
	}
	parents := map[int]int{}
	for _, p := range ps {
		parent, err := p.Ppid()
		if err != nil {
			uids, uidErr := p.Uids()
			live, _ := p.IsRunning()
			if live && (uidErr != nil || len(uids) == 0 || uids[0] == uint32(os.Geteuid())) {
				return false
			}
			continue
		}
		parents[int(p.Pid)] = int(parent)
	}
	matched := 0
	for _, p := range ps {
		pid := int(p.Pid)
		ancestor := pid
		for range 32 {
			if ancestor == daemon || ancestor <= 1 {
				break
			}
			parent, ok := parents[ancestor]
			if !ok {
				ancestor = 0
				break
			}
			ancestor = parent
		}
		if pid == daemon || ancestor != daemon {
			continue
		}
		args, err := p.CmdlineSlice()
		if err != nil {
			return false
		}
		if slices.Equal(args, launch.Args) {
			pin, err := ProcessPin(pid)
			if err != nil || pin != launch.Process {
				return false
			}
			matched++
		}
	}
	return matched == 1 && launch.Process.Live()
}
