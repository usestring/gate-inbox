package tmux

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeTmux writes a tmux stand-in that behaves per socket, and returns a
// driver pointed at it. A real tmux cannot be made to hang on demand, and the
// hang is the whole subject here: the server this scan has to survive is one
// that is up, has accepted the connection, and is busy inside some other
// client's command.
//
// The script sleeps rather than blocking on the socket so that the sleep is a
// child of the command exec kills, which is the case CombinedOutput would
// otherwise go on waiting through -- the pipe stays open while that child
// holds it.
func fakeTmux(t *testing.T, body string) *Driver {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tmux")
	script := "#!/bin/sh\n" + body
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake tmux: %v", err)
	}
	return &Driver{bin: path, socket: "scan-timeout-test"}
}

// hangingOn sleeps out any command aimed at one socket and answers every
// other one the way a machine with no tmux server on it does.
func hangingOn(socket string) string {
	return `case " $* " in
  *" -L ` + socket + ` "*) sleep 30; exit 0 ;;
esac
echo "no server running on $2" >&2
exit 1
`
}

// TestScanPanesGivesUpOnAServerThatNeverAnswers is the deadline itself: one
// tmux call used to be able to hold a poll pass for as long as it liked --
// 10.8s measured on a live board -- because nothing bounded the fork.
func TestScanPanesGivesUpOnAServerThatNeverAnswers(t *testing.T) {
	driver := fakeTmux(t, hangingOn("scan-timeout-test"))

	start := time.Now()
	scan, err := driver.ScanPanes()
	elapsed := time.Since(start)

	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("ScanPanes() error = %v, want %v", err, ErrTimeout)
	}
	if len(scan.PIDs) != 0 || len(scan.Gone) != 0 {
		t.Errorf("a scan that timed out came back with findings: %+v", scan)
	}
	if elapsed > abandonedBy {
		t.Errorf("ScanPanes() took %s, want it abandoned near %s", elapsed.Round(time.Millisecond), ScanTimeout)
	}
}

// abandonedBy is the bound the timing assertions use. The fake sleeps for
// thirty seconds, so the question they are really asking is whether the
// deadline fired at all; doubling the budget leaves that answer intact and
// stops a loaded machine's scheduling from reading as a missed deadline.
const abandonedBy = 2 * ScanTimeout

// TestATimedOutAdoptedServerNamesNothingGone is the safety property under the
// deadline, and the reason serverPanes checks for a timeout before it reads
// the output. A killed tmux prints nothing; "nothing" is also how tmux
// answers when no server is running, and that answer means every adopted pane
// on the socket is gone -- which is a row the poller deletes.
//
// The pair is the test: the same driver, the same adopted session, answered
// once and killed once, and only the answer may produce Gone.
func TestATimedOutAdoptedServerNamesNothingGone(t *testing.T) {
	const adoptedSocket = "borrowed-server"
	const id = "borrowed"

	answers := fakeTmux(t, hangingOn("nothing-hangs-here"))
	if err := answers.Adopt(id, Target{Socket: adoptedSocket, Name: "%7"}); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	scan, err := answers.ScanPanes()
	if err != nil {
		t.Fatalf("ScanPanes() over a server that answered: %v", err)
	}
	if !scan.Gone[id] {
		t.Fatalf("a server that answered and did not name %s left it out of Gone: %+v", id, scan)
	}

	hangs := fakeTmux(t, hangingOn(adoptedSocket))
	if err := hangs.Adopt(id, Target{Socket: adoptedSocket, Name: "%7"}); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	start := time.Now()
	scan, err = hangs.ScanPanes()
	elapsed := time.Since(start)

	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("ScanPanes() over a hung adopted server: error = %v, want %v", err, ErrTimeout)
	}
	if scan.Gone[id] {
		t.Errorf("a scan that timed out called %s gone; nothing answered for it", id)
	}
	if elapsed > abandonedBy {
		t.Errorf("ScanPanes() took %s, want it abandoned near %s", elapsed.Round(time.Millisecond), ScanTimeout)
	}
}
