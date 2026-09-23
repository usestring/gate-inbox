// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package tmux

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// tmuxOn builds a raw tmux command aimed at any socket, for reaching the
// stand-in "user's own" server these tests adopt panes from.
func tmuxOn(socket string, args ...string) *exec.Cmd {
	// These helpers kill the server they run on, so one that resolved to
	// tmux's default would end whatever the operator has running there.
	if !OwnsSocket(socket) {
		panic("test tmux command aimed at tmux's default server: " + socket)
	}
	return exec.Command("tmux", append([]string{"-L", socket}, args...)...)
}

func uniqueID(prefix string) string {
	return prefix + strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
}

// foreignServer stands in for a tmux server the manager does not own: a
// session someone else created, on its own socket, split into two panes so a
// test can tell a pane-scoped command from a window-scoped one. The panes run
// `cat`, which echoes whatever is typed into them, and sit in different
// directories so pane_current_path distinguishes them too.
func foreignServer(t *testing.T) (string, []string) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	// A socket of its own per call. One shared name meant the kill-server in
	// one test's cleanup raced the new-session in the next one's setup, which
	// surfaced as "foreign new-session: server exited unexpectedly" -- a CI
	// failure with nothing to do with whatever change was under test. Nothing
	// accumulates from the fresh names: the cleanup kills the server, and
	// tmuxtest sweeps whatever a panicking run leaves.
	socket := tmuxtest.NewSocket("foreign")
	if out, err := tmuxOn(socket, "new-session", "-d", "-s", "user", "-c", "/", "-x", "80", "-y", "24", "cat").CombinedOutput(); err != nil {
		t.Fatalf("foreign new-session: %v: %s", err, out)
	}
	t.Cleanup(func() {
		tmuxtest.KillServer(socket)
		tmuxtest.ReapSocket(socket)
	})
	if out, err := tmuxOn(socket, "split-window", "-t", "user", "-c", "/tmp", "cat").CombinedOutput(); err != nil {
		t.Fatalf("foreign split-window: %v: %s", err, out)
	}
	out, err := tmuxOn(socket, "list-panes", "-t", "user", "-F", "#{pane_id}").CombinedOutput()
	if err != nil {
		t.Fatalf("foreign list-panes: %v: %s", err, out)
	}
	panes := strings.Fields(string(out))
	if len(panes) != 2 {
		t.Fatalf("foreign server should have 2 panes, got %q", panes)
	}
	return socket, panes
}

func capturePaneOn(t *testing.T, socket, pane string) string {
	t.Helper()
	out, err := tmuxOn(socket, "capture-pane", "-p", "-t", pane).CombinedOutput()
	if err != nil {
		t.Fatalf("capture %s on %s: %v: %s", pane, socket, err, out)
	}
	return string(out)
}

func adopt(t *testing.T, driver *Driver, id, socket, pane string) {
	t.Helper()
	if err := driver.Adopt(id, Target{Socket: socket, Name: pane}); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
}

