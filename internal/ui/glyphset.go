package ui

import (
	"github.com/usestring/gate-inbox/internal/forge"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// The board's marks are geometric shapes from one weight family, and they
// are chosen for coverage before legibility: a phone or an iPad resolves a
// glyph its font has no cell for as a `?`, or out of the colour emoji font
// at double width, which shunts the rest of the row sideways. glyphs_test.go
// is the guard that keeps the default set inside that repertoire.
//
// The cost is that the shapes carry state only to somebody who has learned
// them -- ◐ against ◔ against ○ is a scale, not a meaning -- and colour is
// what does the work, which leaves nothing for a terminal drawing none.
//
// So the emoji are an opt-in second set rather than a replacement. Anyone
// turning them on is on a terminal they have watched render them, which is
// the only way that font claim can be made at all; the shapes stay the
// default for everybody who has not.

// glyphSet is one full vocabulary of marks: session states, the queue mark,
// and the check-run states the work rows read.
type glyphSet struct {
	name          string
	working       string
	starting      string
	waiting       string
	finished      string
	errored       string
	idle          string
	muted         string
	deaf          string
	hookless      string
	checksPassing string
	checksFailing string
	checksPending string
}

// shapeGlyphs is the board as it has always drawn: one geometric mark per
// state, all from the same weight family so a column of them reads as a
// single scale rather than as a mix of punctuation.
var shapeGlyphs = glyphSet{
	name:          glyphsShapes,
	working:       "◐",
	starting:      "◌",
	waiting:       "◆",
	finished:      "●",
	errored:       "✕",
	idle:          "○",
	muted:         "⊘",
	deaf:          "⊗",
	hookless:      "⊙",
	checksPassing: "✓",
	checksFailing: "✕",
	checksPending: "◔",
}

// emojiGlyphs names each state rather than ranking it. Every mark is the
// same width as every other, so the column stays a column -- a set mixing
// widths would move every name on the board by one cell depending on what
// its agent happened to be doing.
var emojiGlyphs = glyphSet{
	name:          glyphsEmoji,
	working:       "🔄",
	starting:      "⏳",
	waiting:       "❓",
	finished:      "✅",
	errored:       "❌",
	idle:          "💤",
	muted:         "🔕",
	deaf:          "🔇",
	hookless:      "🔌",
	checksPassing: "✅",
	checksFailing: "❌",
	checksPending: "⏳",
}

const (
	glyphsSetting = "glyphs"
	glyphsShapes  = "shapes"
	glyphsEmoji   = "emoji"
)

// glyphsModes is the setting's cycle order.
var glyphsModes = []string{glyphsShapes, glyphsEmoji}

// currentGlyphs is the set in force, held package-level for the same reason
// the theme is: every row, badge and legend reads it while painting, and
// threading it through would touch every signature in the package.
var currentGlyphs = shapeGlyphs

func applyGlyphSet(name string) {
	if normalizeGlyphs(name) == glyphsEmoji {
		currentGlyphs = emojiGlyphs
		return
	}
	currentGlyphs = shapeGlyphs
}

func storedGlyphs(st *store.Store) string {
	chosen, err := st.Setting(glyphsSetting)
	if err != nil {
		return glyphsShapes
	}
	return normalizeGlyphs(chosen)
}

func normalizeGlyphs(chosen string) string {
	for _, mode := range glyphsModes {
		if chosen == mode {
			return mode
		}
	}
	return glyphsShapes
}

// statusGlyph is the mark for one session state, in whichever set is on.
// Shape or name carries the state; colour reinforces it.
func statusGlyph(s string) string {
	switch s {
	case status.Working:
		return currentGlyphs.working
	case status.Starting:
		return currentGlyphs.starting
	case status.Waiting:
		return currentGlyphs.waiting
	case status.Finished:
		return currentGlyphs.finished
	case status.Errored, status.Dead:
		return currentGlyphs.errored
	default:
		return currentGlyphs.idle
	}
}

// mutedGlyph marks a row triage has been told to walk past. It is drawn in
// the subtle tint beside the name rather than in place of the status mark:
// the state is still true, and the mark is about the operator's queue.
func mutedGlyph() string { return currentGlyphs.muted }

// deafGlyph marks a row whose pane has stopped acting on input. It is ⊘'s
// neighbour on purpose: the two marks sit in the same place on a row and mean
// adjacent things -- one that the drain was told to walk past this session,
// one that it cannot usefully stop at it.
func deafGlyph() string { return currentGlyphs.deaf }

// hooklessGlyph marks a row whose status is being read off its pane because
// nothing is writing its hook file -- the agent in the pane is not the one the
// manager launched, so the flag that wires the hooks is not on its command
// line.
//
// It completes the ⊘ ⊗ family and is drawn in the same place on the row, but
// it is the only one of the three that is not about a problem with the
// session. A hookless session is working correctly; the board is just reading
// it with worse instruments. So it takes the subtle tint a mute does rather
// than the errored tint ⊗ takes: it is there for the operator who wonders why
// a row is slower to settle, not a thing to go and fix.
func hooklessGlyph() string { return currentGlyphs.hookless }

func checksGlyph(state forge.ChecksState) string {
	switch state {
	case forge.ChecksPassing:
		return currentGlyphs.checksPassing
	case forge.ChecksFailing:
		return currentGlyphs.checksFailing
	case forge.ChecksPending:
		return currentGlyphs.checksPending
	default:
		return ""
	}
}
