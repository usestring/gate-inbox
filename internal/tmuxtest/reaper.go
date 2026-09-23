// Package tmuxtest is the shared teardown for this module's tmux fixtures.
//
// A tmux control-mode client is a process, and it outlives the tmux server it
// attached to: `kill-server` cannot collect one that a dropped driver left
// running. Every package here that drives tmux therefore leaks clients unless
// it both closes its drivers and sweeps what a panicking or timing-out run
// left behind. internal/ui grew that sweep first, alone; one run of it used to
// leak 238 clients, and the packages that did not have it kept leaking -- 19
// fixtures were collected off one board by hand afterwards.
//
// This package is that sweep, once, for all of them. It is imported only from
// _test.go files.
//
// Everything here is built around one refusal: the operator's own tmux -- their
// attached shell, their live agent panes, hours of unrecoverable work -- runs
// on tmux's default server. The reaper kills by pid. A predicate that widened
// by one case would end all of it, so Owns is written to be over-strict and
// its clauses are redundant on purpose.
package tmuxtest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
)

// socketPrefix is the one literal every socket this module's tests create
// begins with, and the only thing Owns generates a match from. One literal
// rather than a registry of them because the registry was the drift: a package
// that invented its own name -- "adopt-test-...", "amwatcher-...", a dozen
// others -- created servers nothing swept, which is how the leak survived
// being fixed in internal/ui alone.
const socketPrefix = "gitest-"

// legacyPrefixes are the names earlier revisions of these tests created. They
// are matched, never generated, so a machine that already accumulated their
// strays heals on the next run instead of needing a human with a kill list.
// Each is a literal for the same reason socketPrefix is.
var legacyPrefixes = []string{
	"amtest-",
	"amuitest-",
	"amtmuxtest-",
	"amsesstest-",
	"amtermtest-",
	"amuiforeign-",
	"amtmuxforeign-",
	"amoperator-",
	"amterminal-",
	"amwatched-",
	"amwatcher-",
	"amattachterm-",
	"amsoloterm-",
	"amdetached-",
	"amscrollback-",
	"amscrolllive-",
	"amsweeplive-",
	"amfleet-",
	"amtmuxgone-",
}

// NewSocket returns a fresh socket name for one family of fixtures -- "ui",
// "operator", "watcher". The family is only there to make a `ps` listing
// readable; the suffix is what lets two checkouts test the same package at the
// same time, since a fixed name let each run's kill-server take the other's
// server down, which reads as an unrelated flake.
func NewSocket(family string) string {
	if family == "" || strings.ContainsAny(family, " \t/-") {
		panic("tmuxtest: bad socket family " + strconv.Quote(family))
	}
	return socketPrefix + family + "-" + uuid.NewString()[:8]
}

// Owns reports whether a tmux socket name is one this module's own tests
// created, and is the only thing standing between the reaper and the
// operator's live work.
//
// Every clause is a refusal:
//
//   - the name must carry the module's own prefix -- matched against a
//     repeated literal rather than against socketPrefix, because a constant
//     that was later emptied or renamed would turn a HasPrefix test into a
//     match on every socket on the machine -- and must be strictly longer than
//     it, so a bare "gitest-" names nothing;
//   - it must not be empty, which tmux reads as its default server;
//   - and it must not be the default socket by name. That check is the same
//     one tmux.OwnsSocket makes, restated here rather than imported: this
//     package is imported from internal/tmux's own tests, and importing back
//     would be a cycle. TestOwnsAgreesWithTheDriver keeps the two aligned.
func Owns(socket string) bool {
	if socket == "" || socket == "default" {
		return false
	}
	if strings.HasPrefix(socket, "gitest-") && len(socket) > len("gitest-") {
		return true
	}
	for _, prefix := range legacyPrefixes {
		if strings.HasPrefix(socket, prefix) && len(socket) > len(prefix) {
			return true
		}
	}
	return false
}

// Client is one live tmux process aimed at a socket this module owns.
type Client struct {
	PID    int
	Socket string
	Age    time.Duration
}

// Clients returns every tmux process attached to a socket Owns accepts.
// Reading `ps` rather than asking tmux is what finds the orphans at all: these
// clients have no server left to list them.
func Clients() []Client {
	// etime comes along because the stray sweep needs it: see ReapStrays.
	out, err := exec.Command("ps", "-axo", "pid=,etime=,args=").Output()
	if err != nil {
		return nil
	}
	var clients []Client
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		socket, ok := SocketOf(fields[2:])
		if !ok {
			continue
		}
		clients = append(clients, Client{PID: pid, Socket: socket, Age: parseETime(fields[1])})
	}
	return clients
}