// Text sent to an adopted id has to land in that one pane on that one server:
// not on the manager's socket, and not in the pane's neighbour.
func TestAdoptedSessionReachesTheForeignPane(t *testing.T) {
	driver := requireTmux(t)
	socket, panes := foreignServer(t)
	id := uniqueID("reach")
	adopt(t, driver, id, socket, panes[1])

	const marker = "hello-adopted-pane"
	if err := driver.SendText(id, marker); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var pane string
	for time.Now().Before(deadline) {
		pane = capturePaneOn(t, socket, panes[1])
		if strings.Contains(pane, marker) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(pane, marker) {
		t.Fatalf("adopted pane never received the text; pane:\n%s", pane)
	}
	if neighbour := capturePaneOn(t, socket, panes[0]); strings.Contains(neighbour, marker) {
		t.Fatalf("the neighbouring pane received the text too:\n%s", neighbour)
	}

	captured, err := driver.CapturePane(id)
	if err != nil {
		t.Fatalf("CapturePane: %v", err)
	}
	if !strings.Contains(captured, marker) {
		t.Fatalf("CapturePane read a different pane:\n%s", captured)
	}
	if err := tmuxCmd("has-session", "-t", sessionName(id)).Run(); err == nil {
		t.Fatalf("adopting %s should not create %s on the manager socket", id, sessionName(id))
	}
}

// Adoption is per id: a registry entry for one session must leave every other
// session resolving to gi_<id> on the manager's own socket exactly as before.
func TestManagedSessionIsUnaffectedByAnAdoption(t *testing.T) {
	driver := requireTmux(t)
	socket, panes := foreignServer(t)
	adopt(t, driver, uniqueID("other"), socket, panes[1])

	id := uniqueID("managed")
	if err := driver.Create(id, "/tmp", "printf 'managed-pane-marker'", nil, 0, 0); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { driver.Kill(id) })

	if got, want := driver.TargetFor(id), (Target{Socket: testSocket, Name: sessionName(id)}); got != want {
		t.Fatalf("TargetFor(managed) = %+v, want %+v", got, want)
	}
	if !driver.Exists(id) {
		t.Fatal("managed session should exist after Create")
	}
	deadline := time.Now().Add(2 * time.Second)
	var captured string
	for time.Now().Before(deadline) {
		var err error
		if captured, err = driver.CapturePane(id); err != nil {
			t.Fatalf("CapturePane: %v", err)
		}
		if strings.Contains(captured, "managed-pane-marker") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(captured, "managed-pane-marker") {
		t.Fatalf("managed capture missing marker:\n%s", captured)
	}
	if err := driver.SetLabel(id, "still mine"); err != nil {
		t.Fatalf("SetLabel on a managed session: %v", err)
	}
	if err := driver.RefreshChrome(id); err != nil {
		t.Fatalf("RefreshChrome on a managed session: %v", err)
	}
	if err := driver.Kill(id); err != nil {
		t.Fatalf("Kill on a managed session: %v", err)
	}
	if driver.Exists(id) {
		t.Fatal("managed session should be gone after Kill")
	}
}

// A pane is not a session, and the display that does answer for one reports a
// pane that has closed as an empty line with a zero exit status. Exists has to
// read the pane id back, or every adopted session looks alive forever.
func TestExistsAnswersForAdoptedPanes(t *testing.T) {
	driver := requireTmux(t)
	socket, panes := foreignServer(t)

	id := uniqueID("exists")
	adopt(t, driver, id, socket, panes[1])
	if !driver.Exists(id) {
		t.Fatal("a live adopted pane should exist")
	}

	missing := uniqueID("missing")
	adopt(t, driver, missing, socket, "%999")
	if driver.Exists(missing) {
		t.Fatal("a pane id that was never opened should not exist")
	}

	if out, err := tmuxOn(socket, "kill-pane", "-t", panes[1]).CombinedOutput(); err != nil {
		t.Fatalf("kill the adopted pane: %v: %s", err, out)
	}
	if driver.Exists(id) {
		t.Fatal("a closed adopted pane should not exist")
	}
}

// The refusals are the safety core: the manager must not destroy, recreate or
// restyle a pane it did not open. Reading one is not on the list -- a control
// client attaches with ignore-size and never touches the window it watches,
// which TestControlClientLeavesAWatchedWindowAlone holds it to.
func TestAdoptedSessionRefusesOwnerOnlyOperations(t *testing.T) {
	driver := requireTmux(t)
	socket, panes := foreignServer(t)
	id := uniqueID("refuse")
	adopt(t, driver, id, socket, panes[1])

	refusals := []struct {
		name string
		run  func() error
	}{
		{"Kill", func() error { return driver.Kill(id) }},
		{"Create", func() error { return driver.Create(id, "/tmp", "", nil, 0, 0) }},
		{"RefreshChrome", func() error { return driver.RefreshChrome(id) }},
		{"SetLabel", func() error { return driver.SetLabel(id, "mine now") }},
		{"installSessionUX", func() error { return driver.installSessionUX(id) }},
		{"styleStatusBar", func() error { return driver.styleStatusBar(id) }},
	}
	for _, refusal := range refusals {
		err := refusal.run()
		if err == nil {
			t.Fatalf("%s on an adopted session should refuse, got nil", refusal.name)
		}
		if !errors.Is(err, ErrAdopted) {
			t.Fatalf("%s error = %v, want one wrapping ErrAdopted", refusal.name, err)
		}
		if !strings.Contains(err.Error(), panes[1]) {
			t.Fatalf("%s error = %v, want it to name the pane it protected", refusal.name, err)
		}
	}

	if !driver.Exists(id) {
		t.Fatal("the adopted pane should have survived every refusal")
	}
	if err := tmuxCmd("has-session", "-t", sessionName(id)).Run(); err == nil {
		t.Fatalf("a refused Create should not leave %s behind", sessionName(id))
	}
	if err := tmuxOn(socket, "has-session", "-t", sessionName(id)).Run(); err == nil {
		t.Fatalf("a refused Create should not leave %s on the foreign server", sessionName(id))
	}
}

// bind-key -n and the theme options are server-wide. Sent to a user's own
// server they would seize C-q, C-\, C-r and M-o across every session that
// person has open, so they stay on the manager's socket whatever is adopted.
func TestServerGlobalWritesStayOnTheManagerSocket(t *testing.T) {
	driver := requireTmux(t)
	t.Cleanup(func() { clearPaneTheme(t) })
	socket, panes := foreignServer(t)
	id := uniqueID("global")
	adopt(t, driver, id, socket, panes[1])
	if err := driver.SendKeys(id, "Space"); err != nil {
		t.Fatalf("SendKeys to the adopted pane: %v", err)
	}

	if err := driver.EnsureBindings(); err != nil {
		t.Fatalf("EnsureBindings: %v", err)
	}
	driver.PublishPaneTheme(PaneTheme{Background: "#123456", ColorFgBg: "15;0"})
	if err := driver.PushPaneTheme(); err != nil {
		t.Fatalf("PushPaneTheme: %v", err)
	}

	out, err := tmuxOn(socket, "list-keys", "-T", "root").CombinedOutput()
	if err != nil {
		t.Fatalf("foreign list-keys: %v: %s", err, out)
	}
	if strings.Contains(string(out), requestOption) {
		t.Fatalf("the foreign server carries the manager's request binding:\n%s", out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		for _, seized := range []string{"C-q", "C-r", "M-o"} {
			if fields[3] == seized {
				t.Fatalf("the foreign server had %s rebound: %q", seized, line)
			}
		}
	}
	style, err := tmuxOn(socket, "show-options", "-gv", "window-style").CombinedOutput()
	if err != nil {
		t.Fatalf("foreign show-options: %v: %s", err, style)
	}
	if strings.Contains(string(style), "#123456") {
		t.Fatalf("the foreign server took the manager's pane theme: %q", style)
	}

	if got := globalWindowStyle(t); got != "bg=#123456" {
		t.Fatalf("manager window-style = %q, want the pushed theme", got)
	}
	managerKeys, err := tmuxCmd("list-keys", "-T", "root").CombinedOutput()
	if err != nil {
		t.Fatalf("manager list-keys: %v: %s", err, managerKeys)
	}
	if !strings.Contains(string(managerKeys), requestOption) {
		t.Fatalf("the manager socket should carry the bindings:\n%s", managerKeys)
	}
}

// list-panes takes a pane id but answers for that pane's whole window, so an
// adopted pane in a split would otherwise report its neighbour's pid and cwd.
func TestPanePropertiesFollowTheAdoptedPane(t *testing.T) {
	driver := requireTmux(t)
	socket, panes := foreignServer(t)
	id := uniqueID("props")
	adopt(t, driver, id, socket, panes[1])

	pid, err := driver.PanePID(id)
	if err != nil {
		t.Fatalf("PanePID: %v", err)
	}
	if got := paneField(t, socket, panes[1], "#{pane_pid}"); strconv.Itoa(pid) != got {
		t.Fatalf("PanePID = %d, want the adopted pane's pid %s", pid, got)
	}
	if got := paneField(t, socket, panes[0], "#{pane_pid}"); strconv.Itoa(pid) == got {
		t.Fatalf("PanePID = %d, which is the neighbouring pane", pid)
	}

	path, err := driver.PaneCurrentPath(id)
	if err != nil {
		t.Fatalf("PaneCurrentPath: %v", err)
	}
	if want := paneField(t, socket, panes[1], "#{pane_current_path}"); path != want {
		t.Fatalf("PaneCurrentPath = %q, want %q", path, want)
	}
}

func paneField(t *testing.T, socket, pane, format string) string {
	t.Helper()
	out, err := tmuxOn(socket, "display-message", "-p", "-t", pane, format).CombinedOutput()
	if err != nil {
		t.Fatalf("display %s for %s: %v: %s", format, pane, err, out)
	}
	return strings.TrimSpace(string(out))
}

// Panes is the poll loop's liveness map. An adopted pane carries no gi_ name
// and sits on another server, so it has to be listed separately or every
// adopted session reads as dead.
func TestPanesCoversAdoptedSessions(t *testing.T) {
	driver := requireTmux(t)
	socket, panes := foreignServer(t)
	adoptedID := uniqueID("live")
	adopt(t, driver, adoptedID, socket, panes[1])

	managedID := uniqueID("both")
	if err := driver.Create(managedID, "/tmp", "", nil, 0, 0); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { driver.Kill(managedID) })

	live, err := driver.Panes()
	if err != nil {
		t.Fatalf("Panes: %v", err)
	}
	if live[managedID] <= 0 {
		t.Fatalf("Panes should map the managed session to a pid, got %v", live)
	}
	if want := paneField(t, socket, panes[1], "#{pane_pid}"); strconv.Itoa(live[adoptedID]) != want {
		t.Fatalf("Panes[%s] = %d, want the adopted pane's pid %s", adoptedID, live[adoptedID], want)
	}

	if out, err := tmuxOn(socket, "kill-pane", "-t", panes[1]).CombinedOutput(); err != nil {
		t.Fatalf("kill the adopted pane: %v: %s", err, out)
	}
	live, err = driver.Panes()
	if err != nil {
		t.Fatalf("Panes after the pane closed: %v", err)
	}
	if _, listed := live[adoptedID]; listed {
		t.Fatalf("a closed adopted pane should be absent from Panes, got %v", live)
	}
}

// Release is how the manager lets go of a pane it never owned: the pane keeps
// running, and the id falls back to managed resolution.
func TestReleaseReturnsTheIDToManagedResolution(t *testing.T) {
	driver := requireTmux(t)
	socket, panes := foreignServer(t)
	id := uniqueID("release")
	adopt(t, driver, id, socket, panes[1])
	driver.Release(id)

	if _, adopted := driver.AdoptedTarget(id); adopted {
		t.Fatal("Release should drop the registry entry")
	}
	if got, want := driver.TargetFor(id), (Target{Socket: testSocket, Name: sessionName(id)}); got != want {
		t.Fatalf("TargetFor after Release = %+v, want %+v", got, want)
	}
	if err := driver.Kill(id); err != nil {
		t.Fatalf("Kill after Release should be an ordinary no-op, got %v", err)
	}
	if pane := paneField(t, socket, panes[1], "#{pane_id}"); pane != panes[1] {
		t.Fatalf("the released pane should still be running, got %q", pane)
	}
}

func TestAdoptValidatesItsTarget(t *testing.T) {
	driver := &Driver{socket: testSocket}
	if err := driver.Adopt("", Target{Name: "%1"}); err == nil {
		t.Fatal("Adopt should reject an empty session id")
	}
	if err := driver.Adopt("someid", Target{Socket: "elsewhere"}); err == nil {
		t.Fatal("Adopt should reject an empty tmux target")
	}
	if err := driver.Adopt("someid", Target{Name: "%1"}); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if got, want := driver.TargetFor("someid"), (Target{Socket: testSocket, Name: "%1"}); got != want {
		t.Fatalf("TargetFor = %+v, want the manager socket filled in as %+v", got, want)
	}
}

// The poll loop and the UI drive one driver from different goroutines, so
// every registry read and write has to hold the lock.
func TestAdoptRegistryIsSafeForConcurrentUse(t *testing.T) {
	driver := &Driver{socket: testSocket}
	var wait sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			id := "session" + strconv.Itoa(worker)
			for round := 0; round < 200; round++ {
				driver.Adopt(id, Target{Socket: "sock", Name: "%" + strconv.Itoa(round)})
				driver.TargetFor(id)
				driver.AdoptedTarget("session" + strconv.Itoa((worker+1)%8))
				driver.Release(id)
			}
		}(worker)
	}
	wait.Wait()
}

