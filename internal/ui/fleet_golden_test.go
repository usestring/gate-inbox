package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/envname"
)

// goldenFrames is the file this package's render output is pinned to. It was
// written by the code as it stood before the render path was memoised, so a
// diff against it is the whole claim that memoising changed nothing: every
// cache in rendercache.go stores lipgloss's own answer, and this is what
// proves the stored answer is the one that would have been computed.
const goldenFrames = "testdata/fleet_frames.txt"

// goldenCases covers every surface the memoised renders reach: the rail's
// session, group and artifact rows, both densities, the triage queue, the
// group detail card, the header strip and the footer legend, at a rail-narrow
// width and a wide one, and at the tall sizes where a panel has more rows
// than the fleet has entries.
type goldenCase struct {
	name  string
	build func(t testing.TB) *Model
}

func goldenCases() []goldenCase {
	sized := func(n, w, h int) func(testing.TB) *Model {
		return func(t testing.TB) *Model { return fleetModel(t, n, w, h) }
	}
	return []goldenCase{
		{"n=6/120x34", sized(6, 120, 34)},
		{"n=6/120x24/focus", func(t testing.TB) *Model {
			m := fleetModel(t, 6, 120, 24)
			m.mode = modeFocus
			return m
		}},
		{"n=87/40x50", sized(87, 40, 50)},
		{"n=87/120x34", sized(87, 120, 34)},
		{"n=87/200x50", sized(87, 200, 50)},
		{"n=87/200x50/comfortable", func(t testing.TB) *Model {
			m := fleetModel(t, 87, 200, 50)
			m.comfortableRows = true
			return m
		}},
		{"n=87/200x50/triage", func(t testing.TB) *Model {
			m := fleetModel(t, 87, 200, 50)
			m.triage = true
			m.rebuildRows()
			return m
		}},
		{"n=87/200x50/cursor-on-root", func(t testing.TB) *Model {
			return fleetModel(t, 87, 200, 50).placeCursor(0)
		}},
		{"n=87/200x50/cursor-on-group", func(t testing.TB) *Model {
			m := fleetModel(t, 87, 200, 50)
			for i, row := range m.rows {
				if row.isGroup && !row.isRoot() {
					return m.placeCursor(i)
				}
			}
			return m
		}},
		{"n=87/200x50/cursor-on-artifact", func(t testing.TB) *Model {
			m := fleetModel(t, 87, 200, 50)
			for i, row := range m.rows {
				if row.isArtifact() {
					return m.placeCursor(i)
				}
			}
			return m
		}},
		{"n=87/200x50/searching", func(t testing.TB) *Model {
			m := fleetModel(t, 87, 200, 50)
			m.searching, m.search = true, "large"
			m.rebuildRows()
			return m
		}},
		{"n=87/200x50/scrolled-to-end", func(t testing.TB) *Model {
			m := fleetModel(t, 87, 200, 50)
			return m.placeCursor(len(m.rows) - 1)
		}},
		// A fleet the rail seats with rows to spare, which is where the
		// machine dock and the preview panel both used to leave a hole.
		{"n=30/200x100", sized(30, 200, 100)},
		// Taller still, and narrow with it: a fleet that overflows the rail
		// even here, so the dock lands at the foot the way it always did.
		{"n=87/120x120", sized(87, 120, 120)},
		// A phone: one panel wide, and short. 45x27 is the client with its
		// keyboard up, where the full dock still fits beside a list of twelve;
		// 45x20 folds the dock to a line and the legend to a row; 45x12 hides
		// the legend and moves the key-map hint into the header.
		{"n=87/45x27", sized(87, 45, 27)},
		{"n=87/45x20", sized(87, 45, 20)},
		{"n=87/45x12", sized(87, 45, 12)},
		{"n=87/45x12/comfortable", func(t testing.TB) *Model {
			m := fleetModel(t, 87, 45, 12)
			m.comfortableRows = true
			return m
		}},
		// A session with messages waiting wears the inbox badge, and until
		// this case the corpus had none: the badge's glyph reached the
		// operator's iPad without ever having gone through the frame's
		// font-coverage guard, and broke there.
		{"n=87/200x50/inbox-badges", func(t testing.TB) *Model {
			m := fleetModel(t, 87, 200, 50)
			m.queuedMessages = map[string]int{}
			for i, sess := range m.sessions {
				if i%3 == 0 {
					m.queuedMessages[sess.ID] = i%4 + 1
				}
			}
			m.rebuildRows()
			return m
		}},
		{"n=87/45x12/focus", func(t testing.TB) *Model {
			m := fleetModel(t, 87, 45, 12)
			m.mode = modeFocus
			return m
		}},
	}
}

func goldenBody(t testing.TB) string {
	var b strings.Builder
	for _, c := range goldenCases() {
		fmt.Fprintf(&b, "=== %s\n%s\n", c.name, c.build(t).frame())
	}
	return b.String()
}

// TestFleetFramesAreUnchanged is the guard on the render-path optimisation.
// Set GATE_INBOX_GOLDEN=write to re-record, which is only ever correct when the
// rendered output is meant to change.
func TestFleetFramesAreUnchanged(t *testing.T) {
	baseline, err := os.ReadFile(goldenFrames)
	if err != nil {
		t.Fatal(err)
	}
	expected := goldenSections(string(baseline))
	path := goldenFrames
	if runtime.GOOS == "darwin" {
		path = "testdata/fleet_frames_darwin.txt"
	}
	got := goldenBody(t)
	if envname.Get(envname.Golden) == "write" {
		if runtime.GOOS == "darwin" {
			var changed strings.Builder
			for _, frame := range strings.Split(got, "=== ")[1:] {
				name, body, _ := strings.Cut(frame, "\n")
				if body != expected[name] {
					changed.WriteString("=== " + frame)
				}
			}
			got = changed.String()
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Skip("recorded " + path)
	}
	if runtime.GOOS == "darwin" {
		overrides, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for name, frame := range goldenSections(string(overrides)) {
			expected[name] = frame
		}
	}
	for name, frame := range goldenSections(got) {
		t.Run(name, func(t *testing.T) {
			if frame != expected[name] {
				t.Fatalf("frame differs\n got: %q\nwant: %q", frame, expected[name])
			}
		})
	}
}

func goldenSections(body string) map[string]string {
	sections := make(map[string]string)
	for _, frame := range strings.Split(body, "=== ")[1:] {
		name, content, _ := strings.Cut(frame, "\n")
		sections[name] = content
	}
	return sections
}
