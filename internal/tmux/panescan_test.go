package tmux

import "testing"

// A directory name is its occupant's to choose, line breaks included. What
// follows one must not read as a pane line: here it claims a session that
// has no pane, and a pid for one that does.
func TestPaneScanIgnoresALineBreakInADirectory(t *testing.T) {
	const mark = "0123abcd "
	out := mark + "gi_aaaa 100 /work/one\n" +
		mark + "gi_bbbb 200 /evil\ngi_ghost 300 /x\ngi_aaaa 400 /y\n" +
		mark + "gi_cccc 500 /work/ends in a space \n" +
		mark + "someone-else 600 /home\n"
	scan := parsePaneScan(out, mark)
	want := map[string]int{"aaaa": 100, "bbbb": 200, "cccc": 500}
	if len(scan.PIDs) != len(want) {
		t.Fatalf("PIDs = %v, want %v", scan.PIDs, want)
	}
	for id, pid := range want {
		if scan.PIDs[id] != pid {
			t.Errorf("PIDs[%s] = %d, want %d", id, scan.PIDs[id], pid)
		}
	}
	if path, ok := scan.Paths["bbbb"]; ok {
		t.Errorf("Paths[bbbb] = %q; a directory cut at its line break is no answer", path)
	}
	if got := scan.Paths["aaaa"]; got != "/work/one" {
		t.Errorf("Paths[aaaa] = %q, want /work/one", got)
	}
	if got := scan.Paths["cccc"]; got != "/work/ends in a space " {
		t.Errorf("Paths[cccc] = %q, want the trailing space kept", got)
	}
}

// The board's poll pass scans every two seconds and shows no directory, so
// its scan must not ask tmux for one; the listing that reports sessions does.
func TestOnlyThePathScanReadsDirectories(t *testing.T) {
	driver := requireTmux(t)
	id := uniqueID("paths")
	if err := driver.Create(id, "/tmp", "cat", nil, 80, 24); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { driver.Kill(id) })
	scan, err := driver.ScanPanes()
	if err != nil {
		t.Fatalf("ScanPanes: %v", err)
	}
	if _, ok := scan.PIDs[id]; !ok || scan.Paths != nil {
		t.Errorf("ScanPanes = pids %v, paths %v; want the pane live and no directories read", scan.PIDs, scan.Paths)
	}
	scan, err = driver.ScanPanesWithPaths()
	if err != nil {
		t.Fatalf("ScanPanesWithPaths: %v", err)
	}
	if _, ok := scan.PIDs[id]; !ok || scan.Paths[id] != "/tmp" {
		t.Errorf("ScanPanesWithPaths = pids %v, paths %v; want the pane live in /tmp", scan.PIDs, scan.Paths)
	}
}