// A manager holding only adopted sessions never starts a server of its own, so
// the listing of managed panes fails with "no server running". Answering the
// whole question with an empty map there left every adopted session absent,
// which the poll loop reads as dead — and that is the ordinary case for
// adoption, not an edge one.
func TestPanesCoversAdoptedSessionsWithNoServerOfOurOwn(t *testing.T) {
	socket, panes := foreignServer(t)

	// A socket no server was ever started on: exactly what the manager has
	// before it launches anything itself.
	driver, err := NewWithSocket(tmuxtest.NewSocket("empty"))
	if err != nil {
		t.Fatalf("NewWithSocket: %v", err)
	}
	if _, err := driver.run("list-panes", "-a"); err == nil {
		t.Fatal("this driver's own server is up; the test proves nothing")
	}

	adoptedID := uniqueID("lonely")
	adopt(t, driver, adoptedID, socket, panes[1])

	live, err := driver.Panes()
	if err != nil {
		t.Fatalf("Panes with no server of our own: %v", err)
	}
	if want := paneField(t, socket, panes[1], "#{pane_pid}"); strconv.Itoa(live[adoptedID]) != want {
		t.Fatalf("Panes[%s] = %d, want the adopted pane's pid %s — an adopted session reads as dead", adoptedID, live[adoptedID], want)
	}
}