// parseETime reads ps's elapsed-time field, [[dd-]hh:]mm:ss. An unreadable one
// answers zero, which reads as "brand new" and so keeps the stray sweep off it:
// the sweep's whole job is collecting the old, and the safe failure is to
// collect nothing.
func parseETime(field string) time.Duration {
	days := 0
	if dash := strings.IndexByte(field, '-'); dash >= 0 {
		d, err := strconv.Atoi(field[:dash])
		if err != nil {
			return 0
		}
		days, field = d, field[dash+1:]
	}
	parts := strings.Split(field, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0
	}
	secs := 0
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil {
			return 0
		}
		secs = secs*60 + n
	}
	return time.Duration(days)*24*time.Hour + time.Duration(secs)*time.Second
}

// SocketOf pulls the -L socket out of an argv, and answers only for a tmux
// binary. Matching the whole argv shape rather than searching the line for a
// substring is what keeps the reaper off, say, a grep or an editor that merely
// has "-L amuitest-..." somewhere on its command line.
func SocketOf(argv []string) (string, bool) {
	if len(argv) == 0 || filepath.Base(argv[0]) != "tmux" {
		return "", false
	}
	for i := 0; i+1 < len(argv); i++ {
		// tmux takes one -L and the first wins, so this never keeps
		// looking past it: a socket name that was itself "-L" would
		// otherwise walk the reaper onto the wrong argument.
		if argv[i] == "-L" {
			socket := argv[i+1]
			return socket, Owns(socket)
		}
	}
	return "", false
}

// ClientsOn lists one run's own clients: the tightest scope there is, and the
// only one that stays correct while a second checkout tests in parallel on its
// own suffixed socket.
func ClientsOn(socket string) []Client {
	var on []Client
	for _, c := range Clients() {
		if c.Socket == socket {
			on = append(on, c)
		}
	}
	return on
}

// kill sends SIGKILL to clients, re-checking ownership on the way past. The
// check is redundant -- nothing reaches here that Clients did not already
// vouch for -- and it stays because this is the line that ends processes, and
// the cost of it being wrong once is the operator's live work.
func kill(clients []Client) int {
	killed := 0
	for _, c := range clients {
		if !Owns(c.Socket) {
			continue
		}
		if syscall.Kill(c.PID, syscall.SIGKILL) == nil {
			killed++
		}
	}
	return killed
}

// ReapSocket kills every client on one socket. Only ever called with a run's
// own socket, after its server is down.
func ReapSocket(socket string) int {
	return kill(ClientsOn(socket))
}

// ReapStrays collects leftovers from earlier runs, so a machine that already
// accumulated them heals on the next `go test` instead of needing a human with
// a kill list.
//
// A socket whose server still answers is left alone. That is the whole
// concurrency story: a second checkout running right now holds a live server
// on its own socket, and its clients are not strays. The ones worth killing
// are exactly the ones whose server is already gone -- which is also the shape
// of the leak, since a client that outlives its server is what makes these
// unreapable by kill-server.
//
// It takes no "except mine" argument because at the point a TestMain calls it
// the run owns no clients yet, and a socket name that did collide with an
// earlier run's would hold strays worth collecting rather than anything to
// protect.
//
// A client younger than strayAge is also left alone, and that clause is what
// makes one sweep safe for the whole module. `go test ./...` runs packages
// concurrently, and every package's sockets now match the same prefix, so this
// sweep can see a sibling package's live clients. The server check alone would
// not be enough: a fixture is momentarily serverless between its kill-server
// and its new-session, and on a loaded machine the liveness probe itself can
// fail against a server that is perfectly alive. Age is the property those
// cases do not share -- a stray is left over from an earlier run, not opened
// seconds ago -- so nothing this run or its siblings just built is a candidate.
func ReapStrays() int {
	bySocket := map[string][]Client{}
	for _, c := range Clients() {
		if c.Age < strayAge {
			continue
		}
		bySocket[c.Socket] = append(bySocket[c.Socket], c)
	}
	killed := 0
	for socket, clients := range bySocket {
		if ServerAlive(socket) {
			continue
		}
		killed += kill(clients)
	}
	sweepDeadSocketFiles()
	return killed
}

// strayAge is how long a client must have been running before the stray sweep
// will consider it. Ten minutes is longer than any test in this module takes
// and far shorter than the strays it collects have been there.
const strayAge = 10 * time.Minute

// staleSocketAge is how long a socket file must have gone untouched before this
// package will unlink it. tmux recreates the file when a server starts, so the
// window is only there to keep the sweep away from a server another checkout
// started between the liveness probe and the unlink.
const staleSocketAge = time.Hour

// sweepDeadSocketFiles removes the socket files of test servers that are no
// longer running. A tmux server unlinks nothing when it exits, so every fixture
// server this module ever started left a file behind: one board had 31,337 of
// them in the socket directory, which is also the directory adoption scans to
// find the servers on a machine.
//
// It is the file half of the same leak, and it is bounded the same way: a name
// Owns refuses is never touched, a socket whose server still answers is left
// alone, and a file younger than staleSocketAge is left for its owner.
func sweepDeadSocketFiles() {
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("tmux-%d", os.Getuid()))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		socket := entry.Name()
		if !Owns(socket) {
			continue
		}
		info, err := entry.Info()
		if err != nil || time.Since(info.ModTime()) < staleSocketAge {
			continue
		}
		if ServerAlive(socket) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, socket))
	}
}

