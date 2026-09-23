package ui

import (
	"testing"

	"github.com/usestring/gate-inbox/internal/forge"
	"github.com/usestring/gate-inbox/internal/status"
)

// withGlyphs runs a check under one mark set and puts the package-level
// choice back, so a test cannot leave the rest of the suite drawing emoji.
func withGlyphs(t *testing.T, name string, check func()) {
	t.Helper()
	before := currentGlyphs
	t.Cleanup(func() { currentGlyphs = before })
	applyGlyphSet(name)
	check()
}

// The shapes are the default and stay the default: nothing on an existing
// board changes until somebody asks for it.
func TestShapesAreTheDefaultMarkSet(t *testing.T) {
	m := buildModel(t)
	if got := storedGlyphs(m.store); got != glyphsShapes {
		t.Errorf("a fresh store reads %q, want %q", got, glyphsShapes)
	}
	if currentGlyphs.name != glyphsShapes {
		t.Errorf("the package came up on %q, want %q", currentGlyphs.name, glyphsShapes)
	}
}

// A mark that does not tell one state from another is the complaint this
// setting answers, so both sets have to be distinct across every state.
func TestBothMarkSetsSeparateEveryState(t *testing.T) {
	states := []string{status.Working, status.Starting, status.Waiting, status.Finished, status.Errored, status.Idle}
	for _, set := range []string{glyphsShapes, glyphsEmoji} {
		withGlyphs(t, set, func() {
			seen := map[string]string{}
			for _, state := range states {
				mark := statusGlyph(state)
				if mark == "" {
					t.Errorf("%s: %s has no mark", set, state)
					continue
				}
				if other, clash := seen[mark]; clash {
					t.Errorf("%s: %s and %s both draw %q", set, other, state, mark)
				}
				seen[mark] = state
			}
		})
	}
}

// dead reads as errored in both sets, which is the one deliberate collision:
// a session that died and one that failed are the same thing to look at.
func TestDeadSharesTheErroredMark(t *testing.T) {
	for _, set := range []string{glyphsShapes, glyphsEmoji} {
		withGlyphs(t, set, func() {
			if statusGlyph(status.Dead) != statusGlyph(status.Errored) {
				t.Errorf("%s: dead and errored drew different marks", set)
			}
		})
	}
}

// Every emoji mark is the same width as every other. A set mixing widths
// would move every name on the board by a cell depending on what its agent
// happened to be doing, which is worse than any mark it could gain.
func TestEmojiMarksShareOneWidthAndAreDistinct(t *testing.T) {
	marks := emojiMarks(emojiGlyphs)
	want := cellWidth(marks[0])
	if want < 1 {
		t.Fatalf("the first emoji mark %q measures %d cells", marks[0], want)
	}
	for _, mark := range marks {
		if got := cellWidth(mark); got != want {
			t.Errorf("mark %q is %d cells wide, but the set is %d", mark, got, want)
		}
	}
}

// The exemption in glyphs_test.go is written against this set, so a mark
// added to it without a thought about coverage is caught here rather than
// silently inheriting the exemption.
func TestTheEmojiExemptionCoversExactlyTheEmojiSet(t *testing.T) {
	for _, mark := range emojiMarks(emojiGlyphs) {
		for _, r := range mark {
			if r < 0x80 || widelyDrawnRunes[r] {
				continue
			}
			if !emojiGlyphRunes[r] {
				t.Errorf("emoji mark %q draws U+%04X, which the exemption does not cover", mark, r)
			}
		}
	}
	for r := range emojiGlyphRunes {
		found := false
		for _, mark := range emojiMarks(emojiGlyphs) {
			for _, m := range mark {
				if m == r {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("the exemption covers U+%04X, which no emoji mark draws", r)
		}
	}
}

// The work rows read the same vocabulary, so a board on emoji does not draw
// a session in one alphabet and its pull requests in another.
func TestTheWorkMarksFollowTheSet(t *testing.T) {
	withGlyphs(t, glyphsEmoji, func() {
		if got := checksGlyph(forge.ChecksPassing); got != emojiGlyphs.checksPassing {
			t.Errorf("a passing check drew %q under emoji, want %q", got, emojiGlyphs.checksPassing)
		}
		if got := mutedGlyph(); got != emojiGlyphs.muted {
			t.Errorf("the muted mark drew %q under emoji, want %q", got, emojiGlyphs.muted)
		}
		if got := checksGlyph(forge.ChecksState("")); got != "" {
			t.Errorf("an unknown check state drew %q, want nothing", got)
		}
	})
}

// The rail's row measures its own marks, so the board's columns line up
// whichever set is on.
func TestARowMeasuresItsMarkUnderEitherSet(t *testing.T) {
	for _, set := range []string{glyphsShapes, glyphsEmoji} {
		withGlyphs(t, set, func() {
			m := fleetModel(t, 12, 120, 40)
			for i, line := range splitLines(m.frame()) {
				if got := cellWidth(line); got != 120 {
					t.Fatalf("%s: frame line %d is %d cells, want 120", set, i, got)
				}
			}
		})
	}
}

// The choice survives the panel and the store, takes effect as it is
// stepped, and an unknown stored value leaves the board on the shapes.
func TestGlyphSettingRoundTripsAndFailsSafe(t *testing.T) {
	m := buildModel(t)
	t.Cleanup(func() { applyGlyphSet(glyphsShapes) })
	m.openSettings()
	m.settings.field = settingsFieldGlyphs
	m.applyCmd(t, m.cycleSetting(1))
	if m.settings.glyphs != glyphsEmoji {
		t.Fatalf("one step landed on %q, want %q", m.settings.glyphs, glyphsEmoji)
	}
	if currentGlyphs.name != glyphsEmoji {
		t.Error("stepping the picker did not put the marks it names on screen")
	}
	if _, cmd := m.saveAndCloseSettings(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if got := storedGlyphs(m.store); got != glyphsEmoji {
		t.Fatalf("stored %q, want %q", got, glyphsEmoji)
	}

	if err := m.store.SetSetting(glyphsSetting, "runes"); err != nil {
		t.Fatal(err)
	}
	if got := storedGlyphs(m.store); got != glyphsShapes {
		t.Errorf("an unknown stored value read as %q, want %q", got, glyphsShapes)
	}
	applyGlyphSet("runes")
	if currentGlyphs.name != glyphsShapes {
		t.Errorf("an unknown set put %q in force, want %q", currentGlyphs.name, glyphsShapes)
	}
}
