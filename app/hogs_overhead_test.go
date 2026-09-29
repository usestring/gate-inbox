package app

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// TestHogWatchOverheadE2E measures what hog detection costs the board: the
// same board, on the same panes, run for a window with the watcher on at the
// built-in tiers and a window with it off, reading the board process's own
// CPU from /proc. The panes are eight stand-in agents of 41 processes each:
// two also run a busy loop at nice 19 and two a headless Chrome, whose
// renderers are the most expensive processes on a machine to read PSS from.
// The on window also takes a CPU profile through the board's pprof listener.
//
// It holds a scratch board for two windows of GATE_INBOX_E2E_HOGS_WINDOW
// (default 5m), so it is opt-in:
//
//	GATE_INBOX_E2E_HOGS_OVERHEAD=1 [GATE_INBOX_E2E_HOGS_OUT=<dir>] \
//	  go test ./app -run TestHogWatchOverheadE2E -timeout 30m -v
func TestHogWatchOverheadE2E(t *testing.T) {
	if os.Getenv("GATE_INBOX_E2E_HOGS_OVERHEAD") == "" {
		t.Skip("GATE_INBOX_E2E_HOGS_OVERHEAD unset")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("the board polls a tmux server")
	}
	window := 5 * time.Minute
	if v := os.Getenv("GATE_INBOX_E2E_HOGS_WINDOW"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			t.Fatal(err)
		}
		window = d
	}
	out := os.Getenv("GATE_INBOX_E2E_HOGS_OUT")
	if out == "" {
		out = t.TempDir()
	} else if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join("testdata", "hogagent.sh"))
	if err != nil {
		t.Fatal(err)
	}
	bin := buildBoard(t)
	agents := tmuxtest.NewSocket("hogcost")
	driver, err := tmux.NewWithSocket(agents)
	if err != nil {
		t.Fatal(err)
	}
	tmpdir := os.Getenv("TMUX_TMPDIR")
	t.Cleanup(func() { killTestServer(t, tmpdir, agents) })

	type pane struct{ id, load, dir string }
	var panes []pane
	for i := range 8 {
		load := []string{"cpu", "none", "browser", "none"}[i%4]
		p := pane{id: fmt.Sprintf("c0de%04d", 100+i), load: load, dir: t.TempDir()}
		cmd := "HOG_FANOUT=40 HOG_NICE=19 bash " + script + " " + load + " prompt " + p.dir + "; :"
		if err := driver.Create(p.id, p.dir, cmd, nil, 120, 40); err != nil {
			t.Fatalf("create %s: %v", p.id, err)
		}
		t.Cleanup(func() {
			stopAgent(p.dir)
			_ = driver.Kill(p.id)
		})
		panes = append(panes, p)
	}

	measure := func(name, hogs string) float64 {
		t.Helper()
		env := fixtureHome(t, "tmux_socket = \""+agents+"\"\n"+hogTool+hogs)
		home := envValue(env, "GATE_INBOX_HOME")
		db := filepath.Join(home, "state.db")
		skipWelcome(t, db)
		st, err := store.Open(db)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range panes {
			if err := st.CreateSession(store.Session{ID: p.id, Name: "cost " + p.load + " " + p.id, Tool: "hogger", Status: "idle", Cwd: p.dir, CreatedAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
		}
		st.Close()
		port := freePort(t)
		host := tmuxtest.NewSocket("hogcosthost")
		hostPath := hostSocket(t, tmpdir, host)
		cmd := exec.Command("tmux", "-S", hostPath, "new-session", "-d", "-s", "board", "-x", "200", "-y", "50", bin)
		cmd.Env = append(env, "GATE_INBOX_PPROF=127.0.0.1:"+port)
		if msg, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("start the board: %v\n%s", err, msg)
		}
		defer killTestServer(t, tmpdir, host)
		pid := boardPID(t, hostPath, bin)

		// The first minute is startup: the first capture of every pane, the
		// store opening, the first full process scan. Steady state is after.
		time.Sleep(time.Minute)
		before := cpuSeconds(t, pid)
		start := time.Now()
		profile := make(chan []byte, 1)
		go func() {
			resp, err := http.Get("http://127.0.0.1:" + port + "/debug/pprof/profile?seconds=" + strconv.Itoa(int(min(window, 2*time.Minute).Seconds())))
			if err != nil {
				profile <- nil
				return
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			profile <- body
		}()
		time.Sleep(window)
		after := cpuSeconds(t, pid)
		elapsed := time.Since(start).Seconds()
		if body := <-profile; len(body) > 0 {
			_ = os.WriteFile(filepath.Join(out, "board-"+name+".pprof"), body, 0o644)
		}
		own := (after[0] - before[0]) / elapsed * 100
		kids := (after[1] - before[1]) / elapsed * 100
		t.Logf("%s: board %.2f%% of one core (own), %.2f%% with reaped children, over %s", name, own, own+kids, time.Duration(elapsed*float64(time.Second)).Round(time.Second))
		return own
	}

	on := measure("on", "")
	off := measure("off", "\n[hogs]\nenabled = false\n")
	t.Logf("hog watch costs %.2f%% of one core (on %.2f%%, off %.2f%%)", on-off, on, off)
}

// boardPID finds the board process in the host pane: the pane's own process
// when tmux exec'd it, or the child running bin when a shell sits between.
func boardPID(t *testing.T, hostPath, bin string) int {
	t.Helper()
	raw, err := exec.Command("tmux", "-S", hostPath, "display-message", "-p", "-t", "board", "#{pane_pid}").Output()
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		if exe, _ := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe"); exe == bin {
			return pid
		}
		kids, _ := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", pid, pid))
		if f := strings.Fields(string(kids)); len(f) > 0 {
			if child, err := strconv.Atoi(f[0]); err == nil {
				if exe, _ := os.Readlink("/proc/" + f[0] + "/exe"); exe == bin {
					return child
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("no board process under pane %d", pid)
	return 0
}

// cpuSeconds is a process's own user+system seconds and those of the
// children it has reaped -- the tmux commands a board forks.
func cpuSeconds(t *testing.T, pid int) [2]float64 {
	t.Helper()
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		t.Fatalf("the board exited: %v", err)
	}
	fields := strings.Fields(string(raw[strings.LastIndexByte(string(raw), ')')+1:]))
	num := func(field int) float64 {
		v, _ := strconv.ParseFloat(fields[field-3], 64)
		return v / 100
	}
	return [2]float64{num(14) + num(15), num(16) + num(17)}
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
}