// fillPane types numbered lines into a pane the test owns, past the point
// where they fit on screen, so a region below the visible screen has
// somewhere to come from.
func fillPane(t *testing.T, socket, target, prefix string, lines int) {
	t.Helper()
	var text strings.Builder
	for line := 1; line <= lines; line++ {
		fmt.Fprintf(&text, "%s%03d\n", prefix, line)
	}
	// Without this the fill reaches the pane twice over: the line discipline
	// echoes each character and the cat running there writes it again. The
	// two streams interleave differently every run -- a loaded machine
	// produces rows like "region-region-line-034" with two lines spliced
	// into one, and a line count that decides for itself how far the screen
	// has scrolled. One writer makes the history the same every time.
	silenceEcho(t, socket, target)
	if out, err := tmuxOn(socket, "send-keys", "-t", target, "-l", text.String()).CombinedOutput(); err != nil {
		t.Fatalf("fill %s: %v: %s", target, err, out)
	}
	waitForPane(t, socket, target, fmt.Sprintf("%s%03d", prefix, lines))
	settlePane(t, socket, target)
}

// silenceEcho turns off the echo on a pane's own terminal, which is reachable
// because tmux names it.
func silenceEcho(t *testing.T, socket, target string) {
	t.Helper()
	tty := strings.TrimSpace(paneField(t, socket, target, "#{pane_tty}"))
	// The device has to precede the settings: BSD stty reads "-echo" first as
	// the whole argument list, aims at stdin, and fails there instead.
	if out, err := exec.Command("stty", sttyDeviceFlag(), tty, "-echo").CombinedOutput(); err != nil {
		t.Fatalf("silence echo on %s: %v: %s", tty, err, out)
	}
}

