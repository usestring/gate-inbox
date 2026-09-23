package tmuxtest

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/tmux"
)

// TestTheReaperRefusesEverySocketItDoesNotOwn covers the one bug in this
// package that would be unrecoverable. The reaper kills by pid, and the
// operator's own tmux -- their attached shell, their live agent panes -- is on
// the default socket, so a predicate that widened by one case would end their
// work.
func TestTheReaperRefusesEverySocketItDoesNotOwn(t *testing.T) {
	refused := []string{
		"",
		"default",
		"gitest-",   // the bare prefix names no run
		"amuitest-", // a bare legacy prefix names no run either
		"gitest",
		"gi_1b364bb3",
		"main",
		"claude",
		"notgitest-abc",
		"amtest-",         // nor does the bare prefix before gitest-
		"adopt-test-1832", // the unswept name this package's prefix replaced
		"/tmp/tmux-1000/default",
	}
	for _, socket := range refused {
		if Owns(socket) {
			t.Errorf("Owns(%q) = true, want false: the reaper would kill processes it does not own", socket)
		}
	}
	for _, family := range []string{"ui", "tmux", "adopt", "launch", "operator"} {
		if socket := NewSocket(family); !Owns(socket) {
			t.Errorf("Owns(%q) = false, want true: the reaper would never collect %s's own clients", socket, family)
		}
	}
	// The names earlier revisions created are still collectable, which is
	// what heals a board that already accumulated them.
	for _, prefix := range legacyPrefixes {
		if socket := prefix + "abc12345"; !Owns(socket) {
			t.Errorf("Owns(%q) = false: strays an earlier revision left behind would never be collected", socket)
		}
	}
}

// TestEveryPrefixIsSafe holds the shape of the legacy list, because Owns is
// only as narrow as the narrowest thing in it: a prefix that was empty, short,
// or generic would turn a clause in Owns into a match on most of the machine.
func TestEveryPrefixIsSafe(t *testing.T) {
	seen := map[string]bool{socketPrefix: true}
	if len(socketPrefix) < len("gitest-") || !strings.HasPrefix(socketPrefix, "gi") {
		t.Fatalf("socket prefix %q is too weak to name only this module's servers", socketPrefix)
	}
	for _, prefix := range legacyPrefixes {
		if len(prefix) < len("gitest-") {
			t.Errorf("socket prefix %q is too short to name only this module's servers", prefix)
		}
		if !strings.HasPrefix(prefix, "am") || !strings.HasSuffix(prefix, "-") {
			t.Errorf("socket prefix %q is not of the form am<family>-", prefix)
		}
		if seen[prefix] {
			t.Errorf("socket prefix %q is registered twice", prefix)
		}
		seen[prefix] = true
	}
}

// TestOwnsAgreesWithTheDriver keeps the local default-socket refusal aligned
// with the driver's. Owns restates it rather than calling tmux.OwnsSocket
// because internal/tmux's own tests import this package, and importing back
// would be a cycle -- so the two can drift, and this is what notices.
func TestOwnsAgreesWithTheDriver(t *testing.T) {
	for _, family := range []string{"ui", "tmux", "adopt"} {
		socket := NewSocket(family)
		if !tmux.OwnsSocket(socket) {
			t.Errorf("the driver does not consider %q the manager's own server, but the reaper does", socket)
		}
	}
	// The one name that matters in the other direction: the driver refuses
	// tmux's default server, and so must this.
	if Owns(tmux.DefaultSocket) {
		t.Errorf("Owns(%q) = true: that is tmux's default server", tmux.DefaultSocket)
	}
}