// ServerAlive reports whether a tmux server is running on a socket.
// list-sessions is the probe because it never starts a server: asking with a
// command that does would resurrect every dead socket it was pointed at.
func ServerAlive(socket string) bool {
	if !Owns(socket) {
		return false
	}
	return exec.Command("tmux", "-L", socket, "list-sessions").Run() == nil
}

// KillServer tears down the server on a socket this module owns. Every raw
// tmux command in these tests goes through a check like this one: a
// kill-server that resolved to tmux's default socket would end whatever the
// operator has running there.
func KillServer(socket string) {
	if !Owns(socket) {
		panic("tmuxtest: refusing to kill the tmux server on " + strconv.Quote(socket) + ": not a test socket")
	}
	// Failing is the normal case -- usually no server is up.
	_ = exec.Command("tmux", "-L", socket, "kill-server").Run()
}

// AwaitClientsOn waits for the client count on a socket to fall to want.
// Close returns once tmux has gone, but the kernel keeps the zombie until the
// Wait goroutine collects it, so an assertion taken the instant a cleanup
// returns can read one process that is already dead.
func AwaitClientsOn(socket string, want int, within time.Duration) int {
	deadline := time.Now().Add(within)
	for {
		got := len(ClientsOn(socket))
		if got <= want || time.Now().After(deadline) {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// ClearInheritedTmuxEnv unsets the variables that point code at the tmux pane
// the test binary is itself running in.
//
// The manager finds its own pane through TMUX_PANE and TMUX, and several
// driver paths -- Resize, PrepareAttach, the visibility read -- act on what
// they find. A test process inherits both from whatever real tmux the
// developer happens to be running in, so leaving them set points those paths
// at the operator's own pane on tmux's default server: the one server no test
// here may touch under any circumstances. A fixture pane id colliding with the
// operator's would also make Resize silently no-op, which is a flake nobody
// would attribute to this.
//
// Called from TestMain so it is a property of the package rather than a habit
// somebody has to remember.
func ClearInheritedTmuxEnv() {
	os.Unsetenv("TMUX_PANE")
	os.Unsetenv("TMUX")
}

// Guard is the teardown a tmux-driving package's TestMain wraps its run in: it
// sweeps strays before the run, and after it tears the run's own server down
// and collects the clients that outlived it.
//
// It returns the run's exit code so a TestMain reads as one line, which is the
// point: the leak came back once already because the sweep was a sequence of
// steps each package had to remember to repeat.
func Guard(socket string, run func() int) int {
	ClearInheritedTmuxEnv()
	ReapStrays()
	KillServer(socket)
	code := run()
	KillServer(socket)
	// The server is down; anything still holding this socket is a client
	// that outlived it, which is precisely what kill-server cannot collect.
	ReapSocket(socket)
	return code
}

// AssertNoLeak is the per-package guard test's body: it runs work that drives
// tmux and fails if the clients it opened are still there afterwards. The
// caller passes the work as a subtest body so the cleanups under test have
// actually run by the time the counts are compared.
func AssertNoLeak(t *testing.T, socket string, work func(t *testing.T)) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	// Earlier tests on a package-wide socket may legitimately still hold
	// clients; only the delta across this work is this guard's business.
	before := len(ClientsOn(socket))

	var peak int
	t.Run("drives tmux", func(t *testing.T) {
		work(t)
		peak = len(ClientsOn(socket))
	})

	// Without this the test passes on a board where nothing ever opened a
	// client, which is how a guard quietly stops guarding.
	if peak <= before {
		t.Fatalf("no control client was opened (%d -> %d): this guard would pass no matter what leaked", before, peak)
	}

	after := AwaitClientsOn(socket, before, 10*time.Second)
	if after > before {
		t.Fatalf("tmux clients on %s leaked: %d before, %d at peak, %d after cleanup", socket, before, peak, after)
	}
}

// AssertNoLeakOnItsOwnSocket is AssertNoLeak for a package whose fixtures each
// build a server of their own: the socket is not known until the work has run,
// and nothing can have been holding it before, so the whole count afterwards is
// the leak. work returns the socket its fixture used.
func AssertNoLeakOnItsOwnSocket(t *testing.T, work func(t *testing.T) string) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	var socket string
	var peak int
	t.Run("drives tmux", func(t *testing.T) {
		socket = work(t)
		peak = len(ClientsOn(socket))
	})

	// Without this the test passes on a board where nothing ever opened a
	// client, which is how a guard quietly stops guarding.
	if peak == 0 {
		t.Fatalf("no control client was opened on %s: this guard would pass no matter what leaked", socket)
	}

	if after := AwaitClientsOn(socket, 0, 10*time.Second); after > 0 {
		t.Fatalf("tmux clients on %s leaked: %d at peak, %d after cleanup", socket, peak, after)
	}
}