// sttyDeviceFlag names the terminal to act on. Only GNU coreutils spells it
// -F; the BSD stty macOS ships takes -f and rejects -F.
func sttyDeviceFlag() string {
	if runtime.GOOS == "linux" {
		return "-F"
	}
	return "-f"
}

// settlePane waits for a pane to stop changing, since the last line arriving
// only proves the screen is still scrolling under the write. A region read
// taken then finds the newest line inside the scrollback it is supposed to
// sit above.
func settlePane(t *testing.T, socket, target string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	previous := ""
	for time.Now().Before(deadline) {
		current := capturePaneOn(t, socket, target)
		if current == previous {
			return
		}
		previous = current
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("pane %s never stopped changing", target)
}

func waitForPane(t *testing.T, socket, target, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		pane := capturePaneOn(t, socket, target)
		if strings.Contains(pane, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane %s never printed %q:\n%s", target, want, pane)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// regionOn is the region a plain tmux reads straight off the server holding
// the pane: the answer CaptureRegion has to match.
func regionOn(t *testing.T, socket, target string, start, end int) string {
	t.Helper()
	out, err := tmuxOn(socket, "capture-pane", "-p", "-e", "-t", target,
		"-S", strconv.Itoa(start), "-E", strconv.Itoa(end)).CombinedOutput()
	if err != nil {
		t.Fatalf("region %d..%d of %s on %s: %v: %s", start, end, target, socket, err, out)
	}
	return string(out)
}

func paneHeight(t *testing.T, socket, target string) int {
	t.Helper()
	raw := paneField(t, socket, target, "#{pane_height}")
	height, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("pane height %q: %v", raw, err)
	}
	return height
}

// Scrolling back in an adopted pane is the only read of its history there is:
// control mode, which serves the scrollback of a managed session, is refused
// on a server the manager does not own. So the region has to be read off that
// pane's own server, and it has to reach above the visible screen.
func TestCaptureRegionReadsAnAdoptedPanesScrollback(t *testing.T) {
	driver := requireTmux(t)
	socket, panes := foreignServer(t)
	id := uniqueID("region")
	adopt(t, driver, id, socket, panes[1])
	fillPane(t, socket, panes[1], "region-line-", 60)
	height := paneHeight(t, socket, panes[1])

	screen, err := driver.CaptureRegion(id, 0, height-1)
	if err != nil {
		t.Fatalf("CaptureRegion of the visible screen: %v", err)
	}
	if !strings.Contains(screen, "region-line-060") {
		t.Fatalf("the visible region is missing the newest line:\n%s", screen)
	}
	if got, want := screen, regionOn(t, socket, panes[1], 0, height-1); got != want {
		t.Fatalf("visible region =\n%s\nwant\n%s", got, want)
	}

	// Everything above the visible screen: the newest line cannot appear
	// here, so a capture that ignored the region would fail this.
	history, err := driver.CaptureRegion(id, -2*height, -1)
	if err != nil {
		t.Fatalf("CaptureRegion of the scrollback: %v", err)
	}
	if !strings.Contains(history, "region-line-") {
		t.Fatalf("the scrollback region carried no history at all:\n%s", history)
	}
	if strings.Contains(history, "region-line-060") {
		t.Fatalf("the region above the screen carries the newest line, so it never read scrollback:\n%s", history)
	}
	if got, want := history, regionOn(t, socket, panes[1], -2*height, -1); got != want {
		t.Fatalf("scrollback region =\n%s\nwant\n%s", got, want)
	}
	if neighbour := capturePaneOn(t, socket, panes[0]); strings.Contains(neighbour, "region-line-") {
		t.Fatalf("the test wrote into the neighbouring pane too:\n%s", neighbour)
	}
}

// The same call on a session the manager did create still reads that
// session's own region on the manager's own socket.
func TestCaptureRegionReadsAManagedSession(t *testing.T) {
	driver := requireTmux(t)
	id := uniqueID("managedregion")
	command := `i=1; while [ "$i" -le 60 ]; do printf 'managed-line-%03d\n' "$i"; i=$((i+1)); done`
	if err := driver.Create(id, "/tmp", command, nil, 80, 24); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { driver.Kill(id) })
	target := sessionName(id)
	waitForPane(t, testSocket, target, "managed-line-060")
	height := paneHeight(t, testSocket, target)

	screen, err := driver.CaptureRegion(id, 0, height-1)
	if err != nil {
		t.Fatalf("CaptureRegion of the visible screen: %v", err)
	}
	if !strings.Contains(screen, "managed-line-060") {
		t.Fatalf("the visible region is missing the newest line:\n%s", screen)
	}

	history, err := driver.CaptureRegion(id, -2*height, -1)
	if err != nil {
		t.Fatalf("CaptureRegion of the scrollback: %v", err)
	}
	if !strings.Contains(history, "managed-line-") {
		t.Fatalf("the scrollback region carried no history at all:\n%s", history)
	}
	if strings.Contains(history, "managed-line-060") {
		t.Fatalf("the region above the screen carries the newest line, so it never read scrollback:\n%s", history)
	}
	if got, want := history, regionOn(t, testSocket, target, -2*height, -1); got != want {
		t.Fatalf("scrollback region =\n%s\nwant\n%s", got, want)
	}
}

// livePanes lists every pane a server still holds, which is how a kill is
// proved: the target gone, its neighbour untouched.
func livePanes(t *testing.T, socket string) []string {
	t.Helper()
	out, err := tmuxOn(socket, "list-panes", "-a", "-F", "#{pane_id}").CombinedOutput()
	if err != nil {
		t.Fatalf("list-panes on %s: %v: %s", socket, err, out)
	}
	return strings.Fields(string(out))
}

func waitGone(t *testing.T, driver *Driver, id string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !driver.Exists(id) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s was still alive two seconds after the kill", id)
}

// The manager can end a pane it never started, but only by naming the method
// that does it. Kill goes on refusing; KillAdopted takes that one pane and
// leaves the neighbour its owner is also working in.
func TestKillAdoptedEndsTheForeignPane(t *testing.T) {
	driver := requireTmux(t)
	socket, panes := foreignServer(t)
	id := uniqueID("killadopted")
	adopt(t, driver, id, socket, panes[1])

	if err := driver.Kill(id); !errors.Is(err, ErrAdopted) {
		t.Fatalf("Kill on an adopted session = %v, want a refusal wrapping ErrAdopted", err)
	}
	if !driver.Exists(id) {
		t.Fatal("a refused Kill must leave the pane running")
	}

	if err := driver.KillAdopted(id); err != nil {
		t.Fatalf("KillAdopted: %v", err)
	}
	waitGone(t, driver, id)
	if live := livePanes(t, socket); len(live) != 1 || live[0] != panes[0] {
		t.Fatalf("KillAdopted should have taken %s and left %s; panes now %q", panes[1], panes[0], live)
	}
	if _, adopted := driver.AdoptedTarget(id); !adopted {
		t.Fatal("killing the pane must not stop the manager tracking the session: Release is what does that")
	}
	if err := driver.KillAdopted(id); err != nil {
		t.Fatalf("KillAdopted on a pane that is already gone = %v, want a no-op", err)
	}
}

// The deliberate kill is only for adopted panes: pointed at a session the
// manager started, it refuses rather than quietly doing Kill's job, and the
// ordinary Kill still ends that session with an adoption on the books.
func TestKillAdoptedRefusesAManagedSession(t *testing.T) {
	driver := requireTmux(t)
	socket, panes := foreignServer(t)
	adopt(t, driver, uniqueID("bystander"), socket, panes[1])

	id := uniqueID("managed")
	if err := driver.Create(id, "/tmp", "cat", nil, 0, 0); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { driver.Kill(id) })

	if err := driver.KillAdopted(id); err == nil {
		t.Fatal("KillAdopted on a managed session should refuse, got nil")
	}
	if !driver.Exists(id) {
		t.Fatal("the refused KillAdopted must leave the managed session running")
	}
	if err := driver.Kill(id); err != nil {
		t.Fatalf("Kill on a managed session: %v", err)
	}
	waitGone(t, driver, id)
	if live := livePanes(t, socket); len(live) != 2 {
		t.Fatalf("killing a managed session must not touch the foreign server, panes now %q", live)
	}
}

