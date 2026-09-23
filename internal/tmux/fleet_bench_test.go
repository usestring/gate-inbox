package tmux

import (
	"fmt"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// fleetSize is this operator's real board at its busiest: 87 adopted panes,
// which is what makes one fork per pane the largest single cost in the
// program.
const fleetSize = 87

// fleetServers is more than one on purpose. Adopted panes follow whichever
// tmux server their owner started them on, so a pass that only ever works
// against a single server would measure a shape the board does not have.
const fleetServers = 2

// fleetFixture builds a board of adopted panes spread over several tmux
// servers and returns the driver that resolves them.
func fleetFixture(tb testing.TB) (*Driver, []string) {
	tb.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		tb.Skip("tmux not installed")
	}
	driver, err := NewWithSocket(testSocket)
	if err != nil {
		tb.Fatalf("NewWithSocket: %v", err)
	}
	var ids []string
	for server := 0; server < fleetServers; server++ {
		socket := tmuxtest.NewSocket("fleet")
		tmuxOn(socket, "kill-server").Run()
		tb.Cleanup(func() { tmuxOn(socket, "kill-server").Run() })
		for i := server; i < fleetSize; i += fleetServers {
			name := "s" + strconv.Itoa(i)
			args := []string{"new-session", "-d", "-s", name, "-x", "200", "-y", "50",
				fmt.Sprintf("printf 'pane-%d ready'; cat", i)}
			if out, err := tmuxOn(socket, args...).CombinedOutput(); err != nil {
				tb.Fatalf("fleet new-session: %v: %s", err, out)
			}
			out, err := tmuxOn(socket, "list-panes", "-t", name, "-F", "#{pane_id}").CombinedOutput()
			if err != nil {
				tb.Fatalf("fleet list-panes: %v: %s", err, out)
			}
			id := "fleet" + strconv.Itoa(i)
			if err := driver.Adopt(id, Target{Socket: socket, Name: strings.TrimSpace(string(out))}); err != nil {
				tb.Fatalf("Adopt: %v", err)
			}
			ids = append(ids, id)
		}
	}
	return driver, ids
}

// BenchmarkFleetCaptureControl is the poll pass as it now runs: one batch per
// tmux server over a client held open across passes, forking nothing.
func BenchmarkFleetCaptureControl(b *testing.B) {
	driver, ids := fleetFixture(b)
	defer driver.CloseCaptureClients()
	for id, capture := range driver.CapturePanes(ids) {
		if capture.Err != nil {
			b.Fatalf("%s: %v", id, capture.Err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for id, capture := range driver.CapturePanes(ids) {
			if capture.Err != nil {
				b.Fatalf("%s: %v", id, capture.Err)
			}
		}
	}
}

// BenchmarkFleetCaptureExec is the same pass the way it ran before: one tmux
// process per pane, every pass, on a two second interval.
func BenchmarkFleetCaptureExec(b *testing.B) {
	driver, ids := fleetFixture(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, id := range ids {
			if _, err := driver.CapturePane(id); err != nil {
				b.Fatalf("%s: %v", id, err)
			}
		}
	}
}
