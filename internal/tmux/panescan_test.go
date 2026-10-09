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