// A row is removed on what ScanPanes calls gone, so the difference between
// "that server listed its panes and yours was not among them" and "that
// server could not be listed" is the difference between a tidy board and an
// empty one. The first case: a live pane and a closed one on a server that
// answers.
func TestScanPanesNamesOnlyThePaneAServerAnsweredFor(t *testing.T) {
	driver := requireTmux(t)
	socket, panes := foreignServer(t)
	liveID, closedID := uniqueID("live"), uniqueID("closed")
	adopt(t, driver, liveID, socket, panes[0])
	adopt(t, driver, closedID, socket, panes[1])

	scan, err := driver.ScanPanes()
	if err != nil {
		t.Fatalf("ScanPanes: %v", err)
	}
	if len(scan.Gone) != 0 {
		t.Fatalf("nothing has closed yet, got Gone = %v", scan.Gone)
	}

	if out, err := tmuxOn(socket, "kill-pane", "-t", panes[1]).CombinedOutput(); err != nil {
		t.Fatalf("kill the adopted pane: %v: %s", err, out)
	}
	scan, err = driver.ScanPanes()
	if err != nil {
		t.Fatalf("ScanPanes after the pane closed: %v", err)
	}
	if !scan.Gone[closedID] {
		t.Fatalf("the closed pane should be proven gone, got Gone = %v", scan.Gone)
	}
	if scan.Gone[liveID] {
		t.Fatalf("the surviving pane is on the same listing and is not gone, got Gone = %v", scan.Gone)
	}
	if scan.PIDs[liveID] <= 0 {
		t.Fatalf("PIDs should still carry the live pane, got %v", scan.PIDs)
	}
	if _, listed := scan.PIDs[closedID]; listed {
		t.Fatalf("a closed pane should be absent from PIDs, got %v", scan.PIDs)
	}
}