// TestEverySocketCarriesTheOnePrefix is what keeps a new package from quietly
// creating servers the sweep cannot see. That is how the leak survived its
// first fix: internal/ui swept its own sockets and every other package's went
// on accumulating.
func TestEverySocketCarriesTheOnePrefix(t *testing.T) {
	socket := NewSocket("newpackage")
	if !strings.HasPrefix(socket, socketPrefix) || !Owns(socket) {
		t.Fatalf("NewSocket(%q) = %q, which the reaper does not own: its sockets would never be collected", "newpackage", socket)
	}
	// A family that could not be told apart from the rest of the name would
	// make a `ps` listing unreadable, and an empty one would produce a name
	// one character from the bare prefix.
	for _, bad := range []string{"", "a b", "a/b", "a-b"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("NewSocket(%q) did not refuse", bad)
				}
			}()
			NewSocket(bad)
		}()
	}
}

// TestOnlyATmuxArgvNamesASocket keeps the argv match from degrading into a
// substring search over `ps` output, which would put any command line
// mentioning a test socket -- a grep, an editor, this test's own harness -- on
// the kill list.
func TestOnlyATmuxArgvNamesASocket(t *testing.T) {
	own := NewSocket("ui")
	cases := []struct {
		name string
		argv []string
		want bool
	}{
		{"a control client", []string{"tmux", "-L", own, "-C", "attach-session", "-t", "gi_poll-anchor"}, true},
		{"an absolute tmux", []string{"/usr/bin/tmux", "-L", own, "kill-server"}, true},
		{"a grep for the socket", []string{"grep", "-L", own}, false},
		{"tmux on the default socket", []string{"tmux", "attach"}, false},
		{"tmux naming another socket", []string{"tmux", "-L", "default", "ls"}, false},
		{"an editor holding the name", []string{"vim", "notes-" + own + ".txt"}, false},
		{"empty", nil, false},
		{"a dangling -L", []string{"tmux", "-L"}, false},
	}
	for _, c := range cases {
		socket, got := SocketOf(c.argv)
		if got != c.want {
			t.Errorf("%s: SocketOf(%q) = %q,%v, want %v", c.name, c.argv, socket, got, c.want)
		}
	}
}

// TestKillServerRefusesASocketItDoesNotOwn is the same refusal at the other
// end: kill-server ends every session on a socket, so one that resolved to
// tmux's default would take the operator's whole board with it.
func TestKillServerRefusesASocketItDoesNotOwn(t *testing.T) {
	for _, socket := range []string{"", "default", "main"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("KillServer(%q) did not refuse: it would kill a server this module does not own", socket)
				}
			}()
			KillServer(socket)
		}()
	}
}

// TestTheStraySweepLeavesAFreshClientAlone is the guard on the one way this
// package could break a passing test rather than fix a leak. `go test ./...`
// runs packages concurrently and they all name sockets with the same prefix
// now, so the sweep sees its siblings' clients; a fixture is briefly
// serverless between kill-server and new-session, and under load the liveness
// probe can fail against a live server. Age is what separates a stray from a
// client somebody is using.
func TestTheStraySweepLeavesAFreshClientAlone(t *testing.T) {
	for _, age := range []string{"00:00", "00:03", "09:59"} {
		if parseETime(age) >= strayAge {
			t.Errorf("a client %s old would be swept while its own test is still running", age)
		}
	}
	for _, age := range []string{"10:01", "01:00:00", "2-03:04:05"} {
		if parseETime(age) < strayAge {
			t.Errorf("a client %s old would never be collected", age)
		}
	}
	// An unreadable field has to read as new, so the failure is collecting
	// nothing rather than collecting somebody's live client.
	for _, bad := range []string{"", "??", "1:2:3:4", "x-01:00"} {
		if got := parseETime(bad); got != 0 {
			t.Errorf("parseETime(%q) = %v, want 0 so the sweep skips it", bad, got)
		}
	}
}

// TestElapsedTimeMatchesPS keeps the parse honest against the ps on this
// machine, since the field's format is the one thing here that is not this
// package's own invention.
func TestElapsedTimeMatchesPS(t *testing.T) {
	out, err := exec.Command("ps", "-o", "etime=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		t.Skipf("ps: %v", err)
	}
	field := strings.TrimSpace(string(out))
	if parseETime(field) == 0 && field != "00:00" {
		t.Fatalf("ps reports this process's elapsed time as %q, which the parse reads as zero", field)
	}
}
