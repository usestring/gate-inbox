package status

import (
	"os"
	"strings"
	"testing"
)

// claude draws "✔ Update installed · Restart to update" between the
// turn-end summary and the composer once a CLI update has been staged. The
// fixture is a real pane capture, not a hand-written approximation.
func updateBannerFrame(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/claude-update-banner-resting.txt")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	frame := string(raw)
	if !strings.Contains(frame, "✔ Update installed") {
		t.Fatal("fixture has lost the update banner it exists to carry")
	}
	return frame
}

// The banner is indented and starts with ✔, so it used to count as the
// region's last content line: not a turn_end marker, so the whole verdict
// collapsed to default_status with matched=false. A claude-hooks session
// whose hooks have died needs a MATCHED finished here for applyHookStatus to
// talk its stale "working" back down, and inbox delivery needs the status it
// leaves behind.
func TestUpdateBannerLeavesTheTurnFinished(t *testing.T) {
	engine := defaultEngine(t)
	frame := updateBannerFrame(t)

	got, matched := engine.Match("claude", frame)
	if got != Finished || !matched {
		t.Fatalf("resting turn under the update banner = %q matched=%v, want %q matched=true", got, matched, Finished)
	}

	// Same frame without the banner: the banner is the only difference, so a
	// regression here is a regression in the banner handling and nothing else.
	lines := strings.Split(frame, "\n")
	kept := lines[:0:0]
	for _, line := range lines {
		if !strings.Contains(line, "✔ Update installed") {
			kept = append(kept, line)
		}
	}
	bare := strings.Join(kept, "\n")
	if bare == frame {
		t.Fatal("fixture did not yield a banner-free variant")
	}
	if got, matched := engine.Match("claude", bare); got != Finished || !matched {
		t.Fatalf("same frame without the banner = %q matched=%v, want %q matched=true", got, matched, Finished)
	}
}

// The banner is chrome, not a trailing_note. trailing_note is sticky --
// settledBelow skips everything after the first match -- so parking the
// banner there would report a turn that is streaming below it as finished,
// which types into a busy pane.
func TestUpdateBannerDoesNotSwallowANewerTurn(t *testing.T) {
	engine := defaultEngine(t)
	frame := insertBelowBanner(t, updateBannerFrame(t), "● picking the next thing up")
	if got, _ := engine.Match("claude", frame); got == Finished {
		t.Fatal("output below the update banner still reads as a finished turn")
	}
}

// insertBelowBanner puts a line directly under the banner row, which is where
// a newer turn's first output lands. It fails rather than returning the frame
// unchanged, so the test above cannot quietly stop testing anything.
func insertBelowBanner(t *testing.T, frame, line string) string {
	t.Helper()
	lines := strings.Split(frame, "\n")
	for i, l := range lines {
		if !strings.Contains(l, "✔ Update installed") {
			continue
		}
		return strings.Join(append(append(append([]string{}, lines[:i+1]...), line), lines[i+1:]...), "\n")
	}
	t.Fatal("fixture has no banner row to insert below")
	return ""
}

// Only chrome_line is consulted by lastContentIndex, so only chrome_line
// keeps the banner from hiding the interrupt banner above it.
func TestUpdateBannerDoesNotMaskAnInterrupt(t *testing.T) {
	engine := defaultEngine(t)
	frame := strings.Replace(updateBannerFrame(t),
		"✻ Baked for 33s · done 4:56 AM",
		"⏺ Interrupted · What should Claude do instead?", 1)
	if got, matched := engine.Match("claude", frame); got != Waiting || !matched {
		t.Fatalf("interrupt under the update banner = %q matched=%v, want %q matched=true", got, matched, Waiting)
	}
}