// The second case, and the one that has to be safe: a listing that fails for
// any reason other than the server being down. Answering with an empty server
// would report every session on it gone, so the whole scan fails instead.
func TestScanPanesFailsRatherThanCallAnUnreadableServerEmpty(t *testing.T) {
	dir := t.TempDir()
	stub := dir + "/tmux"
	script := "#!/bin/sh\ncase \"$*\" in\n" +
		"  \"-L foreignsock list-panes\"*) echo 'tmux: server exited unexpectedly' >&2; exit 1;;\n" +
		"esac\nexit 0\n"
	if err := os.WriteFile(stub, []byte(script), 0o700); err != nil {
		t.Fatalf("stub: %v", err)
	}
	driver := &Driver{bin: stub, socket: testSocket}
	adopt(t, driver, "borrowed", "foreignsock", "%3")

	scan, err := driver.ScanPanes()
	if err == nil {
		t.Fatalf("a listing that failed should fail the scan, got %+v", scan)
	}
	if len(scan.Gone) != 0 {
		t.Fatalf("a failed scan proves nothing gone, got %v", scan.Gone)
	}
	if !strings.Contains(err.Error(), "foreignsock") {
		t.Fatalf("the error should name the server that could not be listed, got %v", err)
	}
}

// A server that is not running is the one absence tmux reports as a normal
// answer rather than an error, and it is a real one: a pane cannot outlive
// its server. Keeping the row instead would leave it addressing "%1" on
// whatever server comes back on that socket next.
func TestScanPanesCountsAServerThatIsNotRunningAsGone(t *testing.T) {
	driver := requireTmux(t)
	adoptedID := uniqueID("vanished")
	adopt(t, driver, adoptedID, uniqueID("neverstarted"), "%1")

	scan, err := driver.ScanPanes()
	if err != nil {
		t.Fatalf("ScanPanes: %v", err)
	}
	if !scan.Gone[adoptedID] {
		t.Fatalf("a pane on a server that is not running is gone, got Gone = %v", scan.Gone)
	}
}
