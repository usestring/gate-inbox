package tmux

import (
	"testing"
	"time"
)

func TestPaneLivenessReadsTheActivityStamp(t *testing.T) {
	scan := parsePaneLiveness("gi_aaaa 100 1793639859\ngi_bbbb 200 0\ngi_cccc 300\nsomeone-else 400 1793639859\n")
	if got := scan.PIDs; len(got) != 3 || got["aaaa"] != 100 || got["bbbb"] != 200 || got["cccc"] != 300 {
		t.Fatalf("PIDs = %v", got)
	}
	if got := scan.Activity["aaaa"]; !got.Equal(time.Unix(1793639859, 0)) {
		t.Errorf("Activity[aaaa] = %v", got)
	}
	// A zero or missing stamp is no stamp: read as a time it would make a
	// pane look quiet since 1970.
	for _, id := range []string{"bbbb", "cccc"} {
		if at, ok := scan.Activity[id]; ok {
			t.Errorf("Activity[%s] = %v, want none", id, at)
		}
	}
}

func TestServerPanesKeepASessionNameWithSpaces(t *testing.T) {
	live := parseServerPanes("%1 100 1793639859 my session\n%2 200 0 other\nshort line\n")
	if got := live["%1"]; got.pid != 100 || !got.activity.Equal(time.Unix(1793639859, 0)) {
		t.Errorf("%%1 = %+v", got)
	}
	if got, ok := live["my session"]; !ok || got.pid != 100 {
		t.Errorf("session name with a space = %+v, %v", got, ok)
	}
	if got := live["%2"]; got.pid != 200 || !got.activity.IsZero() {
		t.Errorf("%%2 = %+v, want no stamp", got)
	}
}

// The poll pass leaves a resting pane uncaptured while tmux says nothing was
// written to it, so the stamp has to move on the pane's own output -- not
// only on a client's keystrokes, which is what #{session_activity} tracks.
func TestScanActivityAdvancesOnPaneOutput(t *testing.T) {
	driver := requireTmux(t)
	id := uniqueID("activity")
	if err := driver.Create(id, "/tmp", "sleep 2; printf 'late output'; exec cat", nil, 80, 24); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { driver.Kill(id) })
	scan, err := driver.ScanPanes()
	if err != nil {
		t.Fatalf("ScanPanes: %v", err)
	}
	before, ok := scan.Activity[id]
	if !ok {
		t.Fatalf("no activity stamp for %s in %v", id, scan.Activity)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		scan, err := driver.ScanPanes()
		if err != nil {
			t.Fatalf("ScanPanes: %v", err)
		}
		if scan.Activity[id].After(before) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("activity stamp never moved past %v after the pane printed", before)
}
