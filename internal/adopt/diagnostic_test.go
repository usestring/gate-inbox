package adopt

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
)

// TestScanThisMachine reports what identification makes of the tmux panes actually
// running on this machine. Read-only: it lists panes and captures their visible
// text, and does nothing else to any server it finds.
func TestScanThisMachine(t *testing.T) {
	if os.Getenv("ADOPT_PROBE") == "" {
		t.Skip("set ADOPT_PROBE=1")
	}

	cfg, err := config.Default()
	if err != nil {
		t.Fatalf("default config: %v", err)
	}
	var tools []Tool
	for name, tool := range cfg.Tools {
		spec := Tool{Name: name, Command: tool.Command, Shell: tool.Shell}
		if tool.ActivityCutoff != "" {
			re, err := regexp.Compile(tool.ActivityCutoff)
			if err != nil {
				t.Fatalf("%s activity_cutoff: %v", name, err)
			}
			spec.Prompt = re
		}
		tools = append(tools, spec)
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	t.Logf("configured tools: %d", len(tools))

	sockets := realSockets(t)
	t.Logf("tmux servers: %v", sockets)

	procs := NewProcTable()
	total, identified, confident := 0, 0, 0
	for _, socket := range sockets {
		panes := Panes(socket)
		label := socket
		if label == "" {
			label = "(default)"
		}
		t.Logf("\n=== %s: %d panes ===", label, len(panes))
		for _, pane := range panes {
			total++
			text, err := Capture(pane.Socket, pane.PaneID)
			if err != nil {
				t.Logf("  %-5s %-18s capture failed: %v", pane.PaneID, pane.Session, err)
				continue
			}
			match, ok := Identify(pane, tools, text, procs)
			verdict := "-"
			if ok {
				identified++
				verdict = match.Tool + " " + fmt.Sprint(match.Signals)
				if match.Confident() {
					confident++
					verdict = "ADOPT  " + verdict
				} else {
					verdict = "propose " + verdict
				}
			}
			t.Logf("  %-5s %-18s cmd=%-10s tree=%-28s cwd=%s\n        -> %s",
				pane.PaneID, trim(pane.Session, 18), trim(pane.Command, 10),
				trim(strings.Join(procs.Cmdlines(pane.PID), " | "), 28),
				trim(pane.Cwd, 44), verdict)
		}
	}
	t.Logf("\npanes=%d identified=%d confident=%d", total, identified, confident)
}

// realSockets lists the tmux servers this user has running, which is what a
// scan would look at.
func realSockets(t *testing.T) []string {
	t.Helper()
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("tmux-%d", os.Getuid()))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Logf("no tmux socket directory at %s: %v", dir, err)
		return nil
	}
	var sockets []string
	for _, entry := range entries {
		if entry.Name() == "default" {
			sockets = append(sockets, "")
			continue
		}
		sockets = append(sockets, entry.Name())
	}
	sort.Strings(sockets)
	return sockets
}

func trim(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
